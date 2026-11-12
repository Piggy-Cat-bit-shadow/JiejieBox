package trusttunnel

// Q36: the UDP segments conserve.
//
// The Q35 matrix attributes "no reply" to a missing ingress, a missing reply, a
// queue drop, or a reclaimed association. That attribution is only sound if the
// segments add up: every datagram that reached the tunnel must be accounted for
// exactly once, at rest, as delivered, dropped, refused or discarded. Before
// these counters the equation did not close - datagrams drained by a closing
// association and replies lost while writing them were attributed to no segment
// at all, so "delivered + dropped + refused" was silently less than "received"
// and a tunnel-side loss could be read as a target that never answered.
//
// This file pins the equation on the real service, with the queue saturated and
// the stream closed underneath it.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// requireConserved reports the ingress segments that do not add up. It is the
// single assertion every cell of this file ends with: at rest, queued bytes are
// zero and received equals delivered + dropped + refused + discarded.
func requireConserved(t *testing.T, stats UDPStats, context string) {
	t.Helper()
	require.Zero(t, stats.QueuedBytes, "%s: the accounting was not taken at rest", context)
	accounted := stats.DatagramsDelivered + stats.DroppedDatagrams +
		stats.RefusedAssociations + stats.DatagramsDiscarded
	require.Equal(t, stats.DatagramsReceived, accounted,
		"%s: ingress does not conserve (received=%d delivered=%d dropped=%d refused=%d discarded=%d); stats %+v",
		context, stats.DatagramsReceived, stats.DatagramsDelivered, stats.DroppedDatagrams,
		stats.RefusedAssociations, stats.DatagramsDiscarded, stats)
}

// TestUDPSegmentsConserveThroughASaturatedQueueAndClose fills the per-flow item
// queue (64 datagrams) with a handler that never reads, drops the overflow, and
// then closes the stream while the queue is still full. Every datagram is either
// still queued, dropped by the full queue, or discarded by the close; none may be
// unattributed.
func TestUDPSegmentsConserveThroughASaturatedQueueAndClose(t *testing.T) {
	handler := &holdingHandler{release: make(chan struct{})}
	// The handler never reads in this cell, so let its goroutine exit with the
	// test instead of waiting for a release that never comes.
	t.Cleanup(func() {
		select {
		case <-handler.release:
		default:
			close(handler.release)
		}
	})
	// The item queue (udpFlowQueueSize) is the bound under test; the byte budget
	// is large enough not to interfere.
	server := startTestService(t, ProtocolHTTP2, handler, UDPLimits{
		IdleTimeout:        time.Minute,
		SessionQueuedBytes: 1 << 20,
		ServiceQueuedBytes: 1 << 20,
	})
	client := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)
	packetConn, err := client.ListenPacket(testContext(t))
	require.NoError(t, err)

	const total = udpFlowQueueSize + 36
	destination := udpDestinations(1)[0]
	for index := 0; index < total; index++ {
		writeDatagram(t, packetConn, []byte("q"), destination)
	}
	stats := waitForUDPStats(t, server.service, "every frame to reach the tunnel", func(stats UDPStats) bool {
		return stats.DatagramsReceived == total
	})
	// The queue is full and the overflow was dropped at admission; the handler
	// has read nothing.
	require.EqualValues(t, 36, stats.DroppedDatagrams, "the full item queue must account for the overflow: %+v", stats)
	require.Zero(t, stats.DatagramsDelivered)
	require.Zero(t, stats.DatagramsDiscarded)
	require.EqualValues(t, udpFlowQueueSize, stats.QueuedBytes, "a one-byte payload per queued datagram")

	// The stream closes with the queue still full: every queued datagram is
	// discarded and its bytes return to the budget.
	started := time.Now()
	require.NoError(t, packetConn.Close())
	stats = waitForUDPStats(t, server.service, "the close to reclaim the saturated association", func(stats UDPStats) bool {
		return stats.Associations == 0 && stats.QueuedBytes == 0
	})
	require.Less(t, time.Since(started), 15*time.Second, "a full queue must not stall the close")
	require.EqualValues(t, total, stats.DatagramsReceived)
	require.EqualValues(t, 36, stats.DroppedDatagrams)
	require.EqualValues(t, 0, stats.DatagramsDelivered)
	require.EqualValues(t, udpFlowQueueSize, stats.DatagramsDiscarded)
	require.Zero(t, stats.DatagramsSent)
	require.Zero(t, stats.RepliesFailed)
	requireConserved(t, stats, "saturated queue closed under load")
}

// TestUDPSegmentsConserveWhenTheByteBudgetDrops drives the other admission
// refusal: the queue has room for items but not for bytes. Two datagrams fit,
// three are dropped, and the two that were queued are then delivered.
func TestUDPSegmentsConserveWhenTheByteBudgetDrops(t *testing.T) {
	handler := &holdingHandler{release: make(chan struct{})}
	server := startTestService(t, ProtocolHTTP2, handler, UDPLimits{
		IdleTimeout:        time.Minute,
		SessionQueuedBytes: 25,
		ServiceQueuedBytes: 1 << 20,
	})
	client := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)
	packetConn, err := client.ListenPacket(testContext(t))
	require.NoError(t, err)

	destination := udpDestinations(1)[0]
	payload := make([]byte, 10)
	for index := 0; index < 5; index++ {
		writeDatagram(t, packetConn, payload, destination)
	}
	stats := waitForUDPStats(t, server.service, "the byte budget to refuse three", func(stats UDPStats) bool {
		return stats.DatagramsReceived == 5 && stats.DroppedDatagrams == 3
	})
	require.EqualValues(t, 20, stats.QueuedBytes)
	require.Zero(t, stats.DatagramsDelivered)

	// Handing the queue to the handler delivers exactly what was admitted.
	close(handler.release)
	stats = waitForUDPStats(t, server.service, "the admitted datagrams to be delivered", func(stats UDPStats) bool {
		return stats.DatagramsDelivered == 2 && stats.QueuedBytes == 0
	})
	require.EqualValues(t, 3, stats.DroppedDatagrams)
	requireConserved(t, stats, "byte budget with delivery")

	require.NoError(t, packetConn.Close())
	stats = waitForUDPStats(t, server.service, "the association release", func(stats UDPStats) bool {
		return stats.Associations == 0 && stats.QueuedBytes == 0
	})
	// The association closed after its queue was drained, so nothing was
	// discarded: the delivery happened before the close.
	require.Zero(t, stats.DatagramsDiscarded)
	requireConserved(t, stats, "byte budget after close")
}

// lateReplyHandler keeps the association without replying, and then writes one
// reply when the test tells it to. That is the shape of a target that answers
// after the client has gone.
type lateReplyHandler struct {
	access  sync.Mutex
	conns   []N.PacketConn
	release chan struct{}
	written atomic.Int32
	failed  atomic.Int32
}

func (h *lateReplyHandler) NewConnection(ctx context.Context, conn *ServerConn, metadata Metadata) {
	conn.Close()
}

func (h *lateReplyHandler) NewPacketConnection(ctx context.Context, conn N.PacketConn, metadata Metadata) {
	h.access.Lock()
	h.conns = append(h.conns, conn)
	h.access.Unlock()
	go func() {
		<-h.release
		buffer := buf.NewSize(MaxPayloadSize)
		_, _ = buffer.Write([]byte("late"))
		err := conn.WritePacket(buffer, metadata.Destination)
		buffer.Release()
		if err != nil {
			h.failed.Add(1)
		} else {
			h.written.Add(1)
		}
	}()
}

func (h *lateReplyHandler) association() N.PacketConn {
	h.access.Lock()
	defer h.access.Unlock()
	if len(h.conns) == 0 {
		return nil
	}
	return h.conns[0]
}

// TestUDPSegmentsAttributeAReplyThatCannotBeWritten is the reply half of the
// equation: the target answered, the tunnel could not return it, and that is
// counted as a failed reply rather than disappearing.
func TestUDPSegmentsAttributeAReplyThatCannotBeWritten(t *testing.T) {
	handler := &lateReplyHandler{release: make(chan struct{})}
	server := startTestService(t, ProtocolHTTP2, handler, UDPLimits{IdleTimeout: time.Minute})
	client := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)
	packetConn, err := client.ListenPacket(testContext(t))
	require.NoError(t, err)

	writeDatagram(t, packetConn, []byte("question"), udpDestinations(1)[0])
	waitForUDPStats(t, server.service, "the association", func(stats UDPStats) bool {
		return stats.Associations == 1 && stats.DatagramsReceived == 1
	})
	require.NotNil(t, handler.association())

	// The client goes away first, so the reply cannot be framed onto the stream.
	require.NoError(t, packetConn.Close())
	waitForUDPStats(t, server.service, "the association release after the client left", func(stats UDPStats) bool {
		return stats.Associations == 0 && stats.QueuedBytes == 0
	})
	close(handler.release)
	waitFor(t, "the late reply attempt", func() bool {
		return handler.failed.Load()+handler.written.Load() == 1
	})
	stats := server.service.UDPStats()
	require.EqualValues(t, 1, handler.failed.Load(), "a reply onto a closed stream must fail")
	require.EqualValues(t, 0, stats.DatagramsSent, "a failed write must not count as sent")
	require.EqualValues(t, 1, stats.RepliesFailed, "the failed reply must be attributed")
	requireConserved(t, stats, "failed reply")
}

// TestUDPSegmentsConserveOnTheEchoPath is the positive control: every datagram
// is delivered and answered, so delivered == received and sent == received, with
// no drop, refusal, discard or failure.
func TestUDPSegmentsConserveOnTheEchoPath(t *testing.T) {
	handler := &testHandler{}
	server := startTestService(t, ProtocolHTTP2, handler, UDPLimits{IdleTimeout: time.Minute})
	client := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)
	packetConn, err := client.ListenPacket(testContext(t))
	require.NoError(t, err)
	defer packetConn.Close()

	destination := udpDestinations(1)[0]
	const total = 3
	for index := 0; index < total; index++ {
		writeDatagram(t, packetConn, []byte("echo"), destination)
		reply := readDatagram(t, packetConn, 5*time.Second)
		require.NoError(t, reply.err)
		require.Equal(t, "echo", string(reply.payload))
	}
	stats := waitForUDPStats(t, server.service, "the echo attribution", func(stats UDPStats) bool {
		return stats.DatagramsReceived == total && stats.DatagramsSent == total
	})
	require.EqualValues(t, total, stats.DatagramsDelivered)
	require.Zero(t, stats.DroppedDatagrams)
	require.Zero(t, stats.DatagramsDiscarded)
	require.Zero(t, stats.RefusedAssociations)
	require.Zero(t, stats.RepliesFailed)
	requireConserved(t, stats, "echo path")
}

// TestUDPFullQueueIsARefusalNotAStall is the bounded-degradation half of Q36's
// queue policy: when the queue is full the datagrams are refused, the stream and
// its association keep working, and the next datagram after the stall is
// delivered. The queue never applies backpressure to the frame reader, so a slow
// handler cannot stop the tunnel's control flow.
func TestUDPFullQueueIsARefusalNotAStall(t *testing.T) {
	handler := &holdingHandler{release: make(chan struct{})}
	server := startTestService(t, ProtocolHTTP2, handler, UDPLimits{
		IdleTimeout:        time.Minute,
		SessionQueuedBytes: 1 << 20,
		ServiceQueuedBytes: 1 << 20,
	})
	client := server.newClient(t, ProtocolHTTP2, testPassword, &testDialer{}, nil)
	packetConn, err := client.ListenPacket(testContext(t))
	require.NoError(t, err)
	defer packetConn.Close()

	destination := udpDestinations(1)[0]
	for index := 0; index < udpFlowQueueSize+8; index++ {
		writeDatagram(t, packetConn, []byte("x"), destination)
	}
	stats := waitForUDPStats(t, server.service, "the queue to refuse the overflow", func(stats UDPStats) bool {
		return stats.DroppedDatagrams == 8
	})
	require.EqualValues(t, udpFlowQueueSize+8, stats.DatagramsReceived)
	require.EqualValues(t, 1, stats.Associations, "the association must survive a full queue")

	// The reader is still live: releasing the queue delivers the queued
	// datagrams and the association keeps its slot.
	close(handler.release)
	stats = waitForUDPStats(t, server.service, "the saturated queue to drain", func(stats UDPStats) bool {
		return stats.DatagramsDelivered == udpFlowQueueSize && stats.QueuedBytes == 0
	})
	require.EqualValues(t, 8, stats.DroppedDatagrams)
	require.EqualValues(t, 1, stats.Associations)
	requireConserved(t, stats, "full queue drained")

	// And a datagram after the stall is admitted as usual.
	writeDatagram(t, packetConn, []byte("after"), destination)
	stats = waitForUDPStats(t, server.service, "the datagram after the stall", func(stats UDPStats) bool {
		return stats.DatagramsDelivered == udpFlowQueueSize+1 && stats.QueuedBytes == 0
	})
	require.EqualValues(t, udpFlowQueueSize+9, stats.DatagramsReceived)
	requireConserved(t, stats, "after the stall")
}
