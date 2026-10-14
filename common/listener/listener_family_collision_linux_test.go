//go:build linux

package listener

import (
	"context"
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// The collision this file asserts only exists where the platform lets the v6 wildcard answer v4.
//
// # Why it is a Linux test
//
// Go creates a socket for network "udp" on "::" without setting IPV6_V6ONLY, so the kernel default
// decides. On Linux that default is 0 - `net.ipv6.bindv6only=0` - and the socket is DUAL-STACK: it
// accepts IPv4 traffic, and it makes the v4 wildcard on the same port unbindable. On Windows and the
// BSDs the default is v6-only, so the collision does not arise and a test written here would pass
// vacuously. MEASURED on this repository's Windows host: "udp" on [::]:P then "udp4" on 0.0.0.0:P
// both succeed.
//
// The build tag is not a convenience. Without it the test would run on Windows, pass because the
// platform hides the defect, and be read as evidence that the defect is fixed - which is the exact
// failure mode the explicit-family fix is about.

// TestDualStackWildcardCollidesAndExplicitFamilyDoesNot is the regression for the operator-visible
// symptom: a v4 listener and a v6 listener could not be configured side by side.
//
// It has three parts, and the first is what makes the other two meaningful: it demonstrates that this
// host really does default to dual-stack. Without that precondition the test could pass on a host where
// the collision is impossible, and prove nothing.
func TestDualStackWildcardCollidesAndExplicitFamilyDoesNot(t *testing.T) {
	port := wildcardUDPPort(t)
	ctx := context.Background()
	var listenConfig net.ListenConfig

	// --- part 1: establish the precondition, and never skip past it silently -------------
	dualStack, err := listenConfig.ListenPacket(ctx, "udp",
		netip.AddrPortFrom(netip.IPv6Unspecified(), uint16(port)).String())
	if err != nil {
		t.Skipf("SKIP (not a pass): cannot bind the IPv6 wildcard on this host: %v", err)
	}
	_, collisionErr := listenConfig.ListenPacket(ctx, "udp4",
		netip.AddrPortFrom(netip.IPv4Unspecified(), uint16(port)).String())
	require.Error(t, collisionErr,
		"precondition: this host must default to DUAL-STACK for 'udp' on '::', otherwise this test "+
			"cannot observe the defect and must not be treated as evidence. Set "+
			"net.ipv6.bindv6only=0, or accept that the Linux default is what this asserts")
	dualStack.Close()

	// --- part 2: the fixed behaviour, on the same port ------------------------------------
	ipv6Conn, err := listenConfig.ListenPacket(ctx,
		NetworkWithAddressFamily("udp", netip.IPv6Unspecified()),
		netip.AddrPortFrom(netip.IPv6Unspecified(), uint16(port)).String())
	if err != nil {
		t.Skipf("SKIP (not a pass): cannot bind udp6 on this host: %v", err)
	}
	defer ipv6Conn.Close()

	ipv4Conn, err := listenConfig.ListenPacket(ctx,
		NetworkWithAddressFamily("udp", netip.IPv4Unspecified()),
		netip.AddrPortFrom(netip.IPv4Unspecified(), uint16(port)).String())
	require.NoError(t, err,
		"with the family stated explicitly the IPv4 wildcard MUST bind on the same port the IPv6 "+
			"wildcard holds. This is the regression: before the fix it failed with 'address already "+
			"in use' and a v4 listener could not coexist with a v6 listener at all")
	defer ipv4Conn.Close()

	// --- part 3: and the kernel must agree the socket is v6-only ---------------------------
	// Reading IPV6_V6ONLY back is the direct statement of the contract, and it is stronger than
	// inferring exclusivity from a successful bind.
	rawConn, err := ipv6Conn.(*net.UDPConn).SyscallConn()
	require.NoError(t, err)
	var v6Only int
	controlErr := rawConn.Control(func(fd uintptr) {
		v6Only, err = unix.GetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_V6ONLY)
	})
	require.NoError(t, controlErr)
	require.NoError(t, err)
	require.Equal(t, 1, v6Only,
		"the udp6 socket must have IPV6_V6ONLY set to 1. A 0 here means the socket is dual-stack "+
			"despite the explicit network name, and the v4 wildcard would only appear to be independent")
}
