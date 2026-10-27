package route

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSystemResumedResetsOnTheSameNetwork is the macOS lid-close recovery contract.
//
// The interface fingerprint can be identical before and after a sleep - same Wi-Fi, same gateway,
// same SSID - so no environment boundary is owed and no interface callback fires. The resume still
// has to reset, because the sockets the outbound pools hold were black-holed while the radio was
// off. This is the same action the Windows power listener performs on EVENT_RESUME.
func TestSystemResumedResetsOnTheSameNetwork(t *testing.T) {
	manager := newPowerHarness(t)
	router := manager.router.(*countingRouter)
	before := dnsResetCount(router)
	generation := manager.NetworkResetGeneration()

	manager.SystemResumed()

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

	manager.SystemResumed()

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

	manager.SystemResumed()
	manager.SystemResumed()

	require.Equal(t, before+1, dnsResetCount(router),
		"one physical resume produced two resets, so a dial started between them would be cancelled")
}

// TestSystemResumedBeforeStartIsIgnored covers the window in which a suspend watchdog can fire but
// the manager has no run context yet: claiming a transition there would leave the network unsettled
// on a network that never moved.
func TestSystemResumedBeforeStartIsIgnored(t *testing.T) {
	manager := &NetworkManager{}
	manager.SystemResumed()
	require.Zero(t, manager.NetworkResetGeneration())
}
