package wireguard

// The UDP ownership wrapper must keep the capabilities the wrapped object really has, and must not
// claim the ones it does not. Independent review caught the regression this file prevents:
//
//	raw=*tun.GoUDPConn          NetPacketConn=true  PacketReadWaiter=true
//	owned=*udpAssociationPacketConn NetPacketConn=false PacketReadWaiter=false
//
// which silently moved every UDP flow onto the generic read-buffer/copy path, because the product
// adapts packet conns through iponly -> bufio and those helpers SELECT the fast path by type
// assertion.

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/iponly"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// TestUDPAssociationPreservesPacketCapabilities is the review's probe as a permanent test: through
// the SAME iponly/bufio adaptation the product uses, the ownership wrapper must still reach the
// optimised packet reader.
func TestUDPAssociationPreservesPacketCapabilities(t *testing.T) {
	pair := lazyStartPair(t, option.AmneziaWGOptions{})
	raw, err := pair.client.tunDevice.ListenPacket(context.Background(), pair.destination)
	require.NoError(t, err)
	defer raw.Close()
	owned, err := pair.client.ListenPacket(context.Background(), pair.destination)
	require.NoError(t, err)
	defer owned.Close()

	logger := log.NewNOPFactory().Logger()
	for name, conn := range map[string]net.PacketConn{"raw": raw, "owned": owned} {
		_, isNetPacketConn := conn.(N.NetPacketConn)
		require.True(t, isNetPacketConn, "%s must expose ReadPacket/WritePacket", name)
		adapted := iponly.NewPacketConn(logger, conn)
		_, hasWaiter := bufio.CreatePacketReadWaiter(adapted)
		require.True(t, hasWaiter, "%s must still reach the PacketReadWaiter fast path through iponly/bufio", name)
	}

	// The selection itself: bufio.NewPacketConn returns the object unchanged only when it implements
	// N.NetPacketConn, and that identity is what keeps the fast path.
	selected := bufio.NewPacketConn(owned)
	require.Same(t, owned, selected,
		"the product's adapter must select the wrapper's packet methods instead of a generic copy wrapper")
}

// TestUDPAssociationConnPreservesCapabilities is the connected-association half (DialContext udp).
func TestUDPAssociationConnPreservesCapabilities(t *testing.T) {
	pair := lazyStartPair(t, option.AmneziaWGOptions{})
	conn, err := pair.client.DialContext(context.Background(), "udp", pair.destination)
	require.NoError(t, err)
	defer conn.Close()

	_, isNetPacketConn := conn.(N.NetPacketConn)
	require.True(t, isNetPacketConn, "a connected UDP association must keep ReadPacket/WritePacket")
	_, hasWaiter := bufio.CreatePacketReadWaiter(conn.(N.PacketReader))
	require.True(t, hasWaiter, "a connected UDP association must keep the waiter fast path")
}

// TestUDPAssociationDataPathThroughForwardedMethods sends and receives through the forwarded packet
// methods, not through the generic ReadFrom/WriteTo path: payload and source address must be right.
func TestUDPAssociationDataPathThroughForwardedMethods(t *testing.T) {
	pair := lazyStartPair(t, option.AmneziaWGOptions{})
	startUDPEcho(t, pair)
	conn, err := pair.client.ListenPacket(context.Background(), pair.destination)
	require.NoError(t, err)
	defer conn.Close()

	packetConn, isPacketConn := conn.(N.NetPacketConn)
	require.True(t, isPacketConn)
	payload := []byte("forwarded-packet-path")
	require.NoError(t, packetConn.WritePacket(buf.As(payload), pair.destination))

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(20*time.Second)))
	reply := buf.New()
	defer reply.Release()
	source, readErr := packetConn.ReadPacket(reply)
	require.NoError(t, readErr)
	require.Equal(t, payload, reply.Bytes())
	require.True(t, source.Addr.IsValid(), "the forwarded reader must report the source address")

	// Deadline semantics survive the wrapper.
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(100*time.Millisecond)))
	idle := buf.New()
	defer idle.Release()
	_, timeoutErr := packetConn.ReadPacket(idle)
	require.Error(t, timeoutErr)
	require.True(t, errors.Is(timeoutErr, os.ErrDeadlineExceeded), "deadline errors must pass through: %v", timeoutErr)
}

// TestUDPAssociationWaitReadPacketPath exercises the waiter itself, since that is the path the
// regression removed from the product.
func TestUDPAssociationWaitReadPacketPath(t *testing.T) {
	pair := lazyStartPair(t, option.AmneziaWGOptions{})
	startUDPEcho(t, pair)
	conn, err := pair.client.ListenPacket(context.Background(), pair.destination)
	require.NoError(t, err)
	defer conn.Close()

	packetConn, isPacketConn := conn.(N.NetPacketConn)
	require.True(t, isPacketConn)
	waiter, hasWaiter := bufio.CreatePacketReadWaiter(packetConn)
	require.True(t, hasWaiter)
	payload := []byte("waiter-path")
	require.NoError(t, packetConn.WritePacket(buf.As(payload), pair.destination))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(20*time.Second)))
	waiter.InitializeReadWaiter(N.ReadWaitOptions{FrontHeadroom: 0, MTU: 1500})
	reply, _, readErr := waiter.WaitReadPacket()
	require.NoError(t, readErr)
	defer reply.Release()
	require.Equal(t, payload, reply.Bytes())
}

// TestUDPAssociationCloseCountsExactlyOnce covers repeated and concurrent Close.
func TestUDPAssociationCloseCountsExactlyOnce(t *testing.T) {
	pair := lazyStartPair(t, option.AmneziaWGOptions{})
	first, err := pair.client.ListenPacket(context.Background(), pair.destination)
	require.NoError(t, err)
	second, err := pair.client.DialContext(context.Background(), "udp", pair.destination)
	require.NoError(t, err)
	require.EqualValues(t, 2, pair.client.udpAssociations.Load())

	require.NoError(t, first.Close())
	require.EqualValues(t, 1, pair.client.udpAssociations.Load())
	_ = first.Close()
	require.EqualValues(t, 1, pair.client.udpAssociations.Load(), "a repeated Close must not release twice")

	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_ = second.Close()
		}()
	}
	group.Wait()
	require.EqualValues(t, 0, pair.client.udpAssociations.Load(), "concurrent Close must release exactly once")
}

// stubPacketConn is ONLY a net.PacketConn: it deliberately has none of the optional capabilities.
type stubPacketConn struct{ closed chan struct{} }

func (c *stubPacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, os.ErrDeadlineExceeded
}
func (c *stubPacketConn) WriteTo(p []byte, _ net.Addr) (int, error) { return len(p), nil }
func (c *stubPacketConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}
func (c *stubPacketConn) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (c *stubPacketConn) SetDeadline(time.Time) error      { return nil }
func (c *stubPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (c *stubPacketConn) SetWriteDeadline(time.Time) error { return nil }

// TestUDPAssociationDoesNotClaimMissingCapabilities is the other half of the rule: an object without
// the optional interfaces must not be advertised as having them.
func TestUDPAssociationDoesNotClaimMissingCapabilities(t *testing.T) {
	endpoint := &Endpoint{}
	wrapped := wrapUDPAssociationPacket(&stubPacketConn{closed: make(chan struct{})}, endpoint)
	_, isNetPacketConn := wrapped.(N.NetPacketConn)
	require.False(t, isNetPacketConn, "a plain net.PacketConn must not be advertised as N.NetPacketConn")
	genericAdapter := bufio.NewPacketConn(wrapped)
	_, hasWaiter := bufio.CreatePacketReadWaiter(genericAdapter)
	require.False(t, hasWaiter, "a plain net.PacketConn has no waiter to forward")

	require.EqualValues(t, 1, endpoint.udpAssociations.Load())
	require.NoError(t, wrapped.Close())
	require.EqualValues(t, 0, endpoint.udpAssociations.Load())

	// The generic adapter still produces a working capability set for the object it was given.
	upstream, hasUpstream := wrapped.(common.WithUpstream)
	require.True(t, hasUpstream, "the wrapper must expose its upstream to the documented fallback chain")
	require.NotNil(t, upstream.Upstream())
}

// NOTE on the A/B measurement the review asked for (allocations/throughput fast vs generic): it was
// implemented and then REMOVED as untrustworthy. The waiter path blocks as soon as the primed datagram
// queue drains, so a bounded run either hangs for minutes (measured 6m0s for 200 reads, with every
// read after the queue emptied waiting out the 5s deadline) or measures whatever fraction happened to
// be primed. Publishing those numbers would be exactly the "capability PASS reported as a quantified
// benefit" the review warns against. The capability SELECTION is what this file pins; the quantified
// benefit is left unmeasured rather than invented.
// strippedPacketConn hides the optional write capability on purpose, so the A/B check can measure the
// generic adapter without touching production code.
type strippedPacketConn struct {
	net.PacketConn
}

func (c *strippedPacketConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	return M.Socksaddr{}, os.ErrInvalid
}

func (c *strippedPacketConn) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	buffer.Release()
	return os.ErrInvalid
}

var _ N.PacketConn = (*strippedPacketConn)(nil)
