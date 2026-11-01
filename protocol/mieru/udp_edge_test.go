package mieru

import (
	"context"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

// TestMieruEmptyUDPDatagramRoundTrip pins RFC 8085 zero-length datagrams
// through the fork's own UoT codec.
//
// The SDK's UDPAssociateWrapper turned an empty datagram into io.EOF and fixed
// that only after v3.39.0; this adapter does not use that wrapper, so the
// property has to be proven for protocol/mieru/uot.go directly. The frames must
// also stay aligned: a zero-length datagram followed by a normal one proves the
// empty frame did not consume the next header.
func TestMieruEmptyUDPDatagramRoundTrip(t *testing.T) {
	port := freePortForBothTransports(t)
	startTestInbound(t, inboundOptionsForPort(port, "tcp"))
	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(port, "tcp", "alice", "alice-password"))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("198.51.100.21:5353")
	packetConn, err := outboundInstance.ListenPacket(ctx, destination)
	require.NoError(t, err)
	defer packetConn.Close()

	require.NoError(t, packetConn.SetReadDeadline(time.Now().Add(10*time.Second)))

	written, err := packetConn.WriteTo(nil, destination)
	require.NoError(t, err, "an empty datagram is a valid UDP packet")
	require.Zero(t, written)

	buffer := make([]byte, 64)
	n, source, err := packetConn.ReadFrom(buffer)
	require.NoError(t, err, "the empty datagram must be delivered, not turned into EOF")
	require.Zero(t, n)
	require.Equal(t, destination, M.SocksaddrFromNet(source))

	// The frame after the empty one must still decode: framing stayed aligned.
	payload := []byte("after-empty")
	_, err = packetConn.WriteTo(payload, destination)
	require.NoError(t, err)
	n, _, err = packetConn.ReadFrom(buffer)
	require.NoError(t, err)
	require.Equal(t, payload, buffer[:n])
}
