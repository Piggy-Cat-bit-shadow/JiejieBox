package dialer

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Layer 8 of the debug round: literal recovery, which the brief calls a high-risk boundary.
//
// # Why it is the riskiest path in this round
//
// It is the only path where a destination the APPLICATION chose is deliberately set aside in favour of
// addresses derived from a name the PEER influenced. Three separate policies meet here and each one
// can silently widen another:
//
//	the strict family strategy   a configuration decision
//	the local family verdict     a property of the machine
//	the original-promotion rule  the application's own address leads its family
//
// The failure modes the brief names are all versions of "one of them quietly overrode another", so the
// cases below attack each override explicitly, plus the structural ones - recursion and ordering -
// that a family filter could disturb by accident.

// recoveryHarness drives dialLiteralWithRecovery through the production resolve dialer, with recovery
// answered by a scripted router and the socket answered by a fake transport.
type recoveryHarness struct {
	leaf      *capabilityLeaf
	resolver  *resolveDialer
	router    *scriptedFamilyRouter
	original  M.Socksaddr
	recovered []netip.Addr
}

// newRecoveryHarness builds a resolve dialer whose recovery router answers with the given addresses
// for a sniffed domain.
//
// # Why the AAAA half is delayed
//
// For DETERMINISM, not to work around a defect. Recovery streams, so with both families answering
// concurrently the fixture would decide which one this test observes. Delaying the AAAA half makes the A
// answer arrive first on every run, so the assertions below are about what the filter does rather than
// about resolver ordering.
//
// This delay used to be load-bearing for a different reason: recovery took the first PUBLISHED family and
// filtered it afterwards, so an undialable AAAA answer arriving first ended the lookup and the usable A
// answer was discarded. `recoverCandidates` now filters as part of the choice, which is asserted directly
// by TestRecoverySkipsAnUndialableAnswerToReachAUsableOne; that test answers the undialable family first
// on purpose, which is the case this fixture avoids.
func newRecoveryHarness(t *testing.T, ipv4 netcap.FamilyState, ipv6 netcap.FamilyState, recovered []netip.Addr) *recoveryHarness {
	t.Helper()
	leaf := newCapabilityLeaf(ipv4, ipv6)
	leaf.inner.stall = nil
	leaf.inner.connectDelay = 0

	router := newScriptedFamilyRouter()
	// A deliberate ordering, so which family this test observes is decided by the fixture and not by
	// the scheduler.
	router.delayAAAA = 50 * time.Millisecond
	router.addressesA = common4(recovered)
	router.addressesAAAA = common6(recovered)

	resolver := &resolveDialer{
		router: router,
		dialer: leaf,
	}
	return &recoveryHarness{leaf: leaf, resolver: resolver, router: router, recovered: recovered}
}

// dialWithSniffedDomain drives one literal dial whose context carries a sniffed name, which is what
// makes recovery run at all.
func (h *recoveryHarness) dialWithSniffedDomain(t *testing.T, original string) (int, []netip.Addr, error) {
	t.Helper()
	h.original = M.ParseSocksaddr(original)
	ctx := adapter.WithContext(context.Background(), &adapter.InboundContext{
		Destination: h.original,
		Domain:      "sniffed.example",
	})
	conn, err := h.resolver.dialLiteralWithRecovery(ctx, "tcp", h.original)
	if conn != nil {
		conn.Close()
	}
	attempts := len(h.leaf.tried())
	return attempts, h.leaf.tried(), err
}

func common4(addresses []netip.Addr) []netip.Addr {
	var out []netip.Addr
	for _, address := range addresses {
		if address.Is4() {
			out = append(out, address)
		}
	}
	return out
}

func common6(addresses []netip.Addr) []netip.Addr {
	var out []netip.Addr
	for _, address := range addresses {
		if address.Is6() {
			out = append(out, address)
		}
	}
	return out
}

// TestRecoveryIsTheOnlyWayOutOfAnUndialableOriginalFamily is the case the whole feature exists for.
//
// The application connected to an IPv6 literal on a machine with no IPv6 addressing, and the sniffer
// recovered a name that also has an A record. The IPv4 record is the only way this connection can
// happen at all, so recovery must run - and it must be what carries the connection.
func TestRecoveryIsTheOnlyWayOutOfAnUndialableOriginalFamily(t *testing.T) {
	recovered4 := netip.MustParseAddr("192.0.2.50")
	harness := newRecoveryHarness(t, netcap.StateProvisioned, netcap.StateUnavailable,
		[]netip.Addr{netip.MustParseAddr("2001:db8::50"), recovered4})

	attempts, tried, err := harness.dialWithSniffedDomain(t, "2001:db8::1:443")
	require.NoError(t, err, "recovery must still run when the original's family is unavailable")
	require.Greater(t, attempts, 0)
	require.Contains(t, tried, recovered4,
		"the recovered address of the dialable family is the only candidate that can connect")
	require.NotContains(t, tried, netip.MustParseAddr("2001:db8::1"),
		"the original literal must not be attempted: the machine has no path for its family")
}

// TestStrictStrategyIsNotWidenedByTheCapability is the override the brief warns about most directly.
//
// With ipv6_only the IPv6 original is permitted by configuration and the recovered IPv4 is excluded by
// it. If the capability were consulted INSTEAD of the strategy - or allowed to relax it - the
// recovered IPv4 would be dialled and a strict policy would have been silently downgraded into a
// preference. Nothing about the machine may widen a policy the operator wrote.
func TestStrictStrategyIsNotWidenedByTheCapability(t *testing.T) {
	recovered4 := netip.MustParseAddr("192.0.2.50")
	harness := newRecoveryHarness(t, netcap.StateProvisioned, netcap.StateProvisioned,
		[]netip.Addr{recovered4})
	harness.resolver.queryOptions.Strategy = C.DomainStrategyIPv6Only

	_, tried, _ := harness.dialWithSniffedDomain(t, "2001:db8::1:443")
	require.NotContains(t, tried, recovered4,
		"ipv6_only excludes the recovered IPv4 address regardless of what the machine can dial")
}

// TestUndialableOriginalFamilyDoesNotBecomeADifferentFamily is the mirror of the same rule.
//
// With ipv4_only the IPv4 original is admitted and the recovered IPv6 is excluded. The machine's
// verdict must not turn that exclusion into an admission either: the two refusals are independent, and
// the second one must not become the first one's override.
func TestUndialableOriginalFamilyDoesNotBecomeADifferentFamily(t *testing.T) {
	recovered6 := netip.MustParseAddr("2001:db8::50")
	harness := newRecoveryHarness(t, netcap.StateProvisioned, netcap.StateUnavailable,
		[]netip.Addr{recovered6})
	harness.resolver.queryOptions.Strategy = C.DomainStrategyIPv4Only

	_, tried, _ := harness.dialWithSniffedDomain(t, "192.0.2.1:443")
	require.NotContains(t, tried, recovered6,
		"the recovered address is excluded by the strategy; the capability must not admit it")
}

// TestRecoveredOrderWithinAFamilyIsPreserved pins the promise that this package never second-guesses a
// resolver's ordering.
//
// The resolver's order carries information - priority, weight, the authoritative server's intent - and
// the capability filter must remove only what it refuses while leaving the relative order of
// everything else exactly as it arrived. A filter that rebuilt the list from a map, or that sorted it,
// would silently reorder every connection it touched.
func TestRecoveredOrderWithinAFamilyIsPreserved(t *testing.T) {
	first := netip.MustParseAddr("192.0.2.11")
	second := netip.MustParseAddr("192.0.2.12")
	third := netip.MustParseAddr("192.0.2.13")
	undialable := netip.MustParseAddr("2001:db8::11")

	// The undialable family sits in the MIDDLE of the list, so a filter that got the order wrong would
	// be visible rather than accidentally correct.
	addresses := []netip.Addr{first, undialable, second, third}
	policy := newLocalFamilyPolicy(netcap.FamilyCapability{
		IPv4: netcap.StateProvisioned,
		IPv6: netcap.StateUnavailable,
	})

	filtered := withoutUndialable(append([]netip.Addr(nil), addresses...), policy)
	require.Equal(t, []netip.Addr{first, second, third}, filtered,
		"the refused address is removed and every other address keeps its resolver position")

	// With nothing refused the input is returned untouched, so the common case cannot reorder anything
	// at all.
	same := withoutUndialable(append([]netip.Addr(nil), addresses...), localFamilyPolicy{})
	require.Equal(t, addresses, same)
}

// TestRecoveryRefusesToRecurse is the structural guard, asserted alongside the new filtering because
// the filtering gave the recovery path a reason to be re-entered.
//
// Recovery resolves a name, and that resolution may itself travel through a direct outbound. Without
// the marker, resolving a name would require resolving a name. The new local-verdict filter runs
// inside recovery, so the guard is asserted on the same path.
func TestRecoveryRefusesToRecurse(t *testing.T) {
	leaf := newCapabilityLeaf(netcap.StateProvisioned, netcap.StateUnavailable)
	router := newScriptedFamilyRouter()
	router.addressesA = []netip.Addr{netip.MustParseAddr("192.0.2.50")}
	resolver := &resolveDialer{router: router, dialer: leaf}

	sniffed := adapter.WithContext(context.Background(), &adapter.InboundContext{
		Destination: M.ParseSocksaddr("2001:db8::1:443"),
		Domain:      "sniffed.example",
	})
	require.NotNil(t, resolver.recoverCandidates(sniffed, M.ParseSocksaddr("2001:db8::1:443")),
		"a plain sniffed context recovers")

	require.Nil(t, resolver.recoverCandidates(withoutAddressRecovery(sniffed), M.ParseSocksaddr("2001:db8::1:443")),
		"a context already inside a recovery lookup must not recover again, however the addresses are "+
			"filtered afterwards")
}

// TestRecoveredUndialableFamilyIsDroppedBeforeTheRace pins where the filter sits on this path.
//
// It is applied to the recovered list itself, not only inside the scheduler, because the SIZE of that
// list is what decides whether the caller races recovery or keeps waiting for the original. A list that
// looks non-empty while holding only unreachable addresses sends the caller into a race that cannot
// connect.
func TestRecoveredUndialableFamilyIsDroppedBeforeTheRace(t *testing.T) {
	leaf := newCapabilityLeaf(netcap.StateProvisioned, netcap.StateUnavailable)
	resolver := &resolveDialer{dialer: leaf}

	only6 := []netip.Addr{netip.MustParseAddr("2001:db8::50")}
	require.Nil(t, resolver.withoutUndialableCandidates(only6),
		"a recovered list of only-undialable addresses becomes empty, so the caller does not race it")

	both := []netip.Addr{netip.MustParseAddr("2001:db8::50"), netip.MustParseAddr("192.0.2.50")}
	require.Equal(t, []netip.Addr{netip.MustParseAddr("192.0.2.50")},
		resolver.withoutUndialableCandidates(both),
		"and a mixed list keeps the dialable family in its original order")
}

// TestRecoveryIsNotShortCircuitedWhenTheOriginalIsRefused is the ordering inside
// dialLiteralWithRecovery, and it is easy to get backwards.
//
// The original attempt and the recovery lookup start together. When the machine refuses the original's
// family there is no attempt to make, so the ONLY thing that can carry the connection is recovery -
// which makes running it more important on this path, not less. An implementation that returned the
// refusal immediately would fail a connection that had a working answer in hand.
func TestRecoveryIsNotShortCircuitedWhenTheOriginalIsRefused(t *testing.T) {
	recovered4 := netip.MustParseAddr("192.0.2.50")
	harness := newRecoveryHarness(t, netcap.StateProvisioned, netcap.StateUnavailable,
		[]netip.Addr{recovered4})

	start := time.Now()
	_, tried, err := harness.dialWithSniffedDomain(t, "2001:db8::1:443")
	require.NoError(t, err)
	require.Contains(t, tried, recovered4)
	require.Less(t, time.Since(start), time.Second,
		"and it must not wait for a connect timeout first: there was no original attempt to wait for")
}

// TestRecoveryWithoutARecoveredAnswerReportsTheMachinesReason is the failure case.
//
// With the original refused and recovery returning nothing usable, the caller must be told about the
// machine rather than handed a connect timeout - which is the difference between an operator fixing
// their network and an operator staring at "i/o timeout".
func TestRecoveryWithoutARecoveredAnswerReportsTheMachinesReason(t *testing.T) {
	harness := newRecoveryHarness(t, netcap.StateProvisioned, netcap.StateUnavailable, nil)

	start := time.Now()
	_, tried, err := harness.dialWithSniffedDomain(t, "2001:db8::1:443")
	require.Error(t, err)
	require.ErrorIs(t, err, errLocalIPv6Unavailable,
		"the refusal must name the machine's addressing")
	require.Empty(t, tried)
	require.Less(t, time.Since(start), time.Second)
}
