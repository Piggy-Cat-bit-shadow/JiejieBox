package provider

// The provider close protocol: Close is a terminal state, and an apply either commits entirely
// before it or is refused entirely and reclaims what it installed.
//
// The tests below pin the four interleavings with barriers, never with sleeps: a hook parks the
// apply at one exact point (inside the constructor, after a manager replacement, before the batch
// commit), the test starts Close, waits until the terminal phase is published, then releases the
// apply. Every assertion after that is about state, not about scheduling order.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func waitForUpdate(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("UpdateNodes did not return")
		return nil
	}
}

func waitForClose(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return")
		return nil
	}
}

// TestProviderCannotPublishAfterClose is the formal regression for the reviewer's probe: a call
// that starts after Close returned must be refused at the entry, construct nothing, publish no
// snapshot and install no manager outbound.
func TestProviderCannotPublishAfterClose(t *testing.T) {
	fixture := newRecordFixture(t)
	require.NoError(t, fixture.provider.Close())
	buildsAfterClose := fixture.builds.Load()

	err := fixture.provider.UpdateNodes(recordNodes(map[string]int{"alpha": 1}, nil, nil))
	require.Error(t, err)
	require.ErrorContains(t, err, "closed")
	require.Equal(t, buildsAfterClose, fixture.builds.Load(),
		"a closed provider must refuse before constructing a candidate")
	require.Nil(t, fixture.provider.currentSnapshot(),
		"a closed provider must not publish a snapshot")
	_, found := fixture.manager.Outbound("subscription/alpha")
	require.False(t, found, "a closed provider must not install a manager outbound")
	require.Zero(t, fixture.closes.Load(), "nothing was constructed, so no object may be closed")
}

// TestProviderCloseDuringCandidateConstructionAbortsApply is interleaving 2: Close starts while a
// node is being constructed. Close cancels first, the constructor observes the cancellation, and
// the apply abandons the whole batch - nothing reaches the manager or the snapshot.
func TestProviderCloseDuringCandidateConstructionAbortsApply(t *testing.T) {
	fixture := newRecordFixture(t)
	buildEntered := make(chan string, 1)
	fixture.hooks.setBuild(func(ctx context.Context, tag string) error {
		select {
		case buildEntered <- tag:
		default:
		}
		// This constructor honours cancellation, which is the contract Close relies on: it cancels
		// BEFORE draining, so this returns instead of parking provider teardown forever.
		<-ctx.Done()
		return ctx.Err()
	})

	updateDone := make(chan error, 1)
	go func() {
		updateDone <- fixture.provider.UpdateNodes(recordNodes(map[string]int{"alpha": 1}, nil, nil))
	}()
	select {
	case tag := <-buildEntered:
		require.Equal(t, "subscription/alpha", tag)
	case <-time.After(10 * time.Second):
		t.Fatal("the candidate construction never started")
	}

	require.NoError(t, fixture.provider.Close(),
		"Close must not wait for a construction that honours the cancellation")
	require.Error(t, waitForUpdate(t, updateDone))
	require.Nil(t, fixture.provider.currentSnapshot())
	_, found := fixture.manager.Outbound("subscription/alpha")
	require.False(t, found, "a cancelled candidate must never be installed")
	require.Equal(t, providerPhaseClosed, fixture.provider.phaseState())
}

// TestProviderCloseAfterManagerInstallReclaimsCandidate is interleaving 3 (replacement published
// by the manager, provider publication not yet made): the apply's candidate is reclaimed, the
// previous record generation stays readable through its lease, and both the previous object and
// the candidate are released exactly once.
func TestProviderCloseAfterManagerInstallReclaimsCandidate(t *testing.T) {
	fixture := newRecordFixture(t)
	require.NoError(t, fixture.provider.UpdateNodes(recordNodes(map[string]int{"alpha": 1}, nil, nil)))
	oldLease := fixture.provider.acquireSnapshotLease()
	require.NotNil(t, oldLease)
	oldRef, ok := oldLease.Ref("subscription/alpha")
	require.True(t, ok)
	oldStore := oldRef.Store()
	oldPayload, ok := oldLease.Payload("subscription/alpha")
	require.True(t, ok)
	oldGeneration := oldLease.Generation()

	installed := make(chan string, 1)
	release := make(chan struct{})
	fixture.provider.testAfterNodeInstall = func(tag string) {
		select {
		case installed <- tag:
		default:
		}
		<-release
	}

	updateDone := make(chan error, 1)
	go func() {
		updateDone <- fixture.provider.UpdateNodes(recordNodes(map[string]int{"alpha": 2}, nil, nil))
	}()
	select {
	case tag := <-installed:
		require.Equal(t, "subscription/alpha", tag)
	case <-time.After(10 * time.Second):
		t.Fatal("the replacement never reached its install barrier")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- fixture.provider.Close() }()
	waitForCondition(t, func() bool {
		return fixture.provider.phaseState() != providerPhaseOpen
	}, "Close did not publish its closing phase while the apply was parked")
	close(release)

	require.Error(t, waitForUpdate(t, updateDone), "an apply refused at the publish point reports the refusal")
	require.NoError(t, waitForClose(t, closeDone))
	require.Nil(t, fixture.provider.currentSnapshot())
	_, found := fixture.manager.Outbound("subscription/alpha")
	require.False(t, found, "the provisional candidate must be reclaimed, not published")
	require.Nil(t, fixture.provider.acquireSnapshotLease(), "a closed provider publishes no new lease")

	// The old generation is still readable through the lease that observed it, and is freed only
	// when that lease is released.
	payload, ok := oldLease.Payload("subscription/alpha")
	require.True(t, ok, "a lease taken before Close must keep reading its generation")
	require.Equal(t, oldPayload, payload)
	require.Equal(t, oldGeneration, oldLease.Generation())
	require.False(t, oldStore.Freed())
	oldLease.Close()
	require.True(t, oldStore.Freed())

	// rev1 and the refused rev2 candidate are each closed exactly once. A double release would make
	// this 3, and a leak would make it 1.
	require.EqualValues(t, 2, fixture.closes.Load())
}

// TestProviderCloseBeforeSnapshotSwapRefusesPublish is interleaving 4: Close starts after every
// replacement was installed and before the batch (outbounds map + record store) is published. The
// gate refuses the batch, so no new store generation is built at all.
func TestProviderCloseBeforeSnapshotSwapRefusesPublish(t *testing.T) {
	fixture := newRecordFixture(t)
	require.NoError(t, fixture.provider.UpdateNodes(recordNodes(map[string]int{"alpha": 1}, nil, nil)))
	generationBefore := fixture.provider.snapshotGeneration
	oldLease := fixture.provider.acquireSnapshotLease()
	require.NotNil(t, oldLease)
	oldPayload, ok := oldLease.Payload("subscription/alpha")
	require.True(t, ok)

	entered := make(chan struct{})
	release := make(chan struct{})
	fixture.provider.testBeforeCommit = func() {
		close(entered)
		<-release
	}

	updateDone := make(chan error, 1)
	go func() {
		updateDone <- fixture.provider.UpdateNodes(recordNodes(map[string]int{"alpha": 2}, nil, nil))
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the apply never reached the publication gate")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- fixture.provider.Close() }()
	waitForCondition(t, func() bool {
		return fixture.provider.phaseState() != providerPhaseOpen
	}, "Close did not publish its closing phase while the apply was parked")
	close(release)

	require.Error(t, waitForUpdate(t, updateDone))
	require.NoError(t, waitForClose(t, closeDone))
	require.EqualValues(t, generationBefore, fixture.provider.snapshotGeneration,
		"a refused batch must not even build the next record generation")
	require.Nil(t, fixture.provider.currentSnapshot())
	_, found := fixture.manager.Outbound("subscription/alpha")
	require.False(t, found)

	payload, ok := oldLease.Payload("subscription/alpha")
	require.True(t, ok)
	require.Equal(t, oldPayload, payload)
	oldLease.Close()
	require.EqualValues(t, 2, fixture.closes.Load())
}

// TestProviderCloseBoundsAnUncancellableCandidate is the bounded-wait half of the protocol: the
// constructor ignores cancellation, so Close cannot drain it. Close must still return in bounded
// time, seal the provider, and the late candidate must be reclaimed by the apply when it unwedges.
// The provider never publishes the candidate: the commit gate was already closed.
func TestProviderCloseBoundsAnUncancellableCandidate(t *testing.T) {
	fixture := newRecordFixture(t)
	fixture.tolerateCloseError = true
	fixture.provider.closeDrainTimeout = 100 * time.Millisecond

	buildEntered := make(chan struct{}, 1)
	releaseConstruction := make(chan struct{})
	fixture.hooks.setBuild(func(context.Context, string) error {
		select {
		case buildEntered <- struct{}{}:
		default:
		}
		// Deliberately ignores the cancelled context: this is the construction the provider cannot
		// cancel, and it is the case the drain bound exists for.
		<-releaseConstruction
		return nil
	})

	updateDone := make(chan error, 1)
	go func() {
		updateDone <- fixture.provider.UpdateNodes(recordNodes(map[string]int{"alpha": 1}, nil, nil))
	}()
	select {
	case <-buildEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("the uncancellable candidate never started")
	}

	start := time.Now()
	closeErr := fixture.provider.Close()
	require.Error(t, closeErr, "a Close that could not drain must report it")
	require.Less(t, time.Since(start), 5*time.Second,
		"Close waited for the uncancellable construction instead of bounding the drain")
	require.Equal(t, providerPhaseClosed, fixture.provider.phaseState())
	require.Nil(t, fixture.provider.currentSnapshot())
	_, found := fixture.manager.Outbound("subscription/alpha")
	require.False(t, found, "the candidate cannot have been installed while its constructor is parked")

	// Let the construction finish. The manager publishes the object, then the apply observes the
	// closed gate and reclaims it before returning.
	close(releaseConstruction)
	require.Error(t, waitForUpdate(t, updateDone))
	waitForCondition(t, func() bool {
		_, stillPublished := fixture.manager.Outbound("subscription/alpha")
		return !stillPublished && fixture.closes.Load() == 1
	}, "the late candidate was not reclaimed after the construction finished")
}
