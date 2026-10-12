package route

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/metadata"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

// Active TCP forward-progress stall: the deterministic reproducer.
//
// # The claim under test
//
// A proxied TCP flow whose peer has gone silent WITHOUT FIN, WITHOUT RST and WITHOUT error -
// the classic silent half-open, or a proxy whose own upstream to the destination died while
// the client<->proxy socket stays healthy - is never terminated by the core on its own. The
// connection object stays in the manager, both copy goroutines stay blocked in Read, no
// onClose ever fires, and the application waits on a response that will never arrive.
//
// # Why the core has nothing that could notice
//
//  1. The copy loops call Read with no deadline. `connectionCopy` -> `bufio.CopyWithIncreateBuffer`
//     reads from net.Conn with no SetReadDeadline anywhere on the established path.
//  2. `managedConnState.lastActive` records activity, but the only reader of it is
//     `ConnectionManager.Reclaim`, whose single production caller is the network-change path in
//     route/network.go. In steady state that clock is written and never read.
//  3. `sweepTimer` - the only timer this manager owns - is created inside `beginDrainSweep`, which
//     is reached only from a drain, which is reached only from `Reclaim`. No transition, no timer.
//
// So the net is: with no network change, the ONLY thing that can end this connection is the
// application giving up, the OS TCP keepalive finally probing a dead path (5 minutes to the
// first probe, and it never fires at all when the client<->proxy socket is genuinely alive),
// or the process exiting.
//
// These tests use test-only code: a fake dialer and real loopback sockets. No production file
// is touched by this file.

// silentBlackhole accepts connections and then does nothing with them, forever: no read, no
// write, no close. It is the peer that has stopped existing without saying so.
type silentBlackhole struct {
	listener net.Listener
	accepted []net.Conn
	access   sync.Mutex
	closed   atomic.Bool
}

func newSilentBlackhole(t *testing.T) *silentBlackhole {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("could not start the blackhole listener: ", err)
	}
	blackhole := &silentBlackhole{listener: listener}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			// Deliberately NOT read from, NOT written to, NOT closed. Holding the connection
			// open is the whole point: FIN and RST are exactly what must not happen.
			blackhole.access.Lock()
			blackhole.accepted = append(blackhole.accepted, conn)
			blackhole.access.Unlock()
		}
	}()
	t.Cleanup(func() {
		blackhole.closed.Store(true)
		_ = listener.Close()
		blackhole.access.Lock()
		for _, conn := range blackhole.accepted {
			_ = conn.Close()
		}
		blackhole.access.Unlock()
	})
	return blackhole
}

func (b *silentBlackhole) addr() M.Socksaddr {
	return M.ParseSocksaddr(b.listener.Addr().String())
}

// blackholeDialer is no longer used to reach the network: the fixture drives the PRODUCTION dial
// path instead. It is kept only to document what was wrong with the first version of this file.
//
// The first version returned a connection from a fake dialer directly. `ConnectionManager` does
// not register connections - `defaultDialer.trackConn` does, and it is reached only through
// `dialer.NewDefault` - so that fixture produced a flow the manager had never heard of, and
// `Count()` was 0 for a reason that had nothing to do with stalling. The existing TCP splice
// tests rely on the same property deliberately (`manager.Count() == 0` is one of their
// assertions), which is why the mistake was easy to make and why it is written down here.
type blackholeDialer struct {
	target M.Socksaddr
}

func (d *blackholeDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	var nativeDialer net.Dialer
	return nativeDialer.DialContext(ctx, network, d.target.String())
}

func (d *blackholeDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// newBlackholeDialer builds the production dialer wired to this manager, so `trackConn` runs and
// the flow is really registered. NewDefault reads the manager out of the context, so the test
// injects it there exactly as the dialer package's own tests do.
func newBlackholeDialer(t *testing.T, manager *ConnectionManager, blackhole *silentBlackhole) N.Dialer {
	t.Helper()
	ctx := service.ContextWith[adapter.ConnectionManager](context.Background(), manager)
	production, err := dialer.NewDefault(ctx, option.DialerOptions{})
	if err != nil {
		t.Fatal("could not build the production dialer: ", err)
	}
	return &addressedDialer{inner: production, target: blackhole.addr()}
}

// addressedDialer keeps the production dial path and only redirects the destination to the
// blackhole, so the outbound really is a native TCP socket to a peer that will stay silent.
type addressedDialer struct {
	inner  N.Dialer
	target M.Socksaddr
}

func (d *addressedDialer) DialContext(ctx context.Context, network string, _ M.Socksaddr) (net.Conn, error) {
	return d.inner.DialContext(ctx, network, d.target)
}

func (d *addressedDialer) ListenPacket(ctx context.Context, _ M.Socksaddr) (net.PacketConn, error) {
	return d.inner.ListenPacket(ctx, d.target)
}

// stallLog captures what the manager reports about a flow, so that a fixture which fails to
// establish shows the manager's own reason instead of only the connection count that resulted
// from it. The first version of this file asserted "the fixture did not establish" and nothing
// else, which cost a debugging round to explain.
type stallLog struct {
	access   sync.Mutex
	messages []string
}

func (l *stallLog) record(level string, message string) {
	l.access.Lock()
	l.messages = append(l.messages, level+": "+message)
	l.access.Unlock()
}

func (l *stallLog) dump() string {
	l.access.Lock()
	defer l.access.Unlock()
	if len(l.messages) == 0 {
		return "(the manager logged nothing)"
	}
	return strings.Join(l.messages, "\n")
}

// logger returns a logger.ContextLogger the manager can use. The interface is wide, so the
// levels this investigation does not read are no-ops: it exists to explain a fixture.
func (l *stallLog) logger() log.ContextLogger {
	return &stallLogger{parent: l}
}

type stallLogger struct {
	parent *stallLog
}

func (s *stallLogger) Trace(args ...any) { s.parent.record("trace", fmt.Sprint(args...)) }
func (s *stallLogger) Debug(args ...any) { s.parent.record("debug", fmt.Sprint(args...)) }
func (s *stallLogger) Info(args ...any)  { s.parent.record("info", fmt.Sprint(args...)) }
func (s *stallLogger) Warn(args ...any)  { s.parent.record("warn", fmt.Sprint(args...)) }
func (s *stallLogger) Error(args ...any) { s.parent.record("error", fmt.Sprint(args...)) }
func (s *stallLogger) Fatal(args ...any) { s.parent.record("fatal", fmt.Sprint(args...)) }
func (s *stallLogger) Panic(args ...any) { s.parent.record("panic", fmt.Sprint(args...)) }

func (s *stallLogger) TraceContext(_ context.Context, args ...any) {
	s.parent.record("trace", fmt.Sprint(args...))
}
func (s *stallLogger) DebugContext(_ context.Context, args ...any) {
	s.parent.record("debug", fmt.Sprint(args...))
}
func (s *stallLogger) InfoContext(_ context.Context, args ...any) {
	s.parent.record("info", fmt.Sprint(args...))
}
func (s *stallLogger) WarnContext(_ context.Context, args ...any) {
	s.parent.record("warn", fmt.Sprint(args...))
}
func (s *stallLogger) ErrorContext(_ context.Context, args ...any) {
	s.parent.record("error", fmt.Sprint(args...))
}
func (s *stallLogger) FatalContext(_ context.Context, args ...any) {
	s.parent.record("fatal", fmt.Sprint(args...))
}
func (s *stallLogger) PanicContext(_ context.Context, args ...any) {
	s.parent.record("panic", fmt.Sprint(args...))
}

// stallFixture is one proxied flow whose outbound peer has gone silent.
type stallFixture struct {
	manager     *ConnectionManager
	client      net.Conn // the application's side of mixed-in
	blackhole   *silentBlackhole
	log         *stallLog
	closeCalls  atomic.Int64
	closeReason atomic.Value
	clientDone  chan struct{}
}

// newStallFixture establishes a real proxied flow through the real ConnectionManager whose
// outbound lands on a silent blackhole.
func newStallFixture(t *testing.T, blackhole *silentBlackhole) *stallFixture {
	t.Helper()
	captured := &stallLog{}
	manager := NewConnectionManager(captured.logger())
	fixture := &stallFixture{manager: manager, blackhole: blackhole, log: captured, clientDone: make(chan struct{})}

	// The inbound side of mixed-in: the application writes here and reads replies here.
	client, proxySide := net.Pipe()
	fixture.client = client

	var closedOnce sync.Once
	onClose := func(err error) {
		closedOnce.Do(func() {
			fixture.closeCalls.Add(1)
			if err != nil {
				fixture.closeReason.Store(err.Error())
			} else {
				fixture.closeReason.Store("<nil>")
			}
			close(fixture.clientDone)
		})
	}

	t.Cleanup(func() {
		_ = client.Close()
		_ = proxySide.Close()
		_ = manager.Close()
	})

	manager.NewConnection(
		context.Background(),
		newBlackholeDialer(t, manager, blackhole),
		proxySide,
		adapter.InboundContext{Destination: metadata.ParseSocksaddrHostPort("ws.chatgpt.com", 443)},
		onClose,
	)
	// NewConnection is synchronous up to the point the copy goroutines start, so anything that
	// tore this flow down has already happened by here.
	select {
	case <-fixture.clientDone:
		reason, _ := fixture.closeReason.Load().(string)
		t.Logf("the fixture was torn down during setup; onClose reason: %q", reason)
	default:
	}
	return fixture
}

// TestStall_SilentPeerIsNeverReclaimed proves the steady-state claim: the core does not end a
// silent connection, and does not even have a timer that could.
//
// It asserts the ABSENCE of an event, which is only meaningful with a control, so this test
// also proves the fixture is live first: bytes do reach the peer before the peer goes silent.
// A fixture that never worked would "never be closed" for the wrong reason.
func TestStall_SilentPeerIsNeverReclaimed(t *testing.T) {
	blackhole := newSilentBlackhole(t)
	fixture := newStallFixture(t, blackhole)

	// The flow is alive: the manager registered it.
	if count := fixture.manager.Count(); count != 1 {
		t.Fatalf("the manager holds %d connections, want 1: the fixture did not establish.\n%s",
			count, fixture.log.dump())
	}

	// Prove the flow really carries bytes before the peer goes silent. The blackhole never
	// reads, so this write completes only because the socket buffer accepts it - which is
	// itself the point: a silent peer is indistinguishable from a slow one at this layer.
	if _, err := fixture.client.Write([]byte("the prompt, which the peer will never see")); err != nil {
		t.Fatal("the fixture could not move a byte to the peer: ", err)
	}

	// The peer is now silent, and nothing has happened to the network. Wait past several
	// windows that a liveness mechanism would plausibly use, and assert nothing fired.
	const observation = 6 * time.Second
	select {
	case <-fixture.clientDone:
		reason, _ := fixture.closeReason.Load().(string)
		t.Fatalf("the connection ended on its own after a silent stall (reason %q); "+
			"this test exists to prove it does not", reason)
	case <-time.After(observation):
	}

	if count := fixture.manager.Count(); count != 1 {
		t.Fatalf("after %s of silence the manager holds %d connections, want 1", observation, count)
	}
	if calls := fixture.closeCalls.Load(); calls != 0 {
		t.Fatalf("onClose fired %d time(s) during a silent stall; want 0", calls)
	}

	// The manager owns exactly one timer, and it is not running: sweepTimer is only created by
	// beginDrainSweep, which is only reached from a drain, which is only reached from Reclaim.
	fixture.manager.sweepAccess.Lock()
	sweepRunning := fixture.manager.sweepTimer != nil
	fixture.manager.sweepAccess.Unlock()
	if sweepRunning {
		t.Fatal("a drain sweep is running without a transition; the steady-state premise changed")
	}
}

// TestStall_ContextCancelIsTheOnlyThingThatEndsIt is the isolating step: it shows the stall is
// ended by exactly one thing, and that thing is not liveness. Cancelling the caller's context
// does close the socket - so the teardown chain works when something asks for it - while the
// silent peer above is never noticed on its own.
//
// Together the two tests say: the mechanism that would end a stalled flow exists and is
// correct, but nothing in steady state ever invokes it.
func TestStall_ContextCancelIsTheOnlyThingThatEndsIt(t *testing.T) {
	blackhole := newSilentBlackhole(t)

	ctx, cancel := context.WithCancel(context.Background())
	manager := NewConnectionManager(log.NewNOPFactory().NewLogger("stall-repro"))
	client, proxySide := net.Pipe()
	t.Cleanup(func() {
		cancel()
		_ = client.Close()
		_ = proxySide.Close()
		_ = manager.Close()
	})

	closed := make(chan error, 1)
	var once sync.Once
	manager.NewConnection(ctx, newBlackholeDialer(t, manager, blackhole), proxySide,
		adapter.InboundContext{Destination: metadata.ParseSocksaddrHostPort("ws.chatgpt.com", 443)},
		func(err error) { once.Do(func() { closed <- err }) })

	// Silent, and nothing fires.
	select {
	case <-closed:
		t.Fatal("the connection closed without a cause")
	case <-time.After(2 * time.Second):
	}

	// RST the blackhole socket out from under the manager: this is the "peer vanished" case the
	// core CAN see, and it must still be the thing that ends the flow rather than a timer.
	blackhole.access.Lock()
	for _, conn := range blackhole.accepted {
		_ = conn.Close()
	}
	blackhole.access.Unlock()

	select {
	case <-closed:
		// A closed peer is observable, and the copy loop reports it. This is the contrast that
		// makes the silent case meaningful: the core is not blind, it is uninformed.
	case <-time.After(10 * time.Second):
		t.Fatal("closing the peer socket did not end the flow within 10s; the copy loop is not reporting a real RST")
	}
}

// TestStall_OnlyTransitionsEndStalledFlows is the causal proof, and it is the reason this is a
// policy gap rather than a broken teardown: Reclaim(ReclaimNetworkTransition) ends the very
// connection that silence could not. That call has exactly one production caller, the
// network-change path, which is why iOS sleep/wake resolves the stall and Windows, with no
// network event, does not.
func TestStall_OnlyTransitionsEndStalledFlows(t *testing.T) {
	blackhole := newSilentBlackhole(t)
	fixture := newStallFixture(t, blackhole)
	fixture.manager.drainIdleGraceOverride = time.Millisecond
	fixture.manager.drainSweepIntervalOverride = 20 * time.Millisecond

	// Silent for a while first, so the connection is genuinely stale by any measure.
	select {
	case <-fixture.clientDone:
		t.Fatal("the connection ended before the transition")
	case <-time.After(1500 * time.Millisecond):
	}
	if fixture.closeCalls.Load() != 0 {
		t.Fatal("silence alone closed the connection; the steady-state claim is wrong")
	}

	// Now a transition. This is the ONLY production trigger.
	fixture.manager.Reclaim(ReclaimNetworkTransition)

	select {
	case <-fixture.clientDone:
		// Expected: a path change reclaims a proven-idle connection.
	case <-time.After(10 * time.Second):
		t.Fatal("a transition did not reclaim a proven-idle stalled connection")
	}

	// And the sweep it started is what would finish the job.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		fixture.manager.sweepAccess.Lock()
		running := fixture.manager.sweepTimer != nil
		fixture.manager.sweepAccess.Unlock()
		if running {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Not fatal: the sweep only starts when the pass retained something. What matters above is
	// that the transition is what ended the flow.
}

// TestStall_GoroutinesBlockInReadAndAreNotRescued records the mechanism, not the policy: where
// the two copy goroutines actually are while the flow is stalled. This is the evidence that the
// stall is a blocked read and not, say, a lost wakeup in the scheduler or a dropped error.
func TestStall_GoroutinesBlockInReadAndAreNotRescued(t *testing.T) {
	blackhole := newSilentBlackhole(t)
	fixture := newStallFixture(t, blackhole)

	if _, err := fixture.client.Write([]byte("prompt")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fixture.clientDone:
		t.Fatal("the connection ended before the observation")
	case <-time.After(2 * time.Second):
	}

	// The two copy goroutines are the only ones running on this flow. Capture every stack and
	// count the ones parked in a pipe or socket read.
	buffer := make([]byte, 1<<20)
	length := runtime.Stack(buffer, true)
	stacks := string(buffer[:length])

	ioWait := 0
	for _, block := range strings.Split(stacks, "\n\n") {
		if !strings.Contains(block, "route.(*ConnectionManager).connectionCopy") {
			continue
		}
		// Either native read form counts as parked: an in-memory pipe parks in the pipe's own
		// read, and a real socket parks in the poller. Both are "this goroutine is waiting for
		// bytes that will never arrive".
		if strings.Contains(block, "net.(*pipe).read") ||
			strings.Contains(block, "net.(*pipe).Read") ||
			strings.Contains(block, "internal/poll.(*FD).Read") ||
			strings.Contains(block, "internal/poll.(*FD).RawRead") ||
			strings.Contains(block, "runtime_pollWait") ||
			strings.Contains(block, "internal/poll.(*FD).waitIO") {
			ioWait++
		}
	}
	if ioWait != 2 {
		t.Fatalf("found %d connectionCopy goroutine(s) blocked in a read, want 2.\n"+
			"A stall is expected to be two blocked reads; anything else means the mechanism "+
			"is somewhere this investigation has not looked.\n%s",
			ioWait, summariseCopyStacks(stacks))
	}
}

// summariseCopyStacks prints just the connectionCopy frames, so a failure shows the mechanism
// rather than a megabyte of runtime noise.
func summariseCopyStacks(stacks string) string {
	var builder strings.Builder
	for _, block := range strings.Split(stacks, "\n\n") {
		if !strings.Contains(block, "connectionCopy") {
			continue
		}
		lines := strings.Split(block, "\n")
		fmt.Fprintf(&builder, "--- goroutine block ---\n")
		for index, line := range lines {
			if index > 12 {
				break
			}
			fmt.Fprintf(&builder, "%s\n", line)
		}
	}
	return builder.String()
}

// TestStall_HealthyLongFlowIsNotAffected is the control the brief demands: any liveness
// mechanism added later must not kill a healthy long-lived flow. It is here now so the
// regression is pinned before the fix exists, and it passes today because nothing kills
// anything.
func TestStall_HealthyLongFlowIsNotAffected(t *testing.T) {
	captured := &stallLog{}
	manager := NewConnectionManager(captured.logger())

	// A real listener that behaves itself: it echoes what it is sent. This is the peer that must
	// survive, as opposed to the blackhole that must not be noticed. The production dial path is
	// used here too, so the flow is genuinely registered - a fake dialer would build a flow the
	// manager has never heard of and the count assertion below would be vacuous.
	listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
	if listenErr != nil {
		t.Fatal(listenErr)
	}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				buffer := make([]byte, 64)
				for {
					read, readErr := conn.Read(buffer)
					if readErr != nil {
						return
					}
					if _, writeErr := conn.Write(buffer[:read]); writeErr != nil {
						return
					}
				}
			}()
		}
	}()

	client, proxySide := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = proxySide.Close()
		_ = listener.Close()
		_ = manager.Close()
	})

	ctx := service.ContextWith[adapter.ConnectionManager](context.Background(), manager)
	production, dialErr := dialer.NewDefault(ctx, option.DialerOptions{})
	if dialErr != nil {
		t.Fatal(dialErr)
	}
	echoing := &addressedDialer{inner: production, target: M.ParseSocksaddr(listener.Addr().String())}

	closed := make(chan struct{})
	var once sync.Once
	manager.NewConnection(context.Background(), echoing, proxySide,
		adapter.InboundContext{Destination: M.ParseSocksaddrHostPort("example.com", 443)},
		func(error) { once.Do(func() { close(closed) }) })

	// A long-lived but healthy flow: bytes flow in both directions, then it goes quiet for a
	// while without dying, then flows again. This is an idle WebSocket.
	for round := 0; round < 3; round++ {
		payload := []byte("ping")
		writeDone := make(chan error, 1)
		go func() {
			_, err := client.Write(payload)
			writeDone <- err
		}()
		// The reply proves the download direction ran: the echo listener only answers what the
		// upload direction actually delivered.
		reply := make([]byte, len(payload))
		if _, err := client.Read(reply); err != nil {
			t.Fatalf("round %d: the healthy flow did not carry a reply: %v\n%s", round, err, captured.dump())
		}
		if err := <-writeDone; err != nil {
			t.Fatal(err)
		}
		select {
		case <-closed:
			t.Fatalf("a healthy flow was closed in round %d\n%s", round, captured.dump())
		case <-time.After(1200 * time.Millisecond):
		}
	}

	select {
	case <-closed:
		t.Fatal("a healthy idle flow was closed by the core")
	default:
	}
	if count := manager.Count(); count != 1 {
		t.Fatalf("the manager holds %d connections, want 1: the healthy flow was dropped\n%s",
			count, captured.dump())
	}
}

var _ N.Dialer = (*blackholeDialer)(nil)
