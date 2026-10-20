//go:build with_quic

package http

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// The Capsule branch, measured separately from the DATAGRAM branch.
//
// # Why this cannot be inferred from the DATAGRAM result
//
// A DATAGRAM write is bounded because a peer that gets no acknowledgements cannot keep sending, so
// one side's idle timeout eventually fires. A capsule write blocks on the request stream's
// flow-control window instead, and the connection can stay perfectly healthy around it.
//
// # The two cases, and why the second one is the interesting one
//
// Without keepalive, the parked write also means we send nothing, so the peer stops hearing from us
// and its idle timeout closes the connection. That is the control below, and it is *not* evidence
// that a congested capsule write is always bounded: a keepalive keeps the peer alive while the write
// stays parked, and both sides keep acknowledging. The production MASQUE client configures a
// keepalive (`defaultKeepAlivePeriod` in transport/masque), so the second case is the realistic one.

// stalledCapsuleServer accepts a CONNECT-UDP request and then never reads the request body.
func stalledCapsuleServer(t *testing.T, idleTimeout, keepAlive time.Duration) string {
	t.Helper()
	tlsConfig := testServerTLSConfig(t)
	tlsConfig.NextProtos = []string{http3.NextProtoH3}
	packetConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	server := &http3.Server{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Capsule-Protocol", "?1")
			writer.WriteHeader(http.StatusOK)
			if flusher, ok := writer.(http.Flusher); ok {
				flusher.Flush()
			}
			// Deliberately never reads the request body.
			<-request.Context().Done()
		}),
		TLSConfig: tlsConfig,
		QUICConfig: &quic.Config{
			HandshakeIdleTimeout: 5 * time.Second,
			MaxIdleTimeout:       idleTimeout,
			KeepAlivePeriod:      keepAlive,
			EnableDatagrams:      false,
		},
		EnableDatagrams: false,
	}
	go func() { _ = server.Serve(packetConn) }()
	t.Cleanup(func() { _ = server.Close() })
	return packetConn.LocalAddr().String()
}

// stalledCapsuleClient builds the production client with an explicit QUIC configuration, because the
// keepalive is the variable under test and the shared helper fixes it.
func stalledCapsuleClient(t *testing.T, address string, idleTimeout, keepAlive time.Duration) *Client {
	t.Helper()
	clientTLS, err := tls.NewSTDClient(t.Context(), logger.NOP(), "example.test",
		option.OutboundTLSOptions{
			Enabled:    true,
			ServerName: "example.test",
			Insecure:   true,
			ALPN:       []string{"h3"},
		})
	require.NoError(t, err)
	impl := &http3ClientImpl{
		dialer:    &connectedUDPDialer{},
		server:    M.ParseSocksaddr(address),
		authority: address,
		tlsConfig: clientTLS,
		quicConfig: &quic.Config{
			HandshakeIdleTimeout: 5 * time.Second,
			MaxIdleTimeout:       idleTimeout,
			KeepAlivePeriod:      keepAlive,
			EnableDatagrams:      false,
		},
		transport: &http3.Transport{EnableDatagrams: false, DisableCompression: true},
	}
	impl.connectCandidate = impl.connectCandidateAt
	return &Client{
		dialer:            &connectedUDPDialer{},
		http1Dialer:       &connectedUDPDialer{},
		authorityOverride: address,
		version:           3,
		server:            M.ParseSocksaddr(address),
		http3:             impl,
		http3Authority:    address,
	}
}

// openStalledCapsuleTunnel opens a tunnel and writes until one write parks for more than a second.
//
// Each write runs in its own goroutine so a permanently blocked write cannot hang the test: the
// caller gets the channel that will receive its result, and the packet connection is closed from a
// cleanup, which is what eventually releases it.
func openStalledCapsuleTunnel(t *testing.T, client *Client) (N.PacketConn, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	conn, err := client.listenPacket(ctx, M.ParseSocksaddr("echo.test:443"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	payload := make([]byte, 1200)
	destination := M.ParseSocksaddr("echo.test:443")
	for written := 0; written < 200000; written++ {
		pkt := buf.NewSize(len(payload))
		pkt.Write(payload)
		done := make(chan error, 1)
		go func(buffer *buf.Buffer) {
			done <- conn.WritePacket(buffer, destination)
		}(pkt)
		select {
		case writeErr := <-done:
			require.NoError(t, writeErr)
		case <-time.After(time.Second):
			t.Logf("write %d parked: no return within 1s", written)
			return conn, done
		}
	}
	t.Fatal("the stream window was never filled")
	return nil, nil
}

// TestCapsuleWriteWithoutKeepAliveEndsAtThePeerIdleTimeout is the control: nothing keeps the peer
// alive while the write is parked, so its own idle timeout ends the connection.
func TestCapsuleWriteWithoutKeepAliveEndsAtThePeerIdleTimeout(t *testing.T) {
	const peerIdleTimeout = 10 * time.Second
	address := stalledCapsuleServer(t, peerIdleTimeout, 0)
	client := stalledCapsuleClient(t, address, 60*time.Second, 0)

	_, parked := openStalledCapsuleTunnel(t, client)
	start := time.Now()
	select {
	case writeErr := <-parked:
		t.Logf("MEASURED without keepalive: the parked write returned after %v with %v",
			time.Since(start).Round(time.Millisecond), writeErr)
		require.Greater(t, time.Since(start), peerIdleTimeout/2,
			"too early to be the peer's idle timeout")
	case <-time.After(3 * peerIdleTimeout):
		t.Fatal("the parked write did not return even without keepalive")
	}
}

// TestCapsuleWriteWithKeepAliveStaysParked is the counterexample the earlier round could not settle.
//
// Both sides are explicitly configured (3 s idle, no zero-value ambiguity), the client sends a
// keepalive every 200 ms, and the peer never reads the request body. If the connection stays alive
// while the write stays parked, then the capsule branch is not bounded by any idle timeout: the
// keepalive refreshes the peer, and the peer's acknowledgements refresh us.
func TestCapsuleWriteWithKeepAliveStaysParked(t *testing.T) {
	const idleTimeout = 3 * time.Second
	address := stalledCapsuleServer(t, idleTimeout, 0)
	client := stalledCapsuleClient(t, address, idleTimeout, 200*time.Millisecond)

	packetConn, parked := openStalledCapsuleTunnel(t, client)

	// Hold the state for several idle timeouts.
	time.Sleep(4 * idleTimeout)

	// Is the connection actually alive? A closed connection would fail this immediately; a live one
	// opens a second tunnel on the same physical connection.
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	probe, probeErr := client.listenPacket(probeCtx, M.ParseSocksaddr("echo.test:443"))
	probeCancel()
	connectionAlive := probeErr == nil
	if connectionAlive {
		t.Log("MEASURED: a second CONNECT-UDP request succeeded on the same connection, so the QUIC connection is ALIVE")
		_ = probe.Close()
	} else {
		t.Logf("the connection is gone: a second request failed with %v", probeErr)
	}

	select {
	case writeErr := <-parked:
		t.Logf("the parked write returned with %v", writeErr)
		require.False(t, connectionAlive,
			"the write returned while the connection is alive, so something else released it")
	case <-time.After(10 * time.Second):
		require.True(t, connectionAlive, "the connection died but the parked write did not return")
		t.Log("MEASURED: the capsule write is STILL parked 10s after it began, with the connection alive")
		t.Log("VERDICT: with a keepalive the capsule branch has no idle-timeout bound; only a cancelled context or a closed connection ends it")

		// That is the mechanism the endpoint fix relies on: the scope cancels its context before the
		// inbound cleanup runs, the endpoint closes this client there, and the parked write is
		// released. Prove the release half here.
		closeStart := time.Now()
		_ = packetConn.Close()
		select {
		case writeErr := <-parked:
			t.Logf("MEASURED: closing the tunnel released the parked write after %v with %v",
				time.Since(closeStart).Round(time.Millisecond), writeErr)
			require.Less(t, time.Since(closeStart), 5*time.Second,
				"closing the connection must release the parked write promptly")
		case <-time.After(10 * time.Second):
			t.Fatal("closing the connection did not release the parked write")
		}
	}
}
