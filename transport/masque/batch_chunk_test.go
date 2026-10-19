//go:build with_quic

package masque

import (
	"testing"

	transportHTTP "github.com/sagernet/sing-box/transport/http"
	E "github.com/sagernet/sing/common/exceptions"

	"github.com/sagernet/sing/common/buf"
	"github.com/stretchr/testify/require"
)

// queueLimit mirrors session.maxOwnedDatagramBatch (quic-go's owned DATAGRAM queue capacity).
// It is a local constant so the batch-size tests stay compilable against an older session.
const queueLimit = 32

// chunkLimitingBatchSender models the transport's queue capacity: a group larger than the queue is
// refused outright (quic-go's AddBatch does exactly that), and a configured number of successful
// chunks can be followed by a refusal so the partial-transfer fallback is reachable.
type chunkLimitingBatchSender struct {
	inner       transportHTTP.BatchOwnedDatagramSender
	limit       int
	refuseAfter int // -1 means never refuse
	calls       int
}

func (s *chunkLimitingBatchSender) SendDatagramsOwned(buffers []*buf.Buffer) error {
	if len(buffers) > s.limit {
		return E.New("batch of ", len(buffers), " exceeds the queue capacity of ", s.limit)
	}
	if s.refuseAfter >= 0 && s.calls >= s.refuseAfter {
		return E.New("batch refused")
	}
	s.calls++
	return s.inner.SendDatagramsOwned(buffers)
}

func newChunkLimitedSession(t *testing.T, sink *copyCountingStream, limit int, refuseAfter int) *clientSession {
	t.Helper()
	current := batchTestSession(t, sink)
	current.session.batchOwnedDatagrams = &chunkLimitingBatchSender{
		inner:       current.session.batchOwnedDatagrams,
		limit:       limit,
		refuseAfter: refuseAfter,
	}
	return current
}

// A burst larger than the queue must still use the batch path for every chunk that fits.
//
// Before chunking, a 64-packet burst was refused whole: one batch call, then 64 per-packet sends.
// The test asserts the transport saw two full chunks and nothing went through the per-packet path.
func TestWriteBatchChunksABurstAboveTheQueueCapacity(t *testing.T) {
	sink := &copyCountingStream{recordPayloads: true}
	current := newChunkLimitedSession(t, sink, queueLimit, -1)

	const packets = 64
	buffers := newBenchPacketBuffers(packets, batchTestPacket(1200))
	require.NoError(t, current.session.writePackets(buffers))

	calls, payloads := sink.batchStats()
	require.Equal(t, 2, calls, "64 packets must be submitted as two chunks of 32")
	require.Equal(t, packets, payloads)
	_, owned, legacy := sink.stats()
	require.Zero(t, owned, "no packet may also take the per-packet owned path")
	require.Zero(t, legacy)

	recorded := sink.drainPayloads()
	require.Len(t, recorded, packets)
	for _, payload := range recorded {
		require.Equal(t, byte(0x45), payload[1],
			"each transferred packet must carry exactly one context ID before the IPv4 header")
	}
}

// When a later chunk is refused, only that chunk and the ones after it may fall back.
//
// Replaying the accepted prefix would send those packets twice; releasing them again would corrupt
// the pool. Both are invisible at the call site, so the test counts the paths and checks the
// layout of every payload that reached the transport.
func TestWriteBatchFallsBackOnlyForTheRefusedTail(t *testing.T) {
	sink := &copyCountingStream{recordPayloads: true}
	current := newChunkLimitedSession(t, sink, queueLimit, 1)

	const packets = 64
	buffers := newBenchPacketBuffers(packets, batchTestPacket(1200))
	require.NoError(t, current.session.writePackets(buffers))

	calls, batched := sink.batchStats()
	require.Equal(t, 1, calls)
	require.Equal(t, queueLimit, batched)

	_, owned, legacy := sink.stats()
	require.Equal(t, packets-queueLimit, owned,
		"only the refused chunk and its tail may take the per-packet path")
	require.Zero(t, legacy)

	recorded := sink.drainPayloads()
	require.Len(t, recorded, packets)
	for _, payload := range recorded {
		require.Equal(t, byte(0x45), payload[1],
			"the fallback must not prepend a second context ID to the refused chunk")
	}
}

// Every batch size around the queue limit must be split into the minimum number of chunks, and the
// single-packet case must keep bypassing the batch path entirely.
func TestWriteBatchChunkBoundarySizes(t *testing.T) {
	for _, packets := range []int{2, 31, 32, 33, 64, 65, 128} {
		packets := packets
		t.Run("", func(t *testing.T) {
			sink := &copyCountingStream{}
			current := newChunkLimitedSession(t, sink, queueLimit, -1)
			require.NoError(t, current.session.writePackets(newBenchPacketBuffers(packets, batchTestPacket(1200))))
			calls, payloads := sink.batchStats()
			require.Equal(t, (packets+queueLimit-1)/queueLimit, calls,
				"%d packets must take the minimum number of chunks", packets)
			require.Equal(t, packets, payloads)
			if _, owned, _ := sink.stats(); owned != 0 {
				t.Fatalf("%d packets must not fall back to the per-packet path", packets)
			}
		})
	}
}

// A refusal before the first chunk transfers anything must leave every buffer with the caller, so
// the whole group falls back and each packet carries exactly one context ID.
func TestWriteBatchRefusalOfTheFirstChunkFallsBackForAll(t *testing.T) {
	sink := &copyCountingStream{recordPayloads: true}
	current := newChunkLimitedSession(t, sink, queueLimit, 0)

	const packets = 40
	require.NoError(t, current.session.writePackets(newBenchPacketBuffers(packets, batchTestPacket(1200))))

	if calls, batched := sink.batchStats(); calls != 0 || batched != 0 {
		t.Fatalf("a refused first chunk must not be counted as accepted: calls=%d payloads=%d", calls, batched)
	}
	if _, owned, _ := sink.stats(); owned != packets {
		t.Fatalf("every packet must fall back, got %d", owned)
	}
	for _, payload := range sink.drainPayloads() {
		require.Equal(t, byte(0x45), payload[1], "the refused chunk must be restored before the fallback")
	}
}
