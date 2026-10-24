package masque

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	transportHTTP "github.com/sagernet/sing-box/transport/http"
	"github.com/sagernet/sing/common/buf"
)

// These tests drive the real inbound path, not the interface preference:
//
//	capsule bytes -> session.readDatagramCapsule -> handler.WaitWriteInboundBuffers -> device queue
//
// The device here is a bounded, close-wakeable queue with the same contract as the memory TUN (the
// primitive itself is covered by sing-tun's tun_memory tests, where the TUN can be attached). What
// this file pins is that the session honours backpressure instead of buffering without bound, that
// a full device parks the capsule loop rather than dropping, and that a handler with only the
// non-blocking contract keeps the old behaviour.

type boundedDevice struct {
	access   sync.Mutex
	pending  int
	closed   bool
	writable *sync.Cond
	writes   int
	err      error
}

func newBoundedDevice(pending int) *boundedDevice {
	device := &boundedDevice{pending: pending}
	device.writable = sync.NewCond(&device.access)
	return device
}

func (d *boundedDevice) WriteInboundBuffers(packetBuffers []*buf.Buffer) error {
	d.access.Lock()
	defer d.access.Unlock()
	d.writes += len(packetBuffers)
	for _, packetBuffer := range packetBuffers {
		packetBuffer.Release()
	}
	return d.err
}

func (d *boundedDevice) WaitWriteInboundBuffers(packetBuffers []*buf.Buffer) error {
	d.access.Lock()
	defer d.access.Unlock()
	for d.pending <= 0 && !d.closed {
		d.writable.Wait()
	}
	if d.closed {
		for _, packetBuffer := range packetBuffers {
			packetBuffer.Release()
		}
		return os.ErrClosed
	}
	d.pending--
	d.writes += len(packetBuffers)
	for _, packetBuffer := range packetBuffers {
		packetBuffer.Release()
	}
	return d.err
}

func (d *boundedDevice) drainOne() {
	d.access.Lock()
	d.pending++
	d.writable.Signal()
	d.access.Unlock()
}

func (d *boundedDevice) close() {
	d.access.Lock()
	d.closed = true
	d.writable.Broadcast()
	d.access.Unlock()
}

func (d *boundedDevice) writeCount() int {
	d.access.Lock()
	defer d.access.Unlock()
	return d.writes
}

type capsulePathHandler struct {
	writer   inboundBufferWriter
	attempts chan struct{}
	result   chan error
}

func (h *capsulePathHandler) handleAddressAssign([]AssignedAddress) error { return nil }
func (h *capsulePathHandler) handleAddressRequest([]AssignedAddress) error {
	return nil
}
func (h *capsulePathHandler) handleRouteAdvertisement([]AddressRange) error { return nil }
func (h *capsulePathHandler) handleDNSAssign([]DNSConfiguration) error      { return nil }
func (h *capsulePathHandler) handlePREF64([]netip.Prefix) error             { return nil }
func (h *capsulePathHandler) handlePacketTooBig(buffer *buf.Buffer, _ int)  { buffer.Release() }

func (h *capsulePathHandler) handlePacket(buffer *buf.Buffer) {
	h.attempts <- struct{}{}
	err := writeInboundBuffers(h.writer, []*buf.Buffer{buffer})
	h.result <- err
}

func packetCapsuleForTest(payload []byte) []byte {
	// Context ID 0 is a one-byte varint, so the capsule payload is the context ID plus the packet.
	length := 1 + len(payload)
	capsule := []byte{byte(transportHTTP.CapsuleTypeDatagram), byte(length), 0}
	return append(capsule, payload...)
}

func testPacketPayload() []byte {
	packet := make([]byte, 24)
	packet[0] = 0x45
	packet[19] = 17
	return packet
}

func TestCapsuleInboundParksOnAFullDeviceAndCloseWakesIt(t *testing.T) {
	device := newBoundedDevice(0)
	handler := &capsulePathHandler{
		writer:   device,
		attempts: make(chan struct{}, 8),
		result:   make(chan error, 8),
	}
	peer, sessionSide := net.Pipe()
	t.Cleanup(func() { peer.Close() })
	current := newSession(context.Background(), sessionSide, handler, func() int { return 0 })
	runDone := make(chan error, 1)
	go func() { runDone <- current.run() }()

	if _, err := peer.Write(packetCapsuleForTest(testPacketPayload())); err != nil {
		t.Fatalf("write capsule: %v", err)
	}
	select {
	case <-handler.attempts:
	case <-time.After(5 * time.Second):
		t.Fatal("the capsule never reached the inbound write path")
	}

	// The device queue is full and open: the capsule loop must stay parked, not drop the packet and
	// not buffer it somewhere else.
	select {
	case err := <-handler.result:
		t.Fatalf("the inbound write returned %v while the device was full", err)
	case <-time.After(200 * time.Millisecond):
	}
	if writes := device.writeCount(); writes != 0 {
		t.Fatalf("a full device accepted %d writes", writes)
	}

	// A drained slot releases exactly one parked write.
	device.drainOne()
	select {
	case err := <-handler.result:
		if err != nil {
			t.Fatalf("the unblocked inbound write returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("draining the device did not release the parked write")
	}

	// The session is still alive and reads the next capsule.
	device.drainOne()
	if _, err := peer.Write(packetCapsuleForTest(testPacketPayload())); err != nil {
		t.Fatalf("write second capsule: %v", err)
	}
	select {
	case err := <-handler.result:
		if err != nil {
			t.Fatalf("the second inbound write returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the session stopped reading capsules after a blocked write")
	}

	// Closing the device while another write is parked must return os.ErrClosed, not hang.
	if _, err := peer.Write(packetCapsuleForTest(testPacketPayload())); err != nil {
		t.Fatalf("write third capsule: %v", err)
	}
	select {
	case <-handler.attempts:
	case <-time.After(5 * time.Second):
		t.Fatal("the third capsule was not handed to the device")
	}
	device.close()
	select {
	case err := <-handler.result:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("the parked write returned %v after close, want os.ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closing the device did not wake the parked write")
	}

	_ = peer.Close()
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the session did not return after its stream was closed")
	}
}

type nonBlockingOnlyDevice struct {
	access sync.Mutex
	writes int
}

func (d *nonBlockingOnlyDevice) WriteInboundBuffers(packetBuffers []*buf.Buffer) error {
	d.access.Lock()
	d.writes += len(packetBuffers)
	d.access.Unlock()
	for _, packetBuffer := range packetBuffers {
		packetBuffer.Release()
	}
	return nil
}

func (d *nonBlockingOnlyDevice) writeCount() int {
	d.access.Lock()
	defer d.access.Unlock()
	return d.writes
}

func TestCapsuleInboundFallsBackToTheNonBlockingWriter(t *testing.T) {
	device := &nonBlockingOnlyDevice{}
	handler := &capsulePathHandler{
		writer:   device,
		attempts: make(chan struct{}, 4),
		result:   make(chan error, 4),
	}
	peer, sessionSide := net.Pipe()
	t.Cleanup(func() { peer.Close() })
	current := newSession(context.Background(), sessionSide, handler, func() int { return 0 })
	go func() { _ = current.run() }()

	if _, err := peer.Write(packetCapsuleForTest(testPacketPayload())); err != nil {
		t.Fatalf("write capsule: %v", err)
	}
	select {
	case err := <-handler.result:
		if err != nil {
			t.Fatalf("the non-blocking fallback returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the non-blocking fallback never completed")
	}
	if device.writeCount() != 1 {
		t.Fatalf("the non-blocking fallback wrote %d packets", device.writeCount())
	}
}
