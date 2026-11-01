package dialer

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/common/netcap"
)

// These tests try to FALSIFY the family gate's own self-consistency contract rather than to confirm
// a handful of hand-picked cases.
//
// The gate is composed of several terms - scope, family existence, global IPv6 egress, and network
// ownership - and each is implemented separately:
//
//	localFamilyPolicy.restrictsNothing   the fast path that skips candidate-list work entirely
//	localFamilyPolicy.hardUndialable     the per-destination decision
//	localFamilyPolicy.hardUndialableFamily  the same question for a caller holding only a family
//	netcap.FamilyCapability.HardUndialable  the family + egress composition
//
// The contract is that they agree. `restrictsNothing` documents it in as many words: "An unowned
// policy restricts nothing, because that is what the gate answers for it - the two must never
// disagree, or the fast path would skip a filter that would have removed something." A disagreement
// is a silent correctness bug in the direction that matters (the fast path walking past a filter),
// and it is exactly the kind of thing a table of examples misses.
//
// So these enumerate the whole SPACE rather than a sample: every family state, every egress state,
// both ownership outcomes, every scope class of destination, and a zero policy.

// policySpace is every combination of the two family states, the egress state, and ownership.
func policySpace() []localFamilyPolicy {
	states := []netcap.FamilyState{
		netcap.StateUnknown,
		netcap.StateUnavailable,
		netcap.StateProvisioned,
		netcap.StateValidated,
		netcap.StateDegraded,
	}
	ownerships := []struct {
		name  string
		owned bool
	}{
		{"owned", true},
		{"unowned", false},
	}
	var policies []localFamilyPolicy
	for _, ipv4 := range states {
		for _, ipv6 := range states {
			for _, egress := range states {
				for _, ownership := range ownerships {
					policy := localFamilyPolicy{
						capability: netcap.FamilyCapability{
							IPv4:             ipv4,
							IPv6:             ipv6,
							GlobalIPv6Egress: egress,
						},
					}
					if !ownership.owned {
						policy.ownership.predicate = func() bool { return false }
					}
					policies = append(policies, policy)
				}
			}
		}
	}
	// The zero policy, which is what a manager that cannot report a capability yields.
	policies = append(policies, localFamilyPolicy{})
	return policies
}

// falsificationDestinations is one address of every scope class the gate distinguishes, plus an
// invalid one (the "no destination" case) and IPv4-mapped forms.
func falsificationDestinations() []netip.Addr {
	return []netip.Addr{
		{},
		netip.MustParseAddr("127.0.0.1"),
		netip.MustParseAddr("::1"),
		netip.MustParseAddr("169.254.1.1"),
		netip.MustParseAddr("fe80::1"),
		netip.MustParseAddr("10.0.0.1"),
		netip.MustParseAddr("192.168.1.1"),
		netip.MustParseAddr("fd00::1"),
		netip.MustParseAddr("93.184.216.34"),
		netip.MustParseAddr("2001:db8::1"),
		netip.MustParseAddr("::ffff:93.184.216.34"),
		netip.MustParseAddr("::ffff:192.168.1.1"),
		netip.MustParseAddr("0.0.0.0"),
		netip.MustParseAddr("::"),
	}
}

// TestRestrictsNothingNeverDisagreesWithTheGate is the invariant the fast path rests on.
//
// If a policy reports that it restricts nothing, then hardUndialable must answer "dialable" for
// EVERY destination. The converse is not asserted, and deliberately: a policy may restrict
// something and still allow a particular destination (a local-scope address on a policy that
// refuses global IPv6 is the ordinary case).
func TestRestrictsNothingNeverDisagreesWithTheGate(t *testing.T) {
	destinations := falsificationDestinations()
	checked := 0
	for _, policy := range policySpace() {
		if !policy.restrictsNothing() {
			continue
		}
		for _, destination := range destinations {
			checked++
			if policy.hardUndialable(destination) {
				t.Fatalf("restrictsNothing() is true but hardUndialable(%v) refused it: the fast path "+
					"would skip a filter that removes this destination.\ncapability=%+v owned=%v",
					destination, policy.capability, policy.ownership.owns())
			}
		}
	}
	if checked == 0 {
		t.Fatal("no (policy, destination) pair was examined; this test would pass vacuously")
	}
	t.Logf("examined %d (policy, destination) pairs through the fast path", checked)
}

// TestOwnershipIsTheOnlyThingThatDisablesTheGate isolates the ownership term.
//
// An unowned policy must refuse NOTHING, for any capability - that is the documented degradation,
// and it is the direction that keeps a hand-over from refusing a family. The test also pins that
// the SAME capability DOES refuse when owned, so the assertion above cannot pass by the gate being
// inert for every policy.
func TestOwnershipIsTheOnlyThingThatDisablesTheGate(t *testing.T) {
	unavailable := netcap.FamilyCapability{
		IPv4:             netcap.StateUnavailable,
		IPv6:             netcap.StateUnavailable,
		GlobalIPv6Egress: netcap.StateUnavailable,
	}
	globalIPv4 := netip.MustParseAddr("93.184.216.34")
	globalIPv6 := netip.MustParseAddr("2001:db8::1")

	owned := newLocalFamilyPolicy(unavailable)
	if !owned.hardUndialable(globalIPv4) || !owned.hardUndialable(globalIPv6) {
		t.Fatal("precondition: an owned policy with both families unavailable must refuse both; " +
			"without this the unowned assertion below is vacuous")
	}

	unowned := localFamilyPolicy{
		capability: unavailable,
		ownership:  localDialOwnership{predicate: func() bool { return false }},
	}
	if unowned.hardUndialable(globalIPv4) || unowned.hardUndialable(globalIPv6) {
		t.Fatal("an unowned policy must refuse nothing: a verdict from a network this attempt is " +
			"not on describes no network at all, and refusing on it breaks a hand-over")
	}
	if !unowned.restrictsNothing() {
		t.Fatal("and it must therefore report that it restricts nothing")
	}
}

// TestTheFamilyOnlyQuestionAgreesWithTheDestinationQuestion pins the pair used by the plan-level
// fast path against the per-destination gate.
//
// hardUndialableFamily exists so a caller can ask "could this policy refuse anything at all of this
// family" before walking a list. Its doc says it "deliberately answers the family question only"
// because the egress term is applied per candidate - so the two must agree on every GLOBAL
// destination's family component, and any disagreement must be attributable to the egress term and
// to nothing else.
func TestTheFamilyOnlyQuestionAgreesWithTheDestinationQuestion(t *testing.T) {
	checked := 0
	for _, policy := range policySpace() {
		globalIPv4 := netip.MustParseAddr("93.184.216.34")
		globalIPv6 := netip.MustParseAddr("2001:db8::1")

		if policy.hardUndialableFamily(familyIPv4) && !policy.hardUndialable(globalIPv4) {
			t.Fatalf("the family question refuses IPv4 but the destination gate allows a global IPv4 "+
				"destination; one of the two is wrong.\ncapability=%+v", policy.capability)
		}
		// The IPv6 direction is allowed to differ in exactly one way: the family is fine but the
		// global egress is provably absent. Anything else is a disagreement.
		familyRefuses := policy.hardUndialableFamily(familyIPv6)
		destinationRefuses := policy.hardUndialable(globalIPv6)
		if familyRefuses && !destinationRefuses {
			t.Fatalf("the family question refuses IPv6 but the destination gate allows a global IPv6 "+
				"destination.\ncapability=%+v", policy.capability)
		}
		if !familyRefuses && destinationRefuses {
			if policy.capability.GlobalIPv6Egress != netcap.StateUnavailable {
				t.Fatalf("the destination gate refused a global IPv6 destination while the family term "+
					"allowed it, and the egress term is not the reason (%v).\ncapability=%+v",
					policy.capability.GlobalIPv6Egress, policy.capability)
			}
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no policy was examined; this test would pass vacuously")
	}
	t.Logf("examined %d policies across both families", checked)
}

// TestTheEgressTermOnlyEverTouchesGlobalIPv6 is the "do not enlarge the public failure into the LAN"
// rule, attacked from the other side.
//
// A global-IPv6-egress verdict must never change the answer for IPv4, for a link-local address, or
// for a unique-local address - those are reached without a global route. Only a GLOBAL IPv6
// destination may be refused because of it.
func TestTheEgressTermOnlyEverTouchesGlobalIPv6(t *testing.T) {
	// The family is fully provisioned on both sides, so the ONLY term that can change an answer is
	// the egress one.
	provisioned := netcap.FamilyCapability{
		IPv4: netcap.StateProvisioned,
		IPv6: netcap.StateProvisioned,
	}
	withEgress := provisioned
	withEgress.GlobalIPv6Egress = netcap.StateProvisioned
	withoutEgress := provisioned
	withoutEgress.GlobalIPv6Egress = netcap.StateUnavailable

	mustNotChange := []netip.Addr{
		netip.MustParseAddr("93.184.216.34"), // global IPv4
		netip.MustParseAddr("10.0.0.1"),      // private IPv4
		netip.MustParseAddr("127.0.0.1"),     // loopback
		netip.MustParseAddr("::1"),           // loopback v6
		netip.MustParseAddr("fe80::1"),       // link-local
		netip.MustParseAddr("fd00::1"),       // unique-local
		netip.MustParseAddr("169.254.1.1"),   // link-local v4
	}
	for _, destination := range mustNotChange {
		a := newLocalFamilyPolicy(withEgress).hardUndialable(destination)
		b := newLocalFamilyPolicy(withoutEgress).hardUndialable(destination)
		if a != b {
			t.Fatalf("the global-IPv6-egress term changed the verdict for %v (%v -> %v). A missing "+
				"public IPv6 route must not refuse a destination that is not reached over the public "+
				"IPv6 Internet", destination, a, b)
		}
	}

	// And the one destination it MUST change: a global IPv6 address.
	global := netip.MustParseAddr("2001:db8::1")
	if newLocalFamilyPolicy(withEgress).hardUndialable(global) {
		t.Fatal("precondition: with a global route, a global IPv6 destination must be dialable")
	}
	if !newLocalFamilyPolicy(withoutEgress).hardUndialable(global) {
		t.Fatal("with no global IPv6 route, a global IPv6 destination must be refused - that is the " +
			"entire reason the egress term exists")
	}
}
