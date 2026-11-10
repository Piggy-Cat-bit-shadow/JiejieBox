package route

// The ownership boundary between the network-change body and the physical Close.
//
// # What these tests are for
//
// `Reclaim` removes a connection from the tracked set under `m.access` and then hands it to a
// disposer. Shutdown must see the object at every instant of that hand-off: not only after the
// disposer has registered it. The first test holds the hand-off open and starts `Close` inside it;
// before the fix the manager returned while the socket had not even begun closing.
//
// The second test is the backlog side: one disposer per reclaim means twelve transitions start
// twelve simultaneous blocking closes. The fix keeps ONE worker per manager and a counted backlog,
// so the number of sockets inside Close is one no matter how many generations were reclaimed.

import (
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

// blockReclaims holds the hand-off open: the reclaim has removed the object from the tracked set and
// has not yet registered it with the disposer.
func blockReclaims(manager *ConnectionManager) (staged <-chan struct{}, release func()) {
	stagedCh := make(chan struct{})
	resume := make(chan struct{})
	var stagedOnce sync.Once
	var releaseOnce sync.Once
	manager.disposalStageHook = func() {
		stagedOnce.Do(func() { close(stagedCh) })
		<-resume
	}
	return stagedCh, func() {
		releaseOnce.Do(func() { close(resume) })
		manager.disposalStageHook = nil
	}
}

// TestShutdownWaitsForAConnectionInTheDisposalHandoff is the shutdown half of the ownership rule.
func TestShutdownWaitsForAConnectionInTheDisposalHandoff(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	blocked := newBlockedPacketClose(t)
	manager.TrackPacketConn(blocked)

	staged, releaseStage := blockReclaims(manager)
	t.Cleanup(releaseStage)

	reclaimDone := make(chan struct{})
	go func() {
		defer close(reclaimDone)
		manager.Reclaim(ReclaimNetworkTransition)
	}()
	select {
	case <-staged:
	case <-time.After(5 * time.Second):
		t.Fatal("the reclaim never reached the hand-off")
	}

	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		_ = manager.Close()
	}()

	// The object has left the tracked set but is not yet registered with the disposer. Shutdown
	// must not be able to return here: it has no record of the socket.
	select {
	case <-closeDone:
		releaseStage()
		t.Fatal("Close returned while the old socket was between the tracked set and the disposer: " +
			"shutdown cannot see the hand-off")
	case <-time.After(200 * time.Millisecond):
	}

	releaseStage()
	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		t.Fatalf("the old socket's physical Close never started (pending=%d closes=%d reclaim=%t)",
			manager.pendingDisposals(), blocked.closes.Load(), func() bool {
				select {
				case <-reclaimDone:
					return true
				default:
					return false
				}
			}())
	}

	// Now the close is registered and blocked. Shutdown still owes the caller a completed release.
	select {
	case <-closeDone:
		t.Fatal("Close returned before the old socket's physical Close finished")
	case <-time.After(100 * time.Millisecond):
	}

	blocked.releaseClose()
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the socket's physical Close finished")
	}
	<-reclaimDone
	require.EqualValues(t, 1, blocked.closes.Load(), "the old socket must close exactly once")
}

// enteredCloses counts how many of the blocking sockets have entered Close.
func enteredCloses(conns []*blockedPacketClose) int32 {
	var entered int32
	for _, conn := range conns {
		if conn.closes.Load() > 0 {
			entered++
		}
	}
	return entered
}

// TestConsecutiveReclaimsKeepOnePhysicalCloseInFlight is the backlog half: twelve transitions, one
// socket each, every Close blocked. The disposer count must not grow with the number of changes.
func TestConsecutiveReclaimsKeepOnePhysicalCloseInFlight(t *testing.T) {
	const rounds = 12
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = manager.Close() })

	// One socket per generation: track it, then reclaim. The next one is stamped with the new
	// generation, so the next reclaim owns exactly that socket - the shape that produced one
	// disposer goroutine per change.
	conns := make([]*blockedPacketClose, 0, rounds)
	for round := 0; round < rounds; round++ {
		conn := newBlockedPacketClose(t)
		conns = append(conns, conn)
		manager.TrackPacketConn(conn)
		manager.Reclaim(ReclaimNetworkTransition)
	}

	require.Eventually(t, func() bool { return enteredCloses(conns) >= 1 },
		2*time.Second, 5*time.Millisecond, "the first physical Close never started")
	// Every disposer that is going to enter Close has had time to do so.
	time.Sleep(250 * time.Millisecond)
	require.LessOrEqual(t, enteredCloses(conns), int32(1),
		"physical closes must be serialised by one worker, but %d sockets were inside Close at once "+
			"after %d consecutive reclaims", enteredCloses(conns), rounds)
	require.Equal(t, rounds, manager.pendingDisposals(),
		"every commissioned socket must be in the ledger while the worker is blocked")

	for _, conn := range conns {
		conn.releaseClose()
	}
	require.Eventually(t, func() bool {
		for _, conn := range conns {
			if conn.closes.Load() != 1 {
				return false
			}
		}
		return true
	}, 5*time.Second, 5*time.Millisecond)
	for index, conn := range conns {
		require.EqualValues(t, 1, conn.closes.Load(), "socket %d must close exactly once", index)
	}
}

// TestDisposalBacklogWaitsForRoomAndRecovers is the saturation behaviour: with a budget of two, a
// reclaim that removed six sockets must wait for the worker to catch up, and must return once it
// has. The objects are owned and counted the whole time, never dropped and never duplicated.
func TestDisposalBacklogWaitsForRoomAndRecovers(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	manager.disposalBudgetOverride = 2
	t.Cleanup(func() { _ = manager.Close() })

	conns := make([]*blockedPacketClose, 0, 6)
	for index := 0; index < 6; index++ {
		conn := newBlockedPacketClose(t)
		conns = append(conns, conn)
		manager.TrackPacketConn(conn)
	}

	reclaimDone := make(chan struct{})
	go func() {
		defer close(reclaimDone)
		manager.Reclaim(ReclaimNetworkTransition)
	}()

	require.Eventually(t, func() bool { return enteredCloses(conns) >= 1 },
		2*time.Second, 5*time.Millisecond, "the first physical Close never started")
	require.Greater(t, manager.pendingDisposals(), 2,
		"the backlog must be visible while it exceeds the budget")
	select {
	case <-reclaimDone:
		t.Fatal("the reclaim returned while six sockets were pending and the budget was two: " +
			"the backlog is not bounded")
	case <-time.After(150 * time.Millisecond):
	}

	for _, conn := range conns {
		conn.releaseClose()
	}
	select {
	case <-reclaimDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the reclaim never recovered after the sockets were released")
	}
	require.Eventually(t, func() bool { return manager.pendingDisposals() == 0 },
		5*time.Second, 5*time.Millisecond, "the ledger must be empty once the worker drained")
	for index, conn := range conns {
		require.EqualValues(t, 1, conn.closes.Load(), "socket %d must close exactly once", index)
	}
}
