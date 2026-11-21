package outbound

// The eviction WINDOW: what happens to a demand, and to a concurrent Close, while an expansion is
// being torn down.
//
// A teardown is not atomic. Evict marks the object retiring, then runs the protocol's cleanups outside
// the object's lock, then settles the state. Two behaviours live in that window and both were wrong:
//
//   - a demand was refused with os.ErrClosed even though the node was about to be buildable again;
//   - a Close that landed there returned nil and was then UNDONE by the eviction's tail, which put the
//     node back to never-built - a terminal state that silently un-terminated itself.
//
// The tests below pin the window OPEN with a blocked scope cleanup, so the interleaving is exact
// rather than hoped for.

import (
	"context"
	"os"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// evictableFixture builds one expanded node and returns it with the gate that holds its teardown open.
func evictableFixture(t *testing.T) (*deferredFixture, *LazyOutbound[string]) {
	t.Helper()
	fixture := newDeferredFixture(t)
	fixture.cleanupGate = &dialGate{entered: make(chan struct{}), release: make(chan struct{})}
	lazy := fixture.create(t, "window-node")
	fixture.start(t)
	conn, err := lazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.Equal(t, 1, lazy.Builds())
	return fixture, lazy
}

// TestEvictionWindowServesTheNextDemand is the contract chosen for A3: a demand that arrives inside an
// eviction waits for the teardown and then rebuilds. It is NOT refused, because the node was about to
// become usable again and the refusal surfaced as "file already closed" to a real caller.
func TestEvictionWindowServesTheNextDemand(t *testing.T) {
	fixture, lazy := evictableFixture(t)

	eviction := make(chan bool, 1)
	go func() { eviction <- lazy.Evict() }()
	<-fixture.cleanupGate.entered
	require.Equal(t, LazyStateRetiring, lazy.State(), "the teardown is in progress")

	demand := make(chan dialOutcome, 1)
	go func() {
		conn, err := lazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
		demand <- dialOutcome{conn: conn, err: err}
	}()
	select {
	case outcome := <-demand:
		t.Fatalf("a demand returned inside the eviction window (%v): it must wait for the teardown "+
			"rather than be refused, because the node is about to be buildable again", outcome.err)
	case <-time.After(100 * time.Millisecond):
	}

	close(fixture.cleanupGate.release)
	require.True(t, <-eviction)

	var outcome dialOutcome
	select {
	case outcome = <-demand:
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting demand never completed after the eviction settled")
	}
	require.NoError(t, outcome.err, "the wait must end in a successful rebuild, never in os.ErrClosed")
	require.NotNil(t, outcome.conn)
	require.NoError(t, outcome.conn.Close())
	require.Equal(t, 2, lazy.Builds(), "the waiting demand rebuilt the node")
	require.Equal(t, LazyStateReady, lazy.State())
}

// TestEvictionWindowDemandIsBoundedByItsContext: the wait is bounded by the CALLER's context, so a slow
// teardown can never turn an eviction into a stalled dial. The error is the caller's deadline, not a
// closed-node error.
func TestEvictionWindowDemandIsBoundedByItsContext(t *testing.T) {
	fixture, lazy := evictableFixture(t)
	defer close(fixture.cleanupGate.release)

	eviction := make(chan bool, 1)
	go func() { eviction <- lazy.Evict() }()
	<-fixture.cleanupGate.entered

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	conn, err := lazy.DialContext(ctx, "tcp", M.Socksaddr{})
	elapsed := time.Since(started)
	require.Nil(t, conn)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NotErrorIs(t, err, os.ErrClosed)
	require.GreaterOrEqual(t, elapsed, 100*time.Millisecond)
	require.Less(t, elapsed, 5*time.Second, "the wait must be the CALLER's budget, not the teardown's")
}

// TestCloseInsideTheEvictionWindowStaysTerminal is the A1 contract at the object layer: whatever a
// Close and an Evict do to each other inside the window, the object ends closed and never builds
// again.
func TestCloseInsideTheEvictionWindowStaysTerminal(t *testing.T) {
	fixture, lazy := evictableFixture(t)

	eviction := make(chan bool, 1)
	go func() { eviction <- lazy.Evict() }()
	<-fixture.cleanupGate.entered
	require.Equal(t, LazyStateRetiring, lazy.State())

	require.NoError(t, lazy.Close(), "Close during the eviction window is accepted")
	close(fixture.cleanupGate.release)
	require.True(t, <-eviction)
	require.Equal(t, LazyStateClosed, lazy.State(),
		"the eviction's tail must settle on closed, not put a closed node back to never-built")

	conn, err := lazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.Nil(t, conn)
	require.ErrorIs(t, err, os.ErrClosed)
	require.Equal(t, 1, lazy.Builds(), "a closed node never rebuilds")
	_, expanded := lazy.Built()
	require.False(t, expanded)
}

// TestEvictionRefusedWhileAnOpenConnectionExists is the "active connection" half of the reclaim
// contract at the manager level: the connection outlives the hand-off, and the node stays in use until
// it is closed.
func TestEvictionRefusedWhileAnOpenConnectionExists(t *testing.T) {
	fixture := newDeferredFixture(t)
	lazy := fixture.create(t, "streaming-node")
	fixture.start(t)

	conn, err := lazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	observation := lazy.ExpansionObservation()
	require.True(t, observation.Expanded)
	require.Equal(t, 1, observation.Conns)

	require.False(t, lazy.Evict(), "an open connection is not idle")
	require.NoError(t, conn.Close())
	require.True(t, lazy.Evict())
	require.Equal(t, 0, lazy.ExpansionObservation().Conns)
}
