//go:build with_quic

package http

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	E "github.com/sagernet/sing/common/exceptions"

	"github.com/stretchr/testify/require"
)

// Stage injection for the tunnel setup: each test provides a peer that gets the setup past an
// earlier stage and then stops, so the fallback decision is observed at the stage it belongs to.
//
// The stages are ordered: UDP dial -> QUIC handshake -> SETTINGS -> stream open -> CONNECT header.
// A failure before the header is provably safe to retry on another version; a failure after it is
// not.

// TestH3HandshakeSilenceFallsBackBeforeSending is the handshake stage: the UDP socket accepts
// datagrams and never answers, so the QUIC handshake cannot complete and the peer cannot have
// processed anything. The setup must fall back.
func TestH3HandshakeSilenceFallsBackBeforeSending(t *testing.T) {
	packetConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = packetConn.Close() })

	tcp := &recordingDialer{err: E.New("tcp fallback reached")}
	client := newNoExtendedConnectClient(t, packetConn.LocalAddr().String(), tcp)
	previousWindow := http3TunnelEstablishTimeout
	http3TunnelEstablishTimeout = 200 * time.Millisecond
	t.Cleanup(func() { http3TunnelEstablishTimeout = previousWindow })

	start := time.Now()
	_, _, transport, err := client.openTunnel(context.Background(), tunnelRequestForTest())
	elapsed := time.Since(start)
	require.Error(t, err)
	require.Equal(t, TunnelTransportUnknown, transport)
	require.Less(t, elapsed, 2*time.Second,
		"a silent handshake must be bounded by our window; measured %s", elapsed)
	require.NotZero(t, tcp.calls,
		"a handshake that never completed cannot have processed the CONNECT; the fallback must run")
	require.False(t, errors.Is(err, errHTTP3ResponsePending),
		"nothing could reach a peer that never completed the handshake")
}

// startSettingsOnlyH3Server completes the handshake and sends SETTINGS, but grants no bidirectional
// stream credit (`MaxIncomingStreams: 0`), so the client's OpenStreamSync blocks and the CONNECT
// header can never be written.
func startSettingsOnlyH3Server(t *testing.T) (address string, acceptedStreams *atomic.Int32) {
	t.Helper()
	tlsConfig := testServerTLSConfig(t)
	tlsConfig.NextProtos = []string{http3.NextProtoH3}
	packetConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	listener, err := quic.ListenEarly(packetConn, tlsConfig, &quic.Config{
		MaxIncomingStreams:   -1,
		HandshakeIdleTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	acceptedStreams = &atomic.Int32{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		_ = packetConn.Close()
	})
	go func() {
		for {
			conn, acceptErr := listener.Accept(ctx)
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.CloseWithError(0, "")
				// A control stream with an empty SETTINGS frame is enough for the client to get
				// past its settings wait.
				control, streamErr := conn.OpenUniStream()
				if streamErr != nil {
					return
				}
				_, _ = control.Write([]byte{0x00, 0x04, 0x02, 0x08, 0x01}) // control stream + SETTINGS{ENABLE_CONNECT_PROTOCOL=1}
				for {
					stream, acceptStreamErr := conn.AcceptStream(ctx)
					if acceptStreamErr != nil {
						return
					}
					acceptedStreams.Add(1)
					_ = stream
				}
			}()
		}
	}()
	return packetConn.LocalAddr().String(), acceptedStreams
}

// TestH3StreamCreditExhaustionIsBoundedAndNeverSendsTheHeader is the stream-open stage: SETTINGS
// arrived, but no stream can be opened. Nothing is written, so the setup must be bounded by our
// window and fall back.
func TestH3StreamCreditExhaustionIsBoundedAndNeverSendsTheHeader(t *testing.T) {
	address, acceptedStreams := startSettingsOnlyH3Server(t)
	tcp := &recordingDialer{err: E.New("tcp fallback reached")}
	client := newNoExtendedConnectClient(t, address, tcp)
	previousWindow := http3TunnelEstablishTimeout
	http3TunnelEstablishTimeout = 200 * time.Millisecond
	t.Cleanup(func() { http3TunnelEstablishTimeout = previousWindow })

	start := time.Now()
	_, _, transport, err := client.openTunnel(context.Background(), tunnelRequestForTest())
	elapsed := time.Since(start)
	require.Error(t, err)
	require.Equal(t, TunnelTransportUnknown, transport)
	require.Less(t, elapsed, 2*time.Second,
		"a stream-credit wait must be bounded by our window; measured %s", elapsed)
	require.Zero(t, acceptedStreams.Load(),
		"no request stream can exist while the peer grants no stream credit")
	require.NotZero(t, tcp.calls, "nothing was written, so the fallback must run")
	require.False(t, errors.Is(err, errHTTP3ResponsePending))
}

// startRejectingH3Server sends SETTINGS and then closes the connection as soon as the client opens
// its request stream: the CONNECT header write races the close, which is the write-failure shape.
func startRejectingH3Server(t *testing.T) (address string) {
	t.Helper()
	tlsConfig := testServerTLSConfig(t)
	tlsConfig.NextProtos = []string{http3.NextProtoH3}
	packetConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	listener, err := quic.ListenEarly(packetConn, tlsConfig, &quic.Config{
		HandshakeIdleTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		_ = packetConn.Close()
	})
	go func() {
		for {
			conn, acceptErr := listener.Accept(ctx)
			if acceptErr != nil {
				return
			}
			go func() {
				control, streamErr := conn.OpenUniStream()
				if streamErr != nil {
					_ = conn.CloseWithError(0, "")
					return
				}
				_, _ = control.Write([]byte{0x00, 0x04, 0x02, 0x08, 0x01}) // control stream + SETTINGS{ENABLE_CONNECT_PROTOCOL=1}
				stream, acceptStreamErr := conn.AcceptStream(ctx)
				if acceptStreamErr != nil {
					_ = conn.CloseWithError(0, "")
					return
				}
				_ = stream
				// Give the client time to write the CONNECT header and start waiting for the
				// response, then kill the connection under it: the header IS in flight, so a
				// second tunnel would be a guess. 150 ms is far more than a loopback write.
				time.Sleep(150 * time.Millisecond)
				_ = conn.CloseWithError(0x100, "write failure injection")
			}()
		}
	}()
	return packetConn.LocalAddr().String()
}

// TestH3ConnectWriteFailureDoesNotFallBack is the write-failure stage (B7): the connection dies
// while the CONNECT header may be in flight, so a second tunnel over TCP/HTTP2 would be a guess.
// The setup must fail instead of falling back.
func TestH3ConnectWriteFailureDoesNotFallBack(t *testing.T) {
	address := startRejectingH3Server(t)
	tcp := &recordingDialer{err: E.New("tcp fallback reached")}
	client := newNoExtendedConnectClient(t, address, tcp)

	_, _, transport, err := client.openTunnel(context.Background(), tunnelRequestForTest())
	require.Error(t, err)
	require.Equal(t, TunnelTransportUnknown, transport)
	require.Zero(t, tcp.calls,
		"a CONNECT that may have been written must not be repeated on another version")
}
