package dialer

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/control"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// A dial that begins DURING a transition and completes AFTER the commit, on a real socket.
//
// # What was missing
//
// `TestEveryDialEntryPointSharesTheTransitionContract` pins the transition rule on the PREDICATE: it captures
// while unsettled, commits, and asserts the capture is still refused - because a commit settles the network
// without advancing the epoch, so an epoch comparison alone finds nothing changed. What no test did was put a
// socket in that window. The predicate can be right while the path that uses it forgets to, and the two
// questions are answered by different lines of code.
//
// So this drives the same interleaving through the real dial path, with the production Control hook holding
// the dial inside connect while the transition commits around it.
//
// # Why the answer must be a refusal
//
// The capture records that the dial BEGAN unsettled. Once the transition commits, the network is settled and
// the epoch has not moved - so every check that re-reads the CURRENT state passes. Only the record of the
// start state rejects it, and that refusal is the correct transient failure: the caller retries and the next
// attempt is taken in a settled network and succeeds normally.

// newTransitionDuringHarness wires a DefaultDialer to a manager that reports BOTH an epoch and a settled
// flag, which is the shape that can express "the transition is pending".
func newTransitionDuringHarness(t *testing.T) (*epochHarness, *atomic.Bool) {
	t.Helper()
	target := loopbackTarget(t)

	epoch := &atomic.Uint64{}
	epoch.Store(1)
	stable := &atomic.Bool{}
	stable.Store(false) // the capture must be taken DURING the transition
	manager := &epochManager{}

	ctx := service.ContextWith[adapter.ConnectionManager](context.Background(), manager)
	ctx = service.ContextWith[adapter.NetworkManager](ctx, &stabilityNetworkManager{
		epoch:  epoch,
		stable: stable,
		finder: control.NewDefaultInterfaceFinder(),
	})
	dialer, err := NewDefault(ctx, option.DialerOptions{})
	require.NoError(t, err)

	return &epochHarness{dialer: dialer, epoch: epoch, manager: manager, target: target}, stable
}

// TestADialThatBeginsDuringATransitionIsRefusedAfterItCommits is the interleaving.
func TestADialThatBeginsDuringATransitionIsRefusedAfterItCommits(t *testing.T) {
	harness, stable := newTransitionDuringHarness(t)
	entered, release := harness.gateDial()
	outcome := harness.dial(context.Background())

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the dial never entered the dial path")
	}

	// The transition COMMITS while the dial is in flight. The epoch deliberately does not move: a commit
	// settles the state, it does not start a new generation. If this test passed only because the epoch
	// advanced, it would be pinning the wrong mechanism - the one the DURING case exists to cover.
	harness.epoch.Store(1)
	stable.Store(true)

	close(release)

	received := <-outcome
	require.Nil(t, received.conn,
		"the caller received a connection produced while ownership was unresolved")
	require.Error(t, received.err,
		"a dial taken during a transition must be refused even though the network is settled by the time "+
			"it completes: the connection observed the network being left")
	require.Contains(t, received.err.Error(), "network changed",
		"and the refusal must be the transient one the caller retries, not a timeout or a dial failure")
	// It stays TRACKED, and that is the design rather than an oversight: tracking happens BEFORE the
	// hand-over check, precisely so that a reset which lands after the track can close the socket even when
	// this check rejects it. Dropping it on refusal would leave a real socket that nobody can close - the
	// one direction the ordering in stillCurrentEpoch rules out.
	require.Equal(t, 1, harness.manager.trackedCount())
	harness.manager.CloseAll()
	require.EqualValues(t, 1, harness.manager.closed.Load(),
		"and the manager can still close it, which is what the tracking order buys")
}

// TestADialThatBeginsAfterTheCommitSucceeds is the control, and it keeps the refusal above from being
// "everything fails after a transition".
func TestADialThatBeginsAfterTheCommitSucceeds(t *testing.T) {
	harness, stable := newTransitionDuringHarness(t)

	// Commit first, then dial: this is the ordinary post-transition case.
	stable.Store(true)

	conn, err := harness.dialer.DialContext(context.Background(), "tcp", M.ParseSocksaddr(harness.target))
	require.NoError(t, err)
	require.NotNil(t, conn)
	conn.Close()
	require.Equal(t, 1, harness.manager.trackedCount(), "and the connection is tracked normally")
}
