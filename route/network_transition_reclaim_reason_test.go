package route

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

// Tests for WHICH reclaim policy a network reset asks for.
//
// # Why the policy table alone is not enough
//
// conn_reclaim_test.go already pins the table: ReclaimDeadPath closes everything, ReclaimNetworkTransition
// drains. What was never pinned is the DECISION - the fact that a reset which has held offline long
// enough asks for the destructive one and an ordinary path change does not. Measured: replacing the
// classification at the call site with `_ = r.networkIsConfirmedOffline()` and a constant
// ReclaimNetworkTransition left the whole suite green, because every existing dead-path assertion
// calls Reclaim(ReclaimDeadPath) directly and so tests the branch, not the choice of branch.
//
// # What the choice costs when it is wrong
//
// The two halves fail in opposite directions, and a round has been spent on each:
//
//	always DRAIN       a device that really is offline keeps every dead socket tracked until it falls
//	                   silent. Nothing on the far side will ever answer, and the drain sweep is the only
//	                   thing that ends them - so the "aggressively close when the path is dead" rule
//	                   silently stops existing.
//	always CLOSE       a Wi-Fi to cellular handover passes through a moment with no default interface.
//	                   Closing there ends every stream the device was running, on a device that is
//	                   about to have a perfectly good network - the drain undone by its own special case.
//
// The confirmation window (HardTransitionConfirm) is the whole of the difference between them, so the
// three tests below are the window read from both sides at the production call site.

// recordingReclaimer is a connection manager double that records the policy the network layer asked
// for, which is the fact these tests are about. The real ConnectionManager's Reclaim reports only a
// count, and its destructive branch returns before the reclaim log, so the reason is not observable
// from an ordinary manager.
type recordingReclaimer struct {
	adapter.ConnectionManager

	access    sync.Mutex
	reasons   []ReclaimReason
	closedAll bool
}

func (r *recordingReclaimer) Reclaim(reason ReclaimReason) int {
	r.access.Lock()
	defer r.access.Unlock()
	r.reasons = append(r.reasons, reason)
	return 0
}

func (r *recordingReclaimer) CloseAll() {
	r.access.Lock()
	defer r.access.Unlock()
	r.closedAll = true
}

func (r *recordingReclaimer) recorded() ([]ReclaimReason, bool) {
	r.access.Lock()
	defer r.access.Unlock()
	return append([]ReclaimReason(nil), r.reasons...), r.closedAll
}

// reclaimThroughReset runs one real network reset through the production entry point and returns the
// reclaim reasons it asked for.
//
// It drives ResetNetwork rather than resetNetworkLocked because the classification is decided inside
// the reset body and reads the LIVE pause state; a test that called the body directly would still be
// exercising the same code, but this way the whole public path is in scope.
func reclaimThroughReset(t *testing.T) []ReclaimReason {
	t.Helper()
	harness := newInterfaceTransitionHarness(t)
	recorder := new(recordingReclaimer)
	harness.manager.connectionManager = recorder

	harness.manager.ResetNetwork(context.Background())

	reasons, closedAll := recorder.recorded()
	require.False(t, closedAll,
		"the fallback CloseAll ran, so the manager never reached a reclaim policy at all")
	return reasons
}

// TestConfirmedOfflineIsReclaimedAsADeadPath is the "aggressively close" half.
//
// A device that has been without a default interface for longer than HardTransitionConfirm is offline,
// not mid-handover: there is no socket to drain and no reconnect to protect, so the transition must
// close everything rather than leave dead connections tracked.
func TestConfirmedOfflineIsReclaimedAsADeadPath(t *testing.T) {
	harness := newInterfaceTransitionHarness(t)
	paused := &trackingPauseManager{}
	harness.manager.pauseManager = paused
	recorder := new(recordingReclaimer)
	harness.manager.connectionManager = recorder

	// Offline, and it has HELD: the onset is older than the confirmation window.
	paused.NetworkPause()
	harness.manager.networkPausedSince.Store(time.Now().Add(-HardTransitionConfirm - time.Second).UnixNano())
	require.True(t, harness.manager.networkIsConfirmedOffline(), "the fixture must be a confirmed outage")

	harness.manager.ResetNetwork(context.Background())

	reasons, _ := recorder.recorded()
	require.Equal(t, []ReclaimReason{ReclaimDeadPath}, reasons,
		"a confirmed dead path was reclaimed as an ordinary path change. Nothing on the far side of "+
			"those sockets will ever answer, so the only thing that ends them is the drain sweep - and "+
			"the rule that a dead path is closed aggressively quietly stops existing")
}

// TestAGapThatHasNotHeldIsStillADrain is the other side of the same window, at the same call site.
//
// This is the Wi-Fi to cellular handover: the platform reports no default interface for a moment, and
// a reset lands inside it. The state has not held, so it is not offline - it is mid-transition, and
// the streams on the old path must be given the chance to finish.
func TestAGapThatHasNotHeldIsStillADrain(t *testing.T) {
	harness := newInterfaceTransitionHarness(t)
	paused := &trackingPauseManager{}
	harness.manager.pauseManager = paused
	recorder := new(recordingReclaimer)
	harness.manager.connectionManager = recorder

	// Paused, but the gap started NOW. The onset is what the confirmation window is measured from.
	paused.NetworkPause()
	harness.manager.networkPausedSince.Store(time.Now().UnixNano())
	require.False(t, harness.manager.networkIsConfirmedOffline(), "a just-started gap is not an outage")

	harness.manager.ResetNetwork(context.Background())

	reasons, _ := recorder.recorded()
	require.Equal(t, []ReclaimReason{ReclaimNetworkTransition}, reasons,
		"a handover gap that has not held was reclaimed destructively, closing every stream the device "+
			"was running on a device that is about to have a working network")
}

// TestAnOnlineTransitionIsADrain is the ordinary case, and the regression that started this line of
// work: a path change on a device that is plainly online must never be destructive.
func TestAnOnlineTransitionIsADrain(t *testing.T) {
	require.Equal(t, []ReclaimReason{ReclaimNetworkTransition}, reclaimThroughReset(t),
		"an online path change reclaimed destructively; the drain would be undone by its own default")
}
