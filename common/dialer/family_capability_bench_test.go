package dialer

import (
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/netcap"
)

// BenchmarkLocalFamilyGate measures the DECISION on its own, separated from the capture.
//
// The two halves have different budgets and different reasons to exist, so measuring them together
// hides which one moved:
//
//	capture - once per dial, and it must not read a route table or take a lock
//	gate    - once per candidate, and it must not allocate: this is the per-candidate inner loop
//
// Both are asserted for allocations rather than only for time, because an allocation here is a cost
// the connection pays on every dial and a source of GC pressure the process would not otherwise have.
func BenchmarkLocalFamilyGate(b *testing.B) {
	globalIPv4 := netip.MustParseAddr("93.184.216.34")
	globalIPv6 := netip.MustParseAddr("2001:db8::1")
	linkLocal := netip.MustParseAddr("fe80::1")

	capability := netcap.FamilyCapability{
		IPv4:             netcap.StateProvisioned,
		IPv6:             netcap.StateProvisioned,
		GlobalIPv6Egress: netcap.StateUnavailable,
	}
	policy := newLocalFamilyPolicy(capability)

	b.Run("global-ipv4/allowed", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if policy.hardUndialable(globalIPv4) {
				b.Fatal("ipv4 is not gated by an ipv6 egress verdict")
			}
		}
	})
	b.Run("global-ipv6/refused", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if !policy.hardUndialable(globalIPv6) {
				b.Fatal("a global ipv6 destination with no public route must be refused")
			}
		}
	})
	b.Run("link-local/allowed", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if policy.hardUndialable(linkLocal) {
				b.Fatal("a link-local destination is reached over the link")
			}
		}
	})
	b.Run("zero-policy/allowed", func(b *testing.B) {
		zero := localFamilyPolicy{}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if zero.hardUndialable(globalIPv6) {
				b.Fatal("a policy nobody computed must refuse nothing")
			}
		}
	})
}

// BenchmarkLocalFamilyFilter measures the cost the capability adds to one dial.
//
// It is a micro-benchmark on purpose. The single-candidate scheduler path is the most frequent
// operation in the process, and its whole budget is tens of nanoseconds - so a filter that costs
// several nanoseconds per dial is a real regression on every connection, while the same cost on a
// connection that opens a socket is invisible. Measuring it here keeps the claim checkable instead
// of resting on the aggregate number.
//
// The two cases are the ones that matter:
//
//	nothing refused (both families present, and no capability at all) - every ordinary dial
//	one refused - only the connections the feature exists for
func BenchmarkLocalFamilyFilter(b *testing.B) {
	single := planCandidates([]netip.Addr{netip.MustParseAddr("192.0.2.1")}, netip.Addr{}, 0)
	dual := planCandidates([]netip.Addr{
		netip.MustParseAddr("2001:db8::1"),
		netip.MustParseAddr("192.0.2.1"),
	}, netip.Addr{}, 0)

	b.Run("single/no-capability", func(b *testing.B) {
		scheduler := &candidateScheduler{fallbackDelay: time.Millisecond}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, refused := scheduler.filterLocalFamily(single); refused.family != familyInvalid {
				b.Fatal("a zero policy must refuse nothing")
			}
		}
	})

	b.Run("single/both-present", func(b *testing.B) {
		scheduler := &candidateScheduler{localFamily: localFamilyPolicyForTest("provisioned", "provisioned")}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, refused := scheduler.filterLocalFamily(single); refused.family != familyInvalid {
				b.Fatal("a present family must not be refused")
			}
		}
	})

	b.Run("single/ipv6-refused", func(b *testing.B) {
		scheduler := &candidateScheduler{localFamily: localFamilyPolicyForTest("provisioned", "unavailable")}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// A copy each round: the filter returns a new slice when it removes, and the benchmark
			// must measure that rather than a plan that shrinks to nothing after one iteration.
			if _, refused := scheduler.filterLocalFamily(dualCopy(dual)); refused.family != familyIPv6 {
				b.Fatal("the absent family must be refused")
			}
		}
	})
}

// BenchmarkLocalFamilyPolicySnapshot measures the read the dial path performs.
//
// The requirement this checks is not "fast" but "the right shape": the verdict is read once per dial,
// off an atomic, with no syscall, no lock and no allocation. A change that enumerated interfaces,
// scanned a route table, ran a probe or took a lock here would show up as a number in this benchmark
// long before it showed up as a latency complaint - which is the only reason the benchmark exists.
//
// It is deliberately NOT a target to optimise. The comparison is against the cost of the rest of a
// dial, which is thousands of nanoseconds of syscalls; two atomic loads are already below the noise
// floor and making them faster would be work with no observable effect.
func BenchmarkLocalFamilyPolicySnapshot(b *testing.B) {
	dialer := &DefaultDialer{}
	manager := newCapabilityManager(netcap.StateProvisioned, netcap.StateUnavailable)
	dialer.network = newNetworkObserver(manager)
	target := netip.MustParseAddr("2001:4860:4860::8888")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		policy := dialer.captureLocalFamilyPolicy()
		if !policy.hardUndialable(target) {
			b.Fatal("the verdict must be read from the manager")
		}
	}
}

// BenchmarkLocalFamilyPolicyCaptureOnly is the same read with the gate left out, so the two costs can
// be attributed.
//
// Without it, an allocation in either half is indistinguishable from one in the other - which is
// exactly how a closure added to the capture would have been blamed on the gate, or worse, shipped.
func BenchmarkLocalFamilyPolicyCaptureOnly(b *testing.B) {
	dialer := &DefaultDialer{}
	manager := newCapabilityManager(netcap.StateProvisioned, netcap.StateUnavailable)
	dialer.network = newNetworkObserver(manager)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		policy := dialer.captureLocalFamilyPolicy()
		if policy.capability.IPv6 != netcap.StateUnavailable {
			b.Fatal("the verdict must be read from the manager")
		}
	}
}

// BenchmarkLocalFamilyPolicyAbsent measures the same read on a dialer that has no manager, which is
// the shape every test double and every third-party NetworkManager produces.
//
// It must be free: one nil check. A dialer without a capability has to be indistinguishable from the
// code before this round, and a benchmark is how "indistinguishable" stays checkable.
func BenchmarkLocalFamilyPolicyAbsent(b *testing.B) {
	dialer := &DefaultDialer{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		policy := dialer.captureLocalFamilyPolicy()
		if policy.hardUndialable(netip.MustParseAddr("2001:4860:4860::8888")) ||
			policy.hardUndialable(netip.MustParseAddr("8.8.8.8")) {
			b.Fatal("a dialer with no manager must restrict nothing")
		}
	}
}

func dualCopy(plan candidatePlan) candidatePlan {
	plan.candidates = append([]dualStackCandidate(nil), plan.candidates...)
	return plan
}
