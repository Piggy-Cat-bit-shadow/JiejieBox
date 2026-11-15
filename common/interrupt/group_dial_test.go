package interrupt

// The setup half of a group switch.
//
// An established connection is protected by interrupt_exist_connections: a switch may leave it
// running. A dial that has not produced a connection is not protected, because it is not a flow
// yet - it is a request waiting on the member the user just switched away from. Leaving it blocked
// is how "switch node" appears to do nothing while the old request waits for its own timeout, or
// forever when it has none.
//
// The transfer from pending setup to established connection is atomic (P1-2). These tests pin both
// sides of the boundary: a switch either cancels the setup before it can be delivered, or sees the
// connection in the established list and applies the configured policy to it.

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBeginDialContextIsCancelledByInterrupt(t *testing.T) {
	group := NewGroup()
	dial, stale := group.BeginDial(context.Background(), group.Revision())
	require.False(t, stale)
	require.NotNil(t, dial)
	require.NoError(t, dial.Context().Err())

	blocked := make(chan error, 1)
	go func() {
		<-dial.Context().Done()
		blocked <- context.Cause(dial.Context())
	}()

	group.InterruptSelection(false)
	select {
	case err := <-blocked:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("a pending dial survived InterruptSelection")
	}
	dial.Release()
}

func TestBeginDialRejectsAStaleSelection(t *testing.T) {
	group := NewGroup()
	revision := group.Revision()
	group.InterruptSelection(false)

	dial, stale := group.BeginDial(context.Background(), revision)
	require.True(t, stale, "a dial selected before the switch must not run against the old member")
	require.Nil(t, dial)

	// A dial that selected under the new revision is current.
	current, stale := group.BeginDial(context.Background(), group.Revision())
	require.False(t, stale)
	require.NoError(t, current.Context().Err())
	current.Release()
}

// TestInterruptDoesNotCutEstablishedConnectionsWhenDisabled keeps the existing promise intact: only
// the setup is cancelled unconditionally; the established flow still obeys the setting.
func TestInterruptDoesNotCutEstablishedConnectionsWhenDisabled(t *testing.T) {
	group := NewGroup()
	conn := &trackedConn{}
	group.Add(conn, true)
	dial, stale := group.BeginDial(context.Background(), group.Revision())
	require.False(t, stale)
	defer dial.Release()

	group.InterruptSelection(false)
	require.ErrorIs(t, dial.Context().Err(), context.Canceled)
	require.False(t, conn.closed.Load(), "interrupt_exist_connections=false must keep the established flow")
}

// TestBeginDialReleaseReleasesTheEntry pins the ownership boundary: release cancels the SETUP (the
// dial has returned, so the setup's authority has ended) and removes the entry, exactly once.
func TestBeginDialReleaseReleasesTheEntry(t *testing.T) {
	group := NewGroup()
	dial, _ := group.BeginDial(context.Background(), group.Revision())
	dial.Release()
	dial.Release()
	require.ErrorIs(t, dial.Context().Err(), context.Canceled)

	group.access.Lock()
	require.Empty(t, group.dials)
	group.access.Unlock()
}

func TestConcurrentInterruptAndBeginDial(t *testing.T) {
	group := NewGroup()
	const callers = 32
	var (
		staleCount atomic.Int32
		waitGroup  sync.WaitGroup
	)
	waitGroup.Add(callers)
	for index := 0; index < callers; index++ {
		go func() {
			defer waitGroup.Done()
			dial, stale := group.BeginDial(context.Background(), group.Revision())
			if stale {
				staleCount.Add(1)
				return
			}
			dial.Release()
		}()
	}
	// Race the switch against the starts. Every dial must end in exactly one of the two states.
	group.InterruptSelection(false)
	waitGroup.Wait()

	// A later dial is current and is not affected by the earlier switch.
	late, stale := group.BeginDial(context.Background(), group.Revision())
	require.False(t, stale)
	require.NoError(t, late.Context().Err())
	late.Release()
}

// TestInterruptLeavesASubscriptionRefreshDialAlone pins the other half of the boundary: a provider
// republication that replaces the object behind the SAME selected tag is not a switch. The dial's
// lease keeps the old object open, so the dial must be allowed to finish it; cancelling here turned a
// routine subscription refresh into a failed connection.
func TestInterruptLeavesASubscriptionRefreshDialAlone(t *testing.T) {
	group := NewGroup()
	revision := group.Revision()
	dial, stale := group.BeginDial(context.Background(), revision)
	require.False(t, stale)
	defer dial.Release()

	group.Interrupt(false)
	require.NoError(t, dial.Context().Err(), "a provider refresh must not cancel an in-flight dial")
	require.Equal(t, revision, group.Revision(), "a provider refresh is not a selection change")
}

// TestCompleteConnAfterSelectionChangeClosesTheConnection is the deterministic side of P1-2: the
// switch wins the pending entry first, so the hand-off that arrives afterwards must close the
// connection instead of registering it behind the switch's back.
func TestCompleteConnAfterSelectionChangeClosesTheConnection(t *testing.T) {
	group := NewGroup()
	dial, stale := group.BeginDial(context.Background(), group.Revision())
	require.False(t, stale)
	conn := &trackedConn{}

	group.InterruptSelection(true)
	registered, err := dial.CompleteConn(conn, true)
	require.Nil(t, registered)
	require.ErrorIs(t, err, context.Canceled)
	require.True(t, conn.closed.Load(), "a connection that lost the switch race was delivered")

	group.access.Lock()
	require.Empty(t, group.dials)
	require.Zero(t, group.connections.Len())
	group.access.Unlock()
	dial.Release()
}

// TestInterruptAfterCompletionClosesTheEstablishedConnection is the other deterministic side: the
// hand-off wins, so the switch must see the connection in the established list and apply the
// configured policy to it, not miss it because it was appended after the scan.
func TestInterruptAfterCompletionClosesTheEstablishedConnection(t *testing.T) {
	group := NewGroup()
	dial, stale := group.BeginDial(context.Background(), group.Revision())
	require.False(t, stale)
	conn := &trackedConn{}

	registered, err := dial.CompleteConn(conn, true)
	require.NoError(t, err)
	require.NotNil(t, registered)
	require.False(t, conn.closed.Load())
	dial.Release()

	group.InterruptSelection(true)
	require.True(t, conn.closed.Load(),
		"a connection registered before the switch was not interrupted")
	require.NoError(t, registered.Close())
}

// TestCompleteConnRacesInterruptSelectionNeverDeliversAnOpenConnection is the adversarial form:
// hand-off and switch are released together, many times. The only two legal outcomes are
// "hand-off succeeded and the switch closed it" or "hand-off failed and closed it itself"; in both,
// the connection is closed once the switch returns and nothing stays registered.
func TestCompleteConnRacesInterruptSelectionNeverDeliversAnOpenConnection(t *testing.T) {
	group := NewGroup()
	const rounds = 200
	for round := 0; round < rounds; round++ {
		dial, stale := group.BeginDial(context.Background(), group.Revision())
		require.False(t, stale)
		conn := &trackedConn{}
		start := make(chan struct{})
		var waitGroup sync.WaitGroup
		waitGroup.Add(2)
		go func() {
			defer waitGroup.Done()
			<-start
			group.InterruptSelection(true)
		}()
		go func() {
			defer waitGroup.Done()
			<-start
			_, _ = dial.CompleteConn(conn, true)
		}()
		close(start)
		waitGroup.Wait()
		require.True(t, conn.closed.Load(),
			"round %d: a connection was delivered open across a switch", round)
		dial.Release()
	}
	group.access.Lock()
	require.Empty(t, group.dials)
	require.Zero(t, group.connections.Len())
	group.access.Unlock()
}

// TestInvalidateSetupCancelsDialWithoutClosingEstablished is the provider/clear-selection case: the
// pending setup is stale, but a flow that is already carrying traffic is not cut by a single target
// failure.
func TestInvalidateSetupCancelsDialWithoutClosingEstablished(t *testing.T) {
	group := NewGroup()
	conn := &trackedConn{}
	group.Add(conn, true)
	dial, stale := group.BeginDial(context.Background(), group.Revision())
	require.False(t, stale)
	defer dial.Release()

	group.InvalidateSetup()
	require.ErrorIs(t, dial.Context().Err(), context.Canceled)
	require.False(t, conn.closed.Load(), "InvalidateSetup must not close established connections")
}

type trackedConn struct {
	closed    atomic.Bool
	closeOnce sync.Once
}

func (c *trackedConn) Read([]byte) (int, error)         { return 0, net.ErrClosed }
func (c *trackedConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *trackedConn) LocalAddr() net.Addr              { return nil }
func (c *trackedConn) RemoteAddr() net.Addr             { return nil }
func (c *trackedConn) SetDeadline(time.Time) error      { return nil }
func (c *trackedConn) SetReadDeadline(time.Time) error  { return nil }
func (c *trackedConn) SetWriteDeadline(time.Time) error { return nil }

func (c *trackedConn) Close() error {
	c.closeOnce.Do(func() { c.closed.Store(true) })
	return nil
}
