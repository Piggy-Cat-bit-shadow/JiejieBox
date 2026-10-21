package shadowtls

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	E "github.com/sagernet/sing/common/exceptions"

	"github.com/stretchr/testify/require"
)

// The pre-authentication classifier has to separate two conditions that look
// identical to the net package: "the peer on OUR socket went silent" and "the
// handshake target we dialled never answered".
//
// Both arrive as a net.Error whose Timeout() is true. Only the second is an
// operator-visible fault, and probe_log.go states that contract explicitly - but
// a connect timeout that the Go runtime synthesises (rather than one the OS
// reports as an errno) reaches the classifier through a different branch than the
// errno matrix in probe_log_test.go covers. These tests use errors the runtime
// really produces, so the branch cannot be "covered" by a hand-built value that
// has the wrong shape.

// realDialTimeout produces a genuine Go-level connect timeout.
//
// The deadline is deliberately already expired: net.Dialer.DialContext turns it
// into net.errTimeout (net/dial.go: partialDeadline -> "ran out of time") before
// any socket work, which is the same value a connect that exceeded Dialer.Timeout
// produces. It needs no network and no blackhole, so it cannot flake on an
// environment where a TUN accepts everything.
func realDialTimeout(t *testing.T) error {
	t.Helper()
	var dialer net.Dialer
	dialer.Deadline = time.Now().Add(-time.Hour)
	conn, err := dialer.Dial("tcp", "127.0.0.1:1")
	if conn != nil {
		_ = conn.Close()
	}
	require.Error(t, err)
	return err
}

// TestHandshakeTargetDialTimeoutStaysVisible is the requirement: a configured
// handshake target that never answers is a fault and must stay at error level.
func TestHandshakeTargetDialTimeoutStaysVisible(t *testing.T) {
	dialErr := realDialTimeout(t)

	// Pin the shape, because the whole test is about which branch it takes.
	var opErr *net.OpError
	require.ErrorAs(t, dialErr, &opErr)
	require.Equal(t, "dial", opErr.Op, "the failure must be the DIAL, not a later operation")
	var netErr net.Error
	require.ErrorAs(t, dialErr, &netErr)
	require.True(t, netErr.Timeout())
	require.True(t, errors.Is(dialErr, context.DeadlineExceeded),
		"the Go-level connect timeout reports itself as context.DeadlineExceeded; a "+
			"classifier that downgrades every DeadlineExceeded therefore downgrades "+
			"the handshake target not answering")

	wrapped := E.Cause(dialErr, "server handshake")
	require.False(t, shadowTLSErrorIsExpected(wrapped),
		"a handshake target that times out is an operator-visible fault: the "+
			"configured upstream is not answering, which is exactly the condition "+
			"that must not be hidden at debug")
}

// TestPreAuthReadTimeoutStaysQuiet is the other half, and the control for the
// assertion above: the SAME runtime timeout shape on a read from our own socket is
// a silent prober and must stay at debug.
//
// It also pins the severity of the pre-authentication bound this release adds, so
// that bounding the first record cannot turn into a new source of error-log
// flooding.
func TestPreAuthReadTimeoutStaysQuiet(t *testing.T) {
	server, _ := newTCPPair(t)
	wrapped := newFirstRecordTimeoutConn(server, 20*time.Millisecond)
	buffer := make([]byte, 8)
	_, readErr := wrapped.Read(buffer)
	require.Error(t, readErr)

	var netErr net.Error
	require.ErrorAs(t, readErr, &netErr)
	require.True(t, netErr.Timeout())

	require.True(t, shadowTLSErrorIsExpected(E.Cause(readErr, "read client handshake")),
		"a peer that goes silent on OUR socket is routine scanner traffic and must "+
			"not raise an error log")
}

// TestCanceledDialLooksLikeLifecycleNotAFault keeps the fix narrow: a dial that
// the CALLER canceled is a shutdown, not a handshake target fault, and must stay
// quiet.
func TestCanceledDialLooksLikeLifecycleNotAFault(t *testing.T) {
	canceledDial := &net.OpError{Op: "dial", Net: "tcp", Err: context.Canceled}
	require.True(t, shadowTLSErrorIsExpected(E.Cause(canceledDial, "server handshake")),
		"a canceled dial is the local lifecycle shutting down, not the configured "+
			"handshake target failing")

	// And the deadline variant, when it is NOT about a dial, stays expected too.
	readDeadline := &net.OpError{Op: "read", Net: "tcp", Err: context.DeadlineExceeded}
	require.True(t, shadowTLSErrorIsExpected(readDeadline),
		"a deadline on our own read remains a routine probe outcome")
}
