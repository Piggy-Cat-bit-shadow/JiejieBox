package libbox

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/option"
	sbtun "github.com/sagernet/sing-box/protocol/tun"

	"github.com/stretchr/testify/require"
)

// TestConfiguredMappedLoopbackReachesTheCorrectGetter closes the chain the Apple client depends on:
// configuration spelling -> the family split the real TUN inbound uses -> the strings the
// NEIPv4Settings/NEIPv6Settings builders read.
//
// A mapped address in the IPv6 getter is not cosmetic: ExtensionPlatformInterface builds
// NEIPv6Route(destinationAddress:networkPrefixLength:) from that iterator, so an IPv4 literal would
// be handed to the IPv6 route builder while the IPv4 list, which gets the /32 that actually delivers
// the packet, stays empty. The configuration test in protocol/tun proves NewInbound passes the
// configured list through SplitLoopbackAddresses; this test proves that split's output reaches the
// two getters with the same spelling.
func TestConfiguredMappedLoopbackReachesTheCorrectGetter(t *testing.T) {
	var inbound option.TunInboundOptions
	require.NoError(t, json.Unmarshal([]byte(`{
		"loopback_address": ["::ffff:10.7.0.1", "10.7.0.2", "fdfe:7::1"]
	}`), &inbound))
	inet4, inet6 := sbtun.SplitLoopbackAddresses(inbound.LoopbackAddress)
	options := newTestTunOptions(inet4, inet6)

	require.Equal(t, []string{"10.7.0.1", "10.7.0.2"}, collectLoopback(t, options.GetInet4LoopbackAddress()),
		"the Apple IPv4 route builder must receive both IPv4 addresses")
	require.Equal(t, []string{"fdfe:7::1"}, collectLoopback(t, options.GetInet6LoopbackAddress()),
		"the Apple IPv6 route builder must never receive an IPv4 literal")

	// Keep the spelling explicit: the getter is also what a client compares against, so a future
	// change that "helpfully" re-adds the mapped form to the v6 list must fail here.
	for _, address := range collectLoopback(t, options.GetInet6LoopbackAddress()) {
		parsed, err := netip.ParseAddr(address)
		require.NoError(t, err)
		require.False(t, parsed.Unmap().Is4(), "an IPv4 address reached the IPv6 getter: %s", address)
	}
}
