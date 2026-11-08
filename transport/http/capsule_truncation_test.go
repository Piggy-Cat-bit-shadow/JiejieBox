package http

// X01 — an oversized datagram capsule must not end the read loop.
//
// The outer QUIC boundary is a length-prefixed capsule stream: one capsule carries one UDP
// datagram (context id + payload), and the reader owns a fixed target buffer. A datagram
// larger than that buffer is the shape that could break the loop permanently, because there
// is nowhere to put it. The reader handles it kernel-style instead: the payload is
// DISCARDED and the loop continues to the next capsule, so the datagram after the oversized
// one is still delivered.
//
// That is a protocol requirement, not a nicety: a peer is allowed to send a datagram this
// side's buffer cannot hold (a larger MTU on its path), and losing every later datagram
// because of one oversized one is a permanent black hole on a live tunnel.
//
// These cases pin the reader directly - the layer that decides - rather than through a QUIC
// connection, because the decision is made before anything is enqueued.

import (
	std_bufio "bufio"
	"bytes"
	"testing"

	"github.com/sagernet/sing/common/buf"

	"github.com/stretchr/testify/require"
)

// appendDatagramCapsule encodes one datagram capsule with the given context id.
func appendDatagramCapsule(wire *bytes.Buffer, contextID uint64, payload []byte) {
	// The capsule length covers the context id varint plus the payload.
	length := uint64(VarintLen(contextID) + len(payload))
	headerLength := 1 + VarintLen(length)
	header := make([]byte, headerLength)
	header[0] = CapsuleTypeDatagram
	PutVarint(header[1:], length)
	_, _ = wire.Write(header)
	contextBytes := make([]byte, VarintLen(contextID))
	PutVarint(contextBytes, contextID)
	_, _ = wire.Write(contextBytes)
	_, _ = wire.Write(payload)
}

// TestAnOversizedDatagramCapsuleIsDroppedAndTheNextIsDelivered is the exact condition: an
// oversized datagram followed by a normal one. The reader must return the NORMAL one and
// leave nothing from the oversized payload behind.
func TestAnOversizedDatagramCapsuleIsDroppedAndTheNextIsDelivered(t *testing.T) {
	oversized := make([]byte, 64*1024)
	for index := range oversized {
		oversized[index] = byte(index)
	}
	wire := new(bytes.Buffer)
	appendDatagramCapsule(wire, 0, oversized)
	appendDatagramCapsule(wire, 0, []byte("ping"))

	reader := std_bufio.NewReader(wire)
	target := buf.NewSize(16)
	defer target.Release()

	require.NoError(t, readDatagramCapsule(reader, target),
		"an oversized datagram must be skipped, not returned as an error")
	require.Equal(t, "ping", string(target.Bytes()),
		"the datagram after the oversized one must be delivered intact")
}

// TestANonZeroContextIDDatagramIsDroppedAndTheNextIsDelivered covers the other skip the loop
// performs: a datagram for another context is not this tunnel's traffic.
func TestANonZeroContextIDDatagramIsDroppedAndTheNextIsDelivered(t *testing.T) {
	wire := new(bytes.Buffer)
	appendDatagramCapsule(wire, 1, []byte("nop"))
	appendDatagramCapsule(wire, 0, []byte("pong"))

	reader := std_bufio.NewReader(wire)
	target := buf.NewSize(16)
	defer target.Release()

	require.NoError(t, readDatagramCapsule(reader, target))
	require.Equal(t, "pong", string(target.Bytes()))
}

// TestACapsuleBeyondTheProtocolLimitIsAnError pins the one case that DOES end the loop: a
// length beyond MaxCapsuleLength is a protocol violation and must not be skipped silently,
// because the remaining stream cannot be resynchronised.
func TestACapsuleBeyondTheProtocolLimitIsAnError(t *testing.T) {
	wire := new(bytes.Buffer)
	header := wire.AvailableBuffer()
	header = append(header, CapsuleTypeDatagram)
	// 4-byte varint for MaxCapsuleLength+1 (0x100001): 10xxxxxx + three bytes.
	header = append(header, 0x80, 0x10, 0x00, 0x01)
	_, err := wire.Write(header)
	require.NoError(t, err)

	reader := std_bufio.NewReader(wire)
	target := buf.NewSize(16)
	defer target.Release()

	err = readDatagramCapsule(reader, target)
	require.Error(t, err, "an unrepresentable capsule length must end the read loop")
	require.Contains(t, err.Error(), "capsule too large")
}
