package v2rayxhttp

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// lx: XHTTP Round 2 — HTTP/2 keep-alive parity.
//
// The rule being pinned is Xray's (transport/internet/splithttp/dialer.go:248-259
// with net.ChromeH2KeepAlivePeriod = 45 s): 0 selects the Chrome-equivalent
// default, a negative value disables pings, a positive value is used as
// configured. Before this, 0 went straight into ReadIdleTimeout — the same as
// disabling pings — while the reference kept pinging, which is a parity defect
// the option documentation had already promised away.

func TestHTTP2KeepAlivePeriodFollowsTheReference(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{name: "zero selects the reference default", in: 0, want: http2DefaultKeepAlivePeriod},
		{name: "negative disables pings", in: -1 * time.Second, want: 0},
		{name: "negative zero-ish disables pings", in: -500 * time.Millisecond, want: 0},
		{name: "positive is used as configured", in: 30 * time.Second, want: 30 * time.Second},
		{name: "one second is used as configured", in: time.Second, want: time.Second},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := http2KeepAlivePeriod(testCase.in); got != testCase.want {
				t.Fatalf("http2KeepAlivePeriod(%v) = %v, want %v", testCase.in, got, testCase.want)
			}
		})
	}
	if http2DefaultKeepAlivePeriod != 45*time.Second {
		t.Fatalf("http2DefaultKeepAlivePeriod = %v, want Xray's 45s Chrome period", http2DefaultKeepAlivePeriod)
	}
}

// TestHTTP2TransportCarriesTheNormalizedPeriod is the transport-config half: the
// value a node actually ends up with on its pooled HTTP/2 transports.
func TestHTTP2TransportCarriesTheNormalizedPeriod(t *testing.T) {
	t.Parallel()
	// The dialer is only captured in a closure here; nothing dials in this test.
	tlsDialer := tls.NewDialer(N.SystemDialer, testTLSConfig(t, "h2"))
	for _, testCase := range []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{name: "default", in: 0, want: http2DefaultKeepAlivePeriod},
		{name: "disabled", in: -1, want: 0},
		{name: "configured", in: 12 * time.Second, want: 12 * time.Second},
	} {
		transport := newHTTP2Transport(tlsDialer, testCase.in)
		if transport.ReadIdleTimeout != testCase.want {
			t.Fatalf("%s: ReadIdleTimeout = %v, want %v", testCase.name, transport.ReadIdleTimeout, testCase.want)
		}
	}
}

// TestClientDefaultKeepAliveReachesThePooledTransport is the config-path half:
// a node that never sets h_keep_alive_period must end up with the reference's
// default on the transports XMUX will pool, and an explicit negative must still
// disable pings.
func TestClientDefaultKeepAliveReachesThePooledTransport(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name string
		xmux *option.V2RayXHTTPXmuxOptions
		want time.Duration
	}{
		{name: "unset xmux", xmux: nil, want: http2DefaultKeepAlivePeriod},
		{name: "zero", xmux: &option.V2RayXHTTPXmuxOptions{HKeepAlivePeriod: 0}, want: http2DefaultKeepAlivePeriod},
		{name: "negative", xmux: &option.V2RayXHTTPXmuxOptions{HKeepAlivePeriod: -1}, want: 0},
		{name: "configured", xmux: &option.V2RayXHTTPXmuxOptions{HKeepAlivePeriod: 20}, want: 20 * time.Second},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			transport, err := NewClient(context.Background(), N.SystemDialer,
				M.ParseSocksaddr("127.0.0.1:443"), option.V2RayXHTTPOptions{
					Mode: modePacketUp,
					Xmux: testCase.xmux,
				}, testTLSConfig(t, "h2"))
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			client, ok := transport.(*Client)
			if !ok {
				t.Fatalf("NewClient returned %T, want *Client", transport)
			}
			pooled, ok := client.xmux.newConn().(*http2XmuxConn)
			if !ok {
				t.Fatalf("the pooled connection is not an *http2XmuxConn: TLS with alpn h2 must select HTTP/2")
			}
			if pooled.transport.ReadIdleTimeout != testCase.want {
				t.Fatalf("pooled transport ReadIdleTimeout = %v, want %v", pooled.transport.ReadIdleTimeout, testCase.want)
			}
		})
	}
}

// TestIdlePooledConnectionIsReusedWithoutANewDial guards the behaviour the
// keep-alive exists for: an idle period well under the ping period must not
// produce a second TCP connection. A ping itself takes 45 s to observe, which a
// unit test cannot wait for; what it can pin is that the pool keeps and reuses
// the connection across idle time, so a future change that turned the keep-alive
// period into an idle timeout (or a reconnect trigger) fails here.
func TestIdlePooledConnectionIsReusedWithoutANewDial(t *testing.T) {
	server := newCountingH2CServer(t)
	client := h2cClient(t, server.addr, modePacketUp)

	first, err := client.DialContext(context.Background())
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	if _, err = first.Write([]byte("first")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	server.waitForRequests(t, 1)
	if err = first.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}

	time.Sleep(1200 * time.Millisecond)

	second, err := client.DialContext(context.Background())
	if err != nil {
		t.Fatalf("second dial after idle: %v", err)
	}
	defer second.Close()
	if _, err = second.Write([]byte("second")); err != nil {
		t.Fatalf("second write: %v", err)
	}
	server.waitForRequests(t, 2)

	if accepts := server.listener.count(); accepts != 1 {
		t.Fatalf("server saw %d TCP connections across an idle period, want 1 (the pooled connection was re-dialed)", accepts)
	}
	if pooled := pooledClientCount(client); pooled != 1 {
		t.Fatalf("XMUX reports %d pooled connections after an idle period, want 1", pooled)
	}
}
