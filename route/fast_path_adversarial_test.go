package route

import (
	"context"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	R "github.com/sagernet/sing-box/route/rule"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// The policy layer under adversarial combinations the contract tests do not cover.
//
// The equivalence suite answers "does the field do anything"; this file attacks the ways it could
// do the wrong thing: leak between groups that share a leaf, disappear when the leaf switches, or
// behave differently when the same object is reached twice. The `false` decision is included
// everywhere because it is the one value with an observable effect.

// twoGroupRouter serves two groups by tag, with the first as the default.
func twoGroupRouter(t *testing.T, first adapter.Outbound, firstName string, second adapter.Outbound, secondName string, policies FastPathPolicies) *Router {
	t.Helper()
	router := &Router{
		ctx:    context.Background(),
		logger: log.NewNOPFactory().Logger(),
		outbound: &stubOutboundManager{
			byTag: map[string]adapter.Outbound{firstName: first, secondName: second},
			def:   first,
		},
	}
	router.SetFastPathPolicies(policies)
	return router
}

// TestSharedDirectLeafKeepsPerGroupPoliciesIncludingFalse is the cross-group isolation attack with
// the REAL direct leaf, not the bypassable test double.
//
// One direct outbound is selected by two groups. One group writes false, the other says nothing.
// Each flow must resolve its own group's decision, and neither order of resolution may change the
// other's answer.
func TestSharedDirectLeafKeepsPerGroupPoliciesIncludingFalse(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")
	leaf := eligibleLeaf()
	refusing := &transparentGroup{tag: "refusing", selected: leaf}
	passing := &transparentGroup{tag: "passing", selected: leaf}

	router := twoGroupRouter(t, refusing, "refusing", passing, "passing", FastPathPolicies{
		"refusing": {Enabled: false},
	})

	resolve := func(tag string) adapter.PreMatchResult {
		metadata := fastBypassMetadata(N.NetworkTCP, destination)
		return router.preMatchFlow(context.Background(), &metadata, destination, nil, tag)
	}

	// Refusing group first, then the passing one, then the refusing one again: the order is the
	// attack, because a decision cached on the leaf would survive the second call.
	require.Equal(t, adapter.PreMatchContinue, resolve("refusing").Action,
		"the refusing group's flows must not attempt the bypass")
	require.Equal(t, adapter.PreMatchBypass, resolve("passing").Action,
		"the passing group's flows must take it, even though the leaf was just used by a group "+
			"that refused")
	require.Equal(t, adapter.PreMatchContinue, resolve("refusing").Action,
		"and the reverse order must not change the first group's answer")

	// The leaf itself was not marked by either decision.
	_, isPolicyCarrier := adapter.Outbound(leaf).(adapter.FastPathAuthorizedGroup)
	require.False(t, isPolicyCarrier, "a leaf must never become a policy carrier")
}

// TestSharedDirectLeafConcurrentPoliciesDoNotCross runs both groups at once and asserts the
// decisions never swap. Under -race this also covers the shared-state question.
func TestSharedDirectLeafConcurrentPoliciesDoNotCross(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")
	leaf := eligibleLeaf()
	refusing := &transparentGroup{tag: "refusing", selected: leaf}
	passing := &transparentGroup{tag: "passing", selected: leaf}
	router := twoGroupRouter(t, refusing, "refusing", passing, "passing", FastPathPolicies{
		"refusing": {Enabled: false},
	})

	const iterations = 300
	failures := make(chan string, iterations*2)
	var wait sync.WaitGroup

	run := func(tag string, want adapter.PreMatchAction, name string) {
		defer wait.Done()
		for i := 0; i < iterations; i++ {
			metadata := fastBypassMetadata(N.NetworkTCP, destination)
			result := router.preMatchFlow(context.Background(), &metadata, destination, nil, tag)
			if result.Action != want {
				failures <- name + " flow got the other group's decision"
				return
			}
		}
	}

	wait.Add(2)
	go run("refusing", adapter.PreMatchContinue, "refusing")
	go run("passing", adapter.PreMatchBypass, "passing")
	wait.Wait()
	close(failures)

	var collected []string
	for failure := range failures {
		collected = append(collected, failure)
	}
	require.Empty(t, collected)
}

// TestFastPathFalseSurvivesASelectorSwitch keeps the decision through a membership change. The
// group is the same object throughout; only what it selects changes.
func TestFastPathFalseSurvivesASelectorSwitch(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")
	direct := eligibleLeaf()
	proxy := &nonBypassableOutbound{}
	group := &transparentGroup{tag: "国内直连", selected: direct}

	falseRouter := fastPathRouter(t, group, FastPathPolicies{"国内直连": {Enabled: false}})
	unsetRouter := fastPathRouter(t, group, FastPathPolicies{})

	action := func(router *Router) adapter.PreMatchAction {
		metadata := fastBypassMetadata(N.NetworkTCP, destination)
		return router.preMatchFlow(context.Background(), &metadata, destination, nil, "").Action
	}

	require.Equal(t, adapter.PreMatchContinue, action(falseRouter),
		"false must withhold the attempt while the group serves direct")
	require.Equal(t, adapter.PreMatchBypass, action(unsetRouter),
		"the control: the same group with no policy takes it")

	group.setSelected(proxy)
	require.Equal(t, adapter.PreMatchContinue, action(falseRouter),
		"switching to a proxy cannot make a refused flow bypass")
	require.Equal(t, adapter.PreMatchContinue, action(unsetRouter),
		"and the proxy is refused on its own terms with no policy")

	group.setSelected(direct)
	require.Equal(t, adapter.PreMatchContinue, action(falseRouter),
		"switching back must not lose the decision")
	require.Equal(t, adapter.PreMatchBypass, action(unsetRouter),
		"while the unset group takes it again")
}

// TestAnExplicitBypassRuleIgnoresTheFastPathGate records the asymmetry between the two bypass
// mechanisms, and pins it deliberately.
//
// `action: bypass` is an operator instruction, not an implicit direct flow: it returns the bypass
// verdict without consulting canFastBypass, so a tracker does not refuse it. That is the action's
// stated meaning ("let the platform carry this"), and it is not what `fast_path` controls. The
// consequence is stated rather than discovered later: in Linux redirect mode those flows leave
// userspace by explicit choice and are not counted.
func TestAnExplicitBypassRuleIgnoresTheFastPathGate(t *testing.T) {
	router, _, destination := optionsFixture(t)
	router.trackers = []adapter.ConnectionTracker{noopTracker{}}
	router.rules = []adapter.Rule{&fixedRule{action: &R.RuleActionBypass{}}}

	result := router.PreMatch(fastBypassMetadata(N.NetworkTCP, destination), nil)
	require.Equal(t, adapter.PreMatchBypass, result.Action,
		"an explicit bypass action must keep its meaning; if this changes, the action's contract "+
			"changed with it")

	// Control: the same flow through the IMPLICIT path (a direct outbound, no bypass rule) is
	// refused by the tracker. That is the difference this test exists to pin.
	implicitMetadata := fastBypassMetadata(N.NetworkTCP, destination)
	implicitRouter, implicitOutbound := eligibleRouter(t)
	implicitRouter.trackers = []adapter.ConnectionTracker{noopTracker{}}
	require.Equal(t, BypassRefusedTracker,
		implicitRouter.canFastBypass(&implicitMetadata, destination,
			[]adapter.Outbound{implicitOutbound}, implicitOutbound, false),
		"the implicit path must keep refusing on the tracker")
}
