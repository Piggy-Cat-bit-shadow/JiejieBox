package resource

// The reF1nd "cached first packet" mechanism, re-run against the PINNED dependency.
//
// # Why this file exists
//
// The stage-03 record closed this item as NOT_ADOPTED because "the seam is in the
// sing fork and moving the pin is a sub-repo change". That is no longer the fact:
// the pinned sing fork ships the mechanism (common/bufio/cache.go: CachedConn,
// CachedReader, CachedPacketConn) and this repository's router already consumes it
// at the production entries asserted at the bottom of this file. This harness is the
// re-runnable evidence the item asked for: it pins the semantics the production
// chain relies on and measures the userspace cost of the first packet with and
// without the cache.
//
// # What is measured here
//
//   - the cached first packet is delivered exactly once, to the destination the
//     sniffer saw, and the underlying conn is NOT read for it (the saving);
//   - the one-shot contract: the second ReadCachedPacket is nil, and after it the
//     wrapper reports itself replaceable, which is what lets later unwrapping and
//     batch readers reach the real conn;
//   - Close before the take releases the cached bytes without reading the conn;
//   - userspace cost and allocations of "deliver one first packet", cached vs
//     re-read (BenchmarkFirstPacket*).
//
// # What is NOT measured here
//
// The device-side read this avoids: on TUN the packet has already been read once to
// sniff it, and the saving is a second read of the device plus the round trip
// through the NAT table, not a userspace copy. That needs a real TUN (WSL or a
// device) and belongs to the TUN part of this window's item 2, not to this stand.
// The numbers below therefore support "the mechanism is present, its semantics are
// intact and its userspace overhead is bounded", not "the device benefit is proven".
//
// # Re-run
//
//	go test ./test/resource -run TestCached -bench BenchmarkFirstPacket -benchmem

import (
	"bytes"
	"io"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
	singbufio "github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

var testDestination = M.ParseSocksaddr("192.0.2.10:443")

// probePacketConn is a packet conn whose reads are counted: the point of the cache
// is that the first packet does not cause one.
type probePacketConn struct {
	packets      [][]byte
	destinations []M.Socksaddr
	reads        atomic.Int64
	closed       atomic.Bool
	readErr      error
}

func newProbePacketConn(packets ...[]byte) *probePacketConn {
	conn := &probePacketConn{}
	for _, packet := range packets {
		conn.packets = append(conn.packets, packet)
		conn.destinations = append(conn.destinations, testDestination)
	}
	return conn
}

func (c *probePacketConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	if len(c.packets) == 0 {
		if c.readErr != nil {
			return M.Socksaddr{}, c.readErr
		}
		return M.Socksaddr{}, io.EOF
	}
	c.reads.Add(1)
	packet := c.packets[0]
	destination := c.destinations[0]
	c.packets = c.packets[1:]
	c.destinations = c.destinations[1:]
	_, err := buffer.Write(packet)
	return destination, err
}

func (c *probePacketConn) WritePacket(buffer *buf.Buffer, _ M.Socksaddr) error {
	buffer.Release()
	return nil
}

func (c *probePacketConn) Close() error {
	c.closed.Store(true)
	return nil
}

func (c *probePacketConn) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (c *probePacketConn) SetDeadline(time.Time) error      { return nil }
func (c *probePacketConn) SetReadDeadline(time.Time) error  { return nil }
func (c *probePacketConn) SetWriteDeadline(time.Time) error { return nil }

// recordingPacketWriter records the delivered payloads and destinations in order.
type recordingPacketWriter struct {
	payloads     [][]byte
	destinations []M.Socksaddr
}

func (w *recordingPacketWriter) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	w.payloads = append(w.payloads, bytes.Clone(buffer.Bytes()))
	w.destinations = append(w.destinations, destination)
	buffer.Release()
	return nil
}

func cachedFirstPacket(payload []byte, conn N.PacketConn) *singbufio.CachedPacketConn {
	cache := buf.NewSize(len(payload))
	_, _ = cache.Write(payload)
	return singbufio.NewCachedPacketConn(conn, cache, testDestination)
}

// TestCachedFirstPacketIsDeliveredWithoutARead pins the whole saving in one test:
// the payload the sniffer saw is delivered first, to the sniffer's destination, and
// only the packets after it cost an underlying read.
func TestCachedFirstPacketIsDeliveredWithoutARead(t *testing.T) {
	first := []byte("first packet from the tun")
	second := []byte("second packet")
	conn := newProbePacketConn(second)
	cached := cachedFirstPacket(first, conn)
	writer := &recordingPacketWriter{}

	_, err := singbufio.CopyPacket(writer, cached)
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, [][]byte{first, second}, writer.payloads)
	require.Equal(t, []M.Socksaddr{testDestination, testDestination}, writer.destinations)
	require.Equal(t, int64(1), conn.reads.Load(),
		"only the packet after the cached one may cost a read")

	// The control: the same two packets without the cache cost one more read.
	plain := newProbePacketConn(first, second)
	plainWriter := &recordingPacketWriter{}
	_, err = singbufio.CopyPacket(plainWriter, plain)
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, [][]byte{first, second}, plainWriter.payloads)
	require.Equal(t, int64(2), plain.reads.Load())
	require.Equal(t, conn.reads.Load()+1, plain.reads.Load(),
		"the cache must save exactly the read of the first packet")
}

// TestCachedFirstPacketIsOneShot pins the one-shot contract the router depends on:
// one take, then the wrapper reports itself replaceable so later unwrapping (and the
// batch/statistics wrappers above it) see the real reader again.
func TestCachedFirstPacketIsOneShot(t *testing.T) {
	first := []byte("cached")
	conn := newProbePacketConn()
	cached := cachedFirstPacket(first, conn)

	require.False(t, cached.ReaderReplaceable(), "before the take the wrapper must not be replaced")
	packet := cached.ReadCachedPacket()
	require.NotNil(t, packet)
	require.Equal(t, first, packet.Buffer.Bytes())
	require.Equal(t, testDestination, packet.Destination)
	packet.Buffer.Release()
	N.PutPacketBuffer(packet)

	require.Nil(t, cached.ReadCachedPacket(), "the cache is one-shot")
	require.True(t, cached.ReaderReplaceable(), "after the take the wrapper is transparent")
	require.Same(t, conn, cached.Upstream())
	require.Equal(t, int64(0), conn.reads.Load())
}

// TestCachedFirstPacketReleasesOnClose pins that a conn torn down before the take
// does not hold the cached bytes and does not read the conn.
func TestCachedFirstPacketReleasesOnClose(t *testing.T) {
	conn := newProbePacketConn()
	cached := cachedFirstPacket([]byte("never read"), conn)
	require.NoError(t, cached.Close())
	require.True(t, conn.closed.Load())
	require.Nil(t, cached.ReadCachedPacket(), "a closed wrapper must hand out nothing")
	require.Equal(t, int64(0), conn.reads.Load())
	require.NoError(t, cached.Close(), "Close must stay idempotent")
}

// TestProbabilityChainConsumesTheDependency is the wiring checkpoint: the pinned
// dependency has the mechanism AND the production router calls it, so this item does
// not reopen merely because the fork's capability is unknown. Paths are relative to
// test/resource; they fail loudly if the production entries move.
func TestProbabilityChainConsumesTheDependency(t *testing.T) {
	routeSource, err := os.ReadFile("../../route/route.go")
	require.NoError(t, err)
	source := string(routeSource)
	require.Contains(t, source, "bufio.NewCachedConn(conn, buffer)",
		"the TCP path must still hand its sniffed first buffers to the copy engine through the cache")
	require.Contains(t, source, "bufio.NewCachedPacketConn(conn, buffer.Buffer, buffer.Destination)",
		"the UDP path must still hand its sniffed first packets to the copy engine through the cache")
}

// BenchmarkFirstPacketCached measures delivering one first packet through the cache:
// one pooled PacketBuffer and one copy into the destination buffer, plus the cache
// buffer the wrapper is handed. In production that buffer is the one the sniffer
// already owns (route.go passes the first packet's buffer straight in), so the setup
// share of this number is not a cost the cache introduces; the comparison below is
// read as "what does delivering the same packet cost on each side".
func BenchmarkFirstPacketCached(b *testing.B) {
	payload := bytes.Repeat([]byte("c"), 1200)
	b.ReportAllocs()
	for b.Loop() {
		conn := newProbePacketConn()
		cached := cachedFirstPacket(payload, conn)
		target := buf.NewSize(len(payload))
		_, err := cached.ReadPacket(target)
		if err != nil {
			b.Fatalf("ReadPacket: %v", err)
		}
		target.Release()
		_ = cached.Close()
	}
}

// BenchmarkFirstPacketReread measures the same delivery when the conn is read for it
// instead. The comparison is userspace only (see the header): the device read the
// cache avoids is not priced here, and that saving is the reason the cache exists.
func BenchmarkFirstPacketReread(b *testing.B) {
	payload := bytes.Repeat([]byte("r"), 1200)
	conn := newProbePacketConn(payload)
	b.ReportAllocs()
	for b.Loop() {
		// Reborrow the one packet the conn holds; the read is what this side pays.
		conn.packets = append(conn.packets[:0], payload)
		conn.destinations = append(conn.destinations[:0], testDestination)
		target := buf.NewSize(len(payload))
		_, err := conn.ReadPacket(target)
		if err != nil {
			b.Fatalf("ReadPacket: %v", err)
		}
		target.Release()
	}
}
