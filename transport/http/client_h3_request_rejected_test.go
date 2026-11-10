//go:build with_quic

package http

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	E "github.com/sagernet/sing/common/exceptions"

	"github.com/stretchr/testify/require"
)

// startRequestRejectingH3Server sends SETTINGS and resets every request stream with
// H3_REQUEST_REJECTED (0x10b): the RFC 9114 signal that the request was NOT processed.
func startRequestRejectingH3Server(t *testing.T) (address string, requestStreams *atomic.Int32) {
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
				control, streamErr := conn.OpenUniStream()
				if streamErr != nil {
					return
				}
				_, _ = control.Write([]byte{0x00, 0x04, 0x02, 0x08, 0x01})
				for {
					stream, acceptStreamErr := conn.AcceptStream(ctx)
					if acceptStreamErr != nil {
						return
					}
					requestStreams.Add(1)
					stream.CancelWrite(quic.StreamErrorCode(0x10b))
				}
			}()
		}
	}()
	return packetConn.LocalAddr().String(), requestStreams
}

// TestConnectRequestRejectedByThePeerFallsBack is the A2 control: the peer EXPLICITLY refuses to
// process the CONNECT (H3_REQUEST_REJECTED). That is proof the retry cannot duplicate an effect,
// so the setup must fall back, and nothing may be charged to the transport verdict.
func TestConnectRequestRejectedByThePeerFallsBack(t *testing.T) {
	address, requestStreams := startRequestRejectingH3Server(t)
	tcp := &recordingDialer{err: E.New("tcp fallback reached")}
	client := newNoExtendedConnectClient(t, address, tcp)

	_, _, transport, err := client.openTunnel(context.Background(), tunnelRequestForTest())
	require.ErrorContains(t, err, "tcp fallback reached",
		"an explicitly unprocessed CONNECT must be retried on the fallback")
	require.Equal(t, TunnelTransportUnknown, transport)
	require.EqualValues(t, 1, requestStreams.Load(),
		"the peer saw exactly one attempt: no hidden second CONNECT")
	require.Zero(t, client.http3Broken.Load(), "a rejection is not a transport verdict")
}

// TestExplicitRejectKeepsItsVerdictWhenTheWindowExpires forces the ordering the review reproduced:
// the rejection is classified, then the setup window expires before the classification is returned.
// The outer layering must keep the rejection (fallback, no verdict), not rewrite it to a deadline.
func TestExplicitRejectKeepsItsVerdictWhenTheWindowExpires(t *testing.T) {
	freezeTunnelWindow(t, 200*time.Millisecond)
	address, requestStreams := startRequestRejectingH3Server(t)
	tcp := &recordingDialer{err: E.New("tcp fallback reached")}
	client := newNoExtendedConnectClient(t, address, tcp)
	t.Cleanup(func() { _ = client.Close() })

	hookEntered := make(chan struct{})
	release := make(chan struct{})
	testHookAfterRejectClassification = func() {
		close(hookEntered)
		<-release
	}
	t.Cleanup(func() { testHookAfterRejectClassification = nil })
	go func() {
		<-hookEntered
		// Let the 200 ms setup window expire while the classification is held.
		time.Sleep(300 * time.Millisecond)
		close(release)
	}()

	_, _, _, err := client.openTunnel(context.Background(), tunnelRequestForTest())
	require.ErrorContains(t, err, "tcp fallback reached",
		"a classified rejection must still reach the fallback after the window expires")
	require.EqualValues(t, 1, requestStreams.Load())
	require.Zero(t, client.http3Broken.Load(),
		"the setup window expiring after a peer rejection must not arm the H3 backoff")
}
