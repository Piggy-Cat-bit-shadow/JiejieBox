package provider_test

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/provider"
	"github.com/sagernet/sing-box/log"
	"github.com/stretchr/testify/require"
)

// TestProviderManagerLifecycle pins the manager's contract around repeated lifecycle calls, which a
// daemon can produce when a start fails and is retried, or when Close races a shutdown hook.
func TestProviderManagerLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	logger := log.NewNOPFactory()
	manager := provider.NewManager(ctx, logger.Logger(), provider.NewRegistry())
	scope := adapter.NewScope(ctx, logger.Logger())
	defer scope.Close()

	require.NoError(t, manager.Start(adapter.StartStateInitialize, scope))
	require.ErrorContains(t, manager.Start(adapter.StartStateInitialize, scope), "already started")
	require.NoError(t, manager.Start(adapter.StartStateStart, scope))
	require.NoError(t, manager.Start(adapter.StartStatePostStart, scope))
	require.NoError(t, manager.Start(adapter.StartStateStarted, scope))
	require.NoError(t, manager.Close())
	require.NoError(t, manager.Close(), "Close must be idempotent")

	// A closed provider is no longer resolvable; a stale tag must not return a usable object.
	_, found := manager.Get("gone")
	require.False(t, found)
	require.Empty(t, manager.Providers())
}
