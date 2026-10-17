package dialer

import (
	"context"
	"net"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// A chain that passes through an OUTBOUND still reaches the dialer underneath it.
//
// # The defect this closes
//
// `localFamilyOwner` and `familyHealthOwner` are unexported names satisfied by exactly one production
// type, `*DefaultDialer`. A type in another package cannot implement an interface with an unexported
// method, so an `adapter.Outbound` could not answer either question - and a resolve dialer built OVER an
// outbound (which `daemon/started_service.go` does for the network-quality and STUN probes) fell through
// to the zero policy and to a scheduler with no history.
//
// The zero policy is the dangerous half: it does not say "unknown", it says "nothing is restricted", so
// the chain behaved as though this machine could dial every family while the dialer below it knew
// otherwise. That is the same silent-capability-loss shape as the rest of this round, one layer out.
//
// # Why the fix is a chain walk rather than an exported hook
//
// The capability was never missing - the chain had no way to say where it was. `protocol/direct.Outbound`
// now reports its dialer through the Upstream protocol the resolve wrappers already implement, and both
// owners are found by walking it. No public API was added and nothing is re-exposed across packages.

// outboundHop is the shape an outbound presents to a dialer built over it: a dialer that forwards to a
// chain, and says so on the package-scoped hop protocol.
type outboundHop struct {
	chain N.Dialer
}

func (h outboundHop) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return h.chain.DialContext(ctx, network, destination)
}

func (h outboundHop) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return h.chain.ListenPacket(ctx, destination)
}

// DialChain is the hop protocol `common/dialer` follows; it is deliberately not `Upstream()`, which
// `common.Cast` walks repo-wide (see the interface doc in default_parallel_network.go).
func (h outboundHop) DialChain() N.Dialer { return h.chain }

// newChainOverHop builds the production chain - leaf over the manager, then the resolve wrapper - and
// puts an outbound-shaped hop in front of it, which is what `NewResolveDialer` sees when it is handed an
// outbound.
//
// The manager is the counting one, which reports IPv4 provisioned and IPv6 unavailable: the machine the
// reported outages were about.
func newChainOverHop(t *testing.T) N.Dialer {
	t.Helper()
	ctx := service.ContextWith[adapter.NetworkManager](context.Background(), newCountingNetworkManager())
	leaf, err := NewDefault(ctx, option.DialerOptions{})
	require.NoError(t, err)
	return NewResolveDialer(ctx, outboundHop{chain: leaf}, false, "", adapter.DNSQueryOptions{}, 0)
}

// TestAFamilyVerdictIsReachedThroughAnOutboundHop is the regression.
func TestAFamilyVerdictIsReachedThroughAnOutboundHop(t *testing.T) {
	chain := newChainOverHop(t)

	address6 := netip.MustParseAddr("2001:4860:4860::8888")
	address4 := netip.MustParseAddr("112.53.55.92")

	require.True(t, chain.(LocalFamilyDialer).LocalFamilyBlocked(address6),
		"the verdict belongs to the leaf, and a hop in between must not hide it: a chain that answers "+
			"'not blocked' here behaves as though this machine can dial a family it does not have")
	require.False(t, chain.(LocalFamilyDialer).LocalFamilyBlocked(address4),
		"and the family the machine does have is untouched")

	// The same walk must find the health owner, or the probe's scheduler starts with no history.
	owner, found := findFamilyHealthOwner(chain)
	require.True(t, found,
		"the shared family health is in the same chain and is found by the same walk")
	require.NotNil(t, owner)
}

// TestTheWalkIsBoundedAndSurvivesACycle keeps the new step from turning a fault into a hang.
func TestTheWalkIsBoundedAndSurvivesACycle(t *testing.T) {
	cyclic := &cyclicHop{}
	cyclic.self = cyclic

	// A dialer built over a chain that points at itself must answer "no owner", not spin.
	require.False(t, func() bool {
		_, found := findFamilyHealthOwner(cyclic)
		return found
	}(),
		"a cyclic chain must report no owner rather than walking forever")

	policy := (&resolveDialer{dialer: cyclic}).dialerLocalFamilyPolicy()
	require.True(t, policy.restrictsNothing(),
		"and the zero policy is the answer, which restricts nothing - the same degradation a chain with "+
			"no capability has always had")
}

// cyclicHop is a dialer whose Upstream returns itself.
type cyclicHop struct {
	ParallelInterfaceDialer
	self *cyclicHop
}

func (h *cyclicHop) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, context.Canceled
}

func (h *cyclicHop) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, context.Canceled
}

func (h *cyclicHop) Upstream() any { return h.self }

// TestAChainOverAHopGetsASchedulerBoundToTheOwner closes the third asker.
//
// `newScheduler` used to assert `d.dialer` directly while the other two lookups walked. For a resolve dialer
// built over an outbound - the network-quality probe - that meant a scheduler with NO health and, through
// `newDualStackScheduler`, no `localFamily` either: on one object the verdict was available to the gate and
// invisible to the plan, so the very candidates the machine cannot dial were raced and refused later.
//
// The assertion is that the scheduler carries the owner's own capture, which is the only way the plan and the
// gate can agree.
func TestAChainOverAHopGetsASchedulerBoundToTheOwner(t *testing.T) {
	chain := newChainOverHop(t)
	resolver, isResolver := chain.(*resolveDialer)
	require.True(t, isResolver, "the chain the probes build is the serial resolve wrapper")

	scheduler := resolver.newScheduler()
	require.NotNil(t, scheduler.health,
		"the shared family health must be found through the hop, or every probe re-learns a broken family")
	require.False(t, scheduler.localFamily.restrictsNothing(),
		"and the scheduler must carry the owner's verdict: a zero policy here races candidates the gate will "+
			"refuse, which is the disagreement this test exists for")
	require.NotZero(t, scheduler.networkGeneration,
		"and the health bucket is the generation the same capture describes")
}
