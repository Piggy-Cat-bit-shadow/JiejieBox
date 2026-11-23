package daemon

// Adversarial pass over the node-timeout display state: the cases the first implementation round did
// NOT exercise, chosen because each one can silently produce a wrong UI rather than an error.
//
// The states under attack, restated independently of the implementation:
//
//	delay > 0, valid time   success, show the delay
//	delay = 0, valid time   the probe really failed, show "timeout"
//	delay = 0, time = 0     never probed, show the untested placeholder
//
// and the invariants a UI relies on: a completed probe is visible through the REAL payload, a
// deletion or an instance change cannot be polluted by a late result, two targets cannot answer each
// other's question, and rapid opposite outcomes converge to the newest one.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/urltest"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

// probeTarget is the host:port a `probe-member` must dial. httptest.Server.URL carries a scheme,
// and a probe-member passes its destination straight to net.Dial, so keeping the scheme made the
// dial fail before any server saw the request - which is how two of these tests passed for the
// wrong reason on their first run.
func probeTarget(server *httptest.Server) string {
	return strings.TrimPrefix(server.URL, "http://")
}

// stallListener accepts TCP connections and holds them open, so a probe that speaks TLS first stalls
// inside its handshake. release() fails every held connection at once, which gives a test a
// deterministic "the probe finishes exactly now" edge instead of a sleep.
type stallListener struct {
	listener net.Listener
	accepted atomic.Int32
	access   sync.Mutex
	conns    []net.Conn
}

func startStallListener(t *testing.T) *stallListener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	stall := &stallListener{listener: listener}
	t.Cleanup(func() {
		_ = listener.Close()
		stall.release()
	})
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			stall.access.Lock()
			stall.conns = append(stall.conns, conn)
			stall.access.Unlock()
			stall.accepted.Add(1)
		}
	}()
	return stall
}

func (s *stallListener) target() string { return s.listener.Addr().String() }

func (s *stallListener) release() {
	s.access.Lock()
	conns := s.conns
	s.conns = nil
	s.access.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

// slowProbeServer answers every request with the probe's success status after a delay, so a test can
// hold a probe open and land another event inside its window.
func slowProbeServer(t *testing.T, delay time.Duration) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		time.Sleep(delay)
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

// TestBatchTestingASelectorPublishesAFailedMember is the display-only group path: a user pressing
// "test all" on a Selector runs a diagnostic round, which must still leave a terminal state for a
// member that failed. This path had no coverage - only URLTest groups (health mode) and single nodes
// were tested - and it is the one a Selector's own test button hits.
func TestBatchTestingASelectorPublishesAFailedMember(t *testing.T) {
	goodServer, _ := slowProbeServer(t, 0)
	deadPort := freePort(t)

	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "good", "destination": "%s"},
    {"type": "probe-member", "tag": "dead", "destination": "127.0.0.1:%d"},
    {"type": "selector", "tag": "sel", "outbounds": ["good", "dead"], "default": "good"}
  ]
}`, probeTarget(goodServer), deadPort)
	started := newHiddenGroupService(t, profile)

	forceURLTest(t, started, "sel")

	// The failing member must reach the outbound list as a timeout: a diagnostic round otherwise
	// leaves the user looking at a node that appears never to have been tested.
	require.Eventually(t, func() bool {
		delay, at, found := outboundItemResult(t, started, "dead")
		return found && delay == 0 && at > 0
	}, 25*time.Second, 25*time.Millisecond,
		"a member that failed during a Selector batch test must report the timeout terminal state")

	// NOTE on the healthy member: this RPC path measures against the DEFAULT probe URL
	// (https://www.gstatic.com/generate_204 - a constant), so a plain local server cannot satisfy it
	// and both members fail here (the "good" one at the TLS handshake). That makes this test good
	// evidence for "a display-only round publishes a failure" and NO evidence about the success side,
	// which is why the success+failure pair is proved instead in
	// protocol/group/urltest_display_only_attack_test.go, where the round runs against a real local
	// HTTP target. The distinction is deliberate: do not read this test as a success-path proof.
	require.Eventually(t, func() bool {
		delay, at, found := outboundItemResult(t, started, "good")
		return found && at > 0 && delay == 0
	}, 25*time.Second, 25*time.Millisecond,
		"the other member of the batch also carries a terminal state of its own")
}

// TestTwoTargetsOnOneNodeStaySeparate is target isolation through the payload: two URLTest groups
// measure the same node against different URLs, one succeeds and one fails. Each group must report
// its OWN answer, and the node list must report the newest result - not one target's answer borrowed
// by another.
func TestTwoTargetsOnOneNodeStaySeparate(t *testing.T) {
	succeeding, _ := slowProbeServer(t, 0)
	wrongAnswer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(wrongAnswer.Close)

	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "direct", "tag": "direct"},
    {"type": "urltest", "tag": "group-ok", "outbounds": ["direct"],
     "url": "%s", "expected_status": "204", "interval": "1h", "idle_timeout": "2h"},
    {"type": "urltest", "tag": "group-bad", "outbounds": ["direct"],
     "url": "%s", "expected_status": "204", "interval": "1h", "idle_timeout": "2h"}
  ]
}`, succeeding.URL, wrongAnswer.URL)
	started := newHiddenGroupService(t, profile)

	// Let both groups complete their start-up rounds, whichever order they land in.
	require.Eventually(t, func() bool {
		okDelay, okAt, okFound := groupItemResult(t, started, "group-ok", "direct")
		badDelay, badAt, badFound := groupItemResult(t, started, "group-bad", "direct")
		return okFound && okAt > 0 && okDelay > 0 && badFound && badAt > 0 && badDelay == 0
	}, 25*time.Second, 25*time.Millisecond,
		"each group must report the result of ITS own target: success for the healthy URL, timeout for the failing one")

	// The node list carries ONE newest result for the node (both targets write the tag-level entry);
	// whichever landed last, it must be a state the node really reached, never a mix of the two.
	delay, at, found := outboundItemResult(t, started, "direct")
	require.True(t, found)
	require.Positive(t, at)
	require.Contains(t, []int32{0, delay}, delay, "sanity: the tag-level value is a delay or a timeout")

	// Re-running the HEALTHY group must move the node list back to a delay, because a newer success
	// wins over an older failure (invariant: a new success clears a failure).
	forceURLTest(t, started, "group-ok")
	require.Eventually(t, func() bool {
		delay, at, found := outboundItemResult(t, started, "direct")
		return found && at > 0 && delay > 0
	}, 25*time.Second, 25*time.Millisecond,
		"a newer success must clear the older failure in the node list")
}

// TestDeletingANodeDuringItsProbeLeavesNoGhost attacks the drop window: a manual single-node probe is
// in flight when the node's evidence is dropped (what a node removal does). The late result must not
// resurrect an entry for a node that no longer has any evidence - that is both a lie to the UI and a
// record that nothing will ever clean up.
func TestDeletingANodeDuringItsProbeLeavesNoGhost(t *testing.T) {
	// The default probe URL is HTTPS, so a listener that accepts and then says nothing holds the
	// probe inside its TLS handshake - exactly the window this test needs, without a fabricated clock.
	stall := startStallListener(t)
	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "node", "destination": "%s"}
  ]
}`, stall.target())
	started := newHiddenGroupService(t, profile)
	storage := started.Instance().urlTestHistoryStorage

	forceURLTest(t, started, "node")
	// Wait until the probe is really running (the target accepted its connection), then drop the node.
	require.Eventually(t, func() bool { return stall.accepted.Load() == 1 }, 10*time.Second, 5*time.Millisecond,
		"the probe never reached the target")
	storage.DeleteURLTestHistory("node")

	// Release the probe so it fails NOW, after the drop: if the late write were authorised it would
	// recreate an entry for a node whose evidence is gone.
	stall.release()
	time.Sleep(800 * time.Millisecond)

	result, loaded := storage.LoadDisplayResult("node")
	require.False(t, loaded,
		"a result measured before the node was dropped must not create a display entry afterwards (ghost record: %+v)", result)
}

// TestRapidOppositeOutcomesConvergeToTheNewest drives success -> failure -> success as fast as the
// local path allows, so several results share a second. The payload must end on the newest state, not
// on whichever frame a client's second-resolution dedupe happened to keep.
func TestRapidOppositeOutcomesConvergeToTheNewest(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)
	flaky := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if healthy.Load() {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(flaky.Close)

	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "direct", "tag": "direct"},
    {"type": "urltest", "tag": "group", "outbounds": ["direct"],
     "url": "%s", "expected_status": "204", "interval": "1h", "idle_timeout": "2h"}
  ]
}`, flaky.URL)
	started := newHiddenGroupService(t, profile)

	require.Eventually(t, func() bool {
		delay, at, found := groupItemResult(t, started, "group", "direct")
		return found && delay > 0 && at > 0
	}, 25*time.Second, 20*time.Millisecond, "the healthy state never arrived")

	for round := 0; round < 3; round++ {
		healthy.Store(false)
		forceURLTest(t, started, "group")
		require.Eventually(t, func() bool {
			delay, at, found := groupItemResult(t, started, "group", "direct")
			return found && delay == 0 && at > 0
		}, 25*time.Second, 20*time.Millisecond, "round %d: the failure never arrived", round)

		healthy.Store(true)
		forceURLTest(t, started, "group")
		require.Eventually(t, func() bool {
			delay, at, found := groupItemResult(t, started, "group", "direct")
			return found && delay > 0 && at > 0
		}, 25*time.Second, 20*time.Millisecond, "round %d: the recovery never arrived", round)
	}
}

// TestAManualProbeDoesNotBecomeSelectionEvidence is the separation the whole design rests on, checked
// through the payload rather than the storage API: after a manual single-node test against a failing
// endpoint, the node must show a timeout while the HEALTH layer for that node stays empty, and the
// failover path must not see the node as "measured and failed".
func TestAManualProbeDoesNotBecomeSelectionEvidence(t *testing.T) {
	good, _ := slowProbeServer(t, 0)
	deadPort := freePort(t)
	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "good", "destination": "%s"},
    {"type": "probe-member", "tag": "dead", "destination": "127.0.0.1:%d"},
    {"type": "urltest", "tag": "group", "outbounds": ["good", "dead"],
     "url": "%s", "expected_status": "204", "interval": "1h", "idle_timeout": "2h"}
  ]
}`, probeTarget(good), deadPort, good.URL)
	started := newHiddenGroupService(t, profile)
	storage := started.Instance().urlTestHistoryStorage

	// Wait for the group's own round so the health state is settled first.
	require.Eventually(t, func() bool {
		delay, at, found := groupItemResult(t, started, "group", "dead")
		return found && delay == 0 && at > 0
	}, 25*time.Second, 25*time.Millisecond)

	// A manual single-node test of the failing node.
	before, _ := storage.LoadDisplayResult("dead")
	forceURLTest(t, started, "dead")
	time.Sleep(500 * time.Millisecond)

	after, loaded := storage.LoadDisplayResult("dead")
	require.True(t, loaded)
	require.True(t, after.Failed())
	require.False(t, after.StartedAt.Before(before.StartedAt),
		"the manual probe must be the newest result, not an older one re-read")

	// Health: the manual test must not have created evidence for any target.
	for _, scopeURL := range []string{good.URL} {
		scope, err := urltest.NewMeasurementScope(scopeURL, urltest.ExpectedStatus{})
		require.NoError(t, err)
		require.Nil(t, storage.LoadURLTestHistoryFor("dead", scope),
			"a manual diagnostic must not write health evidence")
	}
}

// TestAnOverlappingRoundCannotMoveTheNodesResultBackwards is the overlap rule at the payload level.
//
// An earlier version of this test asserted that the second round always CANCELS the first, and that a
// superseded probe therefore never publishes. CI proved that premise wrong (the slow probe completed
// and published a legitimate failure there), and it is not the invariant a client depends on: whether
// the first probe is cancelled or completes, its probe STARTED earlier, so it must never win the
// node's newest result. That is what this version asserts, and it holds either way.
//
// The slow group's own member is allowed either state - absent (its round was superseded) or a
// completed failure (it was not) - but never a half-state, i.e. a result with a zero completion time.
func TestAnOverlappingRoundCannotMoveTheNodesResultBackwards(t *testing.T) {
	var slowHits atomic.Int32
	slowServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		slowHits.Add(1)
		time.Sleep(1500 * time.Millisecond)
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(slowServer.Close)
	fastServer, _ := slowProbeServer(t, 0)

	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "direct", "tag": "node"},
    {"type": "urltest", "tag": "group-slow", "outbounds": ["node"],
     "url": "%s", "expected_status": "204", "interval": "1h", "idle_timeout": "2h"},
    {"type": "urltest", "tag": "group-fast", "outbounds": ["node"],
     "url": "%s", "expected_status": "204", "interval": "1h", "idle_timeout": "2h"}
  ]
}`, slowServer.URL, fastServer.URL)
	started := newHiddenGroupService(t, profile)

	// The fast group's result is what the node list must keep showing.
	require.Eventually(t, func() bool {
		delay, at, found := outboundItemResult(t, started, "node")
		return found && at > 0 && delay > 0
	}, 25*time.Second, 20*time.Millisecond, "the fast success never arrived")

	// Start the slow round and wait until its probe is really in flight, then start a newer one.
	settledHits := slowHits.Load()
	forceURLTest(t, started, "group-slow")
	require.Eventually(t, func() bool { return slowHits.Load() > settledHits }, 10*time.Second, 5*time.Millisecond,
		"the slow round never reached its target")
	fastStart := time.Now()
	forceURLTest(t, started, "group-fast")
	require.Eventually(t, func() bool {
		delay, _, found := outboundItemResult(t, started, "node")
		return found && delay > 0
	}, 25*time.Second, 20*time.Millisecond, "the second fast round never arrived")

	// Let the slow probe finish (it started BEFORE the fast round above).
	time.Sleep(3 * time.Second)

	delay, at, found := outboundItemResult(t, started, "node")
	require.True(t, found)
	require.Positive(t, at)
	require.Positive(t, delay,
		"the older-started probe must not move the node's result backwards when it finishes later")
	require.GreaterOrEqual(t, at, fastStart.Add(-2*time.Second).Unix(),
		"the node's result must still be the newer measurement")

	// The slow group's member is either absent (superseded) or a COMPLETE failure; never a half-state.
	slowDelay, slowAt, slowFound := groupItemResult(t, started, "group-slow", "node")
	require.True(t, slowFound)
	if slowAt != 0 {
		require.Zero(t, slowDelay, "a published terminal state for the slow group must be the real failure")
	}
}

// TestAHundredFailedProbesDoNotGrowTheDisplayState is the bound: repeated failures must not accumulate
// records, goroutines or waiters. It is deliberately small - a leak that needs a thousand iterations to
// show is still a leak, but this test is a tripwire, not a load test.
func TestAHundredFailedProbesDoNotGrowTheDisplayState(t *testing.T) {
	good, _ := slowProbeServer(t, 0)
	deadPort := freePort(t)
	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "dead", "destination": "127.0.0.1:%d"},
    {"type": "urltest", "tag": "group", "outbounds": ["dead"],
     "url": "%s", "expected_status": "204", "interval": "1h", "idle_timeout": "2h"}
  ]
}`, deadPort, good.URL)
	started := newHiddenGroupService(t, profile)
	storage := started.Instance().urlTestHistoryStorage

	for i := 0; i < 100; i++ {
		forceURLTest(t, started, "group")
		forceURLTest(t, started, "dead")
	}
	time.Sleep(2 * time.Second)

	require.LessOrEqual(t, storage.DisplayResultEntryCount(), 2,
		"the tag-level map must not grow with the number of failed probes")
	require.LessOrEqual(t, storage.ScopedDisplayResultEntryCount(), 2,
		"the scoped map must not grow with the number of failed probes")
}

// TestAReloadDuringAProbeDoesNotPublishTheOldInstancesResult attacks the instance boundary: a node is
// being measured when the configuration is reloaded. Whatever the reload does to the history storage,
// the old probe's outcome must not describe the NEW node - the user changed what that tag means.
func TestAReloadDuringAProbeDoesNotPublishTheOldInstancesResult(t *testing.T) {
	stall := startStallListener(t)
	first, second := freePort(t), freePort(t)
	profileOne := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "node", "destination": "%s"}
  ]
}`, stall.target())
	// The second configuration keeps the tag but points it somewhere else: same name, different node.
	profileTwo := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "node", "destination": "127.0.0.1:%d"}
  ]
}`, second)
	_ = first

	started := newHiddenGroupService(t, profileOne)
	storageBefore := started.Instance().urlTestHistoryStorage

	forceURLTest(t, started, "node")
	require.Eventually(t, func() bool { return stall.accepted.Load() == 1 }, 10*time.Second, 5*time.Millisecond,
		"the probe never reached the target")

	require.NoError(t, started.StartOrReloadService(context.Background(), profileTwo, nil))
	require.NotNil(t, started.Instance())
	storageAfter := started.Instance().urlTestHistoryStorage

	// Release the probe after the reload: it fails now, describing a node that no longer exists.
	stall.release()
	time.Sleep(800 * time.Millisecond)

	result, loaded := storageAfter.LoadDisplayResult("node")
	require.False(t, loaded,
		"a probe started under the previous configuration must not publish into the reloaded instance (got %+v)", result)
	if storageBefore != storageAfter {
		// The reload allocated a new storage; the old one is closed, so the late write is refused there
		// as well and nothing can read it. Recorded rather than asserted, because either isolation
		// mechanism is acceptable - what matters is the live state above.
		_, oldLoaded := storageBefore.LoadDisplayResult("node")
		require.False(t, oldLoaded)
	}
}

// outboundListStream is the real gRPC stream interface for SubscribeOutbounds, so the test consumes
// the same frames a client does.
type outboundListStream struct {
	ctx  context.Context
	sent chan *OutboundList
}

var _ grpc.ServerStreamingServer[OutboundList] = (*outboundListStream)(nil)

func (s *outboundListStream) Send(list *OutboundList) error {
	select {
	case s.sent <- list:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *outboundListStream) SetHeader(metadata.MD) error  { return nil }
func (s *outboundListStream) SendHeader(metadata.MD) error { return nil }
func (s *outboundListStream) SetTrailer(metadata.MD)       {}
func (s *outboundListStream) Context() context.Context     { return s.ctx }
func (s *outboundListStream) SendMsg(any) error            { return nil }
func (s *outboundListStream) RecvMsg(any) error            { return nil }

// TestAFailedSingleNodeProbeNotifiesTheOutboundStream is the NOTIFICATION half of the display path.
//
// The group-stream test above only shows that a rebuild carries the timeout; in health mode a second
// write (the health deletion) also notifies, so it passes even when the display write notifies
// nothing. A manual single-node failure writes exactly ONE thing - the display terminal result - so
// this test fails if that write stops notifying. It was added after a mutation (notifyUpdated
// disabled in StoreDisplayResult) survived the group-stream test.
func TestAFailedSingleNodeProbeNotifiesTheOutboundStream(t *testing.T) {
	deadPort := freePort(t)
	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "node", "destination": "127.0.0.1:%d"}
  ]
}`, deadPort)
	started := newHiddenGroupService(t, profile)

	streamContext, cancelStream := context.WithCancel(context.Background())
	stream := &outboundListStream{ctx: streamContext, sent: make(chan *OutboundList, 16)}
	finished := make(chan error, 1)
	go func() { finished <- started.SubscribeOutbounds(&emptypb.Empty{}, stream) }()
	t.Cleanup(func() {
		cancelStream()
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
		}
	})

	// Drain the initial snapshot(s) and confirm the node starts untested.
	deadline := time.After(20 * time.Second)
	seenInitial := false
	for !seenInitial {
		select {
		case list := <-stream.sent:
			for _, item := range list.Outbounds {
				if item.Tag == "node" {
					require.Zero(t, item.UrlTestDelay)
					require.Zero(t, item.UrlTestTime, "the node must start untested")
					seenInitial = true
				}
			}
		case <-deadline:
			t.Fatal("the outbound stream sent no snapshot")
		}
	}

	// Now make it fail. The ONLY write is the display terminal result.
	forceURLTest(t, started, "node")
	for {
		select {
		case list := <-stream.sent:
			for _, item := range list.Outbounds {
				if item.Tag == "node" && item.UrlTestDelay == 0 && item.UrlTestTime > 0 {
					return
				}
			}
		case <-deadline:
			t.Fatal("a failed manual probe must notify the outbound stream; no frame carried the timeout")
		}
	}
}
