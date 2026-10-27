package tun

import (
	"net/netip"
	"testing"

	tun "github.com/sagernet/sing-tun"

	"github.com/stretchr/testify/require"
)

// TestConfigureDNSHijackUsesAnExplicitDNSAddress is the regression for the plain-TUN path:
// dns_mode=hijack with an explicit dns_address used to leave dnsHijackAddress empty because the
// population was gated on len(DNSAddress)==0. The explicit address must be in the list, and the
// port rule must be enabled even when auto_redirect is off.
func TestConfigureDNSHijackUsesAnExplicitDNSAddress(t *testing.T) {
	explicit := netip.MustParseAddr("172.19.0.2")
	explicit6 := netip.MustParseAddr("fdfe:dcba:9876::2")
	inbound := &Inbound{
		tunOptions: tun.Options{
			Inet4Address: []netip.Prefix{netip.MustParsePrefix("172.19.0.1/30")},
			Inet6Address: []netip.Prefix{netip.MustParsePrefix("fdfe:dcba:9876::1/126")},
			DNSAddress:   []netip.Addr{explicit, explicit6},
			DNSMode:      tun.DNSModeHijack,
		},
	}

	inbound.configureDNSHijack()

	require.Equal(t, []netip.Addr{explicit, explicit6}, inbound.dnsHijackAddress,
		"both explicitly configured DNS addresses must be hijacked")
	require.True(t, inbound.dnsHijackByPort,
		"dns_mode=hijack must enable the port rule on a plain TUN, not only under auto_redirect")
}

func TestConfigureDNSHijackDerivesTheAddressWhenNoneIsConfigured(t *testing.T) {
	inbound := &Inbound{
		tunOptions: tun.Options{
			Inet4Address: []netip.Prefix{netip.MustParsePrefix("172.19.0.1/30")},
			DNSMode:      tun.DNSModeHijack,
		},
	}

	inbound.configureDNSHijack()

	require.Equal(t, []netip.Addr{netip.MustParseAddr("172.19.0.2")}, inbound.dnsHijackAddress,
		"the derived next address remains the hijack target when dns_address is unset")
	require.True(t, inbound.dnsHijackByPort)
}

func TestConfigureDNSHijackLeavesNativeModeAlone(t *testing.T) {
	inbound := &Inbound{
		tunOptions: tun.Options{
			Inet4Address: []netip.Prefix{netip.MustParsePrefix("172.19.0.1/30")},
			DNSAddress:   []netip.Addr{netip.MustParseAddr("172.19.0.2")},
			DNSMode:      tun.DNSModeNative,
		},
	}

	inbound.configureDNSHijack()

	require.Empty(t, inbound.dnsHijackAddress,
		"native mode must not turn an explicitly configured DNS server into a hijack destination")
	require.False(t, inbound.dnsHijackByPort)
}
