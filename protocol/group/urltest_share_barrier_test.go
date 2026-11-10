package group

// The in-flight share at the entry point every round uses.
//
// The scale test proves the SHAPE at 10,000 nodes but it synchronises on timing: two rounds are
// started and the second is expected to arrive while the first is still dialling. On a loaded host
// that is an assumption, not a fact, and a failure could not say whether the share was broken or the
// window was missed.
//
// This test removes the timing: the probe's dial blocks on a barrier, the second round is started
// while that dial is held, and the assertion is on the share's OWN participant count - so "the
// second caller joined the in-flight question" is observed, not hoped for. Then the barrier opens
// and both rounds must finish from ONE dial. Finally the control: after the question completed, a
// new round dials again, because sharing covers in-flight probes and nothing else.

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// barrierProbeOutbound is one node whose first dial is held until the test releases it, and whose
// connections are in-memory pipes (no loopback ports, so the test measures sharing, not the host).
type barrierProbeOutbound struct {
	adapter.Outbound
	tag     string
	dials   atomic.Int64
	entered chan int64
	release chan struct{}
}

func (o *barrierProbeOutbound) Type() string      { return "barrier-probe" }
func (o *barrierProbeOutbound) Tag() string       { return o.tag }
func (o *barrierProbeOutbound) Network() []string { return []string{N.NetworkTCP} }

func (o *barrierProbeOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	count := o.dials.Add(1)
	select {
	case o.entered <- count:
	default:
	}
	if count == 1 {
		select {
		case <-o.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	client, server := net.Pipe()
	go serveNoContentPipe(server)
	return client, nil
}

func (o *barrierProbeOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// serveNoContentPipe answers every request on the pipe with 204 and keeps the connection open, which
// is what the unified-delay measurement (a warm-up request and a timed one) expects.
func serveNoContentPipe(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		if !strings.HasPrefix(line, "GET ") && !strings.HasPrefix(line, "HEAD ") {
			return
		}
		for {
			header, headerErr := reader.ReadString('\n')
			if headerErr != nil {
				return
			}
			if header == "\r\n" || header == "\n" {
				break
			}
		}
		if _, err = conn.Write([]byte("HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n")); err != nil {
			return
		}
	}
}

// roundOutbounds runs one health round in the background and returns a channel with its result.
func roundOutbounds(ctx context.Context, manager adapter.OutboundManager, history *urltest.HistoryStorage, nodes []adapter.Outbound, link string) <-chan map[string]uint16 {
	result := make(chan map[string]uint16, 1)
	go func() {
		result <- URLTestOutboundsWithTarget(ctx, manager, history,
			log.NewNOPFactory().NewLogger("share-barrier"), nodes, link,
			urltest.ExpectedStatus{}, 0, true, TestHistoryHealth)
	}()
	return result
}

func TestTheGroupSharesOneInFlightProbeBetweenTwoRounds(t *testing.T) {
	node := &barrierProbeOutbound{
		tag:     "node-a",
		entered: make(chan int64, 4),
		release: make(chan struct{}),
	}
	manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{"node-a": node}}
	cast := []adapter.Outbound{node}

	history := urltest.NewHistoryStorage()
	share := urltest.NewMeasurementShare()
	coordinator := urltest.NewCoordinator(3)
	ctx := urltest.ContextWithCoordinator(
		urltest.ContextWithMeasurementShare(context.Background(), share), coordinator)
	// The target is never reached: the pipe answers every request locally.
	server := httptest.NewServer(nil)
	t.Cleanup(server.Close)
	link := server.URL + "/generate_204"

	first := roundOutbounds(ctx, manager, history, cast, link)
	select {
	case count := <-node.entered:
		require.EqualValues(t, 1, count)
	case <-time.After(5 * time.Second):
		t.Fatal("the first round never dialled")
	}

	second := roundOutbounds(ctx, manager, history, cast, link)

	// The barrier is the SHARE's own participant count: two participants means the second round
	// really joined the in-flight question, which is the fact the timing-based test hoped for.
	require.Eventually(t, func() bool { return share.Participants() == 2 },
		5*time.Second, time.Millisecond,
		"the second round must join the in-flight question instead of dialling")
	require.EqualValues(t, 1, node.dials.Load(),
		"the second round must not open a second connection while the first is in flight")

	close(node.release)
	firstResult := <-first
	secondResult := <-second
	require.Len(t, firstResult, 1, "the first round is answered")
	require.Len(t, secondResult, 1, "the waiter is answered from the leader's measurement")
	require.EqualValues(t, 1, node.dials.Load(), "one in-flight question, one dial")

	// The documented control: sharing covers IN-FLIGHT probes only. Once the question completed and
	// its entry is gone, a new round dials again - so a "fixed" implementation that cached forever
	// would fail here, and so would one that shared across rounds.
	third := roundOutbounds(ctx, manager, history, cast, link)
	select {
	case count := <-node.entered:
		require.EqualValues(t, 2, count, "a later round starts a fresh question")
	case <-time.After(5 * time.Second):
		t.Fatal("the later round never dialled; sharing must not turn into caching")
	}
	thirdResult := <-third
	require.Len(t, thirdResult, 1)
	require.EqualValues(t, 2, node.dials.Load())
}

// TestTheCancelledWaiterDoesNotCostTheOtherCallerItsAnswer is the cancellation half: one of the two
// callers gives up while the probe is held, and the other must still be answered.
func TestTheCancelledWaiterDoesNotCostTheOtherCallerItsAnswer(t *testing.T) {
	node := &barrierProbeOutbound{
		tag:     "node-a",
		entered: make(chan int64, 4),
		release: make(chan struct{}),
	}
	manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{"node-a": node}}
	cast := []adapter.Outbound{node}

	history := urltest.NewHistoryStorage()
	share := urltest.NewMeasurementShare()
	coordinator := urltest.NewCoordinator(3)
	base := urltest.ContextWithCoordinator(
		urltest.ContextWithMeasurementShare(context.Background(), share), coordinator)
	server := httptest.NewServer(nil)
	t.Cleanup(server.Close)
	link := server.URL + "/generate_204"

	leaderCtx, cancelLeader := context.WithCancel(base)
	defer cancelLeader()
	leader := roundOutbounds(leaderCtx, manager, history, cast, link)
	select {
	case <-node.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the leader never dialled")
	}

	waiterCtx, cancelWaiter := context.WithCancel(base)
	waiter := roundOutbounds(waiterCtx, manager, history, cast, link)
	require.Eventually(t, func() bool { return share.Participants() == 2 },
		5*time.Second, time.Millisecond, "the waiter must join before being cancelled")

	// The WAITER gives up. The leader keeps its own context and must still be answered.
	cancelWaiter()
	close(node.release)
	result := <-leader
	require.Len(t, result, 1,
		"cancelling a waiter must not cancel the shared probe: the leader's own answer depends on it")
	require.EqualValues(t, 1, node.dials.Load())
	// The cancelled waiter returns (empty or partial); it must not panic or hang.
	select {
	case <-waiter:
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled waiter never returned")
	}
	require.Zero(t, share.InFlight(), "no question survives the callers")
	require.Zero(t, share.Participants())
	require.Zero(t, coordinator.InFlight())
	require.Zero(t, coordinator.Queued())
}

// TestTheCancelledLeaderStillAnswersTheWaiter is the opposite half: the caller that happened to be
// first leaves, and the answer still reaches the other one.
func TestTheCancelledLeaderStillAnswersTheWaiter(t *testing.T) {
	node := &barrierProbeOutbound{
		tag:     "node-a",
		entered: make(chan int64, 4),
		release: make(chan struct{}),
	}
	manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{"node-a": node}}
	cast := []adapter.Outbound{node}

	history := urltest.NewHistoryStorage()
	share := urltest.NewMeasurementShare()
	coordinator := urltest.NewCoordinator(3)
	base := urltest.ContextWithCoordinator(
		urltest.ContextWithMeasurementShare(context.Background(), share), coordinator)
	server := httptest.NewServer(nil)
	t.Cleanup(server.Close)
	link := server.URL + "/generate_204"

	leaderCtx, cancelLeader := context.WithCancel(base)
	leader := roundOutbounds(leaderCtx, manager, history, cast, link)
	select {
	case <-node.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the leader never dialled")
	}
	waiter := roundOutbounds(base, manager, history, cast, link)
	require.Eventually(t, func() bool { return share.Participants() == 2 },
		5*time.Second, time.Millisecond, "the waiter must join")

	cancelLeader()
	close(node.release)
	waiterResult := <-waiter
	require.Len(t, waiterResult, 1,
		"the probe outlives the caller that started it; the waiter's answer must not be a cancellation")
	select {
	case <-leader:
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled leader never returned")
	}
	require.EqualValues(t, 1, node.dials.Load())
	require.Zero(t, share.Participants())
}

// TestACancelledRoundDrainsItsProbes: a round cancelled mid-flight must return and leave nothing
// running - no share participant, no coordinator slot, no queue entry, no goroutine per node.
func TestACancelledRoundDrainsItsProbes(t *testing.T) {
	const nodeCount = 500
	nodes := make([]*scaleProbeOutbound, nodeCount)
	cast := make([]adapter.Outbound, nodeCount)
	for index := range nodes {
		node := &scaleProbeOutbound{tag: fmt.Sprintf("node-%03d", index)}
		nodes[index] = node
		cast[index] = node
	}
	manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{}}
	for _, node := range nodes {
		manager.byTag[node.tag] = node
	}

	history := urltest.NewHistoryStorage()
	share := urltest.NewMeasurementShare()
	coordinator := urltest.NewCoordinator(3)
	ctx := urltest.ContextWithCoordinator(
		urltest.ContextWithMeasurementShare(context.Background(), share), coordinator)
	server := httptest.NewServer(nil)
	t.Cleanup(server.Close)
	link := server.URL + "/generate_204"

	baselineGoroutines := runtime.NumGoroutine()
	roundCtx, cancel := context.WithCancel(ctx)
	done := make(chan map[string]uint16, 1)
	go func() {
		done <- URLTestOutboundsWithTarget(roundCtx, manager, history,
			log.NewNOPFactory().NewLogger("cancel-drain"), cast, link,
			urltest.ExpectedStatus{}, 0, true, TestHistoryHealth)
	}()

	// Wait until the round is demonstrably running before cancelling it.
	require.Eventually(t, func() bool {
		for _, node := range nodes {
			if node.dials.Load() > 0 {
				return true
			}
		}
		return false
	}, 5*time.Second, time.Millisecond, "the round never started probing")
	cancel()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a cancelled round must return; it is holding probes that nobody will store")
	}

	// Drain, not "instantly zero": a probe that was already inside the coordinator may still be
	// unwinding when the round returns. What must not happen is a slot that is never given back.
	require.Eventually(t, func() bool {
		return share.InFlight() == 0 && share.Participants() == 0 &&
			coordinator.InFlight() == 0 && coordinator.Queued() == 0
	}, 5*time.Second, time.Millisecond,
		"a cancelled round must drain its probes: share=%d participants=%d coordinator=%d queued=%d",
		share.InFlight(), share.Participants(), coordinator.InFlight(), coordinator.Queued())
	require.Eventually(t, func() bool { return runtime.NumGoroutine() <= baselineGoroutines+64 },
		5*time.Second, 5*time.Millisecond,
		"the cancelled round must not leave a goroutine per node behind")
}
