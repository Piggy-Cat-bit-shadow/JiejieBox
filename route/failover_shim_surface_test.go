package route

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// A failover group's shim must not hide the per-attempt dial surface from the connection manager.
//
// # The family, fifth instance
//
// `routeDialer` wraps the leaf outbound in a `failoverDialer` that EMBEDS the narrow `adapter.Outbound`
// interface and hand-forwards exactly one capability (`EarlyConnectionBufferGrowth`). A typed assertion on an
// embedded INTERFACE promotes only the methods that interface declares - the file's own comment says so, in
// the paragraph explaining why the copy-buffer opt-in needed forwarding - so every other capability of the
// leaf is invisible through the shim.
//
// The one that matters is the parallel dial surface. `connect` reaches the dialer through
// `dialer.DialSerialNetwork`, which dispatches on `ParallelNetworkDialer` and otherwise falls back to a plain
// serial loop calling `DialContext(ctx, network, destination)` - a signature with no parameter for
// `network_strategy`, `network_type`, `fallback_network_type` or `fallback_delay`. So those four, when the
// matching rule set them through `route_options`, were silently dropped for a flow whose selected outbound is
// a `failover: true` loadbalance group, while the same rule applied to the leaf directly honoured them and
// the packet path - which passes the leaf, not the shim - kept them.
//
// That is the connected-UDP defect with the protocols swapped: one operator configuration governing one
// transport and not the other.

// parallelLeafDouble is a leaf exposing the richer surface, which is what a real direct outbound does.
type parallelLeafDouble struct {
	adapter.Outbound
	access sync.Mutex
	calls  int
	last   adapter.FailoverDialOptions
}

func (d *parallelLeafDouble) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return &failoverStubConn{}, nil
}

func (d *parallelLeafDouble) DialParallelNetwork(ctx context.Context, network string, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, networkType []C.InterfaceType, fallbackNetworkType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	d.access.Lock()
	d.calls++
	d.last = adapter.FailoverDialOptions{
		DestinationAddresses: destinationAddresses,
		NetworkStrategy:      strategy,
		NetworkType:          networkType,
		FallbackNetworkType:  fallbackNetworkType,
		FallbackDelay:        fallbackDelay,
	}
	d.access.Unlock()
	return &failoverStubConn{}, nil
}

func (d *parallelLeafDouble) ListenSerialNetworkPacket(ctx context.Context, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, networkType []C.InterfaceType, fallbackNetworkType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, netip.Addr, error) {
	return nil, netip.Addr{}, nil
}

func (d *parallelLeafDouble) recorded() (int, adapter.FailoverDialOptions) {
	d.access.Lock()
	defer d.access.Unlock()
	return d.calls, d.last
}

var (
	_ N.Dialer                     = (*parallelLeafDouble)(nil)
	_ dialer.ParallelNetworkDialer = (*parallelLeafDouble)(nil)
)

// failoverGroupDouble is an opted-in group that hands the attempt to the member it resolved.
type failoverGroupDouble struct {
	// The group interface is embedded NIL rather than reimplemented: only FailoverEnabled and
	// DialWithFailover are called on this double, and enumerating the rest would have to track a contract that
	// is not under test here. An unexpected call panics loudly instead of silently answering zero.
	adapter.FailoverOutboundGroup
	leaf            adapter.Outbound
	failoverCalls   int
	optionDialCalls int
}

func (g *failoverGroupDouble) FailoverEnabled() bool { return true }

func (g *failoverGroupDouble) DialWithFailover(ctx context.Context, metadata *adapter.InboundContext, network string, destination M.Socksaddr) (net.Conn, error) {
	g.failoverCalls++
	if parallel, isParallel := g.leaf.(dialer.ParallelNetworkDialer); isParallel {
		options := adapter.FailoverDialOptionsFromContext(ctx)
		if !options.IsZero() {
			g.optionDialCalls++
			return parallel.DialParallelNetwork(ctx, network, destination, options.DestinationAddresses, options.NetworkStrategy, options.NetworkType, options.FallbackNetworkType, options.FallbackDelay)
		}
	}
	return g.leaf.DialContext(ctx, network, destination)
}

var _ adapter.FailoverOutboundGroup = (*failoverGroupDouble)(nil)

type failoverStubConn struct{}

func (c *failoverStubConn) Read(p []byte) (int, error)         { return 0, net.ErrClosed }
func (c *failoverStubConn) Write(p []byte) (int, error)        { return len(p), nil }
func (c *failoverStubConn) Close() error                       { return nil }
func (c *failoverStubConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (c *failoverStubConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (c *failoverStubConn) SetDeadline(t time.Time) error      { return nil }
func (c *failoverStubConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *failoverStubConn) SetWriteDeadline(t time.Time) error { return nil }

// TestTheFailoverShimDoesNotHideTheParallelDialSurface is the regression.
func TestTheFailoverShimDoesNotHideTheParallelDialSurface(t *testing.T) {
	leaf := &parallelLeafDouble{}
	group := &failoverGroupDouble{leaf: leaf}

	wrapped := routeDialer(leaf, group, &adapter.InboundContext{})

	_, leafIsParallel := any(leaf).(dialer.ParallelNetworkDialer)
	require.True(t, leafIsParallel, "the fixture's leaf must expose the richer surface")

	// `DialSerialNetwork` dispatches on exactly this assertion, so if it fails here it fails there.
	parallel, wrappedIsParallel := any(wrapped).(dialer.ParallelNetworkDialer)
	require.True(t, wrappedIsParallel,
		"a shim that hides the parallel surface makes DialSerialNetwork fall back to a serial loop whose "+
			"DialContext has no parameter for network_strategy, network_type, fallback_network_type or "+
			"fallback_delay - so a rule that sets them through route_options governs UDP and not TCP")

	strategy := C.NetworkStrategyDefault
	networkType := []C.InterfaceType{C.InterfaceTypeWIFI}
	fallbackType := []C.InterfaceType{C.InterfaceTypeCellular}
	addresses := []netip.Addr{netip.MustParseAddr("192.0.2.92")}

	_, err := parallel.DialParallelNetwork(context.Background(), N.NetworkTCP, M.ParseSocksaddrHostPort("example.com", 443), addresses, &strategy, networkType, fallbackType, 250*time.Millisecond)
	require.NoError(t, err)

	require.Equal(t, 1, group.failoverCalls,
		"the shim must forward through the GROUP so failover still owns the attempt; dialling the leaf "+
			"directly would trade one lost feature for another")
	require.Equal(t, 1, group.optionDialCalls)
	calls, options := leaf.recorded()
	require.Equal(t, 1, calls,
		"and the member the group selected must receive the parallel surface rather than a plain DialContext")
	require.Equal(t, addresses, options.DestinationAddresses)
	require.NotNil(t, options.NetworkStrategy, "the interface strategy must arrive")
	require.Equal(t, strategy, *options.NetworkStrategy)
	require.Equal(t, networkType, options.NetworkType, "and the interface selection")
	require.Equal(t, fallbackType, options.FallbackNetworkType, "and the fallback selection")
	require.Equal(t, 250*time.Millisecond, options.FallbackDelay, "and the cadence")
}

// TestAPlainLeafStillDialsThroughTheFailoverGroup is the control.
//
// Without it, a fix that stopped routing through the group - or that broke the fallback for a member without
// the richer surface - would pass the regression above.
func TestAPlainLeafStillDialsThroughTheFailoverGroup(t *testing.T) {
	leaf := &plainLeafDouble{}
	group := &failoverGroupDouble{leaf: leaf}

	wrapped := routeDialer(leaf, group, &adapter.InboundContext{})
	parallel, isParallel := any(wrapped).(dialer.ParallelNetworkDialer)
	require.True(t, isParallel, "the shim exposes the surface regardless of what the leaf can do")

	_, err := parallel.DialParallelNetwork(context.Background(), N.NetworkTCP, M.ParseSocksaddrHostPort("example.com", 443), nil, nil, nil, nil, 0)
	require.NoError(t, err)

	require.Equal(t, 1, group.failoverCalls, "the group still owns the attempt")
	require.Equal(t, 1, leaf.dialContextCalls,
		"a member that cannot use the options must still be dialled, by DialContext, exactly as before")
}

type plainLeafDouble struct {
	adapter.Outbound
	dialContextCalls int
}

func (d *plainLeafDouble) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	d.dialContextCalls++
	return &failoverStubConn{}, nil
}

var _ N.Dialer = (*plainLeafDouble)(nil)

// TestTheShimForwardsItsCapabilitiesAndReportsNoChain pins what replaced the chain report.
//
// The shim embeds `adapter.Outbound`, and a typed assertion on an embedded interface promotes only the
// methods that interface declares - which is how `EarlyConnectionBufferGrowth` and then
// `ParallelNetworkDialer` were each lost in turn. An adversarial sweep found eight more capabilities hidden
// behind it, all currently unreachable.
//
// The tempting fix was to report the leaf on `Upstream()`, and it was WRONG twice over: the embedded member
// is the FIRST attempt's leaf while the group re-resolves one per attempt, and `Upstream()` is walked by
// `common.Cast` repo-wide - answering it there moved a WireGuard endpoint's bind choice through a detour,
// three packages away. So the shim forwards by hand and reports no chain, and this test pins both halves:
// the forwarded set is present, and no chain is advertised.
func TestTheShimForwardsItsCapabilitiesAndReportsNoChain(t *testing.T) {
	leaf := &parallelLeafDouble{}
	group := &failoverGroupDouble{leaf: leaf}

	wrapped := routeDialer(leaf, group, &adapter.InboundContext{})

	// The forwarded set, as the route layer reads it.
	_, isDialer := any(wrapped).(N.Dialer)
	require.True(t, isDialer, "the connection manager dials it")
	_, isParallel := any(wrapped).(dialer.ParallelNetworkDialer)
	require.True(t, isParallel, "DialSerialNetwork dispatches on exactly this")
	_, isTuner := any(wrapped).(adapter.ConnectionCopyTuner)
	require.True(t, isTuner, "the copy loop asks, and the leaf's opt-in must survive the shim")

	// No chain report on either protocol. `DialChain` is the package-scoped hop protocol and `Upstream` is
	// the repo-wide one `common.Cast` walks; answering either would name a member that may not dial.
	_, reportsDialChain := any(wrapped).(interface{ DialChain() N.Dialer })
	require.False(t, reportsDialChain,
		"the shim must not report a dial chain: the member it would name is the first attempt's, while the "+
			"group resolves one per attempt")
	_, reportsUpstream := any(wrapped).(interface{ Upstream() any })
	require.False(t, reportsUpstream,
		"and it must not answer the repo-wide Upstream protocol, which common.Cast walks everywhere")
}

// TestTheShimStillOwnsTheAttempt is the control that keeps the walk from becoming a bypass.
//
// Reporting the leaf says where the CAPABILITIES are. It must not say where the DIAL goes: the group owns
// the attempt sequence, and a caller that reads the chain must not conclude it may dial the leaf directly.
func TestTheShimStillOwnsTheAttempt(t *testing.T) {
	leaf := &parallelLeafDouble{}
	group := &failoverGroupDouble{leaf: leaf}

	wrapped := routeDialer(leaf, group, &adapter.InboundContext{})
	_, err := wrapped.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddrHostPort("example.com", 443))
	require.NoError(t, err)
	require.Equal(t, 1, group.failoverCalls,
		"a plain dial still goes through the group: the chain walk is capability discovery, not routing")
	attempts, _ := leaf.recorded()
	require.Equal(t, 0, attempts,
		"and the leaf is not dialled behind the group's back")
}
