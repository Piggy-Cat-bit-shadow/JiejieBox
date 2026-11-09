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
