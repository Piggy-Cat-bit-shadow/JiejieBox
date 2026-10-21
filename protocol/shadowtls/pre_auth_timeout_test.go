package shadowtls

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"

	boxTLS "github.com/sagernet/sing-box/common/tls"
)

// The ShadowTLS inbound sits directly behind the public TCP front door, and
// sing-shadowtls reads the ClientHello with an unbounded io.ReadFull. These tests
// pin the bound this repository adds around that read, and - just as important -
// pin that the bound is gone before any established session uses the connection.

const testPreAuthTimeout = 150 * time.Millisecond

// newTCPPair returns a connected loopback pair: the accepted side, which the
// production code wraps, and the peer, which stands in for a remote prober.
func newTCPPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()
	peer, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	var server net.Conn
	select {
	case server = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("the loopback pair was never accepted")
	}
	t.Cleanup(func() {
		_ = server.Close()
		_ = peer.Close()
	})
	return server, peer
}

// TestFirstRecordTimeoutClosesSilentPeer is the no-data case: a peer that
// connects and sends nothing must be closed by the bound, not held forever.
func TestFirstRecordTimeoutClosesSilentPeer(t *testing.T) {
	server, peer := newTCPPair(t)
	wrapped := newFirstRecordTimeoutConn(server, testPreAuthTimeout)
	underlying, ok := wrapped.(*firstRecordTimeoutConn)
	require.True(t, ok, "a positive timeout must install the bound")
	require.True(t, underlying.preAuthDeadlineArmed())

	buffer := make([]byte, 16)
	start := time.Now()
	_, err := wrapped.Read(buffer)
	elapsed := time.Since(start)
	require.Error(t, err)
	require.Less(t, elapsed, 10*testPreAuthTimeout,
		"a silent peer must be released by the pre-authentication budget")

	var netErr net.Error
	require.ErrorAs(t, err, &netErr, "the bound must surface as a net.Error")
	require.True(t, netErr.Timeout(), "and specifically as a timeout, so the shared classifier downgrades it")
	_ = peer
}

// TestFirstRecordTimeoutClosesPartialRecord is the case the first-byte
// formulation would miss: the peer announces a record body and then stalls
// part-way through it.
func TestFirstRecordTimeoutClosesPartialRecord(t *testing.T) {
	server, peer := newTCPPair(t)
	wrapped := newFirstRecordTimeoutConn(server, testPreAuthTimeout)

	record := make([]byte, tlsRecordHeaderSize+10)
	record[0] = 0x16 // handshake
	record[1] = 0x03
	record[2] = 0x01
	binary.BigEndian.PutUint16(record[3:], 4096)
	_, err := peer.Write(record)
	require.NoError(t, err)

	buffer := make([]byte, 4096)
	start := time.Now()
	_, err = io.ReadFull(wrapped, buffer)
	elapsed := time.Since(start)
	require.Error(t, err)
	require.Less(t, elapsed, 10*testPreAuthTimeout,
		"a truncated record must be released by the pre-authentication budget")
}

// TestFirstRecordTimeoutDisarmsAtTheRecordBoundary proves the bound stops
// applying exactly when the first record has been delivered, which is what keeps
// established sessions and the fallback relay out of its reach.
func TestFirstRecordTimeoutDisarmsAtTheRecordBoundary(t *testing.T) {
	server, peer := newTCPPair(t)
	wrapped := newFirstRecordTimeoutConn(server, testPreAuthTimeout)
	underlying := wrapped.(*firstRecordTimeoutConn)

	record := make([]byte, tlsRecordHeaderSize+11)
	record[0] = 0x16
	record[1] = 0x03
	record[2] = 0x01
	binary.BigEndian.PutUint16(record[3:], 11)
	_, err := peer.Write(record)
	require.NoError(t, err)

	_, err = io.ReadFull(wrapped, record)
	require.NoError(t, err)
	require.False(t, underlying.preAuthDeadlineArmed(),
		"the deadline must be cleared once the first record is complete")

	// Functional proof, not just the flag: data that arrives long after the
	// budget has expired must still be readable.
	time.Sleep(3 * testPreAuthTimeout)
	_, err = peer.Write([]byte("x"))
	require.NoError(t, err)
	late := make([]byte, 1)
	_, err = io.ReadFull(wrapped, late)
	require.NoError(t, err)
	require.Equal(t, []byte("x"), late)
}

// TestFirstRecordTimeoutDisabledWhenNonPositive keeps the wrapper opt-out clean.
func TestFirstRecordTimeoutDisabledWhenNonPositive(t *testing.T) {
	server, _ := newTCPPair(t)
	require.Same(t, server, newFirstRecordTimeoutConn(server, 0))
	require.Same(t, server, newFirstRecordTimeoutConn(server, -time.Second))
}

// preAuthTestRouter is the router half the fake inbound needs. The embedded
// adapter.Router is nil on purpose: none of these tests route a connection, and
// implementing the full surface would add test doubles that are never invoked.
type preAuthTestRouter struct {
	adapter.Router
	handoff chan net.Conn
}

func (r *preAuthTestRouter) RouteConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	if r.handoff != nil {
		select {
		case r.handoff <- conn:
		default:
		}
	}
}

// countingAcceptor is a TCP listener that counts accepted connections. It stands
// in for the configured handshake target so a test can prove whether the inbound
// ever dialled it.
type countingAcceptor struct {
	listener net.Listener
	accepted atomic.Int64
}

func newCountingAcceptor(t *testing.T) *countingAcceptor {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	acceptor := &countingAcceptor{listener: listener}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			acceptor.accepted.Add(1)
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return acceptor
}

func (a *countingAcceptor) port() uint16 {
	return uint16(a.listener.Addr().(*net.TCPAddr).Port)
}

func freeLoopbackPort(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	require.NoError(t, listener.Close())
	return port
}

// newPreAuthInbound constructs a real ShadowTLS v3 inbound on loopback whose
// handshake target is the supplied port, with the production pre-authentication
// budget replaced by testPreAuthTimeout so an assertion does not have to wait out
// C.TCPTimeout.
func newPreAuthInbound(t *testing.T, listenPort uint16, handshakePort uint16, router adapter.Router) *Inbound {
	t.Helper()
	if router == nil {
		router = &preAuthTestRouter{}
	}
	created, err := NewInbound(context.Background(), router, log.NewNOPFactory().Logger(), "shadowtls-in", option.ShadowTLSInboundOptions{
		ListenOptions: option.ListenOptions{
			Listen:     common.Ptr(badoption.Addr(netip.MustParseAddr("127.0.0.1"))),
			ListenPort: listenPort,
		},
		Version: 3,
		Users:   []option.ShadowTLSUser{{Name: "alice", Password: "alice-password"}},
		Handshake: option.ShadowTLSHandshakeOptions{
			ServerOptions: option.ServerOptions{
				Server:     "127.0.0.1",
				ServerPort: handshakePort,
			},
		},
	})
	require.NoError(t, err)
	inboundInstance, ok := created.(*Inbound)
	require.True(t, ok)
	// Shrink the budget before any connection can be accepted; nothing else
	// reads the field, so this needs no synchronisation.
	inboundInstance.preAuthTimeout = testPreAuthTimeout
	return inboundInstance
}

// startPreAuthInbound starts the inbound from newPreAuthInbound on a free port and
// returns the port it listens on.
func startPreAuthInbound(t *testing.T, handshakePort uint16, router adapter.Router) (uint16, *Inbound) {
	t.Helper()
	listenPort := freeLoopbackPort(t)
	inboundInstance := newPreAuthInbound(t, listenPort, handshakePort, router)
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	require.NoError(t, inboundInstance.Start(adapter.StartStateStart, scope))
	t.Cleanup(func() { _ = scope.Close() })
	return listenPort, inboundInstance
}

// TestShadowTLSInboundSilentPeerIsBounded is the end-to-end form of the defect:
// a real inbound, a real accepted socket, a peer that sends nothing. The server
// must release the connection, and it must do so without ever dialling the
// configured handshake target - the bound is about the public peer, not about
// the target.
func TestShadowTLSInboundSilentPeerIsBounded(t *testing.T) {
	acceptor := newCountingAcceptor(t)
	listenPort, _ := startPreAuthInbound(t, acceptor.port(), nil)

	conn, err := net.Dial("tcp", M.SocksaddrFrom(netip.MustParseAddr("127.0.0.1"), listenPort).String())
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
	buffer := make([]byte, 16)
	start := time.Now()
	_, readErr := conn.Read(buffer)
	elapsed := time.Since(start)
	require.Error(t, readErr,
		"a silent peer must see the server close the connection within the pre-authentication budget")
	require.Less(t, elapsed, 20*testPreAuthTimeout,
		"the close must come from the budget, not from the client-side deadline")
	require.Zero(t, acceptor.accepted.Load(),
		"a peer that never delivers a ClientHello must never cause a dial to the handshake target")
}

// TestShadowTLSInboundPartialClientHelloIsBounded is the truncated-record case
// against the real service: the header is accepted, the body never arrives.
func TestShadowTLSInboundPartialClientHelloIsBounded(t *testing.T) {
	acceptor := newCountingAcceptor(t)
	listenPort, _ := startPreAuthInbound(t, acceptor.port(), nil)

	conn, err := net.Dial("tcp", M.SocksaddrFrom(netip.MustParseAddr("127.0.0.1"), listenPort).String())
	require.NoError(t, err)
	defer conn.Close()

	partial := []byte{0x16, 0x03, 0x01, 0x10, 0x00}
	partial = append(partial, make([]byte, 32)...)
	_, err = conn.Write(partial)
	require.NoError(t, err)

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
	buffer := make([]byte, 16)
	start := time.Now()
	_, readErr := conn.Read(buffer)
	elapsed := time.Since(start)
	require.Error(t, readErr,
		"a peer that stalls inside its announced record must be released by the budget")
	require.Less(t, elapsed, 20*testPreAuthTimeout,
		"the close must come from the budget, not from the client-side deadline")
	require.Zero(t, acceptor.accepted.Load(),
		"an incomplete ClientHello must never reach the handshake target")
}

// startLocalTLSDecoy starts a real TLS server with its own self-signed
// certificate which echoes whatever it receives. It stands in for the configured
// handshake target of the fallback path.
func startLocalTLSDecoy(t *testing.T) uint16 {
	t.Helper()
	certificate, err := boxTLS.GenerateKeyPair(nil, nil, nil, "decoy.test")
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	tlsListener := tls.NewListener(listener, &tls.Config{
		Certificates: []tls.Certificate{*certificate},
	})
	go func() {
		for {
			conn, acceptErr := tlsListener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
				buffer := make([]byte, 1024)
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

// TestShadowTLSInboundFallbackRelayOutlivesThePreAuthBudget is the safety half of
// the fix at the integration level: a client that is NOT a ShadowTLS client is
// relayed to the real handshake target, and that relayed session must not be
// touched by the pre-authentication budget once the ClientHello has been read.
//
// Without the "clear at the record boundary" rule this test fails: the relayed
// TLS session is killed by the budget a few hundred milliseconds into its life.
func TestShadowTLSInboundFallbackRelayOutlivesThePreAuthBudget(t *testing.T) {
	decoyPort := startLocalTLSDecoy(t)
	listenPort, _ := startPreAuthInbound(t, decoyPort, nil)

	rawConn, err := net.Dial("tcp", M.SocksaddrFrom(netip.MustParseAddr("127.0.0.1"), listenPort).String())
	require.NoError(t, err)
	defer rawConn.Close()

	client := tls.Client(rawConn, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "decoy.test",
		MinVersion:         tls.VersionTLS13,
	})
	require.NoError(t, client.Handshake(),
		"an unauthenticated ClientHello must be relayed to the handshake target and complete a real TLS handshake")

	// Idle for several budgets: the whole point is that the deadline is already
	// gone, so silence must not be punished.
	time.Sleep(3 * testPreAuthTimeout)

	require.NoError(t, client.SetDeadline(time.Now().Add(10*time.Second)))
	payload := []byte("relayed-session-still-alive")
	_, err = client.Write(payload)
	require.NoError(t, err)
	echoed := make([]byte, len(payload))
	_, err = io.ReadFull(client, echoed)
	require.NoError(t, err, "the relayed session must still be usable after the budget would have expired")
	require.Equal(t, payload, echoed)
}

// echoTestRouter is the destination side of the authenticated round trip: it
// echoes whatever the inner session writes, so the client sees its own bytes come
// back through the real ShadowTLS data path.
type echoTestRouter struct {
	adapter.Router
	metadata chan adapter.InboundContext
}

func (r *echoTestRouter) RouteConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	if r.metadata != nil {
		select {
		case r.metadata <- metadata:
		default:
		}
	}
	defer conn.Close()
	buffer := make([]byte, 4096)
	for {
		n, err := conn.Read(buffer)
		if n > 0 {
			if _, writeErr := conn.Write(buffer[:n]); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// TestShadowTLSv3AuthenticatedSessionOutlivesThePreAuthBudget is the safety half
// for the path that matters most: an AUTHENTICATED v3 session, established end to
// end through a real inbound and outbound, must be completely unaffected by the
// pre-authentication budget once it has been handed to the router.
//
// It also pins that ShadowTLS v3 works against a local TLS 1.3 handshake target
// with no public network and no external certificate, which is the fixture the
// combined ShadowTLS + Mieru acceptance is built on.
func TestShadowTLSv3AuthenticatedSessionOutlivesThePreAuthBudget(t *testing.T) {
	decoyPort := startLocalTLSDecoy(t)
	router := &echoTestRouter{metadata: make(chan adapter.InboundContext, 4)}
	listenPort, _ := startPreAuthInbound(t, decoyPort, router)

	created, err := NewOutbound(context.Background(), nil, log.NewNOPFactory().Logger(), "shadowtls-out", option.ShadowTLSOutboundOptions{
		ServerOptions: option.ServerOptions{
			Server:     "127.0.0.1",
			ServerPort: listenPort,
		},
		Version:  3,
		Password: "alice-password",
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
	outboundInstance, ok := created.(*Outbound)
	require.True(t, ok)
	// The ShadowTLS outbound owns no lifecycle state of its own: it satisfies
	// adapter.Outbound and dials, which is all this test needs.

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.1:443"))
	require.NoError(t, err,
		"a ShadowTLS v3 handshake against a local TLS 1.3 target must complete")
	defer conn.Close()

	first := []byte("first-through-shadowtls-v3")
	_, err = conn.Write(first)
	require.NoError(t, err)
	require.Equal(t, first, readExactly(t, conn, len(first)))

	select {
	case metadata := <-router.metadata:
		require.Equal(t, "shadowtls", metadata.InboundType)
		require.Equal(t, "shadowtls-in", metadata.Inbound)
	case <-time.After(10 * time.Second):
		t.Fatal("the authenticated session was never handed to the router")
	}

	// The session must outlive the budget by a wide margin.
	time.Sleep(3 * testPreAuthTimeout)

	second := []byte("after-the-budget")
	_, err = conn.Write(second)
	require.NoError(t, err)
	require.Equal(t, second, readExactly(t, conn, len(second)),
		"an established session must never be killed by the pre-authentication budget")
}

// readExactly reads size bytes, failing the test rather than blocking forever.
func readExactly(t *testing.T, conn net.Conn, size int) []byte {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(15*time.Second)))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	received := make([]byte, size)
	_, err := io.ReadFull(conn, received)
	require.NoError(t, err)
	return received
}
