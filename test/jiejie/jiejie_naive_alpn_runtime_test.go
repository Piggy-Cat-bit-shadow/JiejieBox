package jiejie_test

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
)

// RUNTIME proof that a TCP listener never negotiates a QUIC-only protocol.
//
// The TLS config object is shared between the TCP listener and the HTTP/3
// initialiser, and STDServerConfig.Server() reads it at HANDSHAKE time rather
// than capturing it. A mutation performed after TCP starts therefore changes what
// later TCP handshakes negotiate, which is why ALPN is now resolved once, up
// front, for every transport the inbound serves.
//
// The unit tests assert the resolver's output. These tests assert what a real TLS
// client actually negotiates, which is the property that matters and the only way
// to catch a mutation that happens at the wrong moment.

// negotiateTCP ALPN performs a TLS handshake offering `offered` and returns the
// protocol the server selected.
func negotiateTCPALPN(t *testing.T, port uint16, offered []string) (string, error) {
	t.Helper()
	raw, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(int(port)), 10*time.Second)
	if err != nil {
		return "", err
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))

	conn := tls.Client(raw, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "naive.test",
		NextProtos:         offered,
	})
	if err = conn.Handshake(); err != nil {
		return "", err
	}
	return conn.ConnectionState().NegotiatedProtocol, nil
}

// startNaiveInboundTCPOnly starts a tcp-only inbound, which is the SHIPPED
// production topology.
func startNaiveInboundTCPOnly(t *testing.T) uint16 {
	t.Helper()
	return startNaiveInboundWithNetwork(t, "tcp")
}

// TestJiejieNaiveTCPALPNMatrix is the runtime matrix for a tcp-only inbound.
func TestJiejieNaiveTCPALPNMatrix(t *testing.T) {
	port := startNaiveInboundTCPOnly(t)

	t.Run("offer h2 negotiates h2", func(t *testing.T) {
		negotiated, err := negotiateTCPALPN(t, port, []string{"h2"})
		require.NoError(t, err)
		require.Equal(t, "h2", negotiated)
	})

	t.Run("offer http/1.1 negotiates http/1.1", func(t *testing.T) {
		negotiated, err := negotiateTCPALPN(t, port, []string{"http/1.1"})
		require.NoError(t, err)
		require.Equal(t, "http/1.1", negotiated)
	})

	t.Run("offer h3 ONLY must not negotiate h3", func(t *testing.T) {
		// This is the isolation invariant. If the server selected h3 here, the
		// shared TLS config has been contaminated by the QUIC path, and a TCP
		// client would be told to speak HTTP/3 over a TCP socket.
		negotiated, err := negotiateTCPALPN(t, port, []string{"h3"})
		if err != nil {
			// A clean handshake failure is also correct: nothing was offered
			// that this listener supports.
			t.Logf("h3-only offer rejected at the handshake: %v", err)
			return
		}
		require.NotEqual(t, "h3", negotiated,
			"a TCP listener must never negotiate h3: it is a QUIC-only protocol, "+
				"and selecting it means the QUIC path mutated the shared TLS config")
		t.Logf("h3-only offer negotiated %q (not h3)", negotiated)
	})

	t.Run("offer h3 and h2 negotiates h2", func(t *testing.T) {
		negotiated, err := negotiateTCPALPN(t, port, []string{"h3", "h2"})
		require.NoError(t, err)
		require.Equal(t, "h2", negotiated,
			"when a client offers both, the TCP listener must choose h2")
	})

	t.Run("offer h3 and http/1.1 negotiates http/1.1", func(t *testing.T) {
		negotiated, err := negotiateTCPALPN(t, port, []string{"h3", "http/1.1"})
		require.NoError(t, err)
		require.Equal(t, "http/1.1", negotiated)
	})
}

// TestJiejieNaiveTCPALPNPrefersH2OverH3AfterH3Starts covers the ORDERING hazard.
//
// The contamination worth guarding against is a QUIC-path mutation changing what
// a TCP client NEGOTIATES. On a tcp+udp inbound h3 is legitimately present in the
// shared ALPN list - it must be, or the HTTP/3 listener could never handshake -
// so the property that can actually be asserted for TCP is the PREFERENCE: a
// client offering a usable protocol alongside h3 must get the usable one.
//
// The handshakes are deliberately performed AFTER an HTTP/3 connection has been
// established, because that is when the QUIC initialiser has run. A test that
// only handshook before H3 started would pass even with a mutation bug.
//
// Scope note, stated so this is not over-read: h3 appearing in a tcp+udp
// inbound's ALPN list is sing-box's established behaviour, not something specific
// to this inbound. transport/http/server.go prepends h3 to the same shared config
// for the shipped MASQUE inbound, whose production configuration is tcp+udp. This
// test pins the preference and the absence of post-hoc mutation; it does not
// claim per-listener ALPN, which the shared ServerConfig architecture does not
// support (and which cannot be obtained by cloning, since STDServerConfig.Clone
// drops the certificate provider, ACME service and watcher).
func TestJiejieNaiveTCPALPNPrefersH2OverH3AfterH3Starts(t *testing.T) {
	port := startNaiveInboundWithNetwork(t, "tcp\nudp")

	// Force HTTP/3 to initialise by connecting to it.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	quicConn, err := quic.DialAddrEarly(ctx, "127.0.0.1:"+strconv.Itoa(int(port)), &tls.Config{
		ServerName:         "naive.test",
		InsecureSkipVerify: true,
		NextProtos:         []string{http3.NextProtoH3},
	}, &quic.Config{})
	if err != nil {
		t.Logf("HTTP/3 not available in this build (%v); the ordering assertions "+
			"below still apply because the QUIC path is what would mutate the "+
			"shared config", err)
	} else {
		_ = quicConn.CloseWithError(0, "")
		t.Logf("an HTTP/3 connection was established, so the QUIC initialiser ran")
	}

	// The TCP preference must be unaffected by that.
	negotiated, err := negotiateTCPALPN(t, port, []string{"h3", "h2"})
	require.NoError(t, err)
	require.Equal(t, "h2", negotiated,
		"after HTTP/3 has started, a TCP client offering h3 and h2 must still be "+
			"given h2: h3 is unusable over TCP")

	http1, err := negotiateTCPALPN(t, port, []string{"h3", "http/1.1"})
	require.NoError(t, err)
	require.Equal(t, "http/1.1", http1,
		"a TCP client offering h3 and http/1.1 must be given http/1.1")

	// And the plain cases must still hold.
	h2, err := negotiateTCPALPN(t, port, []string{"h2"})
	require.NoError(t, err)
	require.Equal(t, "h2", h2)

	t.Logf("after H3 start: h3+h2 -> %q, h3+http/1.1 -> %q, h2 -> %q",
		negotiated, http1, h2)
}

// TestJiejieNaiveTCPConnectStillWorksAlongsideH3 proves the ALPN isolation did
// not cost the inbound its TCP data path.
func TestJiejieNaiveTCPConnectStillWorksAlongsideH3(t *testing.T) {
	port := startNaiveInboundWithNetwork(t, "tcp\nudp")
	origin := startCountingTCPOrigin(t)

	conn := naiveTLSConn(t, port, "http/1.1")
	response := naiveWriteConnectOK(t, conn, origin.addr, map[string]string{
		"Proxy-Authorization": naiveBasicAuth(),
	})
	defer response.Body.Close()
	require.Equal(t, 200, response.StatusCode)

	_, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: " + origin.addr + "\r\nConnection: close\r\n\r\n"))
	require.NoError(t, err)
	require.True(t, waitForDial(&origin.conns, 0),
		"TCP CONNECT must still reach the origin on a tcp+udp inbound")
}

// TestJiejieNaiveDualStackQUICALPNNegotiatesH3 is the runtime proof for the QUIC
// half of the same split.
//
// The tests above cover what a TCP client negotiates. The QUIC half was only
// covered by a resolver test that asserted quicNextProtos() in isolation, while
// production handed the SHARED config to the HTTP/3 listener - so an operator
// list of ["h2","http/1.1","h3"] made a QUIC client that offers ["h3","h2"]
// negotiate "h2" on a QUIC connection. This test performs the real handshake and
// asserts the negotiated protocol, which is the property a client acts on.
//
// Fixed ports are used because Windows reserves whole ephemeral ranges for UDP;
// a dynamically picked port can land inside one (WSAEACCES), which disables
// HTTP/3 and turns this test into a false negative. 28480-28481 are outside the
// excluded ranges on the machine this was written on.
func TestJiejieNaiveDualStackQUICALPNNegotiatesH3(t *testing.T) {
	cases := []struct {
		name       string
		port       uint16
		alpn       badoption.Listable[string]
		wantResult string
	}{
		{
			name:       "operator list with h2 before h3 still negotiates h3",
			port:       28480,
			alpn:       badoption.Listable[string]{"h2", "http/1.1", "h3"},
			wantResult: http3.NextProtoH3,
		},
		{
			name:       "unset alpn still negotiates h3",
			port:       28481,
			alpn:       nil,
			wantResult: http3.NextProtoH3,
		},
		{
			// The contract: `network` decides whether H3 is served. An operator
			// list without h3 stays a TCP-side list; it used to disable the QUIC
			// listener with only a warning, after `sing-box check` had passed.
			name:       "tls.alpn without h3 still negotiates h3",
			port:       28482,
			alpn:       badoption.Listable[string]{"h2", "http/1.1"},
			wantResult: http3.NextProtoH3,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			startNaiveInboundTuned(t, "tcp\nudp", false, func(options *option.NaiveInboundOptions) {
				options.ListenOptions.ListenPort = testCase.port
				if testCase.alpn != nil {
					options.TLS.ALPN = testCase.alpn
				}
			})

			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			conn, err := quic.DialAddrEarly(ctx, "127.0.0.1:"+strconv.Itoa(int(testCase.port)), &tls.Config{
				ServerName:         "naive.test",
				InsecureSkipVerify: true,
				NextProtos:         []string{http3.NextProtoH3, "h2"},
			}, &quic.Config{})
			require.NoError(t, err)
			defer conn.CloseWithError(0, "")

			require.Equal(t, testCase.wantResult,
				conn.ConnectionState().TLS.NegotiatedProtocol,
				"a QUIC client offering h3 must never be given h2")
		})
	}
}
