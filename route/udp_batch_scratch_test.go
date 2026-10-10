package route

import (
	"testing"

	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/sagernet/sing-box/common/trafficsched"

	"github.com/stretchr/testify/require"
)

// consumingBatchWriter takes ownership of the buffers the moment it is entered, exactly as every real
// batch writer in sing does, and clears them - which is what makes the ordering assertion below detectable
// rather than theoretical.
type consumingBatchWriter struct {
	batches int
	lengths []int
}

func (w *consumingBatchWriter) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	buffer.Release()
	return nil
}

func (w *consumingBatchWriter) WritePacketBatch(buffers []*buf.Buffer, _ []M.Socksaddr) error {
	w.batches++
	for _, buffer := range buffers {
		// Record what the buffer claims to hold AT THIS POINT, then consume it. A counter that reads
		// lengths after this call sees zeros; one that reads them before sees the payload sizes.
		w.lengths = append(w.lengths, buffer.Len())
		buffer.Release()
	}
	return nil
}

// TestCounterWrapperCountsLengthsBeforeTheWriterConsumesThem pins the ordering the counter depends on.
//
// `counterPreservingPacketWriter.WritePacketBatch` deliberately reads every buffer's length into scratch
// BEFORE handing the batch to the destination, because a batch writer takes ownership of the buffers and
// may release them - after which `Len()` is zero and the traffic is understated. That is a silent
// accounting loss, not a crash: the datagrams arrive, the counters are simply too low, and no functional
// test would notice.
//
// The fixture clears each buffer the way a real writer does, so the ordering is what is being measured
// rather than asserted against a writer that happens to be gentle.
func TestCounterWrapperCountsLengthsBeforeTheWriterConsumesThem(t *testing.T) {
	inner := &consumingBatchWriter{}
	var counted int64
	flow := testFlow(t)
	wrapper := &counterPreservingPacketWriter{
		gate:     trafficsched.NewPacketGate(inner, flow),
		counters: []N.CountFunc{func(n int64) { counted += n }},
	}

	payloads := []string{"aaaa", "bbbbbbbb", "cc"}
	buffers := make([]*buf.Buffer, 0, len(payloads))
	destinations := make([]M.Socksaddr, 0, len(payloads))
	for _, payload := range payloads {
		buffer := buf.NewSize(len(payload))
		_, err := buffer.WriteString(payload)
		require.NoError(t, err)
		buffers = append(buffers, buffer)
		destinations = append(destinations, M.ParseSocksaddrHostPort("example.com", 443))
	}

	require.NoError(t, wrapper.WritePacketBatch(buffers, destinations))

	require.Equal(t, 1, inner.batches,
		"the batch must reach the destination as ONE batch, not be decomposed")

	// This is the assertion that fails if the lengths are read after the write: every buffer would have
	// been released, `Len()` would be 0, and the fixture would report zeros.
	var wantTotal int64
	for _, payload := range payloads {
		wantTotal += int64(len(payload))
	}
	require.EqualValues(t, wantTotal, counted,
		"the counters must be charged the payload sizes as they were BEFORE the writer took ownership; "+
			"reading them afterwards understates the traffic to zero and nothing functional would notice")

	// And the destination must have seen the real sizes at entry, which is the same fact from the other
	// side of the call.
	require.Equal(t, []int{4, 8, 2}, inner.lengths,
		"the destination must observe the payload sizes at entry")
}

// TestPacketBatchScratchKeepsTheCommonCaseOffTheHeap is the boundary test for the stack scratch.
//
// `packetBatchLengths` is a stack array sized by bufio.DefaultPacketReadBatchSize, with a heap fallback for
// an oversized batch, mirroring what the copy engine's own batch scratch does. Neither half was covered.
//
// The sizing is the reason this test exists: the ceiling is a CONSTANT copied from another package, so if
// that constant is ever raised past this array, the sizing mismatch is silent - the scratch keeps working,
// just on the heap, and the UDP fast path pays an allocation per batch with nothing failing. The assertion
// below is therefore on the relationship, not on the number.
func TestPacketBatchScratchKeepsTheCommonCaseOffTheHeap(t *testing.T) {
	var scratch packetBatchLengths

	require.Equal(t, bufio.DefaultPacketReadBatchSize, len(scratch.stack),
		"the scratch is sized by the copy engine's default batch size; if that constant moves, this array "+
			"must move with it or the common case silently leaves the stack")

	// At the ceiling the scratch must be used, and the returned slice must alias the array rather than
	// being a fresh allocation. `common.ptrOffset`-style identity is not available here, so the aliasing
	// is proved by writing through the slice and reading the array.
	atCeiling := scratch.int64s(bufio.DefaultPacketReadBatchSize)
	require.Len(t, atCeiling, bufio.DefaultPacketReadBatchSize)
	atCeiling[0] = 12345
	require.EqualValues(t, 12345, scratch.stack[0],
		"a batch at the ceiling must be served by the stack array, or the scratch is a heap allocation "+
			"wearing a stack array's name")

	// One past the ceiling must NOT alias the array: overrunning it would corrupt an unrelated batch.
	overCeiling := scratch.int64s(bufio.DefaultPacketReadBatchSize + 1)
	require.Len(t, overCeiling, bufio.DefaultPacketReadBatchSize+1)
	overCeiling[0] = 999
	require.EqualValues(t, 12345, scratch.stack[0],
		"an oversized batch must fall back to the heap rather than write past the stack array; "+
			"aliasing here would corrupt the scratch of a concurrent batch")

	// A zero batch is legal and must not panic. It is the shape a read waiter returns for an empty batch.
	require.Empty(t, scratch.int64s(0))
}

var (
	_ N.PacketWriter      = (*consumingBatchWriter)(nil)
	_ N.PacketBatchWriter = (*consumingBatchWriter)(nil)
	_                     = common.Ptr[int64]
)
