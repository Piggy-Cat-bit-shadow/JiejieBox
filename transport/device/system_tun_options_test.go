package device

import (
	"context"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

// A system VPN device (WireGuard, OpenVPN, Tailscale, ...) must not claim the shared iproute2 table
// and rule priority. It has no auto_route of its own, and two such devices previously collided on
// table 2022 while their IPv6 interface rules resolved in a table they did not populate. Upstream
// removed these defaults from the system device; the mapping is asserted here so they cannot come
// back silently.
func TestSystemDeviceDoesNotClaimRouteTable(t *testing.T) {
	device := &systemDevice{options: Options{
		Context: context.Background(),
		Name:    "tun9",
		MTU:     1500,
		Configuration: Configuration{
			Address: []netip.Prefix{netip.MustParsePrefix("fdfe:dcba:9876::1/64")},
		},
	}}
	tunOptions := device.buildTunOptions()
	require.Zero(t, tunOptions.IPRoute2TableIndex)
	require.Zero(t, tunOptions.IPRoute2RuleIndex)
	require.Equal(t, []netip.Prefix{netip.MustParsePrefix("fdfe:dcba:9876::1/64")}, tunOptions.Inet6Address)
}
