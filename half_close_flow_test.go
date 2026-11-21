package box_test

import (
	"io"
	"net"
	"runtime"
	"testing"
	"time"
)

// Half-close, and what happens to the flow when one direction finishes and the other has not.
//
// # Why this is next to the held-flow budget
//
// The budget test measures a connection that is held and QUIET. That is the cheap state, and it is
// also the state in which a broken relay looks perfect: nothing is flowing, so nothing can be lost.
// The expensive question is the one a proxy has to answer every time an HTTP client finishes its
// request - the client half-closes, the server still has a response to send - and the failure mode
// is not a memory number but a lost byte or a goroutine that never returns.
//
// # The two directions, deliberately one at a time
//
// Half-close is per direction, so the sequence is unambiguous:
//
//	client writes a request, then CloseWrite  ->  destination sees the bytes and then EOF
//	destination writes a response, then closes ->  client sees the bytes and then EOF
//
// A relay that turns `CloseWrite` into a full `Close` fails at step two (the response never
// arrives); one that forgets to propagate it fails at step one (the destination blocks forever).
// Neither shows up in an idle-connection budget.
func TestAHalfClosedFlowStillDeliversTheOtherDirection(t *testing.T) {
	if testing.Short() {
		t.Skip("this holds a flow through the real relay; -short skips it")
	}
	const request = "hello from the client"
	const response = "and the answer comes back after the client finished"

	destination, received, closedDestination := talkingDestination(t, response)
	socksAddress := startSOCKSBox(t)
	before := sampleHeldConnections()

	conn, err := dialThroughSOCKS(socksAddress, destination)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err = conn.Write([]byte(request)); err != nil {
		t.Fatalf("write request: %v", err)
	}
	// CloseWrite, not Close: the read half must stay usable for the response.
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		t.Fatalf("expected a *net.TCPConn, got %T", conn)
	}
	if err = tcpConn.CloseWrite(); err != nil {
		t.Fatalf("close write: %v", err)
	}

	// The destination must see the request and then EOF on its read side.
	select {
	case got := <-received:
		if got != request {
			t.Fatalf("the destination received %q, want %q: the relay dropped or reordered the "+
				"bytes that preceded the half-close", got, request)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("the destination never saw EOF after the client half-closed: CloseWrite is not " +
			"propagated through the relay, so a destination that waits for a request boundary " +
			"blocks for the life of the connection")
	}

	// And the response must still arrive after the client's write half is gone.
	reply, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read response after half-close: %v", err)
	}
	if string(reply) != response {
		t.Fatalf("the client read %q, want %q: the response direction was torn down when the "+
			"request direction finished", reply, response)
	}
	_ = conn.Close()
	closedDestination()

	// The teardown half: a flow that went through a half-close must release like any other.
	deadline := time.Now().Add(10 * time.Second)
	for {
		runtime.GC()
		residual := sampleHeldConnections()
		if residual.goroutines <= before.goroutines {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines are still running after a half-closed flow closed "+
				"(baseline %d, now %d): one of the two copies did not finish",
				residual.goroutines-before.goroutines, before.goroutines, residual.goroutines)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// talkingDestination accepts one connection, reads until EOF, reports what it read, then writes its
// answer and closes.
func talkingDestination(t *testing.T, response string) (net.Addr, <-chan string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan string, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		body, readErr := io.ReadAll(conn)
		if readErr != nil {
			return
		}
		received <- string(body)
		_, _ = conn.Write([]byte(response))
	}()
	closeListener := func() { _ = listener.Close() }
	t.Cleanup(closeListener)
	return listener.Addr(), received, closeListener
}
