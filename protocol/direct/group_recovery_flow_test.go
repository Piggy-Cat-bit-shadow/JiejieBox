package direct

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/group"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// The same flow, through a real group and a real Direct: group selection -> Direct's resolved
// surface -> the complete candidate list -> one attempt per candidate -> the healthy one wins.
//
// This closes the "group -> real Direct -> recovery candidates" gap that was previously recorded as
// two segmented facts (a group forwarding test and a Direct dispatch test). It does not run DNS
// recovery itself - the candidate list stands in for the recovered one - but every layer between the
// route dispatch and the per-candidate attempt is the real production code; only the leaf is a double.

// candidateSurface is both surfaces of the resolved chain. The NETWORK method receives the list and
// performs one attempt per candidate, which is the behaviour a recovery feed relies on.
type candidateSurface struct {
	access       sync.Mutex
	attempts     []netip.Addr
	fail         map[netip.Addr]error
	networkCalls atomic.Int32
}

func (s *candidateSurface) record(address netip.Addr) error {
	s.access.Lock()
	s.attempts = append(s.attempts, address)
	failure := s.fail[address]
	s.access.Unlock()
	return failure
}

func (s *candidateSurface) dial(address netip.Addr) (net.Conn, error) {
	if err := s.record(address); err != nil {
		return nil, err
	}
	client, server := net.Pipe()
	go func() {
		_, _ = io.Copy(server, server)
		_ = server.Close()
	}()
	return client, nil
}

func (s *candidateSurface) DialContext(_ context.Context, _ string, destination M.Socksaddr) (net.Conn, error) {
	return s.dial(destination.Addr)
}

func (s *candidateSurface) ListenPacket(_ context.Context, _ M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("not implemented")
}

func (s *candidateSurface) DialParallelInterface(_ context.Context, _ string, destination M.Socksaddr, _ *C.NetworkStrategy, _ []C.InterfaceType, _ []C.InterfaceType, _ time.Duration) (net.Conn, error) {
	return s.dial(destination.Addr)
}

func (s *candidateSurface) DialParallelNetwork(_ context.Context, _ string, _ M.Socksaddr, addresses []netip.Addr, _ *C.NetworkStrategy, _ []C.InterfaceType, _ []C.InterfaceType, _ time.Duration) (net.Conn, error) {
	s.networkCalls.Add(1)
	var lastErr error
	for _, address := range addresses {
		conn, err := s.dial(address)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no candidates")
	}
	return nil, lastErr
}

func (s *candidateSurface) ListenSerialInterfacePacket(_ context.Context, _ M.Socksaddr, _ *C.NetworkStrategy, _ []C.InterfaceType, _ []C.InterfaceType, _ time.Duration) (net.PacketConn, error) {
	return nil, errors.New("not implemented")
}

func (s *candidateSurface) ListenSerialNetworkPacket(_ context.Context, _ M.Socksaddr, _ []netip.Addr, _ *C.NetworkStrategy, _ []C.InterfaceType, _ []C.InterfaceType, _ time.Duration) (net.PacketConn, netip.Addr, error) {
	return nil, netip.Addr{}, errors.New("not implemented")
}

func (s *candidateSurface) attempted() []netip.Addr {
	s.access.Lock()
	defer s.access.Unlock()
	return append([]netip.Addr(nil), s.attempts...)
}

// candidateManager is the outbound namespace the group selects from.
type candidateManager struct {
	outbound *Outbound
	lookups  atomic.Int32
}

func (m *candidateManager) Start(adapter.StartStage, *adapter.Scope) error { return nil }
func (m *candidateManager) Close() error                                   { return nil }
func (m *candidateManager) Outbounds() []adapter.Outbound                  { return []adapter.Outbound{m.outbound} }
func (m *candidateManager) Default() adapter.Outbound                      { return m.outbound }
func (m *candidateManager) Outbound(tag string) (adapter.Outbound, bool) {
	m.lookups.Add(1)
	if tag == "direct" {
		return m.outbound, true
	}
	return nil, false
}
func (m *candidateManager) Create(context.Context, adapter.Router, log.ContextLogger, string, string, any) error {
	return nil
}

func newGroupDirectWorld(t *testing.T) (*group.Selector, *candidateSurface) {
	t.Helper()
	first := netip.MustParseAddr("192.0.2.1")
	surface := &candidateSurface{fail: map[netip.Addr]error{first: errors.New("connection refused")}}
	outbound := newFlowOutbound(surface)
	manager := &candidateManager{outbound: outbound}
	ctx := service.ContextWith[adapter.OutboundManager](context.Background(), manager)
	created, err := group.NewSelector(ctx, nil, log.NewNOPFactory().NewLogger("selector"), "selector", option.SelectorOutboundOptions{
		Outbounds: []string{"direct"},
		Default:   "direct",
	})
	require.NoError(t, err)
	selector := created.(*group.Selector)
	require.NoError(t, selector.Start(adapter.StartStateStart, &adapter.Scope{}))
	return selector, surface
}

// TestGroupThroughRealDirectKeepsTheCandidateList is the same-flow evidence.
func TestGroupThroughRealDirectKeepsTheCandidateList(t *testing.T) {
	first := netip.MustParseAddr("192.0.2.1")
	second := netip.MustParseAddr("192.0.2.2")

	selector, surface := newGroupDirectWorld(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// The route dispatch (a real DialSerialNetwork), the real selector, the real Direct, and the
	// candidate list it forwards.
	conn, err := dialer.DialSerialNetwork(ctx, selector, N.NetworkTCP,
		M.ParseSocksaddrHostPort("example.com", 443),
		[]netip.Addr{first, second}, nil, nil, nil, 0)
	require.NoError(t, err, "attempts: %v", surface.attempted())
	require.NotNil(t, conn)
	require.Equal(t, int32(1), surface.networkCalls.Load(),
		"the group must make one selection and hand the list to the Direct once")
	require.Equal(t, []netip.Addr{first, second}, surface.attempted(),
		"the failed candidate must not end the list: the healthy one is attempted once")

	_, err = conn.Write([]byte("ping"))
	require.NoError(t, err)
	buffer := make([]byte, 4)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = io.ReadFull(conn, buffer)
	require.NoError(t, err)
	require.Equal(t, "ping", string(buffer), "business data must flow over the selected path")
	require.NoError(t, conn.Close())
}

// TestLoadBalanceThroughRealDirectKeepsTheCandidateList covers the second applicable group: a
// LoadBalance selection must forward the same list to the same real Direct.
func TestLoadBalanceThroughRealDirectKeepsTheCandidateList(t *testing.T) {
	first := netip.MustParseAddr("192.0.2.1")
	second := netip.MustParseAddr("192.0.2.2")

	selector, surface := newGroupDirectWorld(t)
	_ = selector
	manager := &candidateManager{outbound: newFlowOutbound(surface)}
	ctx := service.ContextWith[adapter.OutboundManager](context.Background(), manager)
	created, err := group.NewLoadBalance(ctx, nil, log.NewNOPFactory().NewLogger("lb"), "lb", option.LoadBalanceOutboundOptions{
		Outbounds: []string{"direct"},
	})
	require.NoError(t, err)
	loadBalance := created.(*group.LoadBalance)
	require.NoError(t, loadBalance.Start(adapter.StartStateStart, &adapter.Scope{}))

	dialCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dialer.DialSerialNetwork(dialCtx, loadBalance, N.NetworkTCP,
		M.ParseSocksaddrHostPort("example.com", 443),
		[]netip.Addr{first, second}, nil, nil, nil, 0)
	require.NoError(t, err, "attempts: %v", surface.attempted())
	require.NotNil(t, conn)
	require.Equal(t, []netip.Addr{first, second}, surface.attempted())
	require.NoError(t, conn.Close())
}

// newFlowOutbound is a real Direct whose resolved surface points at the double. The adapter is what
// the group resolves the member by, so the tag must be real.
func newFlowOutbound(surface *candidateSurface) *Outbound {
	return &Outbound{
		Adapter: outbound.NewAdapter("direct", "direct", []string{N.NetworkTCP, N.NetworkUDP}, nil),
		surface: dialer.LocalDialSurface{Attempt: surface, Network: surface},
		logger:  log.NewNOPFactory().Logger(),
	}
}
