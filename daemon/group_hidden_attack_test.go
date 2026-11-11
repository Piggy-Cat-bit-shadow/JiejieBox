package daemon

// Second-round adversarial tests for the `hidden` display property.
//
// The first-round tests (group_hidden_display_test.go) proved the basic shape. These try to BREAK
// it: a three-level chain of hidden groups with a rule that routes into it, a detour that points at
// a hidden group, a hidden URLTest that must keep probing, a hidden LoadBalance that must keep
// distributing, the expand cache, and the full A-H matrix of display outcomes.
//
// Every assertion here reads an OUTPUT - the group list readGroups builds, the outbound list, the
// router's own decision, a listener that did or did not receive a connection, the history storage
// the probes write - and never a configuration field. That is the point of this file: a test that
// asserts `options.Hidden == true` proves nothing about what a client sees.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	protocolgroup "github.com/sagernet/sing-box/protocol/group"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// TestHiddenGroupAttackMatrix is prompt 02 section 4.1, cases A-G in ONE running Box, read back
// through the same function the clients' payload comes from.
//
// Case H (every group hidden) is TestEveryGroupHiddenStillStarts, because it needs a service whose
// group list is empty to be meaningful.
func TestHiddenGroupAttackMatrix(t *testing.T) {
	members := map[string]*memberListener{
		"member-a": startMemberListener(t),
		"member-b": startMemberListener(t),
	}
	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "member-a", "destination": "%s"},
    {"type": "probe-member", "tag": "member-b", "destination": "%s"},

    {"type": "selector", "tag": "A-selector-default", "outbounds": ["member-a", "member-b"]},
    {"type": "selector", "tag": "B-selector-explicit-false", "outbounds": ["member-a", "member-b"], "hidden": false},
    {"type": "selector", "tag": "C-selector-hidden", "outbounds": ["member-a", "member-b"], "hidden": true},
    {"type": "urltest", "tag": "D-urltest-hidden", "outbounds": ["member-a", "member-b"], "hidden": true},
    {"type": "loadbalance", "tag": "E-loadbalance-hidden", "outbounds": ["member-a", "member-b"], "hidden": true},
    {"type": "selector", "tag": "F-single-node-visible", "outbounds": ["member-a"]},
    {"type": "capability-free-group", "tag": "G-empty-visible", "outbounds": []}
  ]
}`, members["member-a"].target(), members["member-b"].target())
	started := newHiddenGroupService(t, profile)

	require.Equal(t,
		[]string{"A-selector-default", "B-selector-explicit-false", "F-single-node-visible", "G-empty-visible"},
		reportedGroupTags(t, started),
		"the group list must be exactly the groups that did not ask to be hidden; no member-count or "+
			"emptiness threshold may re-enter through the hidden check")

	// The same profile's outbound list keeps every object, hidden or not.
	require.ElementsMatch(t,
		[]string{
			"member-a", "member-b",
			"A-selector-default", "B-selector-explicit-false", "C-selector-hidden",
			"D-urltest-hidden", "E-loadbalance-hidden", "F-single-node-visible", "G-empty-visible",
		},
		reportedOutboundTags(t, started))

	// A is not a duplicate of B by accident: both are present and both are selectable.
	for _, visible := range []string{"A-selector-default", "B-selector-explicit-false"} {
		_, isLoaded := started.Instance().outboundManager.Outbound(visible)
		require.True(t, isLoaded, "%s must still be a live outbound", visible)
	}
}

// TestHiddenFlagDoesNotPropagateThroughNesting is section 4.2's first scenario, plus the two
// directions a lazy implementation gets wrong: a hidden PARENT with a visible CHILD, and a visible
// parent with a hidden child.
func TestHiddenFlagDoesNotPropagateThroughNesting(t *testing.T) {
	members := map[string]*memberListener{
		"us-01": startMemberListener(t),
		"us-02": startMemberListener(t),
		"hk-01": startMemberListener(t),
		"hk-02": startMemberListener(t),
	}
	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "us-01", "destination": "%s"},
    {"type": "probe-member", "tag": "us-02", "destination": "%s"},
    {"type": "probe-member", "tag": "hk-01", "destination": "%s"},
    {"type": "probe-member", "tag": "hk-02", "destination": "%s"},

    {"type": "selector", "tag": "MAIN", "outbounds": ["REGION-US", "REGION-HK", "CITY-VISIBLE"], "default": "REGION-US"},
    {"type": "selector", "tag": "REGION-US", "outbounds": ["us-01", "us-02"], "default": "us-01", "hidden": true},
    {"type": "selector", "tag": "REGION-HK", "outbounds": ["hk-01", "hk-02"], "default": "hk-01"},
    {"type": "selector", "tag": "CITY-VISIBLE", "outbounds": ["us-02"], "hidden": false}
  ]
}`, members["us-01"].target(), members["us-02"].target(),
		members["hk-01"].target(), members["hk-02"].target())
	started := newHiddenGroupService(t, profile)

	// 1/2/8: the hidden child is absent, its parent and its visible sibling are present.
	require.Equal(t, []string{"MAIN", "REGION-HK", "CITY-VISIBLE"}, reportedGroupTags(t, started),
		"hiding a child must not hide the parent, and must not touch the siblings")

	// 3: the parent's member list is untouched - this is read from the PAYLOAD the clients get.
	require.Equal(t, []string{"REGION-US", "REGION-HK", "CITY-VISIBLE"}, reportedGroupItems(t, started, "MAIN"))
	// A hidden group has no card of its own, so its members are read from the live object: the
	// point is that All() is built exactly as a visible group's and that the object is still there.
	require.Equal(t, []string{"us-01", "us-02"}, liveGroupMembers(t, started, "REGION-US"),
		"a hidden group's own member list is built exactly as a visible one's")

	// 4/5: the parent selects the hidden child through the RPC, and the flow follows its path.
	_, err := started.SelectOutbound(context.Background(), &SelectOutboundRequest{
		GroupTag:    "MAIN",
		OutboundTag: "REGION-US",
	})
	require.NoError(t, err)
	require.Equal(t, "REGION-US", reportedGroupSelection(t, started, "MAIN"))
	require.Equal(t, "us-01", dialThroughNamedGroup(t, started, "MAIN", members),
		"the flow must leave through the hidden child's own selection")

	// 6: the hidden group's internal members are still selectable, and the parent's flow follows the
	// new choice - the hidden group is a full selector, not a pass-through.
	_, err = started.SelectOutbound(context.Background(), &SelectOutboundRequest{
		GroupTag:    "REGION-US",
		OutboundTag: "us-02",
	})
	require.NoError(t, err)
	require.Equal(t, "us-02", dialThroughNamedGroup(t, started, "MAIN", members))
}

// TestThreeLevelHiddenChainResolvesAndRoutes is the "critical attack scenario" of section 11:
//
//	MAIN (visible) -> REGION (hidden selector) -> CITY (hidden urltest) -> LEAF
//	with a route rule whose outbound is REGION.
//
// The flow is routed by the REAL router with the REAL rule, and the observation is which loopback
// listener received the connection - not a field, not a selection getter.
func TestThreeLevelHiddenChainResolvesAndRoutes(t *testing.T) {
	members := map[string]*memberListener{
		"leaf-01":  startMemberListener(t),
		"leaf-02":  startMemberListener(t),
		"fallback": startMemberListener(t),
	}
	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "leaf-01", "destination": "%s"},
    {"type": "probe-member", "tag": "leaf-02", "destination": "%s"},
    {"type": "probe-member", "tag": "fallback", "destination": "%s"},

    {"type": "selector", "tag": "MAIN", "outbounds": ["REGION"], "default": "REGION"},
    {"type": "selector", "tag": "REGION", "outbounds": ["CITY"], "default": "CITY", "hidden": true},
    {"type": "urltest", "tag": "CITY", "outbounds": ["leaf-01", "leaf-02"], "hidden": true}
  ],
  "route": {
    "final": "fallback",
    "rules": [
      {"domain_suffix": ["attack.test"], "outbound": "REGION"}
    ]
  }
}`, members["leaf-01"].target(), members["leaf-02"].target(), members["fallback"].target())
	started := newHiddenGroupService(t, profile)

	// Display: only MAIN.
	require.Equal(t, []string{"MAIN"}, reportedGroupTags(t, started),
		"a three-level chain of hidden groups must leave exactly the visible root")

	// Reference chain intact at every level, and every object present in the complete list.
	require.Equal(t, []string{"REGION"}, reportedGroupItems(t, started, "MAIN"))
	require.ElementsMatch(t,
		[]string{"leaf-01", "leaf-02", "fallback", "MAIN", "REGION", "CITY"},
		reportedOutboundTags(t, started))

	// The rule is what decides the path here: the destination matches `domain_suffix attack.test`,
	// and the only thing that can serve it is REGION, which is hidden.
	//
	// The `fallback` member is the negative control: it IS the configured `route.final`, so a flow
	// that failed to match the rule would land there instead. Without it this assertion would pass
	// for a rule that never matched.
	reached := routeThroughTheRealRouter(t, started, "probe.attack.test:443", members)
	require.Contains(t, []string{"leaf-01", "leaf-02"}, reached,
		"a rule naming a hidden group must resolve it and carry the flow through CITY to a leaf")
	require.NotEqual(t, "fallback", reached,
		"the flow must have matched the rule, not fallen through to route.final")

	// The parent can still select the hidden group, and the routed flow follows that selection.
	_, err := started.SelectOutbound(context.Background(), &SelectOutboundRequest{
		GroupTag:    "MAIN",
		OutboundTag: "REGION",
	})
	require.NoError(t, err)
	require.Equal(t, "REGION", reportedGroupSelection(t, started, "MAIN"))
	secondReached := routeThroughTheRealRouter(t, started, "second.attack.test:443", members)
	require.Contains(t, []string{"leaf-01", "leaf-02"}, secondReached,
		"after selecting the hidden group explicitly, the rule must still route into it")
	require.NotEqual(t, "fallback", secondReached)
}

// TestHiddenGroupIsStillADetourTarget points a REAL protocol outbound's `detour` at a hidden group.
//
// `direct` refuses `detour` outright ("`detour` is not supported in direct context"), so the detour
// is exercised the way a proxy outbound uses one: the SOCKS client dials its SERVER through the
// detour, which is the hidden group. The stub server never completes a SOCKS handshake, so the dial
// itself fails - what is being asserted is that the hidden group's member was reached, which is what
// "the detour resolved" means here.
func TestHiddenGroupIsStillADetourTarget(t *testing.T) {
	members := map[string]*memberListener{
		"leaf-01": startMemberListener(t),
		"leaf-02": startMemberListener(t),
	}
	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "leaf-01", "destination": "%s"},
    {"type": "probe-member", "tag": "leaf-02", "destination": "%s"},
    {"type": "selector", "tag": "REGION", "outbounds": ["leaf-01", "leaf-02"], "default": "leaf-01", "hidden": true},
    {"type": "socks", "tag": "socks-via-hidden", "server": "127.0.0.1", "server_port": 9, "detour": "REGION"}
  ]
}`, members["leaf-01"].target(), members["leaf-02"].target())
	started := newHiddenGroupService(t, profile)

	require.Empty(t, reportedGroupTags(t, started),
		"the only group here is the hidden detour target; the outbound that uses it is not a group")
	require.ElementsMatch(t, []string{"leaf-01", "leaf-02", "REGION", "socks-via-hidden"},
		reportedOutboundTags(t, started),
		"the outbound list keeps both the hidden group and the outbound that detours through it")

	before := make(map[string]int32, len(members))
	for tag, member := range members {
		before[tag] = member.accepted.Load()
	}
	dialer, isLoaded := started.Instance().outboundManager.Outbound("socks-via-hidden")
	require.True(t, isLoaded)
	dialContext, isDialer := dialer.(N.Dialer)
	require.True(t, isDialer)
	// The error is expected: the probe listener closes immediately, so no SOCKS handshake completes.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _ := dialContext.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("origin.test:443"))
	if conn != nil {
		_ = conn.Close()
	}

	reached := ""
	require.Eventually(t, func() bool {
		for tag, member := range members {
			if member.accepted.Load() > before[tag] {
				reached = tag
				return true
			}
		}
		return false
	}, 5*time.Second, 5*time.Millisecond,
		"a `detour` naming a hidden group must still resolve it and dial through it")
	require.Equal(t, "leaf-01", reached, "the hidden selector's default member must be the one dialled")
}

// TestHiddenURLTestStillMeasures pins section 4.5: hiding a URLTest group must not stop it.
//
// Three independent observations, because they fail independently:
//
//	a) the START-time check: the group measures its members on start, hidden or not;
//	b) the PERIODIC loop: after the group is touched (as a parent's selection touches it), the
//	   background ticker keeps measuring and REWRITES the history - the timestamp advancing is what
//	   separates "probed once" from "still probing";
//	c) the MANUAL check: the daemon's URLTest RPC still resolves a hidden group by tag and probes it.
func TestHiddenURLTestStillMeasures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	t.Run("periodic probing continues", func(t *testing.T) {
		profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "direct", "tag": "direct-a"},
    {"type": "direct", "tag": "direct-b"},
    {"type": "urltest", "tag": "hidden-measured", "outbounds": ["direct-a", "direct-b"],
     "url": "%s", "expected_status": "204", "interval": "1s", "hidden": true}
  ]
}`, server.URL)
		started := newHiddenGroupService(t, profile)
		require.Empty(t, reportedGroupTags(t, started), "the group is hidden")

		instance := started.Instance()
		groupOutbound, isLoaded := instance.outboundManager.Outbound("hidden-measured")
		require.True(t, isLoaded)
		urlTestGroup := groupOutbound.(*protocolgroup.URLTest)
		scope := urlTestGroup.MeasurementScope()
		group := groupOutbound.(adapter.OutboundGroup)

		// (a) the start-time check, with nothing touching the group.
		require.Eventually(t, func() bool {
			return instance.urlTestHistoryStorage.LoadURLTestHistoryFor("direct-a", scope) != nil
		}, 15*time.Second, 25*time.Millisecond,
			"a hidden urltest group must perform its start-time measurement")

		// (b) a real flow through the group is what starts its ticker, exactly as a routed flow
		// through a parent does (URLTest.Selected is a pure preview and deliberately does not).
		require.NotNil(t, group.Selected(N.NetworkTCP), "a hidden urltest group must still select a member")
		groupDialer, isDialer := groupOutbound.(N.Dialer)
		require.True(t, isDialer)
		flowContext, cancelFlow := context.WithTimeout(context.Background(), 5*time.Second)
		flowConn, dialErr := groupDialer.DialContext(flowContext, N.NetworkTCP, M.ParseSocksaddr(server.Listener.Addr().String()))
		cancelFlow()
		if dialErr == nil && flowConn != nil {
			_ = flowConn.Close()
		}

		firstProbe := instance.urlTestHistoryStorage.LoadURLTestHistoryFor("direct-a", scope)
		require.NotNil(t, firstProbe)
		require.Eventually(t, func() bool {
			later := instance.urlTestHistoryStorage.LoadURLTestHistoryFor("direct-a", scope)
			return later != nil && later.Time.After(firstProbe.Time)
		}, 20*time.Second, 25*time.Millisecond,
			"a hidden urltest group's background ticker must keep measuring and rewriting history")
	})

	t.Run("manual URL test still works", func(t *testing.T) {
		profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "direct", "tag": "direct-a"},
    {"type": "direct", "tag": "direct-b"},
    {"type": "urltest", "tag": "hidden-manual", "outbounds": ["direct-a", "direct-b"],
     "url": "%s", "expected_status": "204", "interval": "1h", "idle_timeout": "2h", "hidden": true}
  ]
}`, server.URL)
		started := newHiddenGroupService(t, profile)
		instance := started.Instance()
		groupOutbound, isLoaded := instance.outboundManager.Outbound("hidden-manual")
		require.True(t, isLoaded)
		scope := groupOutbound.(*protocolgroup.URLTest).MeasurementScope()
		require.Eventually(t, func() bool {
			return instance.urlTestHistoryStorage.LoadURLTestHistoryFor("direct-a", scope) != nil
		}, 15*time.Second, 25*time.Millisecond)
		before := instance.urlTestHistoryStorage.LoadURLTestHistoryFor("direct-a", scope)
		require.NotNil(t, before)

		_, err := started.URLTest(context.Background(), &URLTestRequest{OutboundTag: "hidden-manual"})
		require.NoError(t, err, "the URLTest RPC must resolve a hidden group by tag, not refuse it")

		require.Eventually(t, func() bool {
			later := instance.urlTestHistoryStorage.LoadURLTestHistoryFor("direct-a", scope)
			return later != nil && later.Time.After(before.Time)
		}, 20*time.Second, 25*time.Millisecond,
			"the manual check must have re-measured the hidden group's members")
	})
}

// TestHiddenLoadBalanceStillDistributes pins section 4.6's observable half: a hidden balancing group
// keeps handing successive flows to successive members.
func TestHiddenLoadBalanceStillDistributes(t *testing.T) {
	members := map[string]*memberListener{
		"leaf-01": startMemberListener(t),
		"leaf-02": startMemberListener(t),
		"leaf-03": startMemberListener(t),
	}
	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "leaf-01", "destination": "%s"},
    {"type": "probe-member", "tag": "leaf-02", "destination": "%s"},
    {"type": "probe-member", "tag": "leaf-03", "destination": "%s"},
    {"type": "loadbalance", "tag": "hidden-pool", "outbounds": ["leaf-01", "leaf-02", "leaf-03"], "hidden": true}
  ]
}`, members["leaf-01"].target(), members["leaf-02"].target(), members["leaf-03"].target())
	started := newHiddenGroupService(t, profile)
	require.Empty(t, reportedGroupTags(t, started))

	pool, isLoaded := started.Instance().outboundManager.Outbound("hidden-pool")
	require.True(t, isLoaded)
	require.ElementsMatch(t, []string{"leaf-01", "leaf-02", "leaf-03"},
		pool.(adapter.OutboundGroup).All(), "hiding must not change the candidate set")

	for range 6 {
		dialThroughNamedGroup(t, started, "hidden-pool", members)
	}
	for _, member := range members {
		require.Positive(t, member.accepted.Load(),
			"six round-robin flows over three members must reach every member")
	}
}

// TestHiddenGroupKeepsItsExpandState pins the other half of section 4.4: the expand flag is stored
// on the cache file keyed by tag, and hiding the group must neither skip the write nor clear it.
//
// The state is deliberately unobservable through the group list (a hidden group has no card to
// expand) - which is exactly why it is easy to lose by accident, and why it is asserted on the
// cache file itself.
func TestHiddenGroupKeepsItsExpandState(t *testing.T) {
	members := map[string]*memberListener{
		"leaf-01": startMemberListener(t),
		"leaf-02": startMemberListener(t),
	}
	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "experimental": {"cache_file": {"enabled": true}},
  "outbounds": [
    {"type": "probe-member", "tag": "leaf-01", "destination": "%s"},
    {"type": "probe-member", "tag": "leaf-02", "destination": "%s"},
    {"type": "selector", "tag": "REGION", "outbounds": ["leaf-01", "leaf-02"], "hidden": true},
    {"type": "selector", "tag": "VISIBLE", "outbounds": ["leaf-01", "leaf-02"]}
  ]
}`, members["leaf-01"].target(), members["leaf-02"].target())
	started := newHiddenGroupService(t, profile)
	require.Equal(t, []string{"VISIBLE"}, reportedGroupTags(t, started))

	for _, groupTag := range []string{"REGION", "VISIBLE"} {
		_, err := started.SetGroupExpand(context.Background(), &SetGroupExpandRequest{
			GroupTag: groupTag,
			IsExpand: true,
		})
		require.NoError(t, err, "SetGroupExpand must not refuse a hidden group: %s", groupTag)
		isExpand, isStored := started.Instance().cacheFile.LoadGroupExpand(groupTag)
		require.True(t, isStored, "%s must have its expand state stored, hidden or not", groupTag)
		require.True(t, isExpand)
	}

	// A reload rebuilds every group from the same cache file; the hidden group's flag survives, and
	// the group is still not listed.
	require.NoError(t, started.StartOrReloadService(context.Background(), profile, nil))
	require.NoError(t, started.waitForStarted(context.Background()))
	require.Equal(t, []string{"VISIBLE"}, reportedGroupTags(t, started))
	restoredExpand, isStored := started.Instance().cacheFile.LoadGroupExpand("REGION")
	require.True(t, isStored, "a reload must not clear a hidden group's expand state")
	require.True(t, restoredExpand)
}

// TestHiddenGroupWithAllProviderNodesFilteredOutStillStarts is the EMPTY production group, built the
// only way this fork can build one that stays empty: a provider that publishes real nodes, and a
// group whose own `exclude` filter drops every one of them.
//
// (An empty provider DOCUMENT is refused - "refusing an empty node set: an unrecognised or empty
// document must not wipe the published nodes" - so a durable provider-backed empty group cannot be
// built that way. Measured, not assumed.)
//
// This is the state the pre-existing comment in readGroups defends ("a group with one member or none
// is emitted exactly as before unless it asks not to be"). It is also the state where a display
// filter could plausibly be written as "skip groups with no items". It must not be.
func TestHiddenGroupWithAllProviderNodesFilteredOutStillStarts(t *testing.T) {
	members := map[string]*memberListener{
		"node-1": startMemberListener(t),
		"node-2": startMemberListener(t),
	}
	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "providers": [
    {"type": "inline", "tag": "pool", "outbounds": [
      {"type": "probe-member", "tag": "node-1", "destination": "%s"},
      {"type": "probe-member", "tag": "node-2", "destination": "%s"}
    ]}
  ],
  "outbounds": [
    {"type": "selector", "tag": "REGION", "providers": ["pool"], "exclude": ".*", "hidden": true},
    {"type": "selector", "tag": "VISIBLE-EMPTY", "providers": ["pool"], "exclude": ".*"},
    {"type": "selector", "tag": "MAIN", "outbounds": ["REGION"]}
  ],
  "route": {"final": "pool/node-1"}
}`, members["node-1"].target(), members["node-2"].target())
	started := newHiddenGroupService(t, profile)

	require.Empty(t, liveGroupMembers(t, started, "REGION"),
		"the hidden provider-backed group really is empty, so the assertions below are not vacuous")
	require.Empty(t, liveGroupMembers(t, started, "VISIBLE-EMPTY"))

	// Both empty groups are listed or not strictly by their own flag: the visible one appears with
	// an empty member list, the hidden one does not appear at all.
	require.Equal(t, []string{"VISIBLE-EMPTY", "MAIN"}, reportedGroupTags(t, started),
		"an empty group must still be listed when it did not ask to be hidden")
	require.Empty(t, reportedGroupItems(t, started, "VISIBLE-EMPTY"),
		"the visible empty group is reported with no members")
	require.Equal(t, []string{"REGION"}, reportedGroupItems(t, started, "MAIN"),
		"the hidden empty group is still a member of its parent")
	require.ElementsMatch(t,
		[]string{"pool/node-1", "pool/node-2", "REGION", "VISIBLE-EMPTY", "MAIN"},
		reportedOutboundTags(t, started))
}

// TestHiddenLoadBalanceHealthFilterStillApplies pins the half of section 4.6 the distribution test
// cannot reach: `hidden` must not disable the group's health-aware candidate filtering.
//
// # The authoritative state
//
// LoadBalance filters a member by one predicate (`healthy` in protocol/group/loadbalance.go): a member
// is a candidate only while the shared store holds a SUCCESSFUL measurement for THIS group's target
// and expected status. A failed probe writes no entry, and a member that was healthy has its entry
// removed, so "no entry" IS the unhealthy verdict - a test must not wait for a failure record that the
// design never writes.
//
// This test therefore rebuilds the group's own measurement identity from configuration, using the same
// public helpers the group calls (`ParseMeasurementTarget`, `ParseExpectedStatus`), waits for the
// healthy member's entry, asserts the broken member has none, and only then asserts distribution. That
// removes the old race where "some history exists" was taken as "the verdict is in".
func TestHiddenLoadBalanceHealthFilterStillApplies(t *testing.T) {
	server := startCountingServer(t)
	serverPort := server.server.Listener.Addr().(*net.TCPAddr).Port
	probeURL := fmt.Sprintf("http://127.0.0.1:%d/", serverPort)

	// A port with nothing behind it: probes through that member are refused, and because a failed
	// probe writes no entry, the member never becomes a candidate.
	deadListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	deadPort := deadListener.Addr().(*net.TCPAddr).Port
	require.NoError(t, deadListener.Close())

	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "healthy", "destination": "127.0.0.1:%d"},
    {"type": "probe-member", "tag": "broken", "destination": "127.0.0.1:%d"},
    {"type": "loadbalance", "tag": "hidden-pool", "outbounds": ["healthy", "broken"],
     "url": "%s", "expected_status": "204", "interval": "1s", "hidden": true}
  ]
}`, serverPort, deadPort, probeURL)
	started := newHiddenGroupService(t, profile)
	require.Empty(t, reportedGroupTags(t, started), "the balancing group is hidden")

	instance := started.Instance()
	pool, isLoaded := instance.outboundManager.Outbound("hidden-pool")
	require.True(t, isLoaded)
	poolDialer, isDialer := pool.(N.Dialer)
	require.True(t, isDialer)

	// The identity the filter uses, rebuilt from configuration with the production helpers.
	target, err := urltest.ParseMeasurementTarget(probeURL)
	require.NoError(t, err)
	expected, err := urltest.ParseExpectedStatus("204")
	require.NoError(t, err)
	scope := urltest.MeasurementScope{URL: target.ScopeURL, Expected: expected.Canonical()}

	healthyOutbound, isLoaded := instance.outboundManager.Outbound("healthy")
	require.True(t, isLoaded)
	brokenOutbound, isLoaded := instance.outboundManager.Outbound("broken")
	require.True(t, isLoaded)
	healthyTag := protocolgroup.RealTag(healthyOutbound, N.NetworkTCP)
	brokenTag := protocolgroup.RealTag(brokenOutbound, N.NetworkTCP)

	require.Eventually(t, func() bool {
		return instance.urlTestHistoryStorage.LoadURLTestHistoryFor(healthyTag, scope) != nil
	}, 20*time.Second, 10*time.Millisecond,
		"the reachable member must hold a successful measurement for the group's own target")
	require.Nil(t, instance.urlTestHistoryStorage.LoadURLTestHistoryFor(brokenTag, scope),
		"an always-failing member never gets a health entry: absence is the verdict, not a failure record")

	// With the verdict in, the failed member cannot be a candidate. Dial more times than there are
	// members: a still-eligible broken member would be picked by round-robin and would error.
	before := server.count.Load()
	for range 4 {
		dialContext, cancelDial := context.WithTimeout(context.Background(), 5*time.Second)
		conn, dialErr := poolDialer.DialContext(dialContext, N.NetworkTCP, M.ParseSocksaddr("origin.test:443"))
		cancelDial()
		require.NoError(t, dialErr,
			"a flow must never be handed to the member its own health check has failed")
		if conn != nil {
			_ = conn.Close()
		}
	}
	require.Eventually(t, func() bool { return server.count.Load()-before >= 4 },
		5*time.Second, 5*time.Millisecond,
		"all four flows must have reached the healthy member's destination")

	// Negative control from CONFIGURATION only: without the health URL the group has no candidate
	// filter, so its round-robin must hand a flow to the failed member and that dial must fail.
	controlProfile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "healthy", "destination": "127.0.0.1:%d"},
    {"type": "probe-member", "tag": "broken", "destination": "127.0.0.1:%d"},
    {"type": "loadbalance", "tag": "hidden-pool", "outbounds": ["healthy", "broken"], "hidden": true}
  ]
}`, serverPort, deadPort)
	controlStarted := newHiddenGroupService(t, controlProfile)
	controlPool, isLoaded := controlStarted.Instance().outboundManager.Outbound("hidden-pool")
	require.True(t, isLoaded)
	controlDialer := controlPool.(N.Dialer)
	var controlFailures int
	for range 4 {
		controlContext, cancelControl := context.WithTimeout(context.Background(), 5*time.Second)
		controlConn, controlErr := controlDialer.DialContext(controlContext, N.NetworkTCP, M.ParseSocksaddr("origin.test:443"))
		cancelControl()
		if controlConn != nil {
			_ = controlConn.Close()
		}
		if controlErr != nil {
			controlFailures++
		}
	}
	require.Positive(t, controlFailures,
		"the control profile must hand at least one flow to the failed member, or this test proves nothing")
}

// TestHiddenProviderBackedGroupStillPublishes is the section-5/acceptance "Provider" row: a provider
// feeds a HIDDEN group, and neither the publication nor the group's use of it may depend on display.
func TestHiddenProviderBackedGroupStillPublishes(t *testing.T) {
	members := map[string]*memberListener{
		"node-1": startMemberListener(t),
		"node-2": startMemberListener(t),
	}
	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "providers": [
    {"type": "inline", "tag": "pool", "outbounds": [
      {"type": "probe-member", "tag": "node-1", "destination": "%s"},
      {"type": "probe-member", "tag": "node-2", "destination": "%s"}
    ]}
  ],
  "outbounds": [
    {"type": "selector", "tag": "REGION", "providers": ["pool"], "hidden": true},
    {"type": "selector", "tag": "MAIN", "outbounds": ["REGION"], "default": "REGION"}
  ]
}`, members["node-1"].target(), members["node-2"].target())
	started := newHiddenGroupService(t, profile)

	// The provider's nodes joined the hidden group, with the rendered "<provider>/<node>" tags.
	membersOfRegion := liveGroupMembers(t, started, "REGION")
	require.ElementsMatch(t, []string{"pool/node-1", "pool/node-2"}, membersOfRegion,
		"a hidden group must still take its membership from a provider")

	// They are real outbounds, and the group is still absent from the display.
	for _, published := range membersOfRegion {
		_, isLoaded := started.Instance().outboundManager.Outbound(published)
		require.True(t, isLoaded, "the provider's node %s must be registered", published)
	}
	require.Equal(t, []string{"MAIN"}, reportedGroupTags(t, started))
	require.ElementsMatch(t,
		[]string{"REGION", "MAIN", "pool/node-1", "pool/node-2"},
		reportedOutboundTags(t, started))

	// The parent still reaches the hidden group, and the flow leaves through a provider node.
	_, err := started.SelectOutbound(context.Background(), &SelectOutboundRequest{
		GroupTag:    "MAIN",
		OutboundTag: "REGION",
	})
	require.NoError(t, err)
	require.Contains(t, []string{"node-1", "node-2"}, dialThroughNamedGroup(t, started, "MAIN", members),
		"a flow through the parent must leave through one of the provider's nodes")
}

// liveGroupMembers reads a group's member list from the RUNNING object, which is the only way to
// read it for a hidden group: it deliberately has no entry in the reported list.
func liveGroupMembers(t *testing.T, started *StartedService, groupTag string) []string {
	t.Helper()
	loaded, isLoaded := started.Instance().outboundManager.Outbound(groupTag)
	require.True(t, isLoaded, "the outbound manager must still hold %s", groupTag)
	group, isGroup := loaded.(adapter.OutboundGroup)
	require.True(t, isGroup, "%s must still be an outbound group", groupTag)
	return group.All()
}

// routeThroughTheRealRouter sends ONE connection through the running Box's router with the given
// destination and reports which member listener received it.
//
// This is the router's own entry point, the one an inbound calls, so the rule engine, the outbound
// lookup and the group chain are all exercised. The returned "" means nothing arrived.
func routeThroughTheRealRouter(t *testing.T, started *StartedService, destination string, members map[string]*memberListener) string {
	t.Helper()
	router := started.Instance().instance.Router()
	require.NotNil(t, router)

	before := make(map[string]int32, len(members))
	for tag, member := range members {
		before[tag] = member.accepted.Load()
	}

	// net.Pipe is unbuffered and synchronous, which is fine: the probe members answer by accepting
	// and closing, so the copy ends without any payload ever being written.
	clientConn, inboundConn := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		router.RouteConnectionEx(ctx, inboundConn, adapter.InboundContext{
			Inbound:     "attack-inbound",
			Network:     N.NetworkTCP,
			Source:      M.ParseSocksaddr("192.0.2.10:34567"),
			Destination: M.ParseSocksaddr(destination),
		}, nil)
	}()

	reached := ""
	require.Eventually(t, func() bool {
		for tag, member := range members {
			if member.accepted.Load() > before[tag] {
				reached = tag
				return true
			}
		}
		return false
	}, 10*time.Second, 5*time.Millisecond, "the routed flow reached no member")
	_ = clientConn.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the router did not return after the flow closed")
	}
	return reached
}
