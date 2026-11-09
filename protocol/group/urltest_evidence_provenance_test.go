package group

// P1.1 — evidence publication must belong to the probe that produced it, and drops must be scoped
// to what they are about.
//
// These are the two failures the review reproduced against the previous fix:
//
//  1. A round that began AFTER DeleteURLTestHistory joined the probe that was already in flight and
//     then stored its result under its own new epochs, resurrecting the invalidated evidence. The
//     probe it received was measured before the drop.
//  2. The drop was a single storage-wide epoch, so deleting node A discarded node B's legitimate
//     result.
//
// The fix carries the probe's start time and the evidence epochs captured when the probe was
// REQUESTED through Measurement, and scopes the epochs per node and per (node, target).

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

func probeTarget(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if delay > 0 {
			time.Sleep(delay)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	return server
}

// stagedProbeOutbound dials a prepared list of destinations, one per dial, holding every dial until
// the gate opens. It reports each dial's start so a test can wait for a REAL probe.
type stagedProbeOutbound struct {
	adapter.Outbound
	tag          string
	destinations []string

	access sync.Mutex
	dials  int

	started chan int
	gate    chan struct{}
}

func newStagedProbeOutbound(tag string, destinations ...string) *stagedProbeOutbound {
	return &stagedProbeOutbound{
		tag:          tag,
		destinations: destinations,
		started:      make(chan int, 8),
		gate:         make(chan struct{}),
	}
}

func (o *stagedProbeOutbound) Type() string      { return "staged-probe" }
func (o *stagedProbeOutbound) Tag() string       { return o.tag }
func (o *stagedProbeOutbound) Network() []string { return []string{N.NetworkTCP} }

func (o *stagedProbeOutbound) DialContext(ctx context.Context, network string, _ M.Socksaddr) (net.Conn, error) {
	o.access.Lock()
	index := o.dials
	o.dials++
	destination := o.destinations[len(o.destinations)-1]
	if index < len(o.destinations) {
		destination = o.destinations[index]
	}
	o.access.Unlock()
	select {
	case o.started <- index + 1:
	default:
	}
	select {
	case <-o.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, destination)
}

func (o *stagedProbeOutbound) dialCount() int {
	o.access.Lock()
	defer o.access.Unlock()
	return o.dials
}

func (o *stagedProbeOutbound) release() { close(o.gate) }

func awaitDial(t *testing.T, outbound *stagedProbeOutbound, want int) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case index := <-outbound.started:
			if index == want {
				return
			}
		case <-deadline:
			t.Fatalf("the expected probe #%d never started; observed dials=%d", want, outbound.dialCount())
		}
	}
}

// provenanceContext is the context a Box installs: one measurement share and one coordinator. The
// share must be present for the "later round joins the in-flight probe" failure to be reproducible
// at all.
func provenanceContext() context.Context {
	return urltest.ContextWithCoordinator(
		urltest.ContextWithMeasurementShare(context.Background(), urltest.NewMeasurementShare()),
		urltest.NewCoordinator(8))
}
func startHealthRound(ctx context.Context, history *urltest.HistoryStorage, outbounds []adapter.Outbound, link string) <-chan map[string]uint16 {
	result := make(chan map[string]uint16, 1)
	go func() {
		result <- URLTestOutboundsWithTarget(ctx, &taggedOutboundManager{byTag: map[string]adapter.Outbound{}},
			history, log.NewNOPFactory().NewLogger("provenance"), outbounds, link,
			urltest.ExpectedStatus{}, 0, true, TestHistoryHealth)
	}()
	return result
}

func scopeFor(t *testing.T, link string) urltest.MeasurementScope {
	t.Helper()
	target, err := urltest.ParseMeasurementTarget(link)
	require.NoError(t, err)
	return urltest.MeasurementScope{URL: target.ScopeURL, Expected: urltest.ExpectedStatus{}.Canonical()}
}

// TestAPostDropRoundStartsItsOwnProbe is review failure 1.
func TestAPostDropRoundStartsItsOwnProbe(t *testing.T) {
	slow := probeTarget(t, 120*time.Millisecond)
	fast := probeTarget(t, 0)
	link := fast.URL + "/generate_204"

	history := urltest.NewHistoryStorage()
	// The first probe lands on the slow listener, the second on the fast one, so the stored delay
	// says which probe's evidence survived.
	outbound := newStagedProbeOutbound("node-a", slow.Listener.Addr().String(), fast.Listener.Addr().String())

	// ONE context, so both rounds reach the same share registry - the way two URLTest groups inside
	// one Box do.
	ctx := provenanceContext()
	firstRound := startHealthRound(ctx, history, []adapter.Outbound{outbound}, link)
	awaitDial(t, outbound, 1)

	// The first probe is in flight when its evidence is invalidated.
	history.DeleteURLTestHistory("node-a")

	secondRound := startHealthRound(ctx, history, []adapter.Outbound{outbound}, link)
	awaitDial(t, outbound, 2) // a NEW probe, not a joiner

	outbound.release()
	<-firstRound
	<-secondRound

	require.EqualValues(t, 2, outbound.dialCount(),
		"a round that begins after the drop must start its own probe instead of joining the invalidated one")
	stored := history.LoadURLTestHistoryFor("node-a", scopeFor(t, link))
	require.NotNil(t, stored)
	require.Less(t, stored.Delay, uint16(100),
		"the surviving evidence comes from the post-drop probe (the fast listener), not the invalidated one")
}

// TestADropOfOneNodeDoesNotInvalidateAnotherProbesEvidence is review failure 2.
func TestADropOfOneNodeDoesNotInvalidateAnotherProbesEvidence(t *testing.T) {
	target := probeTarget(t, 0)
	link := target.URL + "/generate_204"

	history := urltest.NewHistoryStorage()
	a := newStagedProbeOutbound("node-a", target.Listener.Addr().String())
	b := newStagedProbeOutbound("node-b", target.Listener.Addr().String())

	round := startHealthRound(provenanceContext(), history, []adapter.Outbound{a, b}, link)
	awaitDial(t, a, 1)
	awaitDial(t, b, 1)

	history.DeleteURLTestHistory("node-a")

	b.release()
	require.Eventually(t, func() bool {
		return history.LoadURLTestHistoryFor("node-b", scopeFor(t, link)) != nil
	}, 10*time.Second, time.Millisecond,
		"retiring node A must not discard node B's legitimate result")

	a.release()
	<-round
	require.Equal(t, 1, history.HealthEntryCount(),
		"node A's result was invalidated and must not be stored; only node B's evidence remains")
	require.Nil(t, history.LoadURLTestHistoryFor("node-a", scopeFor(t, link)))
}

// TestATargetDropDoesNotInvalidateAnotherTargetsProbe keeps the scope dimension precise.
func TestATargetDropDoesNotInvalidateAnotherTargetsProbe(t *testing.T) {
	target := probeTarget(t, 0)
	link := target.URL + "/generate_204"
	otherLink := target.URL + "/other_target"

	history := urltest.NewHistoryStorage()
	outbound := newStagedProbeOutbound("node-a", target.Listener.Addr().String())

	round := startHealthRound(provenanceContext(), history, []adapter.Outbound{outbound}, link)
	awaitDial(t, outbound, 1)

	// Only a DIFFERENT target's health is dropped for this node.
	history.DeleteHealthHistory("node-a", scopeFor(t, otherLink))

	outbound.release()
	<-round
	require.NotNil(t, history.LoadURLTestHistoryFor("node-a", scopeFor(t, link)),
		"invalidating one target's health must not discard another target's measurement")
}

// TestAHotReplacementOfTheSameTagKeepsTheInvalidationDirection covers the provider replacement
// shape: the retired object's probe is invalidated, the new object's result is stored.
func TestAHotReplacementOfTheSameTagKeepsTheInvalidationDirection(t *testing.T) {
	target := probeTarget(t, 0)
	link := target.URL + "/generate_204"

	history := urltest.NewHistoryStorage()
	retired := newStagedProbeOutbound("node-a", target.Listener.Addr().String())
	replacement := newStagedProbeOutbound("node-a", target.Listener.Addr().String())

	retiredRound := startHealthRound(provenanceContext(), history, []adapter.Outbound{retired}, link)
	awaitDial(t, retired, 1)
	history.DeleteURLTestHistory("node-a")
	replacementRound := startHealthRound(provenanceContext(), history, []adapter.Outbound{replacement}, link)
	awaitDial(t, replacement, 1)
	require.EqualValues(t, 1, retired.dialCount())

	replacement.release()
	<-replacementRound
	require.NotNil(t, history.LoadURLTestHistoryFor("node-a", scopeFor(t, link)))

	retired.release()
	<-retiredRound
	require.EqualValues(t, 1, retired.dialCount(), "the retired object's probe was not joined by the new round")
}

// TestEquivalentRoundsStillShareOneProbe keeps the original saving: without a drop, two equivalent
// health rounds share one probe and one dial.
func TestEquivalentRoundsStillShareOneProbe(t *testing.T) {
	target := probeTarget(t, 0)
	link := target.URL + "/generate_204"

	history := urltest.NewHistoryStorage()
	share := urltest.NewMeasurementShare()
	coordinator := urltest.NewCoordinator(3)
	ctx := urltest.ContextWithCoordinator(
		urltest.ContextWithMeasurementShare(context.Background(), share), coordinator)

	outbound := newStagedProbeOutbound("node-a", target.Listener.Addr().String())
	firstRound := startHealthRound(ctx, history, []adapter.Outbound{outbound}, link)
	awaitDial(t, outbound, 1)
	secondRound := startHealthRound(ctx, history, []adapter.Outbound{outbound}, link)
	require.Eventually(t, func() bool { return share.Participants() == 2 }, time.Second, time.Millisecond,
		"the second round joined the in-flight probe")

	outbound.release()
	<-firstRound
	<-secondRound
	require.EqualValues(t, 1, outbound.dialCount(), "equivalent health rounds still dial once")
	require.Zero(t, share.InFlight())
}
