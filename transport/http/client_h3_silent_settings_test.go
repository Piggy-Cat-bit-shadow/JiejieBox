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

	"github.com/stretchr/testify/require"
)

// A peer that completes the QUIC handshake and then never sends HTTP/3 SETTINGS is the "silent
// SETTINGS" stage of the fallback matrix: the UDP dial works, the handshake works, and the CONNECT
// header must not be written because the client cannot know the peer's settings.
//
// For a plain request the transport sends the request and only gets no response (that is the
// caller's own budget), so the stage is meaningful on the tunnel path, where the CONNECT header is
// gated on SETTINGS.

func startSilentSettingsH3Server(t *testing.T) (address string, requestStreams *atomic.Int32) {
	t.Helper()
	tlsConfig := testServerTLSConfig(t)
	tlsConfig.NextProtos = []string{http3.NextProtoH3}
	packetConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	listener, err := quic.ListenEarly(packetConn, tlsConfig, &quic.Config{
		HandshakeIdleTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	requestStreams = &atomic.Int32{}
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
				for {
					stream, streamErr := conn.AcceptStream(ctx)
					if streamErr != nil {
						return
					}
					requestStreams.Add(1)
					_ = stream
				}
			}()
		}
	}()
	return packetConn.LocalAddr().String(), requestStreams
}

// TestSilentSettingsPeerNeverReceivesTheConnectHeader: the setup must end on our own window rather
// than the QUIC idle timeout, and no CONNECT stream may be opened before the SETTINGS arrive.
func TestSilentSettingsPeerNeverReceivesTheConnectHeader(t *testing.T) {
	address, requestStreams := startSilentSettingsH3Server(t)
	client := newNoExtendedConnectClient(t, address, &recordingDialer{})
	previousWindow := http3TunnelEstablishTimeout
	http3TunnelEstablishTimeout = 200 * time.Millisecond
	t.Cleanup(func() { http3TunnelEstablishTimeout = previousWindow })

	start := time.Now()
	_, _, transport, err := client.openTunnel(context.Background(), tunnelRequestForTest())
	elapsed := time.Since(start)
	require.Error(t, err)
	if errors.Is(err, ErrHTTP3Unavailable) {
		// Under load the loopback dial can lose its budget before the settings stage is reached;
		// that is a different (and legitimate) failure and not what this test pins.
		t.Skipf("dial lost its budget before the settings stage: %v", err)
	}
	require.Equal(t, TunnelTransportUnknown, transport)
	require.Less(t, elapsed, 2*time.Second,
		"the setup must be bounded by our window, not the QUIC idle timeout; measured %s", elapsed)
	require.Zero(t, requestStreams.Load(),
		"no CONNECT stream may be opened before the peer's SETTINGS arrive")
	require.False(t, errors.Is(err, errHTTP3ResponsePending),
		"nothing was sent, so this must not be reported as a possibly-sent attempt")
	require.Zero(t, client.http3Broken.Load(),
		"a silent SETTINGS wait is our own window, not a transport verdict")
}
