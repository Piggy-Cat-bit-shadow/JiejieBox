package daemon

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

// A rule-set's lifetime across a REAL service reload.
//
// # Why this is at the daemon level and not in `route`
//
// `TestTheRouterHandsItsRuleSetResourcesToTheScope` proves the Router hands its rule-sets and its refresh
// goroutine to the Box's Scope. What it cannot prove is the composition the product actually runs:
// `StartOrReloadService` builds a WHOLE NEW Box in the same process (`daemon/started_service.go:280` ->
// `newInstance` -> `box.New`), after closing the old one, against a service context that outlives both.
//
// That is the one place where a stale resource could survive a reload and still be reachable, because the
// service keeps a context - and the registries in it - across every Box it builds. A rule-set map that
// leaked from the old Router into the new one would make a DELETED rule-set keep matching, which no
// single-Box test can see.
//
// # What is asserted
//
//	start      the inline rule-set is built and its rules are live
//	reload     a second Box, built from the same profile, has its own live rule-set (not a stale copy)
//	re-remove  a third Box, built WITHOUT the rule-set, does not have the tag at all
//
// The last one is the load-bearing assertion: it fails if the rule-set map, or the parsed rules behind it,
// outlive the Box that built them.

// reloadRuleSetConfig is a profile whose only interesting content is one inline rule-set and one rule that
// references it. Nothing else is configured, so a failure cannot come from a subsystem this test did not
// mean to start.
const reloadRuleSetConfig = `{
  "log": {"level": "error"},
  "route": {
    "rule_set": [{"type": "inline", "tag": "reload-set", "rules": [{"domain": ["reload.example"]}]}],
    "rules": [{"rule_set": "reload-set", "action": "reject"}]
  }
}`

// reloadRuleSetConfigWithoutRuleSet is the same profile with the rule-set and its rule deleted - the
// "user removed the rule-set and reloaded" case.
const reloadRuleSetConfigWithoutRuleSet = `{
  "log": {"level": "error"},
  "route": {"rules": []}
}`

// matchingRuleSet reports whether the running Box has the tag and its rules match the domain.
func matchingRuleSet(t *testing.T, instance *Instance, tag string, domain string) (present bool, matched bool) {
	t.Helper()
	require.NotNil(t, instance, "the service reported no running instance")
	require.NotNil(t, instance.Box(), "the instance carries no Box")
	ruleSet, loaded := instance.Box().Router().RuleSet(tag)
	if !loaded {
		return false, false
	}
	return true, ruleSet.Match(&adapter.InboundContext{Domain: domain})
}

func TestARuleSetSurvivesAServiceReloadAndDiesWithItsBox(t *testing.T) {
	started := NewStartedService(ServiceOptions{Context: libboxContext(t)})

	require.NoError(t, started.StartOrReloadService(context.Background(), reloadRuleSetConfig, nil))
	first := started.Instance()
	present, matched := matchingRuleSet(t, first, "reload-set", "reload.example")
	require.True(t, present, "the first Box must build the rule-set the profile names")
	require.True(t, matched, "and its rules must be live: `Initialize` only constructs, `StartContext` loads")

	// A reload is a second Box built against the same service context.
	require.NoError(t, started.StartOrReloadService(context.Background(), reloadRuleSetConfig, nil))
	second := started.Instance()
	require.NotSame(t, first, second, "StartOrReloadService must replace the instance, or this proves nothing")
	present, matched = matchingRuleSet(t, second, "reload-set", "reload.example")
	require.True(t, present, "the replacement Box must build its own rule-set")
	require.True(t, matched, "with its own live rules, not a handle to the previous Box's")

	// A third reload that REMOVES the rule-set: the new Box must not know the tag at all. A map, a matcher
	// or a compiled rule that outlived its Box would answer here.
	require.NoError(t, started.StartOrReloadService(context.Background(), reloadRuleSetConfigWithoutRuleSet, nil))
	third := started.Instance()
	present, _ = matchingRuleSet(t, third, "reload-set", "reload.example")
	require.False(t, present,
		"the rule-set still exists after a reload that removed it: a rule-set resource outlived its Box - "+
			"a removed rule would keep matching traffic the profile no longer mentions")
}
