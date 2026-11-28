package route

// E-line (lifecycle coordination) probes for the memory-release entry.
//
// What is under test is not a new mechanism but the CLASSIFICATION the OOM killer crisis step gets
// at the connection layer. The escalation ladder last step is documented as the destructive one
// (docs/FAST-PATH-AND-MEMORY.md, the layered memory section, row for the emergency path;
// KNOWLEDGE/modules/power-mobile-lifecycle.md section 4: Release is the real memory emergency),
// and the drain refactor (31060e78f) states in its own message that ReleaseMemory semantics are
// untouched. These probes measure what the code actually hands the connection manager, and what
// that policy then does to an active stream.
//
// They FAILED on H0 012e63e95 and pass once the entry uses ResetNetworkHard.

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestReleaseMemoryHandsTheConnectionManagerTheMemoryPressureReason pins the classification at the
// seam.
//
// The bug this pins: ReleaseMemory -> ResetNetwork -> performTransitionLocked(..., false) ->
// resetNetworkLocked -> Reclaim(ReclaimNetworkTransition) on a healthy network, which is the DRAIN
// policy - only connections silent for drainIdleGrace are reclaimed and kernel-owned ones are
// protected. The OOM crisis step therefore reached the same policy as a Wi-Fi roam and could not
// free the per-flow retention it exists for.
//
// The fix does not reuse ReclaimDeadPath either: that reason is documented as a device with no
// default interface at all, and memory pressure arrives on a device that is usually online. It gets
// its own class so the reconnect governor can be opened for it without opening it for a dead path.
func TestReleaseMemoryHandsTheConnectionManagerTheMemoryPressureReason(t *testing.T) {
	manager := newPowerHarness(t)
	capture := &reclaimReasonCapture{reasons: make(chan ReclaimReason, 4)}
	manager.connectionManager = capture

	manager.ReleaseMemory(context.Background())

	select {
	case reason := <-capture.reasons:
		require.Equal(t, ReclaimMemoryPressure, reason,
			"the memory-release entry handed the connection manager %v; the crisis step needs the "+
				"destructive memory-pressure class, and neither the drain nor the dead path's class", reason)
	case <-time.After(10 * time.Second):
		t.Fatal("ReleaseMemory never reached the connection manager")
	}
}

// TestReleaseMemoryLeavesAnActiveStreamTracked measures the consequence end to end: the real
// connection manager, a stream that just moved a byte, and the crisis entry.
func TestReleaseMemoryLeavesAnActiveStreamTracked(t *testing.T) {
	manager := newPowerHarness(t)
	connections := NewConnectionManager(nil)
	connections.drainIdleGraceOverride = 10 * time.Second
	t.Cleanup(func() { _ = connections.Close() })
	manager.connectionManager = connections

	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	tracked := connections.TrackConn(client)
	go func() { _, _ = server.Write([]byte("payload")) }()
	buffer := make([]byte, 16)
	_, readErr := tracked.Read(buffer)
	require.NoError(t, readErr)
	require.Equal(t, 1, connections.Count())

	manager.ReleaseMemory(context.Background())

	require.Zero(t, connections.Count(),
		"the memory-release entry left an ACTIVE stream tracked, so the crisis step cannot free the "+
			"per-flow retention it exists for; only ResetNetworkHard (the long-resume entry) closes it")
}

// TestResetNetworkHardClosesTheSameStream is the control: the destructive classification exists and
// works, so the difference between the two entries is the classification alone, not a missing
// mechanism.
func TestResetNetworkHardClosesTheSameStream(t *testing.T) {
	manager := newPowerHarness(t)
	connections := NewConnectionManager(nil)
	connections.drainIdleGraceOverride = 10 * time.Second
	t.Cleanup(func() { _ = connections.Close() })
	manager.connectionManager = connections

	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	tracked := connections.TrackConn(client)
	go func() { _, _ = server.Write([]byte("payload")) }()
	buffer := make([]byte, 16)
	_, readErr := tracked.Read(buffer)
	require.NoError(t, readErr)

	manager.ResetNetworkHard(context.Background())

	require.Zero(t, connections.Count(),
		"the dead-path classification did not close an active stream, so this control proves nothing")
}
