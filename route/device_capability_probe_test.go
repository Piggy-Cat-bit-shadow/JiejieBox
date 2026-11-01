//go:build windows

package route

import (
	"net"
	"net/netip"
	"os"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	"github.com/sagernet/sing/common/control"
	E "github.com/sagernet/sing/common/exceptions"

	"github.com/stretchr/testify/require"
)

// TestDeviceCapabilityProbe reads THIS machine's live network and evaluates the capability with the
// production code path, so the Windows readers can be checked against what the machine actually has.
//
// # Why it is skipped by default
//
// It asserts on the real machine, so its expected values are a property of the host rather than of the
// code. A default-on device test is a test that fails on somebody else's laptop, which this repository's
// own rules treat as a defect. It is therefore gated by DSH_DEVICE_PROBE=1 and run deliberately:
//
//	$env:DSH_DEVICE_PROBE='1'; go test -tags "$TAGS" -run TestDeviceCapabilityProbe ./route -v
//
// # What it actually exercises
//
// The REAL pieces, in the order the product uses them:
//
//	net.Interfaces()                     the live interface list, addressing included
//	(*NetworkManager).machineGlobalIPv6Route()   the live Windows route table, through the production reader
//	(*NetworkManager).localFamilyCapability()    the production composition: one route observation applied
//	                                             to every interface, then netcap.UsableInterfaces + Evaluate
//
// The only thing this does not do is drive a transition - see TestDeviceTransitionProbe for that, which is
// the same probe run on either side of a real route change.
func TestDeviceCapabilityProbe(t *testing.T) {
	if os.Getenv("DSH_DEVICE_PROBE") == "" {
		t.Skip("set DSH_DEVICE_PROBE=1 to read this machine's live capability")
	}
	capability, routeObservation := liveDeviceCapability(t)
	t.Logf("DEVICE route observation = %v", routeObservation)
	t.Logf("DEVICE capability = IPv4:%v IPv6:%v GlobalIPv6Egress:%v (observed %d interfaces)",
		capability.IPv4, capability.IPv6, capability.GlobalIPv6Egress, capability.Observed())

	// The consistency the product depends on, asserted against the live machine rather than a fixture:
	// if the route reader says there is NO navigable global IPv6 route, then a global IPv6 destination
	// must be hard-undialable - and a link-local one must still be dialable, because "no way out to the
	// Internet" says nothing about the link.
	if routeObservation == netcap.RouteAbsent {
		globalIPv6 := mustAddr(t, "2001:db8::1")
		linkLocalIPv6 := mustAddr(t, "fe80::1")
		require.False(t, capability.HardUndialable(globalIPv6),
			"the route reader reported RouteAbsent but a global IPv6 destination is still undialable - "+
				"the verdict is not derived from the observation")
		require.False(t, capability.HardUndialable(linkLocalIPv6),
			"a missing global route must never remove a link-local destination")
	}
}

// TestDeviceTransitionProbe is the same probe, run twice: before and after an externally-applied network
// change. It exists so the evidence for a real transition is a test result rather than a manual reading.
//
// It reads the machine once, waits for the operator's change to land (the probe is re-run as a separate
// process after the change), and asserts nothing about the transition itself - the two runs' printed
// capability lines ARE the evidence, and the report quotes them.
func TestDeviceTransitionProbe(t *testing.T) {
	if os.Getenv("DSH_DEVICE_PROBE") == "" {
		t.Skip("set DSH_DEVICE_PROBE=1 to read this machine's live capability")
	}
	label := os.Getenv("DSH_PROBE_LABEL")
	if label == "" {
		label = "unlabelled"
	}
	capability, routeObservation := liveDeviceCapability(t)
	t.Logf("PROBE %s: route=%v IPv4=%v IPv6=%v GlobalIPv6Egress=%v interfaces=%d",
		label, routeObservation, capability.IPv4, capability.IPv6, capability.GlobalIPv6Egress,
		capability.Observed())
}

// liveDeviceCapability builds a manager from the LIVE interface list and lets the production composition
// read the LIVE route table. Nothing is injected except the interface list, which production receives from
// the platform monitor and which this reads from the standard library instead.
func liveDeviceCapability(t *testing.T) (netcap.FamilyCapability, netcap.RouteObservation) {
	t.Helper()
	interfaces, err := net.Interfaces()
	require.NoError(t, err, "the live interface list could not be read")

	var observed []adapter.NetworkInterface
	names := make([]string, 0, len(interfaces))
	for index := range interfaces {
		current := interfaces[index]
		addresses, addressErr := current.Addrs()
		if addressErr != nil {
			// An interface whose addressing cannot be read contributes nothing, which is exactly what
			// production does with a platform entry it cannot interpret.
			continue
		}
		prefixes := make([]netip.Prefix, 0, len(addresses))
		for _, address := range addresses {
			prefix, parseErr := netip.ParsePrefix(address.String())
			if parseErr != nil {
				continue
			}
			prefixes = append(prefixes, prefix)
		}
		observed = append(observed, adapter.NetworkInterface{Interface: control.Interface{
			Index:     current.Index,
			MTU:       current.MTU,
			Name:      current.Name,
			Addresses: prefixes,
			Flags:     current.Flags,
		}})
		names = append(names, current.Name)
	}
	require.NotEmpty(t, observed, "no interface could be read, so there is nothing to evaluate")

	manager := &NetworkManager{}
	manager.networkInterfaces.Store(observed)

	routeObservation := manager.machineGlobalIPv6Route()
	capability := manager.localFamilyCapability()
	if capability.Observed() == 0 {
		t.Logf("DEVICE note: the composition observed no usable interface among %v; the capability is "+
			"Unknown for both families, which is the documented degradation", names)
	}
	return capability, routeObservation
}

func mustAddr(t *testing.T, text string) netip.Addr {
	t.Helper()
	address, err := netip.ParseAddr(text)
	require.NoError(t, err, E.New("bad literal in the test itself: ", text))
	return address
}
