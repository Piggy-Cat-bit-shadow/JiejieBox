package direct

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// The production graph, asserted through the REAL constructor.
//
// # Why this file exists
//
// Two consecutive rounds shipped a fix that was correct in isolation and unreachable in production,
// because the production object graph differed from the test fixture's:
//
//	round 1  the recovery helper was correct; the production path never reached it
//	round 2  the resolve wrapper grew the parallel-network surface; the outbound still called the
//	         package helper at the narrow type, so the wrapper's new method was dead code
//
// Both times the fixture built the wrapper (or a double) and handed it to the entry point, which is the
// ONE shape in which the richer interface survives. `outbound_network_dispatch_test.go` fixed the second
// half of that - the outbound's choice of surface - but it still constructs `&Outbound{dialer: spy}`
// directly, so it cannot see what `NewOutbound` actually puts in that field.
//
// This file closes the remaining hole. It calls the constructor the box calls, with the options the box
// builds, and asserts on the object that comes out. If `NewWithOptions` ever stops wrapping the leaf in
// `resolveParallelNetworkDialer`, or `NewOutbound` ever stops storing that wrapper, `scheduleNetwork`
// silently degrades to the package helper, sniffed-domain recovery disappears again, and every existing
// test still passes - because they all supply their own dialer.
//
// # Why the context is nearly empty
//
// A box registers a ConnectionManager, a NetworkManager, a PlatformInterface and a DNS transport manager.
// None of them is needed to answer "what is in this field": the constructors degrade honestly on a
// context with fewer services (see `ResolveAuthority`, which reports no authority rather than
// dereferencing nil), and passing them would test the stubs as much as the code. What this file is about
// is the SHAPE of the resulting object, which does not depend on them.

// newProductionOutbound builds the outbound exactly as the box does: same constructor, same option
// defaults the constructor itself applies.
func newProductionOutbound(t *testing.T, options option.DirectOutboundOptions) *Outbound {
	t.Helper()
	ctx := service.ContextWithDefaultRegistry(context.Background())
	built, err := NewOutbound(ctx, nil, log.NewNOPFactory().Logger(), "direct", options)
	require.NoError(t, err, "the production constructor must build a plain direct outbound")
	require.NotNil(t, built)
	outbound, isOutbound := built.(*Outbound)
	require.True(t, isOutbound, "NewOutbound must return a *direct.Outbound")
	return outbound
}

// TestProductionOutboundHoldsTheParallelNetworkSurface is the structural premise of the round-2 fix.
//
// `scheduleNetwork` used to dispatch to the richer surface only if a narrow field happened to hold an
// object that had one. That is a runtime assertion, so nothing in the type system kept it true - which is
// exactly how the capability was lost twice. The field is now the RESOLVED SURFACE, and the constructor
// refuses a chain that cannot fill it; this asserts both halves on the object the constructor produces.
func TestProductionOutboundHoldsTheParallelNetworkSurface(t *testing.T) {
	outbound := newProductionOutbound(t, option.DirectOutboundOptions{})

	require.NotNil(t, outbound.surface.Attempt, "the constructor must resolve a dialer at all")
	require.NotNil(t, outbound.surface.Network,
		"the resolved surface must carry the candidate-planning capability: without it a literal "+
			"destination cannot be recovered from its sniffed domain, and a sniffed-domain flow to an "+
			"unreachable-family literal is refused with no fallback - the exact failure two rounds were "+
			"spent fixing")

	// And it must be the RESOLVE wrapper, not the bare leaf: the wrapper is where discovery lives, and it
	// is also what makes `DialSerialNetwork`'s first type assertion succeed when the connection manager
	// hands it the outbound.
	require.NotNil(t, outbound.surface.Resolve,
		"and the surface must carry the resolve wrapper: that is the object domain recovery is implemented "+
			"on, and the object the native-bypass profile asks for the effective family strategy")
}

// TestProductionDialerChainKeepsTheRicherSurfaceAcrossEveryOption keeps the premise true for the option
// shapes that change how the chain is built.
//
// A `domain_resolver` present, a strategy set, a fallback delay, an interface selector - each of these
// changes what `NewWithOptions` does around the leaf. None of them may drop the wrapper: they are
// per-connection configuration, not a different kind of dialer.
func TestProductionDialerChainKeepsTheRicherSurfaceAcrossEveryOption(t *testing.T) {
	strategy := option.NetworkStrategy(C.NetworkStrategyDefault)
	for _, testCase := range []struct {
		name    string
		options option.DirectOutboundOptions
	}{
		{name: "plain", options: option.DirectOutboundOptions{}},
		{
			name: "explicit domain resolver",
			options: option.DirectOutboundOptions{DialerOptions: option.DialerOptions{
				AbstractDialerOptions: option.AbstractDialerOptions{
					DomainResolver: &option.DomainResolveOptions{Server: "local"},
				},
			}},
		},
		{
			name: "legacy domain strategy",
			options: option.DirectOutboundOptions{DialerOptions: option.DialerOptions{
				AbstractDialerOptions: option.AbstractDialerOptions{
					//nolint:staticcheck
					DomainStrategy: option.DomainStrategy(C.DomainStrategyIPv4Only),
				},
			}},
		},
		{
			name: "fallback delay",
			options: option.DirectOutboundOptions{DialerOptions: option.DialerOptions{
				AbstractDialerOptions: option.AbstractDialerOptions{
					FallbackDelay: badoption.Duration(50 * 1000 * 1000),
				},
			}},
		},
		{
			name: "network strategy",
			options: option.DirectOutboundOptions{DialerOptions: option.DialerOptions{
				AbstractDialerOptions: option.AbstractDialerOptions{NetworkStrategy: &strategy},
			}},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			outbound := newProductionOutbound(t, testCase.options)
			require.NotNil(t, outbound.surface.Network,
				"%s: this option shape must not cost the outbound the parallel-network surface", testCase.name)
		})
	}
}

// TestProductionOutboundReportsTheChainBelowIt is the other half of the hop protocol.
//
// A dialer built OVER this outbound - `daemon/started_service.go` builds one for the network-quality
// probe and one for STUN - reaches the family verdict and the shared family health by walking the chain
// through `Upstream`. That walk can only work if the object the CONSTRUCTOR produces reports the chain it
// actually dials through, which is what this asserts: a fixture that supplied its own hop would prove the
// walk and nothing about production.
func TestProductionOutboundReportsTheChainBelowIt(t *testing.T) {
	outbound := newProductionOutbound(t, option.DirectOutboundOptions{})

	upstream, isUpstream := any(outbound).(interface{ Upstream() any })
	require.True(t, isUpstream,
		"the outbound must say where the capabilities below it live, or a dialer built over it answers "+
			"'this machine has no opinion about address families'")
	require.Same(t, any(outbound.surface.Attempt), upstream.Upstream(),
		"and it must report the chain it actually dials through, not a rebuilt one")

	// The production statement of the same thing: the leaf is reachable from the outbound through the
	// protocol `common.Cast` walks, which is how every capability below a hop is found.
	leaf, reached := common.Cast[*dialer.DefaultDialer](outbound)
	require.True(t, reached,
		"a caller that walks from the outbound must reach the DefaultDialer underneath: that object is "+
			"what answers the family verdict and owns the shared family health")
	require.NotNil(t, leaf)
}

// TestProductionOutboundStillSatisfiesEveryPublicInterface is the rest of the contract, so a future edit
// cannot satisfy the assertion above by returning a different object.
//
// The connection manager dispatches on these, and the e2e suite plus the runtime tests depend on them:
// the outbound must be an N.ParallelDialer, a dialer.ParallelNetworkDialer, a dialer.DirectDialer, and
// an adapter.LocalDestinationDialer.
func TestProductionOutboundStillSatisfiesEveryPublicInterface(t *testing.T) {
	outbound := newProductionOutbound(t, option.DirectOutboundOptions{})

	_, isParallel := any(outbound).(N.ParallelDialer)
	require.True(t, isParallel, "NewConnection's DialParallel arm depends on it")

	_, isNetworkSurface := any(outbound).(dialer.ParallelNetworkDialer)
	require.True(t, isNetworkSurface, "NewConnection's DialSerialNetwork dispatch depends on it")

	direct, isDirectDialer := any(outbound).(dialer.DirectDialer)
	require.True(t, isDirectDialer, "the route layer treats a direct outbound specially")
	require.NotNil(t, direct)

	local, isLocalDestination := any(outbound).(adapter.LocalDestinationDialer)
	require.True(t, isLocalDestination, "the physical-path validator asks this")
	require.True(t, local.DialsDestinationLocally(),
		"a direct outbound IS the destination connection: it opens an OS socket to the target")
}
