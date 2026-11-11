package group

// P02C wiring — the stable/failover policy drives a REAL health round.
//
// The policy core was a pure state machine with no production caller. This file wires it into
// URLTestGroup.runHealthRound and pins the behaviour that matters:
//
//   - the default (disabled) policy is byte-for-byte the old behaviour: every member, every round;
//   - a healthy stable member makes a periodic round probe ONLY that member;
//   - a forced/manual round always measures every member;
//   - the stable member failing widens the next rounds and the best measured alternative is
//     committed through the same selection state readers use;
//   - once the alternative settles it becomes the stable member and the probe set narrows to it;
//   - a same-tag replacement keeps the slot.

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/interrupt"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// switchableProbeOutbound can be made to fail on demand, and counts its dials.
type switchableProbeOutbound struct {
	adapter.Outbound
	tag    string
	dials  atomic.Int32
	failed atomic.Bool
	target string
}

func newSwitchableProbeOutbound(tag string, target string) *switchableProbeOutbound {
	return &switchableProbeOutbound{tag: tag, target: target}
}

func (o *switchableProbeOutbound) Type() string      { return "switchable-probe" }
func (o *switchableProbeOutbound) Tag() string       { return o.tag }
func (o *switchableProbeOutbound) Network() []string { return []string{N.NetworkTCP} }

func (o *switchableProbeOutbound) DialContext(ctx context.Context, network string, _ M.Socksaddr) (net.Conn, error) {
	o.dials.Add(1)
	if o.failed.Load() {
		// A plain failure, NOT net.ErrClosed: that one is classified as this process tearing the\n
		// connection down, which the round deliberately treats as "not measured" rather than as a\n
		// failure of the node.\n
		return nil, errors.New("injected probe failure")
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, o.target)
}

func (o *switchableProbeOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

func (o *switchableProbeOutbound) dialCount() int { return int(o.dials.Load()) }

// newFailoverWiringGroup builds the minimum a health round needs, without starting the group's
// tickers: the round is driven directly so the test owns the sequence.
func newFailoverWiringGroup(t *testing.T, policy urltest.FailoverPolicy, outbounds ...adapter.Outbound) (*URLTestGroup, *urltest.HistoryStorage, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	link := server.URL + "/generate_204"
	target, err := urltest.ParseMeasurementTarget(link)
	require.NoError(t, err)

	history := urltest.NewHistoryStorage()
	t.Cleanup(func() { history.Close() })
	byTag := make(map[string]adapter.Outbound, len(outbounds))
	cast := make([]adapter.Outbound, len(outbounds))
	for index, outbound := range outbounds {
		byTag[outbound.Tag()] = outbound
		cast[index] = outbound
	}
	group := &URLTestGroup{
		outbound:  &taggedOutboundManager{byTag: byTag},
		history:   history,
		logger:    log.NewNOPFactory().NewLogger("failover-wiring"),
		outbounds: cast,
		link:      link,
		expected:  urltest.ExpectedStatus{},
		scope:     urltest.MeasurementScope{URL: target.ScopeURL, Expected: urltest.ExpectedStatus{}.Canonical()},
		interval:  0,
		tolerance: 50,
		// Production always installs this; a selection change interrupts live connections through it.
		interruptGroup: &interrupt.Group{},
		failover:       urltest.NewFailoverState(policy, ""),
	}
	return group, history, server
}

func failoverTestPolicy() urltest.FailoverPolicy {
	return urltest.FailoverPolicy{WidenAfter: 1, SettleAfter: 2, RecoverAfter: 1, ToleranceMs: 1}
}

func selectedTagOf(group *URLTestGroup) string {
	state := group.selected.Load()
	if state == nil || state.tcp == nil {
		return ""
	}
	return state.tcp.Tag()
}

// TestTheDefaultPolicyKeepsEveryMemberProbed is the compatibility contract.
func TestTheDefaultPolicyKeepsEveryMemberProbed(t *testing.T) {
	a := newSwitchableProbeOutbound("a", "")
	b := newSwitchableProbeOutbound("b", "")
	group, _, server := newFailoverWiringGroup(t, urltest.FailoverPolicy{Disabled: true}, a, b)
	target := server.Listener.Addr().String()
	a.target, b.target = target, target

	_, err := group.runHealthRound(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, a.dialCount())
	require.Equal(t, 1, b.dialCount())

	_, err = group.runHealthRound(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 2, a.dialCount(), "a disabled policy probes every member every round")
	require.Equal(t, 2, b.dialCount())
}

// TestAPeriodicRoundProbesOnlyTheStableMember is the power saving, on a real round.
func TestAPeriodicRoundProbesOnlyTheStableMember(t *testing.T) {
	a := newSwitchableProbeOutbound("a", "")
	b := newSwitchableProbeOutbound("b", "")
	c := newSwitchableProbeOutbound("c", "")
	group, _, server := newFailoverWiringGroup(t, failoverTestPolicy(), a, b, c)
	target := server.Listener.Addr().String()
	a.target, b.target, c.target = target, target, target

	// The first round has no stable member yet, so it measures everything and adopts one.
	_, err := group.runHealthRound(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, a.dialCount())
	require.Equal(t, 1, b.dialCount())
	require.Equal(t, 1, c.dialCount())
	require.Equal(t, "a", selectedTagOf(group), "ties are broken by tag, deterministically")
	require.Equal(t, "a", group.failover.Stable())

	_, err = group.runHealthRound(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 2, a.dialCount())
	require.Equal(t, 1, b.dialCount(), "a healthy stable member means the other members are not probed")
	require.Equal(t, 1, c.dialCount())
}

// TestAManualRoundMeasuresEveryMember: an explicit test is never narrowed.
func TestAManualRoundMeasuresEveryMember(t *testing.T) {
	a := newSwitchableProbeOutbound("a", "")
	b := newSwitchableProbeOutbound("b", "")
	group, _, server := newFailoverWiringGroup(t, failoverTestPolicy(), a, b)
	target := server.Listener.Addr().String()
	a.target, b.target = target, target

	_, err := group.runHealthRound(context.Background(), false)
	require.NoError(t, err)
	_, err = group.runHealthRound(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, 2, a.dialCount())
	require.Equal(t, 2, b.dialCount(), "a forced/manual round measures every member")
}

// TestConfirmedDegradationWidensFailsOverAndSettles drives the whole wired sequence.
func TestConfirmedDegradationWidensFailsOverAndSettles(t *testing.T) {
	a := newSwitchableProbeOutbound("a", "")
	b := newSwitchableProbeOutbound("b", "")
	group, _, server := newFailoverWiringGroup(t, failoverTestPolicy(), a, b)
	target := server.Listener.Addr().String()
	a.target, b.target = target, target

	// Round 1: both healthy, a becomes stable.
	_, err := group.runHealthRound(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, "a", selectedTagOf(group))

	// Round 2: a fails. The failure deletes a's evidence, so the group's EXISTING selection rule
	// (best evidence) already moves traffic to b; the policy keeps a as its stable member until the
	// alternative has actually been measured, which is the distinction this wiring preserves.
	a.failed.Store(true)
	_, err = group.runHealthRound(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, "a", group.failover.Stable(), "the policy does not abandon a member it has not replaced yet")
	require.Equal(t, "b", selectedTagOf(group), "the group's evidence rule cannot keep a member with no evidence")

	// Round 3: widened, b is measured, traffic moves to b immediately.
	bBefore := b.dialCount()
	_, err = group.runHealthRound(context.Background(), false)
	require.NoError(t, err)
	require.Greater(t, b.dialCount(), bBefore, "the widened round probes the alternative")
	require.Equal(t, "b", selectedTagOf(group), "a failed stable member yields to the measured alternative")

	// Round 4: b holds the position again, so it settles as the stable member.
	_, err = group.runHealthRound(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, "b", group.failover.Stable())

	// Round 5: the probe set is now only b; a is no longer dialled.
	aBefore := a.dialCount()
	_, err = group.runHealthRound(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, aBefore, a.dialCount(), "after settling, the retired member is not probed")
	require.Equal(t, "b", selectedTagOf(group))
}

// TestAReplacedNodeKeepsTheStableSlot: a provider replaces the object under the same tag.
func TestAReplacedNodeKeepsTheStableSlot(t *testing.T) {
	a := newSwitchableProbeOutbound("a", "")
	b := newSwitchableProbeOutbound("b", "")
	group, _, server := newFailoverWiringGroup(t, failoverTestPolicy(), a, b)
	target := server.Listener.Addr().String()
	a.target, b.target = target, target
	_, err := group.runHealthRound(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, "a", group.failover.Stable())

	replacement := newSwitchableProbeOutbound("a", target)
	group.outbounds[0] = replacement
	group.outbound = &taggedOutboundManager{byTag: map[string]adapter.Outbound{"a": replacement, "b": b}}

	_, err = group.runHealthRound(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, "a", group.failover.Stable(), "the tag keeps the slot across a replacement")
	require.Equal(t, 1, replacement.dialCount(), "the replacement is the member that is probed")
	require.Equal(t, 1, b.dialCount())
}
