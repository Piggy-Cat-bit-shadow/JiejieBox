package adapter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The three-state algebra a rule's network summary is composed with.
//
// # Why the algebra is tested directly rather than only through a configuration
//
// The composition is what turns a compiled rule into a fact about the NETWORK dimension, and two of
// its cases are not observable through any configuration: whether `Not` maps Never to Always, and
// whether `Or` short-circuits on Always, are properties of the lattice itself. A configuration can
// only show them when a route happens to expose the difference, and the difference is exactly the
// one that decides between a false positive and a false negative.
//
// # The rule the tests below encode
//
// The three states are ordered by how much they claim, and the compositional operators must preserve
// that:
//
//	Never   - the predicate provably rejects EVERY flow of this network
//	Unknown - it may or may not, and nothing here can prove which
//	Always  - it provably accepts EVERY flow of this network
//
// The whole reason Unknown exists is that reading it as either neighbour is wrong in one direction:
// as Always it lets an analyzer claim a protocol is filtered when a rule only filters part of it, and
// as Never it lets a real incompatibility through. Every assertion below is one of those two
// mistakes being made impossible.

func TestNeverIsTheZeroOfConjunctionAndAlwaysItsIdentity(t *testing.T) {
	never := ScopeNever()
	always := ScopeAlways()
	unknown := ScopeUnknown()

	require.Equal(t, RuleMatchNever, never.And(always).TCP,
		"Always is the conjunction identity: a rule whose items all constrain nothing is Always")
	require.Equal(t, RuleMatchNever, always.And(never).TCP,
		"and the identity holds on either side")
	require.Equal(t, RuleMatchNever, never.And(unknown).TCP,
		"a single provable rejection survives an unknown condition beside it - this is what makes "+
			"`network: [udp]` plus a domain condition still provable about tcp")
	require.Equal(t, RuleMatchNever, unknown.And(never).TCP)
	require.Equal(t, RuleMatchAlways, always.And(always).TCP)
	require.Equal(t, RuleMatchUnknown, unknown.And(unknown).TCP,
		"two unknowns do not become a proof")
	require.Equal(t, RuleMatchUnknown, always.And(unknown).TCP,
		"and an unknown beside a neutral condition is still unknown")
}

func TestAlwaysShortCircuitsDisjunctionAndNeverIsItsIdentity(t *testing.T) {
	never := ScopeNever()
	always := ScopeAlways()
	unknown := ScopeUnknown()

	require.Equal(t, RuleMatchAlways, never.Or(always).TCP,
		"a logical `or` is proven when ANY branch is proven")
	require.Equal(t, RuleMatchAlways, always.Or(unknown).TCP,
		"and an unknown branch does not weaken a proven one")
	require.Equal(t, RuleMatchNever, never.Or(never).TCP,
		"every branch rejecting is a proof of rejection")
	require.Equal(t, RuleMatchUnknown, never.Or(unknown).TCP,
		"one unknown branch is enough to lose the proof")
}

func TestNotSwapsTheTwoProofsAndLeavesUnknownAlone(t *testing.T) {
	require.Equal(t, RuleMatchAlways, ScopeNever().Not().TCP,
		"`invert` on a predicate that rejects every flow of a network provably ACCEPTS every flow "+
			"of it; reading that as Unknown would lose the rejection a later rule still has to make")
	require.Equal(t, RuleMatchNever, ScopeAlways().Not().TCP,
		"and the other way round: an inverted catch-all matches nothing")
	require.Equal(t, RuleMatchUnknown, ScopeUnknown().Not().TCP,
		"but 'I cannot prove this predicate rejects the flow' is NOT a proof that it accepts it, "+
			"so an unknown stays unknown under inversion")
}

func TestTheNetworkConditionMapsToTheDimensionAndNothingElse(t *testing.T) {
	require.Equal(t, ScopeAlways(), ScopeOfNetworks(nil),
		"a condition that names no network constrains nothing")
	require.Equal(t, RuleMatchAlways, ScopeOfNetworks([]string{"tcp"}).TCP)
	require.Equal(t, RuleMatchNever, ScopeOfNetworks([]string{"tcp"}).UDP,
		"naming tcp is also a proof about udp, which is what lets a mixed rule be decided at all")
	require.Equal(t, RuleMatchUnknown, ScopeOfNetworks([]string{"tcp"}).Match("icmp"),
		"icmp is outside this dimension, so the honest answer is that nothing is known about it")
	require.Equal(t, RuleMatchUnknown, RuleNetworkScope{}.Match("tcp"),
		"a scope that was never filled in answers Unknown rather than the permissive way")
}

func TestTheTwoNetworksAreMirroredRatherThanSpecialCased(t *testing.T) {
	// A model with a udp special case would pass every udp test and fail here.
	require.Equal(t, ScopeOfNetworks([]string{"udp"}),
		RuleNetworkScope{Decided: true, TCP: RuleMatchNever, UDP: RuleMatchAlways})
	require.Equal(t, ScopeOfNetworks([]string{"tcp", "udp"}),
		RuleNetworkScope{Decided: true, TCP: RuleMatchAlways, UDP: RuleMatchAlways})
}
