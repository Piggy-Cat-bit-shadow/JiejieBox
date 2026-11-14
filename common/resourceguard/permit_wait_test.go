package resourceguard

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Permit.Wait must report the ADMISSION wait, not the time the permit has been held.
//
// # The defect this test was written against
//
// `acquired` is stamped when Acquire starts, and Wait() returned time.Since(acquired). Every caller
// that measured after doing real work therefore reported its own holding time as queueing latency:
// a permit held for a 30 ms handshake claimed a 30 ms wait, and the admission latency evidence a
// report would be written from described the workload rather than the gate.
//
// The production path does not call Wait today, which is exactly why the bug survived: it is a
// measurement API whose number is wrong for every caller that uses it as documented.
func TestPermitWaitReportsAdmissionNotHolding(t *testing.T) {
	gate := NewGate(plan{limit: 1, queue: 2, wait: time.Second}, 0)

	permit, err := gate.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)

	// Let real time pass while the permit is held. This is not a timing race: the assertion is
	// about which interval the API reports, and the margin is three orders of magnitude.
	time.Sleep(30 * time.Millisecond)

	require.Less(t, permit.Wait(), 10*time.Millisecond,
		"a permit held for 30 ms reported %v of admission wait", permit.Wait())
	permit.Release()
}

// TestPermitWaitCountsAQueuedWaiter is the other half: a caller that really did wait must see it.
func TestPermitWaitCountsAQueuedWaiter(t *testing.T) {
	gate := NewGate(plan{limit: 1, queue: 2, wait: time.Second}, 0)
	held, err := gate.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)

	waited := make(chan *Permit, 1)
	go func() {
		permit, acquireErr := gate.Acquire(context.Background(), ClassNormal)
		if acquireErr == nil {
			waited <- permit
		}
	}()
	require.Eventually(t, func() bool { return gate.Stats().Waiters == 1 }, time.Second, time.Millisecond)

	time.Sleep(20 * time.Millisecond)
	held.Release()

	var permit *Permit
	select {
	case permit = <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("the queued waiter was never admitted")
	}
	defer permit.Release()

	require.GreaterOrEqual(t, permit.Wait(), 15*time.Millisecond,
		"a caller that queued for ~20 ms reported %v", permit.Wait())
	require.Less(t, permit.Wait(), 2*time.Second)
}
