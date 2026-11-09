package trusttunnel

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing/common/auth"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// drainOnlyHandler consumes every datagram of an association and never replies,
// so the server sees a one-way uplink and no response traffic.
type drainOnlyHandler struct {
	consumed chan struct{}
	closed   chan struct{}
}

func (h *drainOnlyHandler) NewConnection(ctx context.Context, conn *ServerConn, metadata Metadata) {
	conn.Close()
}

func (h *drainOnlyHandler) NewPacketConnection(_ context.Context, conn N.PacketConn, _ Metadata) {
	defer conn.Close()
	defer func() {
		select {
		case h.closed <- struct{}{}:
		default:
		}
	}()
	buffer := buf.NewSize(MaxPayloadSize)
	defer buffer.Release()
	for {
		buffer.Reset()
		if _, err := conn.ReadPacket(buffer); err != nil {
			return
		}
		h.consumed <- struct{}{}
	}
}

// TestUDPUploadOnlyAssociationStaysLive sends datagrams past twice the idle
// window without a single reply. Every admitted datagram is activity, so the
// same association must survive; a rein that only counted replies or creation
// expired this flow after roughly one window.
func TestUDPUploadOnlyAssociationStaysLive(t *testing.T) {
	const timeout = 300 * time.Millisecond
	for _, protocol := range []string{ProtocolHTTP2, ProtocolHTTP3} {
		if protocol == ProtocolHTTP3 && !quicIncluded {
			continue
		}
		t.Run(protocol, func(t *testing.T) {
			handler := &drainOnlyHandler{
				consumed: make(chan struct{}, 64),
				closed:   make(chan struct{}, 4),
			}
			server := startTestService(t, protocol, handler, UDPLimits{IdleTimeout: timeout})
			client := server.newClient(t, protocol, testPassword, &testDialer{}, nil)
			packetConn, err := client.ListenPacket(testContext(t))
			if err != nil {
				t.Fatal(err)
			}
			defer packetConn.Close()
			destination := udpDestinations(1)[0]
			started := time.Now()
			const packets = 24 // 24 x 25 ms = 600 ms, twice the idle window
			for index := 0; index < packets; index++ {
				writeDatagram(t, packetConn, []byte("upload-only"), destination)
				select {
				case <-handler.consumed:
				case <-time.After(5 * time.Second):
					t.Fatalf("packet %d did not reach the handler", index+1)
				}
				select {
				case <-handler.closed:
					t.Fatalf("an active one-way association expired after %s, with %d admitted packets and no replies", time.Since(started).Round(time.Millisecond), index+1)
				case <-time.After(25 * time.Millisecond):
				}
			}
			if elapsed := time.Since(started); elapsed < 2*timeout {
				t.Fatalf("the test crossed only %s of the %s window", elapsed, 2*timeout)
			}
			if stats := server.service.UDPStats(); stats.Associations != 1 || stats.QueuedBytes != 0 {
				t.Fatalf("after the one-way upload: %+v", stats)
			}
			packetConn.Close()
			waitForUDPStats(t, server.service, "the release after the one-way upload", func(stats UDPStats) bool {
				return stats.Associations == 0 && stats.QueuedBytes == 0
			})
		})
	}
}

// TestUDPIdleAssociationStillExpires is the negative control for the test
// above: an association that really goes silent must still return its slot, and
// the stream must stay usable.
func TestUDPIdleAssociationStillExpires(t *testing.T) {
	const timeout = 150 * time.Millisecond
	for _, protocol := range []string{ProtocolHTTP2, ProtocolHTTP3} {
		if protocol == ProtocolHTTP3 && !quicIncluded {
			continue
		}
		t.Run(protocol, func(t *testing.T) {
			handler := &drainOnlyHandler{
				consumed: make(chan struct{}, 8),
				closed:   make(chan struct{}, 4),
			}
			server := startTestService(t, protocol, handler, UDPLimits{IdleTimeout: timeout})
			client := server.newClient(t, protocol, testPassword, &testDialer{}, nil)
			packetConn, err := client.ListenPacket(testContext(t))
			if err != nil {
				t.Fatal(err)
			}
			defer packetConn.Close()
			destinations := udpDestinations(2)
			started := time.Now()
			writeDatagram(t, packetConn, []byte("one"), destinations[0])
			select {
			case <-handler.consumed:
			case <-time.After(5 * time.Second):
				t.Fatal("the datagram did not reach the handler")
			}
			select {
			case <-handler.closed:
			case <-time.After(5 * time.Second):
				t.Fatal("a silent association did not expire")
			}
			if elapsed := time.Since(started); elapsed < timeout/2 {
				t.Fatalf("the association expired after only %s of its %s window", elapsed.Round(time.Millisecond), timeout)
			}
			waitForUDPStats(t, server.service, "the silent expiry", func(stats UDPStats) bool {
				return stats.Associations == 0 && stats.QueuedBytes == 0
			})
			// The slot is reusable on the same stream.
			writeDatagram(t, packetConn, []byte("two"), destinations[1])
			select {
			case <-handler.consumed:
			case <-time.After(5 * time.Second):
				t.Fatal("the stream did not admit a new association after the expiry")
			}
			packetConn.Close()
			waitForUDPStats(t, server.service, "the final release", func(stats UDPStats) bool {
				return stats.Associations == 0 && stats.QueuedBytes == 0
			})
		})
	}
}

// TestUDPIdleExpiryRechecksActivity models the interleaving of the idle sweep
// and a datagram: the sweep snapshots a stale timestamp, the datagram is
// admitted, and only then does the sweep try to close. The close must re-check
// under the queue lock and lose, or only the sweeper's timing would decide
// whether an active association survives.
func TestUDPIdleExpiryRechecksActivity(t *testing.T) {
	service, err := NewService(ServiceOptions{
		Handler:   &rejectHandler{},
		Users:     []auth.User{{Username: testUsername, Password: testPassword}},
		UDPLimits: UDPLimits{IdleTimeout: time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	destination := udpDestinations(1)[0]
	key := udpFlowKey{source: M.Socksaddr{}.AddrPort(), destination: destination.AddrPort()}
	session := &udpSession{
		service: service,
		ctx:     testContext(t),
		flows:   make(map[udpFlowKey]*udpFlow),
		done:    make(chan struct{}),
	}
	flow := &udpFlow{
		session:     session,
		key:         key,
		source:      M.Socksaddr{},
		destination: destination,
		packets:     make(chan *buf.Buffer, udpFlowQueueSize),
		done:        make(chan struct{}),
	}
	if !service.udp.acquireAssociation() {
		t.Fatal("the association slot was refused")
	}
	session.flows[key] = flow
	// The sweeper snapshotted here: the association looked idle.
	flow.lastActivity.Store(time.Now().Add(-time.Minute).UnixNano())
	deadline := time.Now().UnixNano()
	// A datagram is admitted after the snapshot.
	payload := buf.NewSize(8)
	_, _ = payload.Write([]byte("active"))
	if !session.reserveBytesLocked(payload.Len()) {
		t.Fatal("the datagram was refused")
	}
	if !flow.push(payload) {
		t.Fatal("the datagram was dropped")
	}
	if flow.closeIfIdle(deadline) {
		t.Fatal("the sweep closed an association that carried a datagram after its snapshot")
	}
	replyBuffer := buf.NewSize(MaxPayloadSize)
	if _, err = flow.ReadPacket(replyBuffer); err != nil {
		replyBuffer.Release()
		t.Fatalf("the association did not survive the sweep: %s", err)
	}
	replyBuffer.Release()
	// With no later activity it does close, and only once.
	if !flow.closeIfIdle(time.Now().Add(time.Second).UnixNano()) {
		t.Fatal("the idle association did not close")
	}
	if flow.closeIfIdle(time.Now().Add(time.Second).UnixNano()) {
		t.Fatal("the association closed twice")
	}
	stats := service.UDPStats()
	if stats.Associations != 0 || stats.QueuedBytes != 0 {
		t.Fatalf("after the close: %+v", stats)
	}
}

// TestUDPIdleExpiryIgnoresRefusedAndDropped makes the admission refusals and
// queue drops real: a refused association must not refresh the live one, and a
// datagram dropped by the full per-flow queue must not refresh its flow either.
func TestUDPIdleExpiryIgnoresRefusedAndDropped(t *testing.T) {
	const timeout = time.Minute
	handler := &holdingHandler{release: make(chan struct{})}
	t.Cleanup(func() { close(handler.release) })
	service, err := NewService(ServiceOptions{
		Handler: handler,
		Users:   []auth.User{{Username: testUsername, Password: testPassword}},
		UDPLimits: UDPLimits{
			SessionAssociations: 1,
			ServiceAssociations: 8,
			SessionQueuedBytes:  1 << 20,
			ServiceQueuedBytes:  1 << 20,
			IdleTimeout:         timeout,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	destinations := udpDestinations(2)
	session := &udpSession{
		service: service,
		ctx:     testContext(t),
		flows:   make(map[udpFlowKey]*udpFlow),
		done:    make(chan struct{}),
	}
	first := buf.NewSize(8)
	_, _ = first.Write([]byte("first"))
	session.dispatch(M.Socksaddr{}, destinations[0], first)
	key := udpFlowKey{source: M.Socksaddr{}.AddrPort(), destination: destinations[0].AddrPort()}
	flow := session.flows[key]
	if flow == nil {
		t.Fatal("the first association was not admitted")
	}
	before := flow.lastActivity.Load()
	// A different 4-tuple is refused by the stream limit.
	refused := buf.NewSize(8)
	_, _ = refused.Write([]byte("refused"))
	session.dispatch(M.Socksaddr{}, destinations[1], refused)
	if stats := service.UDPStats(); stats.RefusedAssociations == 0 {
		t.Fatalf("the second association was not refused: %+v", stats)
	}
	if flow.lastActivity.Load() != before {
		t.Fatal("a refused association refreshed the live association")
	}
	// Fill the per-flow queue, then offer more: the overflow is dropped.
	for index := 0; index < udpFlowQueueSize; index++ {
		packet := buf.NewSize(8)
		_, _ = packet.Write([]byte("fill"))
		session.dispatch(M.Socksaddr{}, destinations[0], packet)
	}
	before = flow.lastActivity.Load()
	time.Sleep(5 * time.Millisecond)
	overflow := buf.NewSize(8)
	_, _ = overflow.Write([]byte("overflow"))
	session.dispatch(M.Socksaddr{}, destinations[0], overflow)
	stats := service.UDPStats()
	if stats.DroppedDatagrams == 0 {
		t.Fatalf("the queue overflow was not counted: %+v", stats)
	}
	if flow.lastActivity.Load() != before {
		t.Fatal("a queue drop refreshed the association")
	}
	session.close()
	waitForUDPStats(t, service, "the release after the refused and dropped datagrams", func(stats UDPStats) bool {
		return stats.Associations == 0 && stats.QueuedBytes == 0
	})
}
