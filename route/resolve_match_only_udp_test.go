package route

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// The UDP arm of the same publication decision.
//
// A packet flow has no `DialContext` to preserve a name with: the proxy is handed the destination
// through `ListenPacket`, and route/conn.go chooses between `ListenSerialNetworkPacket` (candidate
// list) and a plain `ListenPacket` (no candidates, name kept) on exactly the field `match_only`
// stops populating. Testing only TCP would leave this half unproven.

var errPacketDialRecorded = errors.New("packet dial recorded")

// matchOnlyPacketOutbound records every destination it is handed. It implements only N.Dialer, so
// `ListenSerialNetworkPacket` falls back to its serial loop - which is the honest double for a
// proxy that has no parallel surface.
type matchOnlyPacketOutbound struct {
	adapter.Outbound
	mu          sync.Mutex
	listenCalls []M.Socksaddr
	dialCalls   []M.Socksaddr
}

// Type and Tag are reached by the manager's failure report, which names the outbound that refused
// the socket. They are stated here rather than left to the embedded nil interface.
func (d *matchOnlyPacketOutbound) Type() string { return "test" }
func (d *matchOnlyPacketOutbound) Tag() string  { return "test-packet" }

func (d *matchOnlyPacketOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	d.mu.Lock()
	d.dialCalls = append(d.dialCalls, destination)
	d.mu.Unlock()
	return nil, errPacketDialRecorded
}

func (d *matchOnlyPacketOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	d.mu.Lock()
	d.listenCalls = append(d.listenCalls, destination)
	d.mu.Unlock()
	return nil, errPacketDialRecorded
}

func (d *matchOnlyPacketOutbound) recorded() (listen []M.Socksaddr, dial []M.Socksaddr) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]M.Socksaddr(nil), d.listenCalls...), append([]M.Socksaddr(nil), d.dialCalls...)
}

func TestMatchOnlyResolutionKeepsTheNameForUDP(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = manager.Close() })

	t.Run("match only", func(t *testing.T) {
		outbound := &matchOnlyPacketOutbound{}
		manager.NewPacketConnection(context.Background(), outbound, packetConnStub{}, adapter.InboundContext{
			Destination:    M.Socksaddr{Fqdn: "named.example", Port: 443},
			MatchAddresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")},
		}, nil)

		listen, dial := outbound.recorded()
		require.Empty(t, dial, "a packet flow does not go through DialContext on this arm")
		require.Len(t, listen, 1, "exactly one socket attempt is made")
		require.Equal(t, "named.example", listen[0].Fqdn,
			"the name must reach the proxy: the matching address is not a dial target")
		require.Equal(t, uint16(443), listen[0].Port)
	})

	t.Run("dial candidates", func(t *testing.T) {
		outbound := &matchOnlyPacketOutbound{}
		manager.NewPacketConnection(context.Background(), outbound, packetConnStub{}, adapter.InboundContext{
			Destination:          M.Socksaddr{Fqdn: "named.example", Port: 443},
			DestinationAddresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")},
		}, nil)

		listen, _ := outbound.recorded()
		require.Len(t, listen, 1)
		require.Equal(t, netip.MustParseAddr("198.51.100.10"), listen[0].Addr,
			"the historical action still hands the resolved address to the proxy, as before")
		require.Empty(t, listen[0].Fqdn)
	})
}

var _ N.Dialer = (*matchOnlyPacketOutbound)(nil)
