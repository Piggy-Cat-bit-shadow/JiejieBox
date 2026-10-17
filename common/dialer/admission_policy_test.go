package dialer

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
)

// Tests for the per-flow Candidate Admission Policy value.
//
// # What is being pinned
//
// The policy is the ONE place a flow's admission decision is made. Before it existed the decision was
// composed by hand at every call site - the strategy filter here, the capability filter there, "was the
// list empty because the policy emptied it" worked out separately at each - and the two terms came from
// readings taken at different instants. These tests are about the VALUE: that its zero is harmless, that
// its four verdicts are distinguishable, that its filters report which of them fired, and that its fast
// path cannot disagree with its slow one.
//
// The wiring - that one flow captures one policy and every stage asks it - is pinned in the entry-point
// suites, because a value test cannot see a second capture.

// admissionStrategySpace is every family strategy, so a table cannot omit one.
func admissionStrategySpace() []C.DomainStrategy {
	return []C.DomainStrategy{
		C.DomainStrategyAsIS,
		C.DomainStrategyPreferIPv4,
		C.DomainStrategyPreferIPv6,
		C.DomainStrategyIPv4Only,
		C.DomainStrategyIPv6Only,
	}
}

// TestZeroAdmissionPolicyAdmitsEverything is the degradation the whole design rests on.
//
// A policy nobody captured - a test double with no network source, a chain that leads to no owner - must
// be a no-op. If the zero refused anything, every dialer that cannot report a capability would start
// failing connections, which is the opposite of the "keep the previous behaviour" contract.
func TestZeroAdmissionPolicyAdmitsEverything(t *testing.T) {
	var policy admissionPolicy
	require.True(t, policy.restrictsNothing(), "the zero policy must take the fast path")
	require.Nil(t, policy.lateAdmission(), "and arm no late-candidate guard")
	require.EqualValues(t, 0, policy.generation())
	for _, destination := range falsificationDestinations() {
		require.True(t, policy.admits(destination), "the zero policy refused %v", destination)
		require.Equal(t, admissionAllowed, policy.verdict(destination))
		require.NoError(t, policy.refusalError(destination))
	}
}

// TestAdmissionSeparatesTheFourVerdicts is §18's requirement, stated as one table.
//
// The four outcomes are not decoration: each has a different remedy, and reporting one as another sends
// the operator to look at the wrong thing. In particular a configuration decision (the operator said
// ipv4_only) must never be reported as a property of the device (the machine has no IPv6), and the two
// device facts - no address at all versus an address with no route out - must not be collapsed.
func TestAdmissionSeparatesTheFourVerdicts(t *testing.T) {
	globalIPv4 := netip.MustParseAddr("93.184.216.34")
	globalIPv6 := netip.MustParseAddr("2409:8a00::1")
	siteIPv6 := netip.MustParseAddr("fd00::1")
	linkIPv6 := netip.MustParseAddr("fe80::1")

	dualStack := localFamilyPolicyForTest("provisioned", "provisioned")
	noIPv6 := localFamilyPolicyForTest("provisioned", "unavailable")
	noIPv4 := localFamilyPolicyForTest("unavailable", "provisioned")

	cases := []struct {
		name        string
		policy      admissionPolicy
		destination netip.Addr
		verdict     admissionVerdict
	}{
		{
			// The strategy term is asked FIRST, and this row is why: the address is also one the machine
			// might not reach, but what the operator can act on is their own policy.
			name:        "strategy excludes a family the machine has",
			policy:      newAdmissionPolicyForTest(C.DomainStrategyIPv4Only, dualStack),
			destination: globalIPv6,
			verdict:     admissionStrategyExcluded,
		},
		{
			name:        "strategy excludes the other family",
			policy:      newAdmissionPolicyForTest(C.DomainStrategyIPv6Only, dualStack),
			destination: globalIPv4,
			verdict:     admissionStrategyExcluded,
		},
		{
			name:        "the machine has no IPv6 addressing",
			policy:      newAdmissionPolicyForTest(C.DomainStrategyAsIS, noIPv6),
			destination: globalIPv6,
			verdict:     admissionNoLocalFamily,
		},
		{
			name:        "the machine has no IPv4 addressing",
			policy:      newAdmissionPolicyForTest(C.DomainStrategyAsIS, noIPv4),
			destination: globalIPv4,
			verdict:     admissionNoLocalFamily,
		},
		{
			name:        "a preference admits both families",
			policy:      newAdmissionPolicyForTest(C.DomainStrategyPreferIPv6, noIPv6),
			destination: globalIPv4,
			verdict:     admissionAllowed,
		},
		{
			// The distinction the egress term exists for: IPv6 EXISTS on this link, so a link-local
			// destination is reachable, and no global IPv6 destination is.
			name:        "IPv6 addressing with no global route",
			policy:      newAdmissionPolicyForTest(C.DomainStrategyAsIS, egressRefusingPolicy()),
			destination: globalIPv6,
			verdict:     admissionNoGlobalEgress,
		},
		{
			name:        "the same machine still reaches the link",
			policy:      newAdmissionPolicyForTest(C.DomainStrategyAsIS, egressRefusingPolicy()),
			destination: linkIPv6,
			verdict:     admissionAllowed,
		},
		{
			name:        "and still reaches the site",
			policy:      newAdmissionPolicyForTest(C.DomainStrategyAsIS, egressRefusingPolicy()),
			destination: siteIPv6,
			verdict:     admissionAllowed,
		},
		{
			name:        "an unowned capture refuses nothing",
			policy:      newAdmissionPolicyForTest(C.DomainStrategyAsIS, localFamilyPolicy{}),
			destination: globalIPv6,
			verdict:     admissionAllowed,
		},
		{
			name:        "an invalid address has no verdict",
			policy:      newAdmissionPolicyForTest(C.DomainStrategyIPv4Only, noIPv6),
			destination: netip.Addr{},
			verdict:     admissionAllowed,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.verdict, testCase.policy.verdict(testCase.destination))
			require.Equal(t, testCase.verdict == admissionAllowed, testCase.policy.admits(testCase.destination),
				"the verdict and the gate must agree, or a refusal names a rule that did not fire")
			if testCase.verdict == admissionAllowed {
				require.NoError(t, testCase.policy.refusalError(testCase.destination))
				return
			}
			refusal := testCase.policy.refusalError(testCase.destination)
			require.Error(t, refusal, "a verdict other than allowed must render an error")
			switch testCase.verdict {
			case admissionStrategyExcluded:
				require.Contains(t, refusal.Error(), "excluded by strategy",
					"a configuration decision must read as one")
				require.NotContains(t, refusal.Error(), "local network",
					"and must not be dressed up as a property of the machine")
			case admissionNoLocalFamily:
				require.Contains(t, refusal.Error(), "local network")
				require.NotContains(t, refusal.Error(), "excluded by strategy")
			case admissionNoGlobalEgress:
				require.Contains(t, refusal.Error(), "public ipv6",
					"the route case has its own remedy and its own message")
			}
		})
	}
}

// TestAdmitCandidatesReportsTheThreeOutcomes pins the list-level answer.
//
// The three outcomes are what a caller needs to explain the flow: a list emptied by the OPERATOR's policy
// is a different fact from one emptied by the MACHINE, and both are different from "there was nothing to
// offer". Two call sites used to flatten all three into "no dial candidates", which names neither the
// policy nor the destination.
func TestAdmitCandidatesReportsTheThreeOutcomes(t *testing.T) {
	globalIPv4 := netip.MustParseAddr("93.184.216.34")
	globalIPv6 := netip.MustParseAddr("2409:8a00::1")
	dualStack := localFamilyPolicyForTest("provisioned", "provisioned")
	noIPv6 := localFamilyPolicyForTest("provisioned", "unavailable")

	t.Run("nothing removed is the same slice", func(t *testing.T) {
		policy := newAdmissionPolicyForTest(C.DomainStrategyAsIS, dualStack)
		input := []netip.Addr{globalIPv4, globalIPv6}
		admitted, refused, strategyExcludedAll := policy.admitCandidates(input)
		require.False(t, strategyExcludedAll)
		require.Equal(t, familyInvalid, refused.family)
		require.Equal(t, input, admitted)
		require.Equal(t, &input[0], &admitted[0],
			"the ordinary dual-stack flow must not copy: this slice may be a resolver's cache entry")
	})

	t.Run("the strategy empties the list", func(t *testing.T) {
		policy := newAdmissionPolicyForTest(C.DomainStrategyIPv4Only, dualStack)
		admitted, refused, strategyExcludedAll := policy.admitCandidates([]netip.Addr{globalIPv6})
		require.Nil(t, admitted)
		require.True(t, strategyExcludedAll, "the POLICY emptied it, and that is what must be reported")
		require.Equal(t, familyInvalid, refused.family,
			"no local-family refusal fired: reporting one would name the wrong rule")
	})

	t.Run("the machine empties the list", func(t *testing.T) {
		policy := newAdmissionPolicyForTest(C.DomainStrategyAsIS, noIPv6)
		admitted, refused, strategyExcludedAll := policy.admitCandidates([]netip.Addr{globalIPv6})
		require.Nil(t, admitted)
		require.False(t, strategyExcludedAll, "the strategy admitted it; the machine could not dial it")
		require.Equal(t, familyIPv6, refused.family,
			"the refusal must carry the family so the explanation can name it")
	})

	t.Run("a mixed list keeps what the machine can dial", func(t *testing.T) {
		policy := newAdmissionPolicyForTest(C.DomainStrategyAsIS, noIPv6)
		admitted, _, strategyExcludedAll := policy.admitCandidates([]netip.Addr{globalIPv6, globalIPv4})
		require.False(t, strategyExcludedAll)
		require.Equal(t, []netip.Addr{globalIPv4}, admitted,
			"the usable family survives and the resolver's order within it is preserved")
	})

	t.Run("an empty input is not an excluded one", func(t *testing.T) {
		policy := newAdmissionPolicyForTest(C.DomainStrategyIPv4Only, dualStack)
		admitted, refused, strategyExcludedAll := policy.admitCandidates(nil)
		require.Nil(t, admitted)
		require.False(t, strategyExcludedAll, "nothing was offered, which is not a strategy exclusion")
		require.Equal(t, familyInvalid, refused.family)
	})
}

// TestAdmissionPolicyGenerationIsTheCapabilityOwnership pins §11 for the value itself.
//
// The policy's generation is the capability's own ownership epoch. A second reading - the manager's
// counter, the environment fingerprint - would be a second source, and the scheduler's family health is
// scoped by exactly this number, so a divergence here is what files one network's verdict under another.
func TestAdmissionPolicyGenerationIsTheCapabilityOwnership(t *testing.T) {
	const epoch = 0x5EED
	owned := localFamilyPolicy{capability: netcap.FamilyCapability{IPv4: netcap.StateProvisioned, IPv6: netcap.StateProvisioned}}
	owned.ownership.epoch = epoch
	owned.ownership.startedStable = true
	owned.ownership.predicate = func() bool { return true }
	policy := newAdmissionPolicyForTest(C.DomainStrategyAsIS, owned)
	require.EqualValues(t, epoch, policy.generation())
}

// TestAdmissionFastPathNeverDisagreesWithTheVerdict is the invariant the fast path and every caller rest on.
//
// Enumerated over the WHOLE space rather than sampled: every family state, every egress state, both
// ownership outcomes, every strategy, every scope class of destination, and a zero policy. A table of
// examples cannot catch a composition that is wrong for one state it does not list, and this is exactly
// the kind of rule where the wrong case is the rare one.
func TestAdmissionFastPathNeverDisagreesWithTheVerdict(t *testing.T) {
	destinations := falsificationDestinations()
	strategies := admissionStrategySpace()
	checked := 0
	for _, localFamily := range policySpace() {
		for _, strategy := range strategies {
			policy := newAdmissionPolicyForTest(strategy, localFamily)
			fastPath := policy.restrictsNothing()
			for _, destination := range destinations {
				checked++
				allowed := policy.admits(destination)
				if fastPath && !allowed {
					t.Fatalf("restrictsNothing() is true but admits(%v) is false: the fast path would "+
						"skip a filter that removes this destination.\nstrategy=%v capability=%+v owned=%v",
						destination, strategyName(strategy), localFamily.capability, localFamily.ownership.owns())
				}
				if got := policy.verdict(destination); (got == admissionAllowed) != allowed {
					t.Fatalf("verdict(%v)=%v but admits=%v: the gate and the explanation came from "+
						"different places.\nstrategy=%v capability=%+v owned=%v",
						destination, got, allowed, strategyName(strategy), localFamily.capability,
						localFamily.ownership.owns())
				}
			}
		}
	}
	require.NotZero(t, checked, "no (policy, destination) pair was examined")
	t.Logf("examined %d (policy, destination) pairs", checked)
}
