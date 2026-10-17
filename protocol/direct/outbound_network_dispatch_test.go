package direct

import (
	"context"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The direct outbound must hand a candidate LIST to the richer surface its dialer has.
//
// # The defect these pin
//
// `Outbound.dialer` is declared at the narrow `ParallelInterfaceDialer` type, because that is the
// narrowest surface an attempt needs and because a detour dialer has no parallel network surface. The
// object inside it is normally a `*dialer.resolveParallelNetworkDialer`, which implements the richer
// `ParallelNetworkDialer` - and that method is the ONLY place domain recovery for a literal destination
// lives: the sniffed-domain lookup, the late-candidate feed, the family fallback.
//
// The network methods used to call the package-level helper directly, which takes the narrow type and
// schedules candidates from the list it is given. So the richer method was never invoked, and the
// production log shows the consequence: a WeChat flow to a global IPv6 literal with a sniffed
// `szminorshort.weixin.qq.com` was refused by the local-family gate, with no `dns: lookup domain` line
// between the sniff and the refusal. Fixing the wrapper did not help, because `ConnectionManager` hands
// `DialSerialNetwork` the OUTBOUND, so this method is the entry point.
//
// # Why the assertion is "was the richer method called"
//
// A test that only asserted "a connection came back" would pass on the broken revision whenever the
// narrow path happened to succeed - the recovery decision belongs to the wrapper, and the wrapper
// returns a working connection either way. The property under test is the outbound's CHOICE OF SURFACE,
// so the spy has to be the thing asserted on. The double below is a full ParallelNetworkDialer, because
// that is what the production field actually holds; a narrower spy would test the fallback.

// networkSurfaceSpy implements both parallel surfaces and records which was used.
type networkSurfaceSpy struct {
	networkCalls atomic.Int32
	packetCalls  atomic.Int32
	// connections counts the per-attempt dials, so "one attempt per scheduled candidate" is
	// observable rather than inferred.
	connections atomic.Int32
}

func (s *networkSurfaceSpy) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	s.connections.Add(1)
	return &countingConn{}, nil
}

func (s *networkSurfaceSpy) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return &countingPacketConn{}, nil
}

func (s *networkSurfaceSpy) DialParallelInterface(ctx context.Context, network string, destination M.Socksaddr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	return s.DialContext(ctx, network, destination)
}

func (s *networkSurfaceSpy) ListenSerialInterfacePacket(ctx context.Context, destination M.Socksaddr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, error) {
	return s.ListenPacket(ctx, destination)
}

func (s *networkSurfaceSpy) DialParallelNetwork(ctx context.Context, network string, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	s.networkCalls.Add(1)
	for _, address := range destinationAddresses {
		return s.DialParallelInterface(ctx, network, M.SocksaddrFrom(address, destination.Port), strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
	}
	return s.DialContext(ctx, network, destination)
}

func (s *networkSurfaceSpy) ListenSerialNetworkPacket(ctx context.Context, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, netip.Addr, error) {
	s.packetCalls.Add(1)
	for _, address := range destinationAddresses {
		conn, err := s.ListenSerialInterfacePacket(ctx, M.SocksaddrFrom(address, destination.Port), strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
		return conn, address, err
	}
	return nil, netip.Addr{}, context.Canceled
}

// narrowFieldOutbound is the production outbound with its dialer field set to the spy the way
// NewOutbound sets it: at the narrow type, over an object that can do more.
//
// The logger is the piece that must not be faked: `InfoContext` is called on it before the dispatch
// under test, and a zero interface there panics rather than logging nothing. The factory's own no-op
// implementation is used so the test exercises the real code path with the real nil-safe logger.
func narrowFieldOutbound(spy *networkSurfaceSpy) *Outbound {
	return &Outbound{dialer: spy, logger: log.NewNOPFactory().Logger()}
}

func TestDialParallelNetworkReachesTheRicherSurface(t *testing.T) {
	spy := &networkSurfaceSpy{}
	outbound := narrowFieldOutbound(spy)

	address := netip.MustParseAddr("2409:8c54:871:1007::2c")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := outbound.DialParallelNetwork(ctx, "tcp", M.SocksaddrFrom(address, 80),
		[]netip.Addr{address}, nil, nil, nil, 0)
	require.NoError(t, err)
	require.NotNil(t, conn)
	conn.Close()

	require.Equal(t, int32(1), spy.networkCalls.Load(),
		"the candidate list must reach the network-level method: that is where sniffed-domain recovery lives")
}

func TestDialParallelReachesTheRicherSurface(t *testing.T) {
	spy := &networkSurfaceSpy{}
	outbound := narrowFieldOutbound(spy)

	address := netip.MustParseAddr("2409:8c54:871:1007::2c")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := outbound.DialParallel(ctx, "tcp", M.SocksaddrFrom(address, 80), []netip.Addr{address})
	require.NoError(t, err)
	require.NotNil(t, conn)
	conn.Close()

	require.Equal(t, int32(1), spy.networkCalls.Load(),
		"DialParallel is a separate entry point and must not be the one that was forgotten")
}

func TestListenSerialNetworkPacketReachesTheRicherSurface(t *testing.T) {
	spy := &networkSurfaceSpy{}
	outbound := narrowFieldOutbound(spy)

	address := netip.MustParseAddr("2409:8c54:871:1007::2c")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	packetConn, destination, err := outbound.ListenSerialNetworkPacket(ctx, M.SocksaddrFrom(address, 443),
		[]netip.Addr{address}, nil, nil, nil, 0)
	require.NoError(t, err)
	require.NotNil(t, packetConn)
	packetConn.Close()
	require.Equal(t, address, destination)

	require.Equal(t, int32(1), spy.packetCalls.Load(),
		"the packet walk has no race to fall back on, so the recovered candidate is its only fallback")
}

// TestNetworkSurfaceFallsBackToAPlainDialer is the other half of the dispatch contract, and the reason
// the fix cannot be "always delegate".
//
// A detour routes to another outbound instead of creating a socket, so its dialer has no parallel
// network surface at all. That shape must keep working exactly as before - no scheduler, no recovery,
// no new failure mode - which means the fallback has to be real and tested rather than implied.
func TestNetworkSurfaceFallsBackToAPlainDialer(t *testing.T) {
	plain := &plainDialer{}
	outbound := &Outbound{dialer: plain, logger: log.NewNOPFactory().Logger()}

	address := netip.MustParseAddr("192.0.2.44")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := outbound.DialParallelNetwork(ctx, "tcp", M.SocksaddrFrom(address, 443),
		[]netip.Addr{address}, nil, nil, nil, 0)
	require.NoError(t, err)
	require.NotNil(t, conn)
	conn.Close()

	require.Equal(t, []netip.Addr{address}, plain.tried,
		"with no parallel surface the list is dialled as given, one address at a time")
}

// plainDialer is the detour shape: a dialer with the per-attempt surface and NO parallel network
// surface.
//
// It has to implement ParallelInterfaceDialer because that is the declared type of the field, and NOT
// implement ParallelNetworkDialer because that is the capability whose absence the fallback exists for.
// Both halves matter: a fixture that had neither surface would compile against a different field type
// than production uses.
type plainDialer struct {
	tried []netip.Addr
}

func (d *plainDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	d.tried = append(d.tried, destination.Addr)
	return &countingConn{}, nil
}

func (d *plainDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return &countingPacketConn{}, nil
}

func (d *plainDialer) DialParallelInterface(ctx context.Context, network string, destination M.Socksaddr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	return d.DialContext(ctx, network, destination)
}

func (d *plainDialer) ListenSerialInterfacePacket(ctx context.Context, destination M.Socksaddr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, error) {
	return d.ListenPacket(ctx, destination)
}

// countingConn and countingPacketConn are inert transports; these tests assert on which METHOD was
// called, never on what it returns.
type countingConn struct{}

func (c *countingConn) Read([]byte) (int, error)         { return 0, nil }
func (c *countingConn) Write(b []byte) (int, error)      { return len(b), nil }
func (c *countingConn) Close() error                     { return nil }
func (c *countingConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *countingConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *countingConn) SetDeadline(time.Time) error      { return nil }
func (c *countingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *countingConn) SetWriteDeadline(time.Time) error { return nil }

type countingPacketConn struct{}

func (c *countingPacketConn) ReadFrom([]byte) (int, net.Addr, error) { return 0, nil, nil }
func (c *countingPacketConn) WriteTo([]byte, net.Addr) (int, error)  { return 0, nil }
func (c *countingPacketConn) Close() error                           { return nil }
func (c *countingPacketConn) LocalAddr() net.Addr                    { return &net.UDPAddr{} }
func (c *countingPacketConn) SetDeadline(time.Time) error            { return nil }
func (c *countingPacketConn) SetReadDeadline(time.Time) error        { return nil }
func (c *countingPacketConn) SetWriteDeadline(time.Time) error       { return nil }
