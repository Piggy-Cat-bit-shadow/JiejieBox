package daemon

// C04 — the planner and the live apply touch the same fields, and a review reproduction found
// them doing so unsynchronised.
//
// `PlanServiceChange` reads the running document and its revision; a live-only apply REPLACES both
// on the same instance. A planner that held a pointer into the instance therefore read a document
// from one generation and a revision from another - and did it concurrently with the write. The
// planner now takes a copy under the lock, and a plan that names another generation is refused
// instead of applied to the wrong Box.
//
// This test is the interleaving itself: planners hammer the read path while reloads toggle the mode
// through the real entry. It is meant to be run with -race; without the fix the race detector
// reports the unsynchronised read/write of Instance.options.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlanningWhileALiveChangeCommitsIsRaceFree(t *testing.T) {
	service := startedForChange(t, modeConfig("Global"))
	globalRevision := configRevisionOf(modeConfig("Global"))
	directRevision := configRevisionOf(modeConfig("Direct"))

	var stop atomic.Bool
	var planCount atomic.Int64
	var badSnapshot atomic.Value

	var planners sync.WaitGroup
	for range 4 {
		planners.Add(1)
		go func() {
			defer planners.Done()
			for !stop.Load() {
				plan, err := service.PlanServiceChange(modeConfig("Direct"), nil)
				if err != nil {
					badSnapshot.Store("plan error: " + err.Error())
					return
				}
				planCount.Add(1)
				if !plan.Started {
					continue
				}
				// The plan must describe ONE generation: a revision this service could have held,
				// with the domain answers computed from that same document.
				switch plan.CurrentRevision {
				case globalRevision, directRevision:
				default:
					badSnapshot.Store("a plan named a revision that was never running: " + plan.CurrentRevision)
					return
				}
			}
		}()
	}

	// The committer toggles the mode through the real entry, which is the live-apply path.
	committer := sync.WaitGroup{}
	committer.Add(1)
	go func() {
		defer committer.Done()
		targets := []string{modeConfig("Direct"), modeConfig("Global")}
		for index := 0; index < 32; index++ {
			if err := service.StartOrReloadService(context.Background(), targets[index%2], nil); err != nil {
				badSnapshot.Store("reload error: " + err.Error())
				return
			}
		}
		stop.Store(true)
	}()

	committer.Wait()
	planners.Wait()

	if failure, loaded := badSnapshot.Load().(string); loaded && failure != "" {
		require.Fail(t, failure)
	}
	require.Positive(t, planCount.Load(), "the planners must have produced plans during the interleaving")
	require.NotNil(t, service.Instance(), "the service is still running after the interleaving")
	// The last successful reload left the mode it asked for; the invariant is that the plan's
	// revision still names the running document.
	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Contains(t, []string{globalRevision, directRevision}, report.Plan.TargetRevision)
}
