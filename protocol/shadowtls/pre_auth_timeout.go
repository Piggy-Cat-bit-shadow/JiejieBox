package shadowtls

import (
	"encoding/binary"
	"net"
	"sync"
	"time"

	C "github.com/sagernet/sing-box/constant"
)

// tlsRecordHeaderSize is the fixed TLS record header (RFC 8446 §5.1):
// ContentType(1) + legacy_record_version(2) + length(2).
const tlsRecordHeaderSize = 5

// defaultPreAuthTimeout is how long an unauthenticated peer may take to deliver
// its first TLS record.
//
// It reuses C.TCPTimeout rather than adding a configuration knob: "how long may
// an unauthenticated peer stay silent" is the same question C.TCPTimeout already
// answers for protocol/anytls's pre-authentication read, so the two inbounds on
// the public front door share one budget instead of inventing a second one.
const defaultPreAuthTimeout = C.TCPTimeout

// firstRecordTimeoutConn bounds ONLY the read of the first TLS record on the
// public connection, then gets out of the way completely.
//
// # The defect this exists for
//
// The ShadowTLS inbound sits directly behind the public TCP front door.
// sing-shadowtls reads the ClientHello with extractFrame -> io.ReadFull, and
// that read carries no deadline of its own. A peer that opens the port and sends
// nothing, or sends a record header and only part of the body it announced,
// therefore holds the file descriptor, the goroutine, the read buffers and the
// accepted socket open indefinitely. Repeated cheaply -- no credential, no
// knowledge of the protocol, just half a record each -- that exhausts the host.
// protocol/anytls has the same exposure on the same front door and bounds it
// with firstReadTimeoutConn (there the unbounded read is the AnyTLS prologue).
//
// # Why the bound is the FIRST RECORD and not the first byte
//
// A partial ClientHello is precisely the case that has to be covered, and a
// first-byte trigger would already have been disarmed by the time the peer went
// quiet mid-record: extractFrame reads the 5-byte header with ReadFull and then
// the announced body with a second ReadFull, so any byte-count lower than
// 5 + length leaves the peer holding the connection. Disarming exactly at the
// record boundary covers a silent peer, a header-only peer and a truncated body,
// and nothing else.
//
// # Why this cannot hurt a legitimate connection
//
//   - Authenticated clients: the deadline is gone before the service reaches the
//     HMAC relay, so the long-lived inner connection is never subject to it.
//   - The FALLBACK path (an unauthenticated ClientHello relayed to the real
//     handshake target) is only entered after the complete first record has been
//     read, so the relayed browser session runs with no deadline at all. That is
//     the same ordering protocol/anytls relies on: fallback receives the
//     connection only after the bounded read has already succeeded.
//
// The wrapper does not inspect, delay or reorder any byte; after the deadline is
// cleared it is a pass-through that still forwards Read/Write/deadlines to the
// real connection because it embeds it.
type firstRecordTimeoutConn struct {
	net.Conn

	// access guards the one-shot state. Reads happen on the handler goroutine,
	// but the accounting must not be assumed single-threaded by a future caller.
	access sync.Mutex
	// armed is true until the first record has been read in full.
	armed bool
	// header accumulates the record header until its length can be read.
	header       [tlsRecordHeaderSize]byte
	headerFilled int
	// want is the byte count of the whole first record, or 0 until known.
	want int
	// read is the number of bytes consumed since the wrapper was installed.
	read int
}

// newFirstRecordTimeoutConn arms a one-shot read deadline on conn for the first
// TLS record. A non-positive timeout returns conn untouched.
//
// The deadline is installed immediately: the service's first Read may happen
// before the caller could do anything else. A connection that refuses a read
// deadline (a pipe-like net.Conn) is returned unwrapped rather than with a
// deadline that could not be enforced.
func newFirstRecordTimeoutConn(conn net.Conn, timeout time.Duration) net.Conn {
	if timeout <= 0 {
		return conn
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return conn
	}
	return &firstRecordTimeoutConn{
		Conn:  conn,
		armed: true,
	}
}

func (c *firstRecordTimeoutConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.observe(p[:n])
	}
	return n, err
}

// observe accounts for freshly read bytes and disarms the deadline as soon as
// the first record has been consumed in full.
func (c *firstRecordTimeoutConn) observe(data []byte) {
	c.access.Lock()
	defer c.access.Unlock()
	if !c.armed {
		return
	}
	if c.want == 0 {
		need := tlsRecordHeaderSize - c.headerFilled
		if need > len(data) {
			need = len(data)
		}
		copy(c.header[c.headerFilled:], data[:need])
		c.headerFilled += need
		c.read += need
		data = data[need:]
		if c.headerFilled == tlsRecordHeaderSize {
			c.want = tlsRecordHeaderSize + int(binary.BigEndian.Uint16(c.header[3:tlsRecordHeaderSize]))
		}
	}
	c.read += len(data)
	if c.want != 0 && c.read >= c.want {
		c.armed = false
		// The peer has delivered everything this bound was protecting, so the
		// deadline must not survive into the session. A failure to clear it is
		// ignored: the connection is either already gone or the peer's data
		// arrives before the budget expires.
		_ = c.Conn.SetReadDeadline(time.Time{})
	}
}

// preAuthDeadlineArmed reports whether the one-shot deadline is still pending.
// It exists for tests.
func (c *firstRecordTimeoutConn) preAuthDeadlineArmed() bool {
	c.access.Lock()
	defer c.access.Unlock()
	return c.armed
}

// Upstream keeps every optional capability of the accepted connection reachable,
// because a net.Conn wrapper otherwise hides them from common.Cast.
//
// N.CloseOnHandshakeFailure, which this inbound calls on every failed handshake,
// casts the connection to `interface{ SetLinger(int) error }` to turn the refusal
// into a reset instead of a lingering close. With the capability hidden, the
// failure path would silently fall back to a plain close - a small change to how a
// probe is refused, made by a change that is supposed to be about read deadlines
// only. common.Cast unwraps through Upstream, so this single method preserves that
// and every future cast at once.
func (c *firstRecordTimeoutConn) Upstream() any {
	return c.Conn
}
