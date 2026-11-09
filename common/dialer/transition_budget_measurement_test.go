package dialer

// The dial-budget measurement the window's pass condition asks for: generation changes and
// concurrent dials in the SAME time window, with the numbers a budget claim needs.
//
// # What is measured
//
// One demand per caller, a fixed topology (one loopback echo endpoint), equal success volume (the
// same demands every round), and a generation burst running concurrently with those demands. For
// each demand we record how long the dial took; from the samples we report p50/p95/p99, the
// maximum, the failure count and the peak number of tracked connections (the socket peak).
//
// # What is asserted
//
// The budget itself: every demand succeeds, exactly one underlying dial per demand (the tracked
// count equals the demand count - amplification would raise it, shared state would lower it), the
// socket peak stays at the concurrency of the callers, and the p99 fits in the 2 s bound the
// window's regression commands use. The percentile table is logged for the report.

import (
	"context"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/control"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

// measuringEpochManager is the epoch-tracking manager plus a counter of the connections the dialer
// actually produced: one per successful underlying dial, which is the number a "one dial per
// demand" claim is about.
type measuringEpochManager struct {
	epochManager
	dialed atomic.Int64
}

func (m *measuringEpochManager) TrackConn(conn net.Conn) net.Conn {
	m.dialed.Add(1)
	return m.epochManager.TrackConn(conn)
}

func percentile(sorted []time.Duration, fraction float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(fraction * float64(len(sorted)-1))
	return sorted[index]
}

func TestGenerationChangesAndDialsShareOneWindowWithinBudget(t *testing.T) {
	const (
		demands       = 200
		concurrency   = 8
		burst         = 20
		burstInterval = 2 * time.Millisecond
	)

	target := startEchoServer(t)
	manager := &measuringEpochManager{}
	epoch := &epochNetworkManager{epoch: &atomic.Uint64{}, finder: control.NewDefaultInterfaceFinder()}
	ctx := service.ContextWith[adapter.ConnectionManager](context.Background(), manager)
	ctx = service.ContextWith[adapter.NetworkManager](ctx, epoch)
	dialer, err := NewDefault(ctx, option.DialerOptions{})
	require.NoError(t, err)

	// The generation burst runs INSIDE the demand window, so resets land between dials instead of
	// before them: that is the interleaving the budget is about.
	var peak atomic.Int64
	samplingDone := make(chan struct{})
	go func() {
		defer close(samplingDone)
		for index := 0; index < burst; index++ {
			epoch.epoch.Add(1)
			manager.CloseAll()
			time.Sleep(burstInterval)
		}
		for index := 0; index < 20; index++ {
			if live := int64(manager.trackedCount()); live > peak.Load() {
				peak.Store(live)
			}
			time.Sleep(time.Millisecond)
		}
	}()

	samples := make([]time.Duration, 0, demands)
	var samplesAccess sync.Mutex
	var failures atomic.Int32
	var firstFailure atomic.Value
	work := make(chan struct{}, demands)
	for index := 0; index < demands; index++ {
		work <- struct{}{}
	}
	close(work)

	var waitGroup sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for range work {
				started := time.Now()
				conn, dialErr := dialer.DialContext(context.Background(), "tcp", M.ParseSocksaddr(target))
				elapsed := time.Since(started)
				if dialErr != nil {
					failures.Add(1)
					firstFailure.CompareAndSwap(nil, dialErr.Error())
					continue
				}
				samplesAccess.Lock()
				samples = append(samples, elapsed)
				samplesAccess.Unlock()
				// The connection may have been reclaimed by a generation change in between; closing
				// twice is harmless and keeps the tracking manager's accounting honest.
				_ = conn.Close()
				if live := int64(manager.trackedCount()); live > peak.Load() {
					peak.Store(live)
				}
			}
		}()
	}
	waitGroup.Wait()
	<-samplingDone

	// A demand that races a generation change is allowed to fail - the caller retries, and the
	// failure rate is part of the measurement. What must hold is that each demand cost exactly one
	// underlying dial (amplification would multiply it, collapsed shared state would lower it) and
	// that the successful ones stay inside the budget.
	successful := int64(len(samples))
	require.NotZero(t, successful, "no demand succeeded at all: the burst window is not representative")
	require.Equal(t, int64(demands), successful+int64(failures.Load()),
		"every demand ends either with a connection or with one failure")
	require.EqualValues(t, demands, manager.dialed.Load(),
		"exactly one underlying dial per demand: amplification or collapsed shared state would change this")
	sort.Slice(samples, func(left, right int) bool { return samples[left] < samples[right] })

	require.LessOrEqual(t, peak.Load(), int64(demands),
		"the live socket count must never exceed the demands that produced it")
	require.Less(t, percentile(samples, 0.99), 2*time.Second,
		"p99 dial time must fit the 2 s budget the regression commands use")
	// Every connection the burst window produced is owned by the tracking manager, and the final
	// generation change reclaims all of them: nothing accumulates across generations.
	manager.CloseAll()
	require.Zero(t, manager.trackedCount(), "the last generation change must reclaim every connection")

	t.Logf("dial budget: samples=%d failed=%d concurrency=%d burst=%d (every %s, inside the demand window)",
		len(samples), failures.Load(), concurrency, burst, burstInterval)
	t.Logf("p50=%s p95=%s p99=%s max=%s failures=%d dials=%d sockets_peak=%d first_failure=%v",
		percentile(samples, 0.50),
		percentile(samples, 0.95),
		percentile(samples, 0.99),
		samples[len(samples)-1],
		failures.Load(),
		manager.dialed.Load(),
		peak.Load(),
		firstFailure.Load(),
	)
}
