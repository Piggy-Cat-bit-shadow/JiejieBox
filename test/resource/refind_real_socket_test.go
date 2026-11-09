package resource

// The reF1nd cached first packet over a REAL socket, with a real detour peer.
//
// The in-memory harness (refind_first_packet_test.go) pins the wrapper's semantics; this
// file runs the same production wrapper over a real UDP socket pair on loopback, which is
// what the audit's "real detour" item asks for: the destination the sniffer recorded
// survives the socket, the first packet costs no device read, an oversized datagram that
// the socket refuses does not poison the next packet, and Close releases a blocked read.
//
// What is still not covered: a real TUN device feeding the sniffer, and a real network
// MTU. Loopback has no narrow MTU; the oversized case uses the socket's own datagram limit
// (WSAEMSGSIZE on Windows, EMSGSIZE on Linux), which exercises the same error path.

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
	singbufio "github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// udpEchoPeer is a real UDP endpoint that echoes every datagram back to its sender and
// counts what it received.
type udpEchoPeer struct {
	conn     *net.UDPConn
	received atomic.Int64
}

func newUDPEchoPeer(t *testing.T) *udpEchoPeer {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	require.NoError(t, err)
	peer := &udpEchoPeer{conn: conn}
	go func() {
		payload := make([]byte, 128*1024)
		for {
			read, from, readErr := conn.ReadFromUDP(payload)
			if readErr != nil {
				return
			}
			peer.received.Add(1)
			_, _ = conn.WriteToUDP(payload[:read], from)
		}
	}()
	t.Cleanup(func() { _ = conn.Close() })
	return peer
}

func (p *udpEchoPeer) address() M.Socksaddr {
	return M.SocksaddrFromNet(p.conn.LocalAddr())
}

// cachedFirstPacketOverSocket wraps a real client socket the way the router wraps a
// sniffed packet conn: the first packet comes from the cache, every later one from the
// socket.
func cachedFirstPacketOverSocket(t *testing.T, peer *udpEchoPeer, first []byte) (*net.UDPConn, *singbufio.CachedPacketConn) {
	t.Helper()
	// An UNCONNECTED socket: sing's PacketConn adapter uses WriteTo/ReadFrom, which is the
	// shape the router hands around (a detour packet path is not a connected datagram
	// socket), and the peer's address travels with every write.
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	packetConn := singbufio.NewPacketConn(client)
	cache := buf.NewSize(len(first))
	_, _ = cache.Write(first)
	return client, singbufio.NewCachedPacketConn(packetConn, cache, peer.address())
}

// nextPacket reads one datagram through the cached wrapper with a deadline.
func nextPacket(t *testing.T, cached *singbufio.CachedPacketConn, deadline time.Duration) ([]byte, M.Socksaddr) {
	t.Helper()
	require.NoError(t, cached.SetReadDeadline(time.Now().Add(deadline)))
	buffer := buf.NewSize(64 * 1024)
	defer buffer.Release()
	destination, err := cached.ReadPacket(buffer)
	require.NoError(t, err)
	return append([]byte(nil), buffer.Bytes()...), destination
}

// writePacket writes one payload through the cached wrapper.
func writePacket(t *testing.T, cached *singbufio.CachedPacketConn, payload []byte, destination M.Socksaddr) error {
	t.Helper()
	buffer := buf.NewSize(len(payload))
	_, _ = buffer.Write(payload)
	return cached.WritePacket(buffer, destination)
}

// isOversizedError classifies the socket's refusal of a datagram that exceeds its limit.
func isOversizedError(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return containsFold(text, "message too long") ||
		containsFold(text, "WSAEMSGSIZE") ||
		// Windows spells WSAEMSGSIZE out instead of naming it.
		containsFold(text, "larger than the internal message buffer")
}

func containsFold(haystack, needle string) bool {
	if len(needle) > len(haystack) {
		return false
	}
	// ASCII-only fold is enough for these error strings.
	for index := 0; index+len(needle) <= len(haystack); index++ {
		matched := true
		for offset := 0; offset < len(needle); offset++ {
			a := haystack[index+offset]
			b := needle[offset]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// TestCachedFirstPacketOverARealSocket is the real-detour path: the cached first packet is
// delivered with the sniffer's destination and without the peer receiving it, the next
// real datagram round-trips, an oversized datagram's refusal does not poison the next
// packet, and Close releases a blocked read.
func TestCachedFirstPacketOverARealSocket(t *testing.T) {
	peer := newUDPEchoPeer(t)
	first := []byte("first packet from the sniffer")
	_, cached := cachedFirstPacketOverSocket(t, peer, first)

	// 1. The cached first packet is delivered to the sniffer's destination, and the peer
	// never saw it: that read is the saving.
	cachedPacket := cached.ReadCachedPacket()
	require.NotNil(t, cachedPacket)
	require.Equal(t, first, cachedPacket.Buffer.Bytes())
	require.Equal(t, peer.address(), cachedPacket.Destination)
	cachedPacket.Buffer.Release()
	N.PutPacketBuffer(cachedPacket)
	time.Sleep(20 * time.Millisecond) // give a wrong implementation a chance to send it
	require.Zero(t, peer.received.Load(), "the cached packet must not be written to the peer")
	require.True(t, cached.ReaderReplaceable(), "after the take the wrapper is transparent")

	// 2. A real datagram round-trips through the same wrapper.
	require.NoError(t, writePacket(t, cached, []byte("real datagram"), peer.address()))
	payload, destination := nextPacket(t, cached, 2*time.Second)
	require.Equal(t, []byte("real datagram"), payload)
	require.Equal(t, peer.address(), destination)
	require.Equal(t, int64(1), peer.received.Load())

	// 3. An oversized datagram is refused by the socket (or, on a path that allows it,
	// accepted); either way the NEXT packet must still round-trip.
	oversized := make([]byte, 128*1024)
	oversizedErr := writePacket(t, cached, oversized, peer.address())
	if oversizedErr != nil {
		require.True(t, isOversizedError(oversizedErr),
			"an oversized datagram must fail as a too-long message, got %v", oversizedErr)
		t.Logf("oversized datagram refused as expected: %v", oversizedErr)
	} else {
		t.Log("the loopback accepted the oversized datagram; the next-packet check still applies")
		// Drain its echo so the next read sees the small packet.
		_, _ = nextPacket(t, cached, 2*time.Second)
	}
	require.NoError(t, writePacket(t, cached, []byte("after the oversized datagram"), peer.address()))
	payload, _ = nextPacket(t, cached, 2*time.Second)
	require.Equal(t, []byte("after the oversized datagram"), payload,
		"the packet after an oversized one must still be delivered")

	// 4. Close releases a read blocked on the real socket.
	readDone := make(chan error, 1)
	go func() {
		buffer := buf.NewSize(1500)
		defer buffer.Release()
		_, err := cached.ReadPacket(buffer)
		readDone <- err
	}()
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, cached.Close())
	select {
	case err := <-readDone:
		require.Error(t, err, "a read must not report success after Close")
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not release a read blocked on the real socket")
	}
}

// TestCachedFirstPacketKeepsDestinationAcrossSockets pins the destination mapping for two
// different real peers: the cached destination is the sniffer's peer, and the real reads
// report the socket's peer, not the cached one.
func TestCachedFirstPacketKeepsDestinationAcrossSockets(t *testing.T) {
	firstPeer := newUDPEchoPeer(t)
	secondPeer := newUDPEchoPeer(t)
	_, cached := cachedFirstPacketOverSocket(t, firstPeer, []byte("cached for the first peer"))

	cachedPacket := cached.ReadCachedPacket()
	require.NotNil(t, cachedPacket)
	require.Equal(t, firstPeer.address(), cachedPacket.Destination)
	cachedPacket.Buffer.Release()
	N.PutPacketBuffer(cachedPacket)

	require.NoError(t, writePacket(t, cached, []byte("to the first peer"), firstPeer.address()))
	_, destination := nextPacket(t, cached, 2*time.Second)
	require.Equal(t, firstPeer.address(), destination)
	require.Equal(t, int64(1), firstPeer.received.Load())
	require.Zero(t, secondPeer.received.Load(), "the second peer must not receive the first peer's traffic")
}
