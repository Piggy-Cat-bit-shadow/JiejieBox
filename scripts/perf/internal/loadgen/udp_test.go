package loadgen

import (
	"encoding/binary"
	"testing"
)

func TestSOCKSUDPPayloadStripsTheHeader(t *testing.T) {
	payload := make([]byte, udpHeaderSize+8)
	binary.BigEndian.PutUint32(payload[0:4], udpMagic)
	packet := []byte{0, 0, 0, 0x01, 127, 0, 0, 1, 0x12, 0x34}
	packet = append(packet, payload...)
	got := socksUDPPayload(packet)
	if len(got) != len(payload) {
		t.Fatalf("payload length = %d, want %d", len(got), len(payload))
	}
	if binary.BigEndian.Uint32(got[0:4]) != udpMagic {
		t.Fatal("magic was not preserved")
	}

	domain := []byte{0, 0, 0, 0x03, 3, 'a', 'b', 'c', 0, 80}
	domain = append(domain, payload...)
	if got := socksUDPPayload(domain); len(got) != len(payload) {
		t.Fatalf("domain payload length = %d, want %d", len(got), len(payload))
	}

	if socksUDPPayload([]byte{1, 0, 0, 1}) != nil {
		t.Fatal("a non-zero RSV must be rejected")
	}
	if socksUDPPayload([]byte{0, 0, 0, 0x02}) != nil {
		t.Fatal("an unknown ATYP must be rejected")
	}
	if socksUDPPayload([]byte{0, 0, 0, 0x01, 127}) != nil {
		t.Fatal("a truncated header must be rejected")
	}
}

func TestUDPHeaderRoundTrip(t *testing.T) {
	payload := make([]byte, udpHeaderSize)
	binary.BigEndian.PutUint32(payload[0:4], udpMagic)
	binary.BigEndian.PutUint32(payload[4:8], 42)
	binary.BigEndian.PutUint32(payload[8:12], 7)
	binary.BigEndian.PutUint64(payload[12:20], 123456789)
	magic, epoch, sequence, sentNs, ok := parseUDPHeader(payload)
	if !ok || magic != udpMagic || epoch != 42 || sequence != 7 || sentNs != 123456789 {
		t.Fatalf("parsed (%x,%d,%d,%d,%v)", magic, epoch, sequence, sentNs, ok)
	}
	if _, _, _, _, ok := parseUDPHeader(payload[:udpHeaderSize-1]); ok {
		t.Fatal("a short header was accepted")
	}
}

func TestUDPSeqWindowAttributesDuplicatesAndOrder(t *testing.T) {
	window := newUDPSeqWindow()
	order := []uint32{5, 3, 5, 7, 6}
	wantFirst := []bool{true, true, false, true, true}
	for index, sequence := range order {
		first := window.add(sequence)
		if first != wantFirst[index] {
			t.Fatalf("sequence %d first=%v, want %v", sequence, first, wantFirst[index])
		}
	}
	if window.maxSeen != 7 {
		t.Fatalf("maxSeen = %d, want 7", window.maxSeen)
	}
	if len(window.seen) != 4 {
		t.Fatalf("tracked %d sequences, want 4", len(window.seen))
	}
}
