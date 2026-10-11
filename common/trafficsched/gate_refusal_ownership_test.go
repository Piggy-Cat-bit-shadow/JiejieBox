package trafficsched

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/trafficclass"
	"github.com/sagernet/sing/common/atomic"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// gateOwnershipSink counts how the traffic arrived. It must NOT be reached on any path where admission
// failed - the whole point is that the gate keeps the buffers rather than passing them on.
type gateOwnershipSink struct {
	packets          atomic.Int64
	batches          atomic.Int64
	connectedBatches atomic.Int64
}

func (s *gateOwnershipSink) WritePacket(buffer *buf.Buffer, _ M.Socksaddr) error {
	s.packets.Add(1)
	buffer.Release()
	return nil
}

func (s *gateOwnershipSink) WritePacketBatch(buffers []*buf.Buffer, _ []M.Socksaddr) error {
	s.batches.Add(1)
	buf.ReleaseMulti(buffers)
	return nil
}

func (s *gateOwnershipSink) WriteConnectedPacketBatch(buffers []*buf.Buffer) error {
	s.connectedBatches.Add(1)
	buf.ReleaseMulti(buffers)
	return nil
}

func (s *gateOwnershipSink) CreatePacketBatchWriter() (N.PacketBatchWriter, bool) { return s, true }

func (s *gateOwnershipSink) CreateConnectedPacketBatchWriter() (N.ConnectedPacketBatchWriter, bool) {
	return s, true
}

// The gate's packet side has three write entry points and EVERY one releases the buffers itself when
// admission fails:
//
//	gatePacketWriter.WritePacket           -> buffer.Release()
//	gatePacketBatchWriter.WritePacketBatch -> buf.ReleaseMulti(buffers)
//	gateConnectedPacketBatchWriter.…       -> buf.ReleaseMulti(buffers)
//
// In that case the buffers never reached the upstream, so ownership never left the gate. Releasing nothing
// would drop every datagram of every rejected batch; releasing twice is worse than it sounds, because
// buf.Buffer.Release zeroes the struct and returns the backing array to the pool, so a second release is a
// silent no-op while the array already belongs to whoever allocates next.
//
// The existing coverage never reaches these branches: the packet-batch tests use a nil flow or a healthy
// one, and the only admission-failure test on this side (TestGateReleasesBuffersItNeverDelegated) fails via
// a flow closed WHILE BLOCKED, for the batch entry point only. So the release sites are asserted by reading
// the code rather than by running it.
//
// The refusal is provoked the same way that test provokes it, and for the same reason: `admit` is
// `flow.wait`, and `wait` returns ErrClosed only for a flow closed while it is waiting. A flow that is
// merely closed admits immediately and the write succeeds, which is why an earlier revision of this test
// failed with "an error is expected but got nil".
func TestPacketGateReleasesEveryBufferWhenAdmissionFails(t *testing.T) {
	// refusingBlockedFlow returns a flow that is parked behind a holder, plus a release function that
	// closes the target and returns the error the blocked write produced.
	refusingBlockedFlow := func(t *testing.T) (*Flow, func() error) {
		t.Helper()
		scheduler := NewScheduler(Options{Mode: ModeService, HighIdleWindow: time.Minute})
		holder := scheduler.NewFlow(trafficclass.ClassInteractive)
		require.NoError(t, holder.wait(1), "the holder takes the single service slot")
		target := scheduler.NewFlow(trafficclass.ClassInteractive)
		return target, func() error {
			require.NoError(t, target.Close())
			holder.done()
			return nil
		}
	}

	t.Run("single packet", func(t *testing.T) {
		sink := &gateOwnershipSink{}
		flow, release := refusingBlockedFlow(t)
		gate := NewPacketGate(sink, flow)

		packet := newProbeBuffer("payload")
		require.Equal(t, len("payload"), packet.Len())

		result := make(chan error, 1)
		go func() { result <- gate.WritePacket(packet, M.ParseSocksaddrHostPort("example.com", 443)) }()
		time.Sleep(25 * time.Millisecond)
		require.NoError(t, release())

		err := awaitResult(t, result, "the refused packet")
		require.Error(t, err, "a refused packet must report the refusal rather than pretend to send")
		require.Zero(t, packet.Len(),
			"the packet never reached the upstream, so the gate must release it; a buffer that keeps "+
				"its length was silently dropped and its backing array leaked")
		require.Zero(t, sink.packets.Load(), "and the upstream must not have been reached")
	})

	t.Run("unconnected batch", func(t *testing.T) {
		sink := &gateOwnershipSink{}
		flow, release := refusingBlockedFlow(t)
		gate := NewPacketGate(sink, flow)

		batchWriter, created := bufio.CreatePacketBatchWriter(gate)
		require.True(t, created)

		buffers := probeBuffers([]string{"aaa", "bb", "c"})
		result := make(chan error, 1)
		go func() {
			result <- batchWriter.WritePacketBatch(buffers, []M.Socksaddr{{}, {}, {}})
		}()
		time.Sleep(25 * time.Millisecond)
		require.NoError(t, release())

		require.Error(t, awaitResult(t, result, "the refused batch"))
		for index, buffer := range buffers {
			require.Zero(t, buffer.Len(),
				"every buffer of a refused batch must be released: index %d", index)
		}
		require.Zero(t, sink.batches.Load(), "the upstream must not have been reached")
	})

	t.Run("connected batch", func(t *testing.T) {
		sink := &gateOwnershipSink{}
		flow, release := refusingBlockedFlow(t)
		gate := NewPacketGate(sink, flow)

		batchWriter, created := bufio.CreateConnectedPacketBatchWriter(gate)
		require.True(t, created)

		buffers := probeBuffers([]string{"aaaa", "bb"})
		result := make(chan error, 1)
		go func() { result <- batchWriter.WriteConnectedPacketBatch(buffers) }()
		time.Sleep(25 * time.Millisecond)
		require.NoError(t, release())

		require.Error(t, awaitResult(t, result, "the refused connected batch"))
		for index, buffer := range buffers {
			require.Zero(t, buffer.Len(),
				"every buffer of a refused connected batch must be released: index %d", index)
		}
		require.Zero(t, sink.connectedBatches.Load(), "the upstream must not have been reached")
	})

	// What a refusal does to the flow's own counters is deliberately NOT asserted here. Both
	// `admitted` and `grants` move for a write that was then refused, because `Flow.wait` records before
	// it decides; whether that is intended is a separate question with its own test and its own note
	// (TestRefusedAdmissionStillChargesTheFlowCounter). Keeping it out of this file means the ownership
	// contract below can be read on its own.
}

// TestRefusedAdmissionStillChargesTheFlowCounter pins an inconsistency between what Flow.admitted is
// DOCUMENTED to mean and what it does, so it cannot drift further while the question is open.
//
// The documentation on the field is unambiguous:
//
//	// admitted counts the bytes this flow has handed to the outbound through the gate. It is
//	// per-flow, so the atomic is uncontended; it exists because "did the bytes really pass through
//	// the scheduler" is otherwise unobservable ...
//
// but `Flow.wait` increments it BEFORE the scheduler decides:
//
//	f.admitted.Add(int64(n))
//	if s.inert() { return nil }
//	if !s.options.Mode.paced() && !f.high && !s.armed.Load() { return nil }
//	return s.waitSlow(f, n)          // <- may return an error
//
// So bytes that were never handed to the outbound are counted, and the gap is largest exactly where it
// matters most: a flow closed while blocked, a paced flow whose wait is cancelled, or a bucket that
// refuses. `AdmittedBytes` is then an upper bound on delivered bytes rather than the count it claims.
//
// The same is true of grants, which counts the writes the scheduler arbitrated: a refused write still
// produces one, because the increment happens on the arbitrating path before the refusal is known. Both
// counters therefore describe ATTEMPTED work at the refusal boundary, while their documentation describes
// DELIVERED work.
//
// This is recorded rather than changed. Moving the increments after the decision alters the byte
// accounting the pacing precision tests are built on - `measureAdmittedRate` divides admitted bytes by
// elapsed time - and those are timing tests that cannot be validated on this host (`-race` unavailable,
// and the repo's own raceEnabled gate skips them under instrumentation for exactly this reason). Changing
// a measured contract on the strength of a source reading, with no way to run the measurement, is the
// mistake this session has had to retract more than once.
//
// The test therefore asserts the CURRENT behaviour, so that a future change to it is a deliberate act
// with this note attached rather than a silent drift.
func TestRefusedAdmissionStillChargesTheFlowCounter(t *testing.T) {
	scheduler := NewScheduler(Options{Mode: ModeService, HighIdleWindow: time.Minute})
	holder := scheduler.NewFlow(trafficclass.ClassInteractive)
	require.NoError(t, holder.wait(1), "the holder takes the single service slot")

	target := scheduler.NewFlow(trafficclass.ClassInteractive)
	require.Zero(t, target.AdmittedBytes())

	result := make(chan error, 1)
	go func() { result <- target.wait(4096) }()
	time.Sleep(25 * time.Millisecond)
	require.NoError(t, target.Close())
	holder.done()
	require.Error(t, awaitResult(t, result, "the refused admission"))

	require.EqualValues(t, 4096, target.AdmittedBytes(),
		"the counter currently moves even though the write was refused, which is at odds with the "+
			"field's own documentation. If this assertion starts failing, the increment has been moved "+
			"after the decision - check the pacing precision tests before celebrating")
}

var (
	_ N.PacketWriter                     = (*gateOwnershipSink)(nil)
	_ N.PacketBatchWriter                = (*gateOwnershipSink)(nil)
	_ N.ConnectedPacketBatchWriter       = (*gateOwnershipSink)(nil)
	_ N.PacketBatchWriteCreator          = (*gateOwnershipSink)(nil)
	_ N.ConnectedPacketBatchWriteCreator = (*gateOwnershipSink)(nil)
)
