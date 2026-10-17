package route

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// The route path's half of the bounded failover retry.
//
// # Why the decision is split between here and the group
//
// The route path knows the flow - its metadata, the network it is on - and the group knows
// its members, its strategy and its failure ledger. Neither can make the decision alone:
// the group cannot own the dial without being asked, and the route path cannot choose an
// alternate without the group's selection. The capability interface is where the two meet,
// and this file is the route side of it.
//
// # Why a group without the capability is untouched
//
// routeDialOwner answers nil for every outbound that does not implement the interface AND for
// every group whose configuration did not opt into failover, and both call sites below then
// take exactly the path they took before: the chain is resolved with the same commit flag,
// and the leaf is handed to the connection manager as the dialer. The opt-in group therefore
// does not merely skip its retry - it is routed through the pre-capability code, which is the
// only way an upgrade can promise identical behaviour.

// routeDialOwner reports the group that owns this flow's dial, or nil.
//
// It is consulted BEFORE the chain is resolved, because a group that owns the dial also owns
// the selection the dial commits: resolving with commit=true here would advance the group's
// cursor for a choice the group is about to make again, and every flow would consume two
// rotation slots instead of one.
//
// FailoverEnabled is part of the answer, not an optimization. The capability interface is
// implemented unconditionally, so without this check a configuration that never asked for
// failover would be routed through the preview-and-shim path and would still reach the
// group's dial. Requiring the predicate here means such a configuration resolves with commit
// and dials the resolved leaf exactly as it did before the capability existed - which is the
// compatibility guarantee an upgrade depends on, not merely a retry that happens to make one
// attempt.
func routeDialOwner(outbound adapter.Outbound) adapter.FailoverOutboundGroup {
	if outbound == nil {
		return nil
	}
	failover, isFailover := outbound.(adapter.FailoverOutboundGroup)
	if !isFailover || !failover.FailoverEnabled() {
		return nil
	}
	return failover
}

// routeDialDecision resolves a flow's chain and reports who owns the dial.
//
// This is the whole route-path half of the capability, in one place so the three questions it
// answers cannot drift apart:
//
//   - Does the matched outbound own the dial? If not, the walk commits exactly as it always
//     did and nothing below changes.
//   - Does the flow have more than one candidate address? The connection manager dials each
//     candidate through the SAME dialer, so a group that owned the dial would make a fresh
//     choice per address: after one address failed, the next could go to a different member,
//     and one flow would have taken two. One flow is one member for the flow's whole life, so
//     such a flow keeps the old single-member behaviour and has no failover.
//   - Does the resolved member own the WHOLE connection (adapter.ConnectionHandler)? Then it
//     never returns one to this layer, the capability cannot apply, and the preview that was
//     resolved above would have been this flow's only resolution - so the walk is re-run with
//     commit=true to pay the one decision the flow owes. The member is the one the preview
//     named, which is the invariant the commit flag exists to preserve, so the returned chain
//     is the chain the flow takes.
func routeDialDecision(selected adapter.Outbound, metadata *adapter.InboundContext, network string) ([]adapter.Outbound, adapter.FailoverOutboundGroup, error) {
	owner := routeDialOwner(selected)
	if len(metadata.DestinationAddresses) > 1 {
		owner = nil
	}
	chain, err := resolveOutbound(selected, metadata, network, owner == nil)
	if err != nil {
		return nil, nil, err
	}
	if owner != nil {
		if _, isHandler := chain[len(chain)-1].(adapter.ConnectionHandler); isHandler {
			chain, err = resolveOutbound(selected, metadata, network, true)
			if err != nil {
				return nil, nil, err
			}
			owner = nil
		}
	}
	return chain, owner, nil
}

// routeDialer returns the dialer the connection manager must use for this flow.
//
// Without an owner the leaf is returned unchanged, so the connection manager sees the same
// object it has always seen. With an owner, the leaf is wrapped in a shim that carries the
// leaf's identity and its copy-buffer opt-in across to the group's own dial: the group
// resolves a leaf per attempt, and the shim is what lets this attempt still be described as
// the member the chain named.
func routeDialer(leaf adapter.Outbound, owner adapter.FailoverOutboundGroup, metadata *adapter.InboundContext) N.Dialer {
	if owner == nil {
		return leaf
	}
	return &failoverDialer{Outbound: leaf, group: owner, metadata: metadata}
}

// failoverDialer carries a flow to a failover-capable group.
//
// The embedded outbound is the leaf the chain resolved to for the FIRST attempt. It is not
// what dials: the group does, and it re-resolves the leaf itself for every attempt, so a
// fallback lands on the member the retry chooses rather than on the one this shim was built
// around. The leaf is embedded so that everything the route layer asks the dialer - its tag
// for a log line, its network, whether it opted into early copy-buffer growth - is answered
// by the member the flow was described with.
type failoverDialer struct {
	adapter.Outbound
	group    adapter.FailoverOutboundGroup
	metadata *adapter.InboundContext
}

// The shim's own contract, pinned at compile time.
//
// A typed assertion on an EMBEDDED INTERFACE promotes only what that interface declares, so every capability
// this shim needs beyond `adapter.Outbound` has to be either forwarded by hand or made reachable through
// `Upstream`. The first has already been forgotten twice; the pin makes the second a build fact.
var (
	_ N.Dialer                          = (*failoverDialer)(nil)
	_ dialer.ParallelNetworkDialer      = (*failoverDialer)(nil)
	_ adapter.ConnectionCopyTuner       = (*failoverDialer)(nil)
	_ interface{ Upstream() any }       = (*failoverDialer)(nil)
)

// DialContext hands the whole attempt sequence to the group.
func (d *failoverDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return d.group.DialWithFailover(ctx, d.metadata, network, destination)
}

// EarlyConnectionBufferGrowth forwards the primary leaf's opt-in.
//
// It exists because a typed assertion on an embedded INTERFACE only promotes the methods
// that interface declares, so the capability would otherwise be lost the moment a group
// chose a chained member. Reporting false when the leaf did not opt in is the same answer
// the framework would have reached without this method: use the default threshold.
//
// It describes the FIRST attempt's member. A fallback to a different member may have
// different tuning, and the copy loop's decision is made once the connection exists; using
// the described member's setting is the honest approximation, and it is never worse than
// the default the member would otherwise have received.
func (d *failoverDialer) EarlyConnectionBufferGrowth() bool {
	tuner, isTuner := d.Outbound.(adapter.ConnectionCopyTuner)
	return isTuner && tuner.EarlyConnectionBufferGrowth()
}

// DialParallelNetwork forwards the per-attempt dial surface, which the embedded interface hides.
//
// # The defect this closes
//
// `routeDialer` embeds `adapter.Outbound`, and a typed assertion on an embedded INTERFACE promotes only the
// methods that interface declares. So this shim - which exists so the GROUP owns the failover decision - also
// hid `dialer.ParallelNetworkDialer`, and `dialer.DialSerialNetwork` therefore fell back to its plain serial
// loop:

//	leaf.DialContext(ctx, network, destination)

// a signature with no parameter for `network_strategy`, `network_type`, `fallback_network_type` or
// `fallback_delay`. A rule that set those through `route_options` governed a flow whose outbound was the leaf,
// and did NOT govern the same flow when that outbound was a `failover: true` loadbalance group - while the
// packet path kept them, because it passes the leaf rather than this shim. That is the connected-UDP defect
// with the protocols swapped.
//
// # Why the options go through the context
//
// The group must still own the failover decision, so this cannot dial around it. The options have to reach
// whichever member the group selects, across a recursion where a member may itself be a failover group - which
// is the same problem the flow's attempt budget already solves with a context carrier. `dialMember` reads them
// and dispatches to the member's own parallel surface when it has one.
func (d *failoverDialer) DialParallelNetwork(ctx context.Context, network string, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, networkType []C.InterfaceType, fallbackNetworkType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	ctx = adapter.ContextWithFailoverDialOptions(ctx, adapter.FailoverDialOptions{
		DestinationAddresses: destinationAddresses,
		NetworkStrategy:      strategy,
		NetworkType:          networkType,
		FallbackNetworkType:  fallbackNetworkType,
		FallbackDelay:        fallbackDelay,
	})
	return d.group.DialWithFailover(ctx, d.metadata, network, destination)
}

// ListenSerialNetworkPacket forwards the packet half of the same surface.
//
// The packet flow does not reach this shim in production - the route layer passes the leaf for packet
// connections - so this exists to make the shim's capability honest rather than to change a live path. It
// delegates to the leaf the chain resolved, which is exactly what the packet path does today.
func (d *failoverDialer) ListenSerialNetworkPacket(ctx context.Context, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, networkType []C.InterfaceType, fallbackNetworkType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, netip.Addr, error) {
	if parallel, isParallel := d.Outbound.(dialer.ParallelNetworkDialer); isParallel {
		return parallel.ListenSerialNetworkPacket(ctx, destination, destinationAddresses, strategy, networkType, fallbackNetworkType, fallbackDelay)
	}
	conn, err := d.Outbound.ListenPacket(ctx, destination)
	if err != nil {
		return nil, netip.Addr{}, err
	}
	return conn, destination.Addr, nil
}

// Upstream returns the leaf this shim was built around.
//
// # Why a forwarding hop is not enough
//
// This type embeds `adapter.Outbound`, and a typed assertion on an embedded INTERFACE promotes only the
// methods that interface declares. Two capabilities have already had to be forwarded by hand because of
// that (`EarlyConnectionBufferGrowth`, `DialParallelNetwork`), and an adversarial sweep found eight more
// hidden: `LocalFamilyDialer`, `PacketDialerWithDestination`, `BypassableOutbound`, `ResolveDialer`,
// `FlowOutbound`, the two idle-connection interfaces, `OutboundWithPreferredRoutes` and
// `OutboundWithMultiplex`.
//
// Every one of those losses is unreachable TODAY - the route layer passes the leaf for packet flows, and
// the capabilities it reads from `this` are exactly the forwarded ones - which is why hand-forwarding more
// of them would be churn without a consumer. What is not unreachable is the shape: the next capability
// someone reads from a dialer argument would be hidden again, in the same way, for the third time.
//
// Reporting the leaf on the Upstream protocol fixes the class instead of the instances: `common.Cast` and
// the dialer chain walks see through this hop, so a capability the leaf has is reachable from the shim
// without a forwarding method, and no future interface needs to be remembered here.
func (d *failoverDialer) Upstream() any {
	return d.Outbound
}
