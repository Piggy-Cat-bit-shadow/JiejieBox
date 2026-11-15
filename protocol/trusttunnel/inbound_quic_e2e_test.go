//go:build with_quic

package trusttunnel

import (
	"context"
	"crypto/tls"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
)

// freeDualPort returns a port that was free for both TCP and UDP a moment ago.
//
// A fixed port would collide across CI jobs, and listener.ListenPort=0 would give
// TCP and UDP two different ephemeral ports (the dual-stack property is what is
// under test). Probing both families also skips Windows' excluded UDP ranges,
// where binding fails with WSAEACCES.
//
// The candidate comes from an explicit range below the Windows dynamic range, not
// from Listen(":0"). On a host with large excluded ranges the ephemeral allocator
// can hand the SAME TCP port back on every attempt, and that port may be excluded
// for UDP; the old loop then failed 20 times in a row. Scanning an explicit range
// advances past the excluded block and still verifies that both families bind.
func freeDualPort(t *testing.T) uint16 {
	t.Helper()
	seed := uint64(time.Now().UnixNano())
	for attempt := 0; attempt < 512; attempt++ {
		port := 20000 + int((seed+uint64(attempt)*2654435761)%30000)
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		tcpListener, err := net.Listen("tcp", address)
		if err != nil {
			continue
		}
		udpConn, err := net.ListenPacket("udp", address)
		if err != nil {
			_ = tcpListener.Close()
			continue
		}
		_ = udpConn.Close()
		_ = tcpListener.Close()
		return uint16(port)
	}
	t.Fatal("no free port for both TCP and UDP")
	return 0
}

// startDualStackInbound starts a real Inbound with the given tls.alpn and
// returns the port it listens on.
func startDualStackInbound(t *testing.T, alpn []string) uint16 {
	t.Helper()
	options := validInboundOptions(t)
	options.Network = "tcp\nudp"
	listenAddr := badoption.Addr(netip.MustParseAddr("127.0.0.1"))
	options.ListenOptions.Listen = &listenAddr
	options.ListenPort = freeDualPort(t)
	options.TLS.ALPN = alpn

	inbound, err := NewInbound(context.Background(), nil, log.NewNOPFactory().Logger(), "tt-in", options)
	require.NoError(t, err)
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = scope.Close() })
	require.NoError(t, inbound.Start(adapter.StartStateStart, scope))
	return options.ListenPort
}

// TestDualStackInboundNegotiatesH3OnQUIC is the end-to-end proof for the fix
// that scoped trusttunnel's ALPN per transport: a tcp+udp inbound must give a
// QUIC client that offers ["h3","h2"] the h3 protocol, for every operator ALPN
// shape. The unit test pins resolveTransportALPN's output; this one pins what a
// real handshake negotiates, which is the property the client acts on.
func TestDualStackInboundNegotiatesH3OnQUIC(t *testing.T) {
	cases := []struct {
		name string
		alpn []string
	}{
		{name: "unset", alpn: nil},
		{name: "h2 before h3", alpn: []string{"h2", "http/1.1", "h3"}},
		{name: "h3 first", alpn: []string{"h3", "h2"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			port := startDualStackInbound(t, testCase.alpn)

			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			conn, err := quic.DialAddrEarly(ctx, net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))), &tls.Config{
				ServerName:         "tt.test",
				InsecureSkipVerify: true,
				NextProtos:         []string{http3.NextProtoH3, "h2"},
			}, &quic.Config{})
			require.NoError(t, err)
			defer conn.CloseWithError(0, "")

			require.Equal(t, http3.NextProtoH3, conn.ConnectionState().TLS.NegotiatedProtocol,
				"a QUIC client offering h3 must never be given h2")
		})
	}
}
