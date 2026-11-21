package box_test

import (
	"net"
	"runtime"
	"testing"
	"time"
)

// A destination that resets, rather than closing, must tear the flow down - not leave one copy
// scanning an error path and the other parked in a read forever.
//
// # Why this is the third of the three
//
// The budget test covers a quiet held flow, the half-close test covers a graceful one, and this
// covers the abrupt one. A relay's terminations are not symmetric: a FIN arrives as `io.EOF`, which
// the copy loop can treat as "this direction is done, flush and finish", while an RST arrives as a
// read error, which takes a different branch. A copy that treats only EOF as terminal waits for a
// write that will never complete, and the leaked goroutine is per flow - the accumulation a long NE
// session would show as a stack that never comes back down.
//
// The client's own result is deliberately not pinned to one errno: whether the reset surfaces as
// ECONNRESET, EPIPE or a plain EOF depends on the platform and on how far the write got. What is
// pinned is that BOTH directions finish and the process returns to its baseline.
func TestAFlowThatIsResetIsTornDown(t *testing.T) {
	if testing.Short() {
		t.Skip("this holds a flow through the real relay; -short skips it")
	}
	destination, closedDestination := resettingDestination(t)
	socksAddress := startSOCKSBox(t)
	before := sampleHeldConnections()

	conn, err := dialThroughSOCKS(socksAddress, destination)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// The destination resets on the first byte it receives, so the write may fail rather than the
	// read; both are legal and neither is what this test is about.
	_, _ = conn.Write([]byte("go away"))
	buffer := make([]byte, 64)
	for {
		_, readErr := conn.Read(buffer)
		if readErr != nil {
			// Any terminal error is acceptable: whether the reset surfaces as ECONNRESET or a plain EOF
			// depends on the platform and on how far the write got. What matters is that it IS terminal.
			t.Logf("the flow ended with %v", readErr)
			break
		}
	}
	_ = conn.Close()
	closedDestination()

	deadline := time.Now().Add(10 * time.Second)
	for {
		runtime.GC()
		residual := sampleHeldConnections()
		if residual.goroutines <= before.goroutines {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines are still running after a reset flow closed (baseline %d, now "+
				"%d): one copy treated only EOF as terminal and is still waiting on the other",
				residual.goroutines-before.goroutines, before.goroutines, residual.goroutines)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// resettingDestination accepts one connection and abandons it with a zero linger, which sends an
// RST instead of a FIN.
func resettingDestination(t *testing.T) (net.Addr, func()) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			_ = tcpConn.SetLinger(0)
		}
		_ = conn.Close()
	}()
	closeListener := func() { _ = listener.Close() }
	t.Cleanup(closeListener)
	return listener.Addr(), closeListener
}
