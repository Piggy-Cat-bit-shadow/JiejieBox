package dialer

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
)

// Strict network strategies and the machine verdict are INDEPENDENT restrictions, and adding the
// global-egress term must keep them that way in both directions.
//
// # Why this needs its own test rather than being covered by the gate tests
//
// The two rules look similar - both remove a candidate before it is dialled - and they are enforced in
// different layers for different reasons: the strategy is a CONFIGURATION decision the operator made
// ("this domain must go over IPv6"), and the verdict is a MACHINE fact. A change to one that quietly
// absorbed the other would be invisible in every existing test, because each layer's tests only ever
// exercised its own rule.
//
// The two failure modes this pins:
//
//	the gate admits a family the strategy excluded  -> configuration is silently overridden
//	the strategy admits a family the gate excluded  -> a guaranteed failure is spent a timeout
//
// The second is what the reported outage was; the first is what a careless fix for it would cause.

// egressRefusingPolicy is the reported machine: IPv6 present, no route to a global IPv6 destination.
func egressRefusingPolicy() localFamilyPolicy {
	return newLocalFamilyPolicy(netcap.FamilyCapability{
		IPv4:             netcap.StateProvisioned,
		IPv6:             netcap.StateProvisioned,
		GlobalIPv6Egress: netcap.StateUnavailable,
	})
}

// TestAStrictStrategyIsNotRelaxedByTheEgressVerdict is the "configuration wins" direction.
//
// ipv4_only must still exclude IPv6 even though the machine verdict refuses it too - the reasons must
// stay different, and the gate must not be what removes the candidate, because then the operator's
// own setting would no longer be the stated cause.
func TestAStrictStrategyIsNotRelaxedByTheEgressVerdict(t *testing.T) {
	globalIPv6 := netip.MustParseAddr("2409:8a00::1")
	publicIPv4 := netip.MustParseAddr("93.184.216.34")
	policy := egressRefusingPolicy()

	// The strategy layer answers first and independently of the machine.
	require.False(t, AddressAllowedByStrategy(globalIPv6, C.DomainStrategyIPv4Only))
	require.True(t, AddressAllowedByStrategy(publicIPv4, C.DomainStrategyIPv4Only))
	require.True(t, AddressAllowedByStrategy(globalIPv6, C.DomainStrategyIPv6Only))
	require.False(t, AddressAllowedByStrategy(publicIPv4, C.DomainStrategyIPv6Only))

	// And the machine verdict does not depend on the strategy at all: it is a fact about the network,
	// so it answers the same way whichever configuration is in force.
	require.True(t, policy.hardUndialable(globalIPv6))
	require.False(t, policy.hardUndialable(publicIPv4))
}

// TestAnIPv6OnlyStrategyOnAMachineWithNoPublicIPv6StillRefuses is the composition, and the case an
// operator would actually hit: a domain pinned to ipv6_only on the machine from the report.
//
// The candidate list must end up EMPTY rather than falling back to IPv4. Falling back would override
// the operator's configuration, and the refusal must be attributed to the machine so the log says why
// the configured family could not be used.
func TestAnIPv6OnlyStrategyOnAMachineWithNoPublicIPv6StillRefuses(t *testing.T) {
	globalIPv6 := netip.MustParseAddr("2409:8a00::1")

	// The strategy admits only the IPv6 candidate...
	require.True(t, AddressAllowedByStrategy(globalIPv6, C.DomainStrategyIPv6Only))
	// ...and the machine refuses it. The result is a refusal, not a silent downgrade.
	refused := refusedFamilyIn([]netip.Addr{globalIPv6}, egressRefusingPolicy())
	require.Equal(t, familyIPv6, refused)

	filtered := withoutUndialable([]netip.Addr{globalIPv6}, egressRefusingPolicy())
	require.Empty(t, filtered, "no candidate may survive, and IPv4 must not be substituted")
}

// TestAPermissiveStrategyIsStillSubjectToTheEgressVerdict is the other direction.
//
// "default" and the preference strategies admit both families, so the machine verdict is the only
// thing standing between a global IPv6 candidate and a guaranteed kernel failure. This is the exact
// configuration that produced the reported outage.
func TestAPermissiveStrategyIsStillSubjectToTheEgressVerdict(t *testing.T) {
	globalIPv6 := netip.MustParseAddr("2409:8a00::1")
	publicIPv4 := netip.MustParseAddr("93.184.216.34")

	for _, strategy := range []C.DomainStrategy{
		C.DomainStrategyAsIS,
		C.DomainStrategyPreferIPv4,
		C.DomainStrategyPreferIPv6,
	} {
		require.Truef(t, AddressAllowedByStrategy(globalIPv6, strategy),
			"%s admits IPv6, so the machine verdict is the only thing left", strategy)

		filtered := withoutUndialable([]netip.Addr{globalIPv6, publicIPv4}, egressRefusingPolicy())
		require.Equalf(t, []netip.Addr{publicIPv4}, filtered,
			"%s: the global IPv6 candidate must be dropped and the IPv4 one kept", strategy)
	}
}

// TestTheTwoRulesStayAttributable pins that a refusal keeps saying WHICH rule refused it.
//
// An operator reading the log has to be able to tell "my configuration excludes this" from "this
// machine cannot reach it", because the two are fixed in completely different places.
func TestTheTwoRulesStayAttributable(t *testing.T) {
	globalIPv6 := netip.MustParseAddr("2409:8a00::1")

	// The machine's reason names the machine and the public path, and says nothing about configuration.
	machineReason := egressRefusingPolicy().hardUndialableError(globalIPv6)
	require.Error(t, machineReason)
	require.Contains(t, machineReason.Error(), "no route to a global ipv6 destination")
	require.NotContains(t, machineReason.Error(), "ipv4_only")
	require.NotContains(t, machineReason.Error(), "ipv6_only")

	// The configuration's reason is produced elsewhere and is a different error entirely, so the two
	// can never be confused for one another in a log.
	require.ErrorIs(t, localFamilyRefusalError(familyIPv6), errLocalIPv6Unavailable)
	require.NotErrorIs(t, localFamilyRefusalError(familyIPv6), errLocalIPv6EgressUnavailable)
}
