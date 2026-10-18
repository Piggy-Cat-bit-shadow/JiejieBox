package mieru

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/common/listener"
	"github.com/sagernet/sing-box/common/uot"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"

	mierucommon "github.com/enfein/mieru/v3/apis/common"
	mieruconstant "github.com/enfein/mieru/v3/apis/constant"
	mierumodel "github.com/enfein/mieru/v3/apis/model"
	mieruserver "github.com/enfein/mieru/v3/apis/server"
)

// closeWaitTimeout bounds how long Close waits for the accept loop and active
// sessions to finish. The SDK's Stop closes listeners and tears down underlays
// synchronously, so this is a backstop for a destination that is slow to
// observe the closed stream, not the normal path.
const closeWaitTimeout = 5 * time.Second

func RegisterInbound(registry *inbound.Registry) {
	inbound.Register[option.MieruInboundOptions](registry, C.TypeMieru, NewInbound)
}

// Inbound is the Mieru server.
//
// The official SDK owns the protocol: it authenticates the underlay, decrypts
// sessions, and exposes accepted sessions plus the SOCKS5 request each carried.
// This adapter owns everything the fork owns: sockets (through the listener
// factories, so ListenOptions actually applies), routing metadata, and
// lifecycle.
type Inbound struct {
	inbound.Adapter
	ctx    context.Context
	cancel context.CancelFunc
	router adapter.ConnectionRouterEx
	logger log.ContextLogger

	server        mieruserver.Server
	listenOptions option.ListenOptions

	access  sync.Mutex
	started bool
	closed  bool

	closeOnce sync.Once
	closeErr  error

	acceptDone chan struct{}
	active     connRegistry
	listeners  listenerRegistry
	handlers   sync.WaitGroup
}

func NewInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.MieruInboundOptions) (adapter.Inbound, error) {
	config, err := buildServerConfig(options)
	if err != nil {
		return nil, err
	}
	inboundContext, cancel := context.WithCancel(ctx)
	h := &Inbound{
		Adapter:       inbound.NewAdapter(C.TypeMieru, tag),
		ctx:           inboundContext,
		cancel:        cancel,
		router:        uot.NewRouter(router, logger),
		logger:        logger,
		listenOptions: options.ListenOptions,
	}
	// The factories are attached BEFORE Store so the server object never holds
	// a configuration that could bind with the SDK's own listen semantics.
	streamFactory := &streamListenerFactory{ctx: inboundContext, options: options.ListenOptions, logger: logger, listeners: &h.listeners}
	packetFactory := &packetListenerFactory{ctx: inboundContext, options: options.ListenOptions, logger: logger, listeners: &h.listeners}
	config.StreamListenerFactory = streamFactory
	config.PacketListenerFactory = packetFactory

	server := mieruserver.NewServer()
	if err = server.Store(config); err != nil {
		cancel()
		return nil, E.Cause(err, "mieru: store server config")
	}
	h.server = server
	h.active = connRegistry{conns: make(map[net.Conn]struct{})}
	h.listeners = listenerRegistry{closers: make(map[io.Closer]struct{})}
	return h, nil
}

func (h *Inbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	h.access.Lock()
	if h.closed {
		h.access.Unlock()
		return E.New("mieru: inbound is closed")
	}
	if h.started {
		h.access.Unlock()
		return nil
	}
	if err := h.ctx.Err(); err != nil {
		h.access.Unlock()
		return err
	}
	if err := h.server.Start(); err != nil {
		// The SDK closes the listeners it managed to register on a partial
		// start, but its packet underlay does not close the PacketConn. Close
		// every listener this factory created before returning the error.
		h.listeners.closeAll()
		h.listeners = listenerRegistry{closers: make(map[io.Closer]struct{})}
		h.access.Unlock()
		return E.Cause(err, "mieru: start server")
	}
	h.started = true
	h.acceptDone = make(chan struct{})
	h.access.Unlock()
	// The accept loop must exist before the cleanup is registered: Add runs
	// the cleanup synchronously when the scope has already begun closing, and
	// Close waits on acceptDone.
	go h.acceptLoop()
	scope.Add(h.Close)
	// scope.Add runs the cleanup synchronously when the scope is already
	// closing. In that case the inbound is now stopped; reporting nil would
	// tell the caller a closed server started successfully.
	h.access.Lock()
	closedDuringStart := h.closed
	h.access.Unlock()
	if closedDuringStart {
		return E.New("mieru: inbound was closed while starting")
	}
	h.logger.Info("mieru server started")
	return nil
}

// Close stops accepting, stops the SDK server (which closes listeners and
// underlays), then closes any accepted session still in flight.
//
// The SDK documents that Stop does not terminate established connections as a
// user-facing convenience; the Mux it closes does tear down its underlays, but
// an accepted session can still be inside route handling when Stop returns.
// Closing the tracked sessions here is what makes "the inbound is closed" true
// for the traffic that entered through it, instead of leaving the destination
// copy waiting on a stream the server no longer owns.
func (h *Inbound) Close() error {
	h.closeOnce.Do(func() {
		h.access.Lock()
		h.closed = true
		h.access.Unlock()

		h.cancel()
		h.closeErr = h.server.Stop()
		// PacketUnderlay.Close only interrupts its read loop; it does not
		// close the PacketConn returned by the listener factory. Closing the
		// tracked listeners here is what actually releases the UDP port (and
		// is harmless for the TCP listener the SDK already closed).
		h.listeners.closeAll()
		h.active.closeAll()
		acceptLoopStopped := true
		if h.acceptDone != nil {
			select {
			case <-h.acceptDone:
			case <-time.After(closeWaitTimeout):
				acceptLoopStopped = false
				h.logger.Warn("mieru accept loop did not stop within ", closeWaitTimeout)
			}
		}
		// Only wait for handlers after the accept loop has provably exited.
		// acceptLoop calls handlers.Add immediately before spawning a handler,
		// so a Wait racing a still-running accept loop would be an Add/Wait
		// race on sync.WaitGroup.
		if !acceptLoopStopped {
			return
		}
		done := make(chan struct{})
		go func() {
			h.handlers.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(closeWaitTimeout):
			h.logger.Warn("mieru sessions did not finish within ", closeWaitTimeout)
		}
	})
	return h.closeErr
}

func (h *Inbound) acceptLoop() {
	defer close(h.acceptDone)
	for {
		conn, request, err := h.server.Accept()
		if err != nil {
			// Stop() flips IsRunning before it closes the Mux, so this is the
			// normal shutdown path and must not be logged as a failure.
			if h.ctx.Err() != nil || !h.server.IsRunning() {
				return
			}
			// Mux.Accept returns io.ErrClosedPipe only when the accept-error
			// latch or the Mux itself is closed. While the server claims to be
			// running that is a dead protocol, not a failed handshake; retrying
			// it would spin forever.
			// The SDK's Request.ReadFromSocks5 surfaces io.EOF or
			// io.ErrUnexpectedEOF from Session.Read when a client disappears;
			// those are per-connection. io.ErrClosedPipe is the Mux accept
			// latch set when a listener failed, so it is terminal.
			if errors.Is(err, io.ErrClosedPipe) {
				h.logger.Error("mieru accept loop stopped: ", err)
				return
			}
			// Everything else is per-connection: a bad credential, a malformed
			// request, a client that disappeared. Those are expected under
			// normal load and under attack, so there is deliberately no
			// backoff here - a delay would let one misbehaving client slow down
			// every legitimate connection behind it.
			h.logger.Debug("mieru handshake failed: ", err)
			continue
		}
		h.handlers.Add(1)
		go func() {
			defer h.handlers.Done()
			h.handleConnection(conn, request)
		}()
	}
}

func (h *Inbound) handleConnection(conn net.Conn, request *mierumodel.Request) {
	tracked := &trackedConn{Conn: conn, registry: &h.active}
	if !h.active.add(tracked) {
		_ = conn.Close()
		return
	}
	handedOff := false
	// The route layer takes ownership of a connection as soon as the Ex entry
	// returns: it starts the copy goroutines and expects the connection to
	// stay open. Closing it here would tear down every flow one instant after
	// it was accepted. Only the paths that never hand it off close it.
	defer func() {
		if !handedOff {
			_ = tracked.Close()
		}
	}()

	ctx := log.ContextWithNewID(h.ctx)

	destination, destinationErr := fromNetAddrSpec(mierumodel.NetAddrSpec{AddrSpec: request.DstAddr})
	reply := mieruconstant.Socks5ReplySuccess
	switch {
	case request.Command != mieruconstant.Socks5ConnectCmd && request.Command != mieruconstant.Socks5UDPAssociateCmd:
		reply = mieruconstant.Socks5ReplyCommandNotSupported
	case destinationErr != nil:
		reply = mieruconstant.Socks5ReplyServerFailure
	}
	// The client waits for the SOCKS5 response before reading application
	// data (both in the standard and the no-wait handshake mode, where it is
	// batched with the first write). The bind address is not used by Mieru,
	// but the reply code must reflect a request this server cannot route; a
	// synthetic success followed by a close is a false success.
	response := &mierumodel.Response{
		Reply: reply,
		BindAddr: mierumodel.AddrSpec{
			IP:   net.IPv4zero,
			Port: 0,
		},
	}
	if err := response.WriteToSocks5(conn); err != nil {
		h.logger.DebugContext(ctx, "failed to write mieru response: ", err)
		return
	}
	if reply != mieruconstant.Socks5ReplySuccess {
		if destinationErr != nil {
			h.logger.DebugContext(ctx, "failed to parse mieru destination: ", destinationErr)
		} else {
			h.logger.WarnContext(ctx, "unsupported mieru command: ", request.Command)
		}
		return
	}

	metadata := adapter.InboundContext{
		Inbound:     h.Tag(),
		InboundType: h.Type(),
	}
	//nolint:staticcheck
	metadata.InboundDetour = h.listenOptions.Detour
	if remoteAddr := conn.RemoteAddr(); remoteAddr != nil {
		metadata.Source = M.SocksaddrFromNet(remoteAddr)
	}
	if localAddr := conn.LocalAddr(); localAddr != nil {
		metadata.OriginDestination = M.SocksaddrFromNet(localAddr).Unwrap()
	}
	metadata.Destination = destination
	if userContext, ok := conn.(mierucommon.UserContext); ok {
		metadata.User = userContext.UserName()
	}
	if metadata.User != "" {
		h.logger.InfoContext(ctx, "[", metadata.User, "] inbound connection from ", metadata.Source, " to ", metadata.Destination)
	} else {
		h.logger.InfoContext(ctx, "inbound connection from ", metadata.Source, " to ", metadata.Destination)
	}

	switch request.Command {
	case mieruconstant.Socks5ConnectCmd:
		handedOff = true
		h.router.RouteConnectionEx(ctx, tracked, metadata, nil)
	case mieruconstant.Socks5UDPAssociateCmd:
		handedOff = true
		h.router.RoutePacketConnectionEx(ctx, newInboundPacketConn(tracked), metadata, nil)
	}
}

// trackedConn removes itself from the inbound's registry when the route layer
// closes it, so the registry holds only live flows.
//
// It forwards Upstream so the copy layer can still find the real writer for
// capability checks and early buffer growth; the Mieru session itself offers
// no syscall or splice capability, so wrapping loses nothing the protocol
// could have provided.
type trackedConn struct {
	net.Conn
	registry *connRegistry
}

var _ mierucommon.UserContext = (*trackedConn)(nil)

func (c *trackedConn) Close() error {
	c.registry.remove(c)
	return c.Conn.Close()
}

func (c *trackedConn) Upstream() any {
	return c.Conn
}

// UserName preserves the SDK's authenticated-user capability across the
// registry wrapper. A downstream tracker that type-asserts UserContext on the
// handed-off connection must still see the same name the router metadata got.
func (c *trackedConn) UserName() string {
	if userContext, ok := c.Conn.(mierucommon.UserContext); ok {
		return userContext.UserName()
	}
	return ""
}

// streamListenerFactory is the SDK's StreamListenerFactory backed by the
// fork's ListenOptions.
//
// The context the SDK passes in is its own Background-derived Mux context,
// which carries none of the services ListenNetworkNamespace looks up. The
// inbound's construction context is used for socket setup; the SDK still owns
// closing the returned listener when the Mux shuts down.
type streamListenerFactory struct {
	ctx       context.Context
	options   option.ListenOptions
	logger    logger.ContextLogger
	listeners *listenerRegistry
}

func (f *streamListenerFactory) Listen(_ context.Context, network string, address string) (net.Listener, error) {
	port, err := portFromAddress(address)
	if err != nil {
		return nil, E.Cause(err, "mieru: stream listener address ", address)
	}
	tcpListener, err := listener.ListenTCPWithOptions(f.ctx, f.options, port, false)
	if err != nil {
		return nil, E.Cause(err, "mieru: listen tcp ", port)
	}
	tracked := &trackedListener{Listener: tcpListener, registry: f.listeners}
	if !f.listeners.add(tracked) {
		_ = tcpListener.Close()
		return nil, E.New("mieru: inbound was closed while creating the TCP listener")
	}
	f.logger.Info("mieru tcp server started at ", tcpListener.Addr())
	return tracked, nil
}

type packetListenerFactory struct {
	ctx       context.Context
	options   option.ListenOptions
	logger    logger.ContextLogger
	listeners *listenerRegistry
}

func (f *packetListenerFactory) ListenPacket(_ context.Context, network string, address string) (net.PacketConn, error) {
	port, err := portFromAddress(address)
	if err != nil {
		return nil, E.Cause(err, "mieru: packet listener address ", address)
	}
	packetConn, err := listener.ListenUDPWithOptions(f.ctx, f.options, port, false, net.ListenConfig{})
	if err != nil {
		return nil, E.Cause(err, "mieru: listen udp ", port)
	}
	tracked := &trackedPacketConn{PacketConn: packetConn, registry: f.listeners}
	if !f.listeners.add(tracked) {
		_ = packetConn.Close()
		return nil, E.New("mieru: inbound was closed while creating the UDP listener")
	}
	f.logger.Info("mieru udp server started at ", packetConn.LocalAddr())
	return tracked, nil
}

func portFromAddress(address string) (uint16, error) {
	_, portString, err := net.SplitHostPort(address)
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		return 0, err
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("port %d is out of range", port)
	}
	return uint16(port), nil
}

// connRegistry tracks accepted sessions so Close can terminate the ones the
// SDK does not.
type connRegistry struct {
	access sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
}

func (r *connRegistry) add(conn net.Conn) bool {
	r.access.Lock()
	defer r.access.Unlock()
	if r.closed {
		return false
	}
	r.conns[conn] = struct{}{}
	return true
}

func (r *connRegistry) remove(conn net.Conn) {
	r.access.Lock()
	delete(r.conns, conn)
	r.access.Unlock()
}

func (r *connRegistry) closeAll() {
	r.access.Lock()
	r.closed = true
	conns := make([]net.Conn, 0, len(r.conns))
	for conn := range r.conns {
		conns = append(conns, conn)
		delete(r.conns, conn)
	}
	r.access.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

// listenerRegistry owns the listeners the factories created.
//
// The SDK's PacketUnderlay.Close interrupts its read loop with a deadline but
// does not close the underlying PacketConn. Without this registry the UDP port
// stays bound after Inbound.Close, so a reload cannot rebind it and the process
// leaks one socket per Mieru UDP inbound. TCP listeners are also registered:
// closing them twice is harmless, and it keeps the ownership rule uniform.
type listenerRegistry struct {
	access  sync.Mutex
	closed  bool
	closers map[io.Closer]struct{}
}

func (r *listenerRegistry) add(closer io.Closer) bool {
	r.access.Lock()
	defer r.access.Unlock()
	if r.closed {
		return false
	}
	if r.closers == nil {
		r.closers = make(map[io.Closer]struct{})
	}
	r.closers[closer] = struct{}{}
	return true
}

func (r *listenerRegistry) remove(closer io.Closer) {
	r.access.Lock()
	delete(r.closers, closer)
	r.access.Unlock()
}

func (r *listenerRegistry) count() int {
	r.access.Lock()
	defer r.access.Unlock()
	return len(r.closers)
}

func (r *listenerRegistry) closeAll() {
	r.access.Lock()
	r.closed = true
	closers := make([]io.Closer, 0, len(r.closers))
	for closer := range r.closers {
		closers = append(closers, closer)
		delete(r.closers, closer)
	}
	r.access.Unlock()
	for _, closer := range closers {
		_ = closer.Close()
	}
}

type trackedListener struct {
	net.Listener
	registry  *listenerRegistry
	closeOnce sync.Once
	closeErr  error
}

func (l *trackedListener) Close() error {
	l.closeOnce.Do(func() {
		if l.registry != nil {
			l.registry.remove(l)
		}
		l.closeErr = l.Listener.Close()
	})
	return l.closeErr
}

type trackedPacketConn struct {
	net.PacketConn
	registry  *listenerRegistry
	closeOnce sync.Once
	closeErr  error
}

func (c *trackedPacketConn) Close() error {
	c.closeOnce.Do(func() {
		if c.registry != nil {
			c.registry.remove(c)
		}
		c.closeErr = c.PacketConn.Close()
	})
	return c.closeErr
}
