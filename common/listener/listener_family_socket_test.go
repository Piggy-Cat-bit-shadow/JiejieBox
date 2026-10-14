package listener

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This file proves the family contract against REAL sockets, not only the string mapping.
//
// # Why the collision is not asserted here
//
// The dual-stack collision - the v4 wildcard failing to bind because the v6 socket already answers it -
// only occurs where `net.ipv6.bindv6only` is 0, which is the Linux default. On Windows and on the BSDs
// the default is v6-only, so binding the v6 wildcard first leaves the v4 wildcard free and the
// collision simply does not happen. MEASURED on this repository's Windows host: `"udp"` on `[::]:P`
// followed by `"udp4"` on `0.0.0.0:P` SUCCEEDS.
//
// A test asserting the collision therefore cannot run here without either asserting something false
// or passing for the wrong reason. It lives in `listener_family_collision_linux_test.go`, behind a
// Linux build tag, which is the convention this repository already uses for platform-specific tests.
//
// What this file asserts instead is the platform-NEUTRAL half: when the network name selects a family
// explicitly, the socket really is confined to that family. That property is what makes the Linux
// collision impossible, and unlike the collision it can be checked on every platform.

// wildcardUDPPort returns a port that was free a moment ago, so the binds below are not pre-broken.
func wildcardUDPPort(t *testing.T) int {
	t.Helper()
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	require.NoError(t, err)
	port := probe.LocalAddr().(*net.UDPAddr).Port
	require.NoError(t, probe.Close())
	return port
}

// TestExplicitFamilySocketsAreConfinedToTheirFamily is the invariant the fix exists to create.
//
// An explicit "udp6" socket must not deliver IPv4 traffic, and an explicit "udp4" socket must not
// deliver IPv6 traffic. This is true on Windows because the platform default is v6-only, and it is true
// on Linux because the NAME says so - which is precisely the point: the fix stops relying on a platform
// default and states the family in the network name.
//
// The read half matters. A socket can bind successfully and still be dual-stack, so asserting the bind
// alone would not prove the family is exclusive.
func TestExplicitFamilySocketsAreConfinedToTheirFamily(t *testing.T) {
	port := wildcardUDPPort(t)
	var listenConfig net.ListenConfig
	ctx := context.Background()

	require.Equal(t, "udp6", NetworkWithAddressFamily("udp", netip.IPv6Unspecified()))
	require.Equal(t, "udp4", NetworkWithAddressFamily("udp", netip.IPv4Unspecified()))

	ipv6Conn, err := listenConfig.ListenPacket(ctx, "udp6",
		netip.AddrPortFrom(netip.IPv6Unspecified(), uint16(port)).String())
	if err != nil {
		t.Skipf("SKIP (not a pass): no usable IPv6 stack on this host: %v", err)
	}
	defer ipv6Conn.Close()

	ipv4Conn, err := listenConfig.ListenPacket(ctx, "udp4",
		netip.AddrPortFrom(netip.IPv4Unspecified(), uint16(port)).String())
	require.NoError(t, err, "an explicit udp4 wildcard must bind even while udp6 holds the same port")
	defer ipv4Conn.Close()

	// Address the v4 wildcard on its loopback and confirm only the v4 socket sees it.
	sender, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	require.NoError(t, err)
	defer sender.Close()
	_, err = sender.Write([]byte{0x01})
	require.NoError(t, err)

	require.NoError(t, ipv4Conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	buffer := make([]byte, 16)
	_, _, err = ipv4Conn.ReadFrom(buffer)
	require.NoError(t, err, "the udp4 socket must receive IPv4 traffic addressed to it")

	require.NoError(t, ipv6Conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, _, err = ipv6Conn.ReadFrom(buffer)
	require.Error(t, err,
		"the udp6 socket must NOT receive IPv4 traffic. Receiving it would mean the socket is "+
			"dual-stack, which is the state the explicit network name exists to prevent")
}

// TestWildcardListenerReportsItsOwnFamily is the readability half: the family a socket took must be
// visible from the address it reports, so an operator can see which one is bound.
func TestWildcardListenerReportsItsOwnFamily(t *testing.T) {
	port := wildcardUDPPort(t)
	var listenConfig net.ListenConfig

	ipv6Conn, err := listenConfig.ListenPacket(context.Background(),
		NetworkWithAddressFamily("udp", netip.IPv6Unspecified()),
		netip.AddrPortFrom(netip.IPv6Unspecified(), uint16(port)).String())
	if err != nil {
		t.Skipf("SKIP (not a pass): no usable IPv6 stack on this host: %v", err)
	}
	defer ipv6Conn.Close()
	local, ok := ipv6Conn.LocalAddr().(*net.UDPAddr)
	require.True(t, ok)
	require.True(t, local.IP.IsUnspecified(), "the v6 wildcard must report as unspecified")
	require.Nil(t, local.IP.To4(), "and must not be reported as an IPv4 address")
}
