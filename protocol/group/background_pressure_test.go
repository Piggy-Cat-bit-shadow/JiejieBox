package group

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/common/resourceguard"

	"github.com/stretchr/testify/require"
)

// The periodic health round is background work; a manual/forced round is not, and is deliberately
// left ungated. Armed still probes (failover needs measurements); Critical stops the periodic round
// so the probes stop competing with the flows being kept.
func TestBackgroundHealthRoundsAreSuppressedOnlyUnderCriticalPressure(t *testing.T) {
	groupAt := func(level resourceguard.Level) *URLTestGroup {
		guard := resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), func() resourceguard.Level {
			return level
		})
		return &URLTestGroup{ctx: resourceguard.NewContext(context.Background(), guard)}
	}

	require.True(t, groupAt(resourceguard.LevelNormal).backgroundRoundAllowed(),
		"normal pressure probes")
	require.True(t, groupAt(resourceguard.LevelArmed).backgroundRoundAllowed(),
		"armed pressure must still probe: failover needs measurements")
	require.False(t, groupAt(resourceguard.LevelCritical).backgroundRoundAllowed(),
		"critical pressure must stop the periodic health round")

	bare := &URLTestGroup{ctx: context.Background()}
	require.True(t, bare.backgroundRoundAllowed(),
		"a process without a guard keeps the historical behaviour")
}
