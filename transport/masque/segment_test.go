package masque

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing/common/buf"
	"github.com/stretchr/testify/require"
)

type segmentHandler struct{}

func (segmentHandler) UpdateConfiguration(Configuration) error { return nil }
func (segmentHandler) WriteInboundBuffers(buffers []*buf.Buffer) error {
	buf.ReleaseMulti(buffers)
	return nil
}
func (segmentHandler) FrontHeadroom() int { return 0 }

func TestMASQUESegmentCounters(t *testing.T) {
	client := &Client{}
	// No session: the datagram is this owner's local drop.
	require.NoError(t, client.WritePacketBuffers([]*buf.Buffer{buf.NewSize(64)}, false))
	stats := client.SegmentStats()
	require.EqualValues(t, 1, stats.DatagramsFromDevice)
	require.EqualValues(t, 1, stats.DatagramsDroppedNoSession)
	require.Zero(t, stats.DatagramsToDevice)

	// A published but not-ready session: local "not ready" drop.
	session := newLedgerSession(client, false)
	client.current = session
	require.NoError(t, client.WritePacketBuffers([]*buf.Buffer{buf.NewSize(64)}, false))
	stats = client.SegmentStats()
	require.EqualValues(t, 1, stats.DatagramsDroppedNotReady)

	// A ready session with an inbound packet: ToDevice is the reply segment.
	client.handler = segmentHandler{}
	session.configuration = Configuration{Address: []netip.Prefix{netip.MustParsePrefix("10.0.0.1/24")}}
	session.ready = true
	session.publishStateLocked()
	packet := buf.NewSize(28)
	header := packet.Extend(28)
	header[0] = 0x45
	header[9] = 17
	copy(header[12:16], []byte{10, 0, 0, 1})
	copy(header[16:20], []byte{10, 0, 0, 2})
	session.handlePacket(packet)
	stats = client.SegmentStats()
	require.EqualValues(t, 1, stats.DatagramsToDevice)
}
