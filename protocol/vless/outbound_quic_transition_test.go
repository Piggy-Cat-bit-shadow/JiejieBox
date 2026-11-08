//go:build with_quic

package vless

// NR01 — the V2Ray QUIC outbounds across a physical network change.
//
// The three V2Ray outbounds share ONE wiring: InterfaceUpdated calls the
// transport's Reset when it has one (and Close only as a fallback), and the
// transport is the shared QUIC client that V02A pinned at the unit level. What
// this file adds is the production path on real sockets: the outbound built by
// its real constructor over the real transport registry (transport.type=quic),
// a real QUIC handshake against a local endpoint, and payload before and after
// the transition.
//
// It lives in the vless package because it drives all three siblings through one
// harness: the option types are siblings and the QUIC endpoint, certificate and
// stream accounting would otherwise be triplicated byte for byte. The three
// outbounds' InterfaceUpdated bodies are identical by design (a shared transport
// contract, not a shared implementation file), and this test is the place that
// keeps them identical.
//
// # What a transition must NOT do
//
// Reset is not Close. Close cancels the transport's lifetime context, so a later
// dial can never succeed; that is why the network hook must not use it. The test
// therefore asserts two separate things that are easy to conflate:
//
//   - after InterfaceUpdated, the NEXT dial performs a NEW QUIC handshake (the
//     session pinned to the old network is gone) and still carries payload;
//   - after the final close (the lifecycle scope's transport cleanup), a dial
//     fails and no handshake happens at all (nothing can be resurrected).

import (
	"context"
	stdTLS "crypto/tls"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing-box/adapter"
	boxTLS "github.com/sagernet/sing-box/common/tls"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/trojan"
	"github.com/sagernet/sing-box/protocol/vmess"
	_ "github.com/sagernet/sing-box/transport/v2rayquic"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

const (
	transitionUUID       = "b831381d-6324-4d53-ad4f-8cda48b30811"
	transitionPassword   = "transition-password"
	transitionServerName = "localhost"
	// transitionSettle bounds every wait: a local QUIC handshake and a stream
	// round trip are milliseconds, so a longer wait means something is wedged.
	transitionSettle = 10 * time.Second
)

var (
	transitionCertificateOnce sync.Once
	transitionCertificatePair stdTLS.Certificate
	transitionCertificateErr  error
)

// transitionCertificate returns one throwaway certificate for the package:
// generating it per subtest would dominate the test's runtime.
func transitionCertificate(t *testing.T) stdTLS.Certificate {
	t.Helper()
	transitionCertificateOnce.Do(func() {
		keyPEM, certPEM, err := boxTLS.GenerateCertificate(nil, nil, time.Now, transitionServerName, time.Now().Add(24*time.Hour))
		if err != nil {
			transitionCertificateErr = err
			return
		}
		transitionCertificatePair, transitionCertificateErr = stdTLS.X509KeyPair(certPEM, keyPEM)
	})
	require.NoError(t, transitionCertificateErr)
	return transitionCertificatePair
}

// transitionServer is a real QUIC endpoint speaking the v2ray QUIC transport's
// framing: one QUIC connection, one raw stream per dial, no length prefix.
type transitionServer struct {
	listener *quic.Listener
	addr     M.Socksaddr
	// handshakes counts accepted QUIC connections. It is the observable that
	// separates "reused the session" from "established a new one".
	handshakes atomic.Int32
	access     sync.Mutex
	streams    [][]byte
	// connectionGone closes when the first accepted connection ends.
	connectionGone chan struct{}
	connectionOnce sync.Once
}

func startTransitionServer(t *testing.T) *transitionServer {
	t.Helper()
	listener, err := quic.ListenAddr("127.0.0.1:0", &stdTLS.Config{
		Certificates: []stdTLS.Certificate{transitionCertificate(t)},
		NextProtos:   []string{http3.NextProtoH3},
	}, &quic.Config{})
	require.NoError(t, err)
	server := &transitionServer{
		listener:       listener,
		addr:           M.SocksaddrFromNet(listener.Addr()),
		connectionGone: make(chan struct{}),
	}
	t.Cleanup(func() { _ = listener.Close() })
	go server.acceptLoop()
	return server
}

func (s *transitionServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept(context.Background())
		if err != nil {
			return
		}
		s.handshakes.Add(1)
		go func() {
			<-conn.Context().Done()
			s.connectionOnce.Do(func() { close(s.connectionGone) })
		}()
		go s.serveConnection(conn)
	}
}

func (s *transitionServer) serveConnection(conn *quic.Conn) {
	for {
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			return
		}
		s.access.Lock()
		s.streams = append(s.streams, nil)
		index := len(s.streams) - 1
		s.access.Unlock()
		go s.readStream(stream, index)
	}
}

func (s *transitionServer) readStream(stream *quic.Stream, index int) {
	buffer := make([]byte, 4096)
	for {
		read, err := stream.Read(buffer)
		if read > 0 {
			s.access.Lock()
			s.streams[index] = append(s.streams[index], buffer[:read]...)
			s.access.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (s *transitionServer) streamCount() int {
	s.access.Lock()
	defer s.access.Unlock()
	return len(s.streams)
}

func (s *transitionServer) streamBytes(index int) []byte {
	s.access.Lock()
	defer s.access.Unlock()
	if index < 0 || index >= len(s.streams) {
		return nil
	}
	return append([]byte(nil), s.streams[index]...)
}

// waitForPayload waits until the stream at index has at least `want` bytes and,
// when marker is non-empty, contains it.
func (s *transitionServer) waitForPayload(t *testing.T, index int, want int, marker string) []byte {
	t.Helper()
	var received []byte
	require.Eventually(t, func() bool {
		received = s.streamBytes(index)
		if len(received) < want {
			return false
		}
		return marker == "" || strings.Contains(string(received), marker)
	}, transitionSettle, 5*time.Millisecond,
		"the stream at index %d never carried the payload (want >=%d bytes, marker %q, have %d bytes)",
		index, want, marker, len(received))
	return received
}

// transitionCase is one V2Ray outbound under test.
type transitionCase struct {
	name string
	// plaintext says whether the protocol's uplink bytes are readable on the
	// wire. VLESS and Trojan prefix their request in clear text; VMess encrypts
	// everything after the handshake, so for it the evidence is that the bytes
	// reached the endpoint at all.
	plaintext bool
	// expectsMarker is what a plaintext protocol must show on the wire.
	expectsMarker string
	build         func(t *testing.T, logger log.ContextLogger, server M.Socksaddr) adapter.Outbound
}

func transitionOutboundOptions(server M.Socksaddr) (option.ServerOptions, option.OutboundTLSOptionsContainer, *option.V2RayTransportOptions) {
	return option.ServerOptions{
			Server:     server.AddrString(),
			ServerPort: server.Port,
		}, option.OutboundTLSOptionsContainer{
			TLS: &option.OutboundTLSOptions{
				Enabled:    true,
				ServerName: transitionServerName,
				Insecure:   true,
			},
		}, &option.V2RayTransportOptions{Type: C.V2RayTransportTypeQUIC}
}

func transitionCases() []transitionCase {
	return []transitionCase{
		{
			name:          "vless",
			plaintext:     true,
			expectsMarker: "vless-probe",
			build: func(t *testing.T, logger log.ContextLogger, server M.Socksaddr) adapter.Outbound {
				serverOptions, tlsOptions, transportOptions := transitionOutboundOptions(server)
				instance, err := NewOutbound(context.Background(), nil, logger, "vless-quic", option.VLESSOutboundOptions{
					ServerOptions:               serverOptions,
					UUID:                        transitionUUID,
					OutboundTLSOptionsContainer: tlsOptions,
					Transport:                   transportOptions,
				})
				require.NoError(t, err)
				return instance
			},
		},
		{
			name:          "vmess",
			plaintext:     false,
			expectsMarker: "",
			build: func(t *testing.T, logger log.ContextLogger, server M.Socksaddr) adapter.Outbound {
				serverOptions, tlsOptions, transportOptions := transitionOutboundOptions(server)
				instance, err := vmess.NewOutbound(context.Background(), nil, logger, "vmess-quic", option.VMessOutboundOptions{
					ServerOptions:               serverOptions,
					UUID:                        transitionUUID,
					OutboundTLSOptionsContainer: tlsOptions,
					Transport:                   transportOptions,
				})
				require.NoError(t, err)
				return instance
			},
		},
		{
			name:          "trojan",
			plaintext:     true,
			expectsMarker: "trojan-probe",
			build: func(t *testing.T, logger log.ContextLogger, server M.Socksaddr) adapter.Outbound {
				serverOptions, tlsOptions, transportOptions := transitionOutboundOptions(server)
				instance, err := trojan.NewOutbound(context.Background(), nil, logger, "trojan-quic", option.TrojanOutboundOptions{
					ServerOptions:               serverOptions,
					Password:                    transitionPassword,
					OutboundTLSOptionsContainer: tlsOptions,
					Transport:                   transportOptions,
				})
				require.NoError(t, err)
				return instance
			},
		},
	}
}

// transitionOutbound is the slice of the production outbound this test needs:
// the dialer the router uses, the network-listener hook, and the lifecycle Start
// that owns the transport's final close.
type transitionOutbound interface {
	adapter.Outbound
	InterfaceUpdated(ctx context.Context)
	Start(stage adapter.StartStage, scope *adapter.Scope) error
}

func asTransitionOutbound(t *testing.T, instance adapter.Outbound) transitionOutbound {
	t.Helper()
	target, ok := instance.(transitionOutbound)
	require.True(t, ok, "the outbound must expose InterfaceUpdated and the lifecycle Start, got %T", instance)
	return target
}

// TestNetworkTransitionKeepsTheOutboundDialable drives the whole sequence on
// real sockets for each of the three V2Ray outbounds.
func TestNetworkTransitionKeepsTheOutboundDialable(t *testing.T) {
	for _, testCase := range transitionCases() {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			logger := log.NewNOPFactory().NewLogger("transition-" + testCase.name)
			server := startTransitionServer(t)
			target := asTransitionOutbound(t, testCase.build(t, logger, server.addr))
			scope := adapter.NewScope(context.Background(), logger)
			require.NoError(t, target.Start(adapter.StartStateInitialize, scope))
			t.Cleanup(func() { _ = scope.Close() })

			payload := []byte(testCase.name + "-probe")
			marker := ""
			if testCase.plaintext {
				marker = testCase.expectsMarker
			}

			// dialAndSend performs one dial and returns the connection plus the
			// server-side stream that must carry the payload.
			dialAndSend := func() (net.Conn, int) {
				t.Helper()
				before := server.streamCount()
				ctx, cancel := context.WithTimeout(context.Background(), transitionSettle)
				defer cancel()
				conn, err := target.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("origin.test:443"))
				require.NoError(t, err, "the tunnel must be established")
				_, err = conn.Write(payload)
				require.NoError(t, err)
				require.Eventually(t, func() bool { return server.streamCount() > before }, transitionSettle, 5*time.Millisecond,
					"the dial did not open a stream on the QUIC endpoint")
				index := before
				server.waitForPayload(t, index, len(payload), marker)
				return conn, index
			}

			connBefore, _ := dialAndSend()
			require.Equal(t, int32(1), server.handshakes.Load(), "the first dial performs one QUIC handshake")

			// Control: without a transition the SAME QUIC session is reused (a new
			// stream, no new handshake). Without this the handshake assertion after
			// the transition could pass against a transport that redials every time,
			// which would make Reset unobservable.
			connReused, _ := dialAndSend()
			require.Equal(t, int32(1), server.handshakes.Load(),
				"a second dial on an unchanged network must reuse the QUIC session, not redial")

			// The production entry route's dispatch reaches this: NetworkManager
			// walks every outbound and calls InterfaceUpdated.
			target.InterfaceUpdated(context.Background())

			// The session that carried the old network must actually be gone.
			select {
			case <-server.connectionGone:
			case <-time.After(transitionSettle):
				t.Fatal("InterfaceUpdated did not close the QUIC session: the endpoint still sees it")
			}
			// ...and so must the old streams: writing on a connection pinned to the
			// old session must fail rather than silently continue.
			_ = connBefore.SetWriteDeadline(time.Now().Add(2 * time.Second))
			if _, err := connBefore.Write([]byte("stale")); err == nil {
				t.Fatal("the connection from before the transition still accepted a write: the old session was not retired")
			}
			_ = connReused.Close()

			// The next generation: a NEW handshake and payload again.
			connAfter, _ := dialAndSend()
			require.Equal(t, int32(2), server.handshakes.Load(),
				"the dial after InterfaceUpdated must establish a new QUIC session instead of reusing the "+
					"one the transition retired")
			_ = connAfter.Close()

			// The final close is final: the lifecycle scope owns the transport's
			// Close, and a dial afterwards must fail without a handshake. This is the
			// half that Reset must not perform and that the network hook must not call.
			require.NoError(t, scope.Close())
			ctx, cancel := context.WithTimeout(context.Background(), transitionSettle)
			defer cancel()
			_, err := target.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("origin.test:443"))
			require.Error(t, err, "a dial after the final close must fail instead of establishing a session")
			require.Equal(t, int32(2), server.handshakes.Load(),
				"the failed dial after the final close must not perform a handshake")
		})
	}
}

// TestPendingDialAcrossTheTransitionFailsClosed covers the other side of the same
// hook: a dial that is still in its QUIC handshake when the network changes.
//
// The "endpoint" here is a UDP socket nobody answers, which is what keeps the dial
// pending: the handshake cannot complete on its own. Two things must hold, and they
// are the two halves of Reset's contract:
//
//   - the pending creation is discarded, so its caller is released with an error
//     instead of waiting out the handshake timeout;
//   - Reset is not Close, so the client can start ANOTHER creation afterwards. A
//     client that had been closed here could not reach the handshake at all.
func TestPendingDialAcrossTheTransitionFailsClosed(t *testing.T) {
	for _, testCase := range transitionCases() {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			logger := log.NewNOPFactory().NewLogger("transition-pending-" + testCase.name)
			blackHole, err := net.ListenPacket("udp", "127.0.0.1:0")
			require.NoError(t, err)
			t.Cleanup(func() { _ = blackHole.Close() })
			blackHoleAddr := M.SocksaddrFromNet(blackHole.LocalAddr())

			target := asTransitionOutbound(t, testCase.build(t, logger, blackHoleAddr))

			type dialResult struct {
				conn net.Conn
				err  error
			}
			dialPending := func() <-chan dialResult {
				pending := make(chan dialResult, 1)
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					conn, dialErr := target.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("origin.test:443"))
					pending <- dialResult{conn: conn, err: dialErr}
				}()
				return pending
			}

			first := dialPending()
			select {
			case result := <-first:
				t.Fatalf("the dial finished against an endpoint that never answers: conn=%v err=%v", result.conn, result.err)
			case <-time.After(250 * time.Millisecond):
			}

			target.InterfaceUpdated(context.Background())

			select {
			case result := <-first:
				require.Error(t, result.err,
					"a dial pending across the transition must be released with an error, not a connection")
				require.ErrorIs(t, result.err, net.ErrClosed,
					"the pending dial must learn the creation was discarded, not a handshake timeout")
				require.Nil(t, result.conn)
			case <-time.After(5 * time.Second):
				t.Fatal("the dial pending across the transition was not released: Reset discarded the offer " +
					"but its waiter was left parked")
			}

			// Reset must not have ended the client: a second dial has to reach the
			// handshake again (it stays pending because nothing answers), rather than
			// failing immediately with a closed client.
			second := dialPending()
			select {
			case result := <-second:
				t.Fatalf("the dial after the transition failed instead of starting a new creation: %v", result.err)
			case <-time.After(250 * time.Millisecond):
			}
			// Release it the same way, so the test leaves no dial parked.
			target.InterfaceUpdated(context.Background())
			select {
			case result := <-second:
				require.Error(t, result.err)
			case <-time.After(5 * time.Second):
				t.Fatal("the second pending dial was not released")
			}
		})
	}
}
