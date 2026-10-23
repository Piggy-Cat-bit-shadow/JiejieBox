package route

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// The tri-state `fast_path` field, measured against EVERY production chain shape.
//
// # The contract this file pins
//
// The repository has exactly one production outbound that implements adapter.BypassableOutbound -
// `direct` - and exactly two groups that declare adapter.FastPathAuthorizedGroup - `selector` and
// `urltest`, both transparent. Given that:
//
//  1. a chain whose leaf is not `direct` is refused by resolveFastPathRequest before any policy is
//     read, whatever the field says;
//  2. an explicit policy anywhere in the chain is the chain's decision, scanned outermost to inner.
//     An explicit false therefore WITHHOLDS the attempt on a transparent-direct chain;
//  3. when the chain states no policy at all, a transparent chain over `direct` is authorised by
//     directFastPathLeafIsIntrinsic (silence is not a refusal);
//  4. a single-leaf `direct` chain needs no authorisation at all, because the chain gate is skipped
//     when len(chain) == 1, and no group exists to carry a policy.
//
// So the field has exactly one observable control-plane effect in production: `false` turns the
// bypass ATTEMPT off on the one shape that can take it. `unset` and `true` are indistinguishable;
// neither can grant a bypass the shape forbids. In ordinary TUN the verdict still does not change
// where the bytes go (see protocol/tun and e2e); the effect is only visible where the transport
// honours the verdict, which today is Linux auto_redirect.
//
// # Why this is a tripwire rather than a description
//
// The contract holds only while `direct` is the only bypassable leaf. The day a second one exists,
// the field becomes able to authorise shapes that are not the intrinsic one, and every conclusion in
// the D0 review has to be re-derived. These tests fail at that moment, with that instruction in the
// failure message, rather than letting the review rot.
//
// TestDirectIsTheOnlyProductionBypassableOutbound is the structural half: it scans the production
// tree for CanBypass implementations and names the exact set.

// productionChainShape describes one chain the router can actually resolve in a shipped
// configuration. `groups` lists the logical groups from outermost to innermost; each entry is one of
// "selector", "urltest" (transparent, may carry a policy) or "loadbalance" (opaque).
type productionChainShape struct {
	name       string
	groups     []string
	proxyLeaf  bool
	wantBypass bool
}

// productionChainShapes is exhaustive over the group types that exist in production: every
// combination of transparent selection layers (selector, urltest), with and without an opaque
// `loadbalance` in the middle, over a direct and a proxy leaf. `loadbalance` is the real opaque
// group - it may own the dial, the retry and the accounting of the flow it selected, so bypassing it
// would discard those semantics.
var productionChainShapes = []productionChainShape{
	{name: "bare direct", wantBypass: true},
	{name: "selector/direct", groups: []string{"selector"}, wantBypass: true},
	{name: "urltest/direct", groups: []string{"urltest"}, wantBypass: true},
	{name: "selector/urltest/direct", groups: []string{"selector", "urltest"}, wantBypass: true},
	{name: "urltest/selector/direct", groups: []string{"urltest", "selector"}, wantBypass: true},
	{name: "selector/loadbalance/direct", groups: []string{"selector", "loadbalance"}},
	{name: "loadbalance/direct", groups: []string{"loadbalance"}},
	{name: "selector/proxy", groups: []string{"selector"}, proxyLeaf: true},
	{name: "urltest/selector/proxy", groups: []string{"urltest", "selector"}, proxyLeaf: true},
	{name: "loadbalance/proxy", groups: []string{"loadbalance"}, proxyLeaf: true},
	{name: "selector/loadbalance/proxy", groups: []string{"selector", "loadbalance"}, proxyLeaf: true},
}

// buildProductionChain assembles the doubles for one shape.
//
// The leaf is eligibleLeaf() (a direct outbound that reports itself bypassable) or
// &nonBypassableOutbound{} (a proxy). The groups are the same doubles the rest of the fast-path tests
// use, so this file cannot pass on a shape the product cannot produce.
func buildProductionChain(shape productionChainShape, tags []string) (defaultOutbound adapter.Outbound, leaf adapter.Outbound) {
	leaf = adapter.Outbound(eligibleLeaf())
	if shape.proxyLeaf {
		leaf = &nonBypassableOutbound{}
	}
	outbound := leaf
	for index := len(shape.groups) - 1; index >= 0; index-- {
		switch shape.groups[index] {
		case "selector":
			outbound = &transparentGroup{tag: tags[index], selected: outbound}
		case "urltest":
			outbound = &transparentUrltest{tag: tags[index], selected: outbound}
		case "loadbalance":
			outbound = &opaqueGroup{tag: tags[index], selected: outbound}
		default:
			panic("unknown production group kind: " + shape.groups[index])
		}
	}
	return outbound, leaf
}

// productionTags returns one tag per group for the requested tag style. The style is a dimension of
// the measurement, not decoration: resolveFastPathRequest falls back to the same AI tag matcher
// traffic_class uses, so an AI-looking tag is the one way a chain could become authorised without an
// explicit policy.
func productionTags(shape productionChainShape, aiStyle bool) []string {
	tags := make([]string, len(shape.groups))
	for index := range shape.groups {
		if aiStyle {
			tags[index] = fmt.Sprintf("✨ AI %d", index)
			continue
		}
		tags[index] = fmt.Sprintf("🌐 普通 %d", index)
	}
	return tags
}

// isCarrier reports whether the group at this level may carry a fast_path policy.
func (s productionChainShape) isCarrier(index int) bool {
	return s.groups[index] != "loadbalance"
}

// policyCombination is one assignment of the tri-state field to the policy carriers in a chain.
type policyCombination struct {
	name     string
	policies map[string]bool
}

// hasFalse reports whether this combination states an explicit false anywhere.
func (c policyCombination) hasFalse() bool {
	for _, enabled := range c.policies {
		if !enabled {
			return true
		}
	}
	return false
}

// outermostDecision returns the first explicit value scanning the chain outermost to inner, and
// whether one exists. It is the test's independent statement of the precedence rule, so the
// assertions below do not derive their expected value from the function under test.
func (c policyCombination) outermostDecision(shape productionChainShape, tags []string) (bool, bool) {
	for index := range shape.groups {
		if !shape.isCarrier(index) {
			continue
		}
		if value, loaded := c.policies[tags[index]]; loaded {
			return value, true
		}
	}
	return false, false
}

// policyCombinations enumerates unset/true/false for each carrier group. The deepest production
// shape has two carriers, so 3^2 plus the per-carrier rows is every combination that can exist.
func policyCombinations(shape productionChainShape, tags []string) []policyCombination {
	carriers := make([]string, 0, len(shape.groups))
	for index := range shape.groups {
		if shape.isCarrier(index) {
			carriers = append(carriers, tags[index])
		}
	}
	combinations := []policyCombination{{name: "no policy map"}}
	for _, carrier := range carriers {
		combinations = append(combinations,
			policyCombination{name: carrier + "=true", policies: map[string]bool{carrier: true}},
			policyCombination{name: carrier + "=false", policies: map[string]bool{carrier: false}},
		)
	}
	if len(carriers) >= 2 {
		for _, first := range []bool{true, false} {
			for _, second := range []bool{true, false} {
				combinations = append(combinations, policyCombination{
					name:     fmt.Sprintf("%s=%v,%s=%v", carriers[0], first, carriers[1], second),
					policies: map[string]bool{carriers[0]: first, carriers[1]: second},
				})
			}
		}
	}
	return combinations
}

// productionPolicies converts one combination into the map box.go installs on the router.
func productionPolicies(combination policyCombination) FastPathPolicies {
	policies := make(FastPathPolicies)
	for tag, enabled := range combination.policies {
		policies[tag] = option.FastPathPolicy{Enabled: enabled}
	}
	return policies
}

// productionAction resolves one flow through the public pre-match entry point.
func productionAction(t *testing.T, shape productionChainShape, tags []string, combination policyCombination, network string, destination M.Socksaddr) (adapter.PreMatchAction, BypassVerdict) {
	t.Helper()
	outbound, leaf := buildProductionChain(shape, tags)
	router := fastPathRouter(t, outbound, productionPolicies(combination))
	metadata := fastBypassMetadata(network, destination)
	chain, err := resolveOutbound(outbound, &metadata, network, true)
	require.NoError(t, err)
	authorised := resolveFastPathRequest(chain, router.fastPathPolicies)
	verdict := router.canFastBypass(&metadata, destination, chain, leaf, authorised)
	metadata = fastBypassMetadata(network, destination)
	result := router.preMatchFlow(context.Background(), &metadata, destination, nil, "")
	return result.Action, verdict
}

// TestFastPathUnsetAndTrueAreIndistinguishable is the first half of the contract.
//
// With no explicit false anywhere, the field cannot change anything: every row resolves to the
// shape's own action and to the same BypassVerdict, including the refusal reason.
func TestFastPathUnsetAndTrueAreIndistinguishable(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")

	for _, shape := range productionChainShapes {
		for _, aiStyle := range []bool{false, true} {
			tags := productionTags(shape, aiStyle)
			var combinations []policyCombination
			for _, combination := range policyCombinations(shape, tags) {
				if !combination.hasFalse() {
					combinations = append(combinations, combination)
				}
			}
			for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
				t.Run(fmt.Sprintf("%s/ai=%v/%s", shape.name, aiStyle, network), func(t *testing.T) {
					wantAction := adapter.PreMatchContinue
					if shape.wantBypass {
						wantAction = adapter.PreMatchBypass
					}
					var reference BypassVerdict
					var referenceName string
					for _, combination := range combinations {
						action, verdict := productionAction(t, shape, tags, combination, network, destination)
						require.Equal(t, wantAction, action,
							"%s with %s must produce the shape's own action", shape.name, combination.name)
						require.Equal(t, shape.wantBypass, verdict.BypassAllowed(),
							"%s with %s must produce the shape's own verdict", shape.name, combination.name)
						if referenceName == "" {
							reference, referenceName = verdict, combination.name
							continue
						}
						require.Equal(t, reference, verdict,
							"%s: %s produced %q but %s produced %q - unset and true must be "+
								"indistinguishable", shape.name, combination.name, verdict, referenceName,
							reference)
					}
				})
			}
		}
	}
}

// TestFastPathFalseOnlySuppressesTheIntrinsicShape is the second half: the one thing the field can
// do in production is withhold the attempt on a transparent-direct chain. Everywhere else an
// explicit false changes nothing, because the shape was already refused.
func TestFastPathFalseOnlySuppressesTheIntrinsicShape(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")

	for _, shape := range productionChainShapes {
		for _, aiStyle := range []bool{false, true} {
			tags := productionTags(shape, aiStyle)
			var falseCombinations []policyCombination
			for _, combination := range policyCombinations(shape, tags) {
				if combination.hasFalse() {
					falseCombinations = append(falseCombinations, combination)
				}
			}
			if len(falseCombinations) == 0 {
				// A bare direct leaf has no group that could carry the field; that is asserted
				// separately by TestExplicitFastPathFalseNeedsAGroup.
				continue
			}
			for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
				t.Run(fmt.Sprintf("%s/ai=%v/%s", shape.name, aiStyle, network), func(t *testing.T) {
					_, baselineVerdict := productionAction(t, shape, tags,
						policyCombination{name: "no policy map"}, network, destination)

					for _, combination := range falseCombinations {
						action, verdict := productionAction(t, shape, tags, combination, network, destination)

						// The chain's own precedence: the outermost explicit value decides; an
						// inner false under an outer true does NOT withhold anything.
						decidedValue, decided := combination.outermostDecision(shape, tags)
						wantBypass := shape.wantBypass && (!decided || decidedValue)

						if wantBypass {
							require.True(t, verdict.BypassAllowed(),
								"%s with %s: the outer explicit true must still take the bypass",
								shape.name, combination.name)
							require.Equal(t, adapter.PreMatchBypass, action)
							continue
						}

						require.False(t, verdict.BypassAllowed(),
							"%s with %s must never allow a bypass", shape.name, combination.name)
						require.Equal(t, adapter.PreMatchContinue, action,
							"%s with %s: a withheld attempt continues on the normal path",
							shape.name, combination.name)

						if !shape.wantBypass {
							require.Equal(t, baselineVerdict, verdict,
								"%s with %s: the shape is already refused, so a policy cannot "+
									"change the refusal reason", shape.name, combination.name)
							continue
						}
						require.Equal(t, BypassRefusedOutboundChain, verdict,
							"%s with %s: without the grant the chain gate is the refusal, which is "+
								"the same gate an unauthorised group flow always hit",
							shape.name, combination.name)
					}
				})
			}
		}
	}
}

// TestDirectIntrinsicAuthorisationRequiresSilence pins where the grant comes from.
//
// On every transparent-only chain over `direct`, silence and an explicit true both authorise, an
// explicit false withholds, and withholding it refuses the flow. The control matters: without it the
// test would also pass if the chain gate had stopped consulting the argument at all.
func TestDirectIntrinsicAuthorisationRequiresSilence(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")

	directShapes := make([]productionChainShape, 0, len(productionChainShapes))
	for _, shape := range productionChainShapes {
		// A bare direct leaf has no group, so there is no authorisation to resolve: the chain gate
		// is skipped when len(chain) == 1 and the policy cannot speak to it at all.
		if !shape.wantBypass || len(shape.groups) == 0 {
			continue
		}
		directShapes = append(directShapes, shape)
	}
	require.NotEmpty(t, directShapes, "the table must contain the transparent-direct shapes")

	for _, shape := range directShapes {
		for _, aiStyle := range []bool{false, true} {
			tags := productionTags(shape, aiStyle)
			for _, combination := range policyCombinations(shape, tags) {
				for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
					outbound, leaf := buildProductionChain(shape, tags)
					router := fastPathRouter(t, outbound, productionPolicies(combination))
					metadata := fastBypassMetadata(network, destination)
					chain, err := resolveOutbound(outbound, &metadata, network, true)
					require.NoError(t, err)

					wantAuthorised := true
					if decidedValue, decided := combination.outermostDecision(shape, tags); decided {
						wantAuthorised = decidedValue
					}
					require.Equal(t, wantAuthorised, resolveFastPathRequest(chain, router.fastPathPolicies),
						"%s with %s: the outermost explicit value decides; silence keeps the "+
							"intrinsic grant", shape.name, combination.name)

					require.Equal(t, wantAuthorised,
						router.canFastBypass(&metadata, destination, chain, leaf,
							resolveFastPathRequest(chain, router.fastPathPolicies)).BypassAllowed(),
						"%s with %s: the resolved authorisation must reach the gate unchanged",
						shape.name, combination.name)

					// The control: withholding the authorisation by hand refuses the same chain, so
					// the grant really is load-bearing.
					require.False(t,
						router.canFastBypass(&metadata, destination, chain, leaf, false).BypassAllowed(),
						"%s: withholding the authorisation must refuse the flow", shape.name)
				}
			}
		}
	}
}

// TestExplicitFastPathFalseSuppressesTheDirectChain is the focused regression for the precedence fix.
func TestExplicitFastPathFalseSuppressesTheDirectChain(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")
	outbound, _ := buildProductionChain(productionChainShape{
		name: "selector/direct", groups: []string{"selector"}, wantBypass: true,
	}, []string{"国内直连"})

	falseRouter := fastPathRouter(t, outbound, FastPathPolicies{
		"国内直连": option.FastPathPolicy{Enabled: false},
	})
	unsetRouter := fastPathRouter(t, outbound, FastPathPolicies{})

	metadata := fastBypassMetadata(N.NetworkTCP, destination)
	chain, err := resolveOutbound(outbound, &metadata, N.NetworkTCP, true)
	require.NoError(t, err)

	authorised, decided := resolveFastPathPolicy(chain, falseRouter.fastPathPolicies)
	require.True(t, decided, "the explicit false must be found by the policy scan")
	require.False(t, authorised, "and it says refuse")
	require.False(t, resolveFastPathRequest(chain, falseRouter.fastPathPolicies),
		"the explicit decision must win over the intrinsic grant")
	require.True(t, resolveFastPathRequest(chain, unsetRouter.fastPathPolicies),
		"while the same chain with no policy keeps the intrinsic grant")

	falseResult := falseRouter.preMatchFlow(context.Background(), &metadata, destination, nil, "")
	require.Equal(t, adapter.PreMatchContinue, falseResult.Action,
		"the flow falls through to the normal direct path instead of attempting the bypass")
	unsetMetadata := fastBypassMetadata(N.NetworkTCP, destination)
	unsetResult := unsetRouter.preMatchFlow(context.Background(), &unsetMetadata, destination, nil, "")
	require.Equal(t, adapter.PreMatchBypass, unsetResult.Action)
}

// TestExplicitFastPathFalseNeedsAGroup records the one shape where the field cannot be expressed at
// all: a bare direct leaf has no group to carry it, and box.go refuses the key on a leaf. The chain
// gate is skipped for it, so it is authorised by structure rather than by policy.
func TestExplicitFastPathFalseNeedsAGroup(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")
	outbound, _ := buildProductionChain(productionChainShape{name: "bare direct", wantBypass: true}, nil)
	router := fastPathRouter(t, outbound, FastPathPolicies{})

	metadata := fastBypassMetadata(N.NetworkTCP, destination)
	chain, err := resolveOutbound(outbound, &metadata, N.NetworkTCP, true)
	require.NoError(t, err)
	require.Len(t, chain, 1)
	require.False(t, resolveFastPathRequest(chain, router.fastPathPolicies),
		"a one-element chain has no group, so nothing is resolved")
	require.Equal(t, adapter.PreMatchBypass,
		router.preMatchFlow(context.Background(), &metadata, destination, nil, "").Action,
		"but the chain gate does not apply, so the leaf's own eligibility decides")
}

// TestDirectIsTheOnlyProductionBypassableOutbound is the structural half of the contract.
//
// It scans the production tree for CanBypass method declarations. The count is part of the argument:
// with two bypassable leaves, the field stops being an off switch for one shape and becomes a way to
// authorise shapes the intrinsic rule does not cover, so the whole D0 review has to be repeated.
func TestDirectIsTheOnlyProductionBypassableOutbound(t *testing.T) {
	implementations := scanProductionTree(t, ") CanBypass(")
	require.Equal(t, []string{"protocol/direct/outbound.go"}, implementations,
		"the fast path's authorisation model is derived from `direct` being the single production "+
			"outbound that implements adapter.BypassableOutbound. A second implementation makes the "+
			"tri-state field able to authorise shapes that are not the intrinsic one, which "+
			"invalidates the D0 review in docs/fork/tun-fast-path-value-review.md: re-derive it, "+
			"then decide whether the new leaf should be bypassable at all, before updating this list")
}

// TestFastPathTransparentGroupsAreTheOnlyPolicyCarriers is the companion structural check: the
// intrinsic rule treats every group that declares the marker as a pure selection layer, so a new
// declaration is a new claim that bypassing it loses nothing.
func TestFastPathTransparentGroupsAreTheOnlyPolicyCarriers(t *testing.T) {
	implementations := scanProductionTree(t, ") FastPathTransparent(")
	require.Equal(t, []string{"protocol/group/selector.go", "protocol/group/urltest.go"}, implementations,
		"a new FastPathAuthorizedGroup declares that bypassing it discards no selection, retry or "+
			"accounting semantics. Add it to docs/fork/tun-fast-path-value-review.md's chain table "+
			"and to productionChainShapes before extending this list")
}

// scanProductionTree returns the production files containing marker, relative to the repository
// root. Test files, the client submodules and documentation are excluded: the claim is about code
// that ships in the box binary.
func scanProductionTree(t *testing.T, marker string) []string {
	t.Helper()
	repositoryRoot, err := filepath.Abs("..")
	require.NoError(t, err)

	var matches []string
	walkErr := filepath.WalkDir(repositoryRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "clients", "docs", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !strings.Contains(string(content), marker) {
			return nil
		}
		relative, relErr := filepath.Rel(repositoryRoot, path)
		if relErr != nil {
			return relErr
		}
		matches = append(matches, filepath.ToSlash(relative))
		return nil
	})
	require.NoError(t, walkErr)
	return matches
}
