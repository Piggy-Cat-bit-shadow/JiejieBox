package masque

// flowRegistry owns the inner connections this endpoint handed to the router.
//
// # Why the endpoint has to own them
//
// A CONNECT-IP session's inner flows are created by the userspace stack and handed to the router
// through NewConnectionEx. The stack keys them by 4-tuple and has no idea which session carried
// them, so when a session retires nothing closes them: the old flow keeps its socket and its stack
// resources past the session that it belonged to. The address a flow's source shows IS the session's
// identity - the server assigned it from that session's pool - so the endpoint is the only layer
// that can say "these flows belong to the generation that just left".

import (
	"net"
	"net/netip"
	"sync"

	N "github.com/sagernet/sing/common/network"
)

type flowRegistry struct {
	access sync.Mutex
	// flows maps each live inner connection to the inner source address it arrived from.
	flows map[net.Conn]netip.Addr
	// packets is the datagram half, kept separately because the two connection types do not share a
	// concrete type beyond net.Conn.
	packets map[N.PacketConn]netip.Addr
}

func (r *flowRegistry) add(conn net.Conn, source netip.Addr) {
	r.access.Lock()
	if r.flows == nil {
		r.flows = make(map[net.Conn]netip.Addr)
	}
	r.flows[conn] = source
	r.access.Unlock()
}

func (r *flowRegistry) remove(conn net.Conn) {
	r.access.Lock()
	delete(r.flows, conn)
	r.access.Unlock()
}

func (r *flowRegistry) addPacket(conn N.PacketConn, source netip.Addr) {
	r.access.Lock()
	if r.packets == nil {
		r.packets = make(map[N.PacketConn]netip.Addr)
	}
	r.packets[conn] = source
	r.access.Unlock()
}

func (r *flowRegistry) removePacket(conn N.PacketConn) {
	r.access.Lock()
	delete(r.packets, conn)
	r.access.Unlock()
}

// closeFor closes every flow whose inner source lies in addresses and removes it from the registry.
// It returns how many it closed, which is what the release test asserts on.
func (r *flowRegistry) closeFor(addresses []netip.Addr) int {
	if len(addresses) == 0 {
		return 0
	}
	owned := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		owned[address] = struct{}{}
	}
	r.access.Lock()
	var connections []net.Conn
	for conn, source := range r.flows {
		if _, isOwned := owned[source]; isOwned {
			connections = append(connections, conn)
			delete(r.flows, conn)
		}
	}
	var packetConnections []N.PacketConn
	for conn, source := range r.packets {
		if _, isOwned := owned[source]; isOwned {
			packetConnections = append(packetConnections, conn)
			delete(r.packets, conn)
		}
	}
	r.access.Unlock()
	// Close outside the lock: a Close can block on the peer's socket, and the registry must not hold
	// the endpoint's other flows behind it.
	for _, conn := range connections {
		_ = conn.Close()
	}
	for _, conn := range packetConnections {
		_ = conn.Close()
	}
	return len(connections) + len(packetConnections)
}

// closeAll closes every recorded flow. Endpoint teardown uses it so a Close does not leave inner
// sockets behind the stack it is about to release.
func (r *flowRegistry) closeAll() int {
	r.access.Lock()
	var connections []net.Conn
	for conn := range r.flows {
		connections = append(connections, conn)
	}
	r.flows = nil
	var packetConnections []N.PacketConn
	for conn := range r.packets {
		packetConnections = append(packetConnections, conn)
	}
	r.packets = nil
	r.access.Unlock()
	for _, conn := range connections {
		_ = conn.Close()
	}
	for _, conn := range packetConnections {
		_ = conn.Close()
	}
	return len(connections) + len(packetConnections)
}
