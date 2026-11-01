package direct

import (
	"context"
	"net/netip"
	"syscall"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/control"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// The production outbound, built from a context that carries the services a real Box carries.
//
// # Why the other production-graph tests are not enough
//
// `newProductionOutbound` builds from a registry with NO services, and that is deliberate there: the
// question those tests ask is "what shape does the factory produce", which does not depend on the
// manager. But it leaves the ambient half untested - the part of the decision that comes from the
// NetworkManager rather than from the outbound's own options - and "the test context injected a different
// set of services than production does" is one of the ways a production-only defect stays invisible.
//
// So this file registers the services and drives the same constructor. It is the only place the two halves
// of `BypassBlockers` are exercised together on a REAL outbound: the profile built from the options, and
// the ambient policy read from the manager.

// ambientManager is a NetworkManager that reports a configured ambient policy.
//
// The embedded interface is nil: this double answers only what a dialer's construction and the bypass
// decision actually ask for, and a promoted method called on the nil embed panics rather than silently
// answering zero - which is the behaviour a fixture wants when it is asked for something it does not model.
type ambientManager struct {
	adapter.NetworkManager
	options adapter.NetworkOptions
}

func (m *ambientManager) DefaultOptions() adapter.NetworkOptions { return m.options }
func (m *ambientManager) AutoDetectInterface() bool              { return false }
func (m *ambientManager) AutoRedirectOutputMarkFunc() control.Func {
	return func(network, address string, conn syscall.RawConn) error { return nil }
}
func (m *ambientManager) NetworkEnvironment() uint64 { return 0 }
func (m *ambientManager) InterfaceFinder() control.InterfaceFinder {
	return control.NewDefaultInterfaceFinder()
}

var _ adapter.NetworkManager = (*ambientManager)(nil)

// newProductionOutboundWithServices is `newProductionOutbound` with the manager a Box would have
// registered.
func newProductionOutboundWithServices(t *testing.T, manager *ambientManager, options option.DirectOutboundOptions) *Outbound {
	t.Helper()
	ctx := service.ContextWithDefaultRegistry(context.Background())
	service.MustRegister[adapter.NetworkManager](ctx, manager)
	built, err := NewOutbound(ctx, nil, log.NewNOPFactory().Logger(), "direct", options)
	require.NoError(t, err, "the production constructor must build the outbound with a manager present")
	outbound, isOutbound := built.(*Outbound)
	require.True(t, isOutbound)
	return outbound
}

// TestTheAmbientPolicyReachesTheBypassDecision is the ambient half, on the real object.
func TestTheAmbientPolicyReachesTheBypassDecision(t *testing.T) {
	literal := netip.MustParseAddr("93.184.216.34")

	// Control: no ambient policy, so the literal flow is exactly what the fast path exists for.
	plain := newProductionOutboundWithServices(t, &ambientManager{}, option.DirectOutboundOptions{})
	require.True(t, plain.CanBypass(N.NetworkTCP, literal),
		"the fixture must be eligible with no ambient policy, or this test proves nothing about one")

	// A globally bound socket, which a platform connect does not reproduce.
	bound := newProductionOutboundWithServices(t, &ambientManager{
		options: adapter.NetworkOptions{BindInterface: "eth0"},
	}, option.DirectOutboundOptions{})
	require.False(t, bound.CanBypass(N.NetworkTCP, literal),
		"a globally bound interface would be silently dropped by a bypass: the platform connects with the "+
			"application's socket, which has no such bind")
	require.NotZero(t, bound.BypassBlockers(N.NetworkTCP, literal)&dialer.BlockerGlobalNetworkPolicy,
		"and the refusal must be attributed to the ambient policy rather than to the outbound's options")
	require.NotZero(t, bound.BypassBlockers(N.NetworkUDP, literal)&dialer.BlockerGlobalNetworkPolicy,
		"on both networks: the bind is not transport-specific")
}

// TestTheAmbientPolicyDoesNotChangeTheDialSurface keeps the two halves separable.
//
// The surface is what the dialer factory produced and must not depend on the manager; the ambient policy
// is read per flow and must not be cached into the surface. A regression that welded them together would
// make an interface change silently require a new outbound.
func TestTheAmbientPolicyDoesNotChangeTheDialSurface(t *testing.T) {
	a := newProductionOutboundWithServices(t, &ambientManager{}, option.DirectOutboundOptions{})
	b := newProductionOutboundWithServices(t, &ambientManager{
		options: adapter.NetworkOptions{BindInterface: "eth0"},
	}, option.DirectOutboundOptions{})

	require.NotNil(t, a.surface.Network)
	require.NotNil(t, b.surface.Network)
	require.Equal(t, a.surface.Network != nil, b.surface.Network != nil)
	require.Equal(t, a.surface.Resolve != nil, b.surface.Resolve != nil,
		"the resolve wrapper is part of the CHAIN, and an ambient bind does not change the chain")
	require.Equal(t, a.surface.LocalFamily != nil, b.surface.LocalFamily != nil)
	require.Equal(t, a.IsEmpty(), b.IsEmpty(),
		"and the outbound's own options are identical, so 'does it ask for anything' is identical")
}

// TestAStrictStrategyRefusesTheBypassOnTheRealOutbound is the effective-policy half, end to end.
//
// The strategy is not configured on the outbound at all: it arrives through the resolve wrapper the
// factory built (`domain_strategy` reaches `dnsQueryOptions.Strategy`), which is exactly the case that
// used to be inferred from "did this outbound configure a resolver" and got the answer wrong.
func TestAStrictStrategyRefusesTheBypassOnTheRealOutbound(t *testing.T) {
	ipv6 := netip.MustParseAddr("2001:db8::2c")
	ipv4 := netip.MustParseAddr("192.0.2.92")

	//nolint:staticcheck
	options := option.DirectOutboundOptions{DialerOptions: option.DialerOptions{
		AbstractDialerOptions: option.AbstractDialerOptions{
			DomainStrategy: option.DomainStrategy(C.DomainStrategyIPv4Only),
		},
	}}
	outbound := newProductionOutboundWithServices(t, &ambientManager{}, options)

	require.False(t, outbound.CanBypass(N.NetworkTCP, ipv6),
		"the effective policy excludes this family, so the platform must not connect to it")
	require.False(t, outbound.CanBypass(N.NetworkUDP, ipv6),
		"and the policy is about the destination's family, not the transport")
	require.True(t, outbound.CanBypass(N.NetworkTCP, ipv4),
		"while the family it admits stays bypassable")
}
