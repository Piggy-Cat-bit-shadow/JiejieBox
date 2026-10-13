package route

import (
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficclass"
	"github.com/sagernet/sing-box/option"
)

// FastPathPolicies maps a logical group's tag to the fast_path intent its configuration states
// explicitly.
//
// # Why a tag map rather than a field on the outbound
//
// This is the same placement decision as TrafficClassPolicies, for the same structural reason: the
// field is declared on the configuration envelope, so it exists for every logical group type
// without any of them knowing about it. Attaching it to the outbound OBJECT would mean editing
// every constructor, or wrapping the outbound - and wrapping breaks the type assertions that
// resolveOutbound depends on (`outbound.(adapter.OutboundGroup)` is how a selector is followed to
// its leaf). A tag-keyed lookup leaves every outbound type untouched and cannot interfere with
// group resolution.
//
// The lookup runs at flow setup only. Nothing on the per-read, per-write or per-packet path
// consults it, and nothing here is ever memoised onto an outbound.
type FastPathPolicies map[string]option.FastPathPolicy

// resolveFastPathRequest determines whether a flow's chain authorises an ATTEMPT at the Direct Fast
// Path, from the complete logical outbound chain.
//
// The name is deliberate. This reports a group's AUTHORISATION, never an eligibility decision: a
// true result means "the group this flow entered asked for the fast path", and it is the caller's
// job to then consult canFastBypass, which is the only thing that decides whether the flow may
// actually leave userspace. Nothing in this file, and nothing that reads it, may treat a true
// result as BypassAllowed.
//
// # Precedence
//
//  1. A chain whose LEAF cannot carry the fast path on its own is never authorised. One element
//     means the flow resolved straight to that outbound with no logical group in front of it, so
//     there is no group policy to read and the existing behaviour stands untouched.
//  2. Otherwise the FIRST explicit fast_path, scanning outermost to inner. An explicit false is a
//     decision, so it wins over automatic detection just as an explicit "traffic_class": "default"
//     stops automatic classification.
//  3. Only if NO element of the chain carries an explicit policy, automatic tag detection - the
//     same AI matcher that traffic_class uses, again outermost to inner.
//  4. Otherwise false.
//
// # Why the outermost element wins
//
// Identical to traffic_class, and for the identical business reason. The intent belongs to the
// logical group the flow was routed into, not to the node that happens to serve it: one residential
// leaf is reachable through an AI group and through a general group at the same time, so an
// authorisation inferred from the leaf would grant the fast path to every flow that ever used that
// leaf. Resolving per flow from the chain is what keeps the two paths independent.
//
// An explicit policy anywhere in the chain suppresses automatic detection for the whole chain. The
// chain is one policy decision, not a set of independent ones.
//
// # The leaf is consulted, and only for a negative
//
// The leaf participates solely to answer "could this outbound be bypassed at all". It never grants
// authorisation: a leaf cannot opt a flow into the fast path, because no leaf can carry this
// policy - see resolveFastPathPolicy.
func resolveFastPathRequest(chain []adapter.Outbound, policies FastPathPolicies) bool {
	// A single-element chain has no logical group to carry a policy, so there is nothing to
	// authorise and the flow keeps exactly the behaviour it had before this feature existed.
	if len(chain) < 2 {
		return false
	}
	if _, leafCanCarry := chain[len(chain)-1].(adapter.BypassableOutbound); !leafCanCarry {
		return false
	}
	if authorised, decided := resolveFastPathPolicy(chain, policies); decided {
		return authorised
	}
	for _, outbound := range chain {
		if !canCarryFastPathPolicy(outbound) {
			continue
		}
		if trafficclass.MatchTag(outbound.Tag()) {
			return true
		}
	}
	return false
}

// resolveFastPathPolicy returns the first explicit fast_path policy in the chain, outermost to
// inner, and whether one was found at all.
//
// The three-state config type exists precisely so that "explicitly false" is findable and
// distinguishable from "unset": a caller that read a plain bool could not tell the two apart, and
// would let automatic detection reopen a group an operator had switched off.
//
// The carrier check is what keeps the key GROUP-ONLY at every level of a nested chain. A policy can
// only be stored under a tag whose outbound implements adapter.FastPathAuthorizedGroup - box.go
// refuses the configuration otherwise - so an element that cannot implement it contributes nothing
// here. Requiring it again at read time means a map populated by any other route still cannot turn a
// leaf into a policy carrier.
func resolveFastPathPolicy(chain []adapter.Outbound, policies FastPathPolicies) (bool, bool) {
	for _, outbound := range chain {
		if !canCarryFastPathPolicy(outbound) {
			continue
		}
		policy, loaded := policies[outbound.Tag()]
		if !loaded {
			continue
		}
		return policy.Enabled, true
	}
	return false, false
}

// canCarryFastPathPolicy reports whether this outbound is a logical group that may hold a fast_path
// policy.
//
// Both halves are required. adapter.OutboundGroup alone is not enough: `loadbalance` is a group and
// may own the dial of the flow it selected, so a chain through it is NOT transparent and bypassing
// it would discard the retry its configuration asked for. adapter.FastPathAuthorizedGroup is
// declared only by the pure selection groups (selector, urltest), and that declaration is the
// exclusion - no tag list and no type-name switch is involved.
//
// The method is called rather than merely asserted. An earlier version used an empty marker
// interface and every group-shaped type - including the test double that was supposed to model an
// opaque group - satisfied it, so the check silently passed for the wrong reason. See the interface's
// doc comment.
func canCarryFastPathPolicy(outbound adapter.Outbound) bool {
	group, isGroup := outbound.(adapter.OutboundGroup)
	if !isGroup {
		return false
	}
	transparent, declares := group.(adapter.FastPathAuthorizedGroup)
	return declares && transparent.FastPathTransparent()
}

// fastPathChainReachesLeaf reports whether this chain consists solely of transparent logical groups
// in front of one leaf, so that bypassing the groups loses nothing about the flow.
//
// It exists so that BypassRefusedOutboundChain can keep its original meaning for every chain that
// is not transparent. The check is a loop over the chain elements and runs once per flow at setup;
// it is not on any packet path.
func fastPathChainReachesLeaf(chain []adapter.Outbound) bool {
	// The last element is the leaf, and the caller has already established that it is bypassable.
	// Everything in front of it must be a group whose only effect on the flow is choosing it.
	for _, outbound := range chain[:len(chain)-1] {
		if !canCarryFastPathPolicy(outbound) {
			return false
		}
	}
	return true
}
