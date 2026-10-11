package libbox

import (
	"testing"

	"github.com/sagernet/sing-box/common/runtimecoord"

	"github.com/stretchr/testify/require"
)

// This file is the ownership half of N-9's evidence, and it is separate from
// platform_events_device_axis_test.go for one reason: it is the only test that names the target's
// `remember` field, so it is the one file that cannot compile against the code as it was before the
// fix. Keeping it apart is what lets the window tests be run RED against the pre-fix production file
// without a compile error standing in for a failed assertion.

// TestPlatformDeviceLevelMirrorIsWrittenOnlyForAPublishedAction is the ownership half.
//
// The target's job is to execute a decision the shared policy already made, so it may write the mirror
// exactly once per published ACTION and never for a FACT that the policy coalesced, deferred or
// dropped. The count is taken on the target itself, wrapped around the product's own mirror write, and
// it is reconciled against the policy's own counters rather than against a list of expected calls:
//
//	writes == Pauses + Wakes
//
// is the whole property. A target that wrote the mirror per fact would fail it, and so would a target
// that grew a second opinion about the device level, which is the failure this defect was made of.
func TestPlatformDeviceLevelMirrorIsWrittenOnlyForAPublishedAction(t *testing.T) {
	fixture := newPlatformEventsDeviceAxisFixture(t)
	manager := fixture.started.Instance().PauseManager()
	require.NotNil(t, manager)

	writes := 0
	policy := runtimecoord.NewPlatformEvents(&boxPlatformTarget{
		service: fixture.started,
		remember: func(paused bool) {
			writes++
			fixture.server.rememberDeviceLevel(paused)
		},
	})

	// Nothing has happened yet.
	require.Zero(t, writes)

	// Facts that earn no action: the device is reported usable, repeatedly. `PlatformEvents` decides
	// there is nothing to publish, so the target must not be called at all.
	policy.SetScreenOn(true)
	policy.SetAppForeground(true)
	for range 64 {
		policy.SetScreenOn(true)
	}
	require.Zero(t, writes, "the target wrote the mirror for a fact that earned no action")
	require.Zero(t, policy.Stats().Pauses)

	// One real pause.
	policy.SetScreenOn(false)
	require.Equal(t, 1, writes, "the published pause must reach the mirror")
	require.Equal(t, uint64(1), policy.Stats().Pauses)
	require.True(t, manager.IsDevicePaused())
	paused, touched := rememberedDeviceLevel(fixture.server)
	require.True(t, touched)
	require.True(t, paused)

	// The same fact again: the policy coalesces it against the level it already published, and the
	// target must not see a second action.
	for range 64 {
		policy.SetScreenOn(false)
	}
	require.Equal(t, 1, writes, "the target bypassed the policy's coalescing of a repeated pause")
	require.Equal(t, uint64(1), policy.Stats().Pauses)

	// The device became usable with nobody in front of this app: the wake is DEFERRED, which is a
	// decision not to publish an action. Neither the manager nor the mirror may move.
	policy.SetAppForeground(false)
	policy.SetScreenOn(true)
	require.False(t, policy.AppForeground())
	require.Equal(t, 1, writes, "the target bypassed wakePending: it acted on a deferred wake")
	require.True(t, manager.IsDevicePaused())
	paused, _ = rememberedDeviceLevel(fixture.server)
	require.True(t, paused, "the mirror moved for a wake the policy deferred")

	// The app comes forward: the deferred wake is released, and that IS an action.
	policy.SetAppForeground(true)
	require.Equal(t, 2, writes, "the released wake must reach the mirror")
	require.Equal(t, uint64(1), policy.Stats().Wakes)
	require.False(t, manager.IsDevicePaused())
	paused, _ = rememberedDeviceLevel(fixture.server)
	require.False(t, paused)

	// An explicit user-present in the same awake period is the edge latch's coalescing: the period has
	// already published its nudge, so there is no second action.
	policy.DeviceWake()
	require.Equal(t, 2, writes, "the target bypassed the device-wake edge latch")
	require.Positive(t, policy.Stats().Coalesced)

	// A screen-off ends the awake period and clears the latch, so the next user-present is a NEW
	// action - and the one after it is not.
	policy.SetScreenOn(false)
	require.Equal(t, 3, writes)
	policy.DeviceWake()
	require.Equal(t, 4, writes, "a user-present after a screen-off is a new action, not a repeat")
	policy.DeviceWake()
	require.Equal(t, 4, writes, "the second user-present in one awake period must be coalesced away")

	// The reconciliation, and the no-drift invariant it implies: one mirror write per published
	// action, and a mirror that still describes the level the manager is in.
	stats := policy.Stats()
	require.Equal(t, int(stats.Pauses+stats.Wakes), writes,
		"the target wrote the mirror %d times for %d published device actions", writes, stats.Pauses+stats.Wakes)
	require.Equal(t, uint64(4), stats.Pauses+stats.Wakes)
	paused, touched = rememberedDeviceLevel(fixture.server)
	require.True(t, touched)
	require.Equal(t, manager.IsDevicePaused(), paused,
		"the mirror and the manager disagree about the device level after %d actions", writes)
}
