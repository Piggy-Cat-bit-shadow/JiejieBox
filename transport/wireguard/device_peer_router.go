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
	// wake is the demand signal for a released chain. A retained flow that writes while detached is
	// producing REAL traffic - it is not a background probe, which would have to open a new flow and
	// therefore pass through the endpoint's own gate - so the first such packet asks the endpoint to
	// rebuild the chain. Without this, only a NEW dial could ever rebuild it, and a flow that was
	// already open stalled forever: the release kept the stack and its flows but left them no path
	// back.
	wake atomic.Pointer[func()]
	// wakePending makes the demand single-flight: a burst of packets from several retained flows
	// must produce one rebuild attempt, not one per packet.
	wakePending atomic.Bool
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
	r.wakePending.Store(false)
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
	r.wakePending.Store(false)
	previous := r.routes.Swap(nil)
	if previous != nil {
		previous.close()
	}
}

// setWake installs the demand callback. It is called once, while the endpoint is being built, before
// any flow can exist.
func (r *peerRouter) setWake(wake func()) {
	r.wake.Store(&wake)
}

// requestWake asks for a rebuild of the released chain, once per release cycle.
//
// The callback runs on its own goroutine on purpose: this is the stack's packet-writing path, and a
// rebuild waits on buildAccess (which a concurrent suspend holds while it detaches the routes). Doing
// that work here would put a device build - or a deadlock behind suspend's own callback - inside the
// path that must stay responsive, so the packet is dropped (the flow retries) and the rebuild happens
// beside it.
func (r *peerRouter) requestWake() {
	wake := r.wake.Load()
	if wake == nil {
		return
	}
	if !r.wakePending.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer r.wakePending.Store(false)
		(*wake)()
	}()
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
		// No Device is published, so the write is a stall, not a routing error. But a stall from a
		// flow that was already open means that flow has demand again - the case the release used to
		// lose, because only a new Dial could rebuild the chain. Ask for the rebuild and drop this
		// batch; the flow's own retransmission is what arrives once the chain is live.
		r.requestWake()
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
