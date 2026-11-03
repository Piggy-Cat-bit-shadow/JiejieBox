package anytls

import (
	"context"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

// TestStalledHandshakesLeakNothing is the worst real shape of the device event: a server that
// completes the TCP connection and then never answers the TLS handshake, with more callers than
// the admission limit and every one of them giving up on a deadline.
//
// It attacks three properties at once:
//
//	the ceiling            in-flight handshakes never exceed the limit
//	the accounting         every permit returns, even for callers that timed out while queued
//	the process            no goroutine is left behind by the stalled dials
func TestStalledHandshakesLeakNothing(t *testing.T) {
	dial, peak := stalledStreamDial(t)

	config := resourceguard.DefaultNetworkExtensionConfig()
	config.Limit = 8
	config.Queue = 256
	config.Wait = 150 * time.Millisecond
	guard := resourceguard.NewGuard(config, func() resourceguard.Level { return resourceguard.LevelNormal })

	baselineGoroutines := runtime.NumGoroutine()

	const burst = 200
	var failed atomic.Int64
	var succeeded atomic.Int64
	var wg sync.WaitGroup
	for index := 0; index < burst; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			conn, err := dialWithAdmission(ctx, guard, dial, M.Socksaddr{Fqdn: "example.com", Port: 443})
			if err != nil {
				failed.Add(1)
				return
			}
			succeeded.Add(1)
			conn.Close()
		}()
	}
	wg.Wait()

	require.Equal(t, int64(burst), failed.Load()+succeeded.Load(),
		"every caller must land in exactly one outcome")
	require.Greater(t, failed.Load(), int64(0), "the stalled server must actually have failed callers")
	require.LessOrEqual(t, peak.Load(), int64(8), "the ceiling must hold against a stalled peer")

	stats := guard.Stats().Gate
	require.Zero(t, stats.Inflight, "a stalled handshake must not keep its permit after the caller gave up")
	require.Zero(t, stats.Waiters)
	require.Equal(t, int(stats.Admitted-stats.Released), stats.Inflight,
		"admitted=%d released=%d", stats.Admitted, stats.Released)
	require.Equal(t, uint64(burst), stats.Requested)
	require.Equal(t, stats.Admitted+stats.Refused+stats.Canceled+stats.TimedOut, stats.Requested)

	// The stalled TLS connections live on the server side until the test cleans up, so the
	// goroutine count is expected to settle rather than return exactly to baseline. What must
	// not happen is growth proportional to the burst.
	require.Eventually(t, func() bool {
		return runtime.NumGoroutine() < baselineGoroutines+burst/2
	}, 5*time.Second, 10*time.Millisecond,
		"stalled dials left %d goroutines behind", runtime.NumGoroutine()-baselineGoroutines)
}

// TestStallWithQueueExhaustedRefusesFast is the same attack with the queue deliberately too small
// to absorb the burst, so most callers are refused immediately instead of timing out. The point
// is that the two refusal paths (queue full, wait expired) both leave nothing behind.
func TestStallWithQueueExhaustedRefusesFast(t *testing.T) {
	dial, peak := stalledStreamDial(t)

	config := resourceguard.DefaultNetworkExtensionConfig()
	config.Limit = 4
	config.Queue = 8
	config.Wait = 200 * time.Millisecond
	guard := resourceguard.NewGuard(config, func() resourceguard.Level { return resourceguard.LevelNormal })

	started := time.Now()
	const burst = 500
	var failed atomic.Int64
	var wg sync.WaitGroup
	for index := 0; index < burst; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
			defer cancel()
			conn, err := dialWithAdmission(ctx, guard, dial, M.Socksaddr{Fqdn: "example.com", Port: 443})
			if err != nil {
				failed.Add(1)
				return
			}
			conn.Close()
		}()
	}
	wg.Wait()
	elapsed := time.Since(started)

	require.Equal(t, int64(burst), failed.Load(), "a 4-slot gate against 500 stalled dials admits nobody")
	require.Less(t, elapsed, 3*time.Second, "refusal must be bounded by the wait budget, not by the dial")
	require.LessOrEqual(t, peak.Load(), int64(4), "the ceiling must hold while the queue is exhausted")

	stats := guard.Stats().Gate
	require.Zero(t, stats.Inflight)
	require.Zero(t, stats.Waiters)
	require.Equal(t, int(stats.Admitted-stats.Released), stats.Inflight)
	require.Greater(t, stats.Refused, uint64(0), "the queue bound must have refused someone")
}

// TestStalledSessionDoesNotBlockASecondOutbound guards against a shared-state mistake: the guard
// is per process, not per outbound, so a stalled session on one tag must not silently disable
// another tag's admission - both are served by the same ceiling, in arrival order.
func TestStalledSessionDoesNotBlockASecondOutbound(t *testing.T) {
	dial, _ := stalledStreamDial(t)

	config := resourceguard.DefaultNetworkExtensionConfig()
	config.Limit = 2
	config.Queue = 16
	config.Wait = time.Second
	guard := resourceguard.NewGuard(config, func() resourceguard.Level { return resourceguard.LevelNormal })

	// Two stalled dials take the whole ceiling.
	stalled := make([]*resourceguard.Permit, 0, 2)
	for index := 0; index < 2; index++ {
		permit, err := guard.Acquire(context.Background(), resourceguard.ClassNormal)
		require.NoError(t, err)
		stalled = append(stalled, permit)
	}

	// A second caller from any outbound queues, then gets in when a slot returns.
	admittedSecond := make(chan struct{}, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		conn, err := dialWithAdmission(ctx, guard, dial, M.Socksaddr{Fqdn: "other.example", Port: 443})
		if err == nil {
			conn.Close()
		}
		admittedSecond <- struct{}{}
	}()
	require.Eventually(t, func() bool { return guard.Stats().Gate.Waiters == 1 }, time.Second, time.Millisecond)

	stalled[0].Release()
	require.Eventually(t, func() bool {
		select {
		case <-admittedSecond:
			return true
		default:
			return false
		}
	}, 3*time.Second, 5*time.Millisecond, "a released slot must reach the queued caller")

	stalled[1].Release()
	require.Eventually(t, func() bool { return guard.Stats().Gate.Inflight == 0 }, time.Second, time.Millisecond)
}

// stalledStreamDial builds a seam dial that starts a real TLS handshake against a server that
// never answers, and reports the peak number of handshakes in flight.
func stalledStreamDial(t *testing.T) (streamDialFunc, *atomic.Int64) {
	t.Helper()
	server := newSilentServer(t)
	var inFlight, peak atomic.Int64
	raw := tlsDialOut(server.listener.Addr().String(), &inFlight, &peak)
	return func(ctx context.Context, _ M.Socksaddr) (net.Conn, error) {
		return raw(ctx)
	}, &peak
}
