package mieru

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	boxTLS "github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/shadowtls"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// This file is the automated acceptance for the deployed combination:
//
//	client:  Mieru TCP outbound -> ShadowTLS v3 outbound
//	server:  ShadowTLS v3 inbound -> loopback TCP hop -> Mieru TCP inbound
//
// The link between the two protocols is the loopback TCP hop and nothing else: the
// ShadowTLS inbound hands its authenticated inner stream to the router, and this
// fixture's router dials the Mieru inbound's listener exactly as `route` +
// `direct` does in the deployed configuration. No Mieru injection, no virtual
// listener and no change to the local TCP forwarding is exercised here, because
// none exists.
//
// Everything runs on loopback against a temporary self-signed certificate: no
// public VPS, no real certificate, no device and no external network.

const comboMieruUser = "alice"
const comboMieruPassword = "alice-password"

// startShadowTLSHandshakeTarget starts a real TLS 1.3 server with its own
// self-signed certificate and a byte echo.
//
// It stands in for the public decoy site a ShadowTLS deployment points its
// handshake at. It is what the client actually performs its TLS handshake
// against, for an authenticated client and for a relayed unauthenticated one
// alike, so it must be a genuine TLS server rather than a stub.
func startShadowTLSHandshakeTarget(t *testing.T) uint16 {
	t.Helper()
	certificate, err := boxTLS.GenerateKeyPair(nil, nil, nil, "decoy.test")
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	tlsListener := tls.NewListener(listener, &tls.Config{
		Certificates: []tls.Certificate{*certificate},
		MinVersion:   tls.VersionTLS13,
	})
	go func() {
		for {
			conn, acceptErr := tlsListener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(time.Minute))
				buffer := make([]byte, 4096)
				for {
					n, readErr := conn.Read(buffer)
					if n > 0 {
						if _, writeErr := conn.Write(buffer[:n]); writeErr != nil {
							return
						}
					}
					if readErr != nil {
						return
					}
				}
			}()
		}
	}()
	return uint16(listener.Addr().(*net.TCPAddr).Port)
}

// loopbackForwardRouter is the "本机 TCP 转发" hop of the deployed server side:
// it dials the Mieru inbound's loopback listener and copies bytes both ways.
type loopbackForwardRouter struct {
	adapter.Router
	destination string
	handoffs    atomic.Int64
}

func (r *loopbackForwardRouter) RouteConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	r.handoffs.Add(1)
	upstream, err := net.Dial("tcp", r.destination)
	if err != nil {
		// The deployed route rule fails the same way: the handoff cannot reach
		// the Mieru inbound, so the session is closed instead of being reported
		// as working.
		_ = conn.Close()
		return
	}
	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			_ = conn.Close()
			_ = upstream.Close()
		})
	}
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		_, _ = io.Copy(upstream, conn)
		closeBoth()
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		_, _ = io.Copy(conn, upstream)
		closeBoth()
	}()
	<-done
	<-done
}

// detourOutboundManager is the smallest adapter.OutboundManager that can answer
// the single detour tag the Mieru outbound declares. The embedded interface is
// nil on purpose: a detour dialer only ever calls Outbound(tag), and implementing
// the full manager surface would add test doubles that are never invoked.
type detourOutboundManager struct {
	adapter.OutboundManager
	outbound adapter.Outbound
}

func (m *detourOutboundManager) Outbound(tag string) (adapter.Outbound, bool) {
	if m.outbound != nil && tag == m.outbound.Tag() {
		return m.outbound, true
	}
	return nil, false
}

// closedLoopbackPort returns a loopback port that is provably not listening: it is
// bound, released, and then verified to refuse a connection.
func closedLoopbackPort(t *testing.T) uint16 {
	t.Helper()
	for attempt := 0; attempt < 8; attempt++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := uint16(listener.Addr().(*net.TCPAddr).Port)
		require.NoError(t, listener.Close())
		conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if dialErr != nil {
			return port
		}
		_ = conn.Close()
	}
	t.Fatal("could not produce a refused loopback port for the handshake-target control")
	return 0
}

// startShadowTLSInboundOverLoopback starts the server half: a ShadowTLS v3 inbound
// whose handshake target is the local TLS decoy and whose post-handshake handoff is
// the supplied router.
func startShadowTLSInboundOverLoopback(t *testing.T, router adapter.Router, handshakePort uint16) uint16 {
	t.Helper()
	listenPort := freeLoopbackPortForCombo(t)
	created, err := shadowtls.NewInbound(context.Background(), router, nopLogger(), "shadowtls-in", option.ShadowTLSInboundOptions{
		ListenOptions: option.ListenOptions{
			Listen:     common.Ptr(badoption.Addr(netip.MustParseAddr("127.0.0.1"))),
			ListenPort: listenPort,
		},
		Version: 3,
		Users: []option.ShadowTLSUser{{
			Name:     comboMieruUser,
			Password: comboMieruPassword,
		}},
		Handshake: option.ShadowTLSHandshakeOptions{
			ServerOptions: option.ServerOptions{
				Server:     "127.0.0.1",
				ServerPort: handshakePort,
			},
		},
	})
	require.NoError(t, err)
	scope := adapter.NewScope(context.Background(), nopLogger())
	require.NoError(t, created.Start(adapter.StartStateStart, scope))
	t.Cleanup(func() { _ = scope.Close() })
	return listenPort
}

// startShadowTLSOutboundToLoopback starts the client's tunnel: a ShadowTLS v3
// outbound pointed at the loopback inbound, trusting only the throwaway
// certificate this test generated.
func startShadowTLSOutboundToLoopback(t *testing.T, port uint16) adapter.Outbound {
	t.Helper()
	created, err := shadowtls.NewOutbound(context.Background(), nil, nopLogger(), "shadowtls-out", option.ShadowTLSOutboundOptions{
		ServerOptions: option.ServerOptions{
			Server:     "127.0.0.1",
			ServerPort: port,
		},
		Version:  3,
		Password: comboMieruPassword,
		OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
			TLS: &option.OutboundTLSOptions{
				Enabled:    true,
				ServerName: "decoy.test",
				Insecure:   true,
				MinVersion: "1.3",
				MaxVersion: "1.3",
			},
		},
	})
	require.NoError(t, err)
	outboundInstance, ok := created.(adapter.Outbound)
	require.True(t, ok)
	return outboundInstance
}

// startMieruOutboundThroughDetour starts the Mieru client whose underlay is the
// ShadowTLS outbound, which is the client half of the deployed chain.
func startMieruOutboundThroughDetour(t *testing.T, detour adapter.Outbound, mieruPort uint16) *Outbound {
	t.Helper()
	options := outboundOptionsForPort(mieruPort, "tcp", comboMieruUser, comboMieruPassword)
	options.DialerOptions.Detour = detour.Tag()
	// The registration key is the INTERFACE type: common/dialer resolves the
	// detour through service.FromContext[adapter.OutboundManager], so passing the
	// concrete type here would register a service nothing ever reads.
	ctx := service.ContextWith[adapter.OutboundManager](context.Background(), &detourOutboundManager{outbound: detour})
	created, err := NewOutbound(ctx, nil, nopLogger(), "mieru-out", options)
	require.NoError(t, err)
	outboundInstance, ok := created.(*Outbound)
	require.True(t, ok)
	scope := adapter.NewScope(context.Background(), nopLogger())
	require.NoError(t, outboundInstance.Start(adapter.StartStateStart, scope))
	t.Cleanup(func() { _ = scope.Close() })
	return outboundInstance
}

func freeLoopbackPortForCombo(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	require.NoError(t, listener.Close())
	return port
}

// startCombinedServer starts the deployed server side in one call: Mieru TCP
// inbound on the loopback port, ShadowTLS v3 inbound in front of it, joined by the
// loopback TCP hop, plus the client's two outbounds. It returns the Mieru outbound
// to dial through and the two fixtures a test asserts on.
func startCombinedServer(t *testing.T, mieruPort uint16, handshakePort uint16) (*Outbound, *echoRouter, *loopbackForwardRouter) {
	t.Helper()
	_, mieruRouter, _ := startTestInbound(t, inboundOptionsForPort(mieruPort, "tcp"))
	forwarder := &loopbackForwardRouter{
		destination: M.SocksaddrFrom(netip.MustParseAddr("127.0.0.1"), mieruPort).String(),
	}
	shadowTLSPort := startShadowTLSInboundOverLoopback(t, forwarder, handshakePort)
	shadowTLSOutbound := startShadowTLSOutboundToLoopback(t, shadowTLSPort)
	mieruOutbound := startMieruOutboundThroughDetour(t, shadowTLSOutbound, mieruPort)
	return mieruOutbound, mieruRouter, forwarder
}

// TestShadowTLSv3CarriesMieruTCPOverLoopback is acceptance case 1: with the correct
// credentials, ShadowTLS v3 -> loopback TCP -> Mieru TCP carries business bytes in
// both directions.
func TestShadowTLSv3CarriesMieruTCPOverLoopback(t *testing.T) {
	mieruPort := freePortForBothTransports(t)
	handshakePort := startShadowTLSHandshakeTarget(t)
	mieruOutbound, mieruRouter, forwarder := startCombinedServer(t, mieruPort, handshakePort)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("192.0.2.10:443")
	conn, err := mieruOutbound.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err,
		"ShadowTLS v3 with the correct credentials must carry the Mieru TCP session")
	defer conn.Close()

	payload := []byte("ping through shadowtls v3 and mieru tcp")
	_, err = conn.Write(payload)
	require.NoError(t, err)
	require.Equal(t, payload, readExactWithTimeout(t, conn, len(payload)),
		"the business bytes must round-trip through the whole chain")

	select {
	case metadata := <-mieruRouter.metadata:
		require.Equal(t, "mieru", metadata.InboundType,
			"the session must arrive at the Mieru inbound, not be terminated early")
		require.Equal(t, "mieru-in", metadata.Inbound)
		require.Equal(t, comboMieruUser, metadata.User)
		require.Equal(t, destination, metadata.Destination)
	case <-time.After(10 * time.Second):
		t.Fatal("the Mieru inbound never routed the session")
	}
	require.Equal(t, int64(1), forwarder.handoffs.Load(),
		"exactly one session must cross the loopback TCP hop")
}

// TestShadowTLSv3MieruChainFailsCleanlyWhenTheHandshakeTargetIsRefused is
// acceptance case 2 (handshake failure): a refused handshake target must fail the
// session promptly, must never be reported as a successful connection, and must
// never deliver a session to the Mieru inbound.
func TestShadowTLSv3MieruChainFailsCleanlyWhenTheHandshakeTargetIsRefused(t *testing.T) {
	mieruPort := freePortForBothTransports(t)
	mieruOutbound, _, forwarder := startCombinedServer(t, mieruPort, closedLoopbackPort(t))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	conn, err := mieruOutbound.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.10:443"))
	elapsed := time.Since(start)
	require.Error(t, err,
		"a refused handshake target must never be reported as a successful session")
	require.Nil(t, conn)
	require.Less(t, elapsed, 10*time.Second,
		"the failure must surface on its own, not by waiting out the caller's deadline")
	require.Zero(t, forwarder.handoffs.Load(),
		"no session may reach the Mieru inbound when the ShadowTLS handshake did not complete")
}

// TestShadowTLSv3MieruChainKeepsHandingOffAcrossConcurrentSessions is acceptance
// case 3: a few concurrent sessions survive each other, closing one does not break
// the survivors, and a session dialled after that still hands off correctly.
func TestShadowTLSv3MieruChainKeepsHandingOffAcrossConcurrentSessions(t *testing.T) {
	mieruPort := freePortForBothTransports(t)
	handshakePort := startShadowTLSHandshakeTarget(t)
	mieruOutbound, _, forwarder := startCombinedServer(t, mieruPort, handshakePort)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("198.51.100.7:8443")

	const sessions = 4
	conns := make([]net.Conn, sessions)
	errs := make([]error, sessions)
	var wait sync.WaitGroup
	for i := 0; i < sessions; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			conn, err := mieruOutbound.DialContext(ctx, N.NetworkTCP, destination)
			if err != nil {
				errs[index] = fmt.Errorf("session %d: %w", index, err)
				return
			}
			conns[index] = conn
		}(i)
	}
	wait.Wait()
	for index := 0; index < sessions; index++ {
		if errs[index] != nil {
			t.Fatalf("%v", errs[index])
		}
	}
	for index := 0; index < sessions; index++ {
		defer conns[index].Close()
	}

	// Every concurrently established session must carry traffic.
	for index := 0; index < sessions; index++ {
		payload := []byte(fmt.Sprintf("session-%d", index))
		_, err := conns[index].Write(payload)
		require.NoError(t, err, "session %d must be usable while the others are open", index)
		require.Equal(t, payload, readExactWithTimeout(t, conns[index], len(payload)))
	}

	// Closing some sessions must not disturb the others.
	require.NoError(t, conns[0].Close())
	require.NoError(t, conns[1].Close())
	for index := 2; index < sessions; index++ {
		payload := []byte(fmt.Sprintf("survivor-%d", index))
		_, err := conns[index].Write(payload)
		require.NoError(t, err, "session %d must survive the close of its peers", index)
		require.Equal(t, payload, readExactWithTimeout(t, conns[index], len(payload)))
	}

	// Rebuild: a session dialled after the churn still hands off.
	rebuilt, err := mieruOutbound.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err, "the chain must still accept a new session after the others were closed")
	defer rebuilt.Close()
	payload := []byte("rebuilt-session")
	_, err = rebuilt.Write(payload)
	require.NoError(t, err)
	require.Equal(t, payload, readExactWithTimeout(t, rebuilt, len(payload)))

	// Liveness of the hop, not a connection count: Mieru's own multiplexer may
	// carry several logical connections over one underlay session, so more
	// sessions than hops is correct behaviour rather than a lost handoff. What
	// this pins is that the traffic provably crossed the loopback TCP hop at all.
	require.GreaterOrEqual(t, forwarder.handoffs.Load(), int64(1),
		"the sessions must cross the loopback TCP hop")
}
