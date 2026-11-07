package outbound

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

// The lease was supposed to make "obtained a member" safe across a provider publication. The first
// implementation checked `current == object` under the read lock, dropped the read lock, and only
// then took the write lock to increment the user count. A publication could retire and close the
// object in that gap, and the increment then leased a closed object.
//
// These tests make the gap deterministic. The manager's test hook runs after the read-only check and
// before the exclusive section, exactly where the old code released its read lock.

func publishOwnedNode(t *testing.T, manager *Manager, tag string, owner string) *graphNode {
	t.Helper()
	node := &graphNode{tag: tag}
	manager.access.Lock()
	manager.outbounds = appendOutbound(manager.outbounds, node)
	manager.outboundByTag[tag] = node
	manager.ownedTags[tag] = owner
	manager.access.Unlock()
	return node
}

type acquireResult struct {
	release func()
	ok      bool
}

func TestAcquireOutboundDoesNotLeaseAnObjectClosedBetweenCheckAndCount(t *testing.T) {
	manager := newGraphManager(t)
	old := publishOwnedNode(t, manager, "p/X", "p")

	entered := make(chan struct{})
	resume := make(chan struct{})
	manager.testAcquireAfterRead = func() {
		close(entered)
		<-resume
	}
	defer func() { manager.testAcquireAfterRead = nil }()

	result := make(chan acquireResult, 1)
	go func() {
		release, ok := manager.AcquireOutbound(old)
		result <- acquireResult{release: release, ok: ok}
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("AcquireOutbound never reached the post-read hook")
	}

	// The publication runs while the acquisition has read the old object but has not counted it.
	require.NoError(t, manager.ReplaceOwned(context.Background(), nil, log.NewNOPFactory().NewLogger("test"),
		"p/X", "test", &graphNodeOptions{}, "p"))
	require.NoError(t, manager.CompletePublication("p", []string{"p/X"}))
	require.EqualValues(t, 1, old.closed.Load(),
		"the barrier closed the object because the acquisition had not counted a user yet")

	close(resume)
	acquired := <-result
	if acquired.release != nil {
		defer acquired.release()
	}
	require.False(t, acquired.ok,
		"AcquireOutbound must not report a closed object as usable; the final check and the user count must be one critical section")
}

// TestAcquireOutboundRejectsARemovedOwnedTag covers the other fast path: unlinkLocked deletes the
// tag from BOTH maps, so an object acquired after the removal used to look "not owned" and returned
// true without ever checking whether it was still published or retired.
func TestAcquireOutboundRejectsARemovedOwnedTag(t *testing.T) {
	manager := newGraphManager(t)
	old := publishOwnedNode(t, manager, "p/X", "p")

	require.NoError(t, manager.Remove("p/X"))
	require.EqualValues(t, 1, old.closed.Load())

	release, ok := manager.AcquireOutbound(old)
	if release != nil {
		release()
	}
	require.False(t, ok, "a removed provider object is closed and must not be leased")
}

// TestRemoveDefersCloseWhileAnObjectIsLeased pins the removal half of the same ownership rule: the
// tag disappears immediately, but a dial that already holds the object keeps it alive until its
// hand-off ends, and the object closes exactly once then.
func TestRemoveDefersCloseWhileAnObjectIsLeased(t *testing.T) {
	manager := newGraphManager(t)
	old := publishOwnedNode(t, manager, "p/X", "p")

	release, ok := manager.AcquireOutbound(old)
	require.True(t, ok)

	require.NoError(t, manager.Remove("p/X"))
	require.EqualValues(t, 0, old.closed.Load(),
		"Remove unlinked the tag but must not close an object a dial still owns")

	release()
	require.EqualValues(t, 1, old.closed.Load(), "the last hand-off closes the removed object exactly once")
	release()
	require.EqualValues(t, 1, old.closed.Load(), "a repeated release must not close twice")

	releaseAgain, ok := manager.AcquireOutbound(old)
	if releaseAgain != nil {
		releaseAgain()
	}
	require.False(t, ok, "after the deferred close the object is no longer leasable")
}
