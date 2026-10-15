package wireguard

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStartRegistersThePauseCallbacks is the assertion that would have caught the P0 this file
// exists for.
//
// # The defect
//
// `34cc5834d9a` ("keep the endpoint state lock out of the pause registration") moved the pause
// registration OUT of the stateAccess critical section - which is the right fix for the ABBA cycle
// that commit documents - and deleted the two `RegisterCallback` calls without re-adding them
// anywhere. So:
//
//	registeredPause / registeredWake   declared, never assigned
//	e.pauseCallback = registeredPause  published a nil
//	e.deviceWakeCallback = registeredWake
//
// The endpoint therefore never received `EventNetworkPause`, so a network pause no longer took the
// WireGuard device down (the synchronous resource contract `onPauseUpdated` implements), and the
// DeviceWake recovery nudge never fired. Every idle endpoint kept its sockets and handshake timers
// across a suspend or a network handover.
//
// # Why no existing test could see it
//
// pause_deadlock_test.go registers the callbacks BY HAND on a fake manager
// (`endpoint.pauseCallback = manager.RegisterCallback(...)`) and never calls `Start`. That measures
// the lock ORDER, which is what those tests are for - but it means the production registration path
// had no coverage at all, so its total absence was invisible. Measured in the Phase-2 audit:
// reverting the production code to the pre-fix registration shape still left all three of those
// tests passing.
//
// # What this asserts, and why it reads the endpoint's own field
//
// Asserting on the FAKE MANAGER's callback count would pass for any registration, including one the
// test performed itself. Asserting on `endpoint.pauseCallback` / `deviceWakeCallback` is the version
// that fails when the PRODUCTION code stops registering, because those fields are what `Close`
// unregisters and what the rest of the file treats as "this endpoint is wired to pause events".
//
// The manager's count is asserted as well, and the two must agree: a handle published without a
// registration would be unregistered later as a no-op, and a registration without a published
// handle would outlive Close.
func TestStartRegistersThePauseCallbacks(t *testing.T) {
	manager := &fakePauseManager{}
	endpoint := newPauseEndpoint(t, manager)

	// The fixture's peer is a literal address, so the initial (non-postStart) pass is the one that
	// builds the device - see Start's hasDomainPeer gate.
	require.NoError(t, endpoint.Start(false), "the smallest endpoint must start")

	require.NotNil(t, endpoint.pauseCallback,
		"Start did not register onPauseUpdated, so the endpoint never sees EventNetworkPause and a "+
			"network pause no longer takes the device down. Check that the RegisterCallback calls "+
			"were not deleted again while moving the registration out of the stateAccess section")
	require.NotNil(t, endpoint.deviceWakeCallback,
		"Start did not register onDeviceWake, so the DeviceWake recovery nudge never fires and every "+
			"idle endpoint keeps its sockets and handshake timers across a suspend")

	require.Equal(t, 2, manager.callbackCount(),
		"the manager must hold exactly the endpoint's two callbacks; a handle published without a "+
			"registration, or a registration without a published handle, both show up here")
}

// TestCloseUnregistersTheCallbacksStartRegistered is the other half, driven through the real pair.
//
// The existing Close test registers by hand, so it proves only that Close CAN unregister. This
// proves that what Start registered is what Close gives back. The two are separate facts, and the
// defect above is exactly how easily a fixture can supply one of them on production's behalf.
func TestCloseUnregistersTheCallbacksStartRegistered(t *testing.T) {
	manager := &fakePauseManager{}
	endpoint := newPauseEndpoint(t, manager)

	require.NoError(t, endpoint.Start(false))
	require.Equal(t, 2, manager.callbackCount(),
		"precondition: Start registered both, or the assertion below is vacuous")

	require.NoError(t, endpoint.Close())

	require.Equal(t, 0, manager.callbackCount(),
		"Close must unregister both callbacks Start registered; a leftover registration keeps the "+
			"endpoint reachable from the session-shared pause manager after teardown")
	require.Nil(t, endpoint.pauseCallback)
	require.Nil(t, endpoint.deviceWakeCallback)
}
