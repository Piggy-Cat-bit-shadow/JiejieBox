package route

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/common/netcap"

	"github.com/stretchr/testify/require"
)

// The reset entry points and the address-family verdict.
//
// # The invariant
//
// A network epoch may not consume an address-family verdict that was observed on a DIFFERENT epoch.
// The epoch is the thing that says which network a verdict belongs to, so a verdict left over from
// the previous generation is not "slightly stale" - it is a claim about a network this generation has
// never observed.
//
// # Why the reset entry points were the hole
//
// The interface path recomputes the environment before it claims, so its epoch always arrives with a
// verdict observed at the same instant. The reset entry points did not: ResetNetwork (the control
// plane, ReleaseMemory and the Windows suspend leg) and the Windows resume path each claimed an epoch
// and ran the reset body directly. The epoch therefore advanced while the published capability stayed
// exactly as the previous network left it.
//
// The consumer of that value is a HARD refusal, not a hint. common/dialer reads it to drop every
// candidate of a family from the race and to fail a literal dial before a socket exists, so an
// IPv6-unavailable verdict carried across a reset refuses IPv6 on a network that has it - for the
// whole life of the new generation, or until some later interface event happens to republish.
//
// # Why the two directions are separate tests
//
// A fix that simply CLEARED the verdict on every reset would pass the unavailable -> available case
// and fail its mirror, because clearing throws away a verdict that is still correct for the network
// the device is still on. The fix under test re-observes instead, so both directions have to hold:
// the new generation gets the CURRENT facts, whichever way they point.
//
// This is the integration of the audit line's reset-lifecycle fix onto the platform route-reader
// implementation. The audit branch (audit/dual-stack-ipv4-ipv6-lifecycle @ 200274c5076) found it
// against a squashed rewrite of this same feature; the defect and the fix are identical here, so the
// tests are carried over rather than re-derived.

// TestResetNetworkRepublishesTheVerdictWhenTheAddressingMoved is the reported direction.
//
// Wi-Fi hands out IPv4 only; by the time the control plane asks for a reset the device has IPv6
// addressing. The new generation must see IPv6 as attemptable.
func TestResetNetworkRepublishesTheVerdictWhenTheAddressingMoved(t *testing.T) {
	h := newTransitionWindowHarness(t)

	setInterfaces(h, "192.0.2.1", interfaceWithAddresses(1, "en0", "192.168.1.10/24"))
	h.settle(t, "wifi-ipv4-only")
	require.Equal(t, netcap.StateUnavailable, currentFamilyCapability(t, h).Capability.IPv6,
		"precondition: this network has no IPv6 addressing")
	epochBefore := h.manager.NetworkResetGeneration()

	// The device now has IPv6 addressing. Nothing has republished yet: the control plane is about to
	// ask for a reset, and re-observing is that entry point's job.
	setInterfaces(h, "192.0.2.9",
		interfaceWithAddresses(1, "en0", "192.168.1.10/24", "2001:db8::10/64"))

	h.manager.ResetNetwork(context.Background())

	epochAfter := h.manager.NetworkResetGeneration()
	require.Greater(t, epochAfter, epochBefore, "a reset must still produce a new generation")
	require.Equal(t, netcap.StateProvisioned, currentFamilyCapability(t, h).Capability.IPv6,
		"the new generation must not inherit the previous network's IPv6-unavailable verdict: that "+
			"verdict is a hard refusal, so carrying it across the reset refuses IPv6 on a network "+
			"that has IPv6 addressing")
	require.False(t, currentFamilyCapability(t, h).Capability.HardUnavailable(false))
}

// TestResetNetworkRepublishesTheVerdictWhenIPv6WentAway is the mirror direction.
//
// It is what separates "re-observe" from "clear on reset": a fix that cleared the verdict would make
// this pass vacuously by reporting Unknown, so the test asserts the POSITIVE fact - IPv6 is proven
// unavailable, not merely unproven - and would fail a clearing implementation.
func TestResetNetworkRepublishesTheVerdictWhenIPv6WentAway(t *testing.T) {
	h := newTransitionWindowHarness(t)

	setInterfaces(h, "192.0.2.1",
		interfaceWithAddresses(1, "en0", "192.168.1.10/24", "2001:db8::10/64"))
	h.settle(t, "dual-stack")
	require.Equal(t, netcap.StateProvisioned, currentFamilyCapability(t, h).Capability.IPv6,
		"precondition: this network has IPv6 addressing")
	epochBefore := h.manager.NetworkResetGeneration()

	// The IPv6 lease expired: the same link, now IPv4-only.
	setInterfaces(h, "192.0.2.1", interfaceWithAddresses(1, "en0", "192.168.1.10/24"))

	h.manager.ResetNetwork(context.Background())

	require.Greater(t, h.manager.NetworkResetGeneration(), epochBefore)
	require.Equal(t, netcap.StateUnavailable, currentFamilyCapability(t, h).Capability.IPv6,
		"the new generation must carry the CURRENT facts, not the previous network's verdict. "+
			"Unknown here would be wrong in the other direction: the machine HAS been observed, and "+
			"it has no IPv6 addressing, which is exactly the fast-fail this model exists to enable")
	require.True(t, currentFamilyCapability(t, h).Capability.HardUnavailable(false))
}

// TestResetNetworkOnAnUnchangedEnvironmentKeepsTheVerdictAndTheEpoch pins the case a "clear on
// reset" fix would break, and the case the epoch must not churn on.
//
// The device is on the SAME network with the SAME addressing - which is the ordinary shape of a
// control-plane reset, a ReleaseMemory, or a suspend. The verdict is still correct, so it must
// survive; and because neither the network nor the addressing moved, the reset must not invent a new
// verdict for facts that did not change at all.
func TestResetNetworkOnAnUnchangedEnvironmentKeepsTheVerdictAndTheEpoch(t *testing.T) {
	h := newTransitionWindowHarness(t)

	setInterfaces(h, "192.0.2.1",
		interfaceWithAddresses(1, "en0", "192.168.1.10/24", "2001:db8::10/64"))
	h.settle(t, "stable-network")

	epochBefore := h.manager.NetworkResetGeneration()
	beforeSnapshot := currentFamilyCapability(t, h)

	h.manager.ResetNetwork(context.Background())

	require.Greater(t, h.manager.NetworkResetGeneration(), epochBefore,
		"the reset itself is still a new generation")

	// The facts did not move, so neither may the verdict: it is still the correct answer for the
	// network the device is on, and discarding it would lose a hard negative the machine has earned.
	afterSnapshot := currentFamilyCapability(t, h)
	require.Equal(t, beforeSnapshot.Capability.IPv6, afterSnapshot.Capability.IPv6)
	require.Equal(t, netcap.StateProvisioned, afterSnapshot.Capability.IPv6,
		"an unchanged environment must keep its verdict: the reset must re-observe, not clear")
	require.Equal(t, beforeSnapshot.Capability.IPv4, afterSnapshot.Capability.IPv4)
}

// TestResetNetworkVerdictMatchesTheEpochItIsReadBeside is the invariant stated as a single
// observation, which is how a dial consumes it.
//
// Whichever facts the network has, the triple a consumer reads after a reset must describe ONE
// network: the epoch of the generation the reset produced, and a verdict derived from the interfaces
// as they are now. A reader that takes the snapshot after the reset must never be able to see the
// previous network's verdict beside the new epoch.
func TestResetNetworkVerdictMatchesTheEpochItIsReadBeside(t *testing.T) {
	h := newTransitionWindowHarness(t)

	// Start IPv6-unavailable so an inherited verdict is unambiguous.
	setInterfaces(h, "192.0.2.1", interfaceWithAddresses(1, "en0", "192.168.1.10/24"))
	h.settle(t, "wifi-ipv4-only")
	epochBefore := h.manager.NetworkResetGeneration()

	// The addressing moves to dual stack, and the reset is what publishes it.
	setInterfaces(h, "192.0.2.9",
		interfaceWithAddresses(1, "en0", "192.168.1.10/24", "2001:db8::10/64"))
	h.manager.ResetNetwork(context.Background())

	epoch, stable, capability := h.manager.NetworkFamilyCapabilitySnapshot()
	require.True(t, stable, "the reset committed, so the network it produced is settled")
	require.Greater(t, epoch, epochBefore, "the snapshot must carry the new generation's epoch")

	// The verdict read beside that epoch must be the one the CURRENT interfaces justify. This is the
	// whole invariant in one assertion: epoch N's verdict is derived from epoch N's facts.
	require.Equal(t, netcap.StateProvisioned, capability.Capability.IPv6,
		"the verdict read beside the new epoch must be derived from the facts observed for it")
	require.Equal(t, netcap.StateProvisioned, capability.Capability.IPv4)
}
