package group

// P02B at the real entry point: two URLTest rounds that ask the same health question must dial
// once, and a round measured on another network generation must not reuse the answer.
//
// The package-level tests prove Measure's contract; this one drives URLTestOutboundsWithTarget, the
// function every health round actually goes through, with the share and the coordinator on the
// context exactly as a Box would install them. It is the closest evidence to "in the product" that
// can be produced before the Box wiring lands (box.go is the integration window's file).

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// sharingProbeOutbound is one member outbound whose dials are counted and can be held at a barrier.
type sharingProbeOutbound struct {
	adapter.Outbound
	tag string

	dials   atomic.Int32
	started chan struct{}
	gate    chan struct{}
	once    sync.Once
}

func newSharingProbeOutbound(tag string) *sharingProbeOutbound {
	return &sharingProbeOutbound{tag: tag, started: make(chan struct{}), gate: make(chan struct{})}
}

func (o *sharingProbeOutbound) Type() string      { return "sharing-probe" }
func (o *sharingProbeOutbound) Tag() string       { return o.tag }
func (o *sharingProbeOutbound) Network() []string { return []string{N.NetworkTCP} }

func (o *sharingProbeOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	o.dials.Add(1)
	o.once.Do(func() { close(o.started) })
	select {
	case <-o.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, destination.String())
}

func (o *sharingProbeOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

func newProbeTarget(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	return server
}

type roundResult struct {
	delays map[string]uint16
}

func startRound(ctx context.Context, history *urltest.HistoryStorage, outbound adapter.Outbound, link string) <-chan roundResult {
	result := make(chan roundResult, 1)
	go func() {
		result <- roundResult{delays: URLTestOutboundsWithTarget(ctx, &taggedOutboundManager{byTag: map[string]adapter.Outbound{}},
			history, log.NewNOPFactory().NewLogger("share"), []adapter.Outbound{outbound}, link,
			urltest.ExpectedStatus{}, 0, true, TestHistoryHealth)}
	}()
	return result
}

func TestTwoRoundsAskingTheSameHealthQuestionShareOneDial(t *testing.T) {
	server := newProbeTarget(t)
	link := server.URL + "/generate_204"

	history := urltest.NewHistoryStorage()
	share := urltest.NewMeasurementShare()
	ctx := urltest.ContextWithCoordinator(
		urltest.ContextWithMeasurementShare(context.Background(), share),
		urltest.NewCoordinator(4))

	// One member object, as a Box would resolve it: two groups naming tag "node-a" get the same
	// outbound, so the question is genuinely the same.
	outbound := newSharingProbeOutbound("node-a")

	firstRound := startRound(ctx, history, outbound, link)
	select {
	case <-outbound.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first round never dialled")
	}
	secondRound := startRound(ctx, history, outbound, link)

	require.Eventually(t, func() bool { return share.Participants() == 2 }, time.Second, time.Millisecond,
		"the second round must join the first round's in-flight question")

	close(outbound.gate)
	first := <-firstRound
	second := <-secondRound

	require.EqualValues(t, 1, outbound.dials.Load(),
		"two equivalent health rounds must open one connection, not two")
	require.Contains(t, first.delays, "node-a")
	require.Contains(t, second.delays, "node-a")
	require.Zero(t, share.InFlight())
	require.Zero(t, share.Participants())
}

func TestARoundOnTheNextNetworkGenerationMeasuresAgain(t *testing.T) {
	server := newProbeTarget(t)
	link := server.URL + "/generate_204"

	history := urltest.NewHistoryStorage()
	share := urltest.NewMeasurementShare()
	coordinator := urltest.NewCoordinator(4)
	outbound := newSharingProbeOutbound("node-a")

	roundOn := func(epoch uint64) <-chan roundResult {
		ctx := urltest.ContextWithCoordinator(
			urltest.ContextWithMeasurementShare(context.Background(), share), coordinator)
		ctx = service.ContextWith[adapter.NetworkManager](ctx, &epochOnlyManager{epoch: epoch})
		return startRound(ctx, history, outbound, link)
	}

	// The previous generation's round is still in flight when the new generation asks.
	previousRound := roundOn(3)
	select {
	case <-outbound.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the previous generation's round never dialled")
	}
	nextRound := roundOn(4)

	require.Eventually(t, func() bool { return outbound.dials.Load() == 2 }, time.Second, time.Millisecond,
		"an answer measured on the previous network must not answer the new generation's question")
	close(outbound.gate)
	<-previousRound
	<-nextRound

	require.EqualValues(t, 2, outbound.dials.Load())
	require.Zero(t, share.InFlight())
	require.Zero(t, share.Participants())
}

// TestTwoDifferentObjectsWithTheSameTagDoNotShare is the identity half of the key audit.
//
// A tag is a name, not an identity. A provider publication replaces a member object while keeping
// its tag, so two objects that both answer to "node-a" are two different sockets, routes and TLS
// sessions. Sharing an in-flight probe between them would answer the new object with a measurement
// taken through the retired one.
func TestTwoDifferentObjectsWithTheSameTagDoNotShare(t *testing.T) {
	server := newProbeTarget(t)
	link := server.URL + "/generate_204"

	history := urltest.NewHistoryStorage()
	share := urltest.NewMeasurementShare()
	ctx := urltest.ContextWithCoordinator(
		urltest.ContextWithMeasurementShare(context.Background(), share),
		urltest.NewCoordinator(4))

	retired := newSharingProbeOutbound("node-a")
	replacement := newSharingProbeOutbound("node-a")

	firstRound := startRound(ctx, history, retired, link)
	select {
	case <-retired.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first round never dialled")
	}
	secondRound := startRound(ctx, history, replacement, link)

	require.Eventually(t, func() bool { return replacement.dials.Load() == 1 }, time.Second, time.Millisecond,
		"a different OBJECT with the same tag is a different node: it must dial its own probe")
	require.EqualValues(t, 2, share.InFlight(),
		"two distinct questions are in flight: the replacement is not waiting on the retired object's probe")

	close(retired.gate)
	close(replacement.gate)
	<-firstRound
	<-secondRound
	require.EqualValues(t, 1, retired.dials.Load())
	require.EqualValues(t, 1, replacement.dials.Load())
	require.Zero(t, share.InFlight())
}

// TestADisplayRoundIsNotSharedAsHealthEvidence is the purpose half of the key audit.
//
// A manual test (the delay a user asked to see) records into the display layer. If its measurement
// claimed PurposeHealth, it would join a real health round - or, worse, a health round would join
// IT and store the display probe's answer as health evidence. The share only merges PurposeHealth,
// so the mapping from the round's mode to the measurement's purpose is what keeps the two apart.
func TestADisplayRoundIsNotSharedAsHealthEvidence(t *testing.T) {
	server := newProbeTarget(t)
	link := server.URL + "/generate_204"

	history := urltest.NewHistoryStorage()
	share := urltest.NewMeasurementShare()
	ctx := urltest.ContextWithCoordinator(
		urltest.ContextWithMeasurementShare(context.Background(), share),
		urltest.NewCoordinator(4))

	outbound := newSharingProbeOutbound("node-a")

	healthRound := startRound(ctx, history, outbound, link)
	select {
	case <-outbound.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the health round never dialled")
	}

	displayResult := make(chan roundResult, 1)
	go func() {
		displayResult <- roundResult{delays: URLTestOutboundsWithTarget(ctx, &taggedOutboundManager{byTag: map[string]adapter.Outbound{}},
			history, log.NewNOPFactory().NewLogger("display"), []adapter.Outbound{outbound}, link,
			urltest.ExpectedStatus{}, 0, true, TestHistoryDisplayOnly)}
	}()

	require.Eventually(t, func() bool { return outbound.dials.Load() == 2 }, time.Second, time.Millisecond,
		"a display measurement is never shared with health work: it opens its own probe")
	require.EqualValues(t, 1, share.Participants(),
		"the display round did not register as a participant of the health question")

	close(outbound.gate)
	<-healthRound
	<-displayResult
	require.Zero(t, share.InFlight())

	// The health layer holds the health round's result; the display layer holds the display one.
	require.NotNil(t, history.LoadURLTestHistory("node-a"),
		"the health round stored health evidence")
}

// TestTheMeasurementPurposeFollowsTheRoundMode pins the mapping itself, without a probe.
func TestTheMeasurementPurposeFollowsTheRoundMode(t *testing.T) {
	batch := &urlTestBatch{}
	batch.mode = TestHistoryHealth
	require.Equal(t, urltest.PurposeHealth, batch.measurementPurpose())
	batch.mode = TestHistoryDisplayOnly
	require.Equal(t, urltest.PurposeDisplay, batch.measurementPurpose(),
		"a display round must not claim to be health evidence")
}

// TestTheStructuralKeyFieldsSeparateTheQuestions pins WHEN two measurements are equivalent, at the
// level the share actually compares.
func TestTheStructuralKeyFieldsSeparateTheQuestions(t *testing.T) {
	first := newSharingProbeOutbound("node-a")
	second := newSharingProbeOutbound("node-a")

	require.Equal(t, nodeIdentity(first), nodeIdentity(first),
		"the same live object is the same identity")
	require.NotEqual(t, nodeIdentity(first), nodeIdentity(second),
		"two objects with one tag are two identities")

	same := urltest.MeasurementKey{Node: "node-a", NodeIdentity: nodeIdentity(first), Target: "https://x/generate_204", Purpose: urltest.PurposeHealth}
	sameOtherObject := same
	sameOtherObject.NodeIdentity = nodeIdentity(second)
	require.False(t, same.SharesEvidenceWith(sameOtherObject),
		"an answer measured through one object cannot be evidence for another object")

	display := same
	display.Purpose = urltest.PurposeDisplay
	require.False(t, same.SharesEvidenceWith(display),
		"a display measurement is never shared as health evidence")
	require.True(t, same.SharesEvidenceWith(same))
}
