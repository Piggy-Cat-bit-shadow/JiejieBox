package trusttunnel

// Q35: TrustTunnel UDP blackhole matrix, H2 and (with with_quic) H3.
//
// The blackhole is real: the service's packet handler reads the datagram and does not answer, which
// is what a firewall that accepts packets but never returns them looks like to the tunnel. No TCP
// blackhole is substituted, and no passing UDP probe is counted as the blackhole cell.
//
// The observations, per transport:
//  1. during the blackhole a write is accepted and NO reply arrives within the cell's wall-clock
//     deadline (the wrapper does not expose SetReadDeadline - measured, not assumed - so the
//     deadline is the caller's own select, which is the only absolute bound this API offers);
//  2. the idle association is reclaimed after the blackhole (the service-side flow closes);
//  3. after the blackhole is lifted, one datagram gets its reply;
//  4. that recovery used a NEW association (the reclaimed slot was not reused as if still open);
//  5. two datagrams after recovery come back once each, in order, and no late blackholed reply is
//     delivered (exact reply count).
//
// H2 runs without tags. H3 is compiled in by the build-tagged companion file, because the HTTP/3
// path exists only under with_quic; the matrix is not silently reduced when the tag is present.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

type blackholeUDPHandler struct {
	blackhole atomic.Bool
	flows     atomic.Int32
	closed    atomic.Int32
	packets   atomic.Int64
}

func (h *blackholeUDPHandler) NewConnection(ctx context.Context, conn *ServerConn, metadata Metadata) {
	conn.Close()
}

func (h *blackholeUDPHandler) NewPacketConnection(ctx context.Context, conn N.PacketConn, metadata Metadata) {
	h.flows.Add(1)
	go func() {
		defer h.closed.Add(1)
		defer conn.Close()
		for {
			buffer := buf.NewSize(MaxPayloadSize)
			destination, err := conn.ReadPacket(buffer)
			if err != nil {
				buffer.Release()
				return
			}
			h.packets.Add(1)
			if h.blackhole.Load() {
				buffer.Release()
				continue
			}
			_ = conn.WritePacket(buffer, destination)
		}
	}()
}

func (h *blackholeUDPHandler) liveFlows() int32 {
	return h.flows.Load() - h.closed.Load()
}

func TestUDPBlackholeMatrix(t *testing.T) {
	protocols := []string{ProtocolHTTP2}
	if q35HasQUIC {
		protocols = append(protocols, ProtocolHTTP3)
	}
	for _, protocol := range protocols {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			handler := &blackholeUDPHandler{}
			server := startTestService(t, protocol, handler, UDPLimits{IdleTimeout: 400 * time.Millisecond})
			client := server.newClient(t, protocol, testPassword, &testDialer{}, nil)
			packetConn, err := client.ListenPacket(testContext(t))
			require.NoError(t, err)
			defer packetConn.Close()

			// One reader for the whole cell. A blocked read is the honest state of a blackholed
			// association; replies are delivered to the test through a channel with a caller-side
			// deadline instead of a deadline the wrapper cannot set.
			replies := make(chan string, 8)
			go func() {
				for {
					readBuffer := make([]byte, 2048)
					n, _, readErr := packetConn.ReadFrom(readBuffer)
					if readErr != nil {
						return
					}
					replies <- string(readBuffer[:n])
				}
			}()

			destination := M.ParseSocksaddr("127.0.0.1:5353")
			handler.blackhole.Store(true)
			_, err = packetConn.WriteTo([]byte("during-blackhole"), destination.UDPAddr())
			require.NoError(t, err, "a UDP write into a blackhole is accepted by the socket")
			select {
			case reply := <-replies:
				t.Fatalf("a blackholed datagram produced a reply %q", reply)
			case <-time.After(250 * time.Millisecond):
			}
			require.Eventually(t, func() bool { return handler.packets.Load() >= 1 }, 2*time.Second, 10*time.Millisecond,
				"the service never received the blackholed datagram")

			// Cell 2: the idle association is reclaimed while the blackhole is still on.
			require.Eventually(t, func() bool { return handler.liveFlows() == 0 }, 3*time.Second, 20*time.Millisecond,
				"the idle association was not reclaimed after the blackhole")

			// Cell 3: after the blackhole is lifted, a datagram gets its reply.
			handler.blackhole.Store(false)
			_, err = packetConn.WriteTo([]byte("after-blackhole"), destination.UDPAddr())
			require.NoError(t, err)
			select {
			case reply := <-replies:
				require.Equal(t, "after-blackhole", reply, "the lifted blackhole must recover the reply")
			case <-time.After(5 * time.Second):
				t.Fatal("the lifted blackhole did not recover a reply")
			}

			// Cell 4: recovery used a new association for the reclaimed destination.
			require.GreaterOrEqual(t, handler.flows.Load(), int32(2),
				"the reply came from the reclaimed association instead of a new one")

			// Cell 5: two datagrams after recovery come back once each, in order, and the
			// blackholed datagram never appears late.
			_, err = packetConn.WriteTo([]byte("first"), destination.UDPAddr())
			require.NoError(t, err)
			_, err = packetConn.WriteTo([]byte("second"), destination.UDPAddr())
			require.NoError(t, err)
			select {
			case reply := <-replies:
				require.Equal(t, "first", reply)
			case <-time.After(5 * time.Second):
				t.Fatal("no reply for the first datagram after recovery")
			}
			select {
			case reply := <-replies:
				require.Equal(t, "second", reply)
			case <-time.After(5 * time.Second):
				t.Fatal("no reply for the second datagram after recovery")
			}
			select {
			case reply := <-replies:
				t.Fatalf("a duplicate or late reply was delivered: %q", reply)
			case <-time.After(250 * time.Millisecond):
			}
			require.Equal(t, int64(4), handler.packets.Load(),
				"the service saw a different datagram count than the test sent")

			// Q36's segment attribution, on the same real path: ingress and reply are separate
			// totals, the blackholed datagram was received but never answered, and the drain
			// segment is the idle reclaim observed in cell 2.
			stats := server.service.UDPStats()
			require.EqualValues(t, 4, stats.DatagramsReceived, "ingress segment")
			require.EqualValues(t, 3, stats.DatagramsSent, "reply segment")
			require.Zero(t, stats.DroppedDatagrams, "no queue drop on this path")
			require.GreaterOrEqual(t, stats.AssociationsExpired, uint64(1), "drain segment")
		})
	}
}
