package dialer

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// isLocalFamilyRefusal reports whether an error is the gate's own refusal rather than a failure from
// the socket layer.
func isLocalFamilyRefusal(err error) bool {
	return errors.Is(err, errLocalIPv4Unavailable) || errors.Is(err, errLocalIPv6Unavailable)
}

// newGateTestDialer builds the smallest DefaultDialer that can answer the family question, with the
// capability INJECTED so the verdict is the test's own rather than this host's.
//
// Injecting it is what makes the test portable and meaningful: on a machine that has IPv6 the
// ungated path would succeed, so a test asserting "an error came back" would be measuring the host.
func newGateTestDialer(ipv4 netcap.FamilyState, ipv6 netcap.FamilyState) *DefaultDialer {
	return &DefaultDialer{
		familyHealth: newFamilyHealth(),
		network:      newNetworkObserver(newCapabilityManager(ipv4, ipv6)),
	}
}

// TestTheInterfaceBoundPacketListenerRefusesAnAbsentFamily pins the gate on the ONE packet entry
// point that creates a socket outside ListenPacket.
//
// # The defect this exists for
//
// `DefaultDialer.ListenPacket` refuses a provably absent family BEFORE any socket exists, and a
// comment in the package says that is what makes the UDP path work at all:
//
//	default_parallel_network.go:161-165
//	    "What makes the loop sufficient is that a refusal happens BEFORE socket creation, in
//	     DefaultDialer.ListenPacket, so a family this machine provably cannot reach never enters
//	     it and the next address - the other family - is tried. The gate is therefore load-bearing
//	     twice over here: it is the fast-fail for a stream and the ONLY thing that can move a
//	     packet listener to another family."
//
// But `ListenSerialInterfacePacket` is a PUBLIC entry point (dialer.go:239) that creates the packet
// socket ITSELF and never calls ListenPacket when it is given a strategy - and the branch that
// comment sits above calls exactly it:
//
//	default_parallel_network.go:167-172
//	    conn, err := parallelDialer.ListenSerialInterfacePacket(ctx, ...)
//
// So for that branch the comment described a gate that was not there. MEASURED before the fix, on a
// machine with no IPv6 addressing:
//
//	ListenPacket                 -> refused before socket creation   (the control)
//	ListenSerialInterfacePacket  -> err=nil, an IPv6 packet socket was created
//
// # Why this is worse for a packet listener than for a stream
//
// There is no racer and no recovery on the packet path: the serial loop's first successfully created
// socket wins OUTRIGHT and is never replaced (default_parallel_network.go:148-154), because racing
// two packet sockets would deliver duplicate datagrams. A stream that picks the wrong family
// reconnects on the next attempt; a packet listener that picks the wrong family is handed to the
// caller and delivers nothing - the reported `open UDP connection to [2606:...]` followed by
// `send peer batch: unreachable network`. The pre-socket refusal is the only thing that can move it
// to the other family, so a missing gate is not a lost optimisation here.
//
// # What is asserted, and what is deliberately NOT
//
// The assertion is the SPECIFIC refusal, not the presence of an error: a call that reached the
// socket layer would also fail on a host with no IPv6 addressing, and accepting any error would not
// see the difference. The refusal is returned before any socket or interface work, which is why
// this test needs no interface finder, no listener and no logger.
//
// The positive control is the gate's own decision for the OTHER family and for the wildcard, rather
// than a second call through the socket path: a call with an IPv4 destination proceeds to
// `selectInterfaces` and needs a fully constructed dialer, so driving it here would test the fixture
// rather than the gate. The control's job is to show the gate is not refusing everything, and
// `hardUndialableError` is what decides that.
func TestTheInterfaceBoundPacketListenerRefusesAnAbsentFamily(t *testing.T) {
	ipv6 := netip.MustParseAddr("2001:db8::1")
	dialer := newGateTestDialer(netcap.StateProvisioned, netcap.StateUnavailable)
	strategy := C.NetworkStrategyDefault

	conn, err := dialer.ListenSerialInterfacePacket(context.Background(), M.SocksaddrFrom(ipv6, 443), &strategy, nil, nil, 0)
	require.ErrorIs(t, err, errLocalIPv6Unavailable,
		"the interface-bound packet listener created a socket for a family this machine provably "+
			"cannot reach. On the packet path that socket is the winner and is never replaced, so "+
			"the only family fallback there is is this refusal")
	require.Nil(t, conn, "no socket may survive a refusal")
	require.Contains(t, err.Error(), "ipv6",
		"the refusal must name the family, so it is not mistaken for a generic bind failure")

	// The control, on the same dialer and through the same decision function: the family that IS
	// present, the wildcard, and a local-scope IPv6 destination must not be refused. Without this the
	// assertion above would pass for a gate that refuses everything.
	require.NoError(t, dialer.captureLocalFamilyPolicy().hardUndialableError(netip.MustParseAddr("192.168.1.1")),
		"an IPv4 destination must not be refused by the IPv6 verdict")
	require.NoError(t, dialer.captureLocalFamilyPolicy().hardUndialableError(netip.IPv6Unspecified()),
		"the unspecified address asks for a wildcard socket and carries no family to judge")
	require.NoError(t, dialer.captureLocalFamilyPolicy().hardUndialableError(netip.MustParseAddr("fd00::1")),
		"a unique-local IPv6 destination is not reached over the public path this verdict describes")

	// And the connected packet entry point, which already had its gate, still refuses identically -
	// so the two entry points are pinned to one answer.
	_, err = dialer.ListenPacket(context.Background(), M.SocksaddrFrom(ipv6, 443))
	require.ErrorIs(t, err, errLocalIPv6Unavailable,
		"the connected packet gate and the interface-bound one must answer identically")
}

// TestTheInterfaceBoundGateAgreesWithTheConnectedOneAcrossEveryScopeClass is the invariant
// `TestLocalFamilyBlockedIsTheSameVerdictTheDialPathUses` states for the bypass path, applied to the
// pair that actually disagreed.
//
// They are two entry points into one decision, and the whole value of the verdict is that every
// consumer answers the same way. Before the fix they did not: a global IPv6 destination was refused
// by ListenPacket and accepted by ListenSerialInterfacePacket, which is precisely the split that
// makes the packet path's only fallback useless.
func TestTheInterfaceBoundGateAgreesWithTheConnectedOneAcrossEveryScopeClass(t *testing.T) {
	dialer := newGateTestDialer(netcap.StateProvisioned, netcap.StateUnavailable)

	for _, destination := range []netip.Addr{
		netip.MustParseAddr("2001:db8::1"), // global IPv6
		netip.MustParseAddr("2409:8a00::1"),
		netip.MustParseAddr("fe80::1"),     // link-local
		netip.MustParseAddr("fd00::1"),     // unique-local
		netip.MustParseAddr("::1"),         // loopback
		netip.MustParseAddr("192.168.1.1"), // IPv4
		netip.MustParseAddr("127.0.0.1"),   // loopback IPv4
		netip.MustParseAddr("169.254.1.1"), // link-local IPv4
	} {
		policy := dialer.captureLocalFamilyPolicy()
		gateRefusal := policy.hardUndialableError(destination)

		// The connected entry point, driven for real. A non-refused destination legitimately
		// proceeds to the socket layer - the scope classes the egress verdict does not apply to are
		// SUPPOSED to be dialable - so that outcome is recorded and the socket closed rather than
		// treated as a failure. What is compared is the DECISION.
		connectedConn, connectedErr := dialer.ListenPacket(context.Background(), M.SocksaddrFrom(destination, 443))
		connectedRefused := connectedConn == nil && isLocalFamilyRefusal(connectedErr)
		if connectedConn != nil {
			_ = connectedConn.Close()
		} else {
			require.Error(t, connectedErr,
				"ListenPacket returned a nil socket and a nil error for %v", destination)
		}

		require.Equal(t, gateRefusal != nil, connectedRefused,
			"the gate's own verdict and the connected entry point disagree for %v: gate=%v listener=%v",
			destination, gateRefusal, connectedErr)

		// The interface-bound entry point must reach the same decision. It is only driven for the
		// rows the gate refuses, because a non-refused row proceeds into socket creation and needs a
		// fully constructed dialer; those rows are covered by the control assertions above.
		if gateRefusal == nil {
			continue
		}
		strategy := C.NetworkStrategyDefault
		conn, err := dialer.ListenSerialInterfacePacket(context.Background(), M.SocksaddrFrom(destination, 443), &strategy, nil, nil, 0)
		require.ErrorIs(t, err, gateRefusal,
			"the interface-bound packet entry point did not apply the gate for %v; it is the entry "+
				"point the package's serial packet loop calls, and the pre-socket refusal is that "+
				"loop's only family fallback", destination)
		require.Nil(t, conn)
	}
}
