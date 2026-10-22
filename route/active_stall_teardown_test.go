package route

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
)

// §20 lead: teardown depends on BOTH copies returning.
//
// Reading connectionCopy again with the brief's "check error/cancel propagation, half-close,
// ownership before designing a watchdog" instruction in mind turns up an asymmetry that none of the
// tests so far exercised, because all of them stalled BOTH directions at once.
//
//	first copy to finish   sets done = true and CLOSES ONLY ITS DESTINATION WRITER
//	second copy to finish  runs onClose and closes both connections
//
// So a flow whose upload completes normally, while its download never returns, never runs onClose at
// all. `done` is true, so the upload side has already declined to do it, and the download side is the
// one that is never going to arrive. The connection stays in the manager.
//
// That is not a bug in the half-close handling - half-close is legitimate and required, and
// `CloseWrite` is exactly right for it. It is an ownership observation: the teardown has no owner
// once one direction has finished and the other is parked, and nothing bounds how long that lasts.
// This is the shape §20 asks to look at BEFORE reaching for a watchdog, and it is the closest thing
// found to a defect that does not require a grace period to identify.

// oneSidedFixture is a flow whose upload completes while the download is parked forever.
type oneSidedFixture struct {
	manager   *ConnectionManager
	client    net.Conn
	closed    chan struct{}
	closeCall atomic.Int64
	log       *stallLog
}

// TestTeardown_OneSidedCompletionNeverRunsOnClose pins the asymmetry.
//
// The application writes one request and then CLOSES ITS WRITE SIDE, which is what cleanly finishing
// an upload looks like. The peer never answers. That is the ordinary shape of "request sent, response
// never comes" and it is reached without any blackhole trickery: the peer simply does not reply.
func TestTeardown_OneSidedCompletionNeverRunsOnClose(t *testing.T) {
	// A peer that reads everything and answers nothing: it never closes either.
	peer, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	peerRead := make(chan struct{})
	// peerEOF is closed when the peer's read returns EOF, which is the moment the client's
	// half-close has been carried all the way through the proxy to the peer. It is an ORDERING
	// fact rather than a timing one, and the test waits on it instead of on a sleep: "the upload
	// finished" is something the peer can tell us precisely, and inferring it from elapsed time
	// was guessing at exactly the thing the assertions below depend on.
	peerEOF := make(chan struct{})
	go func() {
		conn, acceptErr := peer.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		buffer := make([]byte, 4096)
		for {
			n, readErr := conn.Read(buffer)
			if n > 0 {
				select {
				case peerRead <- struct{}{}:
				default:
				}
			}
			if readErr != nil {
				// Signal FIRST, then park. The peer must still answer nothing and must NOT
				// close - it stays ESTABLISHED so nothing on this side can observe an error -
				// but the half-close reaching here is the premise of the test, so it is
				// recorded before this goroutine becomes unreachable.
				close(peerEOF)
				select {}
			}
		}
	}()

	captured := &stallLog{}
	manager := NewConnectionManager(captured.logger())
	fixture := &oneSidedFixture{manager: manager, closed: make(chan struct{}), log: captured}

	// The inbound side has to be a REAL TCP connection, not net.Pipe: the premise is a clean
	// half-close by the application, and only a real socket can express one. net.Pipe's Close is
	// all-or-nothing, which is why the first version of this test could not build its own premise.
	inboundListener, listenErr := net.Listen("tcp", "127.0.0.1:0")
	if listenErr != nil {
		t.Fatal(listenErr)
	}
	t.Cleanup(func() { _ = inboundListener.Close() })

	inboundAccepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := inboundListener.Accept()
		if acceptErr == nil {
			inboundAccepted <- conn
		}
	}()

	client, dialErr := net.Dial("tcp", inboundListener.Addr().String())
	if dialErr != nil {
		t.Fatal(dialErr)
	}
	proxySide := <-inboundAccepted
	fixture.client = client
	t.Cleanup(func() { _ = client.Close(); _ = proxySide.Close(); _ = manager.Close() })

	var once sync.Once
	manager.NewConnection(context.Background(),
		newDialerTo(t, manager, M.ParseSocksaddr(peer.Addr().String())), proxySide,
		adapter.InboundContext{Destination: M.ParseSocksaddrHostPort("ws.chatgpt.com", 443)},
		func(error) { once.Do(func() { fixture.closeCall.Add(1); close(fixture.closed) }) })

	if count := manager.Count(); count != 1 {
		t.Fatalf("the flow was not registered (%d connections)\n%s", count, captured.dump())
	}

	// Send the request, then finish the upload the way a clean client does.
	writeDone := make(chan error, 1)
	go func() {
		_, writeErr := client.Write([]byte("the request"))
		writeDone <- writeErr
	}()
	select {
	case writeErr := <-writeDone:
		if writeErr != nil {
			t.Fatalf("the request could not be sent: %v", writeErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the request never went out\n%s", captured.dump())
	}
	select {
	case <-peerRead:
	case <-time.After(10 * time.Second):
		t.Fatalf("the peer never received the request\n%s", captured.dump())
	}

	// Finish the upload. The download is now the only direction left, and it has nothing to read.
	if closer, isCloser := client.(interface{ CloseWrite() error }); isCloser {
		_ = closer.CloseWrite()
	} else {
		t.Fatal("the inbound fixture cannot half-close, so this test cannot build its own premise")
	}

	// Wait for the peer to SEE THE EOF, which is the proof that the upload path completed and
	// propagated its half-close. This replaced a flat 5-second sleep: the sleep was inferring
	// "the upload is done" from elapsed time, and the assertions below - one surviving copy,
	// onClose unrun - are about the state that inference was supposed to have established. A
	// machine slower than the sleep, or one where the half-close is queued behind other work,
	// then failed on the copy count for a reason that had nothing to do with the behaviour
	// under test. The deadline is kept only so a genuine failure to propagate still fails here,
	// with its own message, rather than as a confusing copy-count error later.
	select {
	case <-fixture.closed:
		t.Fatalf("onClose ran; the premise is that a one-sided completion leaves it unrun\n%s",
			captured.dump())
	case <-peerEOF:
	case <-time.After(10 * time.Second):
		t.Fatalf("the peer never saw EOF: the client's half-close did not reach it, so the upload "+
			"path cannot be said to have completed\n%s", captured.dump())
	}

	if count := manager.Count(); count != 1 {
		t.Fatalf("the manager holds %d connections, want 1 (still parked)\n%s", count, captured.dump())
	}
	if calls := fixture.closeCall.Load(); calls != 0 {
		t.Fatalf("onClose fired %d time(s) after a one-sided completion\n%s", calls, captured.dump())
	}

	// And only ONE copy is still alive, which is why nothing ran. The upload finished, set done, and
	// closed only its own destination writer; the download is the copy that would have run onClose,
	// and it is parked in a read that has nothing to return.
	//
	// The count is the assertion. WHICH copy survived is reported, not asserted: telling the two
	// apart from a stack dump means recognising the reader's innermost frame, and that frame is
	// runtime- and platform-dependent - the download parks in `internal/poll.(*FD).RawRead(` on
	// one host and in `copyWaitWithPool` / `syscallReadWaiter` on another. The classifier below
	// only knows the second spelling, so on the first it files the download as the upload and the
	// old `downloadParked` assertion failed on a stack-classifier gap while reporting it as
	// "the surviving copy is not the download". The upload having completed is already
	// established above, by the peer having seen EOF - an ordering fact, not a frame name - and
	// one surviving copy plus an unrun onClose is the state this test exists to pin.
	observation := observeCopyGoroutines()
	t.Logf("one surviving copy: copies=%d upload=%q download=%q",
		observation.copies, observation.uploadFrame, observation.downloadFrame)
	if observation.copies != 1 {
		t.Fatalf("expected exactly one surviving copy, found %d\n%s", observation.copies, captured.dump())
	}
	if !observation.downloadParked {
		// Diagnostic only. The goroutine dump is still worth printing when the classifier
		// cannot name the survivor, because that is how this gap was found at all - but it
		// must not decide the test.
		t.Logf("the surviving copy was classified as an upload rather than a download: "+
			"the stack frame %q is not one this classifier recognises as the reader. "+
			"Copies=%d, so the count assertion above still holds.", observation.uploadFrame, observation.copies)
	}
	t.Log("The flow is registered, has a completed request, an unanswered read, and no owner of its teardown.")

	// # Why this does NOT license a fix
	//
	// The state is cleaner than the blocked-writer signal: no grace period is needed to RECOGNISE it,
	// because the upload goroutine is simply gone. "One copy has finished; the connection can no
	// longer carry anything in that direction" is a fact, not a threshold.
	//
	// But recognising it does not tell you what to do, and this is the sharpest form of the argument
	// against a core-side fix:
	//
	//	curl -X POST ...      sends a request, half-closes, then waits for the response
	//
	// That is exactly this state, and the wait is legitimately unbounded - a slow backend, a long
	// report, a streaming endpoint that has not produced its first byte yet. Bounding the surviving
	// read would kill every one of those. Only the APPLICATION knows whether a response is late or
	// never coming, because only the application knows what it asked for.
	//
	// So this test is a characterisation of the shape §20 asks to look at rather than a defect with a
	// fix attached: the teardown has no owner once one direction has finished, and nothing in the core
	// can distinguish "the answer is late" from "the answer is never coming".
	//
	// # What exists now, and why this test is unchanged
	//
	// A later round added route/conn_halfclose.go. After a clean half-close the finished side is kept
	// under observation: a short keepalive, and a backed-off read that releases the flow ONLY on a
	// terminal error. That error is the one signal this test's premise does not have - the peer here is
	// still there and simply has not answered, so it ACKs the probes and nothing happens. The
	// assertion above (onClose unrun, one surviving copy) is therefore still the correct expectation,
	// and it is exactly what the new mechanism is forbidden to change. Its paired positive controls are
	// `TestHalfClosedLivePeerIsNotReleased`, `TestTUNHalfClosedLiveDeviceKeepsFlow` and
	// `TestTUNHalfClosedSlowResponseIsNotKilled` - a slow but real answer must still arrive.
}
