package buildgate

// Q20: the reservation's derivation, budget, and the paths that must return a slot.
//
// These tests drive the LEVEL directly, which is the only thing the derivation reads. They are not
// a second copy of resourceguard's attack tests: they pin what a BUILD needs from it - a deferral
// that is retryable and counted, a slot returned on every exit path, and a budget that states which
// part is a proven count and which part is deliberately not claimed.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"

	"github.com/stretchr/testify/require"
)

// levelSource is a pressure function the test drives.
type levelSource struct {
	access sync.Mutex
	level  resourceguard.Level
}

func (s *levelSource) set(level resourceguard.Level) {
	s.access.Lock()
	s.level = level
	s.access.Unlock()
}

func (s *levelSource) get() resourceguard.Level {
	s.access.Lock()
	defer s.access.Unlock()
	return s.level
}

func newPressureGate(t *testing.T, limit int, queue int, wait time.Duration) (*Gate, *levelSource) {
	t.Helper()
	source := &levelSource{}
	gate := NewWithPressure(resourceguard.Config{
		Limit:       limit,
		Queue:       queue,
		Wait:        wait,
		DemoteAfter: time.Millisecond,
	}, source.get)
	return gate, source
}

// TestTheBuildLimitFollowsTheProcessPressureLevel is the core of Q20.
//
// At LevelNormal the reservation admits its configured limit. At LevelCritical the derived limit is
// ONE build - which is the degradation the audit asks for ("not just a different constant"): the
// same object that admits two concurrent builds under no pressure admits a single one when the
// process is in crisis, and a second build is deferred rather than queued.
func TestTheBuildLimitFollowsTheProcessPressureLevel(t *testing.T) {
	gate, source := newPressureGate(t, 2, 0, 20*time.Millisecond)

	firstRelease, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	secondRelease, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, gate.Stats().Inflight)

	// The process enters crisis. The already-admitted builds are NOT killed: they hold their
	// permits, and the level only governs what may START.
	source.set(resourceguard.LevelCritical)
	require.Equal(t, resourceguard.LevelCritical, gate.Level())

	_, err = gate.Acquire(context.Background())
	require.ErrorIs(t, err, ErrDeferred, "a third build must be deferred even at the normal limit")
	firstRelease()
	secondRelease()

	criticalRelease, err := gate.Acquire(context.Background())
	require.NoError(t, err, "crisis still admits ONE build: the old version may be unreplaceable")
	require.Equal(t, 1, gate.Stats().Limit, "the applied limit must be the critical derivation")

	_, err = gate.Acquire(context.Background())
	require.ErrorIs(t, err, ErrDeferred, "critical pressure admitted a second build")
	criticalRelease()

	// Recovery: the level drops and the normal limit comes back. This is the "鍘嬪姏鏈熼棿寤舵湡/鎭㈠"
	// half - a deferral must not be permanent.
	source.set(resourceguard.LevelNormal)
	require.Eventually(t, func() bool { return gate.Level() == resourceguard.LevelNormal },
		time.Second, time.Millisecond, "the reservation never demoted after pressure fell")
	require.Equal(t, 2, gate.Stats().Limit)
	recovered, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	recovered()
}

// TestADeferralIsRetryableAndCounted pins the contract a provider's retry relies on.
func TestADeferralIsRetryableAndCounted(t *testing.T) {
	gate, _ := newPressureGate(t, 1, 0, time.Millisecond)
	release, err := gate.Acquire(context.Background())
	require.NoError(t, err)

	_, err = gate.Acquire(context.Background())
	require.ErrorIs(t, err, ErrDeferred)
	require.EqualValues(t, 1, gate.Stats().Deferred)
	require.EqualValues(t, 1, gate.Stats().AdmittedBuilds)

	release()

	// The retry succeeds: deferred was pressure, not a broken document.
	releaseAgain, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 2, gate.Stats().AdmittedBuilds)
	releaseAgain()
}

// TestTheSlotIsReturnedOnFailureAndCancellation covers the release paths a build has.
//
// A build that fails after admission still must give the slot back (its release is deferred), and a
// caller whose context dies must never end up holding one. Both are asserted on the counter, not on
// timing: `Admitted - Released == Inflight` is the invariant, and it must hold after each exit.
func TestTheSlotIsReturnedOnFailureAndCancellation(t *testing.T) {
	gate, _ := newPressureGate(t, 1, 1, 50*time.Millisecond)

	// Failure path: acquire, then release as a failed build would.
	release, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	release()
	stats := gate.Stats()
	require.Zero(t, stats.Inflight, "a failed build leaked its slot")
	require.Equal(t, int(stats.Admitted-stats.Released), stats.Inflight)

	// Cancellation while queued: the second acquire waits behind the first and its caller dies.
	holdRelease, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	cancelledContext, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() {
		_, acquireErr := gate.Acquire(cancelledContext)
		queued <- acquireErr
	}()
	require.Eventually(t, func() bool { return gate.Stats().Waiters == 1 },
		time.Second, time.Millisecond, "the second build never queued")
	cancel()
	require.ErrorIs(t, <-queued, context.Canceled)
	holdRelease()

	stats = gate.Stats()
	require.Zero(t, stats.Inflight)
	require.Zero(t, stats.Waiters)
	require.Equal(t, int(stats.Admitted-stats.Released), stats.Inflight)
	require.EqualValues(t, 1, stats.Canceled)
}

// TestMultipleBuildersCompeteWithoutExceedingTheLimit is the contention case: several providers
// refresh at once, and the reservation must bound the concurrent builds rather than the number of
// callers.
func TestMultipleBuildersCompeteWithoutExceedingTheLimit(t *testing.T) {
	gate, _ := newPressureGate(t, 2, 8, time.Second)
	var (
		access   sync.Mutex
		peak     int
		active   int
		deferred int
		waiting  sync.WaitGroup
	)
	waiting.Add(8)
	for i := 0; i < 8; i++ {
		go func() {
			defer waiting.Done()
			release, err := gate.Acquire(context.Background())
			if err != nil {
				require.True(t, errors.Is(err, ErrDeferred), "unexpected error: %v", err)
				access.Lock()
				deferred++
				access.Unlock()
				return
			}
			access.Lock()
			active++
			if active > peak {
				peak = active
			}
			access.Unlock()
			time.Sleep(5 * time.Millisecond)
			access.Lock()
			active--
			access.Unlock()
			release()
		}()
	}
	waiting.Wait()
	require.LessOrEqual(t, peak, 2, "the reservation admitted more builds at once than its limit")
	stats := gate.Stats()
	require.Equal(t, int(stats.Admitted-stats.Released), stats.Inflight)
	require.Zero(t, stats.Inflight, "a completed builder left its slot behind")
	require.LessOrEqual(t, stats.MaxInflight, 2)
}

// TestTheBudgetStatesTheCountableBoundAndNoByteClaim is Q20's "budget with an honest boundary".
//
// The counts must be the limit actually applied, and the byte claim must be FALSE: an estimate that
// cannot be proven is reported as absent, never as a bound.
func TestTheBudgetStatesTheCountableBoundAndNoByteClaim(t *testing.T) {
	gate, source := newPressureGate(t, 2, 4, time.Second)

	budget := gate.Budget()
	require.Equal(t, resourceguard.LevelNormal, budget.Level)
	require.Equal(t, 2, budget.Limit)
	require.Equal(t, 2, budget.MaxOverlappingBuilds)
	require.Equal(t, 2, budget.MaxOldDocuments, "each build can hold one old document")
	require.Equal(t, 2, budget.MaxNewDocuments)
	require.False(t, budget.ByteBoundKnown,
		"a byte bound was claimed: the document size is only known after download and the index "+
			"is built inside a dependency")
	require.NotEmpty(t, budget.Note)

	source.set(resourceguard.LevelCritical)
	budget = gate.Budget()
	require.Equal(t, resourceguard.LevelCritical, budget.Level)
	require.Equal(t, 1, budget.MaxOverlappingBuilds,
		"the budget did not report the degradation the level applies")
	require.False(t, budget.ByteBoundKnown)
}

// TestNoPressureFunctionMeansNoDerivation keeps an embedder's reservation exactly as it was.
func TestNoPressureFunctionMeansNoDerivation(t *testing.T) {
	gate := NewWithPressure(resourceguard.Config{Limit: 2, Queue: 0, Wait: time.Millisecond}, nil)
	require.Equal(t, resourceguard.LevelNormal, gate.Level())
	release, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	secondRelease, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	release()
	secondRelease()
}
