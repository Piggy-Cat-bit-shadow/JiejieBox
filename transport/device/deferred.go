package device

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"

	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/buf"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
)

// Deferred is a Device that has not built its userspace stack yet.
//
// # Why this exists
//
// The non-system device is created in the endpoint's Initialize stage, and Initialize runs for
// EVERY configured endpoint - including the ones that will never carry a packet. The stack it builds
// is only reachable through a live tunnel session, so an unused node pays for an object nothing can
// use.
//
// The measured cost is a Start-up-shape one: 9.7 KB and 34 objects per node at Initialize, plus
// 43.6 KB at first configuration (reports/R-heavy.md, 5 samples). That is the ENTIRE prize for
// deferral, which is why this type does NOT invent a second lifecycle policy, a timer, or a
// background releaser. It builds on the first demand, keeps ONE generation at a time, and offers an
// explicit Release for a caller that has already proved it holds no flows.
//
// # The one product caller of Release
//
// protocol/masque calls Release at a tunnel session's retirement transition, and only there: the
// session that could carry the stack's flows has ended, no newer session has begun, and the
// endpoint's epoch state machine (§ session_release.go there) has established that ordering. A
// caller must not treat "nothing dialled recently" as permission - a CONNECT-IP tunnel may carry
// remote-initiated flows that produce no local dial at all.
//
// # What stays stable
//
// The caller keeps THIS value forever. The inner device is replaceable; the facade never hands out
// the inner pointer as the caller's identity. MTU, addresses, the return path, the packet writer and
// the rebuild template live here, so they survive a generation change - and a caller that attached a
// return path before the stack existed is not silently dropped.
//
// # What a generation is
//
// One built inner device. `generation` increases on every build, so an object that captured a
// generation can be told apart from a later one. An operation counter covers the dials and inbound
// writes that are INSIDE the stack right now, and an explicit Release refuses while one is running.
// Connections that were already returned are not tracked here on purpose: wrapping them to count
// them would change the concrete type the routing layer receives, and a live connection is a fact
// only the tunnel session can enumerate. Close is the hard path and waits for nothing - it closes
// the stack and fails its connections, exactly as it does today.
//
// # Why there are two locks
//
// access guards the published state and is never held across a build. buildAccess serializes
// construction, Start and configuration application, so a demand that arrives mid-build waits and
// then finds the published generation instead of building a second one. Close takes only access:
// waiting for buildAccess would let a slow construction - or a factory that never returns - block
// teardown, which is the deadline a shutdown path cannot have.
type Deferred interface {
	Device
	// Built reports whether an inner generation currently exists.
	Built() bool
	// Generation is the id of the current (or last) inner device; 0 before the first build.
	Generation() uint64
	// InFlight counts the operations this facade is currently inside: an in-progress dial, an
	// in-progress inbound write, or a queue creation. It is the only release precondition the facade
	// can check for itself.
	InFlight() int
	// Release closes the inner device and returns to the unbuilt state.
	//
	// It refuses while an operation is inside the stack, while a build is still running, and for
	// good once an outbound queue has been handed to a caller (that queue's lifetime is not
	// observable here); it says so through the returned error and a caller that wants to wait can
	// retry. It does not stop a session, cancel a dial or close a return path: proving those are gone
	// is the caller's job, because only the caller knows about the tunnel session that carries the
	// flows. The return path, MTU, addresses and packet writer are facade state and survive.
	Release() error
	// Diagnostics reports the facade's own counters. It exists so a caller can tell "never built"
	// from "a release keeps being refused" without adding logging to the hot path.
	Diagnostics() DeferredDiagnostics
}

var _ Deferred = (*deferredDevice)(nil)

// rawInboundWriter is implemented by the userspace stack device: it writes inbound packets into the
// memory TUN without applying a return path of its own.
type rawInboundWriter interface {
	writeRawBuffers(packetBuffers []*buf.Buffer) error
	waitWriteRawBuffers(packetBuffers []*buf.Buffer) error
}

type deferredGeneration struct {
	id     uint64
	device Device
	raw    rawInboundWriter
}

type deferredDevice struct {
	baseDevice
	// The rebuild template. Configuration and MTU are the CURRENT values, kept beside it.
	factory       func(Options) (Device, error)
	options       Options
	configuration Configuration
	mtu           uint32
	inet4Address  netip.Addr
	inet6Address  netip.Addr

	// buildAccess serializes construction, Start and configuration application.
	buildAccess sync.Mutex
	access      sync.Mutex
	inner       *deferredGeneration
	// started records that Start was requested. A build that happens later applies it, in the same
	// order the endpoint uses (configuration, then Start).
	started    bool
	closed     bool
	generation uint64
	// inFlight counts published-generation operations (dial, inbound write, queue creation).
	// buildInFlight counts a construction that is running but not yet published. Release refuses
	// while either is non-zero: a build that is still working is an operation too, and letting
	// Release succeed there would be contradicted by the publish that follows it.
	inFlight      int
	buildInFlight int
	buildCount    int
	releaseNum    int
	queueFailures int
	// queueHandouts counts the outbound queues this facade handed to callers. A *tun.OutboundQueue
	// is owned by whoever asked for it: the device cannot observe its Close, and MemoryTun.Close
	// deliberately does NOT close caller-owned queues ("Caller-owned queues are closed by their
	// owners" in the pinned sing-tun). So a handed-out queue cannot be proven gone, and Release
	// refuses from the first handout on - see the Release doc.
	queueHandouts int
	lastError     error
	closeOnce     sync.Once
	closeErr      error
}

// DeferredDiagnostics is a bounded snapshot of the facade's own state, for the owner that needs to
// tell "not built yet" from "release keeps refusing".
type DeferredDiagnostics struct {
	Built         bool
	Generation    uint64
	InFlight      int
	Builds        int
	Releases      int
	QueueFailures int
	// QueueHandouts is how many caller-owned outbound queues this facade has returned. A non-zero
	// value is the permanent reason Release refuses: their lifetime is not observable from here.
	QueueHandouts int
	LastError     error
}

// NewDeferred wraps the non-system stack device in a facade that builds it on first demand.
//
// The system device is deliberately excluded: its construction has host side effects (routes,
// listeners, interface ownership) that a "build it later" policy would move across lifecycle
// boundaries the caller cannot see from here. The one caller that needs this (a MASQUE client
// endpoint) uses the userspace stack; the system path keeps calling New.
func NewDeferred(options Options) (Deferred, error) {
	if options.System {
		return nil, E.New("deferred construction is defined for the userspace stack only")
	}
	if options.Logger == nil {
		return nil, E.New("deferred device requires a logger")
	}
	deferred := &deferredDevice{
		options:       options,
		factory:       New,
		configuration: options.Configuration,
		mtu:           options.MTU,
	}
	deferred.inet4Address, deferred.inet6Address = firstAddresses(options.Configuration.Address)
	return deferred, nil
}

func (d *deferredDevice) Built() bool {
	d.access.Lock()
	defer d.access.Unlock()
	return d.inner != nil
}

func (d *deferredDevice) Generation() uint64 {
	d.access.Lock()
	defer d.access.Unlock()
	return d.generation
}

func (d *deferredDevice) InFlight() int {
	d.access.Lock()
	defer d.access.Unlock()
	return d.inFlight
}

func (d *deferredDevice) Diagnostics() DeferredDiagnostics {
	d.access.Lock()
	defer d.access.Unlock()
	return DeferredDiagnostics{
		Built:         d.inner != nil,
		Generation:    d.generation,
		InFlight:      d.inFlight,
		Builds:        d.buildCount,
		Releases:      d.releaseNum,
		QueueFailures: d.queueFailures,
		QueueHandouts: d.queueHandouts,
		LastError:     d.lastError,
	}
}

// recordError stores the last build/queue failure and reports it at error level, once.
//
// The facade has no other way to surface a failure to a caller whose interface method cannot return
// an error carrying diagnostics (NewOutboundQueue returns a queue or nothing). A silent nil would
// present a stack with no queue as a stack that simply has no queue.
func (d *deferredDevice) recordError(cause error) {
	d.access.Lock()
	d.lastError = cause
	d.access.Unlock()
	d.options.Logger.Error(E.Cause(cause, "deferred device"))
}

var (
	errDeviceClosed     = E.New("device is closed")
	errGenerationGone   = E.New("the inner device generation was released while the operation was starting")
	errQueueUnavailable = E.New("no inner device generation is available for an outbound queue")
)

// ensureGeneration builds the inner device when it does not exist yet.
//
// A build result is published only AFTER it is fully configured and started. A build that fails, or
// one that finishes after Close, is closed here and never published - so no caller can be handed a
// device that the next line would close.
func (d *deferredDevice) ensureGeneration() (*deferredGeneration, error) {
	d.access.Lock()
	if d.closed {
		d.access.Unlock()
		return nil, errDeviceClosed
	}
	if published := d.inner; published != nil {
		d.access.Unlock()
		return published, nil
	}
	d.access.Unlock()

	// Single flight: construction, Start and configuration application all serialize here. The
	// re-check after acquiring is what makes a concurrent demand share the generation instead of
	// building a second one.
	d.buildAccess.Lock()
	defer d.buildAccess.Unlock()
	d.access.Lock()
	if d.closed {
		d.access.Unlock()
		return nil, errDeviceClosed
	}
	if published := d.inner; published != nil {
		d.access.Unlock()
		return published, nil
	}
	buildOptions := d.options
	buildOptions.Configuration = d.configuration
	buildOptions.MTU = d.mtu
	buildOptions.System = false
	started := d.started
	// The build itself is an in-flight operation. Without this, an explicit Release that ran while
	// the factory was still working would find nothing to close, report success, and then be
	// contradicted by the publish a moment later.
	d.buildInFlight++
	d.access.Unlock()
	defer func() {
		d.access.Lock()
		d.buildInFlight--
		d.access.Unlock()
	}()

	inner, err := d.factory(buildOptions)
	if err != nil {
		cause := E.Cause(err, "build inner device")
		d.recordError(cause)
		return nil, cause
	}
	// Apply the state the caller may already have set while this generation did not exist, in the
	// same order the endpoint uses: configuration, then Start. Neither call can build anything, so
	// holding buildAccess here is bounded work on a fresh object.
	if err = inner.UpdateConfiguration(buildOptions.Configuration); err != nil {
		_ = inner.Close()
		cause := E.Cause(err, "configure inner device")
		d.recordError(cause)
		return nil, cause
	}
	if started {
		if err = inner.Start(); err != nil {
			_ = inner.Close()
			cause := E.Cause(err, "start inner device")
			d.recordError(cause)
			return nil, cause
		}
	}
	d.access.Lock()
	if d.closed {
		d.access.Unlock()
		_ = inner.Close()
		return nil, errDeviceClosed
	}
	// EVERYTHING the generation needs is initialised before it becomes visible. The raw writer used
	// to be assigned after the publish, which let a concurrent first inbound packet load a
	// generation whose raw field was still nil - a data race AND a silent fallback to the
	// return-path-applying write.
	raw, isRaw := inner.(rawInboundWriter)
	d.generation++
	generation := &deferredGeneration{id: d.generation, device: inner}
	if isRaw {
		generation.raw = raw
	}
	// Applied under the same lock that publishes it: a concurrent SetPacketWriter either ran before
	// this point (so d.packetWriter already holds its value) or will run after it (so it finds the
	// published generation and forwards). There is no window where it stores a new writer and then
	// has it overwritten by the value this build captured earlier.
	inner.SetPacketWriter(d.packetWriter)
	d.inner = generation
	d.buildCount++
	d.access.Unlock()
	return generation, nil
}

// beginOp acquires the current generation and records one in-flight operation until end runs.
//
// The counter is what makes an explicit Release safe against a dial or an inbound write that is
// already inside the stack. It deliberately does NOT track returned connections: wrapping them to
// count leases would replace the concrete connection type the routing layer receives, and a live
// connection is a fact only the tunnel session can enumerate. Release's contract therefore says the
// caller must prove its session and flows are gone.
func (d *deferredDevice) beginOp() (*deferredGeneration, func(), error) {
	// A release that wins between the build and the counter increment is retried a bounded number of
	// times: it is a race with the caller's own idle-release, not a real failure, and surfacing it
	// would make an inbound packet fail for no reason the caller could act on. Three attempts is the
	// bound; after that the caller sees the error and owns the retry.
	for attempt := 0; attempt < 3; attempt++ {
		generation, end, err := d.beginOpOnce()
		if err == nil {
			return generation, end, nil
		}
		if !errors.Is(err, errGenerationGone) {
			return nil, nil, err
		}
	}
	return nil, nil, errGenerationGone
}

func (d *deferredDevice) beginOpOnce() (*deferredGeneration, func(), error) {
	generation, err := d.ensureGeneration()
	if err != nil {
		return nil, nil, err
	}
	d.access.Lock()
	if d.closed || d.inner != generation {
		// The generation was closed or released between the build and this line. Reporting the
		// operation as started would leave a counter for a stack that no longer exists, and using
		// the generation would touch a closed device. The caller sees an error and may retry.
		d.access.Unlock()
		return nil, nil, errGenerationGone
	}
	d.inFlight++
	d.access.Unlock()
	var ended atomic.Bool
	return generation, func() {
		if ended.Swap(true) {
			return
		}
		d.access.Lock()
		if d.inFlight > 0 {
			d.inFlight--
		}
		d.access.Unlock()
	}, nil
}

func (d *deferredDevice) SetPacketWriter(writer PacketWriter) {
	d.access.Lock()
	defer d.access.Unlock()
	d.packetWriter = writer
	// Forwarded under the SAME lock that publishes a generation, so the two orderings that matter
	// are the only ones possible: this call either lands before a build reads packetWriter, or after
	// the build has published a generation it can forward to. It must not run after the build read
	// the old value but before the publish, or the old value would win.
	if d.inner != nil {
		d.inner.device.SetPacketWriter(writer)
	}
}

func (d *deferredDevice) Start() error {
	d.buildAccess.Lock()
	defer d.buildAccess.Unlock()
	d.access.Lock()
	if d.closed {
		d.access.Unlock()
		return errDeviceClosed
	}
	if d.started {
		d.access.Unlock()
		return nil
	}
	published := d.inner
	d.started = true
	d.access.Unlock()
	if published == nil {
		// No demand for a stack yet: the request is remembered and the build applies it.
		return nil
	}
	return published.device.Start()
}

func (d *deferredDevice) UpdateConfiguration(configuration Configuration) error {
	d.buildAccess.Lock()
	defer d.buildAccess.Unlock()
	d.access.Lock()
	if configuration.MTU != 0 {
		d.mtu = configuration.MTU
	}
	d.configuration = configuration
	inet4Address, inet6Address := firstAddresses(configuration.Address)
	d.inet4Address, d.inet6Address = inet4Address, inet6Address
	published := d.inner
	d.access.Unlock()
	// The configuration is stored even when there is no generation. Building a stack here would be
	// the very cost this type exists to avoid: a configuration update is not a demand for a stack,
	// it is state a stack built later will receive.
	if published == nil {
		return nil
	}
	return published.device.UpdateConfiguration(configuration)
}

func (d *deferredDevice) WriteInboundBuffers(packetBuffers []*buf.Buffer) error {
	return d.writeInbound(packetBuffers, false)
}

func (d *deferredDevice) WaitWriteInboundBuffers(packetBuffers []*buf.Buffer) error {
	return d.writeInbound(packetBuffers, true)
}

// writeInbound delivers inbound packets to the current generation, applying this facade's return
// path first.
//
// An inbound packet IS a demand: a MASQUE server may open a flow to our assigned address without
// anything being dialled first, and dropping that packet because no stack had been built would lose
// data the tunnel was created to carry. The build happens here, on the first such packet, and is
// bounded to one construction. The caller keeps ownership of the buffers and releases them after the
// call.
func (d *deferredDevice) writeInbound(packetBuffers []*buf.Buffer, wait bool) error {
	if len(packetBuffers) == 0 {
		return nil
	}
	generation, end, err := d.beginOp()
	if err != nil {
		return err
	}
	defer end()
	write := func(packetBuffers []*buf.Buffer) error {
		return generation.device.WriteInboundBuffers(packetBuffers)
	}
	if generation.raw != nil {
		if wait {
			write = generation.raw.waitWriteRawBuffers
		} else {
			write = generation.raw.writeRawBuffers
		}
	}
	return d.processInboundBuffers(packetBuffers, write)
}

func (d *deferredDevice) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	generation, end, err := d.beginOp()
	if err != nil {
		return nil, err
	}
	defer end()
	return generation.device.DialContext(ctx, network, destination)
}

func (d *deferredDevice) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	generation, end, err := d.beginOp()
	if err != nil {
		return nil, err
	}
	defer end()
	return generation.device.ListenPacket(ctx, destination)
}

func (d *deferredDevice) PortAddresses() (netip.Addr, netip.Addr) {
	d.access.Lock()
	defer d.access.Unlock()
	return d.inet4Address, d.inet6Address
}

func (d *deferredDevice) PortMTU() uint32 {
	d.access.Lock()
	defer d.access.Unlock()
	return d.mtu
}

// UpstreamPort reports the inner stack, or nil before one has been built.
//
// nil is the honest answer: there is no stack to hand out, and returning the facade itself would make
// any consumer that unwraps it operate on a type it does not expect. No consumer in this repository
// unwraps it today (the method exists on the adapter interface only).
func (d *deferredDevice) UpstreamPort() any {
	d.access.Lock()
	published := d.inner
	d.access.Unlock()
	if published == nil {
		return nil
	}
	return published.device.UpstreamPort()
}

func (d *deferredDevice) NewOutboundQueue(handler func(packetBuffers []*buf.Buffer)) *tun.OutboundQueue {
	generation, end, err := d.beginOp()
	if err != nil {
		// The interface returns a queue or nothing, so a nil here is the only signal available.
		// It is not swallowed: the cause is recorded (counted, logged at error level and exposed
		// through Diagnostics) so an owner can tell "no queue because there is no stack" from "the
		// stack failed to build", which a bare nil would hide.
		d.recordError(E.Cause(err, "new outbound queue"))
		d.access.Lock()
		d.queueFailures++
		d.access.Unlock()
		return nil
	}
	defer end()
	queue := generation.device.NewOutboundQueue(handler)
	if queue == nil {
		d.recordError(errQueueUnavailable)
		d.access.Lock()
		d.queueFailures++
		d.access.Unlock()
		return nil
	}
	// The queue's lifetime belongs to the caller from this line on: releasing the generation would
	// leave a live queue whose drain goroutine reads a closed memory TUN, and nothing here can see
	// that caller's Close. Recorded so Release can refuse rather than produce that state.
	d.access.Lock()
	d.queueHandouts++
	d.access.Unlock()
	return queue
}

// Release closes the current generation and returns the facade to its unbuilt state.
//
// The preconditions are the caller's to prove; the only ones this type can check for itself are:
//
//   - no operation is still inside the stack (a dial, an inbound write, a queue creation);
//   - no build is still running;
//   - no outbound queue has ever been handed to a caller.
//
// Everything else - a live tunnel session, a returned connection, an attached return path - belongs
// to the caller. The return path, the MTU, the addresses and the packet writer are FACADE state and
// survive a release, so a caller that attached them does not lose them.
//
// # Why a queue handout is a permanent refusal
//
// A *tun.OutboundQueue is owned by the caller. The pinned sing-tun states it explicitly: MemoryTun's
// Close "does not close caller-owned queues; they are closed by their owners", and the queue's drain
// goroutine lives until that owner closes it. This type cannot observe that Close, so it cannot prove
// the queue is gone - and releasing would leave a live reader on a closed memory TUN. Keeping the
// generation built is the safe answer, so this is a refusal, not a retry: the shape falls back to
// the original "defer construction only" behaviour, stated rather than silently half-released.
func (d *deferredDevice) Release() error {
	d.access.Lock()
	if d.closed {
		d.access.Unlock()
		return errDeviceClosed
	}
	if d.queueHandouts > 0 {
		handouts := d.queueHandouts
		d.access.Unlock()
		return E.New("cannot release the inner device: ", handouts,
			" caller-owned outbound queue(s) were handed out and their lifetime is not observable here")
	}
	if d.inner == nil {
		// A build may still be running even though nothing is published; there is then nothing to
		// release yet, and reporting success would be a promise the publish would break.
		if d.buildInFlight > 0 {
			d.access.Unlock()
			return E.New("cannot release the inner device: a build is still running")
		}
		d.access.Unlock()
		return nil
	}
	if d.inFlight > 0 || d.buildInFlight > 0 {
		busy := d.inFlight + d.buildInFlight
		d.access.Unlock()
		return E.New("cannot release the inner device: ", busy, " operation(s) are still inside it")
	}
	inner := d.inner.device
	d.inner = nil
	d.releaseNum++
	d.access.Unlock()
	return inner.Close()
}

// Close releases the facade and its generation once. It does not wait for a build that is running:
// the builder sees closed at publish time and discards what it built, so shutdown cannot be held by a
// construction it did not ask for.
func (d *deferredDevice) Close() error {
	d.closeOnce.Do(func() {
		d.access.Lock()
		d.closed = true
		inner := d.inner
		d.inner = nil
		d.access.Unlock()
		if inner != nil {
			d.closeErr = inner.device.Close()
		}
	})
	return d.closeErr
}
