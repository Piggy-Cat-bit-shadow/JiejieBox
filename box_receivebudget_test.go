package box

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/receivebudget"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// TestWithNetworkExtensionReceiveBudgetKeepsAnEmbeddersPolicy pins the ownership rule for the second
// per-box platform policy: the box consumes the decision, it does not overwrite one already made.
func TestWithNetworkExtensionReceiveBudgetKeepsAnEmbeddersPolicy(t *testing.T) {
	own := receivebudget.Policy{UnreadPayloadBytes: 1 << 20}
	ctx := service.ContextWithDefaultRegistry(context.Background())
	ctx = receivebudget.NewContext(ctx, own)
	ctx = service.ContextWith[adapter.PlatformInterface](ctx, &platformInterfaceProbe{underNetworkExtension: true})

	installed, ok := receivebudget.FromContext(withNetworkExtensionReceiveBudget(ctx))
	require.True(t, ok)
	require.Equal(t, own, installed, "the embedder's policy must survive the box")
}

// TestWithNetworkExtensionReceiveBudgetWithoutAPlatformInterface covers a host that builds a box with
// no platform interface: the gate must read that as "not an extension" rather than dereference it.
func TestWithNetworkExtensionReceiveBudgetWithoutAPlatformInterface(t *testing.T) {
	ctx := service.ContextWithDefaultRegistry(context.Background())
	ctx = withNetworkExtensionReceiveBudget(ctx)
	_, ok := receivebudget.FromContext(ctx)
	require.False(t, ok)
}

// TestNetworkExtensionBoxGate records the gate both platform policies share, including the case a
// GOOS-based gate would get wrong: a macOS Network Extension reports UnderNetworkExtension() and must
// still receive neither the iOS window policy nor the iOS budget. C.IsIos is false on this host, so
// the true branch cannot be reached here - that is what the device run is for, and the gate itself is
// enumerated in TestNetworkExtensionQUICReceiveWindowsGate.
func TestNetworkExtensionBoxGate(t *testing.T) {
	ctx := service.ContextWithDefaultRegistry(context.Background())
	ctx = service.ContextWith[adapter.PlatformInterface](ctx, &platformInterfaceProbe{underNetworkExtension: true})
	require.False(t, networkExtensionBox(ctx), "this host is not iOS, so the gate is closed")

	ctx = service.ContextWithDefaultRegistry(context.Background())
	require.False(t, networkExtensionBox(ctx), "no platform interface is not an extension")
}
