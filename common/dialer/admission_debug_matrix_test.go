package dialer

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
)

// Phase 03's cross-stage matrix: every stage must give the SAME answer for the same policy.
//
// # Why a matrix rather than more examples
//
// The stages were each correct on their own before this round, and each is still correct on its own now.
// The failure mode this file attacks is DISAGREEMENT: the original admitted by one term while the list
// filter applies two, or the late-candidate guard applying only the strategy while the plan applies both.
// Such a divergence cannot be found by testing a stage; it is found by asking every stage about the same
// input and comparing.
//
// The space is enumerated rather than sampled - three strategies x four capability shapes x every scope
// class of address - because the divergence, if it exists, exists for the combination nobody listed.
//
// # What has to agree
//
//	admits(addr)                      the single predicate
//	strategyAllows(addr)              a strict subset of admits, and a superset of what a late candidate sees
//	admitCandidates(...)              keeps exactly the addresses admits accepts
//	lateAdmission()(addr)             never admits what the strategy excludes
//	verdict(addr) == allowed          exactly when admits(addr)
//	refusalError(addr) != nil         exactly when admits(addr) is false

// admissionMatrixCapabilities is every capability shape the matrix distinguishes.
func admissionMatrixCapabilities() []struct {
	name   string
	ipv4   netcap.FamilyState
	ipv6   netcap.FamilyState
	egress netcap.FamilyState
} {
	return []struct {
		name   string
		ipv4   netcap.FamilyState
		ipv6   netcap.FamilyState
		egress netcap.FamilyState
	}{
		{"dual-stack", netcap.StateProvisioned, netcap.StateProvisioned, netcap.StateUnknown},
		{"no-ipv6", netcap.StateProvisioned, netcap.StateUnavailable, netcap.StateUnknown},
		{"no-ipv4", netcap.StateUnavailable, netcap.StateProvisioned, netcap.StateUnknown},
		{"ipv6-without-egress", netcap.StateProvisioned, netcap.StateProvisioned, netcap.StateUnavailable},
	}
}

// admissionMatrixAddresses is one address per scope class, plus the invalid one.
func admissionMatrixAddresses() []netip.Addr {
	return []netip.Addr{
		{},
		netip.MustParseAddr("127.0.0.1"),
		netip.MustParseAddr("::1"),
		netip.MustParseAddr("169.254.1.1"),
		netip.MustParseAddr("fe80::1"),
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("fd00::1"),
		netip.MustParseAddr("93.184.216.34"),
		netip.MustParseAddr("2001:db8::1"),
		netip.MustParseAddr("::ffff:93.184.216.34"),
	}
}

// TestTheAdmissionMatrixIsOnePolicyAcrossEveryStage is the cross-stage invariant.
func TestTheAdmissionMatrixIsOnePolicyAcrossEveryStage(t *testing.T) {
	addresses := admissionMatrixAddresses()
	checked := 0
	for _, strategy := range admissionStrategySpace() {
		for _, capability := range admissionMatrixCapabilities() {
			policy := newAdmissionPolicyForTest(strategy, newLocalFamilyPolicy(netcap.FamilyCapability{
				IPv4:             capability.ipv4,
				IPv6:             capability.ipv6,
				GlobalIPv6Egress: capability.egress,
			}))
			lateGuard := policy.lateAdmission()

			for _, address := range addresses {
				checked++
				admits := policy.admits(address)
				verdict := policy.verdict(address)
				refusal := policy.refusalError(address)
				strategyAllows := policy.strategyAllows(address)

				require.Equal(t, admits, verdict == admissionAllowed,
					"verdict and admits disagree for %v under %s/%s",
					address, strategyName(strategy), capability.name)
				require.Equal(t, admits, refusal == nil,
					"refusalError and admits disagree for %v under %s/%s",
					address, strategyName(strategy), capability.name)
				require.True(t, admits == false || strategyAllows,
					"admits allowed %v under %s/%s although the operator's policy excludes it",
					address, strategyName(strategy), capability.name)

				// The late-candidate guard is the STRATEGY half by design: the scheduler applies the
				// capability half itself. Both directions matter and they fail differently.
				//
				// Over-refusal loses a candidate the operator admitted - the fail-closed direction that
				// costs a working path. Under-refusal puts a forbidden family into a race - the fail-open
				// direction that makes the policy a suggestion. The second is the one a mutation produces
				// when it returns nil "because the guard is only an optimisation".
				if lateGuard != nil && !lateGuard(address) {
					require.False(t, strategyAllows,
						"the late guard refused %v although the strategy allows it under %s/%s",
						address, strategyName(strategy), capability.name)
				}
				if !strategyAllows && (strategy == C.DomainStrategyIPv4Only || strategy == C.DomainStrategyIPv6Only) {
					require.NotNil(t, lateGuard,
						"a strict strategy armed no late-candidate guard under %s/%s: the guard is the only "+
							"thing standing between a late arrival and the planner, which a late candidate "+
							"never passes", strategyName(strategy), capability.name)
					require.False(t, lateGuard(address),
						"the late guard admitted %v although the operator's policy excludes it under %s/%s",
						address, strategyName(strategy), capability.name)
				}
			}

			// The list filter keeps exactly what admits accepts, in order, for a mixed list.
			admitted, refused, strategyExcludedAll := policy.admitCandidates(addresses)
			var expected []netip.Addr
			for _, address := range addresses {
				if policy.admits(address) {
					expected = append(expected, address)
				}
			}
			require.Equal(t, expected, admitted,
				"admitCandidates disagrees with admits under %s/%s",
				strategyName(strategy), capability.name)

			// And the three list-level outcomes are mutually consistent.
			if len(admitted) == 0 && len(addresses) > 0 {
				require.True(t, refused.refused() || strategyExcludedAll,
					"the list was emptied under %s/%s but neither term claims it: the caller would report "+
						"a policy decision that did not happen",
					strategyName(strategy), capability.name)
			}
			require.False(t, strategyExcludedAll && refused.refused(),
				"both terms claim to have emptied the list under %s/%s",
				strategyName(strategy), capability.name)
		}
	}
	require.NotZero(t, checked, "no (policy, address) pair was examined")
	t.Logf("examined %d (policy, address) pairs across 6 stages", checked)
}
