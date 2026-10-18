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
var errFallbackStale = E.New("mieru: fallback generation changed")

// Outbound is the Mieru client.
//
// The official SDK owns the protocol and its internal multiplexer. This
// adapter owns the fork's dialer (which resolves the server and applies
// domain_resolver / network_strategy / bind_interface / routing_mark), the
// lifecycle, and the translation between Mieru's SOCKS5 request/response
// messages and sing-box's connection contract.
//
// With fallback disabled the object has exactly one transportRuntime and walks
// the same path as before. With fallback enabled it owns two independent
// runtime generations (one TCP, one UDP) and a small selector; the selector
// chooses only a NEW session and never stops either SDK client on a normal
// preference change.
type Outbound struct {
	outbound.Adapter
	logger log.ContextLogger
	// primarySpec/secondarySpec are the immutable inputs for each generation.
	// secondarySpec is nil for a legacy single-transport outbound.
	primarySpec   *transportSpec
	secondarySpec *transportSpec

	primary   *transportRuntime
	secondary *transportRuntime
	fallback  *fallbackSelector

	// config/client/packetDialer are the primary aliases. They preserve the
	// existing single-client code paths and the private hooks used by the
	// lifecycle tests; the actual owners are primary (and secondary).
	config       *mieruclient.ClientConfig
	client       mieruclient.Client
	packetDialer *serverDialer

	access     sync.RWMutex
	started    bool
	closed     bool
	generation uint64
	// inFlight counts DialContext calls between snapshotting the transport and
	// returning its connection. Close and InterfaceUpdated take the write lock
	// first, which prevents new calls, then drain this counter before stopping
	// the generation.
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
	secondaryOptions, fallbackEnabled, err := resolveFallbackOptions(options)
	if err != nil {
		return nil, err
	}
	primarySpec, err := newTransportSpec(ctx, "primary", options)
	if err != nil {
		return nil, err
	}
	var secondarySpec *transportSpec
	if fallbackEnabled {
		secondarySpec, err = newTransportSpec(ctx, "secondary", secondaryOptions)
		if err != nil {
			return nil, err
		}
	}
	primary, err := newTransportRuntime(primarySpec)
	if err != nil {
		return nil, err
	}
	var secondary *transportRuntime
	if secondarySpec != nil {
		secondary, err = newTransportRuntime(secondarySpec)
		if err != nil {
			_ = primary.stop()
			return nil, err
		}
	}
	outboundInstance := &Outbound{
		Adapter:       outbound.NewAdapterWithDialerOptions(C.TypeMieru, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.DialerOptions, options.ServerIsDomain()),
		logger:        logger,
		primarySpec:   primarySpec,
		secondarySpec: secondarySpec,
		primary:       primary,
		secondary:     secondary,
		generation:    1,
		inFlight:      newInflightGroup(),
	}
	if fallbackEnabled {
		outboundInstance.fallback = newFallbackSelector(outboundInstance.generation)
	}
	outboundInstance.setAliasesLocked()
	return outboundInstance, nil
}

// newTransportSpec captures the immutable inputs for one primary/secondary
// SDK generation. No socket or SDK client is created here.
func newTransportSpec(ctx context.Context, label string, options option.MieruOutboundOptions) (*transportSpec, error) {
	rawDialer, err := dialer.New(ctx, options.DialerOptions, M.IsDomainName(options.Server))
	if err != nil {
		return nil, err
	}
	resolver, err := newResolverBridge(ctx, options.DialerOptions)
	if err != nil {
		return nil, err
	}
	return &transportSpec{label: label, options: options, dialer: rawDialer, resolver: resolver}, nil
}

// setAliasesLocked keeps the legacy single-client fields pointing at the
// current primary generation. The caller must hold access (or be constructing
// the object before publication).
func (h *Outbound) setAliasesLocked() {
	if h.primary == nil {
		h.config = nil
		h.client = nil
		h.packetDialer = nil
		return
	}
	h.config = h.primary.config
	h.client = h.primary.client
	h.packetDialer = h.primary.packetDialer
}

func (h *Outbound) ensureRuntimesLocked() error {
	if h.primary == nil {
		primary, err := newTransportRuntime(h.primarySpec)
		if err != nil {
			return err
		}
		h.primary = primary
	}
	if h.secondarySpec != nil && h.secondary == nil {
		secondary, err := newTransportRuntime(h.secondarySpec)
		if err != nil {
			_ = h.primary.stop()
			h.primary = nil
			h.setAliasesLocked()
			return err
		}
		h.secondary = secondary
	}
	h.setAliasesLocked()
	return nil
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
	if err := h.ensureRuntimesLocked(); err != nil {
		h.access.Unlock()
		return E.Cause(err, "mieru: prepare client")
	}
	if err := h.primary.start(); err != nil {
		h.stopRuntimesLocked()
		h.access.Unlock()
		return E.Cause(err, "mieru: start primary client")
	}
	if h.secondary != nil {
		if err := h.secondary.start(); err != nil {
			h.stopRuntimesLocked()
			h.access.Unlock()
			return E.Cause(err, "mieru: start fallback client")
		}
	}
	h.started = true
	h.generation++
	h.dialed.Store(false)
	fallbackEnabled := h.secondary != nil
	fallbackTransport := ""
	if h.secondary != nil {
		fallbackTransport = h.secondary.transport.String()
	}
	if h.fallback != nil {
		h.fallback.reset(h.generation)
	}
	h.access.Unlock()
	// Add may run the cleanup synchronously if the scope is already closing;
	// Close takes the same lock, so it must not be held here.
	scope.Add(h.Close)
	h.access.Lock()
	closedDuringStart := h.closed
	h.access.Unlock()
	if closedDuringStart {
		return E.New("mieru: outbound was closed while starting")
	}
	if fallbackEnabled {
		h.logger.Info("mieru client started (fallback ", fallbackTransport, " enabled)")
	} else {
		h.logger.Info("mieru client started")
	}
	return nil
}

// stopRuntimesLocked releases a generation that may have been only partially
// started. It is called with the write lock held and leaves the aliases nil;
// the next Start rebuilds fresh SDK clients (a stopped Client must not be
// restarted).
func (h *Outbound) stopRuntimesLocked() {
	primary := h.primary
	secondary := h.secondary
	h.primary = nil
	h.secondary = nil
	h.started = false
	h.setAliasesLocked()
	if primary != nil {
		_ = primary.stop()
	}
	if secondary != nil {
		_ = secondary.stop()
	}
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
		if h.fallback != nil {
			h.fallback.close()
		}
		h.drainInFlightLocked()
		primary := h.primary
		secondary := h.secondary
		h.access.Unlock()
		var stopErrors []error
		if primary != nil {
			if err := primary.stop(); err != nil {
				stopErrors = append(stopErrors, E.Cause(err, "mieru: stop primary client"))
			}
		}
		if secondary != nil {
			if err := secondary.stop(); err != nil {
				stopErrors = append(stopErrors, E.Cause(err, "mieru: stop fallback client"))
			}
		}
		h.closeErr = E.Errors(stopErrors...)
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
		// SDK has not created an underlay yet, so stopping and starting a
		// client here would only add a transient unavailable window before any
		// traffic has been attempted.
		return
	}
	h.drainInFlightLocked()
	nextPrimary, err := newTransportRuntime(h.primarySpec)
	if err != nil {
		h.logger.ErrorContext(ctx, "mieru: rebuild primary config after network change: ", err)
		return
	}
	var nextSecondary *transportRuntime
	if h.secondarySpec != nil {
		nextSecondary, err = newTransportRuntime(h.secondarySpec)
		if err != nil {
			h.logger.ErrorContext(ctx, "mieru: rebuild fallback config after network change: ", err)
			_ = nextPrimary.stop()
			return
		}
	}
	if err = nextPrimary.start(); err != nil {
		h.logger.ErrorContext(ctx, "mieru: restart primary client after network change: ", err)
		_ = nextPrimary.stop()
		if nextSecondary != nil {
			_ = nextSecondary.stop()
		}
		return
	}
	if nextSecondary != nil {
		if err = nextSecondary.start(); err != nil {
			h.logger.ErrorContext(ctx, "mieru: restart fallback client after network change: ", err)
			_ = nextPrimary.stop()
			_ = nextSecondary.stop()
			return
		}
	}
	previousPrimary := h.primary
	previousSecondary := h.secondary
	h.primary = nextPrimary
	h.secondary = nextSecondary
	h.generation++
	h.dialed.Store(false)
	if h.fallback != nil {
		h.fallback.reset(h.generation)
	}
	h.setAliasesLocked()
	if previousPrimary != nil {
		if err = previousPrimary.stop(); err != nil {
			h.logger.WarnContext(ctx, "mieru: stop previous primary client after network change: ", err)
		}
	}
	if previousSecondary != nil {
		if err = previousSecondary.stop(); err != nil {
			h.logger.WarnContext(ctx, "mieru: stop previous fallback client after network change: ", err)
		}
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
	primary := h.primary
	secondary := h.secondary
	fallback := h.fallback
	generation := h.generation
	h.dialed.Store(true)
	h.access.RUnlock()
	defer h.inFlight.done()
	if primary == nil {
		return nil, E.New("mieru: outbound has no transport")
	}
	if fallback == nil || secondary == nil {
		conn, class, err := primary.dial(ctx, destination)
		if err != nil {
			if errors.Is(err, mieruclient.ErrClientIsNotRunning) {
				return nil, wrapFailureFacts(E.Cause(err, "mieru: dial"), class)
			}
			return nil, wrapFailureFacts(err, class)
		}
		return conn, nil
	}
	return h.dialWithFallback(ctx, destination, primary, secondary, fallback, generation)
}

func (h *Outbound) dialWithFallback(ctx context.Context, destination mierumodel.NetAddrSpec, primary *transportRuntime, secondary *transportRuntime, selector *fallbackSelector, generation uint64) (net.Conn, error) {
	choice, probe, ok := selector.choose(generation)
	if !ok {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Do not wait for Close/InterfaceUpdated's write lock here: a dial
		// that lost its generation must finish promptly so the owner can
		// drain in-flight work.
		if h.access.TryRLock() {
			closed := h.closed
			h.access.RUnlock()
			if closed {
				return nil, errOutboundClosed
			}
		}
		return nil, errFallbackStale
	}
	selected := primary
	if choice == fallbackSecondary {
		selected = secondary
	}
	conn, class, err := selected.dial(ctx, destination)
	selector.report(generation, choice, probe, class)
	if err == nil {
		return h.acceptGeneratedConn(conn, generation)
	}
	if class != dialTransportFailure {
		switch class {
		case dialTargetFailure:
			h.logger.Debug("mieru: target failure on ", selected.label, " transport; not switching: ", err)
		case dialAmbiguousFailure:
			h.logger.Debug("mieru: ambiguous handshake failure on ", selected.label, " transport; not switching: ", err)
		}
		return nil, wrapFailureFacts(err, class)
	}
	if choice == fallbackSecondary {
		return nil, wrapFailureFacts(err, class)
	}
	// The primary failed in a class that may be a transport failure. Preserve
	// the caller's deadline and try the other transport exactly once.
	if ctx.Err() != nil {
		return nil, wrapFailureFacts(E.Cause(err, "mieru: primary transport failed"), class)
	}
	h.logger.Info("mieru: primary ", primary.transport.String(), " transport failed, trying fallback ", secondary.transport.String())
	secondaryConn, secondaryClass, secondaryErr := secondary.dial(ctx, destination)
	selector.report(generation, fallbackSecondary, false, secondaryClass)
	if secondaryErr == nil {
		return h.acceptGeneratedConn(secondaryConn, generation)
	}
	selector.reportBothFailed(generation, class, secondaryClass)
	aggregate := E.Errors(
		E.Cause(err, "mieru: primary transport"),
		E.Cause(secondaryErr, "mieru: fallback transport"),
	)
	switch secondaryClass {
	case dialTargetFailure, dialTransportFailure:
		return nil, wrapFailureFacts(aggregate, secondaryClass)
	default:
		return nil, aggregate
	}
}

// acceptGeneratedConn enforces the ownership boundary after the network may
// have changed while the dial was in flight. The selector already ignores a
// stale report; without this check the route could still receive a connection
// created for the previous generation. Closing it here is the only place the
// adapter can enforce Close/epoch wins without leaking the SDK session.
func (h *Outbound) acceptGeneratedConn(conn net.Conn, generation uint64) (net.Conn, error) {
	if !h.access.TryRLock() {
		if conn != nil {
			_ = conn.Close()
		}
		return nil, errOutboundClosed
	}
	closed := h.closed
	current := h.generation
	h.access.RUnlock()
	if closed {
		if conn != nil {
			_ = conn.Close()
		}
		return nil, errOutboundClosed
	}
	if current != generation {
		if conn != nil {
			_ = conn.Close()
		}
		return nil, errFallbackStale
	}
	if conn == nil {
		return nil, E.New("mieru: transport returned no connection")
	}
	return conn, nil
}
