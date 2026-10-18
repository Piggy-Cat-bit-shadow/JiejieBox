package mieru

import (
	"net"
	"time"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// inboundPacketConn is the server side of a Mieru UDP association.
//
// The SDK hands the inbound a stream (a Mieru session) whose payload is the
// framed, SOCKS5-addressed datagram encoding produced by the client. The
// router consumes N.PacketConn; this type bridges the two, preserving the
// per-datagram destination so an association can retarget between packets.
type inboundPacketConn struct {
	conn   net.Conn
	framer *uotFramer
}

var (
	_ N.PacketConn    = (*inboundPacketConn)(nil)
	_ N.NetPacketConn = (*inboundPacketConn)(nil)
)

func newInboundPacketConn(conn net.Conn) *inboundPacketConn {
	return &inboundPacketConn{conn: conn, framer: newUOTFramer(conn)}
}

func (c *inboundPacketConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	n, destination, err := c.framer.readFrame(buffer.FreeBytes())
	if err != nil {
		return M.Socksaddr{}, err
	}
	buffer.Truncate(n)
	return destination, nil
}

func (c *inboundPacketConn) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	return writeUOTFrame(c.conn, buffer.Bytes(), destination)
}

func (c *inboundPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, destination, err := c.framer.readFrame(p)
	if err != nil {
		return n, nil, err
	}
	return n, destination, nil
}

func (c *inboundPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	destination, ok := addr.(M.Socksaddr)
	if !ok {
		destination = M.SocksaddrFromNet(addr)
	}
	if err := writeUOTFrame(c.conn, p, destination); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *inboundPacketConn) Close() error                       { return c.conn.Close() }
func (c *inboundPacketConn) LocalAddr() net.Addr                { return c.conn.LocalAddr() }
func (c *inboundPacketConn) SetDeadline(t time.Time) error      { return c.conn.SetDeadline(t) }
func (c *inboundPacketConn) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *inboundPacketConn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }

// uotPacketConn is the client side of a Mieru UDP association: a
// net.PacketConn (and N.NetPacketConn) over the framed session stream.
//
// A literal connected-UDP tunnel (metadata.UDPConnect) needs a net.Conn, not a
// net.PacketConn; udpStreamConn below is that view of the same object.
type uotPacketConn struct {
	net.Conn
	framer *uotFramer
}

var (
	_ N.PacketConn    = (*uotPacketConn)(nil)
	_ N.NetPacketConn = (*uotPacketConn)(nil)
)

func newUOTPacketConn(conn net.Conn) *uotPacketConn {
	return &uotPacketConn{Conn: conn, framer: newUOTFramer(conn)}
}

func (c *uotPacketConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	n, destination, err := c.framer.readFrame(buffer.FreeBytes())
	if err != nil {
		return M.Socksaddr{}, err
	}
	buffer.Truncate(n)
	return destination, nil
}

func (c *uotPacketConn) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	return writeUOTFrame(c.Conn, buffer.Bytes(), destination)
}

func (c *uotPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, destination, err := c.framer.readFrame(p)
	if err != nil {
		return n, nil, err
	}
	return n, destination, nil
}

func (c *uotPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	destination, ok := addr.(M.Socksaddr)
	if !ok {
		destination = M.SocksaddrFromNet(addr)
	}
	if err := writeUOTFrame(c.Conn, p, destination); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Upstream exposes the session to the framework's unwrap helpers, so a counter
// or tracker wrapper above this connection can still reach the real writer.
func (c *uotPacketConn) Upstream() any { return c.Conn }

// udpStreamConn makes a Mieru UDP association look like a connected net.Conn.
//
// It exists for the metadata.UDPConnect fast path: the router then wraps it in
// bufio.NewUnbindPacketConn and expects a stream whose Write carries one
// datagram to the fixed destination. Each Write is framed with that
// destination, each Read strips the frame and returns the payload only.
type udpStreamConn struct {
	*uotPacketConn
	destination M.Socksaddr
}

var _ net.Conn = (*udpStreamConn)(nil)

func newUDPStreamConn(conn net.Conn, destination M.Socksaddr) *udpStreamConn {
	return &udpStreamConn{
		uotPacketConn: newUOTPacketConn(conn),
		destination:   destination,
	}
}

func (c *udpStreamConn) Read(p []byte) (int, error) {
	n, _, err := c.framer.readFrame(p)
	return n, err
}

func (c *udpStreamConn) Write(p []byte) (int, error) {
	if err := writeUOTFrame(c.Conn, p, c.destination); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *udpStreamConn) RemoteAddr() net.Addr {
	return c.destination
}

// connectedPacketConn adapts the connected UDP socket opened for a Mieru UDP
// transport to net.PacketConn.
//
// The socket is already connected to the peer the fork's dialer chose, so no
// destination is needed on write and every read comes from that peer. The
// source reported on read is the address the SDK resolved for the endpoint,
// not the socket's own RemoteAddr: Connect() on a dual-stack strategy may pick
// a different address family than the SDK's single-address lookup, and the SDK
// validates received packets by comparing against the address it was given.
// Reporting the SDK's own expectation is what keeps that validation from
// silently dropping every packet; the connected socket is the real
// enforcement of the peer.
type connectedPacketConn struct {
	net.Conn
	source M.Socksaddr
}

var (
	_ N.PacketConn    = (*connectedPacketConn)(nil)
	_ N.NetPacketConn = (*connectedPacketConn)(nil)
)

func newConnectedPacketConn(conn net.Conn, source M.Socksaddr) *connectedPacketConn {
	return &connectedPacketConn{Conn: conn, source: source}
}

func (c *connectedPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, err := c.Conn.Read(p)
	if err != nil {
		return n, nil, err
	}
	return n, c.source, nil
}

func (c *connectedPacketConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	return c.Conn.Write(p)
}

func (c *connectedPacketConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	n, err := c.Conn.Read(buffer.FreeBytes())
	if err != nil {
		return M.Socksaddr{}, err
	}
	buffer.Truncate(n)
	return c.source, nil
}

func (c *connectedPacketConn) WritePacket(buffer *buf.Buffer, _ M.Socksaddr) error {
	return writeAll(c.Conn, buffer.Bytes())
}

func (c *connectedPacketConn) Upstream() any { return c.Conn }
