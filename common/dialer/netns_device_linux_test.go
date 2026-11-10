//go:build linux

package dialer

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/database64128/tfo-go/v2"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// The device acceptance for §D: a dial into a named network namespace must reach a global IPv6
// target that only that namespace can reach.
//
// # What this proves that the shared tests cannot
//
// The shared tests verify the DECISION (a netns dial discards the host family verdict). This one
// creates real namespaces, dials a real socket and checks where the socket lives:
//
//  1. the host path still refuses the target with the family verdict (the gate is real),
//  2. the namespaced dial succeeds, and its LOCAL address is the address assigned inside the target
//     namespace - proof the socket was created there, not in the host,
//  3. a namespace without the route fails with a real socket error, not the host verdict - proof
//     the success came from the namespace's own routing.
//
// It is skipped unless the environment names the namespaces and the target, so it costs nothing on
// Windows or on a Linux box without the fixture. The setup recipe:
//
//	sudo ip netns add t09peer
//	sudo ip netns add t09target
//	sudo ip netns add t09empty
//	sudo ip link add veth-peer type veth peer name veth-target
//	sudo ip link set veth-target netns t09target
//	sudo ip link set veth-peer netns t09peer
//	sudo ip -n t09peer addr add 2001:db8:99::1/64 dev veth-peer
//	sudo ip -n t09target addr add 2001:db8:99::2/64 dev veth-target
//	sudo ip -n t09peer link set veth-peer up
//	sudo ip -n t09target link set veth-target up
//	sudo ip -n t09target -6 route add default via 2001:db8:99::1 dev veth-target
//	# listener in t09peer on [2001:db8:99::1]:9000 that answers "pong"
//	T09_TARGET='[2001:db8:99::1]:9000' T09_NETNS_TARGET=t09target T09_NETNS_EMPTY=t09empty \
//	  ./dialer.test -test.run TestNetnsDeviceDialsAGlobalIPv6OnlyTheNamespaceHas -test.v
func TestNetnsDeviceDialsAGlobalIPv6OnlyTheNamespaceHas(t *testing.T) {
	target := os.Getenv("T09_TARGET")
	netnsTarget := os.Getenv("T09_NETNS_TARGET")
	netnsEmpty := os.Getenv("T09_NETNS_EMPTY")
	if target == "" || netnsTarget == "" || netnsEmpty == "" {
		t.Skip("set T09_TARGET, T09_NETNS_TARGET and T09_NETNS_EMPTY (recipe in the test header)")
	}
	destination := M.ParseSocksaddr(target)
	require.True(t, destination.IsValid(), "invalid target %q", target)
	require.True(t, destination.Addr.Is6() && destination.Addr.IsGlobalUnicast(),
		"the target must be a global IPv6 address, got %s", destination)

	// 1. The host path refuses with the family verdict. This is the control that makes the
	//    namespaced success meaningful: without it, success could just mean the gate never ran.
	host := netnsDeviceDialer("")
	_, err := host.DialContext(context.Background(), N.NetworkTCP, destination)
	require.ErrorIs(t, err, errLocalIPv6EgressUnavailable,
		"a host dial must still refuse what the host proved it cannot reach")

	// 2. The namespaced dial reaches the target, and the socket carries the namespace's address.
	namespaced := netnsDeviceDialer(netnsTarget)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := namespaced.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err, "the namespace can reach a target the host sample refuses")
	defer conn.Close()
	t.Logf("connected %s -> %s", conn.LocalAddr(), conn.RemoteAddr())
	require.Contains(t, conn.LocalAddr().String(), "2001:db8:99::2",
		"the socket must be created inside the target namespace, with the address assigned there")

	_, err = conn.Write([]byte("ping"))
	require.NoError(t, err)
	buffer := make([]byte, 4)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, err = io.ReadFull(conn, buffer)
	require.NoError(t, err)
	require.Equal(t, "pong", string(buffer), "business data must flow over the namespaced socket")

	// 3. A namespace without the route fails at the socket, not at the host verdict.
	empty := netnsDeviceDialer(netnsEmpty)
	_, err = empty.DialContext(ctx, N.NetworkTCP, destination)
	require.Error(t, err)
	require.NotErrorIs(t, err, errLocalIPv6EgressUnavailable,
		"the empty namespace must fail on its own routing, not on the host's family verdict")
}

// netnsDeviceDialer is a real direct dialer whose family policy comes from the reported machine
// (IPv6 addressing present, no public IPv6 egress).
func netnsDeviceDialer(netns string) *DefaultDialer {
	return &DefaultDialer{
		network: newNetworkObserver(windowsLinkLocalOnly()),
		netns:   netns,
		dialer4: tfo.Dialer{Dialer: net.Dialer{Timeout: 3 * time.Second}, DisableTFO: true},
		dialer6: tfo.Dialer{Dialer: net.Dialer{Timeout: 3 * time.Second}, DisableTFO: true},
	}
}

// TestNetnsDeviceMirrorHostReachableNamespaceNot is the reverse reachability direction.
//
// The forward direction (above) proves a namespace can reach what the host cannot. This one proves
// the failure in the other direction comes from the NAMESPACE's own routing, not from the host
// policy: the host completes a real connection to the target, and the same target from a namespace
// without a route fails with a socket routing error (ENETUNREACH/EHOSTUNREACH).
//
//	T09_HOST_TARGET   host-reachable address:port (e.g. 10.98.0.1:9001)
//	T09_NETNS_MIRROR  a namespace with no route to it (e.g. t09rmirror)
func TestNetnsDeviceMirrorHostReachableNamespaceNot(t *testing.T) {
	hostTarget := os.Getenv("T09_HOST_TARGET")
	mirrorNetns := os.Getenv("T09_NETNS_MIRROR")
	if hostTarget == "" || mirrorNetns == "" {
		t.Skip("set T09_HOST_TARGET and T09_NETNS_MIRROR (recipe in the test header)")
	}
	destination := M.ParseSocksaddr(hostTarget)
	require.True(t, destination.IsValid(), "invalid target %q", hostTarget)

	// 1. The host reaches the target and completes business: the target is genuinely live.
	host := netnsDeviceDialer("")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hostConn, err := host.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err, "the host must reach the mirror target")
	t.Logf("host connected %s -> %s", hostConn.LocalAddr(), hostConn.RemoteAddr())
	_, err = hostConn.Write([]byte("ping"))
	require.NoError(t, err)
	buffer := make([]byte, 4)
	require.NoError(t, hostConn.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, err = io.ReadFull(hostConn, buffer)
	require.NoError(t, err)
	require.Equal(t, "pong", string(buffer))
	require.NoError(t, hostConn.Close())

	// 2. The same target from a namespace with no route must fail at the socket.
	mirrored := netnsDeviceDialer(mirrorNetns)
	_, err = mirrored.DialContext(ctx, N.NetworkTCP, destination)
	require.Error(t, err, "the mirror namespace must not reach the host target")
	var operationError *net.OpError
	require.ErrorAs(t, err, &operationError)
	require.True(t, errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EHOSTUNREACH),
		"the failure must be the namespace's routing, got %v", err)
	require.NotErrorIs(t, err, errLocalIPv6EgressUnavailable,
		"the refusal must come from the socket, not from the host family verdict")
}
