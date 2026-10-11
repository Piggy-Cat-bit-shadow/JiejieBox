package trafficsched

import (
	"testing"

	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// gateAccountingSink records what actually arrived, at the moment it arrives, and clears the buffers the
// way a real destination does - so a size read after the hand-off would see zero.
type gateAccountingSink struct {
	bytes int64
}

func (s *gateAccountingSink) WritePacket(buffer *buf.Buffer, _ M.Socksaddr) error {
	s.bytes += int64(buffer.Len())
	buffer.Release()
	return nil
}

func (s *gateAccountingSink) WritePacketBatch(buffers []*buf.Buffer, _ []M.Socksaddr) error {
	for _, buffer := range buffers {
		s.bytes += int64(buffer.Len())
		buffer.Release()
	}
	return nil
}

func (s *gateAccountingSink) WriteConnectedPacketBatch(buffers []*buf.Buffer) error {
	for _, buffer := range buffers {
		s.bytes += int64(buffer.Len())
		buffer.Release()
	}
	return nil
}

func (s *gateAccountingSink) receivedBytes() int64 { return s.bytes }

// gateManagedMTUSink is the same sink behind a chain that constrains the MTU, which makes NewPacketGate
// return the `gatePacketWriterMTU` shape rather than `gatePacketWriter`.
type gateManagedMTUSink struct {
	gateAccountingSink
	mtu int
}

func (s *gateManagedMTUSink) WriterMTU() int { return s.mtu }

// The accounting identity this test exists for:
//
//	bytes the scheduler ADMITTED  ==  bytes the destination RECEIVED  ==  bytes actually sent
//
// It has to hold for every packet path AND every gate shape, because the two are chosen independently -
// the shape comes from the upstream's capabilities, the path from whatever the copy engine happens to use.
// A gate that admits one figure and settles another misprices real traffic, and nothing functional fails:
// every packet still arrives.
//
// Existing coverage checks one path against one shape at a time (one grant for a batch, the sum of packet
// sizes). This walks the cross-product, so a shape that mishandles a path it does not special-case is
// caught rather than assumed.
func TestPacketGateAccountingIsIdenticalAcrossShapesAndPaths(t *testing.T) {
	payloads := []string{"aaaa", "bbbbbbbb", "cc"}
	var want int64
	for _, payload := range payloads {
		want += int64(len(payload))
	}

	dests := []M.Socksaddr{
		M.ParseSocksaddrHostPort("one.example", 1001),
		M.ParseSocksaddrHostPort("two.example", 1002),
		M.ParseSocksaddrHostPort("three.example", 1003),
	}

	plainSink := &gateAccountingSink{}
	mtuSink := &gateManagedMTUSink{mtu: 1400}

	shapes := []struct {
		name     string
		upstream N.PacketWriter
		received func() int64
		wantMTU  bool
	}{
		{"gatePacketWriter", plainSink, plainSink.receivedBytes, false},
		{"gatePacketWriterMTU", mtuSink, mtuSink.receivedBytes, true},
	}

	paths := []struct {
		name string
		run  func(t *testing.T, gate N.PacketWriter)
	}{
		{"single WritePacket per packet", func(t *testing.T, gate N.PacketWriter) {
			for index, payload := range payloads {
				require.NoError(t, gate.WritePacket(newProbeBuffer(payload), dests[index]))
			}
		}},
		{"unconnected WritePacketBatch", func(t *testing.T, gate N.PacketWriter) {
			batchWriter, created := bufio.CreatePacketBatchWriter(gate)
			require.True(t, created, "the gate must forward the batch capability it was given")
			require.NoError(t, batchWriter.WritePacketBatch(probeBuffers(payloads), dests))
		}},
		{"connected WriteConnectedPacketBatch", func(t *testing.T, gate N.PacketWriter) {
			batchWriter, created := bufio.CreateConnectedPacketBatchWriter(gate)
			require.True(t, created, "the gate must forward the connected batch capability")
			require.NoError(t, batchWriter.WriteConnectedPacketBatch(probeBuffers(payloads)))
		}},
	}

	for _, shape := range shapes {
		for _, path := range paths {
			t.Run(shape.name+"/"+path.name, func(t *testing.T) {
				scheduler := NewScheduler(Options{Mode: ModeService, HighIdleWindow: 0})
				flow := scheduler.NewFlow(0)
				gate := NewPacketGate(shape.upstream, flow)

				// The shape decides whether WriterMTU is present, and it must be ABSENT when the chain
				// has none - see gateWriterMTU for why an invented value is worse than no value.
				_, hasMTU := gate.(N.WriterWithMTU)
				require.Equal(t, shape.wantMTU, hasMTU,
					"the gate must claim WriterMTU exactly when the upstream chain constrains it")

				before := shape.received()
				path.run(t, gate)

				received := shape.received() - before
				require.EqualValues(t, want, received,
					"the destination must receive every byte that was sent")

				admitted := flowAdmitted(flow)
				require.EqualValues(t, received, admitted,
					"the scheduler must be charged EXACTLY what the destination received. A gate that "+
						"admits one figure and settles another misprices real traffic, and no functional "+
						"test notices because every packet still arrives")
			})
		}
	}
}

func newProbeBuffer(payload string) *buf.Buffer {
	buffer := buf.NewSize(len(payload))
	_, err := buffer.WriteString(payload)
	if err != nil {
		panic(err)
	}
	return buffer
}

func probeBuffers(payloads []string) []*buf.Buffer {
	buffers := make([]*buf.Buffer, 0, len(payloads))
	for _, payload := range payloads {
		buffers = append(buffers, newProbeBuffer(payload))
	}
	return buffers
}

var (
	_ N.PacketWriter               = (*gateAccountingSink)(nil)
	_ N.PacketBatchWriter          = (*gateAccountingSink)(nil)
	_ N.ConnectedPacketBatchWriter = (*gateAccountingSink)(nil)
)
