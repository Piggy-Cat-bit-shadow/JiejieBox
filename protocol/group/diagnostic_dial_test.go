package group

// O02 — a DIAGNOSTIC dial runs the production dial and leaves the health evidence alone.
//
// The active connection test deliberately runs production's decision and dial, which is what makes it
// an observation instead of a model lookup. The cost of that honesty is that a group which records
// what a dial proved would record the PROBE's outcome as if the user's traffic had produced it: a
// failed probe demotes a member for every later flow, and a URLTest probe that fails clears the
// selection and asks for a recheck which can delete the member's measurement.
//
// These tests drive REAL groups (no fake group, no fake ledger) and assert both halves: the marked
// dial leaves the ledger and the selection alone, and the UNMARKED dial still writes them - so a
// future change that simply stops writing the ledger fails here instead of looking like a pass.

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// TestADiagnosticDialDoesNotPenaliseAMember is the loadbalance half.
//
// ENETUNREACH is the error that earns a GLOBAL penalty for an ordinary flow (see
// TestLoadBalanceFailoverRetriesAnAlternateAndMovesSelection), so it is the honest worst case for a
// probe: under the marker the retry still happens and the connection is still carried by the
// alternate, but the demotion the probe would have caused must not be recorded.
func TestADiagnosticDialDoesNotPenaliseAMember(t *testing.T) {
	harness := newFailoverHarness(t)
	nodeA := &stubOutbound{tag: "node-a"}
	nodeB := &stubOutbound{tag: "node-b"}
	nodeA.setDialError(syscall.ENETUNREACH)
	harness.add(nodeA, nodeB)
	group := harness.group(t, "lb", nil, option.LoadBalanceOutboundOptions{Strategy: "round_robin", Failover: true})

	diagnosticCtx := adapter.WithDiagnosticDial(failoverContext(t))
	conn, err := group.DialWithFailover(diagnosticCtx, failoverMetadata("example.com"), N.NetworkTCP, failoverDestination())
	require.NoError(t, err, "the probe runs the real production retry, so the alternate still carries it")
	require.NotNil(t, conn)
	require.NoError(t, conn.Close())

	require.Equal(t, 1, nodeA.dialCount())
	require.Equal(t, 1, nodeB.dialCount())
	require.Equal(t, 0, penaltyCountOf(group, "node-a"),
		"a probe must not demote the member carrying the user's traffic: the penalty ledger is what "+
			"the next selection reads, so recording a probe's failure there moves a live selection")
	require.Equal(t, 0, penaltyCountOf(group, "node-b"))

	// The negative control: the SAME failure through an UNMARKED dial still writes the ledger, so
	// the zero above is the marker's effect and not a group that stopped recording anything.
	ordinary := harness.group(t, "lb-control", nil, option.LoadBalanceOutboundOptions{Strategy: "round_robin", Failover: true})
	controlConn, controlErr := ordinary.DialWithFailover(failoverContext(t), failoverMetadata("example.com"), N.NetworkTCP, failoverDestination())
	require.NoError(t, controlErr)
	require.NotNil(t, controlConn)
	require.NoError(t, controlConn.Close())
	require.Equal(t, 1, penaltyCountOf(ordinary, "node-a"),
		"an ordinary flow must still be penalised: suppressing that too would be the opposite defect")
}

// TestADiagnosticDialRecordsTheMemberMove pins the fact the diagnostic reports as a recovery.
//
// The chain is written by the group WHILE the dial runs. Reading the group's selection afterwards
// would describe a path this request never took, which is the Q23 lesson applied to the dial side.
func TestADiagnosticDialRecordsTheMemberMove(t *testing.T) {
	harness := newFailoverHarness(t)
	nodeA := &stubOutbound{tag: "node-a"}
	nodeB := &stubOutbound{tag: "node-b"}
	nodeA.setDialError(syscall.ENETUNREACH)
	harness.add(nodeA, nodeB)
	group := harness.group(t, "lb", nil, option.LoadBalanceOutboundOptions{Strategy: "round_robin", Failover: true})

	path := adapter.NewDialPath()
	path.BeginDial()
	ctx := adapter.WithDiagnosticDial(adapter.WithDialPath(failoverContext(t), path))
	conn, err := group.DialWithFailover(ctx, failoverMetadata("example.com"), N.NetworkTCP, failoverDestination())
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	steps := path.Snapshot()
	require.Len(t, steps, 2, "the group handed this dial to exactly two members")
	require.Equal(t, "node-a", steps[0].Tag)
	require.Equal(t, "node-b", steps[1].Tag)
	require.Equal(t, "lb", steps[0].Group)
	require.Equal(t, "lb", steps[1].Group,
		"the step names the group that chose the member, which is what tells one group retrying from two nested groups")

	recoveredGroup, from, to, recovered := path.Result().Recovery()
	require.True(t, recovered, "the hand-off from the first member to the second must be observable")
	require.Equal(t, "lb", recoveredGroup)
	require.Equal(t, "node-a", from)
	require.Equal(t, "node-b", to)
}

// TestADiagnosticDialDoesNotClearAURLTestSelection is the URLTest half, and it is the more damaging
// one: a failed traffic dial clears the cached selection AND asks for a forced recheck, and that
// recheck probes with TestHistoryHealth - a failed probe DELETES the member's measurement. A probe
// would therefore rewrite the very evidence it is supposed to explain.
//
// The probe is GATED (the same fixture the traffic-failure test uses) so the two observations are
// sequential instead of racing: the state the failure leaves is read while the recheck is provably
// still in flight, and then the recheck is released and its verdict is pinned too. A test that
// asserted the selection after the recheck would be racing the product, not testing it.
func TestADiagnosticDialDoesNotClearAURLTestSelection(t *testing.T) {
	nodeA := &gatedProbeOutbound{
		tag: "node-a", dialErr: errors.New("connection refused"),
		probing: make(chan struct{}), released: make(chan struct{}),
	}
	nodeB := &stubOutbound{tag: "node-b"}

	group, storage := newGroupFixture(t, "https://probe.example/generate_204", nodeA, nodeB)
	storage.StoreURLTestHistoryFor("node-a", group.scope,
		&adapter.URLTestHistory{Time: time.Now(), Delay: 20})
	storage.StoreURLTestHistoryFor("node-b", group.scope,
		&adapter.URLTestHistory{Time: time.Now(), Delay: 500})

	group.performUpdateCheck()
	selected, _ := group.Select(N.NetworkTCP)
	require.NotNil(t, selected)
	require.Equal(t, "node-a", selected.Tag())

	wrapper := newTestURLTestWrapper(group, log.NewNOPFactory().NewLogger("urltest"))

	// From here the recheck a failure requests cannot complete before this test has looked at what
	// the failure itself did.
	nodeA.armed.Store(true)

	diagnosticCtx := adapter.WithDiagnosticDial(context.Background())
	_, err := wrapper.DialContext(diagnosticCtx, N.NetworkTCP, M.ParseSocksaddr("203.0.113.9:443"))
	require.Error(t, err, "the probe reports the real failure to its caller")
	require.Contains(t, err.Error(), "connection refused")

	state := group.selected.Load()
	require.NotNil(t, state)
	require.NotNil(t, state.tcp, "a probe must not clear the cached selection")
	require.Equal(t, "node-a", state.tcp.Tag())
	require.NotNil(t, storage.LoadURLTestHistoryFor("node-a", group.scope),
		"a probe must not be able to delete the measurement it is explaining")
	after, _ := group.Select(N.NetworkTCP)
	require.NotNil(t, after)
	require.Equal(t, "node-a", after.Tag())

	// The negative control: the same failure on an UNMARKED dial still clears the selection, so the
	// property above is the marker's effect rather than a group that never reacts to a failure.
	_, ordinaryErr := wrapper.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("203.0.113.9:443"))
	require.Error(t, ordinaryErr)
	ordinaryState := group.selected.Load()
	require.NotNil(t, ordinaryState)
	require.Nil(t, ordinaryState.tcp,
		"an ordinary failed flow must still invalidate the selection it just proved unusable")

	// And the recheck the ordinary failure asked for really decides - that is what makes the two
	// assertions above legitimate rather than a hope that nothing was going to happen anyway.
	close(nodeA.released)
	require.Eventually(t, func() bool {
		current, _ := group.Select(N.NetworkTCP)
		return current != nil && current.Tag() == "node-b"
	}, 5*time.Second, 10*time.Millisecond,
		"the released recheck must delete node-a's measurement and move the selection")
}

// TestADiagnosticMarkerIsNotSetByAccident guards the marker's zero value: every ordinary context -
// background, timeout, values - must read as "not a probe", or a production flow would silently stop
// being penalised.
func TestADiagnosticMarkerIsNotSetByAccident(t *testing.T) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.False(t, adapter.IsDiagnosticDial(nil))
	require.False(t, adapter.IsDiagnosticDial(context.Background()))
	require.False(t, adapter.IsDiagnosticDial(timeoutCtx))
	require.False(t, adapter.IsDiagnosticDial(context.WithValue(context.Background(), "diagnostic", true)),
		"a caller must not be able to forge the marker with a plain string key")
	require.True(t, adapter.IsDiagnosticDial(adapter.WithDiagnosticDial(context.Background())))
}
