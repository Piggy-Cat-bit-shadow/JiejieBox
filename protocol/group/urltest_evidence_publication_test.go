package group

// P1.1 — one publication decision covers the evidence write, the round's result and the failover
// observation.
//
// The reviewed failures were all the same shape in different places: an answer measured for one
// state was published as evidence for another one.
//
//  1. A probe that began on network A finished after the network had moved to B and back to A. Its
//     stamps were unchanged and its result was returned and stored, although the path it measured
//     is not the path that exists now. The network identity a publication needs is the reset
//     GENERATION (monotonic), not the environment fingerprint (which returns to its old value when a
//     device goes A -> B -> A).
//  2. A result the evidence layer REFUSED (its node was dropped while the probe ran) was still put
//     into the round's result map, so a caller that only reads the result saw a success.
//  3. The failover policy consumed exactly that entry as "the stable member answered", or - after the
//     result is gated - as "the stable member failed", because it inferred failure from
//     "not in the result and no evidence". A refused publication is neither: the member was not
//     measured for this state at all.

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// switchableNetworkFacts is a NetworkManager whose two identities move independently, which is what
// makes the difference between them observable: an A -> B -> A trip changes the environment back to
// its previous value but leaves the reset generation two steps ahead.
type switchableNetworkFacts struct {
	adapter.NetworkManager
	environment     atomic.Uint64
	resetGeneration atomic.Uint64
}

func (m *switchableNetworkFacts) NetworkEnvironment() uint64     { return m.environment.Load() }
func (m *switchableNetworkFacts) NetworkResetGeneration() uint64 { return m.resetGeneration.Load() }

func newNetworkFacts(environment uint64, generation uint64) *switchableNetworkFacts {
	facts := &switchableNetworkFacts{}
	facts.environment.Store(environment)
	facts.resetGeneration.Store(generation)
	return facts
}

// factsContext is the Box context a network manager is installed in.
func factsContext(facts *switchableNetworkFacts) context.Context {
	return service.ContextWith[adapter.NetworkManager](provenanceContext(), facts)
}

// holdableProbe is a probe that can be held inside its dial and can fail on demand, so a test can
// move the state a probe belongs to while the probe is genuinely in flight.
type holdableProbe struct {
	adapter.Outbound
	tag    string
	target string
	dials  atomic.Int32
	hold   atomic.Bool
	fail   atomic.Bool

	started chan struct{}
	gate    chan struct{}
}

func newHoldableProbe(tag string, target string) *holdableProbe {
	return &holdableProbe{tag: tag, target: target, started: make(chan struct{}, 8), gate: make(chan struct{})}
}

func (o *holdableProbe) Type() string      { return "holdable-probe" }
func (o *holdableProbe) Tag() string       { return o.tag }
func (o *holdableProbe) Network() []string { return []string{N.NetworkTCP} }

func (o *holdableProbe) DialContext(ctx context.Context, network string, _ M.Socksaddr) (net.Conn, error) {
	o.dials.Add(1)
	if o.hold.Load() {
		select {
		case o.started <- struct{}{}:
		default:
		}
		select {
		case <-o.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if o.fail.Load() {
		return nil, errors.New("injected probe failure")
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, o.target)
}

func (o *holdableProbe) dialCount() int { return int(o.dials.Load()) }

func awaitHeldDial(t *testing.T, probe *holdableProbe) {
	t.Helper()
	select {
	case <-probe.started:
	case <-time.After(10 * time.Second):
		t.Fatalf("the held probe %s never started; dials=%d", probe.tag, probe.dialCount())
	}
}

// TestAProbeFromANetworkThatChangedBackCannotPublish is failure 1, with the A -> B -> A trip that
// makes the fingerprint insufficient: the generation is two steps ahead of the probe's.
func TestAProbeFromANetworkThatChangedBackCannotPublish(t *testing.T) {
	target := probeTarget(t, 0)
	link := target.URL + "/generate_204"

	history := urltest.NewHistoryStorage()
	t.Cleanup(func() { history.Close() })
	// The probe lands on the listener, so its delay is a genuine success - only its STATE is stale.
	probe := newStagedProbeOutbound("node-a", target.Listener.Addr().String())
	facts := newNetworkFacts(0xA, 1)
	ctx := factsContext(facts)

	round := startHealthRound(ctx, history, []adapter.Outbound{probe}, link)
	awaitDial(t, probe, 1)

	// A -> B -> A while the probe is in flight. The fingerprint is back to its old value; the reset
	// generation is not.
	facts.environment.Store(0xB)
	facts.resetGeneration.Add(1)
	facts.environment.Store(0xA)
	facts.resetGeneration.Add(1)

	probe.release()
	result := <-round

	require.NotContains(t, result, "node-a",
		"a probe that began in an earlier network generation is not a result for this one")
	require.Nil(t, history.LoadURLTestHistoryFor("node-a", scopeFor(t, link)),
		"and it must not become evidence, even though its stamps were untouched")
}

// TestARefusedResultIsNotInTheRoundResult is failure 2: the evidence layer refuses the write because
// the node was dropped, and the round must not report the same result as if it were published.
func TestARefusedResultIsNotInTheRoundResult(t *testing.T) {
	target := probeTarget(t, 0)
	link := target.URL + "/generate_204"

	history := urltest.NewHistoryStorage()
	t.Cleanup(func() { history.Close() })
	probe := newStagedProbeOutbound("node-a", target.Listener.Addr().String())

	round := startHealthRound(provenanceContext(), history, []adapter.Outbound{probe}, link)
	awaitDial(t, probe, 1)
	history.DeleteURLTestHistory("node-a")
	probe.release()
	result := <-round

	require.NotContains(t, result, "node-a",
		"a result the evidence layer refused must not be reported as this round's measurement")
	require.Zero(t, history.HealthEntryCount())
}

// TestALateFailureFromAnotherNetworkCannotDeleteEvidence is the failure half of failure 1: an old
// failure has no more right to remove the new state's evidence than an old success has to write it.
func TestALateFailureFromAnotherNetworkCannotDeleteEvidence(t *testing.T) {
	target := probeTarget(t, 0)
	link := target.URL + "/generate_204"
	scope := scopeFor(t, link)

	history := urltest.NewHistoryStorage()
	t.Cleanup(func() { history.Close() })
	history.StoreHealthHistory("node-a", scope, &adapter.URLTestHistory{
		Time:  time.Now().Add(-time.Minute),
		Delay: 42,
	})

	probe := newHoldableProbe("node-a", target.Listener.Addr().String())
	probe.hold.Store(true)
	probe.fail.Store(true)
	facts := newNetworkFacts(0xA, 1)

	round := startHealthRound(factsContext(facts), history, []adapter.Outbound{probe}, link)
	awaitHeldDial(t, probe)

	// The network moves while the failing probe is in flight; the probe belongs to the old one.
	facts.environment.Store(0xB)
	facts.resetGeneration.Add(1)

	close(probe.gate)
	<-round

	require.NotNil(t, history.LoadURLTestHistoryFor("node-a", scope),
		"a failure measured on the previous network says nothing about this one and must not delete its evidence")
}

// TestADroppedResultIsNotAFailoverFailure is failure 3 on a real health round with the policy wired:
// the stable member's result is refused by the evidence layer, and the policy must not count that as
// the member failing (which would widen the probe set on the next round).
func TestADroppedResultIsNotAFailoverFailure(t *testing.T) {
	a := newHoldableProbe("a", "")
	b := newHoldableProbe("b", "")
	group, history, server := newFailoverWiringGroup(t, failoverTestPolicy(), a, b)
	t.Cleanup(func() { history.Close() })
	target := server.Listener.Addr().String()
	a.target, b.target = target, target

	// Round 1: both measured; ties are broken by tag, so "a" is the stable member.
	_, err := group.runHealthRound(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, "a", group.failover.Stable())

	// Round 2 probes only the stable member. Its result is dropped while the probe is in flight.
	a.hold.Store(true)
	type roundOutcome struct {
		result map[string]uint16
		err    error
	}
	round := make(chan roundOutcome, 1)
	go func() {
		result, roundErr := group.runHealthRound(context.Background(), false)
		round <- roundOutcome{result: result, err: roundErr}
	}()
	awaitHeldDial(t, a)
	history.DeleteURLTestHistory("a")
	close(a.gate)
	outcome := <-round
	require.NoError(t, outcome.err)

	require.NotContains(t, outcome.result, "a", "the dropped result is not published")
	require.Equal(t, 1, b.dialCount(), "round 2 probed only the stable member")

	// Round 3: had the refused result been counted as a failure, WidenAfter=1 would have widened the
	// set and this round would probe "b" again. A refused publication is not evidence of failure.
	_, err = group.runHealthRound(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, b.dialCount(),
		"a publication the evidence layer refused is not a failed measurement of the member")
}
