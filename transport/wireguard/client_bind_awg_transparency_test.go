package wireguard

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/wireguard-go/conn"

	"github.com/stretchr/testify/require"
)

// The two bind modes differ in exactly one place: whether the three bytes after
// the message type are Reserved bytes (WARP-style peers rewrite them) or
// AmneziaWG header-protection bytes (they must pass through untouched).
//
// The tests drive the real socket path of ClientBind: a local UDP socket plays
// the peer, so a rewrite on either direction is observed on the wire.

func TestClientBindRewritesReservedBytes(t *testing.T) {
	far, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	defer far.Close()
	dialer := newSocketDialer(t, far.LocalAddr().(*net.UDPAddr))
	destination := remoteEndpoint(netip.MustParseAddrPort("192.0.2.1:51820"))
	bind := NewClientBind(pauseContext(), log.NewNOPFactory().Logger(), dialer, true,
		netip.MustParseAddrPort("192.0.2.1:51820"), [3]uint8{0xaa, 0xbb, 0xcc})
	t.Cleanup(func() { _ = bind.Close() })

	payload := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	require.NoError(t, bind.Send([][]byte{append([]byte(nil), payload...)}, destination, 0))
	received := make([]byte, 64)
	require.NoError(t, far.SetReadDeadline(time.Now().Add(2*time.Second)))
	count, address, err := far.ReadFromUDP(received)
	require.NoError(t, err)
	require.Equal(t, []byte{0x01, 0xaa, 0xbb, 0xcc, 0x05, 0x06}, received[:count],
		"a WARP-style bind must write the reserved bytes into every packet")

	_, err = far.WriteToUDP([]byte{0x01, 0x11, 0x22, 0x33, 0x05}, address)
	require.NoError(t, err)
	packets := [][]byte{make([]byte, 128)}
	sizes := make([]int, 1)
	endpoints := make([]conn.Endpoint, 1)
	count, receiveErr := bind.receive(packets, sizes, endpoints)
	require.NoError(t, receiveErr)
	require.Equal(t, 1, count)
	require.Equal(t, []byte{0x01, 0x00, 0x00, 0x00, 0x05}, packets[0][:sizes[0]],
		"the received reserved bytes must be cleared before wireguard-go sees them")
}

func TestClientBindAWGTransparentHeader(t *testing.T) {
	far, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	defer far.Close()
	dialer := newSocketDialer(t, far.LocalAddr().(*net.UDPAddr))
	destination := remoteEndpoint(netip.MustParseAddrPort("192.0.2.1:51820"))
	bind := NewClientBind(pauseContext(), log.NewNOPFactory().Logger(), dialer, true,
		netip.MustParseAddrPort("192.0.2.1:51820"), [3]uint8{0xaa, 0xbb, 0xcc})
	bind.SetAWGTransparent(true)
	t.Cleanup(func() { _ = bind.Close() })

	payload := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	require.NoError(t, bind.Send([][]byte{append([]byte(nil), payload...)}, destination, 0))
	received := make([]byte, 64)
	require.NoError(t, far.SetReadDeadline(time.Now().Add(2*time.Second)))
	count, address, err := far.ReadFromUDP(received)
	require.NoError(t, err)
	require.Equal(t, payload, received[:count],
		"an AmneziaWG bind must not rewrite the protected header bytes")

	reply := []byte{0x01, 0x11, 0x22, 0x33, 0x05}
	_, err = far.WriteToUDP(reply, address)
	require.NoError(t, err)
	packets := [][]byte{make([]byte, 128)}
	sizes := make([]int, 1)
	endpoints := make([]conn.Endpoint, 1)
	count, receiveErr := bind.receive(packets, sizes, endpoints)
	require.NoError(t, receiveErr)
	require.Equal(t, 1, count)
	require.Equal(t, reply, packets[0][:sizes[0]],
		"a received AmneziaWG packet must reach wireguard-go with its protected header intact")
}
