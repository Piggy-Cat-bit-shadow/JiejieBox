package sniff_test

// Packet-boundary tests for the QUIC Initial sniffer.
//
// A UDP datagram may carry several coalesced QUIC packets. This sniffer reads only the first one, and
// the Length field of that first packet is what says where it stops: an Initial's Length covers the
// packet number, the payload and the authentication tag, so the packet ends at hdrLen+packetLen and
// not at the end of the datagram. The cases below pin that boundary - the exact end, a coalesced
// suffix, a truncated packet, a Length too small to hold the packet number field - and pin that
// nothing the sniffer reports outlives the datagram it was read from.
//
// Packets are built rather than pasted as hex where the point of the case is a header detail
// (version, token length, connection id length), in the same spirit as buildSTUNMessage and
// wireGuardMessage in sniff_bench_test.go. The frames are always the ones a real captured Chrome 115
// Initial carried - see quicChromeClientHello - so the ClientHello, the server name and the frame
// sequence are real bytes rather than a paraphrase of them.

import (
	"context"
	"crypto"
	"crypto/aes"
	"encoding/binary"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/sniff"
	"github.com/sagernet/sing-box/common/sniff/internal/qtls"
	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/hkdf"
)

// quicChromeClientHello is the ClientHello the CRYPTO frames of the captured Chrome 115 Initial
// (quicInitial, in sniff_bench_test.go) assemble into: a TLS handshake message without the record
// layer QUIC does not use.
var quicChromeClientHello = mustHex(
	"0100012103032d6230d61e1a6d2fdee41187f1fb5d195979a277ebd4c11cdec960b038a52220000006130113021303010000" +
		"f2446900050003026833002d00020101000a00080006001d00170018002b000302030400000013001100000e7777772e676f" +
		"6f676c652e636f6d003300260024001d00209ee4b35b53cb433deeafaff0832a8d31ede2f5ecad9d2a856eb266da486d022f" +
		"00390063070480600000010480007530f4701b2d76d7c24107724326beed6de8050480600000080240647128045256434d20" +
		"04800100000f0080004752040000000180ff73db0c000000015e0e0a4b00000001060480600000030245c009024067040480" +
		"f00000001b0003020002000d00140012040308040401050308050501080606010201001000050003026833")

// quicFrames is the frame sequence of that capture, in the order the frames appear in its plaintext.
// Chrome emits its CRYPTO frames out of offset order, which is why the assembly is by offset and not
// by arrival, and why the sequence is kept exactly as captured.
var quicFrames = []struct {
	offset  uint64
	payload []byte
}{
	{174, quicChromeClientHello[174:259]},
	{89, quicChromeClientHello[89:174]},
	{0, quicChromeClientHello[0:89]},
	{259, quicChromeClientHello[259:269]},
	{269, quicChromeClientHello[269:271]},
	{271, quicChromeClientHello[271:278]},
	{278, quicChromeClientHello[278:293]},
}

// quicInitialPlaintext rebuilds the plaintext of that capture from its frame sequence: sixteen
// PADDING frames, then the CRYPTO frames in capture order. The capture's trailing padding, which a
// real Initial carries only to reach the anti-amplification limit, is left out: the frame sequence
// the client classifier reads is the same without it.
func quicInitialPlaintext() []byte {
	plaintext := make([]byte, 16)
	for _, frame := range quicFrames {
		plaintext = append(plaintext, 0x06)
		plaintext = appendQUICVarint(plaintext, frame.offset)
		plaintext = appendQUICVarint(plaintext, uint64(len(frame.payload)))
		plaintext = append(plaintext, frame.payload...)
	}
	return plaintext
}

func appendQUICVarint(b []byte, value uint64) []byte {
	switch {
	case value < 1<<6:
		return append(b, byte(value))
	case value < 1<<14:
		return append(b, 0x40|byte(value>>8), byte(value))
	default:
		panic("test varint is larger than the values used here")
	}
}

// quicCryptoSegment is one CRYPTO frame carrying part of a handshake message at an offset.
func quicCryptoSegment(payload []byte, offset uint64) []byte {
	plaintext := []byte{0x06}
	plaintext = appendQUICVarint(plaintext, offset)
	plaintext = appendQUICVarint(plaintext, uint64(len(payload)))
	return append(plaintext, payload...)
}

type quicPacketSpec struct {
	version      uint32
	dcid         []byte
	scid         []byte
	token        []byte
	packetNum    uint64
	packetNumLen int
	plaintext    []byte
}

// buildInitialPacket seals one Initial the way a client does: derive the Initial secrets from the
// destination connection id and the version, seal the frames with the header as associated data,
// then apply header protection to the first byte and the packet number field.
func buildInitialPacket(tb testing.TB, spec quicPacketSpec) []byte {
	tb.Helper()
	require.NotEmpty(tb, spec.dcid)
	require.LessOrEqual(tb, len(spec.dcid), 20)
	require.LessOrEqual(tb, len(spec.scid), 20)
	require.GreaterOrEqual(tb, len(spec.plaintext), 20, "header protection samples twenty bytes past the packet number field")
	salt := qtls.SaltOld
	if spec.version == qtls.Version1 {
		salt = qtls.SaltV1
	} else if spec.version == qtls.Version2 {
		salt = qtls.SaltV2
	}
	hpLabel, keyLabel, ivLabel := qtls.HKDFLabelHeaderProtectionV1, qtls.HKDFLabelKeyV1, qtls.HKDFLabelIVV1
	if spec.version == qtls.Version2 {
		hpLabel, keyLabel, ivLabel = qtls.HKDFLabelHeaderProtectionV2, qtls.HKDFLabelKeyV2, qtls.HKDFLabelIVV2
	}
	initialSecret := hkdf.Extract(crypto.SHA256.New, spec.dcid, salt)
	secret := qtls.HKDFExpandLabel(crypto.SHA256, initialSecret, []byte{}, "client in", crypto.SHA256.Size())
	hpKey := qtls.HKDFExpandLabel(crypto.SHA256, secret, []byte{}, hpLabel, 16)
	key := qtls.HKDFExpandLabel(crypto.SHA256, secret, []byte{}, keyLabel, 16)
	iv := qtls.HKDFExpandLabel(crypto.SHA256, secret, []byte{}, ivLabel, 12)

	// Long header form, fixed bit, the packet type bits (0b00 in v1 and Draft29, 0b01 in v2) and the
	// packet number length in the two low bits, which header protection then masks.
	firstByte := byte(0xc0) | byte(spec.packetNumLen-1)
	if spec.version == qtls.Version2 {
		firstByte |= 0x10
	}
	header := []byte{firstByte}
	header = binary.BigEndian.AppendUint32(header, spec.version)
	header = append(header, byte(len(spec.dcid)))
	header = append(header, spec.dcid...)
	header = append(header, byte(len(spec.scid)))
	header = append(header, spec.scid...)
	header = appendQUICVarint(header, uint64(len(spec.token)))
	header = append(header, spec.token...)
	// The Length field covers the packet number, the payload and the sixteen byte tag.
	header = appendQUICVarint(header, uint64(spec.packetNumLen+len(spec.plaintext)+16))
	packetNumberField := make([]byte, 4)
	binary.BigEndian.PutUint32(packetNumberField, uint32(spec.packetNum))
	// The packet number field carries the packet number's low order bytes, most significant first.
	header = append(header, packetNumberField[4-spec.packetNumLen:]...)

	aead := qtls.AEADAESGCMTLS13(key, iv)
	nonce := make([]byte, aead.NonceSize())
	binary.BigEndian.PutUint64(nonce[len(nonce)-8:], spec.packetNum)
	packet := aead.Seal(header, nonce, spec.plaintext, header)

	pnOffset := len(header) - spec.packetNumLen
	block, err := aes.NewCipher(hpKey)
	require.NoError(tb, err)
	mask := make([]byte, aes.BlockSize)
	block.Encrypt(mask, packet[pnOffset+4:pnOffset+4+aes.BlockSize])
	packet[0] ^= mask[0] & 0xf
	// Only the packet number field itself is masked. The sniffer masks all four bytes before it can
	// read the length out of the first byte and then puts the surplus back from the datagram it was
	// handed (`copy(newPacket[extHdrLen:hdrLen+4], packet[extHdrLen:])`), which is why the bytes past
	// the field survive in the packet on the wire. A line number is not quoted here on purpose: the
	// line moved when the bounded copy landed, and a stale reference is worse than none.
	for i := range spec.packetNumLen {
		packet[pnOffset+i] ^= mask[i+1]
	}
	return packet
}

// quicSecondPacket is a well-formed 0-RTT long-header packet: the shape a resuming client coalesces
// behind its Initial, with its own connection ids, Length field, packet number and tag.
func quicSecondPacket(payloadLen int) []byte {
	body := 4 + payloadLen + 16
	packet := []byte{0xd3}
	packet = binary.BigEndian.AppendUint32(packet, qtls.Version1)
	packet = append(packet, 4, 0xde, 0xad, 0xbe, 0xef, 0)
	packet = append(packet, 0x40|byte(body>>8), byte(body))
	packet = append(packet, 0x11, 0x22, 0x33, 0x44)
	for i := range payloadLen {
		packet = append(packet, byte(i*7+1))
	}
	return append(packet, make([]byte, 16)...)
}

// TestQUICInitialPacketBoundary walks the datagram-versus-packet boundary. The captured Initial ends
// exactly where its own Length says; everything after that is either a second packet the sniffer has
// to ignore untouched or a truncation it has to refuse rather than half read.
func TestQUICInitialPacketBoundary(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name       string
		datagram   func() []byte
		wantError  bool
		wantDomain string
		wantSuffix []byte
	}{
		{
			name:       "captured Initial, the packet end is the datagram end",
			datagram:   func() []byte { return append([]byte{}, quicInitial...) },
			wantDomain: "www.google.com",
		},
		{
			name: "coalesced second packet",
			datagram: func() []byte {
				return append(append([]byte{}, quicInitial...), quicSecondPacket(100)...)
			},
			wantDomain: "www.google.com",
			wantSuffix: quicSecondPacket(100),
		},
		{
			name: "coalesced second packet larger than the first",
			datagram: func() []byte {
				return append(append([]byte{}, quicInitial...), quicSecondPacket(1200)...)
			},
			wantDomain: "www.google.com",
			wantSuffix: quicSecondPacket(1200),
		},
		{
			name: "one trailing byte that is not a packet",
			datagram: func() []byte {
				return append(append([]byte{}, quicInitial...), 0x00)
			},
			wantDomain: "www.google.com",
			wantSuffix: []byte{0x00},
		},
		{
			name:      "truncated by one byte",
			datagram:  func() []byte { return append([]byte{}, quicInitial[:len(quicInitial)-1]...) },
			wantError: true,
		},
		{
			name:      "truncated inside the header",
			datagram:  func() []byte { return append([]byte{}, quicInitial[:10]...) },
			wantError: true,
		},
		{
			name:      "truncated at the Length field",
			datagram:  func() []byte { return append([]byte{}, quicInitial[:17]...) },
			wantError: true,
		},
		{
			name: "length too small to hold the packet number field",
			datagram: func() []byte {
				// A long-header v1 Initial whose Length field says three bytes: not enough for the
				// four bytes header protection rewrites, in a datagram long enough for the sample.
				return append([]byte{
					0xc0, 0x00, 0x00, 0x00, 0x01, // long header, v1
					0x01, 0xaa, // destination connection id
					0x00, // source connection id
					0x00, // token length
					0x03, // Length
				}, make([]byte, 32)...)
			},
			wantError: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			datagram := testCase.datagram()
			before := append([]byte{}, datagram...)
			var metadata adapter.InboundContext
			err := sniff.QUICClientHello(context.Background(), &metadata, datagram)
			if testCase.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, C.ProtocolQUIC, metadata.Protocol)
				require.Equal(t, testCase.wantDomain, metadata.Domain)
			}
			require.Equal(t, before, datagram, "the sniffer wrote into the datagram")
			if testCase.wantSuffix != nil {
				require.Equal(t, testCase.wantSuffix, datagram[len(quicInitial):], "the sniffer rewrote the bytes after the first packet")
			}
		})
	}
}

// TestQUICInitialHeaderVariants keeps every version and header shape the parser accepts working. v1,
// v2 and Draft29 differ in their salts, their key labels and their packet type bits, and the token
// and connection id lengths move every field that follows them, including the offset the Length
// field - and so the packet boundary - is read from.
func TestQUICInitialHeaderVariants(t *testing.T) {
	t.Parallel()
	plaintext := quicInitialPlaintext()
	for _, testCase := range []struct {
		name string
		spec quicPacketSpec
	}{
		{"v1", quicPacketSpec{version: qtls.Version1, dcid: []byte{1, 2, 3, 4, 5, 6, 7, 8}, packetNumLen: 1}},
		{"v2", quicPacketSpec{version: qtls.Version2, dcid: []byte{1, 2, 3, 4, 5, 6, 7, 8}, packetNumLen: 1}},
		{"draft29", quicPacketSpec{version: qtls.VersionDraft29, dcid: []byte{1, 2, 3, 4, 5, 6, 7, 8}, packetNumLen: 1}},
		{"v1 with a four byte packet number", quicPacketSpec{version: qtls.Version1, dcid: []byte{1, 2, 3, 4, 5, 6, 7, 8}, packetNum: 0x01020304, packetNumLen: 4}},
		{"v1 with a one byte destination connection id", quicPacketSpec{version: qtls.Version1, dcid: []byte{9}, packetNumLen: 1}},
		{"v1 with a twenty byte destination connection id", quicPacketSpec{version: qtls.Version1, dcid: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}, packetNumLen: 1}},
		{"v1 with a source connection id", quicPacketSpec{version: qtls.Version1, dcid: []byte{1, 2, 3, 4, 5, 6, 7, 8}, scid: []byte{1, 2, 3, 4}, packetNumLen: 1}},
		{"v1 with a token", quicPacketSpec{version: qtls.Version1, dcid: []byte{1, 2, 3, 4, 5, 6, 7, 8}, token: make([]byte, 32), packetNumLen: 1}},
		{"v1 with a long token", quicPacketSpec{version: qtls.Version1, dcid: []byte{1, 2, 3, 4, 5, 6, 7, 8}, token: make([]byte, 300), packetNumLen: 1}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			spec := testCase.spec
			spec.plaintext = plaintext
			packet := buildInitialPacket(t, spec)
			before := append([]byte{}, packet...)
			var metadata adapter.InboundContext
			require.NoError(t, sniff.QUICClientHello(context.Background(), &metadata, packet))
			require.Equal(t, C.ProtocolQUIC, metadata.Protocol)
			require.Equal(t, "www.google.com", metadata.Domain)
			require.Equal(t, C.ClientChromium, metadata.Client)
			require.Equal(t, before, packet, "the sniffer wrote into the datagram")

			// The same packet with a well-formed second packet behind it: same verdict, same bytes.
			datagram := append(append([]byte{}, packet...), quicSecondPacket(100)...)
			suffix := append([]byte{}, datagram[len(packet):]...)
			var coalescedMetadata adapter.InboundContext
			require.NoError(t, sniff.QUICClientHello(context.Background(), &coalescedMetadata, datagram))
			require.Equal(t, metadata.Protocol, coalescedMetadata.Protocol)
			require.Equal(t, metadata.Domain, coalescedMetadata.Domain)
			require.Equal(t, metadata.Client, coalescedMetadata.Client)
			require.Equal(t, suffix, datagram[len(packet):], "the sniffer rewrote the second packet")

			// Truncated by one byte the packet is refused rather than half read, suffix or not.
			require.Error(t, sniff.QUICClientHello(context.Background(), &adapter.InboundContext{}, packet[:len(packet)-1]))
		})
	}
}

// TestQUICInitialRejectsMalformedHeaders is the error side of the same parse: a datagram that cannot
// be an Initial is rejected with an error, and never with a slice panic.
func TestQUICInitialRejectsMalformedHeaders(t *testing.T) {
	t.Parallel()
	plaintext := quicInitialPlaintext()
	valid := buildInitialPacket(t, quicPacketSpec{version: qtls.Version1, dcid: []byte{1, 2, 3, 4, 5, 6, 7, 8}, packetNumLen: 1, plaintext: plaintext})
	// With a one byte packet number field and no token the Length varint sits at the end of an
	// eighteen byte header, which is the offset the parser computes for it.
	require.Equal(t, 18, len(valid)-1-len(plaintext)-16)
	for _, testCase := range []struct {
		name     string
		datagram []byte
	}{
		{"empty", nil},
		{"one byte", []byte{0xc0}},
		{"fixed bit clear", append([]byte{0x80}, valid[1:]...)},
		{"unknown version", append(append(append([]byte{}, valid[:1]...), 0x00, 0x00, 0x00, 0x02), valid[5:]...)},
		{"zero destination connection id length", append(append(append([]byte{}, valid[:5]...), 0x00), valid[7:]...)},
		{"destination connection id length too long", append(append(append([]byte{}, valid[:5]...), 0x15), valid[7:]...)},
		{"length field truncated", append([]byte{}, valid[:17]...)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			datagram := append([]byte{}, testCase.datagram...)
			before := append([]byte{}, datagram...)
			require.Error(t, sniff.QUICClientHello(context.Background(), &adapter.InboundContext{}, datagram))
			require.Equal(t, before, datagram, "the sniffer wrote into the datagram")
		})
	}
}

// FuzzQUICPacketBoundary drives the datagram-versus-packet boundary: a fuzzer-chosen packet
// followed by a fuzzer-chosen suffix, which is what a coalesced datagram is. The invariants are the
// ones the caller depends on - the datagram comes back byte for byte, and a verdict is either an
// error or a QUIC classification - plus the panic the boundary must never produce.
func FuzzQUICPacketBoundary(f *testing.F) {
	f.Add(quicInitial, []byte{}, false)
	f.Add(quicInitial, quicSecondPacket(100), true)
	f.Add(quicInitial, []byte{0x00}, true)
	f.Add(quicInitial, quicSecondPacket(1200), true)
	f.Add(quicInitial[:len(quicInitial)-1], []byte{}, false)
	f.Add(quicInitial[:10], quicSecondPacket(4), true)
	f.Fuzz(func(t *testing.T, packet []byte, suffix []byte, coalesce bool) {
		datagram := append([]byte{}, packet...)
		if coalesce {
			datagram = append(datagram, suffix...)
		}
		before := append([]byte{}, datagram...)
		var metadata adapter.InboundContext
		err := sniff.QUICClientHello(context.Background(), &metadata, datagram)
		require.Equal(t, before, datagram, "the sniffer wrote into the datagram")
		if err == nil {
			require.Equal(t, C.ProtocolQUIC, metadata.Protocol)
			require.NotEmpty(t, metadata.Domain)
		}
	})
}

// FuzzQUICPacketLengthBoundary drives the Length field itself. The packet is a sealed Initial whose
// two Length bytes and datagram size the fuzzer controls, so packetLen crosses the exact end, a
// coalesced suffix, a truncation and a Length too small to hold the packet number field. A packet
// number field of one byte means the Length varint sits at offset 16, which is what the seed fixes.
func FuzzQUICPacketLengthBoundary(f *testing.F) {
	base := buildInitialPacket(f, quicPacketSpec{
		version:      qtls.Version1,
		dcid:         []byte{1, 2, 3, 4, 5, 6, 7, 8},
		packetNumLen: 1,
		plaintext:    quicInitialPlaintext(),
	})
	f.Add(base, uint16(len(base)-18), uint8(0))
	f.Add(base, uint16(0), uint8(1))
	f.Add(base, uint16(1), uint8(2))
	f.Add(base, uint16(3), uint8(3))
	f.Add(base, uint16(4), uint8(16))
	f.Add(base, uint16(0x3fff), uint8(0))
	f.Fuzz(func(t *testing.T, packet []byte, length uint16, extra uint8) {
		if len(packet) < 18 {
			return
		}
		datagram := append([]byte{}, packet...)
		// The Length field is a two byte varint here: 0b01 in the top two bits, fourteen bits of value.
		datagram[16] = 0x40 | byte(length>>8)&0x3f
		datagram[17] = byte(length)
		datagram = append(datagram, make([]byte, extra)...)
		before := append([]byte{}, datagram...)
		var metadata adapter.InboundContext
		err := sniff.QUICClientHello(context.Background(), &metadata, datagram)
		require.Equal(t, before, datagram, "the sniffer wrote into the datagram")
		if err == nil {
			require.Equal(t, C.ProtocolQUIC, metadata.Protocol)
		}
	})
}

// TestQUICInitialFragmentOutlivesTheDatagram is the ownership proof for metadata.SniffContext. A
// fragmented ClientHello is sniffed one datagram at a time and the fragments the first round keeps
// are what the second round assembles; the router releases the datagram it read them from as soon as
// the sniff returns, so a fragment has to be a copy and not a view of the caller's buffer. Here the
// first datagram is overwritten completely before the second round runs.
func TestQUICInitialFragmentOutlivesTheDatagram(t *testing.T) {
	t.Parallel()
	hello := quicChromeClientHello
	require.Len(t, hello, 293)
	const split = 100

	first := buildInitialPacket(t, quicPacketSpec{
		version:      qtls.Version1,
		dcid:         []byte{1, 2, 3, 4, 5, 6, 7, 8},
		packetNumLen: 1,
		plaintext:    quicCryptoSegment(hello[:split], 0),
	})
	var metadata adapter.InboundContext
	require.ErrorIs(t, sniff.QUICClientHello(context.Background(), &metadata, first), sniff.ErrNeedMoreData)
	require.NotNil(t, metadata.SniffContext, "the first round should have kept its CRYPTO fragment")

	// The caller reuses its datagram buffer as soon as the sniff returns.
	for i := range first {
		first[i] = 0x00
	}

	second := buildInitialPacket(t, quicPacketSpec{
		version:      qtls.Version1,
		dcid:         []byte{1, 2, 3, 4, 5, 6, 7, 8},
		packetNumLen: 1,
		plaintext:    quicCryptoSegment(hello[split:], split),
	})
	require.NoError(t, sniff.QUICClientHello(context.Background(), &metadata, second))
	require.Equal(t, "www.google.com", metadata.Domain, "the fragments of the first datagram did not survive it")
}
