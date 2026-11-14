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
// costs. The delay is what lets a whole burst arrive while the one shared creation is in flight.
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

// blockingDialOut parks every caller inside DialOut until the test releases it. With coalescing only
// the creator should ever be parked; waiters attach to the creation instead of starting one.
func blockingDialOut(address string, inFlight, peak *atomic.Int64, release <-chan struct{}) singanytls.DialOutFunc {
	return func(ctx context.Context) (net.Conn, error) {
		current := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			observed := peak.Load()
			if current <= observed || peak.CompareAndSwap(observed, current) {
				break
			}
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		dialer := net.Dialer{}
		return dialer.DialContext(ctx, "tcp", address)
	}
}

func burstDestination() M.Socksaddr {
	return M.Socksaddr{Fqdn: "example.com", Port: 443}
}

// TestSessionBurstSharesOneSessionWithoutGuard is the post-coalescing baseline.
//
// Before the pool shared creations, this burst performed one session per flow (the "amplification"
// test). The client now coalesces concurrent first flows onto one in-flight creation, so the
// baseline itself moved: 200 concurrent flows open ONE session, and the per-flow cost is a stream
// open, not a connect plus handshake. The test is kept as the regression that notices if that
// changed.
func TestSessionBurstSharesOneSessionWithoutGuard(t *testing.T) {
	server := newSessionServer(t)
	var inFlight, peak atomic.Int64
	release := make(chan struct{})
	const burst = 200
	client, err := singanytls.NewClient(singanytls.ClientOptions{
		Password: "test-password",
		DialOut:  blockingDialOut(server.address(), &inFlight, &peak, release),
	})
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	start := make(chan struct{})
	var wg sync.WaitGroup
	var mutex sync.Mutex
	conns := make([]net.Conn, 0, burst)
	var failures atomic.Int64
	for index := 0; index < burst; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			dialCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			conn, dialErr := client.DialContext(dialCtx, burstDestination())
			if dialErr != nil {
				failures.Add(1)
				return
			}
			mutex.Lock()
			conns = append(conns, conn)
			mutex.Unlock()
		}()
	}
	close(start)
	require.Eventually(t, func() bool {
		return client.PendingCreationWaiters() == burst-1
	}, 5*time.Second, time.Millisecond, "the burst did not attach to one creation")
	close(release)
	wg.Wait()
	defer func() {
		for _, conn := range conns {
			conn.Close()
		}
	}()

	require.Zero(t, failures.Load(), "every flow must be admitted to a stream")
	require.Len(t, conns, burst)
	require.EqualValues(t, 1, peak.Load(), "the burst entered session creation %d times", peak.Load())
	require.Eventually(t, func() bool { return server.accepted.Load() == 1 }, 2*time.Second, time.Millisecond,
		"the server saw %d physical sessions for one burst", server.accepted.Load())
}

// TestSessionBurstWithGuardChargesOneCreation is the same burst through the PRODUCTION admission
// hook (the one the outbound wires), not the legacy whole-dial wrapper.
func TestSessionBurstWithGuardChargesOneCreation(t *testing.T) {
	server := newSessionServer(t)
	var inFlight, peak atomic.Int64
	release := make(chan struct{})
	config := resourceguard.DefaultNetworkExtensionConfig()
	config.Limit = 8
	guard := resourceguard.NewGuard(config, func() resourceguard.Level { return resourceguard.LevelNormal })
	client, err := singanytls.NewClient(singanytls.ClientOptions{
		Password:      "test-password",
		DialOut:       blockingDialOut(server.address(), &inFlight, &peak, release),
		AdmitCreation: admitSessionCreation(guard, "burst"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	const burst = 200
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mutex sync.Mutex
	conns := make([]net.Conn, 0, burst)
	var failures atomic.Int64
	for index := 0; index < burst; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			dialCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			conn, dialErr := client.DialContext(dialCtx, burstDestination())
			if dialErr != nil {
				failures.Add(1)
				return
			}
			mutex.Lock()
			conns = append(conns, conn)
			mutex.Unlock()
		}()
	}
	close(start)

	// The creator is parked in DialOut; every other flow waits on the same creation. The gate must
	// therefore show exactly ONE permit and the dialer exactly one parked call.
	require.Eventually(t, func() bool {
		stats := guard.Stats().Gate
		return stats.Inflight == 1 && stats.Admitted == 1 && inFlight.Load() == 1 &&
			client.PendingCreationWaiters() == burst-1
	}, 5*time.Second, time.Millisecond, "the burst did not coalesce onto one charged creation: %+v waiters=%d",
		guard.Stats().Gate, client.PendingCreationWaiters())
	require.Equal(t, 1, guard.Stats().Gate.MaxInflight)

	close(release)
	wg.Wait()
	defer func() {
		for _, conn := range conns {
			conn.Close()
		}
	}()

	require.Zero(t, failures.Load(), "every flow must be admitted to a stream")
	require.Len(t, conns, burst)
	require.EqualValues(t, 1, peak.Load())
	require.Eventually(t, func() bool { return server.accepted.Load() == 1 }, 2*time.Second, time.Millisecond,
		"the burst opened %d physical sessions", server.accepted.Load())

	stats := guard.Stats().Gate
	require.EqualValues(t, 1, stats.Admitted, "the burst was charged %d creations", stats.Admitted)
	require.EqualValues(t, 1, stats.Released)
	require.Zero(t, stats.Inflight)
}

// TestSessionCreationCostCensus measures what one live AnyTLS session costs, because that
// constant is what turns "201 concurrent handshakes" into a number of megabytes.
//
// It is a census, not a gate: the assertion is only that the measurement is non-trivial and
// finite, so the number is printed for the record. The same constant lets a reader convert an
// admission limit into an expected peak: limit x bytesPerSession.
//
// NOTE: this census dials SEQUENTIALLY and keeps every stream open, so each demand still creates its
// own session; coalescing only applies to demand that finds no reusable session while a creation is
// in flight. That is deliberate - it measures the per-session cost, which is unchanged.
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
		conn, dialErr := client.DialContext(context.Background(), burstDestination())
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

// TestSessionBurstGuardDoesNotChangeObservableBehaviour keeps the embedder contract under a guard:
// every stream opens, and every stream closes cleanly, with one charged creation.
func TestSessionBurstGuardDoesNotChangeObservableBehaviour(t *testing.T) {
	server := newSessionServer(t)
	var inFlight, peak atomic.Int64
	release := make(chan struct{})
	config := resourceguard.DefaultNetworkExtensionConfig()
	config.Limit = 4
	guard := resourceguard.NewGuard(config, func() resourceguard.Level { return resourceguard.LevelNormal })
	client, err := singanytls.NewClient(singanytls.ClientOptions{
		Password:      "test-password",
		DialOut:       blockingDialOut(server.address(), &inFlight, &peak, release),
		AdmitCreation: admitSessionCreation(guard, "observable"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	const burst = 16
	conns := make([]net.Conn, 0, burst)
	var wg sync.WaitGroup
	var mutex sync.Mutex
	for index := 0; index < burst; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, dialErr := client.DialContext(context.Background(), burstDestination())
			if dialErr != nil {
				return
			}
			mutex.Lock()
			conns = append(conns, conn)
			mutex.Unlock()
		}()
	}
	require.Eventually(t, func() bool {
		return client.PendingCreationWaiters() == burst-1
	}, 5*time.Second, time.Millisecond, "the burst did not attach to one creation")
	close(release)
	wg.Wait()
	require.Len(t, conns, burst)

	for index, conn := range conns {
		require.NoError(t, conn.Close(), "stream %d must close cleanly under the guard", index)
	}
	require.Zero(t, guard.Stats().Gate.Inflight)
	require.Eventually(t, func() bool { return server.accepted.Load() == 1 }, 2*time.Second, time.Millisecond,
		"the burst opened %d physical sessions", server.accepted.Load())
}
