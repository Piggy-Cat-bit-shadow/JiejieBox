package libbox

import (
	"net/netip"
	"testing"

	tun "github.com/sagernet/sing-tun"
)

// The Apple client builds NEIPv4Settings.includedRoutes from these getters. They therefore have
// to report exactly what the core will honour, in the spelling the core compares: a mismatched
// spelling installs a host route for an address the core does not answer, which is a silent
// half-working feature rather than an error.

func collectLoopback(t *testing.T, iterator StringIterator) []string {
	t.Helper()
	var values []string
	for iterator.HasNext() {
		values = append(values, iterator.Next())
	}
	return values
}

func newTestTunOptions(inet4, inet6 []netip.Addr) *tunOptions {
	return &tunOptions{Options: &tun.Options{
		Inet4LoopbackAddress: inet4,
		Inet6LoopbackAddress: inet6,
	}}
}

func TestTunOptionsExposeBothLoopbackFamilies(t *testing.T) {
	options := newTestTunOptions(
		[]netip.Addr{netip.MustParseAddr("10.7.0.1")},
		[]netip.Addr{netip.MustParseAddr("fdfe:7::1")},
	)
	inet4 := collectLoopback(t, options.GetInet4LoopbackAddress())
	inet6 := collectLoopback(t, options.GetInet6LoopbackAddress())
	if len(inet4) != 1 || inet4[0] != "10.7.0.1" {
		t.Fatalf("inet4 loopback addresses: %v", inet4)
	}
	if len(inet6) != 1 || inet6[0] != "fdfe:7::1" {
		t.Fatalf("inet6 loopback addresses: %v", inet6)
	}
}

func TestTunOptionsUnmapMappedIPv4(t *testing.T) {
	// The core unmap()s the destination before matching, so reporting "::ffff:10.7.0.1" here
	// would install a route for a spelling that never matches.
	options := newTestTunOptions([]netip.Addr{netip.MustParseAddr("::ffff:10.7.0.1")}, nil)
	inet4 := collectLoopback(t, options.GetInet4LoopbackAddress())
	if len(inet4) != 1 || inet4[0] != "10.7.0.1" {
		t.Fatalf("mapped address was not unmapped: %v", inet4)
	}
}

func TestTunOptionsDropInvalidLoopbackAddresses(t *testing.T) {
	// A zero Addr in the configuration must not become "<nil>" or "invalid IP" on the wire.
	options := newTestTunOptions([]netip.Addr{{}}, nil)
	if inet4 := collectLoopback(t, options.GetInet4LoopbackAddress()); len(inet4) != 0 {
		t.Fatalf("an invalid address was rendered: %v", inet4)
	}
}

func TestTunOptionsStripZones(t *testing.T) {
	options := newTestTunOptions(nil, []netip.Addr{netip.MustParseAddr("fe80::1%en0")})
	inet6 := collectLoopback(t, options.GetInet6LoopbackAddress())
	if len(inet6) != 1 || inet6[0] != "fe80::1" {
		t.Fatalf("a zone survived: %v", inet6)
	}
}

func TestTunOptionsWithNoLoopbackAddressesReportNothing(t *testing.T) {
	options := newTestTunOptions(nil, nil)
	if inet4 := collectLoopback(t, options.GetInet4LoopbackAddress()); len(inet4) != 0 {
		t.Fatalf("inet4: %v", inet4)
	}
	if inet6 := collectLoopback(t, options.GetInet6LoopbackAddress()); len(inet6) != 0 {
		t.Fatalf("inet6: %v", inet6)
	}
}

func TestParseLoopbackAddressesSkipsUnparseableEntries(t *testing.T) {
	parsed := parseLoopbackAddresses(newIterator([]string{"10.7.0.1", "not-an-address", "fdfe:7::1"}))
	if len(parsed) != 2 || parsed[0] != netip.MustParseAddr("10.7.0.1") || parsed[1] != netip.MustParseAddr("fdfe:7::1") {
		t.Fatalf("parsed: %v", parsed)
	}
	if parsed := parseLoopbackAddresses(nil); parsed != nil {
		t.Fatalf("nil iterator produced %v", parsed)
	}
}
