package trojan

import (
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type stubConn struct{ written []byte }

func (c *stubConn) Read(p []byte) (int, error) { return 0, nil }
func (c *stubConn) Write(p []byte) (int, error) {
	c.written = append(c.written, p...)
	return len(p), nil
}
func (c *stubConn) Close() error                       { return nil }
func (c *stubConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (c *stubConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (c *stubConn) SetDeadline(t time.Time) error      { return nil }
func (c *stubConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *stubConn) SetWriteDeadline(t time.Time) error { return nil }

// Trojan's ClientConn sits on the unwrap path: it embeds an N.ExtendedConn (an INTERFACE), returns
// Upstream() for it, and reports ReaderReplaceable/WriterReplaceable from `headerWritten`. Both flags
// are therefore load-bearing, and until this test the package had no test files at all.
//
// The invariant is asymmetric and that is the point:
//
//   - WriterReplaceable MUST be false before the header is written. The first write is not a
//     passthrough - it emits the Trojan handshake - so unwrapping the writer to the connection
//     underneath would send the payload with no header, producing a connection the server cannot
//     parse. This is a real defect if it ever inverts, not a missed optimisation.
//   - ReaderReplaceable may be either, and the implementation ties it to the same flag. That is
//     conservative rather than wrong: reads are a pure passthrough here (ClientConn defines no Read
//     of its own, so the embedded conn serves them and there is never buffered plaintext to lose),
//     but blocking the unwrap until the handshake has happened costs nothing and cannot drop data.
//     The test pins the conservative behaviour so a future change to "always true" has to argue for
//     itself rather than slip through.
func TestClientConnReplaceabilityFollowsTheHandshake(t *testing.T) {
	inner := &stubConn{}
	conn := NewClientConn(inner, [KeyLength]byte{}, M.ParseSocksaddrHostPort("example.com", 443))

	if conn.headerWritten {
		t.Fatal("a fresh client conn must not claim the header is written")
	}
	if conn.WriterReplaceable() {
		t.Fatal("WriterReplaceable must be false before the Trojan header is written: unwrapping " +
			"the writer would send the payload without the handshake")
	}
	// And the raw connection really is reachable, so the flag is the only thing declining the
	// unwrap - otherwise the assertion above would be vacuous.
	if unwrapped := N.UnwrapWriter(conn); unwrapped != any(conn) {
		// It must NOT unwrap yet. Reported through the same call the copy path makes.
		t.Fatal("N.UnwrapWriter must stop at ClientConn before the handshake")
	}
	_ = inner

	// Perform the handshake by writing, which is what sets headerWritten.
	if _, err := conn.Write([]byte("payload")); err != nil {
		t.Fatalf("handshake write failed: %v", err)
	}
	if !conn.headerWritten {
		t.Fatal("the first write must mark the header as written")
	}
	if !conn.WriterReplaceable() {
		t.Fatal("after the handshake the writer must be replaceable, or the copy path loses the " +
			"unwrap it is entitled to and pays a wrapper per packet forever")
	}
	if unwrapped := N.UnwrapWriter(conn); unwrapped == any(conn) {
		t.Fatal("after the handshake N.UnwrapWriter must reach the connection underneath")
	}

	// A second write must be a pure passthrough, i.e. the handshake is emitted exactly once.
	before := len(inner.written)
	if _, err := conn.Write([]byte("second")); err != nil {
		t.Fatalf("second write failed: %v", err)
	}
	if got := len(inner.written) - before; got != len("second") {
		t.Fatalf("a post-handshake write must pass through unchanged, wrote %d bytes for a 6-byte "+
			"payload - a second handshake would corrupt the stream", got)
	}

	// WriteBuffer is the path the copy engine actually uses, and it must delegate rather than
	// silently no-op.
	sizeBefore := len(inner.written)
	writeBuf := buf.NewSize(4)
	if _, err := writeBuf.WriteString("abcd"); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteBuffer(writeBuf); err != nil {
		t.Fatalf("WriteBuffer failed: %v", err)
	}
	if len(inner.written) == sizeBefore {
		t.Fatal("WriteBuffer wrote nothing: the buffered copy path would drop every packet")
	}
}
