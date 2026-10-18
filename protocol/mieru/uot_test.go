package mieru

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"

	mierucommon "github.com/enfein/mieru/v3/apis/common"
	mierumodel "github.com/enfein/mieru/v3/apis/model"

	"github.com/stretchr/testify/require"
)

func testDestinations() []M.Socksaddr {
	return []M.Socksaddr{
		M.SocksaddrFrom(netip.MustParseAddr("192.0.2.7"), 53),
		M.SocksaddrFrom(netip.MustParseAddr("2001:db8::1"), 443),
		{Fqdn: "example.com", Port: 80},
	}
}

// TestSocks5AddressCodecMatchesSDK pins the datagram address encoding to the
// SDK's model.AddrSpec. If the SDK ever changes its SOCKS5 encoding, this test
// fails rather than the wire silently diverging.
func TestSocks5AddressCodecMatchesSDK(t *testing.T) {
	for _, destination := range testDestinations() {
		t.Run(destination.String(), func(t *testing.T) {
			var ours [1 + 255 + 2]byte
			length, err := encodeSocks5Address(ours[:], destination)
			require.NoError(t, err)

			sdkAddr := mierumodel.AddrSpec{
				FQDN: destination.Fqdn,
				Port: int(destination.Port),
			}
			if destination.IsIP() {
				sdkAddr.IP = net.IP(destination.Addr.AsSlice())
			}
			var sdk bytes.Buffer
			require.NoError(t, sdkAddr.WriteToSocks5(&sdk))
			require.Equal(t, sdk.Bytes(), ours[:length])

			decoded, decodedLength, err := readSocks5Address(bytes.NewReader(sdk.Bytes()))
			require.NoError(t, err)
			require.Equal(t, length, decodedLength)
			require.Equal(t, destination, decoded)
		})
	}
}

// TestMaxFQDNAddressEncoding covers the SOCKS5 FQDN boundary. The presentation
// form sing-box accepts is at most 253 bytes; 254/255 are rejected before the
// encoder, and a 253-byte name needs a 257-byte SOCKS5 address (ATYP + length +
// name + port), so the 258-byte scratch buffer is exactly sufficient.
func TestMaxFQDNAddressEncoding(t *testing.T) {
	tooLong := M.Socksaddr{Fqdn: strings.Repeat("a", 255), Port: 65535}
	var rejected [1 + 255 + 2]byte
	_, err := encodeSocks5Address(rejected[:], tooLong)
	require.Error(t, err)

	destination := M.Socksaddr{Fqdn: strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61), Port: 65535}
	var encoded [1 + 255 + 2]byte
	var length int
	require.NotPanics(t, func() {
		var encodeErr error
		length, encodeErr = encodeSocks5Address(encoded[:], destination)
		require.NoError(t, encodeErr)
	})
	require.Equal(t, 257, length)
	decoded, decodedLength, err := readSocks5Address(bytes.NewReader(encoded[:length]))
	require.NoError(t, err)
	require.Equal(t, length, decodedLength)
	require.Equal(t, destination, decoded)
}

// TestWriteFrameIsReadableBySDK proves the writer is wire compatible with the
// official stream frame + UDP-associate wrapper.
func TestWriteFrameIsReadableBySDK(t *testing.T) {
	for _, destination := range testDestinations() {
		if destination.IsFqdn() {
			// The SDK's UDPAssociateWrapper documents FQDN in the peer header
			// as unsupported on read; frame-level coverage is below.
			continue
		}
		t.Run(destination.String(), func(t *testing.T) {
			payload := []byte("the quick brown fox")
			clientConn, serverConn := net.Pipe()
			defer serverConn.Close()
			writerDone := make(chan error, 1)
			go func() {
				err := writeUOTFrame(clientConn, payload, destination)
				_ = clientConn.Close()
				writerDone <- err
			}()
			tunnel := mierucommon.NewUDPAssociateWrapper(mierucommon.NewPacketOverStreamTunnel(serverConn))
			received := make([]byte, 64)
			n, addr, err := tunnel.ReadFrom(received)
			require.NoError(t, err)
			require.Equal(t, payload, received[:n])
			require.Equal(t, destination, M.SocksaddrFromNet(addr))
			require.NoError(t, <-writerDone)
		})
	}
}

// TestReadFrameFromSDK is the reverse direction of the previous test.
func TestReadFrameFromSDK(t *testing.T) {
	for _, destination := range testDestinations() {
		if destination.IsFqdn() {
			continue
		}
		t.Run(destination.String(), func(t *testing.T) {
			payload := []byte("directional conformance")
			clientConn, serverConn := net.Pipe()
			defer serverConn.Close()
			go func() {
				tunnel := mierucommon.NewUDPAssociateWrapper(mierucommon.NewPacketOverStreamTunnel(clientConn))
				_, _ = tunnel.WriteTo(payload, destination.UDPAddr())
				_ = clientConn.Close()
			}()
			received := make([]byte, 64)
			n, decoded, err := readUOTFrame(serverConn, received)
			require.NoError(t, err)
			require.Equal(t, payload, received[:n])
			require.Equal(t, destination, decoded)
		})
	}
}

// TestTruncatedAddressCannotConsumeTheNextFrame pins the bounded address
// parse. A frame whose declared length cannot hold its address type must fail
// without reading the suffix or the following frame; otherwise a malformed
// client could desynchronise the association.
func TestTruncatedAddressCannotConsumeTheNextFrame(t *testing.T) {
	// Frame 1 claims 3 data bytes (only the SOCKS5 RSV/FRAG bytes) and has a
	// suffix. It cannot contain an address type.
	broken := []byte{0x00, 0x00, 0x03, 0x00, 0x00, 0x00, 0xff}
	// Frame 2 is a valid packet.
	destination := M.SocksaddrFrom(netip.MustParseAddr("192.0.2.77"), 7)
	valid := &bytes.Buffer{}
	require.NoError(t, writeUOTFrame(valid, []byte("next"), destination))
	stream := bytes.NewReader(append(append([]byte{}, broken...), valid.Bytes()...))

	framer := newUOTFramer(stream)
	buffer := make([]byte, 64)
	_, _, err := framer.readFrame(buffer)
	require.Error(t, err)
	// The suffix of the broken frame is still the next byte: the parser did
	// not consume it as an address.
	suffix := make([]byte, 1)
	_, err = io.ReadFull(stream, suffix)
	require.NoError(t, err)
	require.Equal(t, []byte{0xff}, suffix)

	n, decoded, err := framer.readFrame(buffer)
	require.NoError(t, err)
	require.Equal(t, []byte("next"), buffer[:n])
	require.Equal(t, destination, decoded)
}

// TestTruncatedFixedAddressCannotConsumeSuffix covers the same boundary for a
// frame that names IPv4 but declares too few bytes after the address type.
func TestTruncatedFixedAddressCannotConsumeSuffix(t *testing.T) {
	// data length 6: RSV/FRAG + ATYP + two address bytes; an IPv4 address
	// needs seven bytes after the RSV/FRAG. The suffix follows.
	broken := []byte{0x00, 0x00, 0x06, 0x00, 0x00, 0x00, 0x01, 0xc0, 0x00, 0xff}
	stream := bytes.NewReader(broken)
	framer := newUOTFramer(stream)
	_, _, err := framer.readFrame(make([]byte, 64))
	require.Error(t, err)
	remaining, err := io.ReadAll(stream)
	require.NoError(t, err)
	require.Equal(t, []byte{0xff}, remaining)
}

// TestFrameSequenceStaysAligned sends several frames through both encoders
// and checks that every read returns exactly its own packet.
func TestFrameSequenceStaysAligned(t *testing.T) {
	destination := M.SocksaddrFrom(netip.MustParseAddr("198.51.100.9"), 1234)
	payloads := [][]byte{[]byte("one"), []byte("two-two"), {}, bytes.Repeat([]byte("x"), 300)}
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	go func() {
		for index, payload := range payloads {
			var err error
			if index%2 == 0 {
				err = writeUOTFrame(clientConn, payload, destination)
			} else {
				tunnel := mierucommon.NewUDPAssociateWrapper(mierucommon.NewPacketOverStreamTunnel(clientConn))
				_, err = tunnel.WriteTo(payload, destination.UDPAddr())
			}
			if err != nil {
				t.Errorf("write %d: %v", index, err)
				break
			}
		}
		_ = clientConn.Close()
	}()
	for index, expected := range payloads {
		received := make([]byte, 1024)
		n, _, err := readUOTFrame(serverConn, received)
		require.NoError(t, err, "frame %d", index)
		require.Equal(t, expected, received[:n], "frame %d", index)
	}
}

// TestOversizedFrameIsDrained verifies that a frame larger than the caller's
// buffer advances the stream by the whole frame, so the next read is still
// aligned. This is the difference between a short buffer and a corrupt
// association.
func TestOversizedFrameIsDrained(t *testing.T) {
	destination := M.SocksaddrFrom(netip.MustParseAddr("203.0.113.5"), 9)
	large := bytes.Repeat([]byte{0xab}, 60000)
	small := []byte("after")
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	go func() {
		if err := writeUOTFrame(clientConn, large, destination); err != nil {
			t.Errorf("write large frame: %v", err)
		}
		if err := writeUOTFrame(clientConn, small, destination); err != nil {
			t.Errorf("write small frame: %v", err)
		}
		_ = clientConn.Close()
	}()
	shortBuffer := make([]byte, 16)
	_, _, err := readUOTFrame(serverConn, shortBuffer)
	require.ErrorIs(t, err, io.ErrShortBuffer)

	received := make([]byte, 16)
	n, _, err := readUOTFrame(serverConn, received)
	require.NoError(t, err)
	require.Equal(t, small, received[:n])
}

// TestOversizedFrameRejectsBadSuffix keeps the short-buffer path honest: a
// frame that is too large for the caller still has to end in 0xff. The old
// drain discarded one byte without checking it, so a malformed suffix was
// reported as a plain short buffer.
func TestOversizedFrameRejectsBadSuffix(t *testing.T) {
	destination := M.SocksaddrFrom(netip.MustParseAddr("203.0.113.6"), 10)
	var frame bytes.Buffer
	require.NoError(t, writeUOTFrame(&frame, bytes.Repeat([]byte{0xcd}, 256), destination))
	encoded := frame.Bytes()
	encoded[len(encoded)-1] = 0x00
	_, _, err := readUOTFrame(bytes.NewReader(encoded), make([]byte, 16))
	require.Error(t, err)
	require.NotErrorIs(t, err, io.ErrShortBuffer)
}

// TestFrameLengthEnvelope checks the exact bytes of a minimal frame, so a
// change in framing cannot hide behind two implementations that agree.
func TestFrameLengthEnvelope(t *testing.T) {
	destination := M.SocksaddrFrom(netip.MustParseAddr("192.0.2.1"), 0x1234)
	var encoded bytes.Buffer
	require.NoError(t, writeUOTFrame(&encoded, []byte("hi"), destination))
	frame := encoded.Bytes()
	require.Equal(t, byte(0x00), frame[0])
	dataLength := int(binary.BigEndian.Uint16(frame[1:3]))
	require.Equal(t, len(frame)-4, dataLength)
	require.Equal(t, []byte{0, 0, 0}, frame[3:6])
	require.Equal(t, byte(0x01), frame[6])
	require.Equal(t, []byte{192, 0, 2, 1}, frame[7:11])
	require.Equal(t, []byte{0x12, 0x34}, frame[11:13])
	require.Equal(t, []byte("hi"), frame[13:15])
	require.Equal(t, byte(0xff), frame[len(frame)-1])
}

// TestPacketConnRoundTrip exercises the N.PacketConn adapters themselves over
// a pipe, with the buf API the route layer uses.
// TestUOTPacketConnConcurrentReads exercises the net.PacketConn contract:
// concurrent ReadFrom calls must not share the framer's scratch buffers. Before
// the read mutex, two readers interleaved their header and address reads and
// the race detector also flagged the shared arrays.
func TestUOTPacketConnConcurrentReads(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	packetConn := newUOTPacketConn(clientConn)
	defer packetConn.Close()
	destination := M.SocksaddrFrom(netip.MustParseAddr("192.0.2.80"), 53)

	const readers = 8
	results := make(chan string, readers)
	var waitGroup sync.WaitGroup
	for index := 0; index < readers; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			require.NoError(t, packetConn.SetReadDeadline(time.Now().Add(5*time.Second)))
			buffer := make([]byte, 128)
			n, _, err := packetConn.ReadFrom(buffer)
			if err != nil {
				results <- "error: " + err.Error()
				return
			}
			results <- string(buffer[:n])
		}()
	}
	go func() {
		for index := 0; index < readers; index++ {
			if err := writeUOTFrame(serverConn, []byte(fmt.Sprintf("packet-%d", index)), destination); err != nil {
				return
			}
		}
	}()
	waitGroup.Wait()
	close(results)
	seen := make(map[string]bool, readers)
	for result := range results {
		seen[result] = true
	}
	for index := 0; index < readers; index++ {
		require.True(t, seen[fmt.Sprintf("packet-%d", index)], "missing %d, got %v", index, seen)
	}
}

func TestPacketConnRoundTrip(t *testing.T) {
	destination := M.SocksaddrFrom(netip.MustParseAddr("192.0.2.33"), 5353)
	clientConn, serverConn := net.Pipe()
	server := newInboundPacketConn(serverConn)
	client := newUOTPacketConn(clientConn)
	defer client.Close()
	defer server.Close()

	payload := []byte("packet-over-stream")
	writeDone := make(chan error, 1)
	go func() {
		_, err := client.WriteTo(payload, destination)
		writeDone <- err
	}()
	packet := buf.NewPacket()
	defer packet.Release()
	decoded, err := server.ReadPacket(packet)
	require.NoError(t, err)
	require.NoError(t, <-writeDone)
	require.Equal(t, payload, packet.Bytes())
	require.Equal(t, destination, decoded)

	// And the buf write path is what the reply uses.
	reply := buf.NewPacket()
	defer reply.Release()
	reply.WriteString("reply")
	readDone := make(chan error, 1)
	received := make([]byte, 64)
	var readLength int
	var readAddress net.Addr
	go func() {
		var readErr error
		readLength, readAddress, readErr = client.ReadFrom(received)
		readDone <- readErr
	}()
	require.NoError(t, server.WritePacket(reply, destination))
	require.NoError(t, <-readDone)
	require.Equal(t, len("reply"), readLength)
	require.Equal(t, "reply", string(received[:readLength]))
	require.Equal(t, destination, M.SocksaddrFromNet(readAddress))
}
