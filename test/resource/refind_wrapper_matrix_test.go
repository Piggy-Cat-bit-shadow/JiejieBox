package resource

// The reF1nd first-packet wrapper matrix: what the cache does to NAT destination
// fidelity, batch and connected-batch writers, and byte counters.
//
// The first test file pins the saving and the one-shot contract. These pin the
// surfaces a packet flow actually passes through above and below the cache, because
// the cache sits in the middle of the production chain:
//
//	route/route.go: conn = bufio.NewCachedPacketConn(conn, buffer.Buffer, buffer.Destination)
//
// so "where the counter sits" and "what the destination becomes" are ownership
// questions the cache can silently get wrong, not cosmetic ones.

import (
	"bytes"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
	singbufio "github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

var matrixDestination = M.ParseSocksaddr("203.0.113.7:443")

// destinationProbeConn reports one destination for its first packet and a different
// one afterwards, so a cache that silently re-read the conn for the first packet - and
// therefore relabelled it - is caught.
type destinationProbeConn struct {
	secondDestination M.Socksaddr
	payloads          [][]byte
	reads             atomic.Int64
}

func (c *destinationProbeConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	if len(c.payloads) == 0 {
		return M.Socksaddr{}, io.EOF
	}
	payload := c.payloads[0]
	c.payloads = c.payloads[1:]
	c.reads.Add(1)
	_, err := buffer.Write(payload)
	return c.secondDestination, err
}

func (c *destinationProbeConn) WritePacket(buffer *buf.Buffer, _ M.Socksaddr) error {
	buffer.Release()
	return nil
}

func (c *destinationProbeConn) Close() error                     { return nil }
func (c *destinationProbeConn) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (c *destinationProbeConn) SetDeadline(time.Time) error      { return nil }
func (c *destinationProbeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *destinationProbeConn) SetWriteDeadline(time.Time) error { return nil }

// packetSink records deliveries. The batch variants exist so one type can stand in for
// each capability the copy engine looks for.
type packetSink struct {
	payloads     [][]byte
	destinations []M.Socksaddr
	batchCalls   atomic.Int64
}

func (s *packetSink) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	s.payloads = append(s.payloads, bytes.Clone(buffer.Bytes()))
	s.destinations = append(s.destinations, destination)
	buffer.Release()
	return nil
}

type batchSink struct{ *packetSink }

func (s batchSink) WritePacketBatch(buffers []*buf.Buffer, destinations []M.Socksaddr) error {
	s.batchCalls.Add(1)
	for index, buffer := range buffers {
		_ = s.WritePacket(buffer, destinations[index])
	}
	return nil
}

type connectedBatchSink struct{ *packetSink }

func (s connectedBatchSink) WriteConnectedPacketBatch(buffers []*buf.Buffer) error {
	s.batchCalls.Add(1)
	for _, buffer := range buffers {
		_ = s.WritePacket(buffer, matrixDestination)
	}
	return nil
}

// countingReaderAbove is the same counter face, but above a cached conn: it delegates
// reads to the cache and counts what the copy engine reports.
type countingReaderAbove struct {
	cached  *singbufio.CachedPacketConn
	counter *atomic.Int64
}

func (r *countingReaderAbove) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	return r.cached.ReadPacket(buffer)
}

func (r *countingReaderAbove) UnwrapPacketReader() (N.PacketReader, []N.CountFunc) {
	return r.cached, []N.CountFunc{func(n int64) { r.counter.Add(n) }}
}

// TestCachedPacketKeepsTheSniffedDestination pins that the cached packet is delivered
// to the destination the sniffer saw, and that the packets after it carry the conn's
// own destination. A cache that re-read the conn for the first packet would also
// relabel it.
func TestCachedPacketKeepsTheSniffedDestination(t *testing.T) {
	first := []byte("sniffed first packet")
	second := []byte("second packet")
	conn := &destinationProbeConn{
		secondDestination: testDestination,
		payloads:          [][]byte{second},
	}
	cache := buf.NewSize(len(first))
	_, _ = cache.Write(first)
	cached := singbufio.NewCachedPacketConn(conn, cache, matrixDestination)

	sink := &packetSink{}
	_, err := singbufio.CopyPacket(sink, cached)
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, [][]byte{first, second}, sink.payloads)
	require.Equal(t, []M.Socksaddr{matrixDestination, testDestination}, sink.destinations,
		"the cached packet must keep the sniffer's destination, the next one the conn's")
	require.Equal(t, int64(1), conn.reads.Load(), "the cached packet must not be read again")
}

// TestCachedPacketFlowsThroughBatchWriters pins the two batch capabilities: a
// destination that can take a batch still receives the cached first packet exactly
// once, with its destination, before the packets read from the conn.
func TestCachedPacketFlowsThroughBatchWriters(t *testing.T) {
	first := []byte("cached for batch")
	second := []byte("read for batch")
	cases := []struct {
		name    string
		newSink func(*packetSink) N.PacketWriter
	}{
		{name: "packet batch writer", newSink: func(base *packetSink) N.PacketWriter { return batchSink{base} }},
		{name: "connected packet batch writer", newSink: func(base *packetSink) N.PacketWriter { return connectedBatchSink{base} }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			conn := newProbePacketConn(second)
			cached := cachedFirstPacket(first, conn)
			base := &packetSink{}
			sink := testCase.newSink(base)
			_, err := singbufio.CopyPacket(sink, cached)
			require.ErrorIs(t, err, io.EOF)
			require.Equal(t, [][]byte{first, second}, base.payloads,
				"the cache must hand the first packet to the batch path too")
			require.Len(t, base.destinations, 2)
			require.Equal(t, int64(1), conn.reads.Load(),
				"a batch destination must not make the cache read the first packet again")
		})
	}
}

// TestCachedPacketCounterOwnership pins where a byte counter has to sit. The cache
// replaces a read, so a counter BELOW it (between the cache and the conn) never sees
// the cached bytes; a counter ABOVE it sees them because the copy engine counts the
// cached packet it takes. Production keeps the inbound counters below the cache at the
// point the packet was first read, which is why this is a rule rather than a defect -
// but a new counter added in the wrong place would under-count exactly one packet per
// flow and look healthy, so the rule is pinned.
func TestCachedPacketCounterOwnership(t *testing.T) {
	first := []byte("cached packet bytes")
	second := []byte("second packet bytes")

	// Below the cache: the cached packet never reaches this counter.
	var belowCount atomic.Int64
	cacheBelow := buf.NewSize(len(first))
	_, _ = cacheBelow.Write(first)
	cachedBelow := singbufio.NewCachedPacketConn(
		&countingPacketConnAdapter{probe: newProbePacketConn(second), counter: &belowCount},
		cacheBelow, testDestination)
	sinkBelow := &packetSink{}
	_, err := singbufio.CopyPacket(sinkBelow, cachedBelow)
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, [][]byte{first, second}, sinkBelow.payloads)
	require.Equal(t, int64(len(second)), belowCount.Load(),
		"a counter below the cache cannot see the cached packet's bytes")

	// Above the cache: the engine counts the cached packet it takes. The adapter below
	// keeps its own counter so the two placements cannot be confused.
	var adapterCount, aboveCount atomic.Int64
	cacheAbove := buf.NewSize(len(first))
	_, _ = cacheAbove.Write(first)
	cachedAbove := singbufio.NewCachedPacketConn(
		&countingPacketConnAdapter{probe: newProbePacketConn(second), counter: &adapterCount},
		cacheAbove, testDestination)
	countingAbove := &countingReaderAbove{cached: cachedAbove, counter: &aboveCount}
	sinkAbove := &packetSink{}
	_, err = singbufio.CopyPacket(sinkAbove, countingAbove)
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, [][]byte{first, second}, sinkAbove.payloads)
	require.Equal(t, int64(len(second)), adapterCount.Load())
	require.Equal(t, int64(len(first)+len(second)), aboveCount.Load(),
		"a counter above the cache counts the cached packet as well")
}

// countingPacketConnAdapter is a probe conn that also exposes the counter face, so a
// counter can be placed immediately below the cache.
type countingPacketConnAdapter struct {
	probe   *probePacketConn
	counter *atomic.Int64
}

func (c *countingPacketConnAdapter) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	return c.probe.ReadPacket(buffer)
}

func (c *countingPacketConnAdapter) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	return c.probe.WritePacket(buffer, destination)
}

func (c *countingPacketConnAdapter) Close() error                     { return c.probe.Close() }
func (c *countingPacketConnAdapter) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (c *countingPacketConnAdapter) SetDeadline(time.Time) error      { return nil }
func (c *countingPacketConnAdapter) SetReadDeadline(time.Time) error  { return nil }
func (c *countingPacketConnAdapter) SetWriteDeadline(time.Time) error { return nil }

func (c *countingPacketConnAdapter) UnwrapPacketReader() (N.PacketReader, []N.CountFunc) {
	return c.probe, []N.CountFunc{func(n int64) { c.counter.Add(n) }}
}
