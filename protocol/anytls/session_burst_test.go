package anytls

import (
	"context"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	singanytls "github.com/sagernet/sing-anytls"
	"github.com/sagernet/sing-box/common/resourceguard"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

// sessionServer is a local stand-in for the AnyTLS server: it accepts TCP connections and then
// deliberately says nothing, which is what a slow or congested tunnel looks like from the
// client's side.
//
// It exists so the burst is measured against the REAL client session path (connect, write the
// settings frame, start the read loop) instead of against a mock. Nothing here talks to a public
// network: the listener is on the loopback interface and the only peer is this test.
type sessionServer struct {
	listener net.Listener
	accepted atomic.Int64
	closed   chan struct{}
	once     sync.Once
	conns    sync.Map
}

func newSessionServer(t *testing.T) *sessionServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &sessionServer{listener: listener, closed: make(chan struct{})}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			server.accepted.Add(1)
			server.conns.Store(conn, struct{}{})
		}
	}()
	t.Cleanup(func() {
		server.once.Do(func() { close(server.closed) })
		listener.Close()
		server.conns.Range(func(key, _ any) bool {
			key.(net.Conn).Close()
			return true
		})
	})
	return server
}

func (s *sessionServer) address() string {
	return s.listener.Addr().String()
}

// slowDialOut delays before connecting, which is what a TLS handshake against a distant server
// costs. The delay is what makes concurrent creation observable at all.
func slowDialOut(address string, delay time.Duration, inFlight *atomic.Int64, peak *atomic.Int64) singanytls.DialOutFunc {
	return func(ctx context.Context) (net.Conn, error) {
		current := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			observed := peak.Load()
			if current <= observed || peak.CompareAndSwap(observed, current) {
				break
			}
		}
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		dialer := net.Dialer{}
		return dialer.DialContext(ctx, "tcp", address)
	}
}

// TestSessionBurstWithoutGuardReproducesTheAmplification establishes the baseline the guard is
// measured against: with no admission, every concurrent flow starts its own session.
//
// This is the local, loopback-only reproduction of the measured iOS event, on the real client
// path. It is deliberately kept as a test rather than a one-off measurement: if a future change
// to sing-anytls made sessions share by capacity, this test would notice that the baseline
// moved.
func TestSessionBurstWithoutGuardReproducesTheAmplification(t *testing.T) {
	server := newSessionServer(t)
	var inFlight, peak atomic.Int64
	client, err := singanytls.NewClient(singanytls.ClientOptions{
		Password: "test-password",
		DialOut:  slowDialOut(server.address(), 2*time.Millisecond, &inFlight, &peak),
	})
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	const burst = 200
	var wg sync.WaitGroup
	var mutex sync.Mutex
	conns := make([]net.Conn, 0, burst)
	for index := 0; index < burst; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, dialErr := dialWithAdmission(context.Background(), nil, client.DialContext, M.Socksaddr{Fqdn: "example.com", Port: 443})
			if dialErr != nil {
				return
			}
			// Hold the stream until the whole burst has arrived: a UDP NAT flow stays alive for
			// as long as its entry does, which is exactly why an idle session never becomes
			// available for reuse during the burst.
			mutex.Lock()
			conns = append(conns, conn)
			mutex.Unlock()
		}()
	}
	wg.Wait()
	defer func() {
		for _, conn := range conns {
			conn.Close()
		}
	}()

	require.Len(t, conns, burst)
	require.Equal(t, int64(burst), peak.Load(), "without a guard every flow creates its own session at once")
	// The listener's accept loop runs in its own goroutine, so the count is observed eventually
	// rather than at an instant that races the loop.
	require.Eventually(t, func() bool { return server.accepted.Load() == int64(burst) }, 5*time.Second, 5*time.Millisecond,
		"the server sees one connection per flow")
}

// TestSessionBurstWithGuardBoundsConcurrentCreation is the same burst through the admission
// seam, and it is the regression the iOS fix is built on: the peak number of sessions being
// created at once drops to the limit while every flow is still admitted.
func TestSessionBurstWithGuardBoundsConcurrentCreation(t *testing.T) {
	server := newSessionServer(t)
	var inFlight, peak atomic.Int64
	client, err := singanytls.NewClient(singanytls.ClientOptions{
		Password: "test-password",
		DialOut:  slowDialOut(server.address(), 2*time.Millisecond, &inFlight, &peak),
	})
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	config := resourceguard.DefaultNetworkExtensionConfig()
	config.Limit = 8
	guard := resourceguard.NewGuard(config, func() resourceguard.Level { return resourceguard.LevelNormal })

	const burst = 200
	var wg sync.WaitGroup
	var mutex sync.Mutex
	conns := make([]net.Conn, 0, burst)
	var refused atomic.Int64
	for index := 0; index < burst; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, dialErr := dialWithAdmission(context.Background(), guard, client.DialContext, M.Socksaddr{Fqdn: "example.com", Port: 443})
			if dialErr != nil {
				refused.Add(1)
				return
			}
			mutex.Lock()
			conns = append(conns, conn)
			mutex.Unlock()
		}()
	}
	wg.Wait()
	defer func() {
		for _, conn := range conns {
			conn.Close()
		}
	}()

	require.Zero(t, refused.Load(), "a burst of this size is queued, not refused")
	require.Len(t, conns, burst)
	require.LessOrEqual(t, peak.Load(), int64(8), "concurrent session creation must stay under the limit")
	require.Greater(t, peak.Load(), int64(1), "the burst must actually have been concurrent")

	stats := guard.Stats().Gate
	require.Equal(t, uint64(burst), stats.Admitted)
	require.Zero(t, stats.Inflight)
}

// TestSessionCreationCostCensus measures what one live AnyTLS session costs, because that
// constant is what turns "201 concurrent handshakes" into a number of megabytes.
//
// It is a census, not a gate: the assertion is only that the measurement is non-trivial and
// finite, so the number is printed for the record. The same constant lets a reader convert an
// admission limit into an expected peak: limit x bytesPerSession.
func TestSessionCreationCostCensus(t *testing.T) {
	server := newSessionServer(t)
	var inFlight, peak atomic.Int64
	client, err := singanytls.NewClient(singanytls.ClientOptions{
		Password: "test-password",
		DialOut:  slowDialOut(server.address(), 0, &inFlight, &peak),
	})
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	var baseline runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&baseline)

	const burst = 200
	conns := make([]net.Conn, 0, burst)
	for index := 0; index < burst; index++ {
		conn, dialErr := dialWithAdmission(context.Background(), nil, client.DialContext, M.Socksaddr{Fqdn: "example.com", Port: 443})
		require.NoError(t, dialErr)
		conns = append(conns, conn)
	}

	var loaded runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&loaded)

	perSession := (loaded.HeapAlloc - baseline.HeapAlloc) / burst
	t.Logf("live AnyTLS session cost: %d bytes/session (200 sessions added %d bytes of live heap)",
		perSession, loaded.HeapAlloc-baseline.HeapAlloc)
	require.NotZero(t, perSession, "a live session must cost something")

	for _, conn := range conns {
		conn.Close()
	}
}
func TestSessionBurstGuardDoesNotChangeObservableBehaviour(t *testing.T) {
	server := newSessionServer(t)
	var inFlight, peak atomic.Int64
	client, err := singanytls.NewClient(singanytls.ClientOptions{
		Password: "test-password",
		DialOut:  slowDialOut(server.address(), 0, &inFlight, &peak),
	})
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	config := resourceguard.DefaultNetworkExtensionConfig()
	config.Limit = 4
	guard := resourceguard.NewGuard(config, func() resourceguard.Level { return resourceguard.LevelNormal })

	const burst = 16
	conns := make([]net.Conn, 0, burst)
	var wg sync.WaitGroup
	var mutex sync.Mutex
	for index := 0; index < burst; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, dialErr := dialWithAdmission(context.Background(), guard, client.DialContext, M.Socksaddr{Fqdn: "example.com", Port: 443})
			if dialErr != nil {
				return
			}
			mutex.Lock()
			conns = append(conns, conn)
			mutex.Unlock()
		}()
	}
	wg.Wait()
	require.Len(t, conns, burst)

	for index, conn := range conns {
		require.NoError(t, conn.Close(), "stream %d must close cleanly under the guard", index)
	}
	require.Zero(t, guard.Stats().Gate.Inflight)
}
