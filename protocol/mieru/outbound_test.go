package mieru

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	mieruclient "github.com/enfein/mieru/v3/apis/client"

	"github.com/stretchr/testify/require"
)

func TestOutboundLifecycle(t *testing.T) {
	options := outboundOptionsForPort(1, "tcp", "alice", "alice-password")
	options.Server = "127.0.0.1"
	created, err := NewOutbound(context.Background(), nil, nopLogger(), "mieru-out", options)
	require.NoError(t, err)
	outboundInstance := created.(*Outbound)

	scope := adapter.NewScope(context.Background(), nopLogger())
	require.NoError(t, outboundInstance.Start(adapter.StartStateInitialize, scope))
	require.False(t, outboundInstance.started, "Initialize must not start the SDK client")

	_, err = outboundInstance.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("192.0.2.1:80"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "has not started")

	require.NoError(t, outboundInstance.Start(adapter.StartStateStart, scope))
	require.True(t, outboundInstance.started)

	previous := outboundInstance.client
	outboundInstance.InterfaceUpdated(context.Background())
	require.Same(t, previous, outboundInstance.client, "a network change before the first dial has nothing to rebuild")
	require.True(t, outboundInstance.client.IsRunning())

	// A second Start at the same stage is a no-op, not a second SDK start.
	require.NoError(t, outboundInstance.Start(adapter.StartStateStart, scope))

	require.NoError(t, outboundInstance.Close())
	require.True(t, outboundInstance.closed)
	_, err = outboundInstance.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("192.0.2.1:80"))
	require.ErrorIs(t, err, errOutboundClosed)

	// Close is idempotent, and the scope's cleanup reaches the same object.
	require.NoError(t, outboundInstance.Close())
	require.NoError(t, scope.Close())
}

func TestOutboundCloseBeforeStartIsSafe(t *testing.T) {
	options := outboundOptionsForPort(1, "tcp", "alice", "alice-password")
	created, err := NewOutbound(context.Background(), nil, nopLogger(), "mieru-out", options)
	require.NoError(t, err)
	outboundInstance := created.(*Outbound)
	require.NoError(t, outboundInstance.Close())

	scope := adapter.NewScope(context.Background(), nopLogger())
	err = outboundInstance.Start(adapter.StartStateStart, scope)
	require.ErrorIs(t, err, errOutboundClosed)
	require.NoError(t, scope.Close())
}

func TestOutboundStartOnClosingScopeDoesNotDeadlock(t *testing.T) {
	options := outboundOptionsForPort(1, "tcp", "alice", "alice-password")
	created, err := NewOutbound(context.Background(), nil, nopLogger(), "mieru-out", options)
	require.NoError(t, err)
	outboundInstance := created.(*Outbound)

	scope := adapter.NewScope(context.Background(), nopLogger())
	require.NoError(t, scope.Close())

	done := make(chan error, 1)
	go func() {
		done <- outboundInstance.Start(adapter.StartStateStart, scope)
	}()
	select {
	case err = <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Start deadlocked while registering cleanup on a closing scope")
	}
	require.True(t, outboundInstance.closed, "the cleanup registered on a closing scope must have run")
}

// TestOutboundDialErrorIsNotSwallowed checks the common.Close defect directly:
// a stopped client must report that it is not running instead of pretending a
// dial succeeded.
func TestOutboundDialErrorIsNotSwallowed(t *testing.T) {
	created := mieruclient.NewClient()
	require.NoError(t, created.Stop())
	_, err := created.DialContext(context.Background(), M.ParseSocksaddr("192.0.2.1:80"))
	require.ErrorIs(t, err, mieruclient.ErrClientIsNotRunning)
}

type recordingDialer struct {
	access      sync.Mutex
	network     string
	destination M.Socksaddr
	conn        net.Conn
}

func (d *recordingDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	d.access.Lock()
	d.network = network
	d.destination = destination
	d.access.Unlock()
	return d.conn, nil
}

func (d *recordingDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("unexpected ListenPacket")
}

func (d *recordingDialer) recorded() (string, M.Socksaddr) {
	d.access.Lock()
	defer d.access.Unlock()
	return d.network, d.destination
}

func TestServerDialerStreamPassesDomainThrough(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	recorder := &recordingDialer{conn: clientConn}
	serverDialer, err := newServerDialer(recorder, "example.com.")
	require.NoError(t, err)
	conn, err := serverDialer.DialContext(context.Background(), N.NetworkTCP, "example.com:443")
	require.NoError(t, err)
	require.NotNil(t, conn)
	network, destination := recorder.recorded()
	require.Equal(t, N.NetworkTCP, network)
	require.Equal(t, "example.com:443", destination.String())
}

// TestServerDialerUDPKeepsTheForkInChargeOfResolution pins the UDP transport
// decision: the SDK's pre-resolved address supplies the port, but the dial
// still goes to the CONFIGURED name through the fork's dialer, so the fork's
// family policy and racing are not replaced by the SDK's single-address
// lookup. The SDK's source validation is satisfied by reporting its own
// expected address.
func TestServerDialerUDPKeepsTheForkInChargeOfResolution(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	recorder := &recordingDialer{conn: clientConn}
	serverDialer, err := newServerDialer(recorder, "example.com")
	require.NoError(t, err)

	packetConn, err := serverDialer.ListenPacket(context.Background(), "udp", "", "192.0.2.5:8966")
	require.NoError(t, err)
	defer packetConn.Close()
	network, destination := recorder.recorded()
	require.Equal(t, N.NetworkUDP, network)
	require.Equal(t, "example.com:8966", destination.String(),
		"the configured server name, not the SDK's resolved address, must reach the fork dialer")

	go func() {
		_, _ = serverConn.Write([]byte("payload"))
	}()
	buffer := make([]byte, 64)
	require.NoError(t, packetConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	n, source, err := packetConn.ReadFrom(buffer)
	require.NoError(t, err)
	require.Equal(t, []byte("payload"), buffer[:n])
	require.Equal(t, "192.0.2.5:8966", source.String(),
		"the SDK compares received packets against the address it resolved")
}

type staticDNSRouter struct {
	adapter.DNSRouter
	addresses []netip.Addr
}

func (r *staticDNSRouter) Lookup(ctx context.Context, domain string, options adapter.DNSQueryOptions) ([]netip.Addr, error) {
	return r.addresses, nil
}

func TestResolverBridgeFamilyFiltering(t *testing.T) {
	router := &staticDNSRouter{addresses: []netip.Addr{
		netip.MustParseAddr("192.0.2.1"),
		netip.MustParseAddr("2001:db8::1"),
	}}
	bridge := &resolverBridge{router: router}
	both, err := bridge.LookupIP(context.Background(), "ip", "example.com")
	require.NoError(t, err)
	require.Len(t, both, 2)

	ipv4, err := bridge.LookupIP(context.Background(), "ip4", "example.com")
	require.NoError(t, err)
	require.Len(t, ipv4, 1)
	require.True(t, ipv4[0].To4() != nil)

	ipv6, err := bridge.LookupIP(context.Background(), "ip6", "example.com")
	require.NoError(t, err)
	require.Len(t, ipv6, 1)
	require.True(t, ipv6[0].To4() == nil)

	_, err = bridge.LookupIP(context.Background(), "unix", "example.com")
	require.Error(t, err)

	empty := &resolverBridge{router: &staticDNSRouter{}}
	_, err = empty.LookupIP(context.Background(), "ip", "example.com")
	require.Error(t, err)

	missingRouter := &resolverBridge{}
	_, err = missingRouter.LookupIP(context.Background(), "ip", "example.com")
	require.Error(t, err)
}

// TestOutboundWithDomainServerNeedsNoManualResolution is a construction-level
// check that a domain server is accepted and left to the fork's dialer; the
// DNS router is not present in this test context, so a client that resolved
// eagerly would fail here.
func TestOutboundWithDomainServerNeedsNoManualResolution(t *testing.T) {
	options := option.MieruOutboundOptions{
		ServerOptions: option.ServerOptions{
			Server:     "example.com",
			ServerPort: 8964,
		},
		UserName: "alice",
		Password: "alice-password",
	}
	created, err := NewOutbound(context.Background(), nil, nopLogger(), "mieru-out", options)
	require.NoError(t, err)
	require.NotNil(t, created)
}
