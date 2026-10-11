package sniff_test

// The QUIC cases the task's coverage list names that the existing suite does not pin: the connection
// id matrix (SCID as well as DCID, including the twenty byte maximum), the packet number field
// lengths two and three, the header-protection sample when the packet ends before it, an AEAD that
// does not authenticate, and the metadata.SniffContext ownership scenario end to end.
//
// The packet builder, the captured Chrome ClientHello and the frame sequence all come from
// quic_boundary_test.go, so these cases are built the same way as the ones already there.

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/sniff"
	"github.com/sagernet/sing-box/common/sniff/internal/qtls"
	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
)

// TestQUICInitialConnectionIDAndPacketNumberMatrix walks the two header fields whose lengths move
// every offset behind them, and the packet number field width that decides how many bytes header
// protection masks. The existing variants cover DCID 1, 8 and 20 and a four byte source connection
// id; what is added here is the source connection id at its extremes, the two intermediate packet
// number widths, and the cross product so that a length that is only correct in isolation fails.
func TestQUICInitialConnectionIDAndPacketNumberMatrix(t *testing.T) {
	t.Parallel()
	plaintext := quicInitialPlaintext()
	for _, dcidLen := range []int{1, 8, 20} {
		for _, scidLen := range []int{0, 1, 20} {
			for _, packetNumLen := range []int{1, 2, 3, 4} {
				dcid := make([]byte, dcidLen)
				for i := range dcid {
					dcid[i] = byte(i + 1)
				}
				scid := make([]byte, scidLen)
				for i := range scid {
					scid[i] = byte(0xf0 - i)
				}
				name := "dcid" + strconv.Itoa(dcidLen) + "-scid" + strconv.Itoa(scidLen) + "-pn" + strconv.Itoa(packetNumLen)
				t.Run(name, func(t *testing.T) {
					packetNum := uint64(0)
					if packetNumLen > 1 {
						// A value whose low bytes are non-zero, so a parser that read the wrong
						// end of the packet number field would derive a different nonce.
						packetNum = 0x01020304 >> (8 * (4 - packetNumLen))
					}
					packet := buildInitialPacket(t, quicPacketSpec{
						version:      qtls.Version1,
						dcid:         dcid,
						scid:         scid,
						packetNum:    packetNum,
						packetNumLen: packetNumLen,
						plaintext:    plaintext,
					})
					before := append([]byte{}, packet...)
					var metadata adapter.InboundContext
					require.NoError(t, sniff.QUICClientHello(context.Background(), &metadata, packet))
					require.Equal(t, C.ProtocolQUIC, metadata.Protocol)
					require.Equal(t, "www.google.com", metadata.Domain)
					require.Equal(t, C.ClientChromium, metadata.Client)
					require.Equal(t, before, packet, "the sniffer wrote into the datagram")

					// A well-formed second packet behind it: the coalesced suffix has to leave the
					// verdict alone whatever the header lengths are.
					datagram := append(append([]byte{}, packet...), quicSecondPacket(100)...)
					suffix := append([]byte{}, datagram[len(packet):]...)
					var coalesced adapter.InboundContext
					require.NoError(t, sniff.QUICClientHello(context.Background(), &coalesced, datagram))
					require.Equal(t, metadata.Protocol, coalesced.Protocol)
					require.Equal(t, metadata.Domain, coalesced.Domain)
					require.Equal(t, metadata.Client, coalesced.Client)
					require.Equal(t, suffix, datagram[len(packet):], "the sniffer rewrote the second packet")
				})
			}
		}
	}
}

// TestQUICInitialHeaderProtectionSampleBoundary is the truncation the announced-length check cannot
// see: the packet's own Length says it ends where header protection still needs sixteen bytes, so
// the sample runs past the end of the packet. The datagram is extended past the packet end one byte
// at a time, so the case walks the exact point at which the sample becomes readable, and the answer
// must be a definite refusal at every length - never a claim, never a request for more bytes, and
// never a write into the caller's datagram.
func TestQUICInitialHeaderProtectionSampleBoundary(t *testing.T) {
	t.Parallel()
	plaintext := quicInitialPlaintext()
	packet := buildInitialPacket(t, quicPacketSpec{
		version:      qtls.Version1,
		dcid:         []byte{1, 2, 3, 4, 5, 6, 7, 8},
		packetNumLen: 1,
		plaintext:    plaintext,
	})
	// With a one byte packet number field and no token the Length varint is two bytes at offset 16
	// and the packet number field starts at 18, which is the offset the parser computes.
	pnOffset := len(packet) - 1 - len(plaintext) - 16
	require.Equal(t, 18, pnOffset)
	require.Less(t, 16, pnOffset, "the Length varint is two bytes, so it ends at the packet number field")

	for _, suffix := range []int{0, 1, 3, 4, 5, 8, 15, 16, 17, 32, 100} {
		t.Run("suffix"+strconv.Itoa(suffix), func(t *testing.T) {
			datagram := append([]byte{}, packet[:pnOffset]...)
			// A two byte varint announcing a four byte packet: the packet number field and nothing
			// else, which is the smallest Length the parser accepts.
			datagram[16] = 0x40
			datagram[17] = 0x04
			datagram = append(datagram, make([]byte, suffix)...)
			// The trailing bytes are not zero, so a sample taken from them is not a sample taken
			// from a run of zeros that a zero-filled buffer would also produce.
			for i := pnOffset + 4; i < len(datagram); i++ {
				datagram[i] = byte(i*31 + 7)
			}
			before := append([]byte{}, datagram...)

			var metadata adapter.InboundContext
			err := sniff.QUICClientHello(context.Background(), &metadata, datagram)
			require.Error(t, err, "a packet whose Length cannot hold the protection sample was accepted")
			require.False(t, errors.Is(err, sniff.ErrNeedMoreData),
				"a malformed packet asked for more data instead of deciding")
			require.Empty(t, metadata.Protocol)
			require.Empty(t, metadata.Domain)
			require.Nil(t, metadata.SniffContext)
			require.Equal(t, before, datagram, "the sniffer wrote into the datagram")
		})
	}
}

// TestQUICInitialAEADFailureIsNotAClaim is the authentication half: a packet whose ciphertext, tag,
// version or destination connection id does not match the key it was sealed with must be refused,
// and must not leave a protocol, a domain or a fragment behind. Every case starts from a packet this
// sniffer accepts and changes exactly one thing, so a case can only fail because of that change.
func TestQUICInitialAEADFailureIsNotAClaim(t *testing.T) {
	t.Parallel()
	plaintext := quicInitialPlaintext()
	build := func() []byte {
		return buildInitialPacket(t, quicPacketSpec{
			version:      qtls.Version1,
			dcid:         []byte{1, 2, 3, 4, 5, 6, 7, 8},
			packetNumLen: 1,
			plaintext:    plaintext,
		})
	}
	pnOffset := len(build()) - 1 - len(plaintext) - 16

	for _, testCase := range []struct {
		name    string
		corrupt func(packet []byte)
	}{
		{"ciphertext flipped", func(packet []byte) { packet[pnOffset+6] ^= 0x01 }},
		{"tag flipped", func(packet []byte) { packet[len(packet)-1] ^= 0x80 }},
		{"packet number flipped", func(packet []byte) { packet[pnOffset] ^= 0x01 }},
		{"wrong key for the destination connection id", func(packet []byte) {
			// The Initial secret is derived from the destination connection id, so rewriting it
			// changes the header protection key, the packet key and the iv at once - the shape a
			// packet encrypted for another connection has.
			packet[6] ^= 0xff
		}},
		{"sealed as v2 but announced as v1", func(packet []byte) {
			packet[1], packet[2], packet[3], packet[4] = 0x00, 0x00, 0x00, 0x02
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// The control: the unmodified packet really is accepted, so a failure below is the
			// corruption rather than a packet that never parsed.
			control := build()
			var controlMetadata adapter.InboundContext
			require.NoError(t, sniff.QUICClientHello(context.Background(), &controlMetadata, control))
			require.Equal(t, "www.google.com", controlMetadata.Domain)

			packet := build()
			testCase.corrupt(packet)
			before := append([]byte{}, packet...)
			var metadata adapter.InboundContext
			err := sniff.QUICClientHello(context.Background(), &metadata, packet)
			require.Error(t, err, "a packet that does not authenticate was accepted")
			require.False(t, errors.Is(err, sniff.ErrNeedMoreData),
				"a packet that does not authenticate asked for more data")
			require.Empty(t, metadata.Protocol)
			require.Empty(t, metadata.Domain)
			require.Empty(t, metadata.Client)
			require.Nil(t, metadata.SniffContext)
			require.Equal(t, before, packet, "the sniffer wrote into the datagram")
		})
	}
}

// TestQUICSniffContextOwnershipEndToEnd is the ownership scenario stated as a whole: a ClientHello
// arrives as three Initial packets, the caller's datagram is overwritten after every sniff - that is
// what a pooled datagram reader does the moment the call returns - and the sniff still succeeds on
// the last packet with the right protocol, the right domain and no fragments left over.
//
// # What makes this an oracle rather than an agreement with the implementation
//
// The same three fragments are sniffed twice: once with the caller's datagrams left alone and once
// with every one of them overwritten with a non-zero pattern after its sniff. If a saved fragment
// aliased the caller's buffer, the two runs would disagree - the poisoned run would assemble a
// ClientHello whose bytes are 0xa5 instead of the ones the client sent, and the server name would
// either disappear or change. Agreement between the two runs is the property being tested; the
// domain alone would not be, because a single run can only show what the implementation does.
func TestQUICSniffContextOwnershipEndToEnd(t *testing.T) {
	t.Parallel()
	hello := quicChromeClientHello
	require.Len(t, hello, 293)
	// Three fragments, so the state has to survive two calls rather than one.
	bounds := []int{0, 97, 191, 293}

	buildFragments := func() [][]byte {
		packets := make([][]byte, 0, len(bounds)-1)
		for i := 0; i+1 < len(bounds); i++ {
			packets = append(packets, buildInitialPacket(t, quicPacketSpec{
				version:      qtls.Version1,
				dcid:         []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88},
				packetNum:    uint64(i),
				packetNumLen: 1,
				plaintext:    quicCryptoSegment(hello[bounds[i]:bounds[i+1]], uint64(bounds[i])),
			}))
		}
		return packets
	}

	// run sniffs the fragments in order and reports the final state. poison controls whether the
	// caller reuses each datagram as soon as the sniff returns.
	run := func(t *testing.T, poison bool) adapter.InboundContext {
		t.Helper()
		packets := buildFragments()
		var metadata adapter.InboundContext
		for index, packet := range packets {
			before := append([]byte{}, packet...)
			err := sniff.QUICClientHello(context.Background(), &metadata, packet)
			require.Equal(t, before, packet, "round %d: the sniffer wrote into the datagram", index)
			last := index == len(packets)-1
			if last {
				require.NoError(t, err, "the last fragment did not complete the ClientHello")
			} else {
				require.ErrorIs(t, err, sniff.ErrNeedMoreData,
					"round %d: a fragment the ClientHello still needs was answered with a verdict", index)
				require.NotNil(t, metadata.SniffContext,
					"round %d: the fragment that has not been assembled yet was not kept", index)
			}
			if poison {
				for i := range packet {
					packet[i] = 0xa5
				}
			}
		}
		return metadata
	}

	reference := run(t, false)
	poisoned := run(t, true)

	require.Equal(t, C.ProtocolQUIC, poisoned.Protocol)
	require.Equal(t, "www.google.com", poisoned.Domain)
	require.Nil(t, poisoned.SniffContext, "the fragments were not released once the ClientHello was assembled")

	// The two runs must agree byte for byte on everything the sniff reports: that is what a saved
	// fragment aliasing the caller's buffer would break.
	require.Equal(t, reference.Protocol, poisoned.Protocol,
		"overwriting the first datagrams changed the protocol, so a fragment aliased the caller's buffer")
	require.Equal(t, reference.Domain, poisoned.Domain,
		"overwriting the first datagrams changed the domain, so a fragment aliased the caller's buffer")
	require.Equal(t, reference.Client, poisoned.Client,
		"overwriting the first datagrams changed the client, so a fragment aliased the caller's buffer")

	// The reported classification, recorded rather than asserted as the truth: the frame sequence a
	// client is classified by is read from the packet being decrypted, and a ClientHello split
	// across three Initial packets carries one CRYPTO frame per packet, so every round sees the
	// single-frame shape that is classified as Firefox. See the report for this finding; it is NOT
	// an ownership problem - the control run above produces the same value - and the fix is a
	// SniffContext change, which this round is not allowed to make.
	t.Logf("MEASURED: three-fragment reassembly of the Chrome 115 ClientHello reports client %q "+
		"(the un-poisoned control reports %q, so this is not caused by the caller reusing its buffer)",
		poisoned.Client, reference.Client)
	require.NotEmpty(t, poisoned.Client)
}

// fragmentPayloadRanges reads the payload slice of every qCryptoFragment metadata.SniffContext holds
// as an address range, without naming the unexported type: the field is reached by name and its
// slice header is read through reflect, so this test would keep compiling if the fragment type were
// reshaped, and would fail loudly rather than silently if the field were renamed.
func fragmentPayloadRanges(t *testing.T, sniffContext any) [][2]uintptr {
	t.Helper()
	require.NotNil(t, sniffContext, "no fragments were kept")
	value := reflect.ValueOf(sniffContext)
	require.Equal(t, reflect.Slice, value.Kind(), "SniffContext holds %T", sniffContext)
	ranges := make([][2]uintptr, 0, value.Len())
	for index := 0; index < value.Len(); index++ {
		payload := value.Index(index).FieldByName("payload")
		require.True(t, payload.IsValid(), "the fragment type has no payload field")
		require.Equal(t, reflect.Slice, payload.Kind())
		base := payload.Pointer()
		ranges = append(ranges, [2]uintptr{base, base + uintptr(payload.Len())})
	}
	return ranges
}

// TestQUICSniffContextFragmentsDoNotAliasTheDatagram is the ownership proof stated as an address
// comparison rather than as a behavioural one. The behavioural proof beside it (poisoning the
// datagram and comparing the verdict) can only show that the bytes survived; this one shows why:
// every saved fragment payload has to lie OUTSIDE the caller's datagram, so there is nothing left
// to alias when the caller reuses or releases that buffer.
//
// It is the assertion the task asks for in the negative - "check that the saved fragment does not
// alias the caller's buffer (i.e. it was copied, not retained by reference)" - and it is written so
// that it fails, rather than passes, if the fragment is ever made a view of the datagram again:
// quic.go copies the one packet out of the datagram into `newPacket` and decrypts in place there,
// and the fragments point into that copy.
func TestQUICSniffContextFragmentsDoNotAliasTheDatagram(t *testing.T) {
	t.Parallel()
	hello := quicChromeClientHello
	const split = 100
	packet := buildInitialPacket(t, quicPacketSpec{
		version:      qtls.Version1,
		dcid:         []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88},
		packetNumLen: 1,
		plaintext:    quicCryptoSegment(hello[:split], 0),
	})

	var metadata adapter.InboundContext
	require.ErrorIs(t, sniff.QUICClientHello(context.Background(), &metadata, packet), sniff.ErrNeedMoreData)
	ranges := fragmentPayloadRanges(t, metadata.SniffContext)
	require.Len(t, ranges, 1, "the first packet carried one CRYPTO frame")

	datagramBase := reflect.ValueOf(packet).Pointer()
	datagramEnd := datagramBase + uintptr(len(packet))

	// Non-vacuity: the predicate has to flag a slice that really is a view of the caller's datagram.
	// The datagram itself is that slice, so it is the control this assertion is calibrated against -
	// a predicate that could not see this one would pass for any address at all.
	require.True(t, payloadOverlapsDatagram([2]uintptr{datagramBase, datagramEnd}, datagramBase, datagramEnd),
		"the aliasing predicate cannot see the caller's own datagram, so its answer for the fragments means nothing")
	separate := make([]byte, 16)
	separateBase := reflect.ValueOf(separate).Pointer()
	require.False(t, payloadOverlapsDatagram([2]uintptr{separateBase, separateBase + uintptr(len(separate))}, datagramBase, datagramEnd),
		"the aliasing predicate reports an unrelated allocation as an alias")

	for index, payload := range ranges {
		require.NotZero(t, payload[0], "fragment %d has an empty payload", index)
		require.False(t, payloadOverlapsDatagram(payload, datagramBase, datagramEnd),
			"fragment %d overlaps the caller's datagram: [%#x, %#x) against [%#x, %#x). The fragment is "+
				"a view of the caller's buffer, so releasing that buffer leaves the sniff reading freed "+
				"memory", index, payload[0], payload[1], datagramBase, datagramEnd)
	}
}

// payloadOverlapsDatagram reports whether an address range intersects [base, end).
func payloadOverlapsDatagram(payload [2]uintptr, base, end uintptr) bool {
	return payload[0] < end && payload[1] > base
}
