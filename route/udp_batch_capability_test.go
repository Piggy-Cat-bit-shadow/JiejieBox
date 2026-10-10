package route

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/trafficclass"
	"github.com/sagernet/sing-box/common/trafficsched"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// The production gate is the OUTERMOST writer on the packet path, and it decides what survives.
//
// Measured, and this is why the assertion is `>=` rather than `==`: the gate does NOT forward the
// upstream's headroom verbatim, it ADDS its own requirement to it.
//
//	inner     headroom=128   mtu=1400
//	gate      headroom=256   mtu=1400
//
// That is correct behaviour - a gate between the caller and the destination needs room of its own, so
// it must advertise at least as much - and the first version of this test asserted `== 128`, which was
// wrong and failed against a gate that was doing the right thing. What matters is the invariant:
// whatever the gate advertises must be at least what the upstream needs, and the MTU must be preserved
// exactly (a gate may not widen the MTU).
func TestProductionPacketGateForwardsGeometry(t *testing.T) {
	inner := &geometryPacketWriter{}
	flow := testFlow(t)
	gate := trafficsched.NewPacketGate(inner, flow)

	require.GreaterOrEqual(t, N.CalculateFrontHeadroom(gate), N.CalculateFrontHeadroom(inner),
		"a gate in front of a geometry-declaring writer must not advertise less headroom than it needs")
	require.EqualValues(t, 1400, N.CalculateMTU(context.Background(), gate),
		"a gate in front of a writer that declares an MTU must not hide or widen it")
}

// This is the bug. `uploadPacketGate` (route/conn.go) wraps the destination in
// `counterPreservingPacketWriter` whenever the destination carries write counters, so that the
// counters stay visible across the non-replaceable gate. The wrapper answers only
// `WritePacket` + `UnwrapPacketWriter`.
//
// route enters the copy engine through `bufio.CopyPacketWithCounters` (route/conn.go), and that
// entry point - unlike `bufio.CopyPacket` and `bufio.CopyWithIncreateBuffer` - does NOT unwrap the
// destination. So the engine probes the wrapper itself, `N.CreatePacketBatchWriter` stops there,
// and a batch-capable destination is written one datagram per syscall.
//
// Measured before the fix, control vs treatment:
//
//	control (no counter wrapper)   batches=32  packets=0
//	with counter wrapper           batches=0   packets=512
//
// It is functionally correct - every datagram arrives - which is exactly why the existing suite is
// green. It is a silent performance regression on the hottest UDP path in the process.
func TestCounterWrapperMustNotEatPacketBatchWriter(t *testing.T) {
	inner := &batchRecordingPacketWriter{}
	counters := []N.CountFunc{func(n int64) {}}
	// The production shape: the counter wrapper carries the gate, and the gate carries the real
	// destination.
	flow := testFlow(t)
	wrapper := &counterPreservingPacketWriter{
		gate:     trafficsched.NewPacketGate(inner, flow),
		counters: counters,
	}

	batchWriter, created := bufio.CreatePacketBatchWriter(wrapper)
	require.True(t, created,
		"a counter-preserving wrapper must forward BatchPacketWriter. Losing it degrades every UDP "+
			"session of a counter-carrying destination from one batched syscall to one syscall per "+
			"datagram; the data still arrives, so no functional test notices")
	require.NotNil(t, batchWriter)

	// And the behaviour, not just the interface: one batch in, one batch out.
	buffers := []*buf.Buffer{buf.NewPacket(), buf.NewPacket()}
	for _, buffer := range buffers {
		buffer.WriteString("payload")
	}
	destinations := []M.Socksaddr{M.ParseSocksaddrHostPort("1.1.1.1", 443), M.ParseSocksaddrHostPort("1.1.1.1", 443)}
	require.NoError(t, batchWriter.WritePacketBatch(buffers, destinations))

	require.EqualValues(t, 1, inner.batches,
		"the batch was split into one write per datagram, so the batch capability was lost at the wrapper")
	require.EqualValues(t, 0, inner.packets)
}

// The wrapper also exists to keep the destination's byte counters visible. It must not lose them
// either - the counters are the reason it is there.
func TestCounterWrapperKeepsTheDestinationCounters(t *testing.T) {
	var counted int64
	inner := &batchRecordingPacketWriter{}
	flow := testFlow(t)
	wrapper := &counterPreservingPacketWriter{
		gate:     trafficsched.NewPacketGate(inner, flow),
		counters: []N.CountFunc{func(n int64) { counted += n }},
	}

	// The engine unwraps to learn the counters; that is how they are meant to be applied.
	_, counters := N.UnwrapCountPacketWriter(wrapper, nil)
	require.Len(t, counters, 1, "the wrapper must still expose the destination's write counters")
}

// geometryPacketWriter declares whole-chain geometry and nothing else.
type geometryPacketWriter struct{}

func (w *geometryPacketWriter) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	buffer.Release()
	return nil
}

func (w *geometryPacketWriter) FrontHeadroom() int { return 128 }

func (w *geometryPacketWriter) WriterMTU() int { return 1400 }

// batchRecordingPacketWriter accepts batches and counts how the data actually arrived.
type batchRecordingPacketWriter struct {
	batches int
	packets int
}

func (w *batchRecordingPacketWriter) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	w.packets++
	buffer.Release()
	return nil
}

func (w *batchRecordingPacketWriter) WritePacketBatch(buffers []*buf.Buffer, destinations []M.Socksaddr) error {
	w.batches++
	for _, buffer := range buffers {
		buffer.Release()
	}
	return nil
}

func (w *batchRecordingPacketWriter) FrontHeadroom() int { return 128 }

func (w *batchRecordingPacketWriter) WriterMTU() int { return 1400 }

var (
	_ N.PacketWriter      = (*geometryPacketWriter)(nil)
	_ N.PacketBatchWriter = (*batchRecordingPacketWriter)(nil)
	_                     = bufio.DefaultBatchSize
	_                     = time.Second
)

// testFlow builds a scheduler-backed flow, which is how production obtains one.
func testFlow(t *testing.T) *trafficsched.Flow {
	t.Helper()
	scheduler := trafficsched.NewScheduler(trafficsched.Options{})
	t.Cleanup(func() { _ = scheduler.Close() })
	return scheduler.NewFlow(trafficclass.ClassInteractive)
}
