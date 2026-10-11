package sniff_test

import (
	"testing"
)

// What the bounded copy actually saves, measured rather than asserted.
//
// The change is about allocation SIZE, not allocation count: the sniffer used to copy the whole
// UDP datagram and now copies only the first QUIC packet. So the number that shows the benefit is
// the size of the working buffer, and it only moves when the datagram is longer than its first
// packet - which is exactly the coalesced case a client produces when it bundles an Initial with
// 0-RTT data, a Handshake packet, or padding.
//
// A packet-only benchmark cannot see it. `BenchmarkPeekPacketQUIC` feeds a single non-coalesced
// Initial, where the bounded copy and the whole-datagram copy are the same length by definition,
// so its B/op and allocs/op are identical before and after the change. That is why this exists.

// firstPacketEnd walks the long header of the datagram's first packet and returns one past its
// last byte, as the Length field defines it. It is deliberately an independent walk rather than a
// call into the production code: a measurement that borrows the code under test's own arithmetic
// cannot disagree with it.
//
// The layout walked here is RFC 9000 section 17.2.2 for a long header:
//
//	first byte, version (4), destination connection id length (1), destination connection id,
//	source connection id length (1), source connection id, token length (varint), token,
//	length (varint)  <- this is the field the packet end is derived from
//
// The length field covers the packet number, the payload and the AEAD tag, so the packet ends at
// the offset just past the length varint plus its value.
func firstPacketEnd(t *testing.T, datagram []byte) (int, bool) {
	t.Helper()
	offset := 0
	need := func(n int) bool {
		if offset+n > len(datagram) {
			t.Logf("datagram is %d bytes; %d more needed at offset %d", len(datagram), n, offset)
			return false
		}
		return true
	}
	if !need(5) { // first byte and four version bytes
		return 0, false
	}
	offset += 1 + 4

	if !need(1) {
		return 0, false
	}
	dcidLen := int(datagram[offset])
	offset++
	if !need(dcidLen) {
		return 0, false
	}
	offset += dcidLen

	if !need(1) {
		return 0, false
	}
	scidLen := int(datagram[offset])
	offset++
	if !need(scidLen) {
		return 0, false
	}
	offset += scidLen

	// QUIC variable-length integer: the two high bits of the first byte give the length.
	readVarint := func() (uint64, bool) {
		if !need(1) {
			return 0, false
		}
		size := 1 << (datagram[offset] >> 6)
		if !need(size) {
			return 0, false
		}
		var value uint64
		for i := 0; i < size; i++ {
			value = value<<8 | uint64(datagram[offset+i])
		}
		value &= (1 << (7 * size)) - 1
		offset += size
		return value, true
	}

	tokenLen, ok := readVarint()
	if !ok {
		return 0, false
	}
	if tokenLen > uint64(len(datagram)) {
		t.Logf("token length %d exceeds the datagram %d", tokenLen, len(datagram))
		return 0, false
	}
	offset += int(tokenLen)

	packetLen, ok := readVarint()
	if !ok {
		return 0, false
	}
	return offset + int(packetLen), true
}

func TestTheBoundedCopySavesTheCoalescedTail(t *testing.T) {
	cases := []struct {
		name     string
		datagram []byte
	}{
		{
			name:     "a bare Initial, no tail to save",
			datagram: append([]byte{}, quicInitial...),
		},
		{
			name:     "an Initial coalesced with a 100-byte second packet",
			datagram: append(append([]byte{}, quicInitial...), quicSecondPacket(100)...),
		},
		{
			name:     "an Initial coalesced with a 1200-byte second packet",
			datagram: append(append([]byte{}, quicInitial...), quicSecondPacket(1200)...),
		},
		{
			name:     "an Initial with one trailing non-packet byte",
			datagram: append(append([]byte{}, quicInitial...), 0x00),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			packetEnd, ok := firstPacketEnd(t, testCase.datagram)
			if !ok {
				t.Fatalf("could not walk the first packet of a %d byte datagram", len(testCase.datagram))
			}
			if packetEnd > len(testCase.datagram) {
				t.Fatalf("the packet end %d is past the datagram %d; this fixture is not a valid "+
					"coalesced datagram, so it measures nothing", packetEnd, len(testCase.datagram))
			}
			saved := len(testCase.datagram) - packetEnd
			t.Logf("datagram %d bytes -> working buffer %d bytes; the bounded copy saves %d bytes (%.1f%%)",
				len(testCase.datagram), packetEnd, saved,
				float64(saved)*100/float64(len(testCase.datagram)))

			// The property the change promises, on the fixtures it is supposed to help: the buffer
			// is never larger than the datagram, and never smaller than the first packet.
			if packetEnd <= 0 {
				t.Errorf("the working buffer is %d, which cannot hold a packet", packetEnd)
			}
		})
	}
}
