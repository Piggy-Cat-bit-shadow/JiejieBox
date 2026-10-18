package mieru

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	mieruclient "github.com/enfein/mieru/v3/apis/client"
	mierumodel "github.com/enfein/mieru/v3/apis/model"
)

// dialDrainTimeout bounds how long Close and InterfaceUpdated wait for a dial
// that is already in flight. A handshake has its own 10s SDK read timeout;
// teardown must not inherit it unbounded, but stopping the client under a
// running dial would leave that dial racing a closed Mux.
const dialDrainTimeout = 5 * time.Second

func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.MieruOutboundOptions](registry, C.TypeMieru, NewOutbound)
}

var (
	_ adapter.Outbound                = (*Outbound)(nil)
	_ adapter.InterfaceUpdateListener = (*Outbound)(nil)
)

var errOutboundClosed = E.New("mieru: outbound is closed")

// Outbound is the Mieru client.
//
// The official SDK owns the protocol and its internal multiplexer. This
// adapter owns the fork's dialer (which resolves the server and applies
// domain_resolver / network_strategy / bind_interface / routing_mark), the
// lifecycle, and the translation between Mieru's SOCKS5 request/response
// messages and sing-box's connection contract.
type Outbound struct {
	outbound.Adapter
	logger log.ContextLogger
	config *mieruclient.ClientConfig

	access  sync.RWMutex
	client  mieruclient.Client
	started bool
	closed  bool
	// inFlight counts DialContext calls between snapshotting the client and
	// returning its connection. Close and InterfaceUpdated take the write
	// lock first, which prevents new calls, then drain this counter before
	// stopping the client.
	inFlight inflightGroup
	// dialed is set by the first dial after a (re)start. Before that point the
	// SDK has no underlay or session that could be bound to the old network,
	// so InterfaceUpdated has nothing to rebuild.
	dialed atomic.Bool

	closeOnce sync.Once
	closeErr  error
}

// inflightGroup is a drainable counter.
//
// It exists instead of sync.WaitGroup because the drain has a timeout: a
// WaitGroup Wait that loses the race with the timeout would keep waiting
// after Close returned, and a later Add can then race that parked Wait.
// Closing a per-idle-period channel has neither problem.
type inflightGroup struct {
	access sync.Mutex
	count  int
	idle   chan struct{}
}

func newInflightGroup() inflightGroup {
	idle := make(chan struct{})
	close(idle)
	return inflightGroup{idle: idle}
}

func (g *inflightGroup) begin() {
	g.access.Lock()
	if g.count == 0 {
		g.idle = make(chan struct{})
	}
	g.count++
	g.access.Unlock()
}

func (g *inflightGroup) done() {
	g.access.Lock()
	g.count--
	if g.count == 0 {
		close(g.idle)
	}
	g.access.Unlock()
}

func (g *inflightGroup) wait(timeout time.Duration) bool {
	g.access.Lock()
	idle := g.idle
	g.access.Unlock()
	select {
	case <-idle:
		return true
	case <-time.After(timeout):
		return false
	}
}

func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.MieruOutboundOptions) (adapter.Outbound, error) {
	outboundDialer, err := dialer.New(ctx, options.DialerOptions, M.IsDomainName(options.Server))
	if err != nil {
		return nil, err
	}
	serverDialer, err := newServerDialer(outboundDialer, options.Server)
	if err != nil {
		return nil, err
	}
	resolver, err := newResolverBridge(ctx, options.DialerOptions)
	if err != nil {
		return nil, err
	}
	config, err := buildClientConfig(options, serverDialer, serverDialer, resolver)
	if err != nil {
		return nil, err
	}
	client := mieruclient.NewClient()
	if err = client.Store(config); err != nil {
		return nil, E.Cause(err, "mieru: store client config")
	}
	return &Outbound{
		Adapter:  outbound.NewAdapterWithDialerOptions(C.TypeMieru, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.DialerOptions, options.ServerIsDomain()),
		logger:   logger,
		config:   config,
		client:   client,
		inFlight: newInflightGroup(),
	}, nil
}

func (h *Outbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	h.access.Lock()
	if h.closed {
		h.access.Unlock()
		return errOutboundClosed
	}
	if h.started {
		h.access.Unlock()
		return nil
	}
	if err := h.client.Start(); err != nil {
		h.access.Unlock()
		return E.Cause(err, "mieru: start client")
	}
	h.started = true
	h.dialed.Store(false)
	h.access.Unlock()
	// Add may run the cleanup synchronously if the scope is already closing;
	// Close takes the same lock, so it must not be held here.
	scope.Add(h.Close)
	h.logger.Info("mieru client started")
	return nil
}

// Close stops the SDK client.
//
// This is deliberately an explicit Stop, not common.Close: mieruclient.Client
// implements neither io.Closer nor an Upstream accessor, so common.Close
// silently does nothing (the defect the first downstream integrations shipped).
//
// Stop closes the SDK Mux, and Mux.Close closes every underlay it tracks;
// Session.Close is called by baseUnderlay.Close for each session in the
// underlay. That is what terminates connections that are still established
// when the outbound is torn down.
func (h *Outbound) Close() error {
	h.closeOnce.Do(func() {
		h.access.Lock()
		h.closed = true
		client := h.client
		h.drainInFlightLocked()
		h.access.Unlock()
		h.closeErr = client.Stop()
	})
	return h.closeErr
}

// drainInFlightLocked waits until every dial that snapshotted the client has
// returned. The caller must hold the write lock, so no new dial can be added.
func (h *Outbound) drainInFlightLocked() {
	if !h.inFlight.wait(dialDrainTimeout) {
		h.logger.Warn("mieru: a dial did not finish within ", dialDrainTimeout, " during teardown")
	}
}

// InterfaceUpdated rebuilds the client after a network change.
//
// Mieru's underlays are long-lived and bound to the route that existed when
// they were dialled. The SDK client cannot be restarted after Stop, so the
// only correct way to make new traffic use the new network is a fresh client
// with the same configuration; the old one is stopped, which closes its
// sessions. Holding the write lock across the swap means no dial can observe
// a half-replaced client.
func (h *Outbound) InterfaceUpdated(ctx context.Context) {
	h.access.Lock()
	defer h.access.Unlock()
	if h.closed || !h.started {
		return
	}
	if !h.dialed.Load() {
		// The startup network observation arrives before the first dial. The
		// SDK has not created an underlay yet, so stopping and starting the
		// client here would only add a transient unavailable window (and a
		// full Mux teardown) before any traffic has been attempted.
		return
	}
	h.drainInFlightLocked()
	next := mieruclient.NewClient()
	if err := next.Store(h.config); err != nil {
		h.logger.ErrorContext(ctx, "mieru: rebuild client config after network change: ", err)
		return
	}
	if err := next.Start(); err != nil {
		h.logger.ErrorContext(ctx, "mieru: restart client after network change: ", err)
		return
	}
	previous := h.client
	h.client = next
	h.dialed.Store(false)
	if err := previous.Stop(); err != nil {
		h.logger.WarnContext(ctx, "mieru: stop previous client after network change: ", err)
	}
	h.logger.InfoContext(ctx, "mieru client restarted after network change")
}

func (h *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		h.logger.InfoContext(ctx, "outbound connection to ", destination)
		destinationSpec, err := toNetAddrSpec(destination, N.NetworkTCP)
		if err != nil {
			return nil, err
		}
		return h.dial(ctx, destinationSpec)
	case N.NetworkUDP:
		h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
		destinationSpec, err := toNetAddrSpec(destination, N.NetworkUDP)
		if err != nil {
			return nil, err
		}
		streamConn, err := h.dial(ctx, destinationSpec)
		if err != nil {
			return nil, err
		}
		return newUDPStreamConn(streamConn, destination), nil
	default:
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
}

func (h *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination
	h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	destinationSpec, err := toNetAddrSpec(destination, N.NetworkUDP)
	if err != nil {
		return nil, err
	}
	streamConn, err := h.dial(ctx, destinationSpec)
	if err != nil {
		return nil, err
	}
	return newUOTPacketConn(streamConn), nil
}

// dial calls the SDK client under the lifecycle read lock, so Stop cannot run
// concurrently with a dial and leave a session attached to a closed Mux.
func (h *Outbound) dial(ctx context.Context, destination mierumodel.NetAddrSpec) (net.Conn, error) {
	h.access.RLock()
	if h.closed {
		h.access.RUnlock()
		return nil, errOutboundClosed
	}
	if !h.started {
		h.access.RUnlock()
		return nil, E.New("mieru: outbound has not started")
	}
	h.inFlight.begin()
	client := h.client
	h.dialed.Store(true)
	h.access.RUnlock()
	defer h.inFlight.done()
	conn, err := client.DialContext(ctx, destination)
	if err != nil {
		if errors.Is(err, mieruclient.ErrClientIsNotRunning) {
			return nil, E.Cause(err, "mieru: dial")
		}
		return nil, err
	}
	return conn, nil
}
