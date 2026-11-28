package route

// The memory crisis's seam, pinned from both sides.
//
// These are the contract tests the drain refactor was missing: they fix what ReleaseMemory hands
// the connection manager, and what that class then does to an active stream and to the reconnect
// governor. The first version of this file was written by the independent review (V line) against
// the candidate that reused ReclaimDeadPath; the two assertions that encoded that reuse are updated
// here to the reason this fix introduces, which is the point of keeping them.
//
// History worth knowing: no test pinned this seam before, which is why ReleaseMemory could be
// downgraded to a drain without a single failure.

// V-line review harness (E-01 8b4c23a28).
//
// E-01 turns ReleaseMemory from ResetNetwork (drain policy) into ResetNetworkHard (dead-path policy).
// These tests attack the two claims the commit rests on:
//
//   1. that the ONLY difference between the two resets is the classification flag, so the change is
//      "release now closes per-flow retention" and nothing else;
//   2. that no caller relies on ReleaseMemory being non-destructive.
//
// They also drive ReleaseMemory, TrimMemory and ResetNetwork concurrently, because ReleaseMemory
// takes memoryReclaimAccess and then resetRunAccess: the review question is whether that order can
// invert against the other entry points.

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestV05TheTwoResetsDifferOnlyInTheClassification pins the shape of the change: both resets run the
// same body (one router reset, one generation advance), and the only observable difference is the
// reclaim class handed to the connection manager. ReleaseMemory now shares ResetNetworkHard's class.
func TestTheMemoryCrisisHasItsOwnReclaimClass(t *testing.T) {
	type entry struct {
		name string
		call func(*NetworkManager)
	}
	entries := []entry{
		{name: "ResetNetwork", call: func(manager *NetworkManager) { manager.ResetNetwork(context.Background()) }},
		{name: "ResetNetworkHard", call: func(manager *NetworkManager) { manager.ResetNetworkHard(context.Background()) }},
		{name: "ReleaseMemory", call: func(manager *NetworkManager) { manager.ReleaseMemory(context.Background()) }},
	}
	reasons := make(map[string]ReclaimReason, len(entries))
	for _, item := range entries {
		manager := newPowerHarness(t)
		router := manager.router.(*countingRouter)
		capture := &reclaimReasonCapture{reasons: make(chan ReclaimReason, 4)}
		manager.connectionManager = capture
		generationBefore := manager.NetworkResetGeneration()
		resetsBefore := dnsResetCount(router)

		item.call(manager)

		reasons[item.name] = capture.await(t)
		require.Equal(t, resetsBefore+1, dnsResetCount(router),
			"%s performed a different number of router resets", item.name)
		require.Greater(t, manager.NetworkResetGeneration(), generationBefore,
			"%s did not advance the network generation", item.name)
	}

	require.Equal(t, ReclaimNetworkTransition, reasons["ResetNetwork"],
		"ResetNetwork on a healthy network must stay the drain")
	require.Equal(t, ReclaimDeadPath, reasons["ResetNetworkHard"])
	require.Equal(t, ReclaimMemoryPressure, reasons["ReleaseMemory"],
		"ReleaseMemory must classify as memory pressure, not as a dead path: the two are both "+
			"destructive but a dead path cannot reconnect and memory pressure usually can")
	require.NotEqual(t, reasons["ResetNetworkHard"], reasons["ReleaseMemory"],
		"the memory crisis and the dead-path entry collapsed back into one class")
}

// TestV05AConfirmedOfflineResetIsIndistinguishableFromTheHardEntry is the other direction of the
// same claim: when the device really is confirmed offline, ResetNetwork already asks for
// ReclaimDeadPath, so the flag ResetNetworkHard passes is exactly the constant-offline condition.
func TestAConfirmedOfflineResetStillClassifiesAsTheDeadPath(t *testing.T) {
	offline := newPowerHarness(t)
	paused := &trackingPauseManager{}
	offline.pauseManager = paused
	paused.NetworkPause()
	offline.networkPausedSince.Store(time.Now().Add(-HardTransitionConfirm - time.Second).UnixNano())
	require.True(t, offline.networkIsConfirmedOffline(), "the fixture must be a confirmed outage")
	capture := &reclaimReasonCapture{reasons: make(chan ReclaimReason, 2)}
	offline.connectionManager = capture
	offline.ResetNetwork(context.Background())
	require.Equal(t, ReclaimDeadPath, capture.await(t),
		"a confirmed-offline ResetNetwork no longer matches the hard entry")
}

// TestV05ReleaseMemoryClosesActiveFlowsButDoesNotOpenTheReconnectGovernor records a cost of reusing
// the dead-path class: the drain branch opens the reconnect governor so the re-dial burst after a
// transition is smoothed, and the dead-path branch does not. After an OOM crisis closes every flow,
// the burst is therefore unsmoothed - the one observable difference the reuse introduces beyond the
// intent of the commit.
func TestReleaseMemoryOpensTheReconnectGovernor(t *testing.T) {
	manager := newPowerHarness(t)
	connections := NewConnectionManager(nil)
	t.Cleanup(func() { _ = connections.Close() })
	manager.connectionManager = connections

	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ticket, err := connections.AcquireConnection(context.Background())
	require.NoError(t, err)
	_, err = ticket.TrackConn(client)
	require.NoError(t, err)
	require.Equal(t, 1, connections.Count())
	require.False(t, connections.DialGovernorOpen())

	manager.ReleaseMemory(context.Background())

	require.Zero(t, connections.Count(), "ReleaseMemory did not close the active flow")
	require.True(t, connections.DialGovernorOpen(),
		"the memory crisis closed every flow without opening the reconnect governor: on a device "+
			"that is still online every application re-dials at once, which is the burst the "+
			"governor exists to smooth and the worst moment to arrive unsmoothed")

	// Control: the drain policy DOES open it, which is the asymmetry being recorded.
	manager.ResetNetwork(context.Background())
	require.True(t, connections.DialGovernorOpen(),
		"the transition drain did not open the governor window, so the control proves nothing")
}

// TestV06ReleaseMemoryTrimAndResetDoNotDeadlock drives the three entry points concurrently.
//
// ReleaseMemory holds memoryReclaimAccess and then takes resetRunAccess inside ResetNetworkHard.
// TrimMemory takes memoryReclaimAccess (and the router walk); ResetNetwork takes resetRunAccess
// only. That order is memoryReclaimAccess -> resetRunAccess everywhere, so no inversion exists to
// find - but "no inversion by reading" is what a deadlock hides behind, so the order is exercised.
func TestReleaseMemoryTrimAndResetDoNotDeadlock(t *testing.T) {
	manager, _ := newCoordinatorHarness(t)
	connections := NewConnectionManager(nil)
	t.Cleanup(func() { _ = connections.Close() })
	manager.connectionManager = connections

	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	ticket, err := connections.AcquireConnection(context.Background())
	require.NoError(t, err)
	_, err = ticket.TrackConn(client)
	require.NoError(t, err)

	const rounds = 25
	var waitGroup sync.WaitGroup
	waitGroup.Add(3)
	go func() {
		defer waitGroup.Done()
		for index := 0; index < rounds; index++ {
			manager.ReleaseMemory(context.Background())
		}
	}()
	go func() {
		defer waitGroup.Done()
		for index := 0; index < rounds; index++ {
			manager.TrimMemory(context.Background())
		}
	}()
	go func() {
		defer waitGroup.Done()
		for index := 0; index < rounds; index++ {
			manager.ResetNetwork(context.Background())
		}
	}()

	done := make(chan struct{})
	go func() {
		waitGroup.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("deadlock: ReleaseMemory (memoryReclaimAccess -> resetRunAccess) and " +
			"TrimMemory/ResetNetwork did not complete; a lock-order inversion or a re-entrant " +
			"acquisition would hang exactly here")
	}
}
