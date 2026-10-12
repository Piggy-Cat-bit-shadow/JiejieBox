package rule

import (
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
)

// The composition of a compiled rule's item list into a network scope.
//
// # Why this is tested on hand-built items rather than through a configuration
//
// The dimension has three states and the composition has one decision that configuration-level tests
// cannot see: what an item that constrains something OTHER than the network contributes. Reading it
// as NEUTRAL (Always) says "a domain condition tells us nothing about the network, so this rule
// matches every flow of either network" - which is a false PROOF, and it is precisely the bug that
// made a logical rule over `domain: [example.com]` deliver BOTH networks to a TCP-only outbound.
//
// MEASURED on the configuration-level fixture: the same mutation is invisible end to end, because a
// wrong extra charge and a missing charge can both leave a small graph with the same verdict. The
// composition is where the claim lives, so it is where the test goes.

// neutralItem constrains nothing about the network and says so, which is what NetworkItem does for
// the network dimension.
type neutralItem struct{}

func (neutralItem) Match(metadata *adapter.InboundContext) bool { return true }
func (neutralItem) String() string                              { return "neutral" }
func (neutralItem) NetworkScope() adapter.RuleNetworkScope      { return adapter.ScopeAlways() }

// narrowingItemProvable constrains the network and can prove it - `network: [udp]`.
type narrowingItemProvable struct{ networks []string }

func (narrowingItemProvable) Match(metadata *adapter.InboundContext) bool { return true }
func (narrowingItemProvable) String() string                              { return "network" }
func (i narrowingItemProvable) NetworkScope() adapter.RuleNetworkScope {
	return adapter.ScopeOfNetworks(i.networks)
}

// unprovableItem is a condition the static summary cannot resolve - a domain, a port, a process.
// It answers UNKNOWN, which is the only honest answer and the one the composition must respect.
type unprovableItem struct{}

func (unprovableItem) Match(metadata *adapter.InboundContext) bool { return false }
func (unprovableItem) String() string                              { return "domain=example.com" }

func TestAConditionThatIsNotAboutTheNetworkIsNotANetworkProof(t *testing.T) {
	rule := &abstractDefaultRule{items: []RuleItem{unprovableItem{}}, allItems: []RuleItem{unprovableItem{}}}
	scope := rule.NetworkScope()

	require.Equal(t, adapter.RuleMatchUnknown, scope.TCP,
		"a domain condition is not a network condition: reading it as Always claims the rule matches "+
			"every tcp flow, which charges an outbound for a flow that may never reach it")
	require.Equal(t, adapter.RuleMatchUnknown, scope.UDP,
		"and the same for udp")
}

func TestAnUnprovableConditionStillLetsTheNetworkConditionReject(t *testing.T) {
	// The shape that makes this composition worth having: `network: [udp]` PLUS a domain condition.
	// The network half is a proof of rejection for tcp on its own, so the rule must still be skipped
	// for tcp even though the domain half is Unknown.
	rule := &abstractDefaultRule{
		items:    []RuleItem{narrowingItemProvable{networks: []string{"udp"}}, unprovableItem{}},
		allItems: []RuleItem{narrowingItemProvable{networks: []string{"udp"}}, unprovableItem{}},
	}
	scope := rule.NetworkScope()

	require.Equal(t, adapter.RuleMatchNever, scope.TCP,
		"one provable rejection survives an unknown condition beside it")
	require.Equal(t, adapter.RuleMatchUnknown, scope.UDP,
		"while the udp half stays unknown, because the domain condition may or may not match")
}

func TestTheEmptyItemListMatchesEverythingAndSaysSo(t *testing.T) {
	rule := &abstractDefaultRule{}
	require.Equal(t, adapter.ScopeAlways(), rule.NetworkScope(),
		"Match special-cases the empty item list and returns true, so a bare `{outbound: x}` rule is "+
			"a CATCH-ALL: reading it as matching nothing would delete it from the analysis")
}

func TestInversionIsAppliedToTheComposedScope(t *testing.T) {
	// `network: [udp]` inverted matches everything that is NOT udp, which includes every tcp flow.
	rule := &abstractDefaultRule{
		items:    []RuleItem{narrowingItemProvable{networks: []string{"udp"}}},
		allItems: []RuleItem{narrowingItemProvable{networks: []string{"udp"}}},
		invert:   true,
	}
	scope := rule.NetworkScope()

	require.Equal(t, adapter.RuleMatchAlways, scope.TCP,
		"an inverted udp condition provably matches every tcp flow")
	require.Equal(t, adapter.RuleMatchNever, scope.UDP)
}

func TestTheRuleTypesComposeTheirSubtreesRatherThanAnsweringUnknown(t *testing.T) {
	// A logical AND over one unconditional network condition IS that condition. Answering Unknown
	// for the whole tree would lose a real defect: the constraint is right there in the tree.
	logical := &abstractLogicalRule{
		mode: C.LogicalTypeAnd,
		rules: []adapter.HeadlessRule{
			&abstractDefaultRule{
				items:    []RuleItem{narrowingItemProvable{networks: []string{"tcp"}}},
				allItems: []RuleItem{narrowingItemProvable{networks: []string{"tcp"}}},
			},
		},
	}
	require.Equal(t, adapter.RuleMatchNever, logical.NetworkScope().UDP,
		"the tree's network condition is a proof about udp")
	require.Equal(t, adapter.RuleMatchAlways, logical.NetworkScope().TCP)

	// And an unprovable leaf keeps its parent unknown in the direction that matters.
	unprovable := &abstractLogicalRule{
		mode:  C.LogicalTypeAnd,
		rules: []adapter.HeadlessRule{&abstractDefaultRule{items: []RuleItem{unprovableItem{}}, allItems: []RuleItem{unprovableItem{}}}},
	}
	require.Equal(t, adapter.RuleMatchUnknown, unprovable.NetworkScope().TCP,
		"an unknown leaf does not become a proof by being wrapped in a logical rule")
}

func TestAnUnrecognisedRuleAnswersUnknownRatherThanMatchingNothing(t *testing.T) {
	require.Equal(t, adapter.ScopeUnknown(), NetworkScopeOf(&foreignRule{}),
		"a rule this package cannot summarise must not be read as a filter")
}

// foreignRule is an adapter.Rule that does not implement RuleNetworkScoper, which is what a rule
// type from outside this tree looks like.
type foreignRule struct {
	adapter.Rule
}

func (f *foreignRule) Type() string { return "foreign" }
