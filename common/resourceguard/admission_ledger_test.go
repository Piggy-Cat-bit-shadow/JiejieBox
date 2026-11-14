package resourceguard

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/trafficclass"

	"github.com/stretchr/testify/require"
)

// The shared admission body must settle its permit on every ending, including the ones a protocol
// author has no reason to think about.
//
// Round 1 relies on Guard.AdmitCreation for AnyTLS, the QUIC clients, TrustTunnel, Mieru and
// transport.type=quic. Each of them hands it a constructor that can fail, be cancelled, or panic
// (a nil dereference inside a protocol is still a bug, but it must not also leak one of the 32
// process-wide creation slots into permanence).

// TestAdmitCreationReleasesThePermitWhenTheCreatePanics pins the panic path.
func TestAdmitCreationReleasesThePermitWhenTheCreatePanics(t *testing.T) {
	guard := NewGuard(Config{Limit: 1, Queue: 0, Wait: time.Second}, nil)

	panicked := func() (didPanic bool) {
		defer func() {
			didPanic = recover() != nil
		}()
		_ = guard.AdmitCreation(context.Background(), "panic-key", func(context.Context) error {
			panic("constructor failure")
		})
		return false
	}()
	require.True(t, panicked, "the fixture must actually panic")

	stats := guard.Stats().Gate
	require.EqualValues(t, 1, stats.Admitted)
	require.EqualValues(t, 1, stats.Released, "a panicking creation leaked its permit")
	require.Zero(t, stats.Inflight)

	// And the slot is usable again: a gate that leaked here would refuse everything after the panic.
	permit, err := guard.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)
	permit.Release()
}

// TestAdmitCreationRefusalDoesNotRunTheConstructor keeps the expensive work on the admitted side of
// the gate. A refusal that still dialled would make the bound decorative.
func TestAdmitCreationRefusalDoesNotRunTheConstructor(t *testing.T) {
	guard := NewGuard(Config{Limit: 1, Queue: 0, Wait: time.Second}, nil)
	held, err := guard.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)
	defer held.Release()

	var created atomic.Int32
	err = guard.AdmitCreation(context.Background(), "refused-key", func(context.Context) error {
		created.Add(1)
		return nil
	})
	require.ErrorIs(t, err, ErrQueueFull)
	require.Zero(t, created.Load(), "the constructor ran even though the gate refused the creation")

	stats := guard.Stats().Gate
	require.EqualValues(t, 0, stats.Released, "a refused creation must not be counted as released")
	require.Equal(t, uint64(1), stats.Admitted, "only the externally held permit was admitted")
}

// TestAdmitCreationDoubleReleaseIsInert pins the idempotence the exactly-once accounting depends on:
// a protocol that calls Release twice (a defer plus an explicit call) must not return two slots.
func TestAdmitCreationDoubleReleaseIsInert(t *testing.T) {
	guard := NewGuard(Config{Limit: 1, Queue: 0, Wait: time.Second}, nil)

	permit, err := guard.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)
	permit.Release()
	permit.Release()

	stats := guard.Stats().Gate
	require.EqualValues(t, 1, stats.Released, "a double release was counted twice")
	require.Zero(t, stats.Inflight)

	// One slot is still available: a double release must not have created an extra one.
	second, err := guard.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)
	second.Release()
}

// TestAdmitCreationCarriesTheCallersClass pins the class hand-off: an interactive flow waiting on a
// background creation must be able to use the reserve, which is the reason the class is read from
// the context rather than assumed.
func TestAdmitCreationCarriesTheCallersClass(t *testing.T) {
	guard := NewGuard(Config{Limit: 1, Queue: 0, Wait: time.Second, InteractiveReserve: 1}, nil)
	held, err := guard.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)
	defer held.Release()

	interactiveCtx := trafficclass.WithClass(context.Background(), trafficclass.ClassInteractive)
	err = guard.AdmitCreation(interactiveCtx, "interactive-key", func(context.Context) error { return nil })
	require.NoError(t, err, "an interactive creation must be able to use the reserve")

	// The control: the same call without the class is refused.
	err = guard.AdmitCreation(context.Background(), "normal-key", func(context.Context) error { return nil })
	require.ErrorIs(t, err, ErrQueueFull)
}

// TestConcurrentSnapshotsKeepLevelAndLimitInStep is the stress half of the deterministic snapshot
// test: every snapshot a reader can obtain must describe ONE transition, whatever the churn.
func TestConcurrentSnapshotsKeepLevelAndLimitInStep(t *testing.T) {
	config := DefaultNetworkExtensionConfig()
	var pressure atomic.Uint32
	guard := NewGuard(config, func() Level { return Level(uint8(pressure.Load())) })

	const (
		readers = 8
		writers = 4
		rounds  = 300
	)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := 0; index < writers; index++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			<-start
			levels := []Level{LevelNormal, LevelArmed, LevelCritical}
			for round := 0; round < rounds; round++ {
				pressure.Store(uint32(levels[(round+offset)%len(levels)]))
				permit, err := guard.Acquire(context.Background(), ClassNormal)
				if err == nil {
					permit.Release()
				}
			}
		}(index)
	}
	var mismatch atomic.Int32
	for index := 0; index < readers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for round := 0; round < rounds; round++ {
				stats := guard.Stats()
				if stats.Gate.Limit != config.planFor(stats.Level).limit {
					mismatch.Add(1)
				}
			}
		}()
	}
	close(start)
	wg.Wait()

	require.Zero(t, mismatch.Load(),
		"a snapshot paired a level with the limit of a different transition under churn")
	final := guard.Stats()
	require.Zero(t, final.Gate.Inflight)
	require.Zero(t, final.Gate.Waiters)
}

// TestShedIsNotRunByAConcurrentSnapshotAfterTheFirst keeps the "one entry into critical is one shed"
// rule under a mixed reader/writer storm, not only on the single-threaded metrics path.
func TestShedIsNotRunByAConcurrentSnapshotAfterTheFirst(t *testing.T) {
	pressure := LevelCritical
	guard := NewGuard(DefaultNetworkExtensionConfig(), func() Level { return pressure })
	var sheds atomic.Int64
	guard.RegisterShed(func() { sheds.Add(1) })

	var wg sync.WaitGroup
	for index := 0; index < 16; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 100; round++ {
				_ = guard.Stats()
				permit, err := guard.Acquire(context.Background(), ClassNormal)
				if err == nil {
					permit.Release()
				}
			}
		}()
	}
	wg.Wait()

	require.EqualValues(t, 1, sheds.Load(),
		"concurrent snapshots and acquisitions re-ran the critical shed hooks")
}
