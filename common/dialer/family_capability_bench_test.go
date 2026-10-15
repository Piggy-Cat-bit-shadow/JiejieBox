package dialer

import (
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/netcap"
)

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
			if _, refused := scheduler.filterLocalFamily(single); refused != familyInvalid {
				b.Fatal("a zero policy must refuse nothing")
			}
		}
	})

	b.Run("single/both-present", func(b *testing.B) {
		scheduler := &candidateScheduler{localFamily: localFamilyPolicyForTest("provisioned", "provisioned")}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, refused := scheduler.filterLocalFamily(single); refused != familyInvalid {
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
			if _, refused := scheduler.filterLocalFamily(dualCopy(dual)); refused != familyIPv6 {
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
	dialer.familySnapshot = manager.NetworkFamilyCapabilitySnapshot

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		policy := dialer.captureLocalFamilyPolicy()
		if !policy.excluded(familyIPv6) {
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
		if policy.excluded(familyIPv6) || policy.excluded(familyIPv4) {
			b.Fatal("a dialer with no manager must restrict nothing")
		}
	}
}

func dualCopy(plan candidatePlan) candidatePlan {
	plan.candidates = append([]dualStackCandidate(nil), plan.candidates...)
	return plan
}
