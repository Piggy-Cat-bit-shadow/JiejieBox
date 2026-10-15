package v2rayxhttp

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/tls"
	M "github.com/sagernet/sing/common/metadata"

	"golang.org/x/net/http2"
)

// lx: XHTTP Round 2 — the H2/XMUX redial boundary, measured rather than assumed.
//
// The earlier round observed "server TCP accepts = 2, XMUX pooled connections = 1"
// and recorded it as a known boundary. This file turns it into a conclusion: which
// triggers produce a second dial, what XMUX does and does not learn, whether the
// per-connection budgets survive it, and what it costs.
//
// The mechanism, from the transport's side: an http2.Transport owns a pool of TCP
// connections and re-dials when the one it has dies. XMUX owns an abstraction it
// calls a pooled connection and accounts for it — max_connections,
// c_max_reuse_times, h_max_request_times, the breaker, RetireSuspect — but that
// abstraction is a *transport*, not a socket. A redial inside the transport is
// therefore invisible to XMUX by construction, and the question that matters is
// whether the accounting XMUX promises still holds across it. These tests say it
// does.
//
// A graceful GOAWAY is deliberately not a row here: forcing one means shutting the
// server's listener down as well, so the replacement dial could not be counted.
// What a GOAWAY does to this transport is pinned where it belongs, in
// TestUplinkHandlesAGoawayWithoutConflictingReplay (no hang, no byte-different
// replay, the failure reported).

// redialTestClient builds a client whose XMUX carries explicit limits and whose
// HTTP/2 transport dials the counting server in cleartext (h2c).
func redialTestClient(t *testing.T, server *countingH2CServer, config xmuxConfig) *Client {
	t.Helper()
	meta, err := normalizeMeta(metaOptions{ScMinPostsIntervalMs: "0"}, modePacketUp)
	if err != nil {
		t.Fatalf("normalizeMeta: %v", err)
	}
	return &Client{
		ctx:        context.Background(),
		serverAddr: M.ParseSocksaddr(server.addr),
		xmux: newXmuxManager(config, func() xmuxConn {
			return &http2XmuxConn{transport: &http2.Transport{
				AllowHTTP: true,
				DialTLSContext: func(ctx context.Context, network, address string, _ *tls.STDConfig) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, address)
				},
			}}
		}),
		scheme:       "http",
		host:         server.addr,
		path:         "/upload/",
		mode:         modePacketUp,
		headers:      make(http.Header),
		paddingRange: intRange{0, 0},
		meta:         meta,
	}
}

// tryPost issues one upload POST through a specific pooled connection, the way
// packet-up does, and drains the answer. It returns the error rather than failing
// the test so a caller can tolerate the failure a freshly killed socket produces.
func tryPost(client *Client, pooled *xmuxClient) error {
	request, err := client.newRequest(context.Background(), http.MethodPost, "session", "0", nil)
	if err != nil {
		return err
	}
	response, err := pooled.roundTrip(request)
	if err != nil {
		return err
	}
	drainAndClose(response.Body)
	return nil
}

// TestRedialTriggersAndXmuxBlindness answers "when does the second dial happen".
// Measured: a server that closes the connection produces a redial; normal reuse
// and a short idle period do not. In every case XMUX still reports one pooled
// connection, because what it counts is a transport, not a socket.
func TestRedialTriggersAndXmuxBlindness(t *testing.T) {
	testCases := []struct {
		name         string
		breakThePath func(t *testing.T, server *countingH2CServer)
		wantAccepts  int
	}{
		{
			name:         "normal reuse",
			breakThePath: func(t *testing.T, server *countingH2CServer) {},
			wantAccepts:  1,
		},
		{
			name: "short idle",
			breakThePath: func(t *testing.T, server *countingH2CServer) {
				time.Sleep(1200 * time.Millisecond)
			},
			wantAccepts: 1,
		},
		{
			name: "server closes the connection",
			breakThePath: func(t *testing.T, server *countingH2CServer) {
				server.listener.killAll()
				time.Sleep(50 * time.Millisecond)
			},
			wantAccepts: 2,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := newCountingH2CServer(t)
			client := redialTestClient(t, server, xmuxConfig{})

			pooled, _ := client.xmux.get()
			if err := tryPost(client, pooled); err != nil {
				t.Fatalf("first request: %v", err)
			}
			server.waitForRequests(t, 1)

			testCase.breakThePath(t, server)

			// Keep requesting until the expected connection count is reached. A
			// request that rides the corpse fails once; the next one makes the
			// transport dial again.
			deadline := time.Now().Add(writeFreeBudget)
			for server.listener.count() < testCase.wantAccepts && time.Now().Before(deadline) {
				next, _ := client.xmux.get()
				_ = tryPost(client, next)
				if server.listener.count() >= testCase.wantAccepts {
					break
				}
				time.Sleep(30 * time.Millisecond)
			}
			if accepts := server.listener.count(); accepts != testCase.wantAccepts {
				t.Fatalf("server saw %d TCP connections, want %d", accepts, testCase.wantAccepts)
			}
			if pooledClients := pooledClientCount(client); pooledClients != 1 {
				t.Fatalf("XMUX reports %d pooled connections, want 1: a transport-level redial must not leak transports", pooledClients)
			}
		})
	}
}

// TestRequestBudgetSurvivesARedial is the accounting claim. h_max_request_times
// is a promise about REQUESTS, and a redial underneath the transport must not
// reset the budget: if it did, a client could promise a server "at most N
// requests on this connection" and then keep using fresh sockets past N.
func TestRequestBudgetSurvivesARedial(t *testing.T) {
	server := newCountingH2CServer(t)
	client := redialTestClient(t, server, xmuxConfig{hMaxRequestTimes: intRange{3, 3}})

	pooled, _ := client.xmux.get()
	if err := tryPost(client, pooled); err != nil {
		t.Fatalf("first request: %v", err)
	}
	server.waitForRequests(t, 1)
	if remaining := atomic.LoadInt32(&pooled.leftRequests); remaining != 2 {
		t.Fatalf("leftRequests = %d after one request against a limit of 3, want 2", remaining)
	}

	// The socket dies; the transport will dial a new one on the next request.
	server.listener.killAll()
	time.Sleep(50 * time.Millisecond)

	deadline := time.Now().Add(writeFreeBudget)
	for server.listener.count() < 2 && time.Now().Before(deadline) {
		_ = tryPost(client, pooled)
		time.Sleep(30 * time.Millisecond)
	}
	server.waitForRequests(t, 2)
	if remaining := atomic.LoadInt32(&pooled.leftRequests); remaining != 1 {
		t.Fatalf("leftRequests = %d after a request that rode a redialed socket, want 1 — the budget was reset", remaining)
	}
	if accepts := server.listener.count(); accepts < 2 {
		t.Fatalf("server saw %d TCP connections, want a redial: this test must exercise the redial path", accepts)
	}

	for attempts := 0; attempts < 5; attempts++ {
		if atomic.LoadInt32(&pooled.leftRequests) == 0 {
			break
		}
		_ = tryPost(client, pooled)
		time.Sleep(20 * time.Millisecond)
	}
	if cause := pooled.evictCause(); cause != "requests" {
		t.Fatalf("evictCause = %q after the third request, want %q — the budget must retire the pooled connection", cause, "requests")
	}
}

// TestRetireSuspectAfterARedialDoesNotResurrectTheOldPath: RetireSuspect is the
// network-change boundary — a pooled client is detached so new streams stop using
// it. A redial that happened before the retirement must not make the old path
// reusable again, and the replacement must come from a fresh dial.
func TestRetireSuspectAfterARedialDoesNotResurrectTheOldPath(t *testing.T) {
	server := newCountingH2CServer(t)
	client := redialTestClient(t, server, xmuxConfig{})

	pooled, _ := client.xmux.get()
	if err := tryPost(client, pooled); err != nil {
		t.Fatalf("first request: %v", err)
	}
	server.waitForRequests(t, 1)

	server.listener.killAll()
	time.Sleep(50 * time.Millisecond)
	deadline := time.Now().Add(writeFreeBudget)
	for server.listener.count() < 2 && time.Now().Before(deadline) {
		_ = tryPost(client, pooled)
		time.Sleep(30 * time.Millisecond)
	}
	acceptsAfterRedial := server.listener.count()
	if acceptsAfterRedial < 2 {
		t.Fatalf("server saw %d TCP connections, want a redial before retirement", acceptsAfterRedial)
	}

	client.xmux.RetireSuspect()

	replacement, _ := client.xmux.get()
	if replacement == pooled {
		t.Fatal("RetireSuspect handed the retired pooled connection to a new stream")
	}
	for attempts := 0; attempts < 10 && server.listener.count() <= acceptsAfterRedial; attempts++ {
		_ = tryPost(client, replacement)
		time.Sleep(30 * time.Millisecond)
	}
	if accepts := server.listener.count(); accepts <= acceptsAfterRedial {
		t.Fatalf("server saw %d TCP connections after retirement, want a new dial past %d", accepts, acceptsAfterRedial)
	}
	// A retired pooled connection is either drained (it had a live stream) or
	// closed outright (it was idle); what must never happen is that it stays
	// eligible for new streams.
	if !pooled.draining.Load() && !pooled.conn.IsClosed() {
		t.Fatal("the retired pooled connection is neither draining nor closed: new streams could still be handed to it")
	}
}

// TestRedialCostsOneHandshake quantifies the only cost a redial has: the fresh
// TCP (and, with TLS, TLS) handshake the transport performs when it has no usable
// socket. The numbers are logged rather than graded; the bound exists to catch a
// retry storm, not to grade a host.
//
// The redial itself is pinned by TestRedialTriggersAndXmuxBlindness (a killed
// socket produces a second accept). What this test measures is the work that
// redial performs, taken on a freshly acquired pooled connection so the number
// does not depend on how quickly the transport notices a dead socket — the pool's
// own breaker and backoff can delay a retry long after the redial would happen.
func TestRedialCostsOneHandshake(t *testing.T) {
	server := newCountingH2CServer(t)
	client := redialTestClient(t, server, xmuxConfig{})

	pooled, _ := client.xmux.get()
	if err := tryPost(client, pooled); err != nil {
		t.Fatalf("first request: %v", err)
	}
	server.waitForRequests(t, 1)

	warmStarted := time.Now()
	if err := tryPost(client, pooled); err != nil {
		t.Fatalf("warm request: %v", err)
	}
	warm := time.Since(warmStarted)

	// Retire the pooled connection and time the first request on its replacement:
	// that is one dial plus one handshake, the same work a redial does.
	client.xmux.RetireSuspect()
	replacement, _ := client.xmux.get()
	if replacement == pooled {
		t.Fatal("RetireSuspect handed the retired pooled connection back")
	}
	coldStarted := time.Now()
	if err := tryPost(client, replacement); err != nil {
		t.Fatalf("request on a fresh pooled connection: %v", err)
	}
	cold := time.Since(coldStarted)
	server.waitForRequests(t, 2)

	if accepts := server.listener.count(); accepts < 2 {
		t.Fatalf("server saw %d TCP connections, want a fresh dial for the replacement", accepts)
	}
	t.Logf("request on a warm connection %s; first request on a freshly dialed connection %s (one handshake)", warm, cold)
	if cold > writeFreeBudget {
		t.Fatalf("the first request on a fresh connection took %s: that is a retry storm, not one handshake", cold)
	}
	if pooledClients := pooledClientCount(client); pooledClients != 1 {
		t.Fatalf("XMUX reports %d pooled connections, want 1", pooledClients)
	}
}
