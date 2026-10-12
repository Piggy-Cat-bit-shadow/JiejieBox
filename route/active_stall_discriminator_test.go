package route

import (
	"context"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
)

// Is there a NON-TIMEOUT way to tell a stalled flow from an idle one?
//
// That question is what the investigation's 85 % ceiling turned on. Every fix the brief forbids is
// a timeout, and a timeout cannot tell these two states apart:
//
//	idle WebSocket   the app has nothing to send and the server has nothing to say
//	stalled flow     the app is waiting for a reply that will never come
//
// Both look like "no bytes". So: does anything ELSE differ, observably, from inside the core?
//
// # The candidate discriminator: a blocked writer
//
// There is one structural difference. A socket's receive buffer is finite. When a flow stalls, the
// application keeps writing until that buffer fills, the write blocks, and from then on the flow
// has a BLOCKED WRITER as well as a blocked reader. A healthy idle flow has only parked READERS.
//
//	healthy idle    upload: parked in Read(inbound)      download: parked in Read(outbound)
//	stalled         upload: parked in WRITE(outbound)    download: parked in Read(outbound)
//
// That is not a timeout and not a protocol special case: it is "the peer stopped accepting bytes
// AND stopped sending them", which cannot make progress in either direction no matter how long it
// waits. A flow in that state is ended by nothing that was alive.
//
// # What these two tests establish
//
//	TestStall_RED_IdleAndStalledAreDistinguishable   a full chain DOES produce a blocked writer
//	TestStall_RED_HealthyIdleFlowHasNoBlockedWriter  a healthy idle flow does NOT
//
// If both hold, the discriminator is real, and my earlier conclusion - "a RED test is impossible
// because any fix must be a timeout" - was wrong for any fix keyed on this signal instead.

// copyObservation is what the live goroutine dump says about the two copy goroutines.
type copyObservation struct {
	copies          int
	uploadFrame     string
	downloadFrame   string
	uploadWriting   bool
	downloadWriting bool
	uploadParked    bool
	downloadParked  bool
	// sameSocket is true when both copies are parked on the same outbound descriptor: one writing
	// to it, one reading it. That is the deadlock, and it is visible in the dump.
	sameSocket bool
}

// observeCopyGoroutines classifies the two connectionCopy goroutines from a live stack dump.
//
// Direction is decided by the copy helper the goroutine is inside and the operation it is waiting
// on - never by the function's arguments, which a stack dump does not print:
//
//	upload    copyExtended / copyExtendedWithPool; its destination is the outbound -> a write frame
//	download  copyWaitWithPool, the syscall-direct reader; its source is the outbound -> a read frame
//
// The first version of this keyed on `net.(*pipe)` appearing in the block, which is true only while
// a pipe read is the innermost frame. Once the upload is parked in a socket WRITE the pipe does not
// appear at all, so the upload came back unclassified and the experiment looked like a failure when
// it had in fact produced exactly the state it was looking for.
func observeCopyGoroutines() copyObservation {
	var observation copyObservation
	buffer := make([]byte, 1<<21)
	length := runtime.Stack(buffer, true)
	stacks := string(buffer[:length])

	descriptors := map[string]bool{}
	for _, block := range strings.Split(stacks, "\n\n") {
		if !strings.Contains(block, "route.(*ConnectionManager).connectionCopy") {
			continue
		}
		observation.copies++
		frame := firstSocketFrame(block)
		writing := strings.Contains(frame, ".Write(")

		isDownload := strings.Contains(block, "copyWaitWithPool") ||
			strings.Contains(block, "syscallReadWaiter")
		if isDownload {
			observation.downloadFrame = frame
			observation.downloadParked = true
			observation.downloadWriting = writing
		} else {
			observation.uploadFrame = frame
			observation.uploadParked = true
			observation.uploadWriting = writing
		}
		if descriptor := pollDescriptor(block); descriptor != "" {
			descriptors[descriptor] = true
		}
	}
	observation.sameSocket = observation.copies == 2 && len(descriptors) == 1
	return observation
}

// pollDescriptor extracts the poll.FD pointer a goroutine is waiting on, so two copies parked on the
// same socket can be told apart from two copies parked on different sockets.
func pollDescriptor(block string) string {
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		for _, marker := range []string{
			"internal/poll.(*FD).waitIO(", "internal/poll.(*FD).Write(", "internal/poll.(*FD).RawRead(",
		} {
			index := strings.Index(line, marker)
			if index < 0 {
				continue
			}
			rest := line[index+len(marker):]
			if comma := strings.IndexAny(rest, ",)"); comma > 0 {
				return rest[:comma]
			}
		}
	}
	return ""
}

// firstSocketFrame returns the first frame that names a socket operation TYPE, which is what tells
// a blocked write from a blocked read.
//
// `runtime_pollWait` is deliberately NOT in this list even though it is the innermost frame of both:
// it is reached for reads and writes alike and the read/write distinction lives in its second
// argument (0x72 vs 0x77) rather than in the frame name. Matching it first made every parked
// goroutine look identical, which is what the second version of this classifier got wrong.
func firstSocketFrame(block string) string {
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		for _, marker := range []string{
			"net.(*pipe).Write",
			"net.(*pipe).Read",
			"internal/poll.(*FD).Write(",
			"internal/poll.(*FD).Read(",
			"internal/poll.(*FD).RawWrite(",
			"internal/poll.(*FD).RawRead(",
			"net.(*conn).Write(",
			"net.(*conn).Read(",
		} {
			if strings.Contains(line, marker) {
				return marker
			}
		}
	}
	return "<no socket operation frame>"
}

// dumpCopyBlocks prints the full frames of every connectionCopy goroutine, so a classification that
// comes back empty can be read rather than guessed at.
func dumpCopyBlocks(t *testing.T) {
	t.Helper()
	buffer := make([]byte, 1<<21)
	length := runtime.Stack(buffer, true)
	for index, block := range strings.Split(string(buffer[:length]), "\n\n") {
		if !strings.Contains(block, "connectionCopy") {
			continue
		}
		lines := strings.Split(block, "\n")
		t.Logf("--- copy block %d ---", index)
		for lineIndex, line := range lines {
			if lineIndex > 16 {
				t.Logf("    ... (%d more frames)", len(lines)-lineIndex)
				break
			}
			t.Logf("    %s", line)
		}
	}
}

// fillTheChain writes on the application side until that write itself blocks, which is the moment
// every buffer between the app and the blackholed peer is full.
func fillTheChain(t *testing.T, conn net.Conn, budget time.Duration) bool {
	t.Helper()
	stopped := make(chan struct{})
	var wrote atomic.Int64
	go func() {
		defer close(stopped)
		payload := make([]byte, 32*1024)
		for {
			if _, err := conn.Write(payload); err != nil {
				return
			}
			wrote.Add(int64(len(payload)))
		}
	}()
	select {
	case <-stopped:
		t.Logf("the application write returned after %d bytes", wrote.Load())
		return false
	case <-time.After(budget):
		t.Logf("the application write blocked after %d bytes", wrote.Load())
		return true
	}
}

// TestStall_RED_IdleAndStalledAreDistinguishable builds the stalled state on purpose and asks
// whether it is observably different from a healthy idle one.
func TestStall_RED_IdleAndStalledAreDistinguishable(t *testing.T) {
	blackhole := newSilentBlackhole(t)
	fixture := newStallFixture(t, blackhole)

	if !fillTheChain(t, fixture.client, 4*time.Second) {
		t.Fatal("the application never blocked, so the chain was never filled and this test is not " +
			"observing the state it claims to observe")
	}

	observation := observeCopyGoroutines()
	t.Logf("copies=%d upload=%q download=%q", observation.copies, observation.uploadFrame, observation.downloadFrame)
	if !observation.uploadParked || !observation.downloadParked {
		dumpCopyBlocks(t)
	}
	if observation.copies != 2 {
		t.Fatalf("found %d connectionCopy goroutine(s), want 2", observation.copies)
	}
	if !observation.uploadParked || !observation.downloadParked {
		t.Fatalf("the two copies were not both located: upload=%v download=%v",
			observation.uploadParked, observation.downloadParked)
	}

	// The claim under test: a fully stalled flow has a writer that cannot proceed.
	if !observation.uploadWriting {
		t.Fatalf("the upload copy is parked in %q, not a write, so a full chain does not produce "+
			"the writer signature the discriminator depends on.\ndownload: %q",
			observation.uploadFrame, observation.downloadFrame)
	}

	// And it is the SAME socket: one copy writing it, one reading it, neither able to proceed.
	// That is the deadlock, stated as an assertion rather than described in prose.
	if !observation.sameSocket {
		t.Fatalf("the two copies are not parked on one descriptor, so this is not the mutual "+
			"deadlock the discriminator claims.\nupload: %q\ndownload: %q",
			observation.uploadFrame, observation.downloadFrame)
	}
	t.Log("DEADLOCK: upload parked writing the outbound, download parked reading the same outbound")

	// Still alive to the manager: nothing noticed, nothing closed it.
	if count := fixture.manager.Count(); count != 1 {
		t.Fatalf("the manager holds %d connections, want 1 (open and stalled)", count)
	}
	select {
	case <-fixture.clientDone:
		t.Fatal("the flow ended; this test needs it still stalled")
	default:
	}
}

// TestStall_RED_HealthyIdleFlowHasNoBlockedWriter is the control without which "the writer is
// blocked" would be meaningless. The flow exchanges a few messages, then goes quiet with nothing to
// send in either direction - an idle WebSocket.
func TestStall_RED_HealthyIdleFlowHasNoBlockedWriter(t *testing.T) {
	captured := &stallLog{}
	manager := NewConnectionManager(captured.logger())

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
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
	t.Cleanup(func() { _ = client.Close(); _ = proxySide.Close(); _ = manager.Close() })

	closed := make(chan struct{})
	var once sync.Once
	manager.NewConnection(context.Background(),
		newDialerTo(t, manager, M.ParseSocksaddr(listener.Addr().String())), proxySide,
		adapter.InboundContext{Destination: M.ParseSocksaddrHostPort("ws.chatgpt.com", 443)},
		func(error) { once.Do(func() { close(closed) }) })

	// Prove it carries bytes both ways, then let it go idle.
	for round := 0; round < 2; round++ {
		go func() { _, _ = client.Write([]byte("ping")) }()
		reply := make([]byte, 4)
		if _, err := client.Read(reply); err != nil {
			t.Fatalf("the healthy fixture did not carry a reply: %v\n%s", err, captured.dump())
		}
	}
	select {
	case <-closed:
		t.Fatalf("the healthy flow closed before it could be observed\n%s", captured.dump())
	case <-time.After(1500 * time.Millisecond):
	}

	observation := observeCopyGoroutines()
	t.Logf("copies=%d upload=%q download=%q", observation.copies, observation.uploadFrame, observation.downloadFrame)

	if observation.copies != 2 {
		t.Fatalf("found %d connectionCopy goroutine(s), want 2", observation.copies)
	}

	// The control's assertion. If this fails, the signal does not separate the states and cannot
	// be the basis of any fix - which is a real possible outcome and the reason this test exists.
	if observation.uploadWriting || observation.downloadWriting {
		t.Fatalf("a HEALTHY IDLE flow has a blocked writer (upload=%v download=%v), so writer state "+
			"does not separate idle from stalled.\nupload: %q\ndownload: %q",
			observation.uploadWriting, observation.downloadWriting,
			observation.uploadFrame, observation.downloadFrame)
	}

	if count := manager.Count(); count != 1 {
		t.Fatalf("the healthy flow left the manager (%d connections)\n%s", count, captured.dump())
	}
}

// newDialerTo builds the production dialer bound to this manager, aimed at target. It is the
// blackhole fixture's helper generalised, so the control can point at a well-behaved peer instead.
func newDialerTo(t *testing.T, manager *ConnectionManager, target M.Socksaddr) *addressedDialer {
	t.Helper()
	ctx := service.ContextWith[adapter.ConnectionManager](context.Background(), manager)
	production, err := dialer.NewDefault(ctx, option.DialerOptions{})
	if err != nil {
		t.Fatal("could not build the production dialer: ", err)
	}
	return &addressedDialer{inner: production, target: target}
}
