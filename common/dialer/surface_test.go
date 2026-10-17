package dialer

import (
	"context"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// The local dial surface, resolved from the chain the factory actually produces.
//
// # Why this is not a unit test of NewLocalDialSurface
//
// A test that hands the resolver a hand-built chain asserts the resolver, and nothing about whether the
// chain production builds can fill it. That is the exact gap the two rounds before this one fell into: the
// helper was correct and the object graph never reached it.
//
// So this composes the chain the way `NewWithOptions` does for a locally-dialling outbound - a
// DefaultDialer over the real manager shape, wrapped by NewResolveDialer because the server address is a
// domain - and asserts on what comes back. The wrapper condition is unconditional in that shape
// (`RemoteIsDomain && !hasDetour`), so any direct outbound takes the same branch and there is no option
// matrix to reproduce.

func newProductionChain(t *testing.T) N.Dialer {
	t.Helper()
	ctx := service.ContextWith[adapter.NetworkManager](context.Background(), newCountingNetworkManager())
	leaf, err := NewDefault(ctx, option.DialerOptions{})
	require.NoError(t, err)
	return NewResolveDialer(ctx, leaf, true, "", adapter.DNSQueryOptions{}, 0)
}

// TestTheProductionChainYieldsACompleteSurface is the whole point of resolving at construction.
func TestTheProductionChainYieldsACompleteSurface(t *testing.T) {
	chain := newProductionChain(t)
	require.IsType(t, (*resolveParallelNetworkDialer)(nil), chain,
		"the direct shape must produce the parallel resolve wrapper: the serial one has no candidate "+
			"planning, and a literal destination could not be recovered from its sniffed domain")

	surface, err := NewLocalDialSurface(chain)
	require.NoError(t, err, "the production chain must fill the surface")

	require.NotNil(t, surface.Attempt, "an attempt goes through the chain")
	require.NotNil(t, surface.Network, "and a candidate list goes through the planning surface")
	require.NotNil(t, surface.Resolve,
		"the resolve wrapper is where the effective family strategy comes from, and the native-bypass "+
			"profile asks it the same question the dial path asks")
	require.NotNil(t, surface.LocalFamily,
		"the leaf can describe this machine's addressing, and the bypass needs that verdict")

	// The capabilities must be read off the SAME object the attempts go through, not looked up again: the
	// bypass and the dial path then cannot end up asking different things about one network.
	require.Same(t, any(surface.Attempt), any(surface.Resolve),
		"the resolve capability is read off the attempt chain itself")
	require.Same(t, any(surface.Attempt), any(surface.Network))
	require.Same(t, any(surface.Attempt), any(surface.LocalFamily))
}

// TestASurfaceWithoutThePlanningCapabilityIsAnError is the fail-closed half.
//
// The predecessor of this check was a runtime assertion at the point of use, and when it failed the code
// took a narrower path that silently lost recovery. The error names the factory as the culprit because no
// configuration can produce this shape: `NewWithOptions` with `RemoteIsDomain: true` always wraps, and the
// leaf always implements the per-attempt surface.
func TestASurfaceWithoutThePlanningCapabilityIsAnError(t *testing.T) {
	_, err := NewLocalDialSurface(&narrowChainDouble{})
	require.Error(t, err, "a chain with only the per-attempt surface must not become a local dial surface")
	require.Contains(t, err.Error(), "candidate-planning surface",
		"and the refusal must name the missing capability")

	_, err = NewLocalDialSurface(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no dialer")
}

// narrowChainDouble is the per-attempt shape and nothing more: what a chain looks like when the planning
// surface never got attached.
//
// The embedded interface is nil on purpose - the double must satisfy the per-attempt interface without
// offering the richer one, and a call to one of the promoted methods panicking is the correct behaviour for
// a fixture that exists to be refused.
type narrowChainDouble struct {
	ParallelInterfaceDialer
}

func (d *narrowChainDouble) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return nil, nil
}

func (d *narrowChainDouble) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, nil
}
