package route

// The fake-IP app-side NAT wrapper had no test of its own before this file, while it rewrites the
// destination of every UDP packet of a fake-IP flow in both directions. That is the exact shape the
// triple rounds kept finding bugs in: hand-rolled address state on a data path.
//
// # Where the wrapper sits, which is what makes the two directions non-obvious
//
// `route.Router` wraps the APP-side packet conn (`route/route.go:545`, `newFakeIPNATPacketConn(conn,
// metadata.OriginDestination, metadata.Destination)`) and hands it to the connection manager. So:
//
//	upload   = this wrapper's ReadPacket   (app -> manager): the app wrote to the fake address, and the
//	           manager must be told the real destination (the domain, or later the resolved address)
//	download = this wrapper's WritePacket  (manager -> app): the app must see the packet coming FROM the
//	           fake address it sent to, or its connected socket drops the reply
//
// The tests below pin both directions, the direct-source exception (a STUN-style peer that answers from
// another address, whose replies the app may then address directly), and the batch paths, because a
// mapping that is right on `ReadPacket` and wrong on `WaitReadPackets` is a corruption that only shows
// up under a batched writer.

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

// fakePacketPeer is a packet conn whose reads and writes are scripted, so the test observes exactly
// what the wrapper offers upstream and downstream.
type fakePacketPeer struct {
	reads  []fakeRead
	writes []fakeWrite
}

type fakeRead struct {
	payload     []byte
	destination M.Socksaddr
}

type fakeWrite struct {
	payload     []byte
	destination M.Socksaddr
}

func (p *fakePacketPeer) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	if len(p.reads) == 0 {
		return M.Socksaddr{}, net.ErrClosed
	}
	next := p.reads[0]
	p.reads = p.reads[1:]
	_, _ = buffer.Write(next.payload)
	return next.destination, nil
}

func (p *fakePacketPeer) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	payload := make([]byte, buffer.Len())
	copy(payload, buffer.Bytes())
	buffer.Release()
	p.writes = append(p.writes, fakeWrite{payload: payload, destination: destination})
	return nil
}

func (p *fakePacketPeer) ReadFrom(buffer []byte) (int, net.Addr, error) {
	return 0, nil, net.ErrClosed
}

func (p *fakePacketPeer) WriteTo(payload []byte, addr net.Addr) (int, error) {
	p.writes = append(p.writes, fakeWrite{payload: payload, destination: M.SocksaddrFromNet(addr)})
	return len(payload), nil
}

func (p *fakePacketPeer) Close() error                     { return nil }
func (p *fakePacketPeer) LocalAddr() net.Addr              { return nil }
func (p *fakePacketPeer) SetDeadline(time.Time) error      { return nil }
func (p *fakePacketPeer) SetReadDeadline(time.Time) error  { return nil }
func (p *fakePacketPeer) SetWriteDeadline(time.Time) error { return nil }

func newFakeIPTestConn(t *testing.T) (*fakeIPNATPacketConn, *fakePacketPeer) {
	t.Helper()
	peer := &fakePacketPeer{}
	fakeOrigin := M.ParseSocksaddr("198.18.0.5:443")
	domain := M.ParseSocksaddr("held.example:443")
	adapter := bufio.NewNetPacketConn(peer)
	return newFakeIPNATPacketConn(adapter, fakeOrigin, domain), peer
}

// TestFakeIPUploadIsReportedAsTheDomainBeforeResolution is the unmapping the whole fake-IP path exists
// for: the app wrote to the synthetic address, and the manager must be told the domain.
func TestFakeIPUploadIsReportedAsTheDomainBeforeResolution(t *testing.T) {
	wrapper, peer := newFakeIPTestConn(t)
	peer.reads = []fakeRead{{payload: []byte("query"), destination: M.ParseSocksaddr("198.18.0.5:443")}}

	buffer := buf.New()
	destination, err := wrapper.ReadPacket(buffer)
	require.NoError(t, err)
	require.Equal(t, "query", string(buffer.Bytes()))
	require.Equal(t, "held.example", destination.Fqdn,
		"an upload to the fake address must be reported as the domain it stands for")
	require.Equal(t, uint16(443), destination.Port)
	buffer.Release()
}

// TestFakeIPUploadIsReportedAsTheResolvedAddressOnceKnown is the second half: once the router has a
// resolved address, the flow is carried on that address, and an upload the app still addresses to the
// fake placeholder must be reported as the resolved one.
func TestFakeIPUploadIsReportedAsTheResolvedAddressOnceKnown(t *testing.T) {
	wrapper, peer := newFakeIPTestConn(t)
	resolved := netip.MustParseAddr("93.184.216.34")
	wrapper.UpdateDestination(resolved)

	peer.reads = []fakeRead{{payload: []byte("next"), destination: M.ParseSocksaddr("198.18.0.5:443")}}
	buffer := buf.New()
	destination, err := wrapper.ReadPacket(buffer)
	require.NoError(t, err)
	require.Equal(t, resolved, destination.Addr,
		"after UpdateDestination the flow is carried on the resolved address")
	require.Equal(t, uint16(443), destination.Port)
	buffer.Release()
}

// TestFakeIPDownloadIsDeliveredFromTheFakeAddress is what keeps a connected app socket alive: the
// reply must appear to come from the address the app sent to, not from the server's real one.
func TestFakeIPDownloadIsDeliveredFromTheFakeAddress(t *testing.T) {
	wrapper, peer := newFakeIPTestConn(t)
	require.NoError(t, wrapper.WritePacket(buf.As([]byte("answer")), M.ParseSocksaddr("93.184.216.34:443")))
	require.Len(t, peer.writes, 1)
	require.Equal(t, netip.MustParseAddr("198.18.0.5"), peer.writes[0].destination.Addr,
		"a reply must be delivered to the app as coming from the fake address it addressed")
	require.Equal(t, uint16(443), peer.writes[0].destination.Port)
}

// TestADownloadFromAForeignSourceIsNormalisedToTheFakeAddress pins what the wrapper really does with a
// reply from an address the app never used (a STUN-style alternate source): it is delivered to the app
// as coming from the fake address, because the app believes it talks to exactly one peer. The direct
// exception in the other direction is app-driven, which is the test below.
func TestADownloadFromAForeignSourceIsNormalisedToTheFakeAddress(t *testing.T) {
	wrapper, peer := newFakeIPTestConn(t)
	require.NoError(t, wrapper.WritePacket(buf.As([]byte("stun")), M.ParseSocksaddr("203.0.113.9:3478")))
	require.Len(t, peer.writes, 1)
	require.Equal(t, netip.MustParseAddr("198.18.0.5"), peer.writes[0].destination.Addr,
		"a reply the app did not ask for by address must still appear to come from its own peer")
	require.Equal(t, uint16(3478), peer.writes[0].destination.Port,
		"the reply's own port is preserved, so a connected app socket can match it")
}

// TestAnAddressTheAppAddressedItselfStaysDirect is the other half of the exception, and it is
// APP-driven: when the app itself sends to an address that is not the fake placeholder (it resolved
// the name out of band, or it is talking to a second peer), that address is remembered and later
// replies from it are passed through instead of being rewritten to the fake address.
func TestAnAddressTheAppAddressedItselfStaysDirect(t *testing.T) {
	wrapper, peer := newFakeIPTestConn(t)

	peer.reads = []fakeRead{{payload: []byte("direct"), destination: M.ParseSocksaddr("203.0.113.9:3478")}}
	buffer := buf.New()
	destination, err := wrapper.ReadPacket(buffer)
	require.NoError(t, err)
	require.Equal(t, netip.MustParseAddr("203.0.113.9"), destination.Addr,
		"an upload to a non-fake address is reported unchanged")
	buffer.Release()

	require.NoError(t, wrapper.WritePacket(buf.As([]byte("reply")), M.ParseSocksaddr("203.0.113.9:3478")))
	require.Len(t, peer.writes, 1)
	require.Equal(t, netip.MustParseAddr("203.0.113.9"), peer.writes[0].destination.Addr,
		"a reply from an address the app addressed itself must be passed through, or the app could "+
			"never hold a direct two-way conversation with a second peer")
}

// TestFakeIPBatchPathsUseTheSameMapping is the batched-writer half. A mapping that holds on
// ReadPacket but not on the batch writer would corrupt exactly the flows that use the batched path,
// which is the fast path for bulk UDP.

var _ N.NetPacketConn = (*fakePacketPeer)(nil)
