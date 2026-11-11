package libbox

// Section 5 recovery - a REAL Box with two remote providers and two URLTest groups, driven through
// controlled failure, republication, recovery and stable narrowing.
//
// The previous scale run answered "how expensive is a full manual round at 2,000 / 10,000 nodes".
// That is not recovery evidence: a cumulative probe counter cannot say WHICH window produced a
// probe, and the startup rounds alone can double it. This test drives one Box through named windows
// and reports, per window, its own probe delta, its own success count, the measured elapsed time,
// the selection facts (members/selected/stable/widened) and the queue and probe-concurrency peaks.
//
// The windows are:
//
//	manual-full     every member measured, both groups
//	failure         a quarter of the nodes point at a closed port; those probes fail
//	republication   both provider documents are replaced under the SAME tags
//	recovery        the original documents are restored; every member measures again
//	narrowing       periodic rounds with the failover policy installed: only the stable member is
//	                probed, and the probe set stays narrow
//	cancel-drain    a manual round is cancelled mid-flight; the next full round still succeeds and
//	                nothing is left running
//
// Opt-in: SING_BOX_RECOVERY_TEST=1 (SING_BOX_RECOVERY_NODES defaults to 20, so the semantic run is
// fast; the 2,000 / 10,000 runs use the same code with a larger node count).

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/protocol/group"

	"github.com/stretchr/testify/require"
)

// recoveryRoundRunner is the manual-round capability of a URLTest outbound.
type recoveryRoundRunner interface {
	URLTest(context.Context) (map[string]uint16, error)
}

// recoveryPeriodicRunner is the periodic-round capability (force=false), which is where the
// failover policy's probe set applies.
type recoveryPeriodicRunner interface {
	CheckOutboundsHealth(context.Context, bool)
}

// recoveryPolicyInstaller installs the stable/failover policy; the configuration mapping is a
// separate, try-merge patch, so this test enables it directly.
type recoveryPolicyInstaller interface {
	ApplyFailoverPolicy(urltest.FailoverPolicy)
}

// recoverySnapshotter samples the group's health and queue state.
type recoverySnapshotter interface {
	HealthSnapshot() group.HealthSnapshot
}

// recoveryQueueSampler samples ONLY the admission counters: HealthSnapshot builds the next probe
// set, which is O(members), and a per-millisecond sampler of a large member set would distort the
// round it measures.
type recoveryQueueSampler interface {
	QueueSnapshot() (inFlight int, queued int, limit int, sharedInFlight int, participants int)
}

// recoveryWindow is one named phase of the run.
type recoveryWindow struct {
	name       string
	probes     int32
	elapsed    time.Duration
	successes  []int
	snapshots  []group.HealthSnapshot
	peakFlight int
	peakQueued int
}

func (w recoveryWindow) log(t *testing.T) {
	t.Helper()
	t.Logf("window=%-13s probes=%d successes=%v elapsed=%s peak_inflight=%d peak_queued=%d snapshots=%v",
		w.name, w.probes, w.successes, w.elapsed.Round(time.Millisecond), w.peakFlight, w.peakQueued, w.snapshots)
}

// samplePeaks watches one group while a round runs and records the queue peaks.
func samplePeaks(ctx context.Context, sampler recoveryQueueSampler, window *recoveryWindow) {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			inFlight, queued, _, _, _ := sampler.QueueSnapshot()
			if inFlight > window.peakFlight {
				window.peakFlight = inFlight
			}
			if queued > window.peakQueued {
				window.peakQueued = queued
			}
		}
	}
}

// runManualWindow runs one manual round on both groups and records the window.
func runManualWindow(t *testing.T, scale *scaleProviderServer, runners []recoveryRoundRunner, snapshotters []recoverySnapshotter, name string) recoveryWindow {
	t.Helper()
	window := recoveryWindow{name: name, successes: make([]int, 0, len(runners))}
	probesBefore := scale.probes.Load()
	sampleCtx, stopSampling := context.WithCancel(context.Background())
	var samplers sync.WaitGroup
	for _, snapshotter := range snapshotters {
		sampler, canSample := snapshotter.(recoveryQueueSampler)
		require.True(t, canSample, "the group must expose the cheap queue sampler")
		samplers.Add(1)
		go func(sampler recoveryQueueSampler) {
			defer samplers.Done()
			samplePeaks(sampleCtx, sampler, &window)
		}(sampler)
	}

	started := time.Now()
	results := make([]map[string]uint16, len(runners))
	errors := make([]error, len(runners))
	var rounds sync.WaitGroup
	for index := range runners {
		index := index
		rounds.Add(1)
		go func() {
			defer rounds.Done()
			results[index], errors[index] = runners[index].URLTest(context.Background())
		}()
	}
	rounds.Wait()
	window.elapsed = time.Since(started)
	stopSampling()
	samplers.Wait()
	window.probes = scale.probes.Load() - probesBefore
	for index := range runners {
		require.NoError(t, errors[index], "window %s group %d", name, index+1)
		window.successes = append(window.successes, len(results[index]))
	}
	for _, snapshotter := range snapshotters {
		window.snapshots = append(window.snapshots, snapshotter.HealthSnapshot())
	}
	window.log(t)
	return window
}

// awaitMembers waits until both groups see the expected member count, so a window never measures a
// membership that is still being rebuilt.
func awaitMembers(t *testing.T, snapshotters []recoverySnapshotter, members int) {
	t.Helper()
	deadline := time.Now().Add(120 * time.Second)
	for {
		ready := true
		for _, snapshotter := range snapshotters {
			if snapshotter.HealthSnapshot().Members != members {
				ready = false
				break
			}
		}
		if ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("groups never reached %d members: %v", members, snapshotters[0].HealthSnapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRealBoxProviderRecovery is the named-window recovery sequence.
func TestRealBoxProviderRecovery(t *testing.T) {
	if os.Getenv("SING_BOX_RECOVERY_TEST") == "" {
		t.Skip("opt-in: set SING_BOX_RECOVERY_TEST=1 (SING_BOX_RECOVERY_NODES defaults to 20)")
	}
	nodes := 20
	if raw := os.Getenv("SING_BOX_RECOVERY_NODES"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		require.NoError(t, err)
		nodes = parsed
	}
	if nodes < 8 || nodes%4 != 0 {
		t.Fatalf("node count must be a multiple of 4 and >= 8, got %d", nodes)
	}

	scale := newScaleProviderServer(t, nodes)
	server := startDiagnoseTestCommandServer(t, providerScaleConfig(scale.server.URL))

	refreshUntilPublished := func(providerTag string) {
		deadline := time.Now().Add(300 * time.Second)
		for {
			summary, err := server.RefreshProviders(providerTag, 60000)
			require.NoError(t, err, "refresh %s", providerTag)
			if strings.Contains(summary.Value, `"status":"published"`) {
				return
			}
			if strings.Contains(summary.Value, `"status":"busy"`) && time.Now().Before(deadline) {
				time.Sleep(200 * time.Millisecond)
				continue
			}
			t.Fatalf("refresh %s: %s", providerTag, summary.Value)
		}
	}
	refreshUntilPublished("p1")
	refreshUntilPublished("p2")

	instance := server.Instance()
	require.NotNil(t, instance)
	runners := make([]recoveryRoundRunner, 2)
	periodic := make([]recoveryPeriodicRunner, 2)
	snapshotters := make([]recoverySnapshotter, 2)
	for index, tag := range []string{"g1", "g2"} {
		outbound, loaded := instance.Box().Outbound().Outbound(tag)
		require.True(t, loaded)
		runner, isRunner := outbound.(recoveryRoundRunner)
		require.True(t, isRunner)
		runners[index] = runner
		periodicRunner, isPeriodic := outbound.(recoveryPeriodicRunner)
		require.True(t, isPeriodic)
		periodic[index] = periodicRunner
		snapshotter, isSnapshotter := outbound.(recoverySnapshotter)
		require.True(t, isSnapshotter)
		snapshotters[index] = snapshotter
		installer, isInstaller := outbound.(recoveryPolicyInstaller)
		require.True(t, isInstaller)
		// The same thresholds the component tests use, installed on the REAL group.
		installer.ApplyFailoverPolicy(urltest.FailoverPolicy{WidenAfter: 1, SettleAfter: 2, RecoverAfter: 1, ToleranceMs: 1})
	}
	awaitMembers(t, snapshotters, nodes)

	// The baseline is taken AFTER the Box started and published: the goroutine accounting below is
	// about the rounds, not about the service's own long-lived workers.
	baselineGoroutines := runtime.NumGoroutine()
	t.Logf("nodes=%d goroutines_baseline=%d", nodes, baselineGoroutines)

	// Window 1: a full manual round. This is the reference for "every member is measured".
	full := runManualWindow(t, scale, runners, snapshotters, "manual-full")
	for _, successes := range full.successes {
		require.Equal(t, nodes, successes, "a full round measures every member")
	}

	// Window 2: controlled failure. Every fourth node of the whole set points at a closed port.
	dead := map[string]bool{}
	for index := 0; index < nodes; index++ {
		if index%4 != 0 {
			continue
		}
		if index < nodes/2 {
			dead[fmt.Sprintf("a%d", index)] = true
		} else {
			dead[fmt.Sprintf("b%d", index-nodes/2)] = true
		}
	}
	require.Len(t, dead, nodes/4)
	scale.setDocuments(scale.buildDocuments(nodes, dead, false))
	refreshUntilPublished("p1")
	refreshUntilPublished("p2")
	awaitMembers(t, snapshotters, nodes)
	failure := runManualWindow(t, scale, runners, snapshotters, "failure")
	for _, successes := range failure.successes {
		require.Equal(t, nodes-len(dead), successes, "only the reachable members may report a delay")
	}

	// Window 3: republication under the SAME tags. Both documents are reversed (a real change), so
	// every node object is replaced while membership keeps its identity.
	scale.setDocuments(scale.buildDocuments(nodes, nil, true))
	refreshUntilPublished("p1")
	refreshUntilPublished("p2")
	awaitMembers(t, snapshotters, nodes)
	republication := runManualWindow(t, scale, runners, snapshotters, "republication")
	for _, successes := range republication.successes {
		require.Equal(t, nodes, successes, "the republished generation measures every member again")
	}

	// Window 4: recovery to the original documents. Nothing from the failure window may survive as
	// this window's evidence: every member must be measured and succeed.
	scale.setDocuments(scale.buildDocuments(nodes, nil, false))
	refreshUntilPublished("p1")
	refreshUntilPublished("p2")
	awaitMembers(t, snapshotters, nodes)
	recovery := runManualWindow(t, scale, runners, snapshotters, "recovery")
	for _, successes := range recovery.successes {
		require.Equal(t, nodes, successes)
	}

	// Window 5: stable narrowing. A PERIODIC round (force=false) applies the policy's probe set; the
	// healthy stable member is the only member measured, and the two groups share one probe.
	for iteration := 0; iteration < 3; iteration++ {
		probesBefore := scale.probes.Load()
		for index := range periodic {
			periodic[index].CheckOutboundsHealth(context.Background(), false)
		}
		delta := scale.probes.Load() - probesBefore
		snapshot := snapshotters[0].HealthSnapshot()
		t.Logf("narrowing round=%d probes=%d stable=%q widened=%v selected=%q members=%d",
			iteration+1, delta, snapshot.Stable, snapshot.Widened, snapshot.Selected, snapshot.Members)
		require.False(t, snapshot.Widened, "a healthy stable member must keep the probe set narrow")
		require.NotEmpty(t, snapshot.Stable)
		require.Equal(t, 1, len(snapshot.NextProbes),
			"the next periodic round probes the stable member only, not the whole member set")
		require.LessOrEqual(t, delta, int32(4),
			"a periodic round with fresh evidence measures nothing rather than the whole set")
	}

	// Window 6: a cancelled manual round must not become a node-failure window, and the next full
	// round must still succeed (the batch's deferred join is what makes the cancel drain).
	cancelledCtx, cancelRound := context.WithCancel(context.Background())
	cancelledDone := make(chan struct{})
	go func() {
		defer close(cancelledDone)
		_, _ = runners[0].URLTest(cancelledCtx)
	}()
	time.Sleep(20 * time.Millisecond)
	cancelRound()
	select {
	case <-cancelledDone:
	case <-time.After(30 * time.Second):
		t.Fatal("a cancelled manual round did not return")
	}
	afterCancel := runManualWindow(t, scale, runners, snapshotters, "cancel-drain")
	for _, successes := range afterCancel.successes {
		require.Equal(t, nodes, successes, "a cancelled round must not be recorded as a node failure")
	}

	// Drain and goroutine accounting: the rounds are synchronous, so nothing should be running.
	time.Sleep(500 * time.Millisecond)
	finalGoroutines := runtime.NumGoroutine()
	stack := make([]byte, 1<<20)
	stackSize := runtime.Stack(stack, true)
	t.Logf("goroutines_final=%d delta=%d stack_bytes=%d", finalGoroutines, finalGoroutines-baselineGoroutines, stackSize)
	require.LessOrEqual(t, finalGoroutines-baselineGoroutines, 16,
		"the recovery sequence must not leave goroutines behind; stack dump follows\n%s", string(stack[:stackSize]))
}
