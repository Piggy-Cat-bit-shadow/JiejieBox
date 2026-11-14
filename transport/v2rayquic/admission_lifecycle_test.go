//go:build with_quic

package v2rayquic

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"

	"github.com/stretchr/testify/require"
)

// The creation permit must be settled on every ending, including the ones that never create.
//
// # Why these tests exist
//
// Round 1 put the shared QUIC construction behind the process admission gate. A gate is only as good
// as its release paths: a permit held by a creation that was refused, cancelled, or whose owner was
// closed is a permanent loss of one of the 32 creation slots. The tests below drive each ending
// through the production path and assert the gate's ledger - not just that the call returned an
// error.

// TestCloseDuringAdmissionReleasesTheCreationWaiter is the shutdown race: a creation is queued at the
// gate, and the client is closed before it can be admitted.
//
// The close cancels the offer's lifetime, which must withdraw the gate waiter (not grant it and
// abandon it), and every waiter must be released with the closed error instead of waiting out the
// five-second budget.
func TestCloseDuringAdmissionReleasesTheCreationWaiter(t *testing.T) {
	// A queueing gate, not a refusing one: the point is a creation PARKED at the gate, and a
	// zero-length queue would refuse it in constant time before the close could ever be relevant.
	guard := resourceguard.NewGuard(resourceguard.Config{
		Limit: 1,
		Queue: 4,
		Wait:  5 * time.Second,
	}, nil)
	held, err := guard.Acquire(context.Background(), resourceguard.ClassNormal)
	require.NoError(t, err)
	defer held.Release()

	dial := &countingDial{}
	client := admittedTestClient(t, guard, dial.dial)

	offerDone := make(chan error, 1)
	go func() {
		_, err := client.offer(context.Background())
		offerDone <- err
	}()

	require.Eventually(t, func() bool {
		return guard.Stats().Gate.Waiters == 1
	}, 2*time.Second, time.Millisecond, "the creation never reached the gate")

	closeStarted := time.Now()
	require.NoError(t, client.Close())

	select {
	case err := <-offerDone:
		require.ErrorIs(t, err, net.ErrClosed,
			"a creation cancelled by Close must report the close, not a wait timeout")
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not release the creation waiting at the gate")
	}
	require.Less(t, time.Since(closeStarted), time.Second,
		"the waiter was released by the close, not by the gate's five-second wait budget")

	require.Zero(t, dial.callCount(), "a refused creation must not run the constructor")
	require.Nil(t, client.conn.Load(), "a discarded creation must not be published")

	// Release the permit this test held to make the gate queue, so what remains is only the
	// creation's own accounting.
	held.Release()
	stats := guard.Stats().Gate
	require.Zero(t, stats.Waiters, "the cancelled waiter must leave the queue")
	require.Zero(t, stats.Inflight, "a refused creation must not leave a permit behind")
	require.Equal(t, stats.Admitted, stats.Released,
		"every admitted creation must be released (admitted=%d released=%d)", stats.Admitted, stats.Released)
	require.EqualValues(t, 1, stats.Canceled, "the creation's waiter was withdrawn, not timed out")
}

// TestFailedCreationReleasesThePermit pins the failure ending: the constructor returns an error, and
// the permit must be back at the gate before the error reaches the waiters.
func TestFailedCreationReleasesThePermit(t *testing.T) {
	guard := resourceguard.NewGuard(resourceguard.Config{Limit: 4, Queue: 4, Wait: time.Second}, nil)
	dialErr := errors.New("quic handshake failed")
	dial := &countingDial{err: dialErr}
	client := admittedTestClient(t, guard, dial.dial)

	_, err := client.offer(context.Background())
	require.ErrorIs(t, err, dialErr)
	require.Equal(t, 1, dial.callCount())

	stats := guard.Stats().Gate
	require.EqualValues(t, 1, stats.Admitted)
	require.EqualValues(t, 1, stats.Released, "the failed creation must return its permit")
	require.Zero(t, stats.Inflight)
}

// TestAStaleCreationDoesNotChargeTheNextOne is the generation boundary: a creation discarded by a
// network change must not leave its permit behind, and the retry it causes must take exactly one
// more.
func TestAStaleCreationDoesNotChargeTheNextOne(t *testing.T) {
	guard := resourceguard.NewGuard(resourceguard.Config{Limit: 4, Queue: 4, Wait: time.Second}, nil)
	dial := &countingDial{err: errors.New("first attempt failed")}
	client := admittedTestClient(t, guard, dial.dial)

	_, err := client.offer(context.Background())
	require.Error(t, err)
	require.NoError(t, client.Reset())

	dial.access.Lock()
	dial.err = nil
	created, _ := fakeConnection()
	dial.conn = created
	dial.access.Unlock()

	conn, err := client.offer(context.Background())
	require.NoError(t, err)
	require.Same(t, created, conn)

	stats := guard.Stats().Gate
	require.EqualValues(t, 2, stats.Admitted)
	require.EqualValues(t, 2, stats.Released,
		"both creations must return their permit when they return; the permit bounds creation, not "+
			"the session's lifetime")
	require.Zero(t, stats.Inflight, "the published connection must not still hold a creation permit")
}
