package dns

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/common/resourceguard"

	"github.com/stretchr/testify/require"
)

// An optimistic refresh renews an entry the caller has already been served, so under Critical it is
// the speculative work that must yield to the flows being kept. Armed must not suppress it: the
// preparation state still needs fresh answers for failover.
func TestOptimisticRefreshIsSuppressedOnlyUnderCriticalPressure(t *testing.T) {
	clientAt := func(level resourceguard.Level) *Client {
		guard := resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), func() resourceguard.Level {
			return level
		})
		return &Client{ctx: resourceguard.NewContext(context.Background(), guard)}
	}

	require.False(t, clientAt(resourceguard.LevelNormal).backgroundRefreshSuppressed(),
		"normal pressure refreshes")
	require.False(t, clientAt(resourceguard.LevelArmed).backgroundRefreshSuppressed(),
		"armed pressure must still refresh: failover needs fresh answers")
	require.True(t, clientAt(resourceguard.LevelCritical).backgroundRefreshSuppressed(),
		"critical pressure must not spend a round trip on a speculative refresh")

	// A client built without a process budget keeps the historical behaviour.
	bare := &Client{ctx: context.Background()}
	require.False(t, bare.backgroundRefreshSuppressed())
}
