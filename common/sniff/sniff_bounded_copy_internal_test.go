package sniff

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/sha256"
	"encoding/binary"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	qtls "github.com/sagernet/sing-box/common/sniff/internal/qtls"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/hkdf"
)

// The retained CRYPTO fragment is a view into the working buffer, so that buffer's capacity is what
// the fragment holds alive across packets - and the working buffer's size is the one thing the
// bounded-copy change moves.
//
// # Why this test is internal, and why it measures cap rather than a verdict
//
// Every end-to-end test around this one is blind to the copy half of the change. An adversarial review
// reverted `newPacket` to a whole-datagram copy and the entire package stayed green, because what the
// sniffer REPORTS is byte-identical either way; only the allocation differs. `metadata.SniffContext`
// is `any` holding an unexported fragment type, so an external test cannot read the payload - and the
// payload is where the size is. Hence an internal test, and hence `cap(fragment.payload)`.
//
// # The property
//
// The sniff reads only the first QUIC packet of a datagram. A peer that coalesces a large second
// packet behind a partial Initial must therefore not be able to make this package retain a
// datagram-sized buffer, because nothing past the first packet's Length field is ever read.
//
// # The fixture must retain something
//
// A COMPLETE ClientHello returns a verdict and stores nothing, so a whole-datagram fixture would
// measure an empty fragment list and pass against any implementation. The packet below carries a
// CRYPTO frame whose announced length exceeds the bytes present, which is exactly the case that stores
// a fragment for the next packet - and the test refuses to pass if no fragment was stored.

// boundedCopyCryptoSegment is one CRYPTO frame carrying part of a handshake message at an offset.
func boundedCopyCryptoSegment(payload []byte, offset uint64) []byte {
	plaintext := []byte{0x06}
	plaintext = boundedCopyVarint(plaintext, offset)
	plaintext = boundedCopyVarint(plaintext, uint64(len(payload)))
	return append(plaintext, payload...)
}

func boundedCopyVarint(b []byte, value uint64) []byte {
	switch {
	case value < 1<<6:
		return append(b, byte(value))
	default:
		return append(b, 0x40|byte(value>>8), byte(value))
	}
}

// boundedCopyInitial seals one v1 Initial carrying the given frames, the way a client does: derive the
// Initial secrets from the destination connection id, seal with the header as associated data, then
// apply header protection to the first byte and the packet number field.
//
// It seals locally rather than calling the external test package's builder, which an internal test
// cannot import.
func boundedCopyInitial(t *testing.T, dcid []byte, plaintext []byte) []byte {
	t.Helper()
	require.NotEmpty(t, dcid)
	require.GreaterOrEqual(t, len(plaintext), 20,
		"header protection samples twenty bytes past the packet number field")

	const packetNumLen = 1
	initialSecret := hkdf.Extract(sha256.New, dcid, qtls.SaltV1)
	secret := qtls.HKDFExpandLabel(crypto.SHA256, initialSecret, []byte{}, "client in", sha256.Size)
	hpKey := qtls.HKDFExpandLabel(crypto.SHA256, secret, []byte{}, qtls.HKDFLabelHeaderProtectionV1, 16)
	key := qtls.HKDFExpandLabel(crypto.SHA256, secret, []byte{}, qtls.HKDFLabelKeyV1, 16)
	iv := qtls.HKDFExpandLabel(crypto.SHA256, secret, []byte{}, qtls.HKDFLabelIVV1, 12)

	header := []byte{0xc0 | byte(packetNumLen-1)}
	header = binary.BigEndian.AppendUint32(header, qtls.Version1)
	header = append(header, byte(len(dcid)))
	header = append(header, dcid...)
	header = append(header, 0x00) // no source connection id
	header = boundedCopyVarint(header, 0)
	// The Length field covers the packet number, the payload and the sixteen byte tag.
	header = boundedCopyVarint(header, uint64(packetNumLen+len(plaintext)+16))
	header = append(header, 0x00) // the single packet number byte

	aead := qtls.AEADAESGCMTLS13(key, iv)
	nonce := make([]byte, aead.NonceSize())
	packet := aead.Seal(header, nonce, plaintext, header)

	pnOffset := len(header) - packetNumLen
	block, err := aes.NewCipher(hpKey)
	require.NoError(t, err)
	mask := make([]byte, aes.BlockSize)
	block.Encrypt(mask, packet[pnOffset+4:pnOffset+4+aes.BlockSize])
	packet[0] ^= mask[0] & 0xf
	packet[pnOffset] ^= mask[1]
	return packet
}

// retainedFragmentCapacity sniffs the datagram and returns the capacity and length of the first CRYPTO
// fragment it retained.
func retainedFragmentCapacity(t *testing.T, datagram []byte) (capacity int, length int) {
	t.Helper()
	var metadata adapter.InboundContext
	err := QUICClientHello(context.Background(), &metadata, datagram)
	require.Error(t, err, "a partial ClientHello must ask for more data rather than claim a verdict")
	fragments, isFragments := metadata.SniffContext.([]qCryptoFragment)
	require.True(t, isFragments,
		"the sniff retained no CRYPTO fragment, so this fixture never reaches the retention path and "+
			"the measurement below would pass against any implementation")
	require.NotEmpty(t, fragments)
	return cap(fragments[0].payload), len(fragments[0].payload)
}

// TestTheRetainedFragmentIsBoundedByThePacketNotTheDatagram is the guard for the copy half of the
// bounded-copy change.
func TestTheRetainedFragmentIsBoundedByThePacketNotTheDatagram(t *testing.T) {
	t.Parallel()

	// A ClientHello is split so the first Initial carries only part of it. The exact bytes do not
	// matter to the bound; what matters is that the sniff stores a fragment and asks for the next
	// packet, and that the packet's Length field ends well before the coalesced tail.
	hello := make([]byte, 300)
	for i := range hello {
		hello[i] = byte(i)
	}
	const split = 100
	packet := boundedCopyInitial(t, []byte{1, 2, 3, 4, 5, 6, 7, 8}, boundedCopyCryptoSegment(hello[:split], 0))

	// A tail far larger than the packet, so no allocator rounding can hide the difference between a
	// packet-bounded buffer and a datagram-bounded one.
	const tailLength = 8 << 10
	datagram := append(append([]byte{}, packet...), make([]byte, tailLength)...)

	capacity, length := retainedFragmentCapacity(t, datagram)
	require.Positive(t, length, "the retained fragment must carry the bytes it claims")
	t.Logf("packet %d bytes + %d byte coalesced tail = %d byte datagram -> retained fragment cap %d, len %d",
		len(packet), tailLength, len(datagram), capacity, length)

	// The property, as the bound the production copy provides: the retained buffer is the first packet
	// plus at most the growth a Go allocator applies to it. The margin is generous because a rounded-up
	// capacity is still a bounded one; a whole-datagram copy is more than 8 KiB past it.
	require.LessOrEqual(t, capacity, len(packet)+512,
		"the retained buffer must be the first packet (%d bytes) plus allocator rounding, not the "+
			"datagram (%d bytes): the sniff reads only the first packet, and a peer that coalesces a "+
			"large second one must not decide how much this package holds", len(packet), len(datagram))

	// The same statement in the form that survives a change of allocator: the retained buffer must not
	// grow with the tail. Doubling the tail must not move the capacity at all.
	doubled := append(append([]byte{}, packet...), make([]byte, 2*tailLength)...)
	doubledCapacity, _ := retainedFragmentCapacity(t, doubled)
	require.Equal(t, capacity, doubledCapacity,
		"the retained buffer grew with the coalesced tail: it is sized by the datagram, not the packet")
}
