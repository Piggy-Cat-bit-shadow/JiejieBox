package wireguard

import (
	"net/netip"
	"sync/atomic"

	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing-tun/gtcpip/header"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/wireguard-go/device"
)

type peerRouter struct {
	memoryTun    *tun.MemoryTun
	inet4Address netip.Addr
	inet6Address netip.Addr
	routes       atomic.Pointer[peerRoutes]
	// detached is set while no wireguard-go Device is published (a suspended endpoint whose heavy
	// chain was released). Flows that stay in the gVisor stack can still write packets; with no
	// routes they fall through to the memory tun's unrouted handler, which must DROP them rather
	// than answer ICMP: a suspension is a reversible pause, and injecting "no route" into a live
	// flow would turn it into an error instead of a stall the reconnect can recover from.
	detached atomic.Bool
}

type peerRoutes struct {
	allowedIPs *device.AllowedIPs
	queues     map[*device.Peer]*peerQueue
}

type peerQueue struct {
	peer    *device.Peer
	queue   *tun.OutboundQueue
	packets [][]byte
}

func newPeerRouter(options DeviceOptions) *peerRouter {
	router := &peerRouter{}
	router.inet4Address, router.inet6Address = deviceAddresses(options.Address)
	router.memoryTun = tun.NewMemoryTun(tun.MemoryTunOptions{
		MTU:      int(options.MTU),
		Outbound: router.writeUnrouted,
		Route:    router.route,
	})
	return router
}

func (r *peerRouter) setPeers(allowedIPs *device.AllowedIPs, peers []*device.Peer) {
	routes := &peerRoutes{
		allowedIPs: allowedIPs,
		queues:     make(map[*device.Peer]*peerQueue, len(peers)),
	}
	for _, peer := range peers {
		current := &peerQueue{peer: peer}
		current.queue = r.memoryTun.NewOutboundQueue(current.write)
		routes.queues[peer] = current
	}
	// Publish the routes BEFORE clearing the detached flag: a writer that observes "not detached"
	// must be able to find a queue. The reverse order would leave a window where a packet passes
	// the drop check and then finds no route.
	previous := r.routes.Swap(routes)
	r.detached.Store(false)
	if previous != nil {
		previous.close()
	}
}

// clearPeers detaches every peer route. It is the first step of releasing a suspended endpoint's
// heavy chain: after it returns, no packet from a flow that remains in the stack can reach a Peer
// that belongs to the Device about to be closed.
//
// It does not wait for a queue handler that is already executing a batch (sing-tun's
// OutboundQueue.Close has no such join), and it does not need to: the handler only holds Go
// references to a stopped Peer, and Peer.WritePackets on a device that is no longer up stages
// nothing that can be sent. The Device's own Close then waits for every goroutine it owns, so after
// Close returns there is no live reader or writer of that Device at all.
func (r *peerRouter) clearPeers() {
	r.detached.Store(true)
	previous := r.routes.Swap(nil)
	if previous != nil {
		previous.close()
	}
}

func (r *peerRouter) route(packet []byte) *tun.OutboundQueue {
	routes := r.routes.Load()
	if routes == nil {
		return nil
	}
	var source, destination netip.Addr
	switch header.IPVersion(packet) {
	case header.IPv4Version:
		ipv4Header := header.IPv4(packet)
		source, destination = ipv4Header.SourceAddr(), ipv4Header.DestinationAddr()
	case header.IPv6Version:
		ipv6Header := header.IPv6(packet)
		source, destination = ipv6Header.SourceAddr(), ipv6Header.DestinationAddr()
	default:
		return nil
	}
	current := routes.queues[routes.allowedIPs.LookupFromPacket(source, destination, packet)]
	if current == nil {
		return nil
	}
	return current.queue
}

func (r *peerRouter) writeUnrouted(packetBuffers []*buf.Buffer) {
	defer buf.ReleaseMulti(packetBuffers)
	if r.detached.Load() {
		// Suspended: no Device is published, so the write is a stall, not a routing error.
		return
	}
	replies := make([][]byte, 0, len(packetBuffers))
	for _, packetBuffer := range packetBuffers {
		packet := packetBuffer.Bytes()
		source := r.inet4Address
		if header.IPVersion(packet) == header.IPv6Version {
			source = r.inet6Address
		}
		reply, built := tun.BuildICMPError(packet, tun.ICMPErrorNoRoute, source, 0, 0)
		if built {
			replies = append(replies, reply)
		}
	}
	if len(replies) > 0 {
		r.memoryTun.WritePackets(replies)
	}
}

func (r *peerRouter) close() error {
	r.detached.Store(true)
	routes := r.routes.Swap(nil)
	if routes != nil {
		routes.close()
	}
	return r.memoryTun.Close()
}

func (r *peerRoutes) close() {
	for _, current := range r.queues {
		current.queue.Close()
	}
}

func (q *peerQueue) write(packetBuffers []*buf.Buffer) {
	for _, packetBuffer := range packetBuffers {
		q.packets = append(q.packets, packetBuffer.Bytes())
	}
	q.peer.WritePackets(q.packets)
	clear(q.packets)
	q.packets = q.packets[:0]
	buf.ReleaseMulti(packetBuffers)
}
