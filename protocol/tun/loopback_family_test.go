package tun

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

// TestConfiguredIPv4MappedLoopbackAddressIsAnEffectiveIPv4Address is the configuration -> effective
// TUN option half of the local-signing regression.
//
// The Apple switch injects `10.7.0.1`; when it is OFF the daemon merge is deliberately inert and a
// user's own `::ffff:10.7.0.1` reaches the TUN unchanged. `netip.Addr.Is6()` is true for that
// spelling, so filtering with Is4/Is6 put it in the IPv6 list while sing-tun's matcher unmaps the
// packet destination and looks only in the IPv4 list. The effective options must carry the address
// in the family the stack will compare against.
func TestConfiguredIPv4MappedLoopbackAddressIsAnEffectiveIPv4Address(t *testing.T) {
	var tunOptions option.TunInboundOptions
	require.NoError(t, json.Unmarshal([]byte(`{
		"loopback_address": ["::ffff:10.7.0.1", "10.7.0.2", "fdfe:7::1"]
	}`), &tunOptions))
	require.Len(t, tunOptions.LoopbackAddress, 3, "the configuration spelling must survive parsing")

	inbound, err := NewInbound(newStackTestContext(t), nil, log.NewNOPFactory().Logger(), "tun-in", tunOptions)
	require.NoError(t, err)
	effective, isTun := inbound.(*Inbound)
	require.True(t, isTun)

	require.Equal(t, []netip.Addr{
		netip.MustParseAddr("10.7.0.1"),
		netip.MustParseAddr("10.7.0.2"),
	}, []netip.Addr(effective.tunOptions.Inet4LoopbackAddress),
		"a mapped address must reach the IPv4 list the stack matches against")
	require.Equal(t, []netip.Addr{
		netip.MustParseAddr("fdfe:7::1"),
	}, []netip.Addr(effective.tunOptions.Inet6LoopbackAddress),
		"the IPv6 list must not contain an IPv4 literal")
}

func TestSplitLoopbackAddressesKeepsNormalAndZonedAddresses(t *testing.T) {
	inet4, inet6 := SplitLoopbackAddresses([]netip.Addr{
		netip.MustParseAddr("10.7.0.1"),
		netip.MustParseAddr("fe80::1%en0"),
		netip.MustParseAddr("::ffff:10.7.0.2"),
		{},
	})
	require.Equal(t, []netip.Addr{
		netip.MustParseAddr("10.7.0.1"),
		netip.MustParseAddr("10.7.0.2"),
	}, inet4)
	require.Equal(t, []netip.Addr{
		netip.MustParseAddr("fe80::1"),
	}, inet6)
}
