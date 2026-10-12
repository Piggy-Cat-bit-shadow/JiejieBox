package v2rayhttp

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
	N "github.com/sagernet/sing/common/network"
)

// scriptedConn serves a fixed byte stream and records whether it was read directly, which is how the
// unwrap is observed: if the copy path unwraps past HTTPConn it reaches THIS type, and the bytes
// HTTPConn cached are never seen by anyone.
type scriptedConn struct {
	reader *bytes.Reader
	direct int
}

func (c *scriptedConn) Read(p []byte) (int, error) {
	c.direct++
	return c.reader.Read(p)
}
func (c *scriptedConn) Write(p []byte) (int, error)        { return len(p), nil }
func (c *scriptedConn) Close() error                       { return nil }
func (c *scriptedConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (c *scriptedConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (c *scriptedConn) SetDeadline(t time.Time) error      { return nil }
func (c *scriptedConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *scriptedConn) SetWriteDeadline(t time.Time) error { return nil }

// TestHTTPConnIsNotReplaceableWhileItHoldsCachedBody is the regression guard for a silent
// response-body truncation.
//
// Reading the response headers through a bufio.Reader over-reads: whenever the server's first body
// bytes arrive in the same segment as the headers, `reader.Buffered()` is non-zero and HTTPConn
// stashes those bytes in responseCache. Only Read serves them. If ReaderReplaceable() says true at
// that moment, `N.UnwrapReader` follows it through Upstream() to the RAW connection and hands that to
// the copy loop - so the cached bytes are never delivered and the head of the body disappears with no
// error anywhere.
//
// The assertion is therefore two-sided: unreplaceable while the cache is held, replaceable once it is
// drained. A fix that simply returned false always would satisfy the first half and break the
// optimisation the flag exists for.
func TestHTTPConnIsNotReplaceableWhileItHoldsCachedBody(t *testing.T) {
	const body = "the-body-bytes-the-copy-path-must-not-lose"

	// Headers and body in ONE write, which is what makes bufio over-read and populate the cache.
	raw := "HTTP/1.1 200 OK\r\n" +
		"Content-Length: " + itoa(len(body)) + "\r\n" +
		"\r\n" + body
	inner := &scriptedConn{reader: bytes.NewReader([]byte(raw))}

	request, err := http.NewRequest("GET", "http://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	conn := &HTTPConn{Conn: inner, request: request}

	// Before anything is read the wrapper is not replaceable either - the response has not been
	// parsed, so Read has to run first.
	if conn.ReaderReplaceable() {
		t.Fatal("a connection whose response has not been read must not be replaceable")
	}

	got := make([]byte, 0, len(body))
	chunk := make([]byte, 8)
	for len(got) < len(body) {
		n, readErr := conn.Read(chunk)
		got = append(got, chunk[:n]...)
		if readErr != nil {
			break
		}
	}
	if string(got) != body {
		t.Fatalf("HTTPConn.Read delivered %q, want %q", got, body)
	}

	// The cache is drained by the reads above, so it must now be safe to unwrap - and saying so is
	// the whole point of the flag.
	if !conn.ReaderReplaceable() {
		t.Fatal("once the response cache is drained the connection must be replaceable again, " +
			"otherwise the copy path loses an unwrap it is entitled to")
	}

	// And the guarding property, stated directly: while a cache is held the answer must be false.
	// This is asserted on the field rather than through Read so that it fails for the right reason if
	// the Read loop above is ever changed.
	held := &HTTPConn{Conn: &scriptedConn{reader: bytes.NewReader(nil)}, request: request}
	held.responseRead = true
	held.responseCache = buf.NewSize(4)
	if _, err := held.responseCache.WriteString("head"); err != nil {
		t.Fatal(err)
	}
	if held.ReaderReplaceable() {
		t.Fatal("a connection holding an undrained response cache must NOT be replaceable: the " +
			"copy path would unwrap to the raw connection and drop those bytes")
	}
	// Same state, cache released: now it must be replaceable. Without this the test above would pass
	// for a ReaderReplaceable that simply returns false.
	held.responseCache.Release()
	held.responseCache = nil
	if !held.ReaderReplaceable() {
		t.Fatal("with no cache held, a parsed response must be replaceable")
	}
}

// TestUnwrapReaderStopsAtHTTPConnWhileCached pins the consequence at the level that actually matters:
// N.UnwrapReader itself, not just the flag. If this ever returns the raw connection while a cache is
// held, the body bytes in it are unreachable by construction.
func TestUnwrapReaderStopsAtHTTPConnWhileCached(t *testing.T) {
	inner := &scriptedConn{reader: bytes.NewReader([]byte("x"))}
	request, err := http.NewRequest("GET", "http://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	conn := &HTTPConn{Conn: inner, request: request}
	conn.responseRead = true
	conn.responseCache = buf.NewSize(4)
	if _, err := conn.responseCache.WriteString("head"); err != nil {
		t.Fatal(err)
	}

	unwrapped := N.UnwrapReader(conn)
	if unwrapped != io.Reader(conn) {
		t.Fatalf("N.UnwrapReader must stop at HTTPConn while it holds a response cache, got %T",
			unwrapped)
	}

	// With the cache gone it must unwrap, or the fix has cost the optimisation it was meant to keep.
	conn.responseCache.Release()
	conn.responseCache = nil
	if unwrapped := N.UnwrapReader(conn); unwrapped != io.Reader(inner) {
		t.Fatalf("with no cache the raw connection must be reachable, got %T", unwrapped)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

var _ = strings.TrimSpace
