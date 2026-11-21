package outbound

// LN long loop: five equivalent batches of expand / evict / rebuild / replace / remove, with an exact
// ownership ledger and a per-batch resource sample.
//
// # Why a ledger and not a memory threshold
//
// The defects a lifecycle loop produces are not "the heap is 3% higher": they are a cleanup that ran
// twice, an expansion that was published and never counted, or a close that skipped a scope. Those are
// all COUNTED facts, so the loop asserts an identity after every round:
//
//	builds == cleanupRuns + expansionsThatAreAliveRightNow
//
// i.e. every construction either still owns its expansion or has had its scope drained exactly once.
// The direct-close path (an object that was never started) must stay at zero, because a build that
// really ran Start and was then closed directly would be the double-release defect in the other
// direction. `pending`, the demand trigger's counter, must equal the live expansion count.
//
// The resource samples are the SLOPE evidence: goroutines, process handles and heap objects at every
// batch boundary, after a fold of the released runtimes so the fixture itself does not retain them.
// A steady state looks flat; a leak looks like a positive slope that the ledger identity does not
// explain. The absolute numbers are platform facts and are logged, not asserted.

import (
	"context"
	"fmt"
	"runtime"
	"testing"

	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// churnLedger folds the counters of released runtimes, so the fixture keeps a reference only to
// expansions that are still alive. Without the fold the test itself would keep every runtime of the
// loop reachable and the heap sample would measure the fixture, not the product.
type churnLedger struct {
	init    int64
	cleanup int64
	close   int64
}

func (l *churnLedger) fold(fixture *attackFixture) {
	fixture.access.Lock()
	defer fixture.access.Unlock()
	kept := fixture.runtimes[:0]
	for _, runtimeObject := range fixture.runtimes {
		released := runtimeObject.cleanupRuns.Load() > 0 || runtimeObject.closeCalls.Load() > 0
		if released {
			l.init += int64(runtimeObject.initCalls.Load())
			l.cleanup += int64(runtimeObject.cleanupRuns.Load())
			l.close += int64(runtimeObject.closeCalls.Load())
			continue
		}
		kept = append(kept, runtimeObject)
	}
	fixture.runtimes = kept
}

// totals is the ledger as of now: the folded half plus the runtimes still retained by the fixture.
func (l churnLedger) totals(fixture *attackFixture) churnLedger {
	total := l
	fixture.access.Lock()
	for _, runtimeObject := range fixture.runtimes {
		total.init += int64(runtimeObject.initCalls.Load())
		total.cleanup += int64(runtimeObject.cleanupRuns.Load())
		total.close += int64(runtimeObject.closeCalls.Load())
	}
	fixture.access.Unlock()
	return total
}

// sampleResources reports the batch's resource facts. Handles and goroutines are read BEFORE the
// forced collection, because a collection is itself allowed to create runtime threads (and therefore
// thread handles in the process table) - sampling after one would record the measurement's own cost as
// the product's. The heap sample is taken after two collections so it is a live-object count.
func sampleResources() (goroutines int, handles uint32, handlesMeasured bool, heapObjects uint64) {
	handles, handlesMeasured = processHandleCount()
	goroutines = runtime.NumGoroutine()
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return goroutines, handles, handlesMeasured, stats.HeapObjects
}

// TestAttackLifecycleChurnLedgerAcrossFiveBatches is the LN long loop: 5 x 200 rounds of the full
// lifecycle, with the ledger asserted after every round and the resource slope sampled per batch.
func TestAttackLifecycleChurnLedgerAcrossFiveBatches(t *testing.T) {
	const (
		batches        = 5
		roundsPerBatch = 200
		poolSize       = 8
	)
	ctx := context.Background()
	logger := log.NewNOPFactory().NewLogger("churn")

	fixture := newAttackFixture(t)
	fixture.startAll(t)
	// The policy stays permissive except where the loop deliberately sweeps, so an async sweep can
	// never fire in the middle of a round and make the ledger non-deterministic.
	fixture.manager.SetDeferredReclaimPolicy(attackBudgetPolicy(1 << 20))

	ledger := churnLedger{}
	var (
		batchBuilds int64
	)

	// Warm the runtime's own thread pool: the first collections may create runtime threads, and a
	// thread handle is a process handle. Taking the baseline AFTER that keeps the measurement's own
	// cost out of the slope; handles are then read BEFORE each batch's collection for the same reason.
	runtime.GC()
	runtime.GC()
	baselineGoroutines := runtime.NumGoroutine()
	baselineHandles, handlesSeen := processHandleCount()

	checkLedger := func(where string) {
		t.Helper()
		totals := ledger.totals(fixture)
		builds := int64(fixture.builds.Load())
		expanded := int64(fixture.manager.DeferredExpansionCount())
		require.Equal(t, builds, totals.init, "%s: every construction must reach Start", where)
		require.Equal(t, builds, totals.cleanup+expanded,
			"%s: every expansion is either alive or has had its scope drained exactly once", where)
		require.EqualValues(t, 0, totals.close,
			"%s: a build that ran Start must never also be closed directly", where)
		require.EqualValues(t, expanded, fixture.pending(),
			"%s: the demand trigger's counter must equal the live expansion count", where)
		fixture.access.Lock()
		live := len(fixture.runtimes)
		fixture.access.Unlock()
		require.LessOrEqual(t, live, poolSize+1,
			"%s: the fixture must not be the thing that retains released runtimes", where)
	}

	for batch := 0; batch < batches; batch++ {
		buildsAtBatchStart := int64(fixture.builds.Load())
		for round := 0; round < roundsPerBatch; round++ {
			iteration := batch*roundsPerBatch + round
			tag := fmt.Sprintf("pool-%02d", iteration%poolSize)

			// A provider publication: create or replace the tag, then complete the barrier.
			require.NoError(t, fixture.manager.ReplaceOwned(ctx, nil, logger, tag, "attack", nil, "provider"))
			require.NoError(t, fixture.manager.CompletePublication("provider", []string{tag}))

			// The real hand-off chain a router uses: obtain, lease, dial, release.
			object, loaded := fixture.manager.Outbound(tag)
			require.True(t, loaded)
			release, leased := fixture.manager.AcquireOutbound(object)
			require.True(t, leased)
			conn, err := object.DialContext(ctx, "tcp", M.Socksaddr{})
			require.NoError(t, err)
			require.NoError(t, conn.Close())
			release()

			deferred := object.(*LazyOutbound[string])

			// Evict and rebuild every other round: the recipe must always be able to restore the node.
			if iteration%2 == 0 {
				require.True(t, deferred.Evict(), "an idle expansion must be evictable")
				rebuildConn, rebuildErr := deferred.DialContext(ctx, "tcp", M.Socksaddr{})
				require.NoError(t, rebuildErr, "an evicted node must rebuild on demand")
				require.NoError(t, rebuildConn.Close())
			}

			// Every twentieth round, run the POLICY path instead of an explicit eviction.
			if iteration%20 == 0 {
				fixture.manager.SetDeferredReclaimPolicy(attackBudgetPolicy(2))
				result := fixture.manager.ReclaimDeferredExpansions(ctx, ReclaimCauseExplicit)
				require.Zero(t, result.InUse, "nothing is leased at a round boundary")
				require.LessOrEqual(t, fixture.manager.DeferredExpansionCount(), 2,
					"the sweep must bring the live expansion count down to its budget")
				fixture.manager.SetDeferredReclaimPolicy(attackBudgetPolicy(1 << 20))
			}

			// Every fifth round, remove the tag entirely: the object is unlinked and closed.
			if iteration%5 == 0 {
				require.NoError(t, fixture.manager.Remove(tag))
			}

			// Fold every round: the fixture must keep a reference only to expansions that are still
			// alive, otherwise the live-object bound below would measure the test's own retention.
			ledger.fold(fixture)
			checkLedger(fmt.Sprintf("batch %d round %d", batch, round))
		}

		ledger.fold(fixture)
		checkLedger(fmt.Sprintf("batch %d boundary", batch))
		batchBuilds = int64(fixture.builds.Load()) - buildsAtBatchStart

		goroutines, handles, measured, heapObjects := sampleResources()
		handleText := "n/a"
		if measured {
			handleText = fmt.Sprintf("%d", handles)
		}
		fmt.Printf("L-STRESS batch=%d rounds=%d builds=%d expanded=%d pending=%d goroutines=%d handles=%s heapObjects=%d\n", batch, roundsPerBatch, batchBuilds, fixture.manager.DeferredExpansionCount(), fixture.pending(), goroutines, handleText, heapObjects)

		require.Equal(t, batchBuilds, int64(roundsPerBatch)+int64(roundsPerBatch/2),
			"batch %d is not equivalent to the others: the same operation sequence must produce the "+
				"same number of constructions", batch)
		require.LessOrEqual(t, goroutines, baselineGoroutines+4,
			"batch %d leaked goroutines: %d vs %d at the baseline", batch, goroutines, baselineGoroutines)
		if measured && handlesSeen {
			require.LessOrEqual(t, handles, baselineHandles+32,
				"batch %d leaked OS handles: %d vs %d at the baseline", batch, handles, baselineHandles)
		}
	}

	// Drain every pool tag and close the books: an empty namespace must leave nothing expanded, no
	// pending publishes and no expansion that was released without running its cleanup.
	for index := 0; index < poolSize; index++ {
		tag := fmt.Sprintf("pool-%02d", index)
		if _, loaded := fixture.manager.Outbound(tag); loaded {
			require.NoError(t, fixture.manager.Remove(tag))
		}
	}
	ledger.fold(fixture)
	totals := ledger.totals(fixture)
	require.Zero(t, fixture.manager.DeferredExpansionCount(), "the drained namespace has no expansion")
	require.Zero(t, fixture.pending(), "the drained namespace has no uncounted publish")
	require.Equal(t, int64(fixture.builds.Load()), totals.cleanup,
		"after the drain every construction has run its cleanup exactly once")
	require.EqualValues(t, 0, totals.close)

	t.Logf("L-STRESS-TOTAL rounds=%d builds=%d cleanups=%d foldedClose=%d expanded=0 pending=0",
		batches*roundsPerBatch, fixture.builds.Load(), totals.cleanup, totals.close)
}
