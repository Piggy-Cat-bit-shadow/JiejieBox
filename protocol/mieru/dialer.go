package mieru

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	apicommon "github.com/enfein/mieru/v3/apis/common"
)

// serverDialer adapts the fork's N.Dialer to the Mieru SDK dialer
// interfaces.
//
// The SDK speaks host:port strings; the fork speaks M.Socksaddr and owns
// domain_resolver, network_strategy, bind_interface, routing_mark and the
// dual-stack scheduler. No resolution happens here: a server domain is handed
// to the fork's dialer as a domain.
type serverDialer struct {
	dialer N.Dialer
	server M.Socksaddr

	// PacketUnderlay.Close only interrupts its read loop with a deadline; it
	// does not close the PacketConn. The SDK's read loop can then overwrite
	// that deadline and stay blocked, so the adapter owns the sockets it
	// created and closes them when the client generation is torn down.
	packetAccess sync.Mutex
	packetClosed bool
	packetConns  map[net.PacketConn]struct{}
}

var (
	_ apicommon.Dialer       = (*serverDialer)(nil)
	_ apicommon.PacketDialer = (*serverDialer)(nil)
)

func newServerDialer(outboundDialer N.Dialer, server string) (*serverDialer, error) {
	if server == "" {
		return nil, fmt.Errorf("mieru: server is empty")
	}
	if M.IsDomainName(server) {
		return &serverDialer{
			dialer: outboundDialer,
			server: M.Socksaddr{Fqdn: apicommon.NormalizeDomainName(server)},
		}, nil
	}
	addr, err := netip.ParseAddr(server)
	if err != nil {
		return nil, E.Cause(err, "mieru: server ", server)
	}
	return &serverDialer{
		dialer: outboundDialer,
		server: M.SocksaddrFrom(addr, 0),
	}, nil
}

func (d *serverDialer) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	destination := M.ParseSocksaddr(address)
	if !destination.IsValid() {
		return nil, fmt.Errorf("mieru: invalid server address %q", address)
	}
	conn, err := d.dialer.DialContext(ctx, network, destination)
	observeSocketDial(ctx, err)
	return conn, err
}

// ListenPacket opens the UDP transport socket.
//
// The SDK resolves the endpoint before calling a PacketDialer, because its
// PacketUnderlay stores the resolved address to validate incoming packets
// against. That pre-resolution cannot be bypassed through the public API, and
// dialling its single answer directly would lose the fork's dual-stack racing
// and family policy. Instead the port is taken from the SDK's endpoint and the
// configured server name is dialled through the fork's dialer, which makes the
// real address decision; the socket is connected, so only the peer it chose
// can deliver packets. The SDK is told the address it expected for that peer,
// which is the only thing it uses the value for.
func (d *serverDialer) ListenPacket(ctx context.Context, network string, laddr string, raddr string) (net.PacketConn, error) {
	expected := M.ParseSocksaddr(raddr)
	if !expected.IsValid() || expected.Port == 0 {
		return nil, fmt.Errorf("mieru: invalid UDP server endpoint %q", raddr)
	}
	destination := d.server
	destination.Port = expected.Port
	conn, err := d.dialer.DialContext(ctx, N.NetworkUDP, destination)
	if err != nil {
		observeSocketDial(ctx, err)
		return nil, err
	}
	observeSocketDial(ctx, nil)
	tracked := &dialPacketConn{
		PacketConn: newConnectedPacketConn(conn, expected),
		server:     d,
	}
	if !d.addPacketConn(tracked) {
		_ = tracked.Close()
		return nil, E.New("mieru: outbound was closed while creating the UDP underlay")
	}
	return tracked, nil
}

func (d *serverDialer) addPacketConn(conn net.PacketConn) bool {
	d.packetAccess.Lock()
	defer d.packetAccess.Unlock()
	if d.packetClosed {
		return false
	}
	if d.packetConns == nil {
		d.packetConns = make(map[net.PacketConn]struct{})
	}
	d.packetConns[conn] = struct{}{}
	return true
}

func (d *serverDialer) removePacketConn(conn net.PacketConn) {
	d.packetAccess.Lock()
	delete(d.packetConns, conn)
	d.packetAccess.Unlock()
}

func (d *serverDialer) packetConnCount() int {
	d.packetAccess.Lock()
	defer d.packetAccess.Unlock()
	return len(d.packetConns)
}

// closePacketConns closes the sockets of the current client generation. A
// rebuild installs a new generation, so new sockets may be registered again.
// shutdownPacketConns is the final form used by Outbound.Close.
func (d *serverDialer) closePacketConns() {
	d.closePacketConnsLocked(false)
}

func (d *serverDialer) shutdownPacketConns() {
	d.closePacketConnsLocked(true)
}

func (d *serverDialer) closePacketConnsLocked(final bool) {
	d.packetAccess.Lock()
	if final {
		d.packetClosed = true
	}
	conns := make([]net.PacketConn, 0, len(d.packetConns))
	for conn := range d.packetConns {
		conns = append(conns, conn)
		delete(d.packetConns, conn)
	}
	d.packetAccess.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

type dialPacketConn struct {
	net.PacketConn
	server    *serverDialer
	closeOnce sync.Once
	closeErr  error
}

func (c *dialPacketConn) Close() error {
	c.closeOnce.Do(func() {
		if c.server != nil {
			c.server.removePacketConn(c)
		}
		c.closeErr = c.PacketConn.Close()
	})
	return c.closeErr
}

func (c *dialPacketConn) Upstream() any { return c.PacketConn }

// resolverBridge is the DNS resolver the SDK uses for the UDP transport.
//
// The Mieru UDP path resolves its endpoint inside the SDK unconditionally, so
// it cannot use BypassDialerDNS the way the stream path does. This bridge
// asks the fork's DNS router, with the query policy derived from the same
// DialerOptions the stream path uses, so both transports answer to one DNS
// control plane. The query result is only used for the SDK's endpoint and
// source validation; the actual UDP socket is still dialled by the fork's
// dialer.
type resolverBridge struct {
	router  adapter.DNSRouter
	options adapter.DNSQueryOptions
}

var _ apicommon.DNSResolver = (*resolverBridge)(nil)

func newResolverBridge(ctx context.Context, dialerOptions option.DialerOptions) (*resolverBridge, error) {
	queryOptions, err := dialer.TargetQueryOptions(ctx, dialerOptions)
	if err != nil {
		return nil, err
	}
	return &resolverBridge{
		router:  service.FromContext[adapter.DNSRouter](ctx),
		options: queryOptions,
	}, nil
}

func (r *resolverBridge) LookupIP(ctx context.Context, network string, host string) ([]net.IP, error) {
	if r.router == nil {
		return nil, E.New("mieru: no DNS router available to resolve the UDP server endpoint")
	}
	var requireIPv4, requireIPv6 bool
	switch strings.ToLower(network) {
	case "", "ip":
	case "ip4":
		requireIPv4 = true
	case "ip6":
		requireIPv6 = true
	default:
		return nil, net.UnknownNetworkError(network)
	}
	addresses, err := r.router.Lookup(ctx, apicommon.NormalizeDomainName(host), r.options)
	if err != nil {
		// The SDK resolves the UDP endpoint before it calls the packet dialer,
		// so this error never reaches serverDialer.DialContext; record it here
		// or the classifier only sees the SDK's flattened text.
		observeSocketDial(ctx, err)
		return nil, err
	}
	ips := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		isIPv4 := address.Is4() || address.Is4In6()
		if requireIPv4 && !isIPv4 {
			continue
		}
		if requireIPv6 && isIPv4 {
			continue
		}
		ips = append(ips, net.IP(address.AsSlice()))
	}
	if len(ips) == 0 {
		return nil, &net.AddrError{Err: "IP address not found", Addr: host}
	}
	return ips, nil
}
