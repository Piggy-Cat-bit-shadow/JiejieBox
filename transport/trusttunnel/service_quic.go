//go:build with_quic

package trusttunnel

import (
	"context"
	"net"
	"strconv"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing-quic"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	aTLS "github.com/sagernet/sing/common/tls"
)

// ServeQUIC serves HTTP/3 on the packet conn until it fails or the service is
// closed. The caller keeps ownership of conn.
func (s *Service) ServeQUIC(conn net.PacketConn, tlsConfig aTLS.ServerConfig) error {
	if tlsConfig == nil {
		return E.New("trusttunnel: TLS is required")
	}
	congestionControl, err := congestionControlFunc(s.ctx, s.congestion)
	if err != nil {
		return err
	}
	err = qtls.ConfigureHTTP3(tlsConfig)
	if err != nil {
		return err
	}
	if !common.Contains(tlsConfig.NextProtos(), http3.NextProtoH3) {
		tlsConfig.SetNextProtos(append(append([]string{}, tlsConfig.NextProtos()...), http3.NextProtoH3))
	}
	listener, err := qtls.ListenEarly(conn, tlsConfig, &quic.Config{
		MaxIncomingStreams: serverMaxIncomingStreams,
		Allow0RTT:          false,
		MaxIdleTimeout:     serverIdleTimeout,
		DisablePathManager: true,
	})
	if err != nil {
		return err
	}
	server := &http3.Server{
		Handler:     s,
		IdleTimeout: serverIdleTimeout,
		ConnContext: func(ctx context.Context, conn *quic.Conn) context.Context {
			conn.SetCongestionControl(congestionControl(conn))
			if negotiated := conn.ConnectionState().TLS.NegotiatedProtocol; negotiated != http3.NextProtoH3 {
				// This is the failure that used to be completely silent on the server: the
				// client offered a shared ALPN list, QUIC picked h2, and the only trace was a
				// client-side fallback. One line per connection is cheap and names the shape.
				s.logger.WarnContext(ctx, "trusttunnel: HTTP/3 connection negotiated ALPN ",
					strconv.Quote(negotiated), ", expected ", strconv.Quote(http3.NextProtoH3),
					"; clients that require h3 cannot use this listener")
			}
			return ctx
		},
	}
	closer := &http3Closer{server: server, listener: listener}
	if !s.track(closer) {
		closer.Close()
		return net.ErrClosed
	}
	defer s.untrack(closer)
	err = server.ServeListener(listener)
	if s.isClosed() {
		return net.ErrClosed
	}
	return err
}

type http3Closer struct {
	server   *http3.Server
	listener qtls.EarlyListener
}

func (c *http3Closer) Close() error {
	return common.Close(c.server, c.listener)
}
