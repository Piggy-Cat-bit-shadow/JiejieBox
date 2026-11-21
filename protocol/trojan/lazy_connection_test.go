package trojan

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	transportTrojan "github.com/sagernet/sing-box/transport/trojan"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// Real-connection fixture for the deferred Trojan prototype.
//
// # Why a real server, and not the fake builder
//
// The lifecycle tests prove the state machine; they deliberately do not prove that a node built on
// the FIRST DIAL is the node the eager path would have built. That claim needs bytes: a TLS
// handshake with the recipe's SNI/ALPN, a Trojan header carrying the recipe's password, and a
// bidirectional payload. The server below checks all three from the wire, so a recipe that lost a
// field fails here rather than in production.
//
// The server is a minimal Trojan echo endpoint on a generated self-signed certificate. It is not an
// interoperability claim about any particular Trojan implementation; it is the same codec the
// outbound writes and a byte-accurate assertion on what arrived.

type trojanEchoServer struct {
	listener net.Listener

	accepted  atomic.Int32
	keyOK     atomic.Bool
	alpnOK    atomic.Bool
	closeOnce sync.Once
}

func newTestCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "cold.example"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"cold.example", "localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	derivered, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derivered})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	require.NoError(t, err)
	return certificate
}

func startTrojanEchoServer(t *testing.T, password string, alpn string) *trojanEchoServer {
	t.Helper()
	certificate := newTestCertificate(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{certificate},
		NextProtos:   []string{alpn},
		MinVersion:   tls.VersionTLS12,
	})
	require.NoError(t, err)
	server := &trojanEchoServer{listener: listener}
	go server.serve(password, alpn)
	t.Cleanup(func() { server.close() })
	return server
}

func (s *trojanEchoServer) close() {
	s.closeOnce.Do(func() { _ = s.listener.Close() })
}

func (s *trojanEchoServer) addr() M.Socksaddr {
	return M.ParseSocksaddr(s.listener.Addr().String())
}

func (s *trojanEchoServer) serve(password string, alpn string) {
	expectedKey := transportTrojan.Key(password)
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			s.accepted.Add(1)
			tlsConn, isTLS := conn.(*tls.Conn)
			if !isTLS {
				return
			}
			header := make([]byte, transportTrojan.KeyLength)
			if _, err = io.ReadFull(tlsConn, header); err != nil {
				return
			}
			if bytes.Equal(header, expectedKey[:]) {
				s.keyOK.Store(true)
			}
			var crlf [2]byte
			if _, err = io.ReadFull(tlsConn, crlf[:]); err != nil {
				return
			}
			var command [1]byte
			if _, err = io.ReadFull(tlsConn, command[:]); err != nil {
				return
			}
			if _, err = M.SocksaddrSerializer.ReadAddrPort(tlsConn); err != nil {
				return
			}
			if _, err = io.ReadFull(tlsConn, crlf[:]); err != nil {
				return
			}
			if tlsConn.ConnectionState().NegotiatedProtocol == alpn {
				s.alpnOK.Store(true)
			}
			_, _ = io.Copy(tlsConn, tlsConn)
		}()
	}
}

// lazyTrojanFixture builds a deferred node for the echo server and starts it through a Scope.
func lazyTrojanFixture(t *testing.T, server *trojanEchoServer, password string, alpn []string) (*lazyTrojanOutbound, *adapter.Scope) {
	t.Helper()
	serverAddr := server.addr()
	options := &option.TrojanOutboundOptions{
		ServerOptions: option.ServerOptions{Server: serverAddr.AddrString(), ServerPort: serverAddr.Port},
		Password:      password,
		OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{
			Enabled:    true,
			Insecure:   true,
			ServerName: "cold.example",
			ALPN:       alpn,
		}},
	}
	created, handled, err := deferredTrojanOutbound(context.Background(), nil, log.NewNOPFactory().NewLogger("deferred"), "cold-node", C.TypeTrojan, options, "")
	require.NoError(t, err)
	require.True(t, handled)
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = scope.Close() })
	lifecycle, isLifecycle := created.(adapter.Lifecycle)
	require.True(t, isLifecycle)
	require.NoError(t, lifecycle.Start(adapter.StartStateInitialize, scope))
	return created.(*lazyTrojanOutbound), scope
}

// TestLazyTrojanRealConnectionDeliversBothDirections is the business fixture: a node expanded on the
// first dial produces the same wire behaviour, including the half-close direction.
func TestLazyTrojanRealConnectionDeliversBothDirections(t *testing.T) {
	const password = "cold-node-password"
	server := startTrojanEchoServer(t, password, "h2")
	deferred, _ := lazyTrojanFixture(t, server, password, []string{"h2"})
	require.Equal(t, 0, deferred.Builds(), "the fixture must not have expanded the node")

	conn, err := deferred.DialContext(context.Background(), "tcp", M.Socksaddr{Fqdn: "destination.example", Port: 80})
	require.NoError(t, err, "the first dial must build the recipe and connect")
	defer conn.Close()
	require.Equal(t, 1, deferred.Builds(), "exactly one expansion for the first dial")

	payload := []byte("hello from a cold node")
	_, err = conn.Write(payload)
	require.NoError(t, err)
	echoed := make([]byte, len(payload))
	_, err = io.ReadFull(conn, echoed)
	require.NoError(t, err)
	require.Equal(t, payload, echoed)

	// Half-close: the client stops writing, the server sees EOF and closes, and the read side
	// observes the end of the stream. A node that lost its context or its connection bookkeeping
	// during the expansion would stall here.
	require.NoError(t, N.CloseWrite(conn))
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.EOF)

	require.True(t, server.keyOK.Load(), "the server must have received the recipe's Trojan key")
	require.True(t, server.alpnOK.Load(), "the server must have negotiated the recipe's ALPN")
}

// TestLazyTrojanActiveConnectionSurvivesEvictionAndClose is the object-layer version of "an active
// connection keeps a valid owner": eviction and Close release what the NODE owns, and an
// established connection is owned by its caller. For the eligible Trojan shape the expansion owns no
// pooled object, so neither action can cut a live stream - which is exactly why this shape is safe
// to evict and why it is the prototype.
func TestLazyTrojanActiveConnectionSurvivesEvictionAndClose(t *testing.T) {
	const password = "cold-node-password"
	server := startTrojanEchoServer(t, password, "http/1.1")
	deferred, _ := lazyTrojanFixture(t, server, password, []string{"http/1.1"})

	conn, err := deferred.DialContext(context.Background(), "tcp", M.Socksaddr{Fqdn: "destination.example", Port: 443})
	require.NoError(t, err)
	defer conn.Close()

	echoOnce := func(payload string) {
		t.Helper()
		_, writeErr := conn.Write([]byte(payload))
		require.NoError(t, writeErr)
		echoed := make([]byte, len(payload))
		_, readErr := io.ReadFull(conn, echoed)
		require.NoError(t, readErr)
		require.Equal(t, payload, string(echoed))
	}
	echoOnce("before-eviction")

	require.True(t, deferred.Evict(), "no dial is in flight, so the idle expansion is evictable")
	require.Equal(t, 1, deferred.Builds(), "eviction does not rebuild")
	echoOnce("after-eviction")

	require.NoError(t, deferred.Close())
	echoOnce("after-close")
}
