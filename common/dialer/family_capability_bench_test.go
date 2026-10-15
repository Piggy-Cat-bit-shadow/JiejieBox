package dialer

import (
	"net/netip"
	"testing"
	"time"
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

func dualCopy(plan candidatePlan) candidatePlan {
	plan.candidates = append([]dualStackCandidate(nil), plan.candidates...)
	return plan
}
