package daemon

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
)

// The loopback merge is the only new behaviour these overrides add, and it is the one that must
// be provably inert when the platform does not ask for it. Each case below is a failure mode
// that would otherwise be found on a device: an address injected while the switch is off, the
// user's own entry dropped, a duplicate host route installed, or the wrong TUN edited.

func tunInbound(addresses ...string) option.Inbound {
	options := &option.TunInboundOptions{AutoRoute: true}
	for _, address := range addresses {
		options.LoopbackAddress = append(options.LoopbackAddress, netip.MustParseAddr(address))
	}
	return option.Inbound{Type: "tun", Options: options}
}

func loopbackOf(t *testing.T, inbound option.Inbound) []netip.Addr {
	t.Helper()
	tunOptions, isTUN := inbound.Options.(*option.TunInboundOptions)
	if !isTUN {
		t.Fatalf("not a TUN inbound: %T", inbound.Options)
	}
	return tunOptions.LoopbackAddress
}

func TestLoopbackOverrideIsInertWhenThePlatformAsksForNothing(t *testing.T) {
	t.Parallel()
	inbound := tunInbound("10.7.0.9")
	options := option.Options{Inbounds: []option.Inbound{inbound}}
	applyTUNOverrides(&options, &OverrideOptions{})
	addresses := loopbackOf(t, options.Inbounds[0])
	if len(addresses) != 1 || addresses[0] != netip.MustParseAddr("10.7.0.9") {
		t.Fatalf("the configuration's own loopback addresses changed: %v", addresses)
	}
}

func TestLoopbackOverrideOffLeavesTheConfigSpellingForTheTunBoundary(t *testing.T) {
	t.Parallel()
	// The switch being off must not edit the profile, including a mapped spelling. Normalising it
	// here would be invisible in the config but would change the early-return contract; the TUN
	// boundary canonicalises for the stack's matcher, which is where the family is decided.
	options := option.Options{Inbounds: []option.Inbound{tunInbound("::ffff:10.7.0.1")}}
	applyTUNOverrides(&options, &OverrideOptions{})
	addresses := loopbackOf(t, options.Inbounds[0])
	if len(addresses) != 1 || addresses[0] != netip.MustParseAddr("::ffff:10.7.0.1") {
		t.Fatalf("the config spelling changed while the switch was off: %v", addresses)
	}
}

func TestLoopbackOverrideAppendsThePlatformAddress(t *testing.T) {
	t.Parallel()
	options := option.Options{Inbounds: []option.Inbound{tunInbound()}}
	applyTUNOverrides(&options, &OverrideOptions{LoopbackAddress: []netip.Addr{netip.MustParseAddr("10.7.0.1")}})
	addresses := loopbackOf(t, options.Inbounds[0])
	if len(addresses) != 1 || addresses[0] != netip.MustParseAddr("10.7.0.1") {
		t.Fatalf("the platform address was not injected: %v", addresses)
	}
}

func TestLoopbackOverridePreservesTheUsersOwnEntries(t *testing.T) {
	t.Parallel()
	options := option.Options{Inbounds: []option.Inbound{tunInbound("10.9.0.1", "fd00::9")}}
	applyTUNOverrides(&options, &OverrideOptions{LoopbackAddress: []netip.Addr{netip.MustParseAddr("10.7.0.1")}})
	addresses := loopbackOf(t, options.Inbounds[0])
	want := []netip.Addr{
		netip.MustParseAddr("10.9.0.1"),
		netip.MustParseAddr("fd00::9"),
		netip.MustParseAddr("10.7.0.1"),
	}
	if len(addresses) != len(want) {
		t.Fatalf("got %v, want %v", addresses, want)
	}
	for index, address := range want {
		if addresses[index] != address {
			t.Fatalf("got %v, want %v", addresses, want)
		}
	}
}

func TestLoopbackOverrideDoesNotDuplicateAnAddressTheUserAlreadyHas(t *testing.T) {
	t.Parallel()
	// The IPv4-mapped spelling is the same address to the core's matcher, so adding it must not
	// produce a second host route for it.
	options := option.Options{Inbounds: []option.Inbound{tunInbound("10.7.0.1")}}
	applyTUNOverrides(&options, &OverrideOptions{LoopbackAddress: []netip.Addr{
		netip.MustParseAddr("::ffff:10.7.0.1"),
		netip.MustParseAddr("10.7.0.1"),
	}})
	addresses := loopbackOf(t, options.Inbounds[0])
	if len(addresses) != 1 {
		t.Fatalf("a duplicate loopback address survived: %v", addresses)
	}
	if addresses[0] != netip.MustParseAddr("10.7.0.1") {
		t.Fatalf("unexpected normalisation: %v", addresses)
	}
}

func TestLoopbackOverrideNormalisesAZonedIPv6Address(t *testing.T) {
	t.Parallel()
	options := option.Options{Inbounds: []option.Inbound{tunInbound()}}
	applyTUNOverrides(&options, &OverrideOptions{LoopbackAddress: []netip.Addr{
		netip.MustParseAddr("fe80::1%en0"),
	}})
	addresses := loopbackOf(t, options.Inbounds[0])
	if len(addresses) != 1 || addresses[0].Zone() != "" {
		t.Fatalf("a zoned address reached the configuration: %v", addresses)
	}
}

func TestLoopbackOverrideWithNoTUNIsANoOp(t *testing.T) {
	t.Parallel()
	direct := option.Inbound{Type: "direct", Options: &option.DirectInboundOptions{}}
	options := option.Options{Inbounds: []option.Inbound{direct}}
	applyTUNOverrides(&options, &OverrideOptions{LoopbackAddress: []netip.Addr{netip.MustParseAddr("10.7.0.1")}})
	if _, isTUN := options.Inbounds[0].Options.(*option.DirectInboundOptions); !isTUN {
		t.Fatalf("the override changed an inbound it does not own: %T", options.Inbounds[0].Options)
	}
}

func TestLoopbackOverrideEditsOnlyTheFirstTUN(t *testing.T) {
	t.Parallel()
	// A second TUN is not a shape the Apple client produces, and silently editing it would change
	// an inbound the platform never asked about. The established behaviour of every override in
	// this function is "the first TUN", and this pins that it did not become "all of them".
	options := option.Options{Inbounds: []option.Inbound{
		tunInbound(),
		tunInbound("10.8.0.1"),
	}}
	applyTUNOverrides(&options, &OverrideOptions{LoopbackAddress: []netip.Addr{netip.MustParseAddr("10.7.0.1")}})
	if addresses := loopbackOf(t, options.Inbounds[0]); len(addresses) != 1 || addresses[0] != netip.MustParseAddr("10.7.0.1") {
		t.Fatalf("first TUN not patched as expected: %v", addresses)
	}
	if addresses := loopbackOf(t, options.Inbounds[1]); len(addresses) != 1 || addresses[0] != netip.MustParseAddr("10.8.0.1") {
		t.Fatalf("second TUN was modified: %v", addresses)
	}
}

func TestOtherOverridesKeepTheirExistingBehaviour(t *testing.T) {
	t.Parallel()
	// Regression guard: the loopback addition must not have changed what auto_redirect and the
	// package lists do, including auto_redirect's dependence on auto_route.
	options := option.Options{Inbounds: []option.Inbound{tunInbound()}}
	applyTUNOverrides(&options, &OverrideOptions{
		AutoRedirect:   true,
		IncludePackage: []string{"com.example.app"},
		ExcludePackage: []string{"com.example.other"},
	})
	tunOptions := options.Inbounds[0].Options.(*option.TunInboundOptions)
	if !tunOptions.AutoRedirect {
		t.Fatal("auto_redirect was not applied to an auto_route TUN")
	}
	if len(tunOptions.IncludePackage) != 1 || tunOptions.IncludePackage[0] != "com.example.app" {
		t.Fatalf("include package list changed: %v", tunOptions.IncludePackage)
	}
	if len(tunOptions.ExcludePackage) != 1 || tunOptions.ExcludePackage[0] != "com.example.other" {
		t.Fatalf("exclude package list changed: %v", tunOptions.ExcludePackage)
	}
	if len(tunOptions.LoopbackAddress) != 0 {
		t.Fatalf("loopback addresses were injected without a request: %v", badoption.Listable[netip.Addr](tunOptions.LoopbackAddress))
	}
}
