//go:build with_quic

package trusttunnel

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/congestion"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing-quic"
	congestion_meta1 "github.com/sagernet/sing-quic/congestion_meta1"
	congestion_meta2 "github.com/sagernet/sing-quic/congestion_meta2"
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/ntp"
)

const quicIncluded = true

// maxClientIdleTimeout is the QUIC idle timeout of the client: the official
// endpoint closes a session after client_listener_timeout_secs (600) anyway.
const maxClientIdleTimeout = 600 * time.Second

func init() {
	quicAuthenticationError = isQUICAuthenticationError
	quicTLSLayerError = isQUICTLSLayerError
	quicConnectionError = isQUICConnectionError
	quicStreamError = isQUICStreamError
}

// quicCryptoErrorBase is the first CRYPTO_ERROR code: QUIC encodes a TLS alert as 0x0100 + the alert
// (RFC 9001 §4.8), and the range ends at 0x01ff.
const (
	quicCryptoErrorBase quic.TransportErrorCode = 0x0100
	quicCryptoErrorLast quic.TransportErrorCode = 0x01ff
)

// isQUICTLSLayerError reports whether err is a certificate/name verdict in its QUIC representation.
//
// # Why this is an allow-list, not the whole CRYPTO_ERROR range
//
// QUIC encodes every TLS alert as 0x0100 + the alert (RFC 9001 §4.8), but the alerts do not all mean
// the same thing. bad_certificate, unknown_ca, certificate_expired, unrecognized_name and their
// relatives are statements about the peer's identity or its certificate chain: HTTP/2 presents the
// same certificate to the same name, so retrying there repeats the refusal. handshake_failure,
// protocol_version, no_application_protocol, internal_error and the extension alerts are not: they
// can be specific to this handshake or to HTTP/3/QUIC, and HTTP/2 may well succeed.
//
// The earlier version classified the entire 0x0100-0x01ff range as a TLS-layer verdict. That made a
// server's no_application_protocol alert suppress the HTTP/2 leg - the one case the fallback exists
// for - and made a generic handshake_failure look like a certificate refusal. The range is still
// checked first, but only the identity alerts below are promoted to a verdict.
func isQUICTLSLayerError(err error) bool {
	var transportError *quic.TransportError
	if !errors.As(err, &transportError) {
		return false
	}
	if transportError.ErrorCode < quicCryptoErrorBase || transportError.ErrorCode > quicCryptoErrorLast {
		return false
	}
	switch int(transportError.ErrorCode - quicCryptoErrorBase) {
	case 42, // bad_certificate
		43,  // unsupported_certificate
		44,  // certificate_revoked
		45,  // certificate_expired
		46,  // certificate_unknown
		48,  // unknown_ca
		111, // certificate_unobtainable
		112, // unrecognized_name
		113, // bad_certificate_status_response
		114, // bad_certificate_hash_value
		116: // certificate_required
		return true
	default:
		return false
	}
}

func isQUICAuthenticationError(err error) bool {
	var applicationError *quic.ApplicationError
	if errors.As(err, &applicationError) && applicationError.ErrorCode == errorCodeAuthenticationRequired {
		return true
	}
	var http3Error *http3.Error
	return errors.As(err, &http3Error) && http3Error.ErrorCode == errorCodeAuthenticationRequired
}

// isQUICConnectionError reports a CONNECTION-level failure: the connection is gone (remote or local
// close, stateless reset, idle/handshake timeout, version negotiation). Those are the errors that
// say the CARRIER cannot be used, as opposed to a stream or one target's answer.
//
// It deliberately does not classify http3.Error: that type covers both connection and stream
// conditions, and the classification would need the frame/stream context that this layer does not
// have. A stream error is handled by isQUICStreamError; everything else keeps the conservative
// retire-without-cordon path.
func isQUICConnectionError(err error) bool {
	var applicationError *quic.ApplicationError
	if errors.As(err, &applicationError) {
		return true
	}
	var transportError *quic.TransportError
	if errors.As(err, &transportError) {
		return true
	}
	var idleTimeout *quic.IdleTimeoutError
	if errors.As(err, &idleTimeout) {
		return true
	}
	var handshakeTimeout *quic.HandshakeTimeoutError
	if errors.As(err, &handshakeTimeout) {
		return true
	}
	var versionError *quic.VersionNegotiationError
	if errors.As(err, &versionError) {
		return true
	}
	return false
}

// isQUICStreamError reports a STREAM-level failure. The connection is still expected to carry other
// streams, so one bad CONNECT must not retire the session or arm the HTTP/3 verdict.
func isQUICStreamError(err error) bool {
	var streamError *quic.StreamError
	return errors.As(err, &streamError)
}

func validateCongestionControl(name string) error {
	_, err := congestionControlFunc(context.Background(), name)
	return err
}

func congestionControlFunc(ctx context.Context, name string) (func(conn *quic.Conn) congestion.CongestionControl, error) {
	timeFunc := ntp.TimeFuncFromContext(ctx)
	if timeFunc == nil {
		timeFunc = time.Now
	}
	switch name {
	case "", "cubic":
		return func(conn *quic.Conn) congestion.CongestionControl {
			return congestion_meta1.NewCubicSender(congestion_meta1.DefaultClock{TimeFunc: timeFunc}, conn.InitialPacketSize(), false)
		}, nil
	case "bbr":
		return func(conn *quic.Conn) congestion.CongestionControl {
			return congestion_meta2.NewBbrSenderWithProfile(conn.InitialPacketSize(), congestion_meta2.ProfileStandard)
		}, nil
	case "new_reno", "reno":
		return func(conn *quic.Conn) congestion.CongestionControl {
			return congestion_meta1.NewCubicSender(congestion_meta1.DefaultClock{TimeFunc: timeFunc}, conn.InitialPacketSize(), true)
		}, nil
	default:
		return nil, E.New("trusttunnel: unknown QUIC congestion control: ", name)
	}
}

func (c *Client) connectQUIC(ctx context.Context) (sessionConn, error) {
	congestionControl, err := congestionControlFunc(c.ctx, c.congestion)
	if err != nil {
		return nil, err
	}
	for {
		session, redo, err := c.connectQUICOnce(ctx, congestionControl)
		if redo {
			continue
		}
		return session, err
	}
}

// connectQUICOnce is one attempt to open the session; redo is the hook's
// request for another one.
func (c *Client) connectQUICOnce(ctx context.Context, congestionControl func(*quic.Conn) congestion.CongestionControl) (sessionConn, bool, error) {
	attempt := beginQUICDial(c.quicDialHook, ctx)
	udpConn, err := c.dialer.DialContext(attempt.Context(), N.NetworkUDP, c.server)
	if err != nil {
		return nil, false, E.Cause(err, "dial udp")
	}
	attempt.Dialed(udpConn)
	// No keepalive: the session may time out silently while idle and is
	// reopened by the next request. The idle window of the client is applied
	// by idleWatcher, which spares a session with open streams; the QUIC idle
	// timeout counts packets, not streams, so it stays at the server's figure
	// to keep a silent stream (an idle TCP connection) alive as long as HTTP/2
	// would.
	quicConn, err := qtls.Dial(ctx, udpConn, c.tlsConfig, &quic.Config{
		MaxIncomingStreams: -1,
		MaxIdleTimeout:     maxClientIdleTimeout,
	})
	if err != nil {
		udpConn.Close()
		return nil, attempt.Done(err), E.Cause(err, "QUIC handshake")
	}
	if negotiated := quicConn.ConnectionState().TLS.NegotiatedProtocol; negotiated != http3.NextProtoH3 {
		quicConn.CloseWithError(0, "")
		udpConn.Close()
		err = E.New("server negotiated ALPN ", strconv.Quote(negotiated), ", expected h3")
		if c.logger != nil {
			c.logger.Warn("trusttunnel: HTTP/3 server negotiated ALPN ", strconv.Quote(negotiated),
				", expected ", strconv.Quote(http3.NextProtoH3),
				"; check tls.alpn on both sides. Falling back to HTTP/2 for this network generation")
		}
		return nil, attempt.Done(err), err
	}
	attempt.Done(nil)
	quicConn.SetCongestionControl(congestionControl(quicConn))
	transport := &http3.Transport{DisableCompression: true}
	clientConn := transport.NewClientConn(quicConn)
	// quic-go does not own the socket: release it when the connection ends.
	go func() {
		<-quicConn.Context().Done()
		udpConn.Close()
	}()
	return &http3Session{
		quicConn:   quicConn,
		clientConn: clientConn,
		closeSocket: func() {
			udpConn.Close()
		},
	}, false, nil
}

type http3Session struct {
	quicConn    *quic.Conn
	clientConn  *http3.ClientConn
	closeSocket func()
}

func (s *http3Session) roundTrip(ctx context.Context, authority string, header http.Header, withBody bool) (*rawStream, error) {
	stream, err := s.clientConn.OpenRequestStream(ctx)
	if err != nil {
		return nil, qtls.WrapError(err)
	}
	abort := func() {
		stream.CancelRead(quic.StreamErrorCode(http3.ErrCodeRequestCanceled))
		stream.CancelWrite(quic.StreamErrorCode(http3.ErrCodeRequestCanceled))
	}
	// Register the abort BEFORE the header write. SendRequestHeader performs a synchronous QUIC
	// stream write: once the peer's stream or connection flow-control window is exhausted, it blocks
	// inside quic-go until a window update arrives. This Context cannot interrupt that write by
	// itself - only CancelWrite unblocks it - so registering after the call left a cancellation
	// window in which a caller's deadline could not end a blocked CONNECT and the stream's credit
	// stayed held. The callback is idempotent and safe to run concurrently with ReadResponse.
	stop := context.AfterFunc(ctx, abort)
	err = stream.SendRequestHeader(&http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Host: authority},
		Host:   authority,
		Header: header,
	})
	if err != nil {
		stop()
		abort()
		// A cancellation is a statement about this request, never a verdict about the carrier.
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
		return nil, qtls.WrapError(err)
	}
	if !withBody {
		stream.Close()
	}
	response, err := stream.ReadResponse()
	if !stop() {
		err = ctx.Err()
	}
	if err != nil {
		abort()
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
		return nil, qtls.WrapError(err)
	}
	return &rawStream{
		response: response,
		reader:   stream,
		writer:   stream,
		closeFunc: func() {
			stream.Close()
			stream.CancelRead(quic.StreamErrorCode(http3.ErrCodeNoError))
		},
		deadline:  stream,
		wrapError: qtls.WrapError,
	}, nil
}

func (s *http3Session) alive() bool {
	return s.quicConn.Context().Err() == nil
}

// close drops the socket before the connection, so that no CONNECTION_CLOSE
// leaves: an idle teardown is local, the server times the session out itself.
func (s *http3Session) close() error {
	s.closeSocket()
	return s.quicConn.CloseWithError(0, "")
}

// beginQUICDial starts an attempt on hook, or a no-op attempt without one.
func beginQUICDial(hook QUICDialHook, ctx context.Context) QUICDialAttempt {
	if hook == nil {
		return noQUICDialAttempt{ctx}
	}
	return hook.Begin(ctx)
}

type noQUICDialAttempt struct{ ctx context.Context }

func (a noQUICDialAttempt) Context() context.Context { return a.ctx }
func (noQUICDialAttempt) Dialed(net.Conn)            {}
func (noQUICDialAttempt) Done(error) bool            { return false }
