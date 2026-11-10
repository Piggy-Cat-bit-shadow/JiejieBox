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
	"context"
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

// TestTheDisposalBacklogStopsAdmissionAndRecovers is the saturation contract: with a budget of two,
// a reclaim that retired six sockets returns at once (the network-change body must not wait), queues
// two and condemns four; new connections are refused at admission while the backlog is over budget,
// and are admitted again once the worker has drained the wave. Every object stays on the ledger the
// whole time and closes exactly once.
func TestTheDisposalBacklogStopsAdmissionAndRecovers(t *testing.T) {
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
	select {
	case <-reclaimDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the reclaim waited for room: the generation boundary must not queue behind a Close")
	}
	require.Equal(t, 2, manager.pendingDisposals(), "the queue holds exactly the budget")
	require.Equal(t, 4, manager.condemnedDisposals(), "the rest is condemned but still owned")

	// Admission refuses new work while the backlog is over budget, and the refusal is the caller's
	// context, not an unbounded wait.
	admissionCtx, cancelAdmission := context.WithTimeout(context.Background(), 100*time.Millisecond)
	err := manager.WaitForConnectionRoom(admissionCtx)
	cancelAdmission()
	require.ErrorIs(t, err, context.DeadlineExceeded)

	for _, conn := range conns {
		conn.releaseClose()
	}
	require.Eventually(t, func() bool {
		return manager.pendingDisposals() == 0 && manager.condemnedDisposals() == 0
	}, 5*time.Second, 5*time.Millisecond, "the ledger must be empty once the worker drained")
	for index, conn := range conns {
		require.EqualValues(t, 1, conn.closes.Load(), "socket %d must close exactly once", index)
	}
}

// TestAdmissionDoesNotOutliveTheManager is the attack this design invited: admission waits on a
// ledger signal, and shutdown also signals that channel. If the wake-up is treated as "room
// available", a dial admitted after CloseAll has run would be tracked into a manager nobody will
// close again. Shutdown must turn admission into an error the dialer can propagate.
func TestAdmissionDoesNotOutliveTheManager(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	manager.disposalBudgetOverride = 1
	blocked := newBlockedPacketClose(t)
	manager.TrackPacketConn(blocked)
	manager.Reclaim(ReclaimNetworkTransition)
	require.Equal(t, 1, manager.pendingDisposals())
	t.Cleanup(blocked.releaseClose)

	admitted := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		admitted <- manager.WaitForConnectionRoom(ctx)
	}()
	// Let the waiter park on the ledger signal.
	time.Sleep(50 * time.Millisecond)

	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		_ = manager.Close()
	}()

	select {
	case err := <-admitted:
		require.Error(t, err, "admission returned success after shutdown began: the dial would be "+
			"tracked into a manager that CloseAll has already drained")
	case <-time.After(2 * time.Second):
		t.Fatal("admission did not return when shutdown began")
	}

	blocked.releaseClose()
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the blocked socket was released")
	}
}

// TestCloseAllClosesTheCondemnedSetSynchronously covers the deferred state in the synchronous
// promise: a condemned object is owned but untracked, so CloseAll must take it and close it, and
// must still wait for the queue's in-flight close.
func TestCloseAllClosesTheCondemnedSetSynchronously(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	manager.disposalBudgetOverride = 1
	t.Cleanup(func() { _ = manager.Close() })

	conns := make([]*blockedPacketClose, 0, 2)
	for index := 0; index < 2; index++ {
		conn := newBlockedPacketClose(t)
		conns = append(conns, conn)
		manager.TrackPacketConn(conn)
	}
	manager.Reclaim(ReclaimNetworkTransition)
	require.Equal(t, 1, manager.pendingDisposals(), "one socket is in the queue")
	require.Equal(t, 1, manager.condemnedDisposals(), "the other is condemned")

	closeAllDone := make(chan struct{})
	go func() {
		defer close(closeAllDone)
		manager.CloseAll()
	}()
	require.Eventually(t, func() bool { return conns[1].closes.Load() == 1 }, 5*time.Second, 5*time.Millisecond,
		"CloseAll did not close the condemned socket")
	select {
	case <-closeAllDone:
		t.Fatal("CloseAll returned while the queued socket's Close was still blocked")
	case <-time.After(100 * time.Millisecond):
	}
	// The condemned socket's own Close is blocked too: release both, then CloseAll can finish.
	conns[0].releaseClose()
	conns[1].releaseClose()
	select {
	case <-closeAllDone:
	case <-time.After(5 * time.Second):
		t.Fatal("CloseAll did not return after both closes finished")
	}
	require.Equal(t, 0, manager.pendingDisposals())
	require.Equal(t, 0, manager.condemnedDisposals())
}

// TestABatchStagedAfterShutdownIsStillClosedByClose attacks the other side of the shutdown race:
// CloseAll has already drained the tracked list, so the batch arrives through the ledger only. The
// object must be queued (never condemned), be visible to the pending wait, and be closed before
// Close returns. The admission waiter's error is the deterministic signal that shutdown is set.
func TestABatchStagedAfterShutdownIsStillClosedByClose(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	manager.disposalBudgetOverride = 1
	first := newBlockedPacketClose(t)
	manager.TrackPacketConn(first)
	manager.Reclaim(ReclaimNetworkTransition)
	require.Equal(t, 1, manager.pendingDisposals())

	admitted := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		admitted <- manager.WaitForConnectionRoom(ctx)
	}()
	time.Sleep(50 * time.Millisecond)

	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		_ = manager.Close()
	}()
	select {
	case err := <-admitted:
		require.Error(t, err, "shutdown did not stop admission")
	case <-time.After(5 * time.Second):
		t.Fatal("admission never observed shutdown")
	}

	// Close is now blocked in its pending wait. A batch that arrives afterwards must still be
	// closed before Close returns, even though CloseAll has already drained the tracked list.
	late := newBlockedPacketClose(t)
	manager.TrackPacketConn(late)
	manager.Reclaim(ReclaimNetworkTransition)
	require.Equal(t, 0, manager.condemnedDisposals(),
		"nothing may be condemned after shutdown: CloseAll already took that set")
	require.Equal(t, 2, manager.pendingDisposals(), "both objects must be in the pending account")

	select {
	case <-closeDone:
		t.Fatal("Close returned while two handed-off sockets were still closing")
	case <-time.After(100 * time.Millisecond):
	}
	first.releaseClose()
	late.releaseClose()
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after both sockets were released")
	}
	require.EqualValues(t, 1, first.closes.Load())
	require.EqualValues(t, 1, late.closes.Load())
	require.Equal(t, 0, manager.pendingDisposals())
	require.Equal(t, 0, manager.condemnedDisposals())
}
