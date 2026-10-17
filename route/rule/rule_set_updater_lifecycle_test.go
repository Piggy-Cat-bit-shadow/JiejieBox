package rule

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/power"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// The remote rule-set's UPDATE LOOP: does it stop, and does it obey the device?
//
// # Why this file exists at all
//
// Everything about a remote rule-set's lifecycle was untested. `rule_set_ref_cleanup_test.go` covers the
// reference counter on a constructed object, and nothing covered the goroutine that owns the refreshes: not
// that it exits on Close, and not that the fork's sleep gate is actually wired to it.
//
// The fork added that gate (`RuleSetUpdater.powerGovernor`), and its own comment records that "the intent to
// defer these refreshes was there and the wiring never was" - a field carried unused for a long time. A
// wiring that has never been exercised is exactly the kind that is deleted by a later refactor without a
// single test failing, and the symptom is a radio wakeup at 3am rather than a crash.
//
// # The fixture
//
// A REAL `RemoteRuleSet` over a REAL HTTP server, with the rule-set's transport injected rather than resolved
// from an HTTP client manager: the question is the loop's lifecycle, and a real manager would pull the whole
// dialer graph into the fixture for no extra evidence. Every assertion is a count of requests the server
// actually received, so "the fetch did not happen" is a wire fact rather than a suspicion.

// newRemoteRuleSetPayload builds a valid binary rule-set: one default rule matching a private range.
func newRemoteRuleSetPayload(t *testing.T) []byte {
	t.Helper()
	var payload bytes.Buffer
	err := srs.Write(&payload, option.PlainRuleSet{
		Rules: []option.HeadlessRule{{
			Type: C.RuleTypeDefault,
			DefaultOptions: option.DefaultHeadlessRule{
				IPCIDR: badoption.Listable[*badoption.Prefixable]{
					common.Ptr(badoption.Prefixable(netip.MustParsePrefix("10.0.0.0/8"))),
				},
			},
		}},
	}, C.RuleSetVersionCurrent)
	require.NoError(t, err)
	return payload.Bytes()
}

// updaterFixture is the updater plus the only observable it has: how many times the server was asked.
type updaterFixture struct {
	updater *RuleSetUpdater
	ruleSet *RemoteRuleSet
	hits    *atomic.Int32
}

func newUpdaterFixture(t *testing.T, ctx context.Context, interval time.Duration) *updaterFixture {
	t.Helper()
	payload := newRemoteRuleSetPayload(t)
	hits := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		_, _ = writer.Write(payload)
	}))
	t.Cleanup(server.Close)

	ruleSet, err := NewRemoteRuleSet(ctx, log.NewNOPFactory().Logger(), "updater-lifecycle-test", option.RuleSet{
		Type:   C.RuleSetTypeRemote,
		Format: C.RuleSetFormatBinary,
		RemoteOptions: option.RemoteRuleSet{
			URL:            server.URL,
			UpdateInterval: badoption.Duration(interval),
		},
	})
	require.NoError(t, err)
	// The transport is the test server's own client, for the reason in the file comment.
	ruleSet.httpClient = server.Client()

	updater := NewRuleSetUpdater(ctx, []adapter.RuleSet{ruleSet})
	require.NotNil(t, updater, "a remote rule-set must produce an updater")
	return &updaterFixture{updater: updater, ruleSet: ruleSet, hits: hits}
}

// newRefreshGovernor builds a real governor whose deep-idle threshold cannot be reached during a test, so
// only the explicit pause in the test moves it.
func newRefreshGovernor(t *testing.T) *power.Governor {
	t.Helper()
	policy := power.DefaultPolicy()
	policy.DeepIdleAfter = time.Hour
	policy.WakeStagger = power.WakeStagger{}
	governor := power.NewGovernor(policy)
	t.Cleanup(governor.Close)
	return governor
}

// TestTheUpdaterDoesNotOutliveItsClose is the shutdown contract.
//
// The updater holds the only goroutine a rule-set owns. `Router.Start` hands `updater.Close` to the Box's
// Scope, so a reload cancels it - but nothing proved the goroutine actually returns, and a goroutine parked
// in `select` on a timer that is never rearmed is invisible: it costs one goroutine and one rule-set's
// parsed rules per reload, forever.
func TestTheUpdaterDoesNotOutliveItsClose(t *testing.T) {
	fixture := newUpdaterFixture(t, context.Background(), 5*time.Millisecond)
	fixture.updater.Start()

	require.Eventually(t, func() bool { return fixture.hits.Load() >= 3 }, 10*time.Second, 2*time.Millisecond,
		"the periodic refresh must run before it can be proven to stop")

	require.NoError(t, fixture.updater.Close())

	stopped := fixture.hits.Load()
	time.Sleep(150 * time.Millisecond)
	require.Equal(t, stopped, fixture.hits.Load(),
		"the updater kept fetching after Close: at a 5ms interval a still-running loop would have added "+
			"~30 requests. Every reload would leave one of these behind, holding the old rule-set's rules")
}

// TestAPeriodicRefreshWaitsForTheDeviceToWake is the fork's sleep gate, end to end.
//
// `RuleSetUpdater.powerGovernor` is resolved from the context at construction. Nothing exercised that
// resolution: the governor's own tests prove the wait blocks, and none of them prove the updater asks.
func TestAPeriodicRefreshWaitsForTheDeviceToWake(t *testing.T) {
	governor := newRefreshGovernor(t)
	ctx := service.ContextWith[*power.Governor](context.Background(), governor)

	fixture := newUpdaterFixture(t, ctx, 5*time.Millisecond)
	fixture.updater.Start()
	defer fixture.updater.Close()

	require.Eventually(t, func() bool { return fixture.hits.Load() >= 2 }, 10*time.Second, 2*time.Millisecond,
		"a refresh must run while the device is awake")

	governor.DevicePaused()
	// One pass may already be in flight when the pause lands; let it finish so the snapshot below is not
	// racing it.
	time.Sleep(30 * time.Millisecond)
	paused := fixture.hits.Load()

	time.Sleep(150 * time.Millisecond)
	require.Equal(t, paused, fixture.hits.Load(),
		"a paused device must not wake the radio for a rule-set refresh: that is the whole point of the "+
			"gate, and a nil powerGovernor (a dropped context lookup) fails exactly here")

	governor.DeviceWake()
	require.Eventually(t, func() bool { return fixture.hits.Load() > paused }, 10*time.Second, 2*time.Millisecond,
		"the refresh must resume on wake, or the rules would go stale indefinitely after one nap")
}

// TestTheInitialPassIsNotDeferred pins the deliberate exception.
//
// Routing needs the rules before the tunnel is usable, and Start runs while the device is awake by
// definition. Deferring the first pass would leave a freshly started Box with no rules until the user
// unlocked the phone - a much worse trade than the wakeup it saves.
func TestTheInitialPassIsNotDeferred(t *testing.T) {
	governor := newRefreshGovernor(t)
	governor.DevicePaused()
	ctx := service.ContextWith[*power.Governor](context.Background(), governor)

	fixture := newUpdaterFixture(t, ctx, 5*time.Millisecond)
	fixture.updater.Start()
	defer fixture.updater.Close()

	require.Eventually(t, func() bool { return fixture.hits.Load() >= 1 }, 10*time.Second, 2*time.Millisecond,
		"the first pass must fetch even while paused: a Box that starts while the device is asleep still "+
			"has to route what the device is doing")

	// And the passes after it ARE deferred, which is what makes the exception an exception.
	time.Sleep(30 * time.Millisecond)
	initial := fixture.hits.Load()
	time.Sleep(150 * time.Millisecond)
	require.Equal(t, initial, fixture.hits.Load(),
		"only the FIRST pass is exempt; every later one must wait for the device")
}
