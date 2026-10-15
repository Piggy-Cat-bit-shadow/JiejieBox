package naive

import (
	"bufio"
	"io"
	"net"
	"testing"
	"time"

	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// An adapter that adds no capability to the WRITE path must not hide the socket's.
//
// # The defect these tests pin
//
// `route/conn.go` ends a finished direction with a half-close when it can, and a full
// close when it cannot:
//
//	destinationWriter, _ := N.UnwrapCountWriter(destination, nil)
//	duplexDst, isDuplex := N.UnwrapWriter(destinationWriter).(N.WriteCloser)
//	if isDuplex { err = duplexDst.CloseWrite() } else { destination.Close() }
//
// `N.UnwrapWriter` stops at the first layer that is not `WriterWithUpstream` or whose
// `WriterReplaceable()` is false (sing common/network/conn.go:224-235).
//
// `hijackedConn` - which wraps every native Naive HTTP/1.1 CONNECT tunnel, because
// `http.Hijacker.Hijack()` always returns a non-nil *bufio.ReadWriter - declared
// `Upstream()` but NOT `WriterReplaceable()`, so the walk stopped at it, the
// `.(N.WriteCloser)` assertion failed, and the branch took `destination.Close()`.
//
// That type intercepts only READS: the bytes net/http had already buffered past the
// request headers. Its `Write` is the embedded conn's - the socket - with nothing
// buffered on the write side, so the write path is transparent and the walk must
// continue through it.
//
// The consequence of stopping was that a peer which half-closed its side and then kept
// reading received a full close instead: an HTTP/1.0-style response-then-close, or any
// full-duplex tunnel whose origin finishes writing while the client is still uploading,
// had the client's in-flight body destroyed at the proxy.
//
// # Why the assertion is the unwrap walk, not a byte count
//
// The defect is "the walk stops one layer early", so the honest thing to measure is the
// walk. A test that only counted bytes would have to build a whole HTTP/1.1 hijack and
// would still not say which branch ran.

// halfCloseTCPConn is a REAL socket pair, so `CloseWrite` is a genuine
// `*net.TCPConn.CloseWrite` rather than a stub that any type could satisfy.
func halfCloseTCPConn(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	accepted := make(chan *net.TCPConn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			close(accepted)
			return
		}
		accepted <- conn.(*net.TCPConn)
	}()

	dialed, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	client := dialed.(*net.TCPConn)
	t.Cleanup(func() { _ = client.Close() })

	server, ok := <-accepted
	require.True(t, ok, "the listener must accept the dial")
	t.Cleanup(func() { _ = server.Close() })
	return client, server
}

// hijackedOver builds the adapter the way inbound.go does at the hijack: the
// *bufio.Reader belongs to the connection, which is the point of the type.
func hijackedOver(conn net.Conn) *hijackedConn {
	return &hijackedConn{Conn: conn, reader: bufio.NewReader(conn)}
}

// TestHijackedConnDoesNotHideTheSocketsCloseWrite is the direct statement of the rule.
//
// The walk has to reach the socket: a caller holding the hijacked conn must be able to
// discover that the write side is half-closable. Before the fix `UnwrapWriter` returned
// the `*hijackedConn` itself, the `N.WriteCloser` assertion failed, and route/conn.go
// took the full-close branch.
func TestHijackedConnDoesNotHideTheSocketsCloseWrite(t *testing.T) {
	client, _ := halfCloseTCPConn(t)
	hijacked := hijackedOver(client)

	require.True(t, hijacked.WriterReplaceable(),
		"hijackedConn must report that its WRITE path is the socket's: it intercepts reads only")

	unwrapped := N.UnwrapWriter(hijacked)
	require.NotSame(t, hijacked, unwrapped,
		"the unwrap walk stopped at hijackedConn, so route/conn.go's half-close branch takes "+
			"destination.Close() and destroys a client upload the peer is still reading")

	_, isWriteCloser := unwrapped.(N.WriteCloser)
	require.True(t, isWriteCloser,
		"the walk must reach a connection that can half-close; it reached %T", unwrapped)
}

// TestNaiveConnOverHijackedConnKeepsHalfClose is the production shape.
//
// The conn the route layer is handed is a `naiveConn` over the hijacked one, so the walk
// crosses TWO adapters. This asserts the whole chain reaches the socket rather than only
// the inner one.
func TestNaiveConnOverHijackedConnKeepsHalfClose(t *testing.T) {
	client, _ := halfCloseTCPConn(t)
	tunnel := &naiveConn{Conn: hijackedOver(client)}

	unwrapped := N.UnwrapWriter(tunnel)
	_, isWriteCloser := unwrapped.(N.WriteCloser)
	require.True(t, isWriteCloser,
		"a naiveConn over a hijackedConn must still expose the socket's CloseWrite; the walk "+
			"reached %T", unwrapped)
}

// TestHijackedConnReadSideIsNotReportedReplaceable is the guard on the fix.
//
// `WriterReplaceable` must not be bought by also declaring the READ side replaceable:
// the buffered prologue is the only reason this adapter exists, and a reader that
// reported itself replaceable would let the core's splice paths read the socket
// directly and skip those bytes, starting the tunnel mid-stream - the first UoT request
// header or padding frame header missing and every later read misaligned.
func TestHijackedConnReadSideIsNotReportedReplaceable(t *testing.T) {
	client, _ := halfCloseTCPConn(t)
	hijacked := hijackedOver(client)

	require.False(t, hijacked.ReaderReplaceable(),
		"the reader is this adapter's own, so it may not be reported as replaceable")

	// The two directions are decided independently: a caller must not be able to conclude
	// from one that the other is the socket's.
	require.True(t, hijacked.WriterReplaceable(),
		"the write direction is the socket's even though the read direction is not")
}

// TestHijackedConnForwardsTheHalfClose exercises the explicit methods, which exist so
// that a caller holding this exact type still gets half-close semantics rather than
// having them silently dropped.
func TestHijackedConnForwardsTheHalfClose(t *testing.T) {
	client, server := halfCloseTCPConn(t)
	hijacked := hijackedOver(client)

	require.NoError(t, hijacked.CloseWrite(),
		"CloseWrite must reach the socket rather than being dropped by the adapter")

	// The peer observes a half-close as EOF on its read side while the socket stays open
	// in the other direction: that is exactly what a half-close means, and it is the
	// difference between this and destination.Close().
	require.NoError(t, server.SetReadDeadline(time.Now().Add(5*time.Second)))
	buffer := make([]byte, 1)
	_, err := server.Read(buffer)
	require.ErrorIs(t, err, io.EOF,
		"the peer must see EOF after CloseWrite, not a full close and not nothing")

	// The opposite direction must still be usable, so the response can arrive.
	_, _ = server.Write([]byte("R"))
	_, err = io.ReadFull(hijacked, buffer)
	require.NoError(t, err, "a half-close must leave the opposite direction usable")
}

// TestHijackedConnCloseReadForwards pins the other half-close, which the same wrapper has
// to carry for the same reason: a dropped CloseRead is a dropped half-close in the other
// direction.
func TestHijackedConnCloseReadForwards(t *testing.T) {
	client, _ := halfCloseTCPConn(t)
	hijacked := hijackedOver(client)
	require.NoError(t, hijacked.CloseRead(),
		"CloseRead must reach the socket rather than being dropped by the adapter")
}
