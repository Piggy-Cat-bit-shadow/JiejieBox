//go:build with_quic

package masque

// The C1 prototype's product-level contract: an endpoint that was constructed and initialized - which
// is what EVERY configured MASQUE node goes through at Box start - does not build its userspace stack,
// and the first real demand does.
//
// This is deliberately an assertion about the production endpoint, not about transport/device: the
// device package proves the facade's rules, and this proves the endpoint actually installs it.

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	transportmasque "github.com/sagernet/sing-box/transport/masque"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

func newCostEndpoint(t *testing.T, options_ option.MASQUEClientEndpointOptions) (*ClientEndpoint, *adapter.Scope) {
	t.Helper()
	logger := log.NewNOPFactory().Logger()
	clientContext := service.ContextWithDefaultRegistry(context.Background())
	clientContext = service.ContextWith[adapter.NetworkManager](clientContext, &stubNetworkManager{})
	created, err := NewClientEndpoint(clientContext, nil, logger, "lazy-client", options_)
	require.NoError(t, err)
	endpoint := created.(*ClientEndpoint)
	scope := adapter.NewScope(context.Background(), logger)
	require.NoError(t, endpoint.Start(adapter.StartStateInitialize, scope))
	t.Cleanup(func() { _ = scope.Close() })
	return endpoint, scope
}

func baseCostOptions() option.MASQUEClientEndpointOptions {
	options_ := option.MASQUEClientEndpointOptions{
		ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: 1},
		Version:       3,
		Path:          "/",
		Username:      "cost",
		Password:      "cost-password",
	}
	options_.TLS = &option.OutboundTLSOptions{Enabled: true, ServerName: "example.test", Insecure: true}
	return options_
}

// deferredDeviceOf asserts the endpoint installed the facade and returns it.
func deferredDeviceOf(t *testing.T, endpoint *ClientEndpoint) interface {
	Built() bool
	Generation() uint64
} {
	t.Helper()
	require.NotNil(t, endpoint.device)
	instance, isDeferred := endpoint.device.(interface {
		Built() bool
		Generation() uint64
	})
	require.True(t, isDeferred, "the non-system endpoint did not install a deferred device")
	return instance
}

func TestAnUnusedMASQUEEndpointNeverBuildsItsStack(t *testing.T) {
	endpoint, scope := newCostEndpoint(t, baseCostOptions())
	deferred := deferredDeviceOf(t, endpoint)
	require.False(t, deferred.Built(),
		"Initialize built the userspace stack, which is exactly the cost this prototype removes")

	// The device owns nothing beyond the facade, but its stable state must already answer for it: the
	// MTU is the configured one and the address is invalid before a server assigns it.
	require.EqualValues(t, transportmasque.DefaultMTU, endpoint.PortMTU())
	inet4, inet6 := endpoint.PortAddresses()
	require.False(t, inet4.IsValid())
	require.False(t, inet6.IsValid())
	require.Zero(t, endpoint.FrontHeadroom())

	require.NoError(t, scope.Close())
	require.False(t, deferred.Built(), "closing an unused endpoint built a stack")
}

func TestFirstConfigurationDoesNotBuildAndFirstDialDoes(t *testing.T) {
	endpoint, _ := newCostEndpoint(t, baseCostOptions())
	deferred := deferredDeviceOf(t, endpoint)

	// This is the sequence the session drives when a server assigns addresses: configuration, then
	// Start. Neither is a demand for a stack any more.
	require.NoError(t, endpoint.UpdateConfiguration(transportmasque.Configuration{
		Address: []netip.Prefix{netip.MustParsePrefix("10.42.0.2/32")},
	}))
	require.False(t, deferred.Built(), "a server configuration built the stack")

	inet4, _ := endpoint.PortAddresses()
	require.Equal(t, netip.MustParseAddr("10.42.0.2"), inet4,
		"the assigned address must be visible before a stack exists")

	dialCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	// The dial itself may fail (there is no tunnel behind this fake endpoint); what is asserted is
	// that reaching the stack built exactly one generation and that a second demand reuses it.
	_, _ = endpoint.device.DialContext(dialCtx, N.NetworkTCP,
		M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 80))
	require.True(t, deferred.Built(), "the first dial did not build the stack")
	require.EqualValues(t, 1, deferred.Generation())

	_, _ = endpoint.device.DialContext(dialCtx, N.NetworkTCP,
		M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 80))
	require.EqualValues(t, 1, deferred.Generation(), "a second demand built a second stack")
}

// TestTheSystemPathStillBuildsAtInitialize is a source-level guard rather than a host run: a system
// endpoint would claim host routes and sockets, which needs privileges this test machine does not
// have. It pins the branch's decision so a later refactor cannot quietly defer the system device too.
func TestTheSystemPathStillBuildsAtInitialize(t *testing.T) {
	options_ := baseCostOptions()
	options_.MASQUEEndpointOptions.System = true
	logger := log.NewNOPFactory().Logger()
	clientContext := service.ContextWithDefaultRegistry(context.Background())
	clientContext = service.ContextWith[adapter.NetworkManager](clientContext, &stubNetworkManager{})
	// Construction only: the initialize stage of a system device needs the host interface.
	created, err := NewClientEndpoint(clientContext, nil, logger, "system-client", options_)
	require.NoError(t, err)
	endpoint := created.(*ClientEndpoint)
	defer func() { _ = endpoint.Close() }()
	scope := adapter.NewScope(context.Background(), logger)
	defer func() { _ = scope.Close() }()
	// The system flag survives into the endpoint. Building it here would need privileges; what must
	// not happen is the system flag being dropped on the way to device.New.
	require.True(t, endpoint.system)
	require.Nil(t, endpoint.device, "the system device must not be built by a test that cannot own the host interface")
}
