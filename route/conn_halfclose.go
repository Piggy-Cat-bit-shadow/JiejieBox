package route

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common"
	N "github.com/sagernet/sing/common/network"
)

// Half-close liveness observation.
//
// # The state this exists for
//
// A stream has two copy directions. When one of them ends CLEANLY (the peer sent FIN, so the read
// returned EOF), the connection is half-closed: that direction is finished and will never read the
// connection again, while the other direction keeps the flow alive.
//
// The surviving direction owns the teardown - it runs onClose and closes both sides when it ends.
// The problem is what the finished direction stops doing: it was the only observer of its own peer.
// After its EOF, nothing on this side ever touches that connection again:
//
//   - the read side is at EOF forever, so it can never block on the peer again;
//   - the write side is only used when the SURVIVING direction has data, which for an idle peer is
//     never.
//
// So if that peer's socket then disappears - the app is terminated after it closed, the device's
// stack is gone, the peer's TCP is reset - the connection stays registered in the traffic manager
// and the connection manager, the UI keeps showing it as active, and NOTHING will ever release it.
// Only an explicit close-all does. That is exactly the reported symptom, and it is not a timeout
// problem: there is no timer running at all, in either layer.
//
// # Why a keepalive, and why that is not an idle timeout
//
// The only way to learn that a peer still exists is to send it something. A zero-length TCP
// keepalive probe is the standard mechanism (RFC 1122 4.2.3.6): it is not application data, a live
// peer's kernel answers it without waking the application, and a peer whose socket no longer exists
// answers with RST. That turns "the peer is gone" into a DEFINITIVE event - a socket error - rather
// than an inference from silence.
//
// This distinction is the whole design constraint, because the connection in this state is
// indistinguishable, by construction, from a legitimate one: `curl -X POST` sends a request,
// half-closes, and waits for a response that may take as long as the backend takes. That connection
// must not be killed, and this file never kills it - a live peer ACKs the probes and the connection
// stays up for as long as it likes. What IS killed is a connection whose finished side reports a
// terminal error, which a live peer cannot produce.
//
// # Capability is not the same thing as a usable verdict
//
// Arming a keepalive only proves that the connection accepted the configuration. What this file
// actually depends on is narrower: after the peer's FIN has been read, a LATER Read must report the
// terminal state the transport learns. Measured on this tree:
//
//	userspace stack (*tun.GoConn)   Read returns c.err before the EOF conditions, so a reset or the
//	                                stack's own keepalive timeout is visible to a later read
//	kernel socket (*net.TCPConn)    a later Read keeps returning EOF; a reset is visible only to a
//	                                WRITE (measured on a real loopback socket)
//
// So the two capabilities are separate predicates. `keepAliveConnOf` answers "can this connection be
// asked to probe", and `terminalReadObservable` answers "would a later read be able to report the
// answer". A connection that fails the second is NOT observed: starting an observation that can
// never reach a verdict would only add a timer and a wrong entry in the diagnostics. The kernel
// socket boundary is recorded by the e2e characterisation test from the first round.
//
// # Lifecycle contract of one observation
//
// An observation is created (and counted) only after the probe is armed, so every counted
// observation has a probe in flight. It then reaches exactly ONE final state, decided by a single
// compare-and-swap on `settled`:
//
//	released   a terminal error was read from the finished side and the shared teardown was invoked.
//	           `released` is split into `peer reset` (the peer's stack answered a probe with RST) and
//	           `keepalive timeout` (nothing answered at all).
//	stopped    the observation ended without a peer-gone verdict: the surviving direction finished
//	           the flow first, this side was closed locally, an unexpected byte made the premise
//	           wrong, or the tunnel shut down while the observation was still pending.
//
// Anything counted but not yet settled is `pending`. The invariant is therefore
//
//	observed == released + stopped + pending
//
// and it holds at every snapshot because `observed` is incremented before the probe is scheduled and
// each observation settles at most once. The accessor reads `released` and `stopped` BEFORE
// `observed`, so a settle that races the read cannot make the right-hand side exceed the left.
//
// `pending` is a real state, not a rounding error: the probe runs on a timer, so at any instant some
// observations have not had their next read yet. The tunnel-shutdown summary settles every live
// observation first (ConnectionsManager.settlePendingObservers), because at shutdown the flows are
// closed by the tunnel rather than by a verdict, and a reader should not have to guess whether a
// pending observation was about to release something.
//
// # Latency, derived from the constants rather than promised
//
// Two independent delays add up, and neither is a "cleanup interval":
//
//	peer socket disappears -> terminal error   bounded by the probe schedule the stack owns:
//	                                           idle + count*interval = 20s + 3*10s = 50s
//	terminal error -> the observer reads it    bounded by the poll interval in effect, at most
//	                                           halfClosedProbeMaxInterval = 60s
//	the read -> teardown, both sockets close   immediate (the same goroutine)
//
// So the worst case is under two minutes, and the typical case is 20-110s, measured from the moment
// the peer's FIN was the last thing observed - NOT from the moment the application exited, which
// nothing in the core can see. The keepalive idle and probe count are what the userspace stack does;
// shrinking them would shorten the bound but probe a live peer more often, and the peer may be a
// sleeping phone.
//
// # Cost
//
// One timer and one read syscall per interval, per HALF-CLOSED flow - a connection that never
// half-closes is never observed and costs nothing (`TestTUNHealthyIdleIsNeverObserved` asserts it).
// A waiting request is the common case, so the backoff exists to keep a long wait cheap: the poll
// doubles from 15s to 60s. The probes themselves are the stack's, one small segment per interval,
// answered by the peer's kernel without waking its application.
//
// # What this does NOT cover
//
//   - A peer that is merely SILENT while its socket still exists - a suspended application whose
//     socket the OS has not closed - is never killed here, and must not be: it is alive, it answers
//     the probes, and the connection is legitimately open. Only the OS closing that socket can end
//     it, and when it does, the FIN is seen and this file takes over from there.
//   - A KERNEL-SOCKET inbound (mixed, redirect) whose peer vanishes after its FIN: its read side
//     cannot report the verdict, so it is not observed at all (see the capability section). The
//     e2e characterisation test records that limit.
//
// The phone's own inbound is the TUN stack, where the userspace TCP does report the vanished peer to
// a later read, which is what makes this mechanism effective on the path the report came from.
const (
	// halfClosedKeepAliveIdle is how long a half-closed connection is silent before the first probe.
	//
	// Deliberately short: this clock only starts once the peer has already said it is finished
	// sending, and the point is to notice a vanished peer in under a minute rather than in the two
	// hours the platform's own keepalive would take (sing-tun's userspace stack initialises its
	// keepalive idle from the forward-path established timeout).
	halfClosedKeepAliveIdle = 20 * time.Second
	// halfClosedKeepAliveInterval is the gap between unanswered probes.
	halfClosedKeepAliveInterval = 10 * time.Second
	// halfClosedKeepAliveCount is how many unanswered probes mean the peer is gone. The probe count
	// is what turns "no answer" into the stack's own ETIMEDOUT, which is the terminal state the
	// observer reads. With the three values above the stack's verdict is bounded by 50s.
	halfClosedKeepAliveCount = 3
	// halfClosedProbeInterval is how often the finished side is read, to observe a terminal state
	// that arrived since the last read.
	halfClosedProbeInterval = 15 * time.Second
	// halfClosedProbeMaxInterval bounds the backoff for a half-closed connection that is still alive.
	//
	// A live peer is the common case - a waiting request - and it can wait for a long time, so the
	// poll must not stay at its base rate forever. The cap is one minute rather than the two it
	// started at because the cap is also the worst-case detection delay once the stack's verdict has
	// arrived: 50s (verdict) + 60s (poll) is the "under two minutes" the record states, and the cost
	// of halving the cap is one extra read per half-closed flow per minute - on a state that exists
	// only while a request is waiting.
	halfClosedProbeMaxInterval = time.Minute
	// halfClosedObservationBound is the worst-case detection delay this file promises, and it is
	// asserted against the constants above by TestHalfCloseDetectionBoundMatchesTheConstants. It is
	// NOT the time from "the application exited": nothing in the core observes that, and the clock
	// starts at the peer's FIN.
	halfClosedObservationBound = 2 * time.Minute
)

// keepAliveConfigurable is the capability the observer needs from a finished side to ask it to probe.
//
// Implemented by *net.TCPConn and by sing-tun's userspace *tun.GoConn - i.e. by both connection
// kinds this manager ever holds. A connection that does not implement it cannot be asked anything,
// so it is left exactly as it was.
type keepAliveConfigurable interface {
	SetKeepAliveConfig(config net.KeepAliveConfig) error
}

// halfCloseTerminalReadable is the SECOND capability, and it is not implied by the first: a later
// read on this connection reports a terminal state after EOF.
//
// The production implementer is the userspace stack, matched by concrete type in
// terminalReadObservable because it cannot be given a marker method here. The marker exists for the
// route package's own doubles, which have no stack to ask: a double that claims this must be
// claiming it about the behaviour it scripts.
type halfCloseTerminalReadable interface {
	halfCloseTerminalRead() bool
}

// terminalReadObservable reports whether a later Read on the resolved connection can report the
// verdict this mechanism depends on. See the capability section of the file comment.
func terminalReadObservable(conn any) bool {
	if marker, isMarker := conn.(halfCloseTerminalReadable); isMarker {
		return marker.halfCloseTerminalRead()
	}
	_, isUserspaceStack := conn.(*tun.GoConn)
	return isUserspaceStack
}

// halfCloseOptions carries the production timings, with a test override for each. A zero value
// means "use the constant", the same convention as drainIdleGraceOverride.
type halfCloseOptions struct {
	keepAliveIdle     time.Duration
	keepAliveInterval time.Duration
	probeInterval     time.Duration
}

func (m *ConnectionManager) halfCloseTimings() halfCloseOptions {
	timings := m.halfClose
	if timings.keepAliveIdle <= 0 {
		timings.keepAliveIdle = halfClosedKeepAliveIdle
	}
	if timings.keepAliveInterval <= 0 {
		timings.keepAliveInterval = halfClosedKeepAliveInterval
	}
	if timings.probeInterval <= 0 {
		timings.probeInterval = halfClosedProbeInterval
	}
	return timings
}

// halfCloseOutcome is the final state of one observation. `pending` is not a value of this type: it
// means "no outcome has been decided yet", and it is derived by the snapshot rather than stored.
type halfCloseOutcome uint8

const (
	halfCloseReleased halfCloseOutcome = iota
	halfCloseStopped
)

// flowTeardown is the single owner of "this routed stream is over".
//
// Before this existed, the teardown decision was `done.Swap(true)`: the first copy direction to
// finish got false and left the connection alone, the second got true and tore it down. That works
// for exactly two participants. The half-close observer is a third, so the decision moved here,
// where it is a sync.Once and the answer does not depend on how many observers exist.
type flowTeardown struct {
	ctx     context.Context
	onClose N.CloseHandlerFunc
	conn    net.Conn
	remote  net.Conn
	once    sync.Once
	ran     atomic.Bool
	// observer, when set, is settled as `stopped` by the teardown that wins: the flow ended because
	// the surviving direction (or the tunnel) closed it, not because of a peer-gone verdict. Without
	// this the observer would stay `pending` until its next timer fired.
	observer atomic.Pointer[halfClosedObserver]
}

func newFlowTeardown(ctx context.Context, onClose N.CloseHandlerFunc, conn net.Conn, remote net.Conn) *flowTeardown {
	return &flowTeardown{
		ctx:     ctx,
		onClose: onClose,
		conn:    conn,
		remote:  remote,
	}
}

// close runs the teardown exactly once, whoever got here first.
func (t *flowTeardown) close(err error) {
	if t == nil {
		return
	}
	// Settle a pending observation BEFORE the close callbacks run. The teardown is what ends the
	// flow, so "no peer-gone verdict" is already the truth at this point - and settling first means
	// anything a reader does from onClose (a log line, a shutdown summary, a test waiting on the
	// channel) sees a consistent state instead of a state that becomes consistent a moment later.
	// A release that won the race has settled itself already, so this is a no-op then.
	if observer := t.observer.Load(); observer != nil {
		observer.settle(halfCloseStopped, nil)
	}
	t.once.Do(func() {
		t.ran.Store(true)
		if t.onClose != nil {
			t.onClose(err)
		}
		common.Close(t.conn, t.remote)
	})
}

func (t *flowTeardown) done() bool {
	return t == nil || t.ran.Load()
}

func (t *flowTeardown) attachObserver(observer *halfClosedObserver) {
	t.observer.Store(observer)
	if t.done() {
		// The flow ended between the caller's check and this attachment.
		observer.settle(halfCloseStopped, nil)
	}
}

// halfClosedObserver watches the one side of a stream that stopped being observed.
//
// One timer per half-closed connection, not per connection: a flow that never half-closes never
// creates one, and the timer dies with the flow.
type halfClosedObserver struct {
	manager   *ConnectionManager
	conn      net.Conn
	teardown  *flowTeardown
	interval  time.Duration
	maxInterv time.Duration
	buffer    [1]byte
	// settled is the single owner of the final state; see the lifecycle contract in the file comment.
	settled atomic.Bool
}

// observeHalfClosed starts watching the finished side of the upload direction.
//
// It is called from the copy direction that has just returned EOF, and only from that one. The
// download direction's finished side is the outbound socket, whose liveness is already observed by
// the surviving direction's writes - so watching it would add a timer that can never learn anything
// the write path does not.
func (m *ConnectionManager) observeHalfClosed(source net.Conn, teardown *flowTeardown) {

	if teardown == nil || teardown.done() {
		return
	}
	target := keepAliveConnOf(source)
	if target == nil {
		// Nothing in the chain can be asked to probe. Leave the flow to its surviving direction,
		// exactly as before this file existed.
		m.halfCloseDiag.recordSkippedUnarmable()
		return
	}
	if !terminalReadObservable(target) {
		// It can be asked to probe, but a later read would never report the answer. Starting the
		// observation would only cost a timer and produce a pending entry that can never settle.
		m.halfCloseDiag.recordSkippedUnobservable()
		m.logHalfClose(teardown.ctx, "half-closed flow: read side cannot report a verdict, not observed")
		return
	}
	timings := m.halfCloseTimings()
	config := net.KeepAliveConfig{
		Enable:   true,
		Idle:     timings.keepAliveIdle,
		Interval: timings.keepAliveInterval,
		Count:    halfClosedKeepAliveCount,
	}
	if err := target.SetKeepAliveConfig(config); err != nil {
		// A connection that refuses the configuration is not one this observer may judge.
		m.halfCloseDiag.recordSkippedUnarmable()
		m.logHalfClose(teardown.ctx, "half-closed flow: keepalive unavailable: ", err)
		return
	}
	observer := &halfClosedObserver{
		manager:   m,
		conn:      source,
		teardown:  teardown,
		interval:  timings.probeInterval,
		maxInterv: halfClosedProbeMaxInterval,
	}
	// Counted before the first timer exists, so an observation is never visible as pending before it
	// is part of the invariant.
	m.halfCloseDiag.recordObserved()
	m.registerObserver(observer)
	teardown.attachObserver(observer)
	observer.schedule()
}

// keepAliveConnOf walks the connection chain for a keepalive capability.
//
// The walk is necessary rather than a plain type assertion for the same reason
// managedConnStateOf documents: by the time a connection reaches the copy loops it may sit under
// the traffic tracker's counters, a sniffing cache, or a protocol's own wrapper. It stops on a
// missing link, on a wrapper that points at itself, and after 32 hops - a cycle that does not point
// at itself would otherwise walk forever.
func keepAliveConnOf(conn any) keepAliveConfigurable {
	for depth := 0; conn != nil && depth < 32; depth++ {
		if setter, isSetter := conn.(keepAliveConfigurable); isSetter {
			return setter
		}
		upstream, hasUpstream := conn.(common.WithUpstream)
		if !hasUpstream {
			return nil
		}
		next := upstream.Upstream()
		if next == conn {
			return nil
		}
		conn = next
	}
	return nil
}

// registerObserver makes the observation visible to the shutdown sweep.
func (m *ConnectionManager) registerObserver(observer *halfClosedObserver) {
	m.observersAccess.Lock()
	if m.observers == nil {
		m.observers = make(map[*halfClosedObserver]struct{})
	}
	m.observers[observer] = struct{}{}
	m.observersAccess.Unlock()
}

func (m *ConnectionManager) unregisterObserver(observer *halfClosedObserver) {
	m.observersAccess.Lock()
	delete(m.observers, observer)
	m.observersAccess.Unlock()
}

// settlePendingObservers ends every observation that is still pending.
//
// Called at tunnel shutdown, AFTER the flows have been closed: at that point the remaining
// observations cannot reach a peer-gone verdict that would matter, and leaving them pending would
// make the shutdown summary say "pending N" for flows the tunnel itself just closed. They are
// settled as `stopped`, which is the truthful outcome - the flow ended because the tunnel did.
//
// The map is swapped under the lock and settled outside it: settle() takes the same lock to
// unregister, and calling it while holding would deadlock.
func (m *ConnectionManager) settlePendingObservers() {
	m.observersAccess.Lock()
	pending := m.observers
	m.observers = nil
	m.observersAccess.Unlock()
	for observer := range pending {
		observer.settle(halfCloseStopped, nil)
	}
}

// schedule arms the next probe.
//
// The timer is deliberately NOT stored. There is nothing to stop - a probe that runs after the flow
// ended checks the teardown first and returns - and keeping a timer handle would need a write that
// the timer's own callback can race: the callback may start before the assignment completes, which
// is a data race on the field even though the schedule order is correct.
func (o *halfClosedObserver) schedule() {
	if o.settled.Load() {
		return
	}
	time.AfterFunc(o.interval, o.probe)
}

// probe reads the finished side once, looking for a terminal state.
func (o *halfClosedObserver) probe() {
	if o.settled.Load() {
		return
	}
	if o.teardown.done() {
		// The surviving direction ended the flow while this timer was in flight. A teardown also
		// settles the observation directly; this covers the case where it had not been attached yet.
		o.settle(halfCloseStopped, nil)
		return
	}
	read, err := o.conn.Read(o.buffer[:])
	switch {
	case read > 0:
		// A byte after EOF cannot happen on a well-behaved TCP stream. If one ever arrives - a
		// wrapper that does not mean EOF when it returns it - this observer has no business reading
		// any further: stop and leave the flow to its surviving direction. It must never eat stream
		// data to satisfy a liveness check.
		o.settle(halfCloseStopped, nil)
		o.manager.logHalfClose(o.teardown.ctx, "half-closed flow: unexpected byte, observation stopped")
	case err == nil:
		o.backoff()
	case errors.Is(err, io.EOF):
		// Still half-closed and still there: the peer exists but has nothing to say. This is the
		// ordinary state of a waiting request, and it must be able to last as long as the peer likes.
		o.backoff()
	case errors.Is(err, net.ErrClosed), errors.Is(err, os.ErrClosed), errors.Is(err, io.ErrClosedPipe):
		// THIS side was closed, which is what the surviving direction does when it wins the race.
		// Nothing to release, and the whole class is deliberately separate from the case below:
		// sing's IsClosedOrCanceled also counts ECONNRESET/EPIPE, and those mean the OPPOSITE thing
		// here - the peer is gone.
		o.settle(halfCloseStopped, nil)
	case errors.Is(err, os.ErrDeadlineExceeded):
		// A LOCAL read deadline is not a peer verdict. A wrapper further out in the chain may set
		// one (sniffing does), and treating an expired local deadline as "the peer is gone" would
		// kill exactly the flows this observer exists to protect.
		o.backoff()
	default:
		// A terminal error raised by the peer or by the stack on its behalf: RST, a keepalive that
		// was never answered, a reset stream. This is a definitive answer, not an inference from
		// silence, and it is the only thing that releases a half-closed flow.
		if o.settle(halfCloseReleased, err) {
			o.manager.reclaimHalfClosed(o.teardown, err)
		}
	}
}

// nextInterval is the backoff rule on its own, so the bound can be asserted without timers.
func (o *halfClosedObserver) nextInterval() time.Duration {
	if o.interval >= o.maxInterv {
		return o.maxInterv
	}
	next := o.interval * 2
	if next > o.maxInterv {
		return o.maxInterv
	}
	return next
}

func (o *halfClosedObserver) backoff() {
	o.interval = o.nextInterval()
	o.schedule()
}

// settle records the observation's one final state and reports whether this call decided it.
//
// The compare-and-swap is what makes the exactly-once property hold under the real concurrency: a
// timer callback, the teardown, the shutdown sweep and a local close can all reach here.
func (o *halfClosedObserver) settle(outcome halfCloseOutcome, cause error) bool {
	if !o.settled.CompareAndSwap(false, true) {
		return false
	}
	o.manager.unregisterObserver(o)
	switch outcome {
	case halfCloseReleased:
		o.manager.halfCloseDiag.recordReleased(cause)
	default:
		o.manager.halfCloseDiag.recordStopped()
	}
	return true
}

// reclaimHalfClosed is the one place a half-closed flow is released because its peer is provably
// gone. The accounting for the observation itself happened in settle; this is only the log line and
// the teardown, which is idempotent because every participant shares it.
func (m *ConnectionManager) reclaimHalfClosed(teardown *flowTeardown, cause error) {
	m.logHalfClose(teardown.ctx, "half-closed flow released: peer gone: ", cause)
	teardown.close(cause)
}

// logHalfClose is the diagnostic half of this file. The manager can be built without a logger (the
// fixtures do), and none of the observation may depend on whether one is installed.
func (m *ConnectionManager) logHalfClose(ctx context.Context, args ...any) {
	if m.logger == nil {
		return
	}
	m.logger.DebugContext(ctx, args...)
}
