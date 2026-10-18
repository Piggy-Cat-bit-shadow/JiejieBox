package mieru

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"testing"

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
