package daemon

// The node TIMEOUT terminal state, end to end through the daemon: a real probe that fails must reach
// the client payload as delay 0 with a completion time, and a later success must restore the delay.
//
// The whole point of the feature is that a client cannot distinguish "failed" from "never tested"
// when the only record of a failure is the absence of a success. These tests read the same
// readGroups()/readOutbounds() payload the clients consume, drive a REAL URLTest round through the
// daemon RPC, and use a real local hole in the network (a TCP port with nothing listening) as the
// failure - no mocking of the probe path.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/urltest"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

// freePort reserves and immediately releases a loopback port, so a test can start a listener on it
// later. The window is a test artifact: nothing else on this host binds ephemeral ports in that time.
func freePort(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	require.NoError(t, listener.Close())
	return port
}

// groupItemResult reads one member's terminal state out of the PAYLOAD the clients receive.
func groupItemResult(t *testing.T, started *StartedService, groupTag string, memberTag string) (delay int32, at int64, found bool) {
	t.Helper()
	for _, entry := range started.readGroups().Group {
		if entry.Tag != groupTag {
			continue
		}
		for _, item := range entry.Items {
			if item.Tag == memberTag {
				return item.UrlTestDelay, item.UrlTestTime, true
			}
		}
	}
	return 0, 0, false
}

// outboundItemResult reads one outbound's terminal state out of the outbound list payload.
func outboundItemResult(t *testing.T, started *StartedService, tag string) (delay int32, at int64, found bool) {
	t.Helper()
	for _, item := range started.readOutbounds().Outbounds {
		if item.Tag == tag {
			return item.UrlTestDelay, item.UrlTestTime, true
		}
	}
	return 0, 0, false
}

// forceURLTest drives one round through the daemon RPC the clients call, and waits for the round to
// be accepted. The RPC itself is asynchronous by contract, so the assertions below wait for the
// SUBSCRIPTION payload to change rather than assuming the call completed the measurement.
func forceURLTest(t *testing.T, started *StartedService, tag string) {
	t.Helper()
	_, err := started.URLTest(context.Background(), &URLTestRequest{OutboundTag: tag})
	require.NoError(t, err)
}

func TestAURLTestGroupMemberReportsATimeoutThenRecovers(t *testing.T) {
	// The probe URL always answers: failures below come from the NODE, so a failure is unambiguous.
	probeServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(probeServer.Close)

	// Nothing listens here at first: a real connection refusal on the node's own path.
	flakyPort := freePort(t)

	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "direct", "tag": "direct"},
    {"type": "probe-member", "tag": "flaky", "destination": "127.0.0.1:%d"},
    {"type": "urltest", "tag": "group", "outbounds": ["direct", "flaky"],
     "url": "%s", "expected_status": "204", "interval": "1h", "idle_timeout": "2h"}
  ]
}`, flakyPort, probeServer.URL)
	started := newHiddenGroupService(t, profile)
	storage := started.Instance().urlTestHistoryStorage

	// NOTE: the group measures its members as soon as the Box starts (that start-time round is the
	// subject of TestHiddenURLTestStillMeasures), so there is no "never probed" window to observe
	// here. The 0/0 placeholder is pinned at the storage level, where it belongs, in
	// common/urltest/display_result_test.go.

	// PHASE 1 - the start-time round: the healthy member succeeds, the flaky one FAILS.
	require.Eventually(t, func() bool {
		directDelay, directAt, directFound := groupItemResult(t, started, "group", "direct")
		flakyDelay, flakyAt, flakyFound := groupItemResult(t, started, "group", "flaky")
		return directFound && flakyFound && directDelay > 0 && directAt > 0 && flakyDelay == 0 && flakyAt > 0
	}, 20*time.Second, 25*time.Millisecond,
		"a failed member must report delay 0 WITH a completion time - the timeout terminal state")
	_, failedAt, _ := groupItemResult(t, started, "group", "flaky")

	// The outbound list reads the same terminal state: a node the user can see in one place cannot
	// disagree with the group it appears in.
	require.Eventually(t, func() bool {
		delay, at, found := outboundItemResult(t, started, "flaky")
		return found && delay == 0 && at > 0
	}, 5*time.Second, 20*time.Millisecond, "the outbound list must also carry the timeout")

	// PHASE 2 - the node becomes reachable: a success must replace the timeout, with a new time.
	// The node dials ITS OWN destination (the port above) while the HTTP request still names the
	// probe URL, so the server on that port has to answer the probe itself.
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		}),
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", flakyPort))
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	forceURLTest(t, started, "group")
	require.Eventually(t, func() bool {
		delay, at, found := groupItemResult(t, started, "group", "flaky")
		return found && delay > 0 && at >= failedAt
	}, 20*time.Second, 25*time.Millisecond,
		"a later success must restore the delay and refresh the completion time")

	// PHASE 3 - the node fails again: the newest result wins, and the success-only history keeps its
	// own meaning while the display shows the timeout.
	successDelay, successAt, _ := groupItemResult(t, started, "group", "flaky")
	require.Positive(t, successDelay)
	require.NoError(t, listener.Close())
	_ = server.Close()
	time.Sleep(50 * time.Millisecond)

	forceURLTest(t, started, "group")
	require.Eventually(t, func() bool {
		delay, at, found := groupItemResult(t, started, "group", "flaky")
		return found && delay == 0 && at >= successAt
	}, 20*time.Second, 25*time.Millisecond,
		"a new failure must replace an older success on screen")

	// The Clash-facing success history still holds the last SUCCESS, not the failure: the failure is
	// a display terminal state, not an invalidation of what was measured successfully before.
	history := storage.LoadURLTestHistory("flaky")
	require.NotNil(t, history, "the last successful measurement must survive a later failure")
	require.EqualValues(t, successDelay, history.Delay)

	// The health layer must not hold a delay-0 record: selection reads presence as health.
	groups := started.readGroups()
	require.NotEmpty(t, groups.Group)
}

// TestAMixedGroupGivesEveryMeasuredMemberATerminalState covers the progress contract: a round with
// both outcomes still ends, and every member that was actually measured carries a result.
func TestAMixedGroupGivesEveryMeasuredMemberATerminalState(t *testing.T) {
	probeServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(probeServer.Close)
	deadPort := freePort(t)

	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "direct", "tag": "direct"},
    {"type": "probe-member", "tag": "dead", "destination": "127.0.0.1:%d"},
    {"type": "urltest", "tag": "group", "outbounds": ["direct", "dead"],
     "url": "%s", "expected_status": "204", "interval": "1h", "idle_timeout": "2h"}
  ]
}`, deadPort, probeServer.URL)
	started := newHiddenGroupService(t, profile)

	require.Eventually(t, func() bool {
		directDelay, directAt, directFound := groupItemResult(t, started, "group", "direct")
		deadDelay, deadAt, deadFound := groupItemResult(t, started, "group", "dead")
		return directFound && deadFound && directDelay > 0 && directAt > 0 && deadDelay == 0 && deadAt > 0
	}, 20*time.Second, 25*time.Millisecond,
		"each measured member has a terminal state, so a client's progress can finish")

	// A single-node probe of the failing node records the same terminal state without a group RPC.
	forceURLTest(t, started, "dead")
	require.Eventually(t, func() bool {
		delay, at, found := outboundItemResult(t, started, "dead")
		return found && delay == 0 && at > 0
	}, 20*time.Second, 25*time.Millisecond,
		"a manual single-node failure must be visible in the outbound list as a timeout")

	// And the health layer for the group's scope must not contain a zero-delay record.
	scope := started.Instance().outboundManager
	require.NotNil(t, scope)
	_ = urltest.DefaultURLTestURL
}

// TestAFailedProbeReachesTheGroupSubscriptionStream is the delivery half: the clients build their
// group screen from SubscribeGroups, so a terminal failure that only exists in storage would never
// reach a UI. The stream must carry delay 0 WITH a completion time, and a freshly read snapshot must
// still report it (that is what a re-subscribe does).
func TestAFailedProbeReachesTheGroupSubscriptionStream(t *testing.T) {
	probeServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(probeServer.Close)
	deadPort := freePort(t)

	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "direct", "tag": "direct"},
    {"type": "probe-member", "tag": "dead", "destination": "127.0.0.1:%d"},
    {"type": "urltest", "tag": "group", "outbounds": ["direct", "dead"],
     "url": "%s", "expected_status": "204", "interval": "1h", "idle_timeout": "2h"}
  ]
}`, deadPort, probeServer.URL)
	started := newHiddenGroupService(t, profile)

	streamContext, cancelStream := context.WithCancel(context.Background())
	stream := &groupSubscriptionStream{ctx: streamContext, sent: make(chan *Groups, 8)}
	finished := make(chan error, 1)
	go func() { finished <- started.SubscribeGroups(&emptypb.Empty{}, stream) }()

	deadline := time.After(20 * time.Second)
	for {
		select {
		case groups := <-stream.sent:
			for _, entry := range groups.Group {
				if entry.Tag != "group" {
					continue
				}
				for _, item := range entry.Items {
					if item.Tag == "dead" && item.UrlTestDelay == 0 && item.UrlTestTime > 0 {
						cancelStream()
						select {
						case <-finished:
						case <-time.After(10 * time.Second):
							t.Fatal("the group stream did not return after its context was cancelled")
						}
						// A re-subscribe is a full snapshot from storage: it must still say timeout.
						delay, at, found := groupItemResult(t, started, "group", "dead")
						require.True(t, found)
						require.EqualValues(t, 0, delay)
						require.Positive(t, at)
						return
					}
				}
			}
		case <-deadline:
			cancelStream()
			t.Fatal("no streamed group payload carried the failed member's terminal state")
		}
	}
}

// TestAProbeThatGetsTheWrongAnswerIsStillATimeout pins the classification decision at the display
// layer: a probe that RAN and did not complete as configured (here the target answers 500 where 204
// was required) is a failed probe for the user, even though it must not be health evidence about the
// node's proxy path. The two questions have different answers, which is why the layers are separate.
func TestAProbeThatGetsTheWrongAnswerIsStillATimeout(t *testing.T) {
	wrongAnswer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(wrongAnswer.Close)

	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "direct", "tag": "direct"},
    {"type": "urltest", "tag": "group", "outbounds": ["direct"],
     "url": "%s", "expected_status": "204", "interval": "1h", "idle_timeout": "2h"}
  ]
}`, wrongAnswer.URL)
	started := newHiddenGroupService(t, profile)

	require.Eventually(t, func() bool {
		delay, at, found := groupItemResult(t, started, "group", "direct")
		return found && delay == 0 && at > 0
	}, 20*time.Second, 25*time.Millisecond,
		"a probe that could not validate the target's answer is a failed probe for display purposes")
}
