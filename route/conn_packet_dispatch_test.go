package route

import (
	"context"
	"errors"
	"github.com/sagernet/sing/common/buf"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// A connected UDP flow must reach the same network-scheduling surface a TCP flow does.
//
// # The defect
//
// `NewPacketConnection`'s `UDPConnect` arm gated the whole decision on
// `this.(dialer.ParallelInterfaceDialer)`. The DIRECT outbound does not implement that interface - it
// implements the richer `ParallelNetworkDialer` and nothing narrow - so the arm took the `else` branches
// and called `N.DialSerial` / `this.DialContext`, which accept only a destination and an address list.
//
// The consequence is not a failure: `DialContext` still applies the family gate and still reaches
// sniffed-domain recovery. What it silently DROPS is the rest of the per-connection dial configuration:
// `NetworkStrategy`, `NetworkType`, `FallbackNetworkType` and `FallbackDelay`. The TCP arm passes all four
// (see NewConnection) unconditionally, so the same operator configuration governed TCP traffic and did not
// govern UDP traffic - on a multi-homed machine, that is a UDP egress on an interface the operator
// excluded.
//
// # Why the guard was both redundant and harmful
//
// `dialer.DialSerialNetwork` takes an `N.Dialer` and does the dispatch itself: `ParallelNetworkDialer`
// first, then `ParallelInterfaceDialer`, then a plain serial loop. So the outer assertion could only ever
// REMOVE a path - never add one. That is the third instance of the same mistake in this codebase: a
// capability check written against the narrow interface, silently discarding the richer one.

// dialDispatchRecorder is an outbound that implements ONLY the network-level parallel surface, which is
// exactly the shape `protocol/direct.Outbound` presents, plus the plain `N.Dialer` methods.
//
// It deliberately does NOT implement `ParallelInterfaceDialer`: a double that implemented both would hide
// the defect, because the narrow assertion under test would then succeed.
type dialDispatchRecorder struct {
	mu sync.Mutex

	parallelNetworkCalls int
	plainDialCalls       int

	// lastStrategy and friends record what the dispatch forwarded, so the test can assert the
	// configuration actually travelled rather than only that some method was called.
	lastStrategy      *C.NetworkStrategy
	lastNetworkType   []C.InterfaceType
	lastFallbackType  []C.InterfaceType
	lastFallbackDelay time.Duration
	lastDestinations  []netip.Addr
	lastDestination   M.Socksaddr
	lastNetwork       string
}

func (d *dialDispatchRecorder) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	d.mu.Lock()
	d.plainDialCalls++
	d.lastNetwork = network
	d.lastDestination = destination
	d.mu.Unlock()
	// Fail so the manager takes its error branch and returns without copying packets: this test is about
	// which dispatch ran, never about the connection.
	return nil, errDispatchRecorded
}

func (d *dialDispatchRecorder) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, errDispatchRecorded
}

func (d *dialDispatchRecorder) DialParallelNetwork(ctx context.Context, network string, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	d.mu.Lock()
	d.parallelNetworkCalls++
	d.lastNetwork = network
	d.lastDestination = destination
	d.lastDestinations = append([]netip.Addr(nil), destinationAddresses...)
	d.lastStrategy = strategy
	d.lastNetworkType = interfaceType
	d.lastFallbackType = fallbackInterfaceType
	d.lastFallbackDelay = fallbackDelay
	d.mu.Unlock()
	return nil, errDispatchRecorded
}

func (d *dialDispatchRecorder) ListenSerialNetworkPacket(ctx context.Context, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, netip.Addr, error) {
	return nil, netip.Addr{}, errDispatchRecorded
}

var (
	_ N.Dialer                     = (*dialDispatchRecorder)(nil)
	_ dialer.ParallelNetworkDialer = (*dialDispatchRecorder)(nil)
)

// The assertion that makes the double honest: it must NOT satisfy the narrow interface the old code
// tested for, or this test would pass on the defective code.
var _ = func() {
	var asAny any = (*dialDispatchRecorder)(nil)
	if _, isNarrow := asAny.(dialer.ParallelInterfaceDialer); isNarrow {
		panic("the recorder must not implement ParallelInterfaceDialer: it stands for the direct outbound, " +
			"which does not, and a double that did would hide the defect")
	}
}

var errDispatchRecorded = errors.New("dial dispatch recorded")

// packetConnStub is an inbound packet connection the manager can hand to CloseOnHandshakeFailure.
type packetConnStub struct{}

func (packetConnStub) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) { return M.Socksaddr{}, nil }
func (packetConnStub) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	return nil
}
func (packetConnStub) ReadFrom(p []byte) (int, net.Addr, error)     { return 0, nil, nil }
func (packetConnStub) WriteTo(p []byte, addr net.Addr) (int, error) { return 0, nil }
func (packetConnStub) Close() error                                 { return nil }
func (packetConnStub) LocalAddr() net.Addr                          { return &net.UDPAddr{} }
func (packetConnStub) SetDeadline(t time.Time) error                { return nil }
func (packetConnStub) SetReadDeadline(t time.Time) error            { return nil }
func (packetConnStub) SetWriteDeadline(t time.Time) error           { return nil }

// TestConnectedUDPReachesTheNetworkSchedulingSurface is the regression.
//
// A connected UDP flow whose destination is an address must be scheduled through
// `ParallelNetworkDialer.DialParallelNetwork`, because that is the only surface that carries the network
// strategy, the interface selection and the fallback delay. The direct outbound offers exactly that
// surface and not the narrow one, so gating on the narrow one routes it away from its own capability.
func TestConnectedUDPReachesTheNetworkSchedulingSurface(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = manager.Close() })

	strategy := C.NetworkStrategyDefault
	fallbackDelay := 40 * time.Millisecond
	target := netip.MustParseAddr("2001:db8::2c")

	recorder := &dialDispatchRecorder{}
	metadata := adapter.InboundContext{
		UDPConnect:          true,
		Destination:         M.SocksaddrFrom(target, 443),
		NetworkStrategy:     &strategy,
		NetworkType:         []C.InterfaceType{C.InterfaceTypeWIFI},
		FallbackNetworkType: []C.InterfaceType{C.InterfaceTypeEthernet},
		FallbackDelay:       fallbackDelay,
	}

	manager.NewPacketConnection(context.Background(), recorder, packetConnStub{}, metadata, nil)

	recorder.mu.Lock()
	defer recorder.mu.Unlock()

	require.Equal(t, 1, recorder.parallelNetworkCalls,
		"a connected UDP flow must be scheduled through the network-level surface: it is the only one that "+
			"carries the network strategy, the interface selection and the fallback delay")
	require.Zero(t, recorder.plainDialCalls,
		"and it must not fall back to the per-destination dial, which drops all of them")

	// The configuration must actually travel, not merely be accepted.
	require.NotNil(t, recorder.lastStrategy, "the network strategy must be forwarded")
	require.Equal(t, []C.InterfaceType{C.InterfaceTypeWIFI}, recorder.lastNetworkType)
	require.Equal(t, []C.InterfaceType{C.InterfaceTypeEthernet}, recorder.lastFallbackType)
	require.Equal(t, fallbackDelay, recorder.lastFallbackDelay)
	require.Equal(t, target, recorder.lastDestination.Addr)
	require.Equal(t, N.NetworkUDP, recorder.lastNetwork)
}

// TestConnectedUDPWithCandidatesReachesTheNetworkSchedulingSurface is the same for the arm that already
// holds a candidate list.
//
// This one is reachable today even through the narrow interface when the outbound has it, which is why
// the defect hid: only the direct outbound - the one that has the network surface INSTEAD of the narrow
// one - took the lossy branch.
func TestConnectedUDPWithCandidatesReachesTheNetworkSchedulingSurface(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = manager.Close() })

	ipv6 := netip.MustParseAddr("2001:db8::2c")
	ipv4 := netip.MustParseAddr("192.0.2.92")
	strategy := C.NetworkStrategyDefault

	recorder := &dialDispatchRecorder{}
	metadata := adapter.InboundContext{
		UDPConnect:           true,
		Destination:          M.SocksaddrFrom(ipv6, 443),
		DestinationAddresses: []netip.Addr{ipv6, ipv4},
		NetworkStrategy:      &strategy,
	}

	manager.NewPacketConnection(context.Background(), recorder, packetConnStub{}, metadata, nil)

	recorder.mu.Lock()
	defer recorder.mu.Unlock()

	require.Equal(t, 1, recorder.parallelNetworkCalls)
	require.Zero(t, recorder.plainDialCalls)
	require.Equal(t, []netip.Addr{ipv6, ipv4}, recorder.lastDestinations,
		"the caller's candidate list must reach the scheduler as a list")
	require.NotNil(t, recorder.lastStrategy)
}

// TestConnectedUDPDomainStillResolvesThroughDialContext is the control that keeps the fix from over-reaching.
//
// A DOMAIN destination has no address to schedule and no candidate list: the network-level surface
// panics on that shape ("invalid usage"), and resolution is the dialer's job. It must keep going through
// `DialContext`.
func TestConnectedUDPDomainStillResolvesThroughDialContext(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = manager.Close() })

	recorder := &dialDispatchRecorder{}
	metadata := adapter.InboundContext{
		UDPConnect:  true,
		Destination: M.ParseSocksaddrHostPort("szminorshort.weixin.qq.com", 443),
	}

	manager.NewPacketConnection(context.Background(), recorder, packetConnStub{}, metadata, nil)

	recorder.mu.Lock()
	defer recorder.mu.Unlock()

	require.Zero(t, recorder.parallelNetworkCalls,
		"a domain destination has no candidate list to schedule: it must be resolved, not scheduled")
	require.Equal(t, 1, recorder.plainDialCalls)
}
