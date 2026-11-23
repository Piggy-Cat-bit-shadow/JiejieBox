package group

// The DISPLAY-ONLY round (a user pressing "test" on a Selector or on a plain node) had no coverage of
// its terminal state, only the health-mode URLTest round did. It is a different mode on purpose: it
// must publish the node's display state without touching selection evidence. This drives the real
// round function against a real local target, with one member that succeeds and one that fails, so
// the assertions are about what a client would read rather than about an internal map.

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"

	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// refusingProbeOutbound fails like a node whose endpoint refuses the connection: a real dial to a
// loopback port nothing listens on. It is a failure of the node path, not of the probe target.
type refusingProbeOutbound struct {
	adapter.Outbound
	tag string
}

func (o *refusingProbeOutbound) Type() string      { return "refusing-probe" }
func (o *refusingProbeOutbound) Tag() string       { return o.tag }
func (o *refusingProbeOutbound) Network() []string { return []string{"tcp"} }

func (o *refusingProbeOutbound) DialContext(ctx context.Context, network string, _ M.Socksaddr) (net.Conn, error) {
	var dialer net.Dialer
	dialer.Timeout = 2 * time.Second
	return dialer.DialContext(ctx, "tcp", "127.0.0.1:1")
}

func (o *refusingProbeOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// TestADisplayOnlyRoundPublishesBothTerminalStates is the missing half of the display path: a manual
// round must leave a terminal state for EVERY member it really measured, success and failure alike.
//
// Before this test, a Selector's "test all" could leave a failed member with no record at all, which
// a client can only render as "never tested" - the exact confusion the feature exists to remove.
func TestADisplayOnlyRoundPublishesBothTerminalStates(t *testing.T) {
	server := newProbeTarget(t)
	link := server.URL + "/generate_204"

	history := urltest.NewHistoryStorage()
	t.Cleanup(func() { _ = history.Close() })

	healthy := newSharingProbeOutbound("healthy")
	close(healthy.gate) // let it dial the real local target
	refusing := &refusingProbeOutbound{tag: "refusing"}

	manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{
		"healthy":  healthy,
		"refusing": refusing,
	}}
	URLTestOutboundsWithTarget(context.Background(), manager, history,
		log.NewNOPFactory().NewLogger("display-attack"),
		[]adapter.Outbound{healthy, refusing}, link, urltest.ExpectedStatus{}, 0, true,
		TestHistoryDisplayOnly)

	// 1. The success: a delay with a completion time, and NOT a timeout.
	success, loaded := history.LoadDisplayResult("healthy")
	require.True(t, loaded, "a measured success must have a display terminal state")
	require.False(t, success.Failed(), "a successful probe must not be reported as a failure")
	require.Positive(t, success.Delay)
	require.False(t, success.CompletedAt.IsZero())

	// 2. The failure: zero delay WITH a completion time. This is the timeout a client renders.
	failure, loaded := history.LoadDisplayResult("refusing")
	require.True(t, loaded,
		"a member that failed during a display round must have a terminal state, not merely no record")
	require.True(t, failure.Failed())
	require.Zero(t, failure.Delay)
	require.False(t, failure.CompletedAt.IsZero(), "the failure must carry its completion time")

	// 3. The display success layer keeps its own meaning: the successful member is there, the failed
	//    one is not (it never succeeded).
	require.NotNil(t, history.LoadURLTestHistory("healthy"))
	require.Nil(t, history.LoadURLTestHistory("refusing"))

	// 4. And the health layer is untouched: a display round is not selection evidence.
	scope, err := urltest.NewMeasurementScope(link, urltest.ExpectedStatus{})
	require.NoError(t, err)
	require.Nil(t, history.LoadURLTestHistoryFor("healthy", scope),
		"a manual display round must not create health evidence")
	require.Nil(t, history.LoadURLTestHistoryFor("refusing", scope),
		"a manual display round must not create health evidence")
}

// targetFailingProbeOutbound fails the way a PROXY-TARGET failure is reported: the connection to the
// proxy worked and the destination behind it failed, which the dialer marks with
// adapter.TargetFailureFacts. The distinction matters because the group must NOT treat it as health
// evidence for the member - while still showing the user that their probe failed.
type targetFailingProbeOutbound struct {
	adapter.Outbound
	tag string
}

func (o *targetFailingProbeOutbound) Type() string      { return "target-failing-probe" }
func (o *targetFailingProbeOutbound) Tag() string       { return o.tag }
func (o *targetFailingProbeOutbound) Network() []string { return []string{"tcp"} }

func (o *targetFailingProbeOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	var dialer net.Dialer
	dialer.Timeout = 2 * time.Second
	_, err := dialer.DialContext(ctx, "tcp", destination.String())
	if err == nil {
		return nil, adapter.WrapFailure(E.New("target unreachable"), adapter.TargetFailureFacts())
	}
	return nil, adapter.WrapFailure(err, adapter.TargetFailureFacts())
}

func (o *targetFailingProbeOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// TestATargetSideFailureIsShownButNotRecordedAsHealth pins the branch the general failure test does
// NOT reach: a failure whose facts say the destination behind the proxy failed.
//
// The two questions have different answers and this test is what keeps them apart:
//   - "is this member healthy" -> no verdict, the last valid measurement stands (nothing is deleted);
//   - "did the user's test succeed" -> no, so the display says timeout.
//
// It exists because a mutation removing the display write in that branch survived every other test:
// a status-mismatch error carries NO failure facts, so the "wrong answer" test was exercising the
// general failure path while claiming to cover this one.
func TestATargetSideFailureIsShownButNotRecordedAsHealth(t *testing.T) {
	server := newProbeTarget(t)
	link := server.URL + "/generate_204"

	history := urltest.NewHistoryStorage()
	t.Cleanup(func() { _ = history.Close() })
	scope, err := urltest.NewMeasurementScope(link, urltest.ExpectedStatus{})
	require.NoError(t, err)

	// A previous valid measurement for this target, which the target-side failure must NOT delete.
	history.StoreHealthHistory("member", scope, &adapter.URLTestHistory{Time: time.Now().Add(-time.Minute), Delay: 41})

	outbound := &targetFailingProbeOutbound{tag: "member"}
	manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{"member": outbound}}
	URLTestOutboundsWithTarget(context.Background(), manager, history,
		log.NewNOPFactory().NewLogger("target-failure-attack"),
		[]adapter.Outbound{outbound}, link, urltest.ExpectedStatus{}, 0, true, TestHistoryDisplayOnly)

	// The display shows the failure...
	result, loaded := history.LoadDisplayResult("member")
	require.True(t, loaded, "a target-side failure is still a failed probe for display")
	require.True(t, result.Failed())
	require.False(t, result.CompletedAt.IsZero())

	// ...and the health evidence survives untouched.
	health := history.LoadURLTestHistoryFor("member", scope)
	require.NotNil(t, health, "a target-side failure must not delete the member's health evidence")
	require.EqualValues(t, 41, health.Delay)
}
