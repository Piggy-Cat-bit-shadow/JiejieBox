package dialer

import (
	"context"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// The capability walk must not cross a DETOUR.
//
// # The two reasons, and why both are structural
//
// `DetourDialer.Upstream()` is not a statement about a wrapper chain: it calls `Dialer()`, which triggers the
// lazy outbound resolution, and it returns the outbound the detour resolves to. A walk that followed it
// would therefore do two wrong things at once:
//
//	side effect   resolve an outbound as a side effect of merely asking a capability question - the rule
//	              `resolvedDetour` already states for the preflight, broken on the dial path instead
//	wrong answer  what lies below a detour is the PEER's dial path. Any family verdict found there
//	              describes how this machine reaches the proxy, and applying it to a destination address
//	              would refuse connections the peer can serve.
//
// `unwrapDialer` refuses the detour explicitly. This pins both halves: no resolution happens, and the
// answer is "no opinion" rather than the peer's verdict.

// countingOutboundManager records every Outbound() lookup and hands back one outbound.
type countingOutboundManager struct {
	adapter.OutboundManager
	outbound adapter.Outbound
	lookups  atomic.Int32
}

func (m *countingOutboundManager) Outbound(tag string) (adapter.Outbound, bool) {
	m.lookups.Add(1)
	if m.outbound == nil {
		return nil, false
	}
	return m.outbound, true
}

func (m *countingOutboundManager) Default() adapter.Outbound { return m.outbound }

// peerOutbound is the shape a detour resolves to: an outbound that dials on its own behalf, with a
// `*DefaultDialer` underneath it - exactly the object whose family verdict must NOT be applied to a
// destination this machine is not dialling.
type peerOutbound struct {
	adapter.Outbound
	chain N.Dialer
}

func (o *peerOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return o.chain.DialContext(ctx, network, destination)
}

func (o *peerOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return o.chain.ListenPacket(ctx, destination)
}

// DialChain is what a DIRECT outbound reports (protocol/direct.Outbound). It is on the peer so the walk has
// somewhere to go if it were allowed to cross the detour - which is what makes the assertion meaningful.
func (o *peerOutbound) DialChain() N.Dialer { return o.chain }

func TestTheCapabilityWalkDoesNotCrossADetour(t *testing.T) {
	// The peer's own dial path, over a machine that cannot dial IPv6 at all. If the walk crossed the detour,
	// this verdict would be applied to whatever destination the detour was asked to reach.
	leaf := &DefaultDialer{
		network: newNetworkObserver(newFamilySnapshotOnlyManager(
			netcap.StateProvisioned, netcap.StateUnavailable)),
		familyHealth: newFamilyHealth(),
	}
	manager := &countingOutboundManager{outbound: &peerOutbound{chain: leaf}}
	detourDialer := NewDetour(manager, "peer", false)

	// The question a resolve dialer asks of the chain it holds.
	policy := (&resolveDialer{dialer: detourDialer}).dialerLocalFamilyPolicy()
	require.True(t, policy.restrictsNothing(),
		"a detour must answer 'no opinion' about this machine's families: what is below it is how the "+
			"destination gets reached, not how this machine reaches the peer")

	// And the question the bypass asks of the chain it holds.
	require.False(t, (&resolveDialer{dialer: detourDialer}).LocalFamilyBlocked(netip.MustParseAddr("2001:4860:4860::8888")),
		"a destination the peer can serve must never be refused because of a local verdict that does not "+
			"describe the peer's path")

	require.Zero(t, manager.lookups.Load(),
		"and asking a capability question must not resolve the detour: Dialer() triggers the outbound "+
			"lookup, so a walk that followed Upstream() here would do it as a side effect of asking")

	// The health owner must not be found across a detour either - the same reasoning, one capability over.
	_, found := findFamilyHealthOwner(&resolveDialer{dialer: detourDialer})
	require.False(t, found, "a shared health history belongs to the local path, not to the peer's")

	require.Zero(t, manager.lookups.Load(), "still no resolution")
}

// TestTheCapabilityWalkStillCrossesAnOutboundHop is the control: the refusal is specific to a detour.
//
// Without it, a guard that refused every Upstream() hop would pass the test above and silently undo the fix
// that lets a dialer built over a DIRECT outbound reach the family verdict.
func TestTheCapabilityWalkStillCrossesAnOutboundHop(t *testing.T) {
	leaf := &DefaultDialer{
		network: newNetworkObserver(newFamilySnapshotOnlyManager(
			netcap.StateProvisioned, netcap.StateUnavailable)),
		familyHealth: newFamilyHealth(),
	}
	hop := &peerOutbound{chain: leaf}

	policy := (&resolveDialer{dialer: hop}).dialerLocalFamilyPolicy()
	require.False(t, policy.restrictsNothing(),
		"an outbound hop is transparent: the verdict below it is this machine's own dial path")

	require.True(t, (&resolveDialer{dialer: hop}).LocalFamilyBlocked(netip.MustParseAddr("2001:4860:4860::8888")))

	_, found := findFamilyHealthOwner(&resolveDialer{dialer: hop})
	require.True(t, found, "and the health owner is found through it")
}
