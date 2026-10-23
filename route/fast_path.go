package route

import (
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficclass"
	C "github.com/sagernet/sing-box/constant"
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

// directFastPathLeafIsIntrinsic reports whether this chain ends in a genuine `direct` leaf whose only
// route to that leaf is a series of transparent pure-selection groups.
//
// # The defect this answers
//
// Wrapping `direct` in a selector purely for grouping is a configuration a user reaches for: one
// "国内直连" selector holding a single direct member. Before this, that selector's ABSENCE of
// `fast_path` was read as "not authorised", the chain gate then refused
// (BypassRefusedOutboundChain), and the flow silently degraded to a userspace direct connection -
// losing a Direct Fast Path that the same direct outbound takes when it is named without a group in
// front of it. The group was being treated as if it had turned the fast path off, when it had said
// nothing at all.
//
// # Silence is not a decision; a decision is
//
// The rule this function implements is "a transparent selection layer that says NOTHING does not
// switch the fast path off". It is not "a transparent selection layer cannot switch it off": a group
// that writes `"fast_path": false` has made a decision, and resolveFastPathRequest consults the
// explicit policy FIRST for exactly that reason. Before that precedence was fixed, an explicit false
// was found by the policy scan and then overruled here, which made the documented tri-state
// impossible to use as an off switch on the one leaf that can actually bypass. The intrinsic grant
// now applies only where no element of the chain states a policy.
//
// # Why the transparent-chain condition is not decorative
//
// The condition is what keeps this from becoming "any chain ending in direct is bypassed". A group
// that owns the dial, retries, or carries accounting of its own is NOT transparent, and skipping it
// would discard semantics the configuration asked for - so `loadbalance` in the chain still refuses
// here, exactly as before. Every element in front of the leaf must pass the same
// `canCarryFastPathPolicy` test the gate uses, so transparency has ONE definition rather than two
// that can drift.
//
// # What it deliberately does not do
//
// It grants no eligibility. `direct.CanBypass` and every other blocker in canFastBypass are reached
// afterwards and unchanged, so a direct outbound configured with a bind address, a routing mark or a
// network strategy still refuses - and still refuses for its own stated reason. This function only
// answers "may this flow be CONSIDERED at all", which is the group authorisation question, not the
// eligibility question.
//
// # Why it stays cheap
//
// It reads the already-resolved chain and calls only methods that are already on it. There is no
// graph re-walk, no map lookup keyed by tag and no allocation, and it runs once per flow at setup,
// never per packet. The transparent-group probe is the same call the chain gate makes a few lines
// later, so the added cost is bounded by the chain length.
func directFastPathLeafIsIntrinsic(chain []adapter.Outbound) bool {
	// A single element has no group in front of it, and that case already behaves correctly: the
	// chain gate's `len(chain) != 1` branch does not apply, so the leaf is consulted directly. This
	// function exists only for the wrapped case.
	if len(chain) < 2 {
		return false
	}
	// The leaf must be bypassable before anything is asked of it, and the assertion is what makes the
	// Type() call below safe rather than merely usual: BypassableOutbound embeds Outbound, so a value
	// that satisfies it is guaranteed to answer Type(). Asking first and asserting later would put an
	// interface call on a value that has not been established to be one - which is not a theoretical
	// concern, because a test double that embeds a NIL adapter.Outbound still satisfies every
	// assertion that does not CALL anything, and would panic here.
	leaf, isBypassable := chain[len(chain)-1].(adapter.BypassableOutbound)
	if !isBypassable {
		return false
	}
	// The leaf must be a REAL direct outbound, asked by type rather than by tag. A tag is
	// user-chosen - `outbound.Tag() == "direct"` would miss a direct outbound named anything else
	// and, worse, would match a proxy a user happened to name "direct". C.TypeDirect is the
	// registered identity of protocol/direct and is what the type answers for itself.
	//
	// `direct` is currently the ONLY production outbound that implements CanBypass, so this is
	// narrower than the bypassable-leaf test the other branch uses. That is intentional: this
	// special case is about `direct` specifically, and it must not be read as "any bypassable
	// outbound may now ignore its group's policy". The test double that would catch a regression here
	// is bypassableNonDirectOutbound, which exists precisely to separate "can bypass" from "is direct".
	if leaf.Type() != C.TypeDirect {
		return false
	}
	// And nothing opaque may be in between: every element in front of the leaf must be a group whose
	// only effect on the flow is choosing it.
	//
	// The transparency test is written out here rather than delegated to fastPathChainReachesLeaf,
	// because that function now calls THIS one. Factoring it the other way round produced mutual
	// recursion and a stack overflow, which is the kind of defect a test cannot miss but a reader
	// easily can. canCarryFastPathPolicy remains the single definition of "transparent", so the two
	// loops cannot drift even though they are written twice.
	for _, outbound := range chain[:len(chain)-1] {
		if !canCarryFastPathPolicy(outbound) {
			return false
		}
	}
	return true
}

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
//  2. Otherwise the FIRST explicit fast_path, scanning outermost to inner. An explicit decision is
//     the chain's decision: it wins over the intrinsic grant below, and an explicit false wins over
//     automatic detection, just as an explicit "traffic_class": "default" stops automatic
//     classification. This is the order that makes the tri-state usable as an off switch on the one
//     leaf the product can bypass.
//  3. Only if NO element of the chain carries an explicit policy, a chain that is nothing but
//     transparent selection layers in front of a real `direct` leaf is authorised. This is the
//     Direct Fast Path restated as an intrinsic property of `direct`: a pure selection layer that
//     says nothing does not get to switch it off. See directFastPathLeafIsIntrinsic.
//  4. Only if neither applies, automatic tag detection - the same AI matcher that traffic_class
//     uses, again outermost to inner.
//  5. Otherwise false.
//
// # Where the intrinsic rule sits, and where it does not
//
// Rule 3 is reached only when the chain states no policy at all, and applies only to the
// direct-with-transparent-chain shape. Every other shape - a proxy leaf, a leaf that cannot carry
// the fast path, any chain containing an opaque group - falls through to the same rules 2-4 that
// existed before, including nested explicit-true/false precedence. That boundary is the whole safety
// argument for this rule, so it is stated here rather than left to the call order.
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
// authorisation except through rule 3, where the grant comes from the SHAPE of the chain (transparent
// groups in front of a direct leaf) rather than from anything the leaf reports about itself.
func resolveFastPathRequest(chain []adapter.Outbound, policies FastPathPolicies) bool {
	// A single-element chain has no logical group to carry a policy, so there is nothing to
	// authorise and the flow keeps exactly the behaviour it had before this feature existed.
	if len(chain) < 2 {
		return false
	}
	if _, leafCanCarry := chain[len(chain)-1].(adapter.BypassableOutbound); !leafCanCarry {
		return false
	}
	// Rule 2. An explicit decision anywhere in the chain is the chain's decision, and it wins over
	// both the intrinsic grant and automatic detection. This is what makes an explicit false able to
	// see the fast path off on the one leaf that can take it.
	if authorised, decided := resolveFastPathPolicy(chain, policies); decided {
		return authorised
	}
	// Rule 3. Nothing in the chain stated a policy, and the shape is transparent groups over
	// `direct`: silence is not a refusal.
	if directFastPathLeafIsIntrinsic(chain) {
		return true
	}
	// Rule 4. No explicit policy and not the intrinsic shape: automatic tag detection.
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
