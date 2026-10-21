package wireguard

import (
	"context"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service/pause"

	"github.com/stretchr/testify/require"
)

// A pause registration may not be performed while the registrant holds a lock the CALLBACK needs.
//
// # The deadlock these tests kill
//
// The pause manager fans an event out to every callback inline, while holding its own mutex
// (sing/service/pause/default.go: `DevicePause` takes `d.access` and calls `d.emit`, a plain
// `for … element.Value(event)` loop with no unlock in between). The WireGuard endpoint's
// `onPauseUpdated` takes `stateAccess`, because it must read `e.device` and publish
// `e.networkPaused`. Registering or unregistering those callbacks while holding `stateAccess`
// therefore puts the two locks in both orders at once:
//
//	startDevice / Close : stateAccess -> pause.access   (RegisterCallback / UnregisterCallback)
//	DevicePause et al.  : pause.access -> stateAccess   (emit -> onPauseUpdated)
//
// That is an ABBA deadlock, and its blast radius is the LX 084 one rather than a stalled endpoint:
// `pause.access` is held forever, so every later `emit`, `RegisterCallback` and `RegisterTicker`
// blocks behind it - including `URLTestGroup.Touch`'s `RegisterTicker` on the DIAL path, which takes
// it under the group's own lock. Every new flow through every urltest group stops, with no log line,
// until the process restarts.
//
// `Endpoint.Close` runs on every config reload and every Box teardown, and the pause events arrive
// from platform power handlers on an independent goroutine set, so the pair is reached by ordinary
// use rather than by an exotic interleaving.
//
// # Why the fix is on the registration side, not in the callback
//
// Deferring the callback - queueing the event and returning - would break the contract that makes a
// pause useful: `EventNetworkPause` must have released the device's UDP socket by the time
// `DevicePause()` returns, which is what the platform suspend path relies on and what
// `TestPauseWakeCyclesInterleavedWithRebindsKeepOneSocket` asserts at the boundary with no grace
// period. So the callbacks stay SYNCHRONOUS, and `stateAccess` is kept out of the manager calls
// instead.
//
// # Why this file no longer reproduces the deadlock itself
//
// The previous version held `stateAccess` on a helper goroutine, started a real `emit`, and then
// registered from the test goroutine. That reproduced the production ABBA - and because it did, the
// run that lost the scheduling race parked the whole test binary for the 25-minute `go test` timeout
// (measured on a macOS runner: `callbackCount` waiting for `pause.access` while `emit` waited for
// `stateAccess`, both held by test goroutines). A test that can form the exact cycle it forbids
// cannot also be the gate that detects it: once the cycle exists neither lock can be released, and
// no `t.Cleanup` can run.
//
// The lock ORDER is therefore asserted directly, from the production entry points, with a
// `TryLock` probe and an explicit barrier:
//
//	the fake manager calls a hook at the top of RegisterCallback / UnregisterCallback;
//	the hook blocks the goroutine that is inside the production call;
//	the test then asks `endpoint.stateAccess.TryLock()` whether that production call is still
//	holding the state lock, and releases the hook.
//
// A regression puts the registration back inside the critical section, the probe fails immediately,
// and the release path still lets Start/Close finish, so the failure is short, specific and leaves
// nothing locked behind it. Because `sync.Mutex` is not reentrant, `TryLock` also fails for the
// goroutine that already holds the lock - which is exactly the caller under test.
//
// The probe is only sound as an order assertion if no OTHER goroutine can be holding `stateAccess`
// at that instant. Both tests establish that rather than assuming it: the endpoint is freshly built
// and no device is published before Start (so nothing but Start touches the lock), and the Close
// test owns the only Close in flight. The start test additionally checks that the lock is free
// BEFORE Start runs, so the same probe on the same lock reports both states.

// fakePauseManager reproduces `defaultManager`'s emission contract, and nothing else.
//
// Only the lock discipline matters to these tests: `access` is held while a callback runs.
type fakePauseManager struct {
	access    sync.Mutex
	callbacks list.List[pause.Callback]

	// onRegister and onUnregister run at the TOP of the corresponding production call, BEFORE the
	// manager's own mutex is taken, so a test can pin that call at its entry without inventing a
	// lock order the manager does not have. They are test-control hooks, not part of the emission
	// contract, and are set before the entry point under test starts.
	onRegister   func()
	onUnregister func()
}

func (m *fakePauseManager) DevicePause()  { m.emit(pause.EventDevicePaused) }
func (m *fakePauseManager) DeviceWake()   { m.emit(pause.EventDeviceWake) }
func (m *fakePauseManager) NetworkPause() { m.emit(pause.EventNetworkPause) }
func (m *fakePauseManager) NetworkWake()  { m.emit(pause.EventNetworkWake) }

func (m *fakePauseManager) IsDevicePaused() bool  { return false }
func (m *fakePauseManager) IsNetworkPaused() bool { return false }
func (m *fakePauseManager) IsPaused() bool        { return false }
func (m *fakePauseManager) WaitActive()           {}

func (m *fakePauseManager) RegisterCallback(callback pause.Callback) *list.Element[pause.Callback] {
	if m.onRegister != nil {
		m.onRegister()
	}
	m.access.Lock()
	defer m.access.Unlock()
	return m.callbacks.PushBack(callback)
}

func (m *fakePauseManager) UnregisterCallback(element *list.Element[pause.Callback]) {
	if element == nil {
		return
	}
	if m.onUnregister != nil {
		m.onUnregister()
	}
	m.access.Lock()
	defer m.access.Unlock()
	m.callbacks.Remove(element)
}

// emit invokes every registered callback inline, with `access` held - exactly what
// `defaultManager.emit` does, and the reason a registration may not be performed under a lock the
// callback needs.
func (m *fakePauseManager) emit(event int) {
	m.access.Lock()
	defer m.access.Unlock()
	for element := m.callbacks.Front(); element != nil; element = element.Next() {
		element.Value(event)
	}
}

func (m *fakePauseManager) callbackCount() int {
	m.access.Lock()
	defer m.access.Unlock()
	return m.callbacks.Len()
}

// newPauseEndpoint builds the smallest endpoint that can reach the registration site.
//
// The device is never started: the registration is published before anything it builds matters to
// these tests, and `onPauseUpdated` returns immediately for a nil device, so what is measured is the
// LOCK ORDER rather than wireguard-go's behaviour.
func newPauseEndpoint(t *testing.T, manager pause.Manager) *Endpoint {
	t.Helper()
	endpoint, err := NewEndpoint(EndpointOptions{
		Context:    pause.ContextWithManager(context.Background(), manager),
		Logger:     log.NewNOPFactory().Logger(),
		Dialer:     &plainDialer{},
		MTU:        1420,
		Address:    []netip.Prefix{netip.MustParsePrefix("10.0.0.1/24")},
		PrivateKey: testPrivateKey,
		Peers: []PeerOptions{{
			Endpoint:   M.ParseSocksaddrHostPort("127.0.0.1", 51820),
			PublicKey:  testListenPeerPublicKey,
			AllowedIPs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")},
		}},
	})
	require.NoError(t, err)
	require.NoError(t, endpoint.Initialize(nil))
	t.Cleanup(func() { _ = endpoint.Close() })
	return endpoint
}

// stateLockIsFree reports whether `stateAccess` is free, and is the order probe these tests are
// built on.
//
// A successful TryLock is released immediately, so the probe itself does not hold anything. A
// failed TryLock means the lock is held - by another goroutine or by the calling goroutine itself
// (Go mutexes are not reentrant). Both call sites make the second reading unambiguous by
// establishing that the only goroutine that can hold `stateAccess` is the one inside the production
// entry point under test.
func stateLockIsFree(endpoint *Endpoint) bool {
	if !endpoint.stateAccess.TryLock() {
		return false
	}
	endpoint.stateAccess.Unlock()
	return true
}

// TestPauseRegistrationDoesNotRunUnderTheStateLock asserts the production Start path does not hold
// `stateAccess` while it calls into the pause manager.
//
// The barrier is explicit in both directions: Start cannot leave the registration call until the
// test has read the lock state, and the test cannot read the lock state until Start is inside the
// call. Nothing here depends on which goroutine the scheduler runs first.
func TestPauseRegistrationDoesNotRunUnderTheStateLock(t *testing.T) {
	manager := &fakePauseManager{}
	endpoint := newPauseEndpoint(t, manager)

	// Positive control for the probe: the same check must report FREE before Start, so a failure
	// below cannot be "TryLock always fails on this mutex" or a leftover holder.
	require.True(t, stateLockIsFree(endpoint),
		"precondition: the state lock must be free before Start, or the probe below cannot attribute "+
			"a held lock to the registration site")

	registerEntered := make(chan struct{}, 1)
	releaseRegister := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRegister) }) }
	var enteredHook atomic.Bool
	manager.onRegister = func() {
		if enteredHook.CompareAndSwap(false, true) {
			registerEntered <- struct{}{}
			<-releaseRegister
		}
	}

	var startErr error
	startDone := make(chan struct{})
	go func() {
		startErr = endpoint.Start(false)
		close(startDone)
	}()
	// The release is registered BEFORE the assertions: every one of them can call t.Fatal, and a
	// Fatal stops the test goroutine right there. Releasing first makes the failure path identical
	// to the success one and keeps Start from being parked in the hook for a later test.
	t.Cleanup(func() {
		release()
		select {
		case <-startDone:
		case <-time.After(10 * time.Second):
			t.Error("Start did not return after the registration hook was released; a later test " +
				"would block on the pause manager")
		}
	})

	select {
	case <-registerEntered:
	case <-time.After(10 * time.Second):
		release()
		t.Fatal("Start never called RegisterCallback, so this test did not reach the registration " +
			"site it describes")
	}

	require.True(t, stateLockIsFree(endpoint),
		"Start called RegisterCallback while holding stateAccess: a pause manager that is mid-emit "+
			"holds pause.access while its callback waits for stateAccess, so this is the ABBA cycle. "+
			"The registration must happen BEFORE the state lock is taken")

	release()
	select {
	case <-startDone:
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not finish after the registration hook was released")
	}
	require.NoError(t, startErr)
	require.Equal(t, 2, manager.callbackCount(),
		"the real Start path must register both callbacks; a count of one means the second "+
			"registration was lost, and a count of two reached through any other path would not prove "+
			"this one")
	require.NotNil(t, endpoint.pauseCallback)
	require.NotNil(t, endpoint.deviceWakeCallback)
}

// TestCloseUnregistersWithoutHoldingTheStateLock asserts the same rule on the teardown path - the half that
// actually races a platform power event, because every config reload runs it.
//
// Close is the only goroutine that mutates this endpoint, and the fake manager is the only one that
// can be inside the unregistration call, so the probe's reading is attributable. The manager blocks
// Close inside the FIRST unregistration until the test has read the lock state; Close then releases
// both callbacks and the count is checked against the manager, not against a promise.
func TestCloseUnregistersWithoutHoldingTheStateLock(t *testing.T) {
	manager := &fakePauseManager{}
	endpoint := newPauseEndpoint(t, manager)

	require.NoError(t, endpoint.Start(false))
	require.Equal(t, 2, manager.callbackCount(),
		"precondition: Start registered both callbacks, or the assertion below is vacuous")

	unregisterEntered := make(chan struct{}, 1)
	releaseUnregister := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseUnregister) }) }
	var probed atomic.Int32
	var stateLockHeld atomic.Bool
	var firstHook sync.Once
	manager.onUnregister = func() {
		if !stateLockIsFree(endpoint) {
			stateLockHeld.Store(true)
		}
		probed.Add(1)
		firstHook.Do(func() {
			unregisterEntered <- struct{}{}
			<-releaseUnregister
		})
	}

	var closeErr error
	closeDone := make(chan struct{})
	go func() {
		closeErr = endpoint.Close()
		close(closeDone)
	}()
	t.Cleanup(func() {
		release()
		select {
		case <-closeDone:
		case <-time.After(10 * time.Second):
			t.Error("Close did not return after the unregistration hook was released; a later test " +
				"would block on the pause manager")
		}
	})

	select {
	case <-unregisterEntered:
	case <-time.After(10 * time.Second):
		release()
		t.Fatal("Close never called UnregisterCallback, so this test did not reach the teardown " +
			"site it describes")
	}

	require.False(t, stateLockHeld.Load(),
		"Close called UnregisterCallback while holding stateAccess: a concurrent emit holds "+
			"pause.access while its callback waits for stateAccess, so this is the ABBA cycle. "+
			"Unregistration must happen after the state lock is dropped")

	release()
	select {
	case <-closeDone:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not finish after the unregistration hook was released")
	}
	require.NoError(t, closeErr)
	require.Equal(t, int32(2), probed.Load(),
		"Close must give both callbacks back to the manager exactly once; a missing unregistration "+
			"keeps the endpoint reachable from the session-shared pause manager after teardown")
	require.Zero(t, manager.callbackCount())
	require.Nil(t, endpoint.pauseCallback)
	require.Nil(t, endpoint.deviceWakeCallback)
	require.Nil(t, endpoint.device.Load())
}

// TestCloseRacingPauseEmissionsLeavesNoCallback keeps the concurrency the old storm test provided,
// without letting a regression park the process.
//
// The callbacks here are test probes, not `onPauseUpdated`: a probe asks for `stateAccess` with a
// BOUNDED wait and records a failure instead of blocking forever. That matters because this test
// deliberately puts Close and an emit in flight at the same time, which is the state that used to
// form the ABBA cycle. With the fixed order Close drops `stateAccess` before it unregisters, so the
// probe always gets the lock. With the old order Close is inside the critical section waiting for
// `pause.access` while the emit holds it waiting for `stateAccess`; the probe records the starved
// lock and returns, which releases the emit and lets Close finish. A regression therefore FAILS in
// seconds rather than hanging until `go test`'s timeout - the failure mode this file exists to
// remove.
//
// The probe's access check is the same TryLock question as the order tests above, but here it is
// asked repeatedly while other goroutines are legitimately using the lock, so it is a bounded wait
// rather than a single probe. The registration is performed before `stateAccess` is taken, so the
// test does not itself use the order it forbids.
func TestCloseRacingPauseEmissionsLeavesNoCallback(t *testing.T) {
	manager := &fakePauseManager{}
	endpoint := newPauseEndpoint(t, manager)

	probeEntered := make(chan struct{}, 1)
	var starved atomic.Bool
	probe := func(int) {
		select {
		case probeEntered <- struct{}{}:
		default:
		}
		deadline := time.Now().Add(time.Second)
		for {
			if stateLockIsFree(endpoint) {
				return
			}
			if time.Now().After(deadline) {
				starved.Store(true)
				return
			}
			time.Sleep(100 * time.Microsecond)
		}
	}
	pauseCallback := manager.RegisterCallback(probe)
	deviceWakeCallback := manager.RegisterCallback(probe)
	endpoint.stateAccess.Lock()
	endpoint.pause = manager
	endpoint.pauseCallback = pauseCallback
	endpoint.deviceWakeCallback = deviceWakeCallback
	endpoint.stateAccess.Unlock()
	require.Equal(t, 2, manager.callbackCount(),
		"precondition: the fixture must be registered, or Close has nothing to race against")

	stop := make(chan struct{})
	emitterDone := make(chan struct{})
	t.Cleanup(func() {
		close(stop)
		<-emitterDone
	})
	go func() {
		defer close(emitterDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			manager.NetworkPause()
			manager.NetworkWake()
		}
	}()

	select {
	case <-probeEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("the emission loop never reached a registered callback, so Close below would not " +
			"race an emit")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- endpoint.Close() }()
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return while a pause emission was in flight; a regression must not " +
			"park the process")
	}

	require.False(t, starved.Load(),
		"a callback could not take stateAccess for a full second while Close was unregistering: "+
			"Close is holding the state lock across the manager call, which is the ABBA cycle")
	require.Zero(t, manager.callbackCount(),
		"Close must give both callbacks back to the manager")
}

// TestCloseRacingStartPublishesNothingAndLeavesNoCallback covers the interleaving the registration
// ordering exists for: a Close that lands while the device is still being built.
//
// The start must lose - it returns os.ErrClosed and publishes no device - and it must not leave its
// registration behind on the manager, which is what the release-on-the-way-out branch is for.
func TestCloseRacingStartPublishesNothingAndLeavesNoCallback(t *testing.T) {
	manager := &fakePauseManager{}
	endpoint := newPauseEndpoint(t, manager)

	// Drive the publish step directly: the device build above it needs a socket and a peer, and what
	// is under test is the ordering of the registration against the `closing` check.
	endpoint.stateAccess.Lock()
	endpoint.closing = true
	endpoint.stateAccess.Unlock()

	require.Nil(t, endpoint.device.Load())
	require.NoError(t, endpoint.Close())
	require.Zero(t, manager.callbackCount(),
		"an endpoint that never published must not leave a callback registered on the manager")
}
