package dialer

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// The same question, asked of every path that can carry a literal destination.
//
// # Why a matrix and not three tests in three files
//
// The repository has paid for this twice already: a strict family policy was applied to the stream path
// and not to the packet candidate path, and then to the packet candidate path and not to the packet
// literal entry point. Each repair was correct, and each was written for the path that had been reported.
// A test in the file that owns the newest fix cannot notice the path nobody remembered.
//
// So the entry points are driven side by side, on the same chain and the same call: if one of them stops
// asking the question, this file fails and names which one.
//
//	stream, TCP                DialSerialNetwork(N.NetworkTCP)
//	stream, connected UDP      DialSerialNetwork(N.NetworkUDP)   (route/conn.go's UDPConnect arm)
//	unconnected, candidates    ListenSerialNetworkPacket
//	unconnected, no candidates the LEAF's ListenPacket         (route/conn.go's last arm)
//
// The third column is the leaf rather than the wrapper because that is where the gate lives: the wrapper
// forwards a destination with no candidates straight down. It is asserted through the same machine object
// and the same destination as the other two, so the three answers are comparable even though the code that
// produces them is different.
//
// The three differ in what they can DO about a refusal - the stream races candidates, the packet walk falls
// through, the literal entry point has nothing else to try - but they must never differ in what the answer
// IS.

// pathEntryPoints is the matrix's first three columns.
//
// The two DialSerialNetwork entries are NOT the same path: `route/conn.go` reaches it once for a stream and
// once for connected UDP, and the connected arm was itself the subject of a defect in which the whole
// per-connection network configuration was dropped for one transport and not the other. A matrix that drove
// only TCP would inherit exactly that blind spot.
func pathEntryPoints(chain *parallelProductionChain, port uint16) map[string]func(ctx context.Context, address netip.Addr) (bool, error) {
	streamColumn := func(network string) func(ctx context.Context, address netip.Addr) (bool, error) {
		return func(ctx context.Context, address netip.Addr) (bool, error) {
			before := len(chain.tried())
			conn, err := chain.dialSerialNetwork(ctx, network, M.SocksaddrFrom(address, port), []netip.Addr{address})
			if conn != nil {
				conn.Close()
			}
			return len(chain.tried()) > before, err
		}
	}
	return map[string]func(ctx context.Context, address netip.Addr) (bool, error){
		"stream TCP":           streamColumn(N.NetworkTCP),
		"stream connected UDP": streamColumn(N.NetworkUDP),
		"unconnected with candidates": func(ctx context.Context, address netip.Addr) (bool, error) {
			before := len(chain.tried())
			conn, _, err := chain.listenSerialNetworkPacket(ctx, M.SocksaddrFrom(address, port), []netip.Addr{address})
			if conn != nil {
				conn.Close()
			}
			return len(chain.tried()) > before, err
		},
	}
}

// TestAStrictFamilyPolicyIsAppliedOnEveryPath is the policy row of the matrix, all three columns.
func TestAStrictFamilyPolicyIsAppliedOnEveryPath(t *testing.T) {
	excluded := netip.MustParseAddr("2001:db8::2c")
	admitted := netip.MustParseAddr("192.0.2.92")
	const port uint16 = 443

	// The machine is dual-stack, so only the POLICY can exclude anything: a capability explanation would
	// make this test pass for the wrong reason.
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv4Only)
	chain.inner.packetConn = &countingPacketConn{}
	chain.inner.stall = nil
	chain.inner.connectDelay = 0

	for name, entryPoint := range pathEntryPoints(chain, port) {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			dialled, err := entryPoint(ctx, excluded)
			require.Error(t, err,
				"%s reached the transport for a destination the policy forbids: the answer a flow gets must "+
					"not depend on which entry point it took", name)
			require.False(t, dialled, "%s must not attempt the excluded literal at all", name)
			require.Contains(t, err.Error(), "excluded by strategy",
				"%s must name the policy rather than reporting a network failure", name)

			// The control is per path, in the same subtest, so a path that refuses everything cannot pass
			// by refusing the forbidden address too.
			dialled, err = entryPoint(ctx, admitted)
			require.NoErrorf(t, err, "%s must carry the family the policy admits", name)
			require.Truef(t, dialled, "%s did not reach the transport for an admitted destination", name)
		})
	}

	// Column three: the same chain's literal arm. `resolveDialer.ListenPacket` is what the route layer's
	// last arm calls, and it is the entry point this row of the matrix was written for.
	t.Run("unconnected without candidates", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		before := len(chain.tried())
		conn, err := chain.resolver.ListenPacket(ctx, M.SocksaddrFrom(excluded, port))
		if conn != nil {
			conn.Close()
		}
		require.Error(t, err, "the literal arm must apply the same policy")
		require.Contains(t, err.Error(), "excluded by strategy")
		require.Len(t, chain.tried(), before, "and it must refuse before a socket exists")

		conn, err = chain.resolver.ListenPacket(ctx, M.SocksaddrFrom(admitted, port))
		require.NoError(t, err, "and must carry the admitted family")
		require.NotNil(t, conn)
		conn.Close()
		require.Len(t, chain.tried(), before+1, "reaching the transport exactly once")
	})
}

// TestALocalFamilyFastFailIsAppliedOnEveryPath is the capability row of the same matrix.
//
// It is the reported Windows outage in matrix form: IPv6 exists on the link and no global IPv6 path
// exists, so every path must refuse a global IPv6 literal BEFORE a socket is created - while leaving the
// LAN, the link-local peer and public IPv4 alone. Enlarging the public failure into the LAN is the classic
// over-fix and is asserted against on every path.
//
// # Which layer enforces this, per path - the matrix's real content
//
// The family verdict is enforced at THREE different layers depending on the path, and that layering is
// exactly why one path at a time kept being missed:
//
//	stream                       the candidate scheduler filters before any attempt exists
//	unconnected with candidates  the LEAF's packet gate, because the serial walk has no scheduler
//	unconnected no candidates    the LEAF's literal gate
//
// A fixture whose leaf is a fake therefore shows the stream refusing and the other two accepting - which
// is what this test found when it was first written, and why the last two columns are asserted on a real
// `DefaultDialer` instead of on the harness's stub. They are the SAME machine object and the same
// destination, so the answers are comparable across all three.
func TestALocalFamilyFastFailIsAppliedOnEveryPath(t *testing.T) {
	globalIPv6 := netip.MustParseAddr("2409:8a00:1234::1")
	lanIPv6 := netip.MustParseAddr("fd00:1234::5")
	linkLocalIPv6 := netip.MustParseAddr("fe80::1")
	publicIPv4 := netip.MustParseAddr("93.184.216.34")
	const port uint16 = 443

	// ONE machine description, exactly as the outage report describes it: addressing of both families, no
	// working global IPv6 egress.
	machine := windowsLinkLocalOnly()

	// The stream column: the wrapper's scheduler must filter the candidate.
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyAsIS)
	chain.stored.owner.network = newNetworkObserver(machine)
	chain.stored.owner.familyHealth = newFamilyHealth()
	chain.inner.packetConn = &countingPacketConn{}
	chain.inner.stall = nil
	chain.inner.connectDelay = 0

	// The leaf columns: the production object whose gate the packet paths rely on.
	leaf := &DefaultDialer{network: newNetworkObserver(machine), familyHealth: newFamilyHealth()}

	t.Run("stream", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		dialled, err := pathEntryPoints(chain, port)["stream TCP"](ctx, globalIPv6)
		require.Error(t, err, "the scheduler must refuse a global IPv6 destination on this machine")
		require.False(t, dialled, "and must refuse before any attempt exists")

		for _, allowed := range []netip.Addr{lanIPv6, linkLocalIPv6, publicIPv4} {
			dialled, err = pathEntryPoints(chain, port)["stream TCP"](ctx, allowed)
			require.NoErrorf(t, err, "the scheduler must still carry %s", allowed)
			require.Truef(t, dialled, "the scheduler did not reach the transport for %s", allowed)
		}
	})

	// The two packet columns: refused through the production call, which returns before a socket exists.
	t.Run("unconnected with candidates", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		conn, err := leaf.ListenSerialInterfacePacket(ctx, M.SocksaddrFrom(globalIPv6, port), nil, nil, nil, 0)
		if conn != nil {
			conn.Close()
		}
		require.Error(t, err, "the packet walk has no scheduler, so the leaf gate is the only refusal it has")
		require.ErrorIs(t, err, errLocalIPv6EgressUnavailable,
			"and it must give the EGRESS reason: this machine HAS ipv6, and reporting the family reason "+
				"would send the user hunting for an address that is right there")

		for _, allowed := range []netip.Addr{lanIPv6, linkLocalIPv6, publicIPv4} {
			require.NoErrorf(t, leaf.captureLocalFamilyPolicy().hardUndialableError(allowed),
				"the leaf must not refuse %s", allowed)
		}
	})

	t.Run("unconnected without candidates", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		conn, err := leaf.ListenPacket(ctx, M.SocksaddrFrom(globalIPv6, port))
		if conn != nil {
			conn.Close()
		}
		require.ErrorIs(t, err, errLocalIPv6EgressUnavailable)

		for _, allowed := range []netip.Addr{lanIPv6, linkLocalIPv6, publicIPv4} {
			require.NoErrorf(t, leaf.captureLocalFamilyPolicy().hardUndialableError(allowed),
				"the leaf must not refuse %s", allowed)
		}
	})
}
