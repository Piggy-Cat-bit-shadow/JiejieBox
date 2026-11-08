package resourceguard

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The admission load definition.
//
// # Why this is a test and not a benchmark
//
// A benchmark reports one number (ns/op) and hides the shape that matters here: how many callers were
// admitted, how long the QUEUED ones waited, and how deep in flight the gate actually got. The
// failure this package exists for is a burst that parks hundreds of goroutines, so the replayable
// load has to state the burst size, the work per admitted unit and the resulting wait distribution.
// The parameters are constants so a later change is measured against the same load, not against a
// remembered one.
//
// Run it explicitly and keep the logged line:
//
//	go test ./common/resourceguard -run 'TestAdmissionLoadDefinition' -v -count=1
//
// It is deliberately not a performance claim: it measures the gate's own bookkeeping on the host that
// runs it, with no network and no protocol.
const (
	admissionLoadBurst = 192
	// admissionLoadWork is how long one admitted unit holds its permit. It stands in for the TLS
	// handshake / session setup an expensive creation performs; 2 ms keeps a 192-caller burst under a
	// second while still forcing the queue to form.
	admissionLoadWork = 2 * time.Millisecond
)

// TestAdmissionLoadDefinition replays the observed-shape burst: more callers than permits, fewer than
// limit+queue, so every caller should be admitted after waiting rather than refused.
func TestAdmissionLoadDefinition(t *testing.T) {
	config := DefaultNetworkExtensionConfig()
	guard := NewGuard(config, func() Level { return LevelNormal })

	var (
		access  sync.Mutex
		waits   []time.Duration
		refused atomic.Int64
	)

	started := time.Now()
	var group sync.WaitGroup
	group.Add(admissionLoadBurst)
	for index := 0; index < admissionLoadBurst; index++ {
		go func() {
			defer group.Done()
			permit, err := guard.Acquire(context.Background(), ClassNormal)
			if err != nil {
				refused.Add(1)
				return
			}
			waited := permit.Wait()
			access.Lock()
			waits = append(waits, waited)
			access.Unlock()
			time.Sleep(admissionLoadWork)
			permit.Release()
		}()
	}
	group.Wait()
	elapsed := time.Since(started)

	stats := guard.Stats()
	require.Zero(t, refused.Load(), "a burst below limit+queue must be absorbed, not refused")
	require.Equal(t, uint64(admissionLoadBurst), stats.Gate.Admitted)
	require.Equal(t, uint64(admissionLoadBurst), stats.Gate.Released)
	require.LessOrEqual(t, stats.Gate.MaxInflight, config.Limit+config.InteractiveReserve,
		"the absolute ceiling must hold under a burst")

	sort.Slice(waits, func(i int, j int) bool { return waits[i] < waits[j] })
	t.Logf("LOAD admission burst: callers=%d limit=%d queue=%d work=%s admitted=%d refused=%d maxInflight=%d p50=%s p95=%s total=%s",
		admissionLoadBurst, config.Limit, config.Queue, admissionLoadWork, stats.Gate.Admitted,
		stats.Gate.Refused, stats.Gate.MaxInflight, percentile(waits, 0.50), percentile(waits, 0.95), elapsed)
}

// TestAdmissionOverloadLoadDefinition replays the refusal shape: more callers than limit+queue, so
// the excess must be refused in constant time instead of parked. That constant-time property is the
// reason the queue is explicit; a plain semaphore would have blocked every excess caller.
func TestAdmissionOverloadLoadDefinition(t *testing.T) {
	config := Config{
		Limit:           8,
		Queue:           16,
		Wait:            5 * time.Second,
		DemoteAfter:     time.Second,
		StarvationAfter: time.Second,
	}
	guard := NewGuard(config, func() Level { return LevelNormal })
	const burst = 128

	var (
		admitted atomic.Int64
		refused  atomic.Int64
	)

	started := time.Now()
	var group sync.WaitGroup
	group.Add(burst)
	for index := 0; index < burst; index++ {
		go func() {
			defer group.Done()
			permit, err := guard.Acquire(context.Background(), ClassNormal)
			if err != nil {
				refused.Add(1)
				return
			}
			admitted.Add(1)
			time.Sleep(admissionLoadWork / 4)
			permit.Release()
		}()
	}
	group.Wait()
	elapsed := time.Since(started)

	stats := guard.Stats()
	require.Equal(t, int64(burst), admitted.Load()+refused.Load())
	require.Positive(t, refused.Load(), "a burst beyond limit+queue must produce refusals")
	require.LessOrEqual(t, stats.Gate.MaxInflight, config.Limit)
	require.Equal(t, uint64(refused.Load()), stats.Gate.Refused,
		"every ErrQueueFull must be counted exactly once")
	t.Logf("LOAD admission overload: callers=%d limit=%d queue=%d admitted=%d refused=%d maxInflight=%d total=%s",
		burst, config.Limit, config.Queue, admitted.Load(), refused.Load(), stats.Gate.MaxInflight, elapsed)
}

// percentile returns the nearest-rank percentile of a sorted slice, or zero when it is empty.
func percentile(sorted []time.Duration, fraction float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(float64(len(sorted)) * fraction)
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// BenchmarkGateAcquireRelease measures the uncontended admission bookkeeping - the cost every
// expensive dial pays when the gate admits immediately. It exists so an observation change to the
// gate (a counter, a timestamp) has a number to be compared against instead of an opinion.
func BenchmarkGateAcquireRelease(b *testing.B) {
	guard := NewGuard(DefaultNetworkExtensionConfig(), func() Level { return LevelNormal })
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			permit, err := guard.Acquire(context.Background(), ClassNormal)
			if err != nil {
				b.Fatal(err)
			}
			permit.Release()
		}
	})
}

// BenchmarkGateAcquireReleaseContended measures the queued path: every caller after the first must
// park on its own channel, be handed a slot and release it. It is the shape a burst pays.
func BenchmarkGateAcquireReleaseContended(b *testing.B) {
	guard := NewGuard(Config{
		Limit:           1,
		Queue:           1024,
		Wait:            5 * time.Second,
		DemoteAfter:     time.Second,
		StarvationAfter: time.Second,
	}, func() Level { return LevelNormal })
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			permit, err := guard.Acquire(context.Background(), ClassNormal)
			if err != nil {
				b.Fatal(err)
			}
			permit.Release()
		}
	})
}
