package route

import (
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
)

func defaultRule(options option.RawDefaultRule) option.Rule {
	return option.Rule{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultRule{RawDefaultRule: options}}
}

func logicalRule(rules ...option.Rule) option.Rule {
	return option.Rule{Type: C.RuleTypeLogical, LogicalOptions: option.LogicalRule{RawLogicalRule: option.RawLogicalRule{Rules: rules}}}
}

func defaultDNSRule(options option.RawDefaultDNSRule) option.DNSRule {
	return option.DNSRule{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultDNSRule{RawDefaultDNSRule: options}}
}

func TestNeedsProcessPathsOnlyForPathFields(t *testing.T) {
	t.Parallel()

	require.False(t, needsProcessPaths(nil))
	require.False(t, needsProcessPaths([]option.Rule{defaultRule(option.RawDefaultRule{
		User:   badoption.Listable[string]{"alice"},
		UserID: badoption.Listable[int32]{1000},
	})}), "a user/user_id rule is satisfied by the socket owner alone")
	require.False(t, needsProcessPaths([]option.Rule{logicalRule(defaultRule(option.RawDefaultRule{
		User: badoption.Listable[string]{"alice"},
	}))}))

	require.True(t, needsProcessPaths([]option.Rule{defaultRule(option.RawDefaultRule{
		ProcessPath: badoption.Listable[string]{"/usr/bin/curl"},
	})}))
	require.True(t, needsProcessPaths([]option.Rule{defaultRule(option.RawDefaultRule{
		PackageName: badoption.Listable[string]{"com.example.app"},
	})}))
	require.True(t, needsProcessPaths([]option.Rule{defaultRule(option.RawDefaultRule{
		ProcessName: badoption.Listable[string]{"curl"},
	})}))
	require.True(t, needsProcessPaths([]option.Rule{logicalRule(defaultRule(option.RawDefaultRule{
		ProcessPathRegex: badoption.Listable[string]{`^/usr/bin/`},
	}))}))

	// A rule_set is a dynamic document: it can carry process conditions, so the
	// owner-only path must not be selected from its absence in the inline rule.
	require.True(t, needsProcessPaths([]option.Rule{defaultRule(option.RawDefaultRule{
		RuleSet: badoption.Listable[string]{"geo"},
	})}))
}

func TestNeedsProcessPathDNSRulesOnlyForPathFields(t *testing.T) {
	t.Parallel()

	require.False(t, needsProcessPathDNSRules(nil))
	require.False(t, needsProcessPathDNSRules([]option.DNSRule{defaultDNSRule(option.RawDefaultDNSRule{
		UserID: badoption.Listable[int32]{1000},
	})}))

	require.True(t, needsProcessPathDNSRules([]option.DNSRule{defaultDNSRule(option.RawDefaultDNSRule{
		PackageName: badoption.Listable[string]{"com.example.app"},
	})}))
	require.True(t, needsProcessPathDNSRules([]option.DNSRule{defaultDNSRule(option.RawDefaultDNSRule{
		RuleSet: badoption.Listable[string]{"geo"},
	})}))
}
