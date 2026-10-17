package dialer

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Recovery must keep looking when the FIRST family to answer is one this machine cannot dial.
//
// # The defect
//
// `recoverCandidates` streams the lookup so a fast family is usable immediately, and it took the
// document's word for what "immediately" meant: "The first usable family is returned as soon as it
// arrives". The code did something weaker - it took the first NON-EMPTY family and filtered it
// afterwards:
//
//	select {
//	case first := <-collected:          // first PUBLISHED family
//		recovered = first
//	...
//	return d.withoutUndialableCandidates(recovered)   // filtered AFTER the choice
//
// So when the family that answers first is the one the machine cannot dial, the filter empties the list
// and recovery returns nothing - while the usable answer from the other family is still in flight, and
// `defer cancelStream()` kills it. The dial then fails with a machine-family error although a working
// fallback existed.
//
// # Why this is the reported machine's shape
//
// The reported Windows machine has IPv6 addressing and no global IPv6 egress, so an AAAA answer is
// refused by the egress term. The sniffed name's AAAA commonly answers before its A - the two are
// independent queries and either can win. On such a run, recovery produced nothing and the IPv6 literal
// was refused with no IPv4 attempt, which is exactly the symptom the round-1 work was meant to remove.
//
// The existing fixture had already recorded the hazard and routed around it: `newRecoveryHarness`
// delays the AAAA half "so the A answer is always published first", with a comment explaining that
// otherwise "the IPv6 answer would sometimes arrive first, be refused by the filter, and end the lookup".
// That is this bug, written down as a property of the fixture instead of as a property of the code.

// newInvertedRecoveryHarness answers the UNDIALABLE family FIRST and the usable one second.
//
// This is the case the shipped fixture was built to avoid, which is why nothing caught it.
func newInvertedRecoveryHarness(t *testing.T, ipv4, ipv6 netcap.FamilyState, undialableFirst []netip.Addr, usableAfter []netip.Addr) (*recoveryHarness, time.Duration) {
	t.Helper()
	leaf := newCapabilityLeaf(ipv4, ipv6)
	leaf.inner.stall = nil
	leaf.inner.connectDelay = 0

	router := newScriptedFamilyRouter()
	// The A half is delayed, so the undialable AAAA answer is always published FIRST.
	router.delayA = 40 * time.Millisecond
	router.delayAAAA = 0
	router.addressesAAAA = undialableFirst
	router.addressesA = usableAfter

	resolver := &resolveDialer{router: router, dialer: leaf}
	return &recoveryHarness{leaf: leaf, resolver: resolver, router: router}, router.delayA
}

// TestRecoverySkipsAnUndialableAnswerToReachAUsableOne is the regression.
//
// Capability is the reported machine: IPv4 usable, IPv6 present but undialable. The original is an IPv6
// literal with a sniffed domain, the AAAA answer arrives first and is refused, and the A answer - a real
// dialable IPv4 address - arrives while the lookup is still open.
//
// Recovery must return the IPv4 address. Returning nothing is the bug, and it is invisible to every
// existing test because they all delay the AAAA half.
func TestRecoverySkipsAnUndialableAnswerToReachAUsableOne(t *testing.T) {
	original := netip.MustParseAddr("2409:8c54:871:1007::2c")
	healthyIPv4 := netip.MustParseAddr("112.53.55.92")

	harness, secondFamilyDelay := newInvertedRecoveryHarness(t,
		netcap.StateProvisioned, netcap.StateUnavailable,
		[]netip.Addr{original},    // AAAA first: refused by the machine
		[]netip.Addr{healthyIPv4}, // A second: exactly the fallback the flow needs
	)

	ctx := adapter.WithContext(context.Background(), &adapter.InboundContext{
		Destination: M.SocksaddrFrom(original, 80),
		Domain:      "szminorshort.weixin.qq.com",
	})

	start := time.Now()
	recovered := harness.resolver.recoverCandidates(ctx, M.SocksaddrFrom(original, 80))
	elapsed := time.Since(start)

	require.Equal(t, []netip.Addr{healthyIPv4}, recovered,
		"recovery must wait for a family this machine CAN dial rather than returning the first family to "+
			"answer and filtering it away; returning nothing here means the IPv6 literal is refused with "+
			"no IPv4 attempt")
	require.GreaterOrEqual(t, elapsed, secondFamilyDelay,
		"and it must have waited for the second family, which is what proves it did not simply accept the "+
			"first published answer")
}

// TestRecoveryReturnsAtOnceWhenTheFirstFamilyIsUsable keeps the streaming property.
//
// The whole point of the streaming path is that a fast usable family is used immediately: recovery must
// not wait for the slow family when the first answer can already carry the flow. The fix must not turn
// "usable first" into "wait for everything".
func TestRecoveryReturnsAtOnceWhenTheFirstFamilyIsUsable(t *testing.T) {
	original := netip.MustParseAddr("2409:8c54:871:1007::2c")
	healthyIPv4 := netip.MustParseAddr("112.53.55.92")

	leaf := newCapabilityLeaf(netcap.StateProvisioned, netcap.StateUnavailable)
	leaf.inner.stall = nil
	router := newScriptedFamilyRouter()
	// The USABLE family answers at once and the other one never does, so a recovery that waits for the
	// whole lookup would hang until the context expires.
	router.delayA = 0
	router.delayAAAA = 5 * time.Second
	router.addressesA = []netip.Addr{healthyIPv4}
	router.addressesAAAA = []netip.Addr{original}
	resolver := &resolveDialer{router: router, dialer: leaf}

	ctx, cancel := context.WithTimeout(adapter.WithContext(context.Background(), &adapter.InboundContext{
		Destination: M.SocksaddrFrom(original, 80),
		Domain:      "szminorshort.weixin.qq.com",
	}), 2*time.Second)
	defer cancel()

	start := time.Now()
	recovered := resolver.recoverCandidates(ctx, M.SocksaddrFrom(original, 80))
	elapsed := time.Since(start)

	require.Equal(t, []netip.Addr{healthyIPv4}, recovered)
	require.Less(t, elapsed, time.Second,
		"a usable first family must be returned immediately: recovery is a fallback, and waiting for a "+
			"family that will never answer would delay a flow that already has a candidate")
}

// TestRecoveryReportsNothingWhenEveryFamilyIsUndialable is the control that keeps the fix from looping
// forever or inventing an answer.
//
// Both families answer, both are refused by the machine, and the stream then closes. Recovery must end
// with nothing rather than hanging or returning a refused address.
func TestRecoveryReportsNothingWhenEveryFamilyIsUndialable(t *testing.T) {
	original := netip.MustParseAddr("2409:8c54:871:1007::2c")
	otherIPv6 := netip.MustParseAddr("2409:8c54:871:2007::10d")

	leaf := newCapabilityLeaf(netcap.StateProvisioned, netcap.StateUnavailable)
	leaf.inner.stall = nil
	router := newScriptedFamilyRouter()
	router.delayA = 0
	router.delayAAAA = 0
	router.addressesA = []netip.Addr{otherIPv6}
	router.addressesAAAA = []netip.Addr{original}
	resolver := &resolveDialer{router: router, dialer: leaf}

	ctx, cancel := context.WithTimeout(adapter.WithContext(context.Background(), &adapter.InboundContext{
		Destination: M.SocksaddrFrom(original, 80),
		Domain:      "szminorshort.weixin.qq.com",
	}), 3*time.Second)
	defer cancel()

	done := make(chan []netip.Addr, 1)
	go func() {
		done <- resolver.recoverCandidates(ctx, M.SocksaddrFrom(original, 80))
	}()

	select {
	case recovered := <-done:
		require.Empty(t, recovered,
			"no candidate may be returned for a machine that cannot dial either family")
	case <-time.After(2 * time.Second):
		t.Fatal("recovery must terminate once the stream closes, not wait for a family that will not come")
	}
}
