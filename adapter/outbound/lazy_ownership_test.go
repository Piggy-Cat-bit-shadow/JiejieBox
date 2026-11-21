package outbound

// Ownership tests for the deferred object: WHO releases a build result that will not be published,
// and WHAT keeps an expansion from being released while a hand-off is using it.
//
// Every assertion here is about a counted fact (build count, cleanup count, close count), never about
// a sleep-and-look-at-memory guess, because the defects being pinned are windows: they exist for the
// duration of one teardown and disappear under a scheduler that happens to be kind.

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"

	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

type dialOutcome struct {
	conn net.Conn
	err  error
}

// TestLatePlainBuildResultIsClosedExactlyOnce is the residual the reviewer reproduced: a builder that
// returns AFTER the node was closed, with an object that is closable but not an adapter.Lifecycle,
// must be closed exactly once. It used to be closed zero times (the early-retirement branch returned
// before the object had an owner), and the fix must not over-correct into a double close.
func TestLatePlainBuildResultIsClosedExactlyOnce(t *testing.T) {
	fixture := newDeferredFixture(t)
	fixture.buildBlocker = &dialGate{entered: make(chan struct{}), release: make(chan struct{})}
	fixture.plainRuntime = true
	lazy := fixture.create(t, "late-node")
	fixture.start(t)

	dialResult := make(chan dialOutcome, 1)
	go func() {
		conn, err := lazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
		dialResult <- dialOutcome{conn: conn, err: err}
	}()
	<-fixture.buildBlocker.entered
	require.Equal(t, LazyStateBuilding, lazy.State())
	require.NoError(t, lazy.Close(), "Close during a build must be accepted")

	close(fixture.buildBlocker.release)
	outcome := <-dialResult
	require.ErrorIs(t, outcome.err, os.ErrClosed, "a late result must not be published")
	require.Nil(t, outcome.conn)

	fixture.access.Lock()
	built := fixture.plainBuilt
	fixture.access.Unlock()
	require.NotNil(t, built)
	require.EqualValues(t, 1, built.closed.Load(),
		"the constructed object has no owner left, so it must be closed exactly once")
	require.Equal(t, LazyStateClosed, lazy.State())
	_, expanded := lazy.Built()
	require.False(t, expanded, "nothing was published")
}

// TestBuilderErrorWithAValueReleasesItOnce is the other ownership edge: a builder that returns both an
// object and an error. The lazy layer must not keep an object it will never publish.
func TestBuilderErrorWithAValueReleasesItOnce(t *testing.T) {
	fixture := newDeferredFixture(t)
	fixture.plainRuntime = true
	fixture.plainError = errors.New("builder failed after allocating")
	lazy := fixture.create(t, "failed-node")
	fixture.start(t)

	conn, err := lazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.Nil(t, conn)
	require.ErrorContains(t, err, "builder failed after allocating")
	require.NotErrorIs(t, err, os.ErrClosed, "a build failure is the builder's error, not a closed node")

	fixture.access.Lock()
	built := fixture.plainBuilt
	fixture.access.Unlock()
	require.NotNil(t, built)
	require.EqualValues(t, 1, built.closed.Load())
	require.Equal(t, LazyStateNeverBuilt, lazy.State(), "a failed build must stay retryable")

	// THE RETRY: the same node builds again and this time it is published. The builder now returns a
	// real runtime, so the retry also proves the failure left no half-built state behind.
	fixture.plainRuntime = false
	conn, err = lazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.Equal(t, 2, lazy.Builds())
	require.Equal(t, LazyStateReady, lazy.State())
}

// TestStartFailureReleasesTheStartedScopeExactlyOnce pins the third ownership edge: when Start fails,
// the per-build scope owns whatever the object already registered, and it is closed exactly once. The
// object itself is NOT also closed - the eager lifecycle's release does not do that either, and doing
// both would run a protocol's close path on top of the cleanups that already released the same
// resources.
func TestStartFailureReleasesTheStartedScopeExactlyOnce(t *testing.T) {
	fixture := newDeferredFixture(t)
	fixture.startError = errors.New("start refused at the second stage")
	lazy := fixture.create(t, "start-failure")
	fixture.start(t)

	conn, err := lazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.Nil(t, conn)
	require.ErrorContains(t, err, "start refused at the second stage")
	require.Equal(t, 1, lazy.Builds())
	require.Equal(t, LazyStateNeverBuilt, lazy.State())

	runtimes := fixture.runtimesSnapshot()
	require.Len(t, runtimes, 1)
	require.EqualValues(t, 1, runtimes[0].cleanupRuns.Load(),
		"the cleanup registered before the failing stage must run exactly once")

	// A repaired stage set makes the node usable; the failed build left nothing behind.
	fixture.startError = nil
	conn, err = lazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.Equal(t, 2, lazy.Builds())
	require.Equal(t, LazyStateReady, lazy.State())
}

// TestObjectLeaseRefusesAfterClose keeps the lease honest: a closed object answers ok=false, so the
// manager refuses the hand-off instead of counting a user on a node that can never serve.
func TestObjectLeaseRefusesAfterClose(t *testing.T) {
	fixture := newDeferredFixture(t)
	lazy := fixture.create(t, "closed-node")
	fixture.start(t)
	require.NoError(t, lazy.Close())

	release, ok := fixture.manager.AcquireOutbound(lazy)
	require.False(t, ok)
	release()
}

// TestManagerLeaseRefusesEvictionDuringTheHandoffWindow is the defect the reviewer found: the
// manager's lease lived only in the manager, so an eviction released the expansion between "the
// router obtained this object" and "the dial reached DialContext".
func TestManagerLeaseRefusesEvictionDuringTheHandoffWindow(t *testing.T) {
	fixture := newDeferredFixture(t)
	lazy := fixture.create(t, "leased-node")
	fixture.start(t)

	conn, err := lazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.Equal(t, 1, lazy.Builds())

	// The published NON-provider path is the important one: it returns a no-op manager release, and
	// before this fix it returned no object lease either - which is exactly the common case.
	release, ok := fixture.manager.AcquireOutbound(lazy)
	require.True(t, ok)
	require.False(t, lazy.Evict(),
		"the router holds this object: releasing the expansion now would fail the hand-off with a "+
			"closed runtime")

	// The hand-off proceeds while the lease is held.
	handoff, err := lazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	require.NoError(t, handoff.Close())
	require.Equal(t, 1, lazy.Builds(), "the lease must not force a rebuild")

	release()
	require.True(t, lazy.Evict(), "after the hand-off the node is idle again and reclaimable")
}

// TestNestedManagerLeasesAreCounted is the multi-group case: two chains can hold the same node, and the
// release of one must not make it evictable while the other is still using it.
func TestNestedManagerLeasesAreCounted(t *testing.T) {
	fixture := newDeferredFixture(t)
	lazy := fixture.create(t, "shared-node")
	fixture.start(t)
	conn, err := lazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	firstRelease, firstOK := fixture.manager.AcquireOutbound(lazy)
	secondRelease, secondOK := fixture.manager.AcquireOutbound(lazy)
	require.True(t, firstOK)
	require.True(t, secondOK)
	require.False(t, lazy.Evict())

	firstRelease()
	require.False(t, lazy.Evict(), "one of two hand-offs is still outstanding")
	secondRelease()
	require.True(t, lazy.Evict())
	// Both releases are idempotent, which is what lets a caller defer one and also release on error.
	firstRelease()
	secondRelease()
}
