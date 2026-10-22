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
// # Why the observation has to be a poll
//
// After a read has returned EOF, reading the connection again returns EOF immediately - it does not
// block, so it cannot be "waited on". The socket error that a reset writes is therefore only
// visible to a read that happens LATER. On a userspace stack (the TUN case) that read reports the
// error; on a kernel socket it reports EOF (measured: the error is only visible to a write). So the
// observer polls, backs off, and stops as soon as the flow is closed. It never polls a flow that has
// not half-closed, and it never polls at a rate that matters: a half-closed connection is a waiting
// request, and the poll is one system call per interval.
//
// # What this does NOT cover
//
//   - A peer that is merely SILENT while its socket still exists - a suspended application whose
//     socket the OS has not closed - is never killed here, and must not be: it is alive, it answers
//     the probes, and the connection is legitimately open. Only the OS closing that socket can end
//     it, and when it does, the FIN is seen and this file takes over from there.
//   - A KERNEL-SOCKET inbound (not TUN) whose peer vanishes after its FIN. There the read side keeps
//     reporting EOF and the reset is only visible to a write, so there is no signal to act on; that
//     limit is recorded by the e2e characterisation test
//     (e2e/connection_lifecycle_test.go, TestTrafficTrackerHalfClosedKernelPeerIsNotObservable)
//     rather than papered over with a timeout.
//
// The phone's own inbound is the TUN stack, where the userspace TCP does report the vanished peer to
// a later read, which is what makes this mechanism effective on the path the report came from.
const (
	// halfClosedKeepAliveIdle is how long a half-closed connection is silent before the first probe.
	//
	// Deliberately short: this clock only starts once the peer has already said it is finished
	// sending, and the point is to notice a vanished peer in tens of seconds rather than in the
	// two hours the platform's own keepalive would take (sing-tun's userspace stack initialises its
	// keepalive idle from the forward-path established timeout).
	halfClosedKeepAliveIdle = 20 * time.Second
	// halfClosedKeepAliveInterval is the gap between unanswered probes.
	halfClosedKeepAliveInterval = 10 * time.Second
	// halfClosedKeepAliveCount is how many unanswered probes mean the peer is gone. The probe count
	// is what turns "no answer" into the stack's own ETIMEDOUT, which is the terminal state the
	// observer reads.
	halfClosedKeepAliveCount = 3
	// halfClosedProbeInterval is how often the finished side is read, to observe a terminal state
	// that arrived since the last read.
	halfClosedProbeInterval = 15 * time.Second
	// halfClosedProbeMaxInterval bounds the backoff for a half-closed connection that is still alive.
	//
	// A live peer is the common case - a waiting request - and it can wait for a long time, so the
	// poll must not stay at its base rate forever. Backing off costs detection latency only after a
	// peer that was already proven alive at least once, and the keepalive keeps probing either way.
	halfClosedProbeMaxInterval = 2 * time.Minute
)

// keepAliveConfigurable is the capability the observer needs from a finished side.
//
// Implemented by *net.TCPConn and by sing-tun's userspace *tun.GoConn - i.e. by both connection
// kinds this manager ever holds. A connection that does not implement it cannot be asked anything,
// so it is left exactly as it was.
type keepAliveConfigurable interface {
	SetKeepAliveConfig(config net.KeepAliveConfig) error
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
	timer     *time.Timer
	stopped   atomic.Bool
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
	setter := keepAliveConnOf(source)
	if setter == nil {
		// The finished side cannot be asked to prove liveness. Leave the flow to its surviving
		// direction, exactly as before this file existed.
		return
	}
	timings := m.halfCloseTimings()
	config := net.KeepAliveConfig{
		Enable:   true,
		Idle:     timings.keepAliveIdle,
		Interval: timings.keepAliveInterval,
		Count:    halfClosedKeepAliveCount,
	}
	if err := setter.SetKeepAliveConfig(config); err != nil {
		// A connection that refuses the configuration is not one this observer may judge.
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
	observer.schedule()
}

// keepAliveConnOf walks the connection chain for a keepalive capability.
//
// The walk is necessary rather than a plain type assertion for the same reason
// managedConnStateOf documents: by the time a connection reaches the copy loops it may sit under
// the traffic tracker's counters, a sniffing cache, or a protocol's own wrapper.
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

func (o *halfClosedObserver) schedule() {
	if o.stopped.Load() {
		return
	}
	o.timer = time.AfterFunc(o.interval, o.probe)
}

// probe reads the finished side once, looking for a terminal state.
func (o *halfClosedObserver) probe() {
	if o.stopped.Load() {
		return
	}
	if o.teardown.done() {
		// The surviving direction ended the flow while this timer was in flight.
		o.stopped.Store(true)
		return
	}
	read, err := o.conn.Read(o.buffer[:])
	switch {
	case read > 0:
		// A byte after EOF cannot happen on a well-behaved TCP stream. If one ever arrives - a
		// wrapper that does not mean EOF when it returns it - this observer has no business reading
		// any further: stop and leave the flow to its surviving direction. It must never eat stream
		// data to satisfy a liveness check.
		o.stopped.Store(true)
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
		o.stopped.Store(true)
	case errors.Is(err, os.ErrDeadlineExceeded):
		// A LOCAL read deadline is not a peer verdict. A wrapper further out in the chain may set
		// one (sniffing does), and treating an expired local deadline as "the peer is gone" would
		// kill exactly the flows this observer exists to protect.
		o.backoff()
	default:
		// A terminal error raised by the peer or by the stack on its behalf: RST, a keepalive that
		// was never answered, a reset stream. This is a definitive answer, not an inference from
		// silence, and it is the only thing that releases a half-closed flow.
		o.stopped.Store(true)
		o.manager.reclaimHalfClosed(o.teardown, err)
	}
}

func (o *halfClosedObserver) backoff() {
	if o.interval < o.maxInterv {
		o.interval *= 2
		if o.interval > o.maxInterv {
			o.interval = o.maxInterv
		}
	}
	o.schedule()
}

// reclaimHalfClosed is the one place a half-closed flow is released because its peer is provably
// gone. It is a method so the log line, the teardown and the accounting stay together.
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
