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
// # The theorem this file pins
//
// The repository has exactly one production outbound that implements adapter.BypassableOutbound -
// `direct` - and exactly two groups that declare adapter.FastPathAuthorizedGroup - `selector` and
// `urltest`, both transparent. Given that, `fast_path` cannot change a verdict:
//
//  1. a chain whose leaf is not `direct` is refused by `resolveFastPathRequest` before any policy is
//     read, because the leaf cannot carry the fast path;
//  2. a chain of transparent groups ending in `direct` is authorised by
//     `directFastPathLeafIsIntrinsic` regardless of policy;
//  3. a chain containing an opaque group is refused by `canFastBypass`'s chain check regardless of
//     what the policy says;
//  4. a single-leaf `direct` chain needs no authorisation at all, because the chain gate is skipped
//     when `len(chain) == 1`.
//
// So `true`, `false` and unset produce the same verdict on every production shape, on every
// platform, in both transport modes (ordinary TUN and Linux auto_redirect). That is the D0 finding
// of docs/fork/tun-fast-path-value-review.md: the field is a forward-compatibility surface, not a
// switch that currently does something. The verdicts themselves are safe; the risk is describing the
// field as an acceleration the user can turn on.
//
// # Why this is a tripwire rather than a description
//
// The theorem holds only while `direct` is the only bypassable leaf. The day a second one exists, or
// the day the intrinsic rule's precedence changes, `fast_path` becomes observable and every
// conclusion in the D0 review has to be re-derived - including whether `false` should now be able to
// suppress the fast path. These tests fail at that moment, with that instruction in the failure
// message, rather than letting the review rot.
//
// TestDirectIsTheOnlyProductionBypassableOutbound is the structural half: it scans the production
// tree for `CanBypass` implementations and names the exact set. The table below is the behavioural
// half, and it is built from the same set.

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
// traffic_class uses, so an AI-looking tag is the one other way a chain could become authorised
// without an explicit policy.
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

// TestFastPathPoliciesCannotChangeAnyProductionVerdict is the behavioural half of the theorem.
//
// It resolves every production shape with every assignment of the tri-state field, on TCP and UDP,
// under both an AI-looking and an ordinary tag, and asserts that the router's ACTION is identical in
// all of them. It asserts the expected action too, so a table that silently stopped resolving could
// not pass by producing Continue everywhere.
func TestFastPathPoliciesCannotChangeAnyProductionVerdict(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")

	for _, shape := range productionChainShapes {
		for _, aiStyle := range []bool{false, true} {
			tags := productionTags(shape, aiStyle)
			combinations := policyCombinations(shape, tags)
			for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
				t.Run(fmt.Sprintf("%s/ai=%v/%s", shape.name, aiStyle, network), func(t *testing.T) {
					want := adapter.PreMatchContinue
					if shape.wantBypass {
						want = adapter.PreMatchBypass
					}
					// Every row is asserted against the SHAPE's expected action, which is stronger
					// than comparing the rows to each other: a table whose resolution silently broke
					// would produce Continue everywhere and pass a pure "all rows agree" check.
					for _, combination := range combinations {
						outbound, _ := buildProductionChain(shape, tags)
						router := fastPathRouter(t, outbound, productionPolicies(combination))
						metadata := fastBypassMetadata(network, destination)
						result := router.preMatchFlow(context.Background(), &metadata, destination, nil, "")
						require.Equal(t, want, result.Action,
							"%s with %s must produce the shape's own verdict: the tri-state field is "+
								"not allowed to change it", shape.name, combination.name)
					}
				})
			}
		}
	}
}

// TestFastPathPoliciesCannotChangeAnyProductionBypassVerdict is the verdict-level form, and it is
// stronger than the action-level test above: it compares the full BypassVerdict, including the
// refusal REASON.
//
// The reason matters because "the field does nothing" is only true if the reason does not move
// either. resolveFastPathRequest refuses a chain whose leaf cannot carry the fast path BEFORE it
// reads any policy, so a proxy leaf never even reaches the policy scan; and a chain that contains an
// opaque group fails canFastBypass's own chain check whether or not a policy authorised it. The only
// chain where the authorisation could differ - transparent prefix, opaque group, direct leaf - is
// refused at the chain gate in both states, with the same spelling. So the field changes neither the
// decision nor the diagnostic on a shipped configuration.
func TestFastPathPoliciesCannotChangeAnyProductionBypassVerdict(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")

	for _, shape := range productionChainShapes {
		for _, aiStyle := range []bool{false, true} {
			tags := productionTags(shape, aiStyle)
			combinations := policyCombinations(shape, tags)
			for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
				t.Run(fmt.Sprintf("%s/ai=%v/%s", shape.name, aiStyle, network), func(t *testing.T) {
					var reference BypassVerdict
					var referenceName string
					for _, combination := range combinations {
						outbound, leaf := buildProductionChain(shape, tags)
						router := fastPathRouter(t, outbound, productionPolicies(combination))
						metadata := fastBypassMetadata(network, destination)
						chain, err := resolveOutbound(outbound, &metadata, network, true)
						require.NoError(t, err)

						// The authorisation is resolved from the flow's own chain, exactly as
						// preMatchFlow does it; the policy is the only variable.
						authorised := resolveFastPathRequest(chain, router.fastPathPolicies)
						verdict := router.canFastBypass(&metadata, destination, chain, leaf, authorised)

						require.Equal(t, shape.wantBypass, verdict.BypassAllowed(),
							"%s with %s: the verdict must match the shape, not the policy",
							shape.name, combination.name)
						if referenceName == "" {
							reference, referenceName = verdict, combination.name
							continue
						}
						require.Equal(t, reference, verdict,
							"%s: %s produced %q but %s produced %q - the tri-state field changed a "+
								"production verdict or its refusal reason, so it is observable and "+
								"the D0 review in docs/fork/tun-fast-path-value-review.md must be "+
								"re-derived", shape.name, combination.name, verdict, referenceName,
							reference)
					}
				})
			}
		}
	}
}

// TestDirectIntrinsicAuthorisationIsPolicyIndependent pins WHERE the grant comes from.
//
// On every transparent-only chain over `direct`, the intrinsic rule authorises the flow for every
// assignment of the tri-state field and for both tag styles. Forcing the authorisation argument to
// false on the same chain refuses it, which is what proves the grant really is doing work: the flow
// is allowed because of the SHAPE (transparent groups over direct), not because a policy happened to
// say true.
func TestDirectIntrinsicAuthorisationIsPolicyIndependent(t *testing.T) {
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

					require.True(t, resolveFastPathRequest(chain, router.fastPathPolicies),
						"%s with %s must be authorised by the intrinsic rule", shape.name, combination.name)

					require.True(t, router.canFastBypass(&metadata, destination, chain, leaf, true).BypassAllowed(),
						"%s with %s must reach BypassAllowed when the gate is given the intrinsic "+
							"authorisation", shape.name, combination.name)

					// The control: withhold the authorisation the intrinsic rule supplies and the
					// same chain is refused. Without this, the assertion above would also pass if the
					// chain gate had stopped consulting the argument at all.
					require.False(t, router.canFastBypass(&metadata, destination, chain, leaf, false).BypassAllowed(),
						"%s: withholding the authorisation must refuse the flow, otherwise the "+
							"intrinsic grant is not load-bearing", shape.name)
				}
			}
		}
	}
}

// TestExplicitFastPathFalseDoesNotSuppressTheDirectChainToday pins the one conflict between the
// field's documented tri-state and its precedence, in the direction the shipped code implements.
//
// option/fast_path.go documents "fast_path": false as "refuse, even if the tag looks like AI".
// resolveFastPathRequest rule 2 is checked BEFORE the policy scan, so on a transparent chain over
// `direct` an explicit false is found by the scan and then overruled by the intrinsic rule. That is
// deliberate (afd5b5220c8: a pure selection layer does not get to switch off an intrinsic property
// of `direct`) and it is tested as such, but it means the field cannot suppress the one bypass leaf
// the product has.
//
// The test is written to fail the moment the precedence changes, so that change is a contract
// decision with a failing test and the review document behind it - not a silent behaviour change.
func TestExplicitFastPathFalseDoesNotSuppressTheDirectChainToday(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")
	outbound, _ := buildProductionChain(productionChainShape{
		name: "selector/direct", groups: []string{"selector"}, wantBypass: true,
	}, []string{"国内直连"})
	router := fastPathRouter(t, outbound, FastPathPolicies{
		"国内直连": option.FastPathPolicy{Enabled: false},
	})

	metadata := fastBypassMetadata(N.NetworkTCP, destination)
	chain, err := resolveOutbound(outbound, &metadata, N.NetworkTCP, true)
	require.NoError(t, err)

	authorised, decided := resolveFastPathPolicy(chain, router.fastPathPolicies)
	require.True(t, decided, "the explicit false must be found by the policy scan")
	require.False(t, authorised, "and it says refuse")
	require.True(t, resolveFastPathRequest(chain, router.fastPathPolicies),
		"...but the intrinsic rule is evaluated first and wins. If this assertion fails, the "+
			"precedence changed: update docs/fork/tun-fast-path-value-review.md (the false-semantics "+
			"row of its decision table) and the tests that pin the contract, rather than deleting "+
			"this one")

	result := router.preMatchFlow(context.Background(), &metadata, destination, nil, "")
	require.Equal(t, adapter.PreMatchBypass, result.Action,
		"the shipped precedence lets the flow take the bypass path even though the group said false")
}

// TestDirectIsTheOnlyProductionBypassableOutbound is the structural half of the theorem.
//
// It scans the production tree for CanBypass method declarations. The count is part of the argument:
// with two bypassable leaves, the group authorisation stops being redundant and the whole D0 review
// has to be repeated. A shape-based test cannot see a new implementation that no shape mentions,
// which is exactly the change that would invalidate the conclusion while leaving every other test
// green.
func TestDirectIsTheOnlyProductionBypassableOutbound(t *testing.T) {
	implementations := scanProductionTree(t, ") CanBypass(")
	require.Equal(t, []string{"protocol/direct/outbound.go"}, implementations,
		"the Direct Fast Path's authorisation is redundant only while `direct` is the single "+
			"production outbound that implements adapter.BypassableOutbound. A second implementation "+
			"makes the tri-state field observable and invalidates the D0 review in "+
			"docs/fork/tun-fast-path-value-review.md: re-derive it, then decide whether the new leaf "+
			"should be bypassable at all, before updating this list")
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
