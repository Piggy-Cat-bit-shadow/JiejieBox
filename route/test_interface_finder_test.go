package route

import (
	"net"
	"net/netip"

	"github.com/sagernet/sing-box/common/netcap"
	"github.com/sagernet/sing/common/control"

	"github.com/stretchr/testify/require"
	"testing"
)

// testInterfaceFinder is the real finder with its ENUMERATION replaced by a fixture.
//
// # The defect it exists for
//
// `NetworkManager.UpdateInterfaces` has a Darwin-only enrichment step:
//
//	if C.IsDarwin {
//	    err = r.interfaceFinder.Update()                       // <- enumerates the REAL machine
//	    ...
//	    interfaces = common.Map(interfaces, func(it adapter.NetworkInterface) adapter.NetworkInterface {
//	        iif, _ := r.interfaceFinder.ByIndex(it.Index)      // <- replaces the platform's answer
//	        if iif != nil { it.Interface = *iif }
//	        return it
//	    })
//	}
//
// The reason is real and the comment says so: "NEInterface only provides name,index and type". On
// Darwin the platform's list is deliberately enriched from the system's own view.
//
// In a TEST that is a hermeticity hole. The harness supplies a stub list - one `wlan0`, IPv4 only -
// but `Update()` enumerates the runner's interfaces and `ByIndex(1)` then overwrites the stub entry
// with whatever the machine really has at index 1. The capability is computed from the MACHINE's
// addressing rather than the fixture's, so a network the harness defines as IPv4-only reports IPv6 as
// StateProvisioned on a host that has IPv6. That is what the macOS artifact run reported:
//
//	network_family_refresh_failure_test.go:109: expected: 0x1, actual: 0x2
//	    the harness must start from a network whose verdict really does refuse IPv6
//
// It passed on Windows for the reason this repository keeps re-learning: the non-Darwin arm calls
// `UpdateInterfaces(list)`, which installs the fixture under both accessors, so the fixture WAS the
// finder's answer there. The test was hermetic only on the platform that skips the enrichment.
//
// # Why embedding the real finder is the right shape
//
// `control.InterfaceFinder` has nine methods, including the callback registry the monitor uses; a
// hand-written fake would be nine chances to diverge from the real one. Embedding
// `*control.DefaultInterfaceFinder` and overriding ONLY `Update` keeps every other method real while
// making `UpdateInterfaces` the sole source of what this finder knows - so the production enrichment
// reads back the fixture, which is what the test means.
//
// This is a TEST type. On Darwin the enrichment is correct and load-bearing; nothing here says
// otherwise. What was wrong was a test that could not supply its own input on one platform.
type testInterfaceFinder struct {
	*control.DefaultInterfaceFinder
	fixture []control.Interface
}

// Update deliberately does NOT enumerate the machine. It re-installs the fixture, so `Update` keeps
// its production contract - "after this call the finder describes the current interfaces" - while
// "current" means "what the test said".
func (f *testInterfaceFinder) Update() error {
	f.UpdateInterfaces(f.fixture)
	return nil
}

func newTestInterfaceFinder(initial []control.Interface) *testInterfaceFinder {
	finder := &testInterfaceFinder{
		DefaultInterfaceFinder: control.NewDefaultInterfaceFinder(),
		fixture:                initial,
	}
	finder.UpdateInterfaces(initial)
	return finder
}

// TestTheDarwinEnrichmentReadsTheFixtureNotTheMachine guards the hole above on EVERY platform.
//
// It asserts the property the macOS failure violated: after a harness observes a stub interface list,
// the interface the manager publishes carries the STUB's addressing. On a host without the Darwin
// enrichment this passes trivially; on one with it, it is the assertion that was missing.
//
// It is written against `networkInterfaces` - the list the capability is derived from - as well as
// against the capability, so a future term added to netcap.Evaluate cannot make it pass by accident.
func TestTheDarwinEnrichmentReadsTheFixtureNotTheMachine(t *testing.T) {
	harness := newPlatformHandoverHarness(t)

	interfaces := harness.manager.networkInterfaces.Load()
	require.Len(t, interfaces, 1, "the harness supplies exactly one interface")

	// The fixture is IPv4-only. If the machine's own interface had been substituted, this would see
	// whatever the runner has - which on a dual-stack runner includes IPv6.
	for _, prefix := range interfaces[0].Interface.Addresses {
		address := prefix.Addr()
		require.False(t, address.Is6() && !address.Is4In6(),
			"the published interface carries IPv6 addressing (%v), so the enrichment read the "+
				"MACHINE instead of the fixture. That is the macOS failure: an IPv4-only fixture "+
				"reported StateProvisioned for IPv6 because interfaceFinder.ByIndex returned the "+
				"runner's real interface", address)
	}

	require.Equal(t, "wlan0", interfaces[0].Interface.Name,
		"the interface must still be the fixture's, not the host's")
	require.NotZero(t, interfaces[0].Interface.Flags&net.FlagUp,
		"an up interface is what the publish filter keeps; the fixture's flags must survive")

	// The fact the four failing assertions actually read.
	require.Equal(t, netcap.StateUnavailable, currentFamilyCapabilityOf(t, harness.manager).Capability.IPv6,
		"and the verdict derived from an IPv4-only fixture must refuse IPv6")

	// The positive control: the same finder CAN report IPv6 when the fixture has it, so the
	// assertions above cannot pass by the finder answering nothing at all.
	finder := newTestInterfaceFinder([]control.Interface{{
		Index:     1,
		Name:      "en0",
		Flags:     net.FlagUp,
		Addresses: []netip.Prefix{netip.MustParsePrefix("2001:db8::5/64")},
	}})
	byIndex, err := finder.ByIndex(1)
	require.NoError(t, err)
	require.NotNil(t, byIndex, "a fixture installed into the finder must be readable from it")
	require.Len(t, byIndex.Addresses, 1)
	require.True(t, byIndex.Addresses[0].Addr().Is6(),
		"and it must carry the address the fixture gave, not an empty list")
}
