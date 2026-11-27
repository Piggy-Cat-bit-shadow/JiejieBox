package wireguard

// The UDP ownership wrappers.
//
// # Why they exist
//
// A released heavy chain keeps the stack and its flows but destroys the wireguard-go Device. TCP
// survives that (the transport retransmits and the demand path rebuilds the chain); UDP does not -
// one datagram IS the request - so while a UDP association is open the endpoint must not release.
// The wrappers are how the endpoint knows an association is open: Close is the only moment that
// stops being true, and net.Conn/net.PacketConn give no other hook.
//
// # Why embedding the interface is not enough
//
// Go promotes only the methods of the embedded INTERFACE. The objects this package hands out are
// *tun.GoUDPConn, which additionally implements N.NetPacketConn, N.PacketReadWaiter and
// N.PacketReadWaitCreator. A wrapper embedding bare net.PacketConn hides all of that, and the
// product adapts packet conns through iponly -> bufio, which SELECTS the fast path by type assertion:
// bufio.NewPacketConn returns the object unchanged only when it implements N.NetPacketConn, and
// bufio.CreatePacketReadWaiter looks for the waiter. Hiding the interfaces silently moves every UDP
// flow onto the generic read-buffer/copy path. That regression was caught by independent review and
// is what the capability-preserving variants below prevent.
//
// # The rule
//
// Forward a capability ONLY when the wrapped object really has it. The variants are chosen at
// construction by type assertion, so nothing ever claims a capability the object cannot provide, and
// the association counting is always done by the one wrapper that owns Close.

import (
	"net"
	"sync"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// udpAssociation is the release-boundary token handed to a wrapper. Exactly one release happens no
// matter how often, or how concurrently, Close is called.
type udpAssociation struct {
	endpoint *Endpoint
	once     sync.Once
}

func (a *udpAssociation) release() {
	a.once.Do(func() { a.endpoint.udpAssociations.Add(-1) })
}

// ---------------------------------------------------------------------------------------------
// packet conn side (ListenPacket)
// ---------------------------------------------------------------------------------------------

type udpAssociationPacketConn struct {
	net.PacketConn
	association *udpAssociation
}

func (c *udpAssociationPacketConn) Close() error {
	c.association.release()
	return c.PacketConn.Close()
}

// Upstream exposes the wrapped object to the sing helpers' documented fallback chain. It does not
// transfer ownership: the association is still released by this wrapper's Close.
func (c *udpAssociationPacketConn) Upstream() any { return c.PacketConn }

// ReaderReplaceable is true because the wrapped object is the real source of the data; a copier may
// use it directly, which is what keeps the fast path available for capabilities this file does not
// forward explicitly.
func (c *udpAssociationPacketConn) ReaderReplaceable() bool { return true }

type udpAssociationNetPacketConn struct {
	*udpAssociationPacketConn
	packet N.NetPacketConn
}

func (c *udpAssociationNetPacketConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	return c.packet.ReadPacket(buffer)
}

func (c *udpAssociationNetPacketConn) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	return c.packet.WritePacket(buffer, destination)
}

type udpAssociationWaitPacketConn struct {
	*udpAssociationPacketConn
	waiter N.PacketReadWaiter
}

func (c *udpAssociationWaitPacketConn) InitializeReadWaiter(options N.ReadWaitOptions) (needCopy bool) {
	return c.waiter.InitializeReadWaiter(options)
}

func (c *udpAssociationWaitPacketConn) WaitReadPacket() (*buf.Buffer, M.Socksaddr, error) {
	return c.waiter.WaitReadPacket()
}

type udpAssociationCreatorPacketConn struct {
	*udpAssociationPacketConn
	creator N.PacketReadWaitCreator
}

func (c *udpAssociationCreatorPacketConn) CreateReadWaiter() (N.PacketReadWaiter, bool) {
	return c.creator.CreateReadWaiter()
}

type udpAssociationNetWaitPacketConn struct {
	*udpAssociationNetPacketConn
	waiter N.PacketReadWaiter
}

func (c *udpAssociationNetWaitPacketConn) InitializeReadWaiter(options N.ReadWaitOptions) (needCopy bool) {
	return c.waiter.InitializeReadWaiter(options)
}

func (c *udpAssociationNetWaitPacketConn) WaitReadPacket() (*buf.Buffer, M.Socksaddr, error) {
	return c.waiter.WaitReadPacket()
}

type udpAssociationNetCreatorPacketConn struct {
	*udpAssociationNetPacketConn
	creator N.PacketReadWaitCreator
}

func (c *udpAssociationNetCreatorPacketConn) CreateReadWaiter() (N.PacketReadWaiter, bool) {
	return c.creator.CreateReadWaiter()
}

// wrapUDPAssociationPacket counts one association and returns the richest wrapper the object
// supports, so the product's capability-based selection sees what the object really has.
func wrapUDPAssociationPacket(conn net.PacketConn, endpoint *Endpoint) net.PacketConn {
	association := &udpAssociation{endpoint: endpoint}
	endpoint.udpAssociations.Add(1)
	base := &udpAssociationPacketConn{PacketConn: conn, association: association}
	packet, isPacketConn := conn.(N.NetPacketConn)
	waiter, isWaiter := conn.(N.PacketReadWaiter)
	creator, isCreator := conn.(N.PacketReadWaitCreator)
	switch {
	case isPacketConn && isWaiter:
		return &udpAssociationNetWaitPacketConn{
			udpAssociationNetPacketConn: &udpAssociationNetPacketConn{udpAssociationPacketConn: base, packet: packet},
			waiter:                      waiter,
		}
	case isPacketConn && isCreator:
		return &udpAssociationNetCreatorPacketConn{
			udpAssociationNetPacketConn: &udpAssociationNetPacketConn{udpAssociationPacketConn: base, packet: packet},
			creator:                     creator,
		}
	case isPacketConn:
		return &udpAssociationNetPacketConn{udpAssociationPacketConn: base, packet: packet}
	case isWaiter:
		return &udpAssociationWaitPacketConn{udpAssociationPacketConn: base, waiter: waiter}
	case isCreator:
		return &udpAssociationCreatorPacketConn{udpAssociationPacketConn: base, creator: creator}
	default:
		return base
	}
}

// ---------------------------------------------------------------------------------------------
// conn side (DialContext with network=udp, and any net.Conn handed out by the endpoint)
// ---------------------------------------------------------------------------------------------

type udpAssociationConn struct {
	net.Conn
	association *udpAssociation
}

func (c *udpAssociationConn) Close() error {
	c.association.release()
	return c.Conn.Close()
}

func (c *udpAssociationConn) Upstream() any { return c.Conn }

func (c *udpAssociationConn) ReaderReplaceable() bool { return true }

type udpAssociationNetConn struct {
	*udpAssociationConn
	packet N.NetPacketConn
}

func (c *udpAssociationNetConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	return c.packet.ReadPacket(buffer)
}

func (c *udpAssociationNetConn) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	return c.packet.WritePacket(buffer, destination)
}

func (c *udpAssociationNetConn) ReadFrom(p []byte) (int, net.Addr, error) {
	return c.packet.ReadFrom(p)
}

func (c *udpAssociationNetConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	return c.packet.WriteTo(p, addr)
}

type udpAssociationWaitConn struct {
	*udpAssociationConn
	waiter N.PacketReadWaiter
}

func (c *udpAssociationWaitConn) InitializeReadWaiter(options N.ReadWaitOptions) (needCopy bool) {
	return c.waiter.InitializeReadWaiter(options)
}

func (c *udpAssociationWaitConn) WaitReadPacket() (*buf.Buffer, M.Socksaddr, error) {
	return c.waiter.WaitReadPacket()
}

type udpAssociationCreatorConn struct {
	*udpAssociationConn
	creator N.PacketReadWaitCreator
}

func (c *udpAssociationCreatorConn) CreateReadWaiter() (N.PacketReadWaiter, bool) {
	return c.creator.CreateReadWaiter()
}

type udpAssociationNetWaitConn struct {
	*udpAssociationNetConn
	waiter N.PacketReadWaiter
}

func (c *udpAssociationNetWaitConn) InitializeReadWaiter(options N.ReadWaitOptions) (needCopy bool) {
	return c.waiter.InitializeReadWaiter(options)
}

func (c *udpAssociationNetWaitConn) WaitReadPacket() (*buf.Buffer, M.Socksaddr, error) {
	return c.waiter.WaitReadPacket()
}

type udpAssociationNetCreatorConn struct {
	*udpAssociationNetConn
	creator N.PacketReadWaitCreator
}

func (c *udpAssociationNetCreatorConn) CreateReadWaiter() (N.PacketReadWaiter, bool) {
	return c.creator.CreateReadWaiter()
}

// wrapUDPAssociationConn is the connected-association counterpart; it forwards the same capability
// set, because a connected UDP socket is the same object with a fixed peer.
func wrapUDPAssociationConn(conn net.Conn, endpoint *Endpoint) net.Conn {
	association := &udpAssociation{endpoint: endpoint}
	endpoint.udpAssociations.Add(1)
	base := &udpAssociationConn{Conn: conn, association: association}
	packet, isPacketConn := conn.(N.NetPacketConn)
	waiter, isWaiter := conn.(N.PacketReadWaiter)
	creator, isCreator := conn.(N.PacketReadWaitCreator)
	switch {
	case isPacketConn && isWaiter:
		return &udpAssociationNetWaitConn{
			udpAssociationNetConn: &udpAssociationNetConn{udpAssociationConn: base, packet: packet},
			waiter:                waiter,
		}
	case isPacketConn && isCreator:
		return &udpAssociationNetCreatorConn{
			udpAssociationNetConn: &udpAssociationNetConn{udpAssociationConn: base, packet: packet},
			creator:               creator,
		}
	case isPacketConn:
		return &udpAssociationNetConn{udpAssociationConn: base, packet: packet}
	case isWaiter:
		return &udpAssociationWaitConn{udpAssociationConn: base, waiter: waiter}
	case isCreator:
		return &udpAssociationCreatorConn{udpAssociationConn: base, creator: creator}
	default:
		return base
	}
}
