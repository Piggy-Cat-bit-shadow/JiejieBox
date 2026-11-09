//go:build with_quic

package masque

// What a real MASQUE CONNECT-IP client and server agree about on the wire: the inner address it is
// given, and that it belongs to the server's configured prefix.
//
// The carrier matrix asks the CONNECT-IP row to check the inner IP, the routes and the address
// ownership. The full datapath (inner flows terminated by the server's userspace stack and routed by
// the production engine) needs a dedicated route-engine fixture; the assignment does not - it is the
// exchange both endpoints perform when the tunnel is established, with production objects on both
// sides:
//
//	NewServerEndpoint  a real HTTP/3 listener on loopback with a configured inner prefix and users
//	NewClientEndpoint  the production client endpoint pointed at it with the same credentials
//
// # The lifecycle detail that cost the first attempts
//
// The client endpoint starts its session loop in StartStatePostStart, not StartStateStart: a fixture
// that runs only Initialize and Start gets a fully constructed endpoint whose client never connects,
// with no log line and no error anywhere. With the third stage the tunnel is ready in well under a
// second. That ordering is the Box's, so the fixture follows it rather than working around it.

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/auth"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

const (
	connectIPUser     = "carrier"
	connectIPPassword = "carrier-password"
	connectIPPrefix   = "10.42.0.0/24"
)

// startConnectIPServer starts the production server endpoint and returns it with its UDP port.
func startConnectIPServer(t *testing.T, prefix netip.Prefix) (*ServerEndpoint, uint16) {
	t.Helper()
	logger := log.NewNOPFactory().Logger()
	certPEM, keyPEM := newLoopbackCertificate(t)
	serverContext := service.ContextWithDefaultRegistry(context.Background())
	serverContext = service.ContextWith[adapter.NetworkManager](serverContext, &stubNetworkManager{})
	created, err := NewServerEndpoint(serverContext, nil, logger, "connect-ip-server",
		option.MASQUEServerEndpointOptions{
			MASQUEEndpointOptions: option.MASQUEEndpointOptions{MTU: 1500},
			ListenOptions: option.ListenOptions{
				Listen:     common.Ptr(badoption.Addr(netip.AddrFrom4([4]byte{127, 0, 0, 1}))),
				ListenPort: 0,
			},
			Version: badoption.Listable[int]{3},
			Address: badoption.Listable[netip.Prefix]{prefix},
			Path:    "/",
			Users:   []auth.User{{Username: connectIPUser, Password: connectIPPassword}},
			InboundTLSOptionsContainer: option.InboundTLSOptionsContainer{
				TLS: &option.InboundTLSOptions{
					Enabled:     true,
					ServerName:  "example.test",
					Certificate: []string{certPEM},
					Key:         badoption.Listable[string]{keyPEM},
				},
			},
		})
	require.NoError(t, err)
	serverEndpoint, isServer := created.(*ServerEndpoint)
	require.True(t, isServer)
	scope := adapter.NewScope(context.Background(), logger)
	require.NoError(t, serverEndpoint.Start(adapter.StartStateInitialize, scope))
	require.NoError(t, serverEndpoint.Start(adapter.StartStateStart, scope))
	require.NoError(t, serverEndpoint.Start(adapter.StartStatePostStart, scope))
	t.Cleanup(func() { _ = scope.Close() })
	return serverEndpoint, uint16(serverEndpoint.listener.UDPConn().LocalAddr().(*net.UDPAddr).Port)
}

// startConnectIPClient starts the production client endpoint against the server and waits until the
// session is configured.
func startConnectIPClient(t *testing.T, port uint16) *ClientEndpoint {
	t.Helper()
	logger := log.NewNOPFactory().Logger()
	clientOptions := option.MASQUEClientEndpointOptions{
		ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: port},
		Version:       3,
		Path:          "/",
		Username:      connectIPUser,
		Password:      connectIPPassword,
	}
	// TLS is promoted from an embedded struct, so it cannot be set in the literal above.
	clientOptions.TLS = &option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: "example.test",
		Insecure:   true,
	}
	clientContext := service.ContextWithDefaultRegistry(context.Background())
	clientContext = service.ContextWith[adapter.NetworkManager](clientContext, &stubNetworkManager{})
	created, err := NewClientEndpoint(clientContext, nil, logger, "connect-ip-client", clientOptions)
	require.NoError(t, err)
	clientEndpoint, isClient := created.(*ClientEndpoint)
	require.True(t, isClient)
	scope := adapter.NewScope(context.Background(), logger)
	require.NoError(t, clientEndpoint.Start(adapter.StartStateInitialize, scope))
	require.NoError(t, clientEndpoint.Start(adapter.StartStateStart, scope))
	require.NoError(t, clientEndpoint.Start(adapter.StartStatePostStart, scope))
	t.Cleanup(func() { _ = scope.Close() })
	return clientEndpoint
}

func TestARealCONNECTIPClientIsGivenAnInnerAddressInsideTheServerPrefix(t *testing.T) {
	requireHTTP3Client(t)
	serverPrefix := netip.MustParsePrefix(connectIPPrefix)
	serverEndpoint, port := startConnectIPServer(t, serverPrefix)
	clientEndpoint := startConnectIPClient(t, port)

	require.Eventually(t, func() bool {
		return clientEndpoint.state.Load().configured
	}, 15*time.Second, 5*time.Millisecond,
		"the client endpoint never received its CONNECT-IP configuration; the tunnel did not establish")

	state := clientEndpoint.state.Load()
	require.NotEmpty(t, state.localAddresses, "the client was told it owns no inner address")
	for _, assigned := range state.localAddresses {
		require.True(t, serverPrefix.Contains(assigned.Addr()),
			"the client was assigned %s, which the server's configured prefix %s does not cover",
			assigned, serverPrefix)
	}

	serverAddress, _ := serverEndpoint.PortAddresses()
	clientAddress, _ := clientEndpoint.PortAddresses()
	t.Logf("assigned %v, routes %v, server inner %s, client inner %s",
		state.localAddresses, state.routes, serverAddress, clientAddress)
	require.NotEqual(t, netip.Addr{}, serverAddress, "the server endpoint reports no inner address")
	require.True(t, serverPrefix.Contains(serverAddress),
		"the server's inner address %s is outside its own configured prefix", serverAddress)
}
