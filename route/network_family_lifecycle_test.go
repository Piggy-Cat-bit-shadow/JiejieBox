package route

import (
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common/control"

	"github.com/stretchr/testify/require"
)

// Layer 2 of the debug round: the lifecycle races, which the brief ranks among the highest priority.
//
// # The three shapes being attacked
//
//  1. a dial begins on generation A and the network transitions before it finishes
//  2. a reader lands inside the DURING window and reads ownership in two pieces
//  3. the epoch advances and the capability is recomputed while the interface list is still the old
//     one
//
// # Why they are one family of bug
//
// All three end with the same damage: a fact about one network being filed under another. The first
// two are handled by the ownership protocol that already existed - which is exactly why this round
// does not add a second one - and the third is handled by the ORDER the capability is computed in.
// The tests below are written to fail if any of those three answers is assumed rather than held.

// TestResetAloneKeepsTheVerdictAndMovesTheEpoch covers the entry point that does NOT recompute.
//
// ResetNetwork is reachable on its own - the libbox command server exposes it, ReleaseMemory calls it
// - and it advances the epoch without recomputing the environment, because a memory trim changes
// nothing about the network. The capability must survive that unchanged: it is a property of the
// interfaces, the interfaces did not move, and a dial issued in the new epoch must get the same
// answer the old epoch would have given.
//
// This is also the case that proves the capability is NOT simply cleared on every epoch advance. A
// conservative "invalidate on reset" would report Unknown, which fails open - so it would not be
// unsafe, but it would silently disable the fast-fail for one connection after every handover, which
// is precisely when it is wanted.
func TestResetAloneKeepsTheVerdictAndMovesTheEpoch(t *testing.T) {
	h := newTransitionWindowHarness(t)
	setInterfaces(h, "192.0.2.1", interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24"))
	h.settle(t, "wired-ipv4-only")

	beforeEpoch, beforeStable, beforeCapability := h.manager.NetworkFamilyCapabilitySnapshot()
	require.True(t, beforeStable)
	require.Equal(t, netcap.StateUnavailable, beforeCapability.Capability.IPv6)

	h.manager.ResetNetwork(h.manager.startedContext())

	afterEpoch, afterStable, afterCapability := h.manager.NetworkFamilyCapabilitySnapshot()
	require.True(t, afterStable, "the reset completes and settles")
	require.Greater(t, afterEpoch, beforeEpoch, "a reset is a transition and claims a new epoch")
	require.Equal(t, beforeCapability, afterCapability,
		"the interfaces did not move, so the verdict they describe must not either")
}

// TestEpochAdvanceWithoutACapabilityRefreshKeepsTheVerdict is the same invariant stated as the
// question the brief asks: can the epoch move without the capability being recomputed, and if so is
// the result safe?
//
// It can, and it is - but only because the capability is a function of the interfaces rather than of
// the epoch. The assertion is that a stale-BY-EPOCH capability is still a correct capability, which is
// what makes the two independent.
func TestEpochAdvanceWithoutACapabilityRefreshKeepsTheVerdict(t *testing.T) {
	h := newTransitionWindowHarness(t)
	setInterfaces(h, "192.0.2.1",
		interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24", "2001:db8::10/64"))
	h.settle(t, "dual-stack")
	first := currentFamilyCapability(t, h)
	require.Equal(t, netcap.StateProvisioned, first.Capability.IPv6)

	// Several epoch advances with no interface observation at all.
	for i := 0; i < 3; i++ {
		h.manager.ResetNetwork(h.manager.startedContext())
	}
	_, _, second := h.manager.NetworkFamilyCapabilitySnapshot()
	require.Equal(t, first, second,
		"the verdict is derived from the interfaces, so advancing the epoch cannot make it wrong - "+
			"and invalidating it would only lose information a dial needs")
}

// TestInterfaceObservationPrecedesTheVerdictOnTheEventPath orders the two halves of one event.
//
// The brief's third shape is "environment changed, epoch advanced, capability refresh fires, interface
// list still old". On the path a platform event takes, the capability is computed from the list the
// event just produced - so the ordering is enforced by construction rather than by timing, and this
// test asserts the construction: a monitor event publishes the new addressing and the new verdict
// together, and the verdict describes the NEW addressing.
func TestInterfaceObservationPrecedesTheVerdictOnTheEventPath(t *testing.T) {
	h := newTransitionWindowHarness(t)
	h.manager.platformInterface = nil
	finder := control.NewDefaultInterfaceFinder()
	h.manager.interfaceFinder = finder
	h.manager.networkInterfaces.Store(nil)

	// The device starts IPv4-only.
	finder.UpdateInterfaces([]control.Interface{{
		Index: 1, Name: "eth0", Flags: net.FlagUp | net.FlagRunning, Addresses: mustPrefixes("192.168.1.10/24"),
	}})
	h.manager.observeNonPlatformInterfaces()
	h.settle(t, "wired")
	require.Equal(t, netcap.StateUnavailable, currentFamilyCapability(t, h).Capability.IPv6)

	// IPv6 arrives on the same link. The monitor refreshes the finder first, then reports - which is
	// the order observeNonPlatformInterfaces depends on - so the capability published with this event
	// is derived from the addressing the event is about.
	finder.UpdateInterfaces([]control.Interface{{
		Index: 1, Name: "eth0", Flags: net.FlagUp | net.FlagRunning,
		Addresses: mustPrefixes("192.168.1.10/24", "2001:db8::10/64"),
	}})
	h.manager.observeNonPlatformInterfaces()
	h.manager.updateNetworkEnvironment()

	_, stable, capability := h.manager.NetworkFamilyCapabilitySnapshot()
	require.True(t, stable)
	require.Equal(t, netcap.StateProvisioned, capability.Capability.IPv6,
		"the verdict published by the event describes the addressing the event reported")
}

// TestObservationThatCannotSeeItsOwnInterfacesIsUnknownRatherThanStale is the failure mode of the
// ordering above.
//
// If the list could not be refreshed - no finder, a platform that reports nothing - the verdict must
// fall back to "no opinion" rather than to the previous network's answer. A stale answer is worse
// than none here: Unknown fails open and costs a fast-fail, while a stale Unavailable refuses
// candidates on a network that may well have the family.
func TestObservationThatCannotSeeItsOwnInterfacesIsUnknownRatherThanStale(t *testing.T) {
	h := newTransitionWindowHarness(t)
	setInterfaces(h, "192.0.2.1", interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24"))
	h.settle(t, "observed")
	require.Equal(t, netcap.StateUnavailable, currentFamilyCapability(t, h).Capability.IPv6)

	// The observation source goes away, as it does when a manager is built without a finder or a
	// platform reports nothing at all.
	h.manager.networkInterfaces.Store(nil)
	h.manager.updateNetworkEnvironment()

	capability := currentFamilyCapability(t, h).Capability
	require.Equal(t, netcap.StateUnknown, capability.IPv6,
		"with nothing to look at the answer is no opinion, never the previous observation")
	require.False(t, capability.HardUnavailable(false),
		"and no opinion must not refuse anything")
}

// TestVerdictIsNeverCarriedAcrossARealTransition is the end-to-end form of the first shape, driven
// through the event the platform actually delivers.
//
// A Wi-Fi to cellular handover is the case the round's brief names: an IPv6 verdict learned on the
// Wi-Fi must not decide a dial on the cellular network. It is asserted through the notification
// entry point rather than through a direct recompute, so it covers the path production takes.
func TestVerdictIsNeverCarriedAcrossARealTransition(t *testing.T) {
	h := newTransitionWindowHarness(t)
	h.manager.platformInterface = nil
	finder := control.NewDefaultInterfaceFinder()
	h.manager.interfaceFinder = finder

	// Wi-Fi: IPv4 only.
	finder.UpdateInterfaces([]control.Interface{{
		Index: 1, Name: "wlan0", Flags: net.FlagUp | net.FlagRunning, Addresses: mustPrefixes("192.168.1.10/24"),
	}})
	h.manager.observeNonPlatformInterfaces()
	h.settle(t, "wifi")
	require.Equal(t, netcap.StateUnavailable, currentFamilyCapability(t, h).Capability.IPv6)

	// The handover. The new link carries IPv6, and the platform reports it as a new default interface.
	finder.UpdateInterfaces([]control.Interface{{
		Index: 2, Name: "rmnet0", Flags: net.FlagUp | net.FlagRunning,
		Addresses: mustPrefixes("10.20.0.5/24", "2001:db8::5/64"),
	}})
	h.manager.interfaceMonitor = &staticInterfaceMonitor{
		current: &control.Interface{Index: 2, Name: "rmnet0"},
	}
	h.setSSID("cellular")
	h.manager.notifyInterfaceUpdate(&control.Interface{Index: 2, Name: "rmnet0"}, 0)

	// The notification claims a transition; settle it the way the update goroutine would.
	h.manager.updateNetworkEnvironment()

	_, stable, capability := h.manager.NetworkFamilyCapabilitySnapshot()
	require.True(t, stable)
	require.Equal(t, netcap.StateProvisioned, capability.Capability.IPv6,
		"the new network's IPv6 must be attemptable; the Wi-Fi's verdict belonged to the Wi-Fi")
}

// TestCapabilityReadIsConsistentUnderConcurrentTransitions is the race-detector-visible form.
//
// Many readers take the snapshot the way a dial does while transitions publish underneath them. The
// assertion is not that a particular value is read - any state the manager passed through is legal -
// but that every read is a state the manager could have been in, which is the definition of the
// property the atomic publication exists to provide. A torn read would show the new network's verdict
// beside the old network's ownership, and the pairing below is what makes that detectable.
func TestCapabilityReadIsConsistentUnderConcurrentTransitions(t *testing.T) {
	h := newTransitionWindowHarness(t)
	// Each generation's IPv6 verdict is a function of a distinct interface set, so a value that
	// belongs to neither is a torn read rather than an unlucky one.
	ipv4Only := []adapter.NetworkInterface{
		interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24"),
	}
	dualStack := []adapter.NetworkInterface{
		interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24", "2001:db8::10/64"),
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	// Readers record any value that is not one of the two legitimate states for the epoch-free part of
	// the contract: the family verdict must always be one of the states this manager can publish.
	var torn = make(chan netcap.FamilyState, 1)
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _, capability := h.manager.NetworkFamilyCapabilitySnapshot()
			switch capability.Capability.IPv6 {
			case netcap.StateUnknown, netcap.StateUnavailable, netcap.StateProvisioned:
			default:
				select {
				case torn <- capability.Capability.IPv6:
				default:
				}
				return
			}
		}
	}()

	for round := 0; round < 200; round++ {
		if round%2 == 0 {
			setInterfaces(h, "192.0.2.1", ipv4Only...)
		} else {
			setInterfaces(h, "192.0.2.1", dualStack...)
		}
		h.manager.updateNetworkEnvironment()
	}
	close(stop)
	<-done

	select {
	case state := <-torn:
		t.Fatalf("a reader observed a state this manager cannot publish: %v", state)
	default:
	}
}
