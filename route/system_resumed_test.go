package route

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

// TestSystemResumedResetsOnTheSameNetwork is the macOS lid-close recovery contract.
//
// The interface fingerprint can be identical before and after a sleep - same Wi-Fi, same gateway,
// same SSID - so no environment boundary is owed and no interface callback fires. The resume still
// has to reset, because the sockets the outbound pools hold were black-holed while the radio was
// off.
func TestSystemResumedResetsOnTheSameNetwork(t *testing.T) {
	manager := newPowerHarness(t)
	router := manager.router.(*countingRouter)
	before := dnsResetCount(router)
	generation := manager.NetworkResetGeneration()

	manager.SetSleepEpisode(1)
	manager.SystemResumed(1)

	require.Equal(t, before+1, dnsResetCount(router),
		"a resume must reset the network even when the interface did not move")
	require.Greater(t, manager.NetworkResetGeneration(), generation,
		"the reset must claim a new generation so the dialer's ownership check sees the boundary")
}

// TestSystemResumedLiftsALatchedDevicePause covers the headless case: no Box calls DeviceWoke, so
// the resume must release the level itself or a Mac that slept after Pause would suppress every
// speculative subsystem for the rest of the process.
func TestSystemResumedLiftsALatchedDevicePause(t *testing.T) {
	manager := newPowerHarness(t)
	pauseManager := manager.pauseManager.(*noopPauseManager)
	pauseManager.DevicePause()
	require.True(t, pauseManager.IsDevicePaused())

	manager.SetSleepEpisode(1)
	manager.SystemResumed(1)

	require.False(t, pauseManager.IsDevicePaused(),
		"a resume must lift a device pause that no Box is left to lift")
}

// TestSystemResumedCoalescesTheTwoPlatformReports pins the dedup between the NetworkExtension wake
// and the macOS watchdog: both observe one physical resume, and the second must not start a second
// transition that would cancel a dial made just after the lid opened.
func TestSystemResumedCoalescesTheTwoPlatformReports(t *testing.T) {
	manager := newPowerHarness(t)
	router := manager.router.(*countingRouter)
	before := dnsResetCount(router)

	manager.SetSleepEpisode(1)
	manager.SystemResumed(1) // NetworkExtension wake
	manager.SystemResumed(0) // watchdog, same episode

	require.Equal(t, before+1, dnsResetCount(router),
		"one physical resume produced two resets, so a dial started between them would be cancelled")
}

// TestSystemResumedProcessesASecondSleepImmediately is the boundary the wall-clock window could not
// express: two genuine sleeps one second apart. The second sleep has its own sleep episode, so it
// must reset even though the first reset was moments ago.
func TestSystemResumedProcessesASecondSleepImmediately(t *testing.T) {
	manager := newPowerHarness(t)
	router := manager.router.(*countingRouter)
	before := dnsResetCount(router)

	manager.SetSleepEpisode(1)
	manager.SystemResumed(1)
	manager.SetSleepEpisode(2)
	manager.SystemResumed(2)

	require.Equal(t, before+2, dnsResetCount(router),
		"a fixed coalescing window would have swallowed the second, independent resume")
}

// TestSystemResumedWatchdogDetectionsAreNotSwallowed covers the headless client: no platform sleep
// episode exists, so every watchdog detection is its own episode. Two detections must produce two
// resets; the episode numbering replaces the old wall-clock window.
func TestSystemResumedWatchdogDetectionsAreNotSwallowed(t *testing.T) {
	manager := newPowerHarness(t)
	router := manager.router.(*countingRouter)
	before := dnsResetCount(router)

	manager.SystemResumedGap(time.Second)
	manager.SystemResumedGap(time.Second)

	require.Equal(t, before+2, dnsResetCount(router),
		"the watchdog must be able to report two independent suspends")
}

// TestSystemResumedShortSleepUsesTheMediumDrain pins the other half of the resume policy: a brief
// lid close may leave an active stream genuinely alive, so the medium drain - not the dead-path
// closeAll - is the right transition.
func TestSystemResumedShortSleepUsesTheMediumDrain(t *testing.T) {
	manager := newPowerHarness(t)
	recorder := &reclaimRecorder{}
	manager.connectionManager = recorder

	manager.SetSleepEpisode(1)
	manager.SystemResumed(1)

	require.Equal(t, []ReclaimReason{ReclaimNetworkTransition}, recorder.reasons,
		"a short sleep must preserve a stream that may have survived it")
}

// TestSystemResumedLongSleepUsesTheDeadPath pins the original failure: after a long sleep an active
// TCP stream can be black-holed while still looking idle to the drain. At the threshold the resume
// must close every managed flow, not only the provably idle ones.
func TestSystemResumedLongSleepUsesTheDeadPath(t *testing.T) {
	manager := newPowerHarness(t)
	recorder := &reclaimRecorder{}
	manager.connectionManager = recorder
	manager.SetSleepEpisode(1)
	ageSystemSleep(manager, time.Minute)

	manager.SystemResumed(1)

	require.Equal(t, []ReclaimReason{ReclaimDeadPath}, recorder.reasons,
		"a long sleep must reclaim with closeAll, not with the medium drain")
}

// TestSystemResumedGapAppliesTheSleepLengthPolicy covers the headless path: the watchdog has no
// platform start time, so its measured gap is the duration that decides short versus long.
func TestSystemResumedGapAppliesTheSleepLengthPolicy(t *testing.T) {
	manager := newPowerHarness(t)
	recorder := &reclaimRecorder{}
	manager.connectionManager = recorder

	manager.SystemResumedGap(time.Second)
	manager.SystemResumedGap(time.Minute)

	require.Equal(t, []ReclaimReason{ReclaimNetworkTransition, ReclaimDeadPath}, recorder.reasons,
		"the watchdog gap must select the same short/long policy as the platform sleep duration")
}

// ageSystemSleep moves the recorded sleep start into the past, so a test can exercise the long-sleep
// branch without waiting.
func ageSystemSleep(manager *NetworkManager, age time.Duration) {
	manager.systemResumeAccess.Lock()
	manager.systemSleepStartedAt = time.Now().Add(-age).Round(0)
	manager.systemResumeAccess.Unlock()
}

// reclaimRecorder records the reason the reset passed to the connection manager.
type reclaimRecorder struct {
	adapter.ConnectionManager
	reasons []ReclaimReason
}

func (r *reclaimRecorder) Reclaim(reason ReclaimReason) int {
	r.reasons = append(r.reasons, reason)
	return 0
}

// TestSystemResumedBeforeStartIsIgnored covers the window in which a suspend watchdog can fire but
// the manager has no run context yet: claiming a transition there would leave the network unsettled
// on a network that never moved.
func TestSystemResumedBeforeStartIsIgnored(t *testing.T) {
	manager := &NetworkManager{}
	manager.SystemResumed(1)
	require.Zero(t, manager.NetworkResetGeneration())
}
