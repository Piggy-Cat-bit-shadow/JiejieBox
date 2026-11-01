package dialer

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
)

// Benchmarks for the per-flow admission policy, so §23's performance boundary has a number rather than an
// intention.
//
// # What is measured, and why these three cases
//
// The ordinary connection is the dual-stack one that the policy refuses nothing on, and it must pay
// nothing for the framework: admits returns true without touching the capability, and admitCandidates
// returns the resolver's OWN slice. The refusing case is the one where the work is justified - an
// allocation is acceptable there because an attempt is being saved. The zero policy is what a dialer
// without a capability source gets, and it must be indistinguishable from no policy at all.
//
// These are deliberately the same shapes as BenchmarkLocalFamilyGate in family_capability_bench_test.go,
// so the two sets are comparable: the policy adds a strategy term to that gate, and the delta is what the
// strategy term costs.

func dualStackAdmission() admissionPolicy {
	return newAdmissionPolicyForTest(C.DomainStrategyAsIS, newLocalFamilyPolicy(netcap.FamilyCapability{
		IPv4: netcap.StateProvisioned,
		IPv6: netcap.StateProvisioned,
	}))
}

func BenchmarkAdmissionPolicyAdmits(b *testing.B) {
	globalIPv4 := netip.MustParseAddr("93.184.216.34")
	globalIPv6 := netip.MustParseAddr("2001:db8::1")
	cases := []struct {
		name   string
		policy admissionPolicy
		target netip.Addr
	}{
		{"dual-stack/allowed", dualStackAdmission(), globalIPv6},
		{"dual-stack/ipv4-allowed", dualStackAdmission(), globalIPv4},
		{
			"ipv4-only/refused",
			newAdmissionPolicyForTest(C.DomainStrategyIPv4Only, newLocalFamilyPolicy(netcap.FamilyCapability{
				IPv4: netcap.StateProvisioned,
				IPv6: netcap.StateProvisioned,
			})),
			globalIPv6,
		},
		{"zero/allowed", admissionPolicy{}, globalIPv6},
	}
	for _, testCase := range cases {
		b.Run(testCase.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if testCase.policy.admits(testCase.target) {
					_ = 1
				}
			}
		})
	}
}

func BenchmarkAdmissionPolicyVerdict(b *testing.B) {
	globalIPv6 := netip.MustParseAddr("2001:db8::1")
	policy := newAdmissionPolicyForTest(C.DomainStrategyIPv4Only, newLocalFamilyPolicy(netcap.FamilyCapability{
		IPv4: netcap.StateProvisioned,
		IPv6: netcap.StateProvisioned,
	}))
	b.ReportAllocs()
	for range b.N {
		if policy.verdict(globalIPv6) != admissionAllowed {
			_ = 1
		}
	}
}

func BenchmarkAdmissionPolicyAdmitCandidates(b *testing.B) {
	globalIPv4 := netip.MustParseAddr("93.184.216.34")
	globalIPv6 := netip.MustParseAddr("2001:db8::1")
	both := []netip.Addr{globalIPv4, globalIPv6}
	ipv6Only := []netip.Addr{globalIPv6}

	b.Run("nothing removed", func(b *testing.B) {
		policy := dualStackAdmission()
		b.ReportAllocs()
		for range b.N {
			admitted, _, _ := policy.admitCandidates(both)
			if len(admitted) != 2 {
				b.Fatal("the policy removed a candidate it must admit")
			}
		}
	})

	b.Run("strategy removes one", func(b *testing.B) {
		policy := newAdmissionPolicyForTest(C.DomainStrategyIPv4Only, newLocalFamilyPolicy(netcap.FamilyCapability{
			IPv4: netcap.StateProvisioned,
			IPv6: netcap.StateProvisioned,
		}))
		b.ReportAllocs()
		for range b.N {
			admitted, _, _ := policy.admitCandidates(both)
			if len(admitted) != 1 {
				b.Fatal("the policy admitted a family the operator forbade")
			}
		}
	})

	b.Run("capability empties", func(b *testing.B) {
		policy := newAdmissionPolicyForTest(C.DomainStrategyAsIS, newLocalFamilyPolicy(netcap.FamilyCapability{
			IPv4: netcap.StateProvisioned,
			IPv6: netcap.StateUnavailable,
		}))
		b.ReportAllocs()
		for range b.N {
			admitted, refused, _ := policy.admitCandidates(ipv6Only)
			if admitted != nil || !refused.refused() {
				b.Fatal("the policy admitted a family the machine cannot dial")
			}
		}
	})
}
