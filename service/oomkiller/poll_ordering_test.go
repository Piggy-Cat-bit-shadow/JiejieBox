package oomkiller

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-tun"

	"github.com/stretchr/testify/require"
)

// The published pressure must be an ordering, not a race between pollers.
//
// # Why this interleaving matters
//
// poll() takes a sequence number BEFORE it reads the sample. Two polls can then read in either
// order: the slower one may observe the process while it was still small, be descheduled, and reach
// the publication point after the faster one already published the crisis. Applying the older
// observation would walk the pressure fact - and through it the admission plan - backwards while the
// measurement that justified the crisis is still the newest thing the process knows.
//
// The test drives that exact order with the sample function as the pause point (it is called before
// the lifecycle lock, which is what makes the window reachable in production), then asserts that the
// stale poll published nothing.

func newOrderingTimer(t *testing.T, pressure *atomic.Uint32) *adaptiveTimer {
	t.Helper()
	config := timerConfig{
		policyMode:      policyModeMemoryLimit,
		minInterval:     time.Second,
		maxInterval:     10 * time.Second,
		memoryLimit:     50 * 1024 * 1024,
		safetyMargin:    5 * 1024 * 1024,
		hasSafetyMargin: true,
	}
	timer := &adaptiveTimer{
		timerConfig:     config,
		logger:          log.NewNOPFactory().Logger(),
		limitThresholds: computeLimitThresholds(config.memoryLimit, config.safetyMargin),
		timer:           time.NewTimer(time.Hour),
		pressure:        pressure,
	}
	t.Cleanup(func() { timer.timer.Stop() })
	return timer
}

func TestStalePollCannotPublishAnOlderObservation(t *testing.T) {
	pressure := &atomic.Uint32{}
	timer := newOrderingTimer(t, pressure)
	timer.state = pressureStateNormal

	var usage atomic.Uint64
	usage.Store((timer.limitThresholds.trigger + 1024*1024) / (1024 * 1024)) // MiB, crisis-sized

	var calls atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	timer.sampleFunc = func() memorySample {
		sampled := usage.Load()
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		return idleSample(sampled)
	}

	staleDone := make(chan struct{})
	go func() {
		defer close(staleDone)
		timer.poll()
	}()
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("the first poll never read its sample")
	}

	// The newer poll sees a small process and publishes it. Normal is already the state, so this
	// poll only has to establish itself as the newest observation.
	usage.Store(10)
	timer.poll()
	require.Equal(t, uint32(tun.MemoryPressureNone), pressure.Load())

	// The older, crisis-sized observation is released and reaches its publication point late.
	close(releaseFirst)
	select {
	case <-staleDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the stale poll never finished")
	}

	require.Equal(t, uint32(tun.MemoryPressureNone), pressure.Load(),
		"a poll that started before the newest observation was allowed to publish after it")
	require.Equal(t, pressureStateNormal, timer.state)
}

// TestCriticalPublicationFromReclaimIsCorrectedByTheNextMeasuredPoll bounds the one deliberate
// exception: a reclaim pass publishes Critical without a fresh threshold measurement.
//
// # Why it is bounded rather than forbidden
//
// The Darwin pressure path (runPressureReclaim) and the crisis path both publish Critical from inside
// the destructive action, after the decision was taken on a real signal. That is deliberate
// backpressure during reclaim. What must NOT happen is that it survives a newer measurement: the next
// poll is authoritative, and this test proves the correction happens on that poll rather than needing
// a restart or a second crisis.
func TestCriticalPublicationFromReclaimIsCorrectedByTheNextMeasuredPoll(t *testing.T) {
	pressure := &atomic.Uint32{}
	timer := newOrderingTimer(t, pressure)
	usage := atomic.Uint64{}
	usage.Store(41) // MiB, between armed (40) and trigger (45)
	timer.sampleFunc = func() memorySample { return idleSample(usage.Load()) }

	timer.poll()
	require.Equal(t, uint32(tun.MemoryPressureWarning), pressure.Load())

	// The reclaim pass publishes the crisis-level fact it is acting under.
	timer.releaseMemory()
	require.Equal(t, uint32(tun.MemoryPressureCritical), pressure.Load())

	// The next measured poll sees a process that is still only armed, and must restore Warning.
	timer.poll()
	require.Equal(t, uint32(tun.MemoryPressureWarning), pressure.Load(),
		"a reclaim-time Critical survived the next measurement")
}

// TestCriticalPressureTightensTheRealGate is the end-to-end wiring check the debug brief asks for:
// the pressure the timer publishes must change what the admission gate actually does to a caller.
func TestCriticalPressureTightensTheRealGate(t *testing.T) {
	service := &Service{}
	guard := resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), service.guardLevel)
	service.guard = guard
	service.pressure.Store(uint32(tun.MemoryPressureNone))

	held := make([]*resourceguard.Permit, 0, 8)
	for index := 0; index < 8; index++ {
		permit, err := guard.Acquire(context.Background(), resourceguard.ClassNormal)
		require.NoError(t, err)
		held = append(held, permit)
	}

	service.pressure.Store(uint32(tun.MemoryPressureCritical))

	started := time.Now()
	_, err := guard.Acquire(context.Background(), resourceguard.ClassNormal)
	elapsed := time.Since(started)
	require.Error(t, err, "critical pressure did not change the admission decision")
	require.ErrorIs(t, err, resourceguard.ErrWaitTimeout,
		"at critical the gate waits one short budget and then refuses; got %v", err)
	require.Less(t, elapsed, time.Second)

	stats := guard.Stats()
	require.Equal(t, resourceguard.LevelCritical, stats.Level)
	require.Equal(t, 8, stats.Gate.Limit, "the gate is admitting at the critical limit")
	require.Equal(t, 8, stats.Gate.Inflight)
	require.Equal(t, 8, stats.Gate.MaxInflight)

	for _, permit := range held {
		permit.Release()
	}
	require.Zero(t, guard.Stats().Gate.Inflight)
}
