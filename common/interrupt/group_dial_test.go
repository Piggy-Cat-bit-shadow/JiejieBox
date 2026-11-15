package interrupt

// The setup half of a group switch.
//
// An established connection is protected by interrupt_exist_connections: a switch may leave it
// running. A dial that has not produced a connection is not protected, because it is not a flow
// yet - it is a request waiting on the member the user just switched away from. Leaving it blocked
// is how "switch node" appears to do nothing while the old request waits for its own timeout, or
// forever when it has none.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBeginDialContextIsCancelledByInterrupt(t *testing.T) {
	group := NewGroup()
	revision := group.Revision()
	dialCtx, release, stale := group.BeginDial(context.Background(), revision)
	require.False(t, stale)
	require.NoError(t, dialCtx.Err())

	blocked := make(chan error, 1)
	go func() {
		<-dialCtx.Done()
		blocked <- context.Cause(dialCtx)
	}()

	group.InterruptSelection(false)
	select {
	case err := <-blocked:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("a pending dial survived Interrupt")
	}
	release()
}

func TestBeginDialRejectsAStaleSelection(t *testing.T) {
	group := NewGroup()
	revision := group.Revision()
	group.InterruptSelection(false)

	dialCtx, release, stale := group.BeginDial(context.Background(), revision)
	require.True(t, stale, "a dial selected before the switch must not run against the old member")
	require.ErrorIs(t, dialCtx.Err(), context.Canceled)
	release()

	// A dial that selected under the new revision is current.
	current, releaseCurrent, stale := group.BeginDial(context.Background(), group.Revision())
	require.False(t, stale)
	require.NoError(t, current.Err())
	releaseCurrent()
}

// TestInterruptDoesNotCutEstablishedConnectionsWhenDisabled keeps the existing promise intact: only
// the setup is cancelled unconditionally; the established flow still obeys the setting.
func TestInterruptDoesNotCutEstablishedConnectionsWhenDisabled(t *testing.T) {
	group := NewGroup()
	conn := &trackedCloser{}
	group.Add(conn, true)
	dialCtx, release, stale := group.BeginDial(context.Background(), group.Revision())
	require.False(t, stale)
	defer release()

	group.InterruptSelection(false)
	require.ErrorIs(t, dialCtx.Err(), context.Canceled)
	require.False(t, conn.closed.Load(), "interrupt_exist_connections=false must keep the established flow")
}

// TestBeginDialReleaseReleasesTheEntry pins the ownership boundary: release cancels the SETUP (the
// dial has returned, so the setup's authority has ended) and removes the entry, exactly once.
func TestBeginDialReleaseReleasesTheEntry(t *testing.T) {
	group := NewGroup()
	dialCtx, release, _ := group.BeginDial(context.Background(), group.Revision())
	release()
	release()
	require.ErrorIs(t, dialCtx.Err(), context.Canceled)

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
			dialCtx, release, stale := group.BeginDial(context.Background(), group.Revision())
			defer release()
			if stale {
				staleCount.Add(1)
				return
			}
			_ = dialCtx
		}()
	}
	// Race the switch against the starts. Every dial must end in exactly one of the two states.
	group.InterruptSelection(false)
	waitGroup.Wait()

	// A later dial is current and is not affected by the earlier switch.
	late, releaseLate, stale := group.BeginDial(context.Background(), group.Revision())
	require.False(t, stale)
	require.NoError(t, late.Err())
	releaseLate()
}

// TestInterruptLeavesASubscriptionRefreshDialAlone pins the other half of the boundary: a provider
// republication that replaces the object behind the SAME selected tag is not a switch. The dial's
// lease keeps the old object open, so the dial must be allowed to finish it; cancelling here turned a
// routine subscription refresh into a failed connection.
func TestInterruptLeavesASubscriptionRefreshDialAlone(t *testing.T) {
	group := NewGroup()
	revision := group.Revision()
	dialCtx, release, stale := group.BeginDial(context.Background(), revision)
	require.False(t, stale)
	defer release()

	group.Interrupt(false)
	require.NoError(t, dialCtx.Err(), "a provider refresh must not cancel an in-flight dial")
	require.Equal(t, revision, group.Revision(), "a provider refresh is not a selection change")
}

type trackedCloser struct {
	closed atomic.Bool
}

func (c *trackedCloser) Close() error {
	c.closed.Store(true)
	return nil
}
