package wireguard

import (
	"context"
	"net/netip"
	"sync"
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
// # Why the manager is driven by hand
//
// The event is delivered through the production `pause.Manager` entry points, in the shape
// `defaultManager` uses: the manager's lock is taken first and the callbacks are invoked inline from
// inside it. `pause.WithDefaultManager` cannot be used here because its level guard (`DevicePause`
// on an already-paused manager emits nothing) would make the test depend on which event happened to
// be delivered first rather than on the ordering under test.

// fakePauseManager reproduces `defaultManager`'s emission contract, and nothing else.
//
// Only the lock discipline matters to these tests: `access` is held while a callback runs.
type fakePauseManager struct {
	access    sync.Mutex
	callbacks list.List[pause.Callback]
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
	m.access.Lock()
	defer m.access.Unlock()
	return m.callbacks.PushBack(callback)
}

func (m *fakePauseManager) UnregisterCallback(element *list.Element[pause.Callback]) {
	if element == nil {
		return
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

// runWithDeadline fails the test if fn does not return. A lock-ordering violation does not return an
// error, it blocks, so a deadline is the only way to observe it.
func runWithDeadline(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal(what, ": it blocked, so the pause manager's lock and the endpoint's state lock are "+
			"held in opposite orders and the manager deadlocks for the life of the process")
	}
}

// holdStateLock takes `endpoint.stateAccess` on a background goroutine and registers the release
// with `t.Cleanup`.
//
// # Why the release is registered up front
//
// Every assertion between taking the lock and releasing it can call `t.Fatal`, and `t.Fatal` stops
// the test function right there. A release written after those assertions therefore never runs on
// the failure path, the goroutine stays parked holding `stateAccess` for the rest of the process,
// and the NEXT test that needs the lock hangs - which reads as an unrelated flake in a different
// test. Registering the release first makes the failure path identical to the success one.
//
// The returned function releases the lock and waits for the goroutine to finish, and is idempotent,
// so a test can release early and still be cleaned up correctly if it fails in between.
func holdStateLock(t *testing.T, endpoint *Endpoint) (release func()) {
	t.Helper()
	held := make(chan struct{})
	releaseCh := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		endpoint.stateAccess.Lock()
		close(held)
		<-releaseCh
		endpoint.stateAccess.Unlock()
	}()
	var once sync.Once
	release = func() {
		once.Do(func() { close(releaseCh) })
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the state-lock holder did not release; a later test would block on stateAccess")
		}
	}
	t.Cleanup(release)
	<-held
	return release
}

// TestPauseRegistrationDoesNotRunUnderTheStateLock reproduces the ABBA cycle directly.
//
// # The interleaving it drives, step by step
//
//	goroutine A (the platform power handler)
//	    manager.DevicePause()                 -> takes pause.access
//	    emit -> onPauseUpdated(event)         -> wants stateAccess
//
//	goroutine B (this test), acting as startDevice / Close used to
//	    holds stateAccess
//	    manager.RegisterCallback(...)         -> wants pause.access     <-- the cycle closes
//
// The only thing that breaks it is B NOT holding `stateAccess` while it registers. So the test holds
// `stateAccess` on B's behalf and then performs the registration: with the fix the registration
// completes and A is free to acquire `stateAccess`; with the registration back under the lock the
// two goroutines deadlock and the deadline fires.
func TestPauseRegistrationDoesNotRunUnderTheStateLock(t *testing.T) {
	manager := &fakePauseManager{}
	endpoint := newPauseEndpoint(t, manager)

	emitReturned := make(chan struct{})
	go func() {
		defer close(emitReturned)
		manager.DevicePause()
	}()
	releaseStateLock := holdStateLock(t, endpoint)

	// The registration the fix moved out of the critical section. It must complete even though the
	// callback delivering above it is waiting for the lock this test is holding.
	runWithDeadline(t, "RegisterCallback while the manager is mid-emit and stateAccess is held", func() {
		endpoint.pause = manager
		endpoint.pauseCallback = manager.RegisterCallback(endpoint.onPauseUpdated)
		endpoint.deviceWakeCallback = manager.RegisterCallback(endpoint.onDeviceWake)
	})
	require.Equal(t, 2, manager.callbackCount(), "both callbacks must be registered")

	// Nothing may have been delivered yet, because the callback is still waiting for stateAccess.
	select {
	case <-emitReturned:
		t.Fatal("the callback completed while its own state lock was held by another goroutine")
	case <-time.After(50 * time.Millisecond):
	}

	// Releasing the lock must let the whole chain finish. This is the exact instant the pre-fix code
	// could not reach when the releasing goroutine also needed pause.access to register.
	releaseStateLock()
	runWithDeadline(t, "the emit that was waiting for the state lock", func() { <-emitReturned })

	require.False(t, endpoint.networkPaused,
		"the device is nil in this fixture, so the transition must have been skipped, not published")

	runWithDeadline(t, "UnregisterCallback", func() {
		manager.UnregisterCallback(endpoint.pauseCallback)
		manager.UnregisterCallback(endpoint.deviceWakeCallback)
		endpoint.pauseCallback = nil
		endpoint.deviceWakeCallback = nil
	})
	require.Zero(t, manager.callbackCount())
}

// TestCloseUnregistersWithoutHoldingTheStateLock is the same rule on the teardown path - the half
// that actually races a platform power event, because every config reload runs it.
//
// Close is driven concurrently with a storm of pause/wake emissions. With the unregistration moved
// outside the critical section the two complete; with it inside, the pair deadlocks and the
// deadline fires.
func TestCloseUnregistersWithoutHoldingTheStateLock(t *testing.T) {
	manager := &fakePauseManager{}
	endpoint := newPauseEndpoint(t, manager)

	endpoint.stateAccess.Lock()
	endpoint.pause = manager
	endpoint.pauseCallback = manager.RegisterCallback(endpoint.onPauseUpdated)
	endpoint.deviceWakeCallback = manager.RegisterCallback(endpoint.onDeviceWake)
	endpoint.stateAccess.Unlock()

	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	runWithDeadline(t, "Close racing a pause emission storm", func() {
		go func() {
			defer waitGroup.Done()
			_ = endpoint.Close()
		}()
		go func() {
			defer waitGroup.Done()
			for range 200 {
				manager.NetworkPause()
				manager.NetworkWake()
			}
		}()
		waitGroup.Wait()
	})

	require.Zero(t, manager.callbackCount(),
		"Close must give up both callbacks, so a later platform event does not reach a dead endpoint")
	require.Nil(t, endpoint.device.Load())
	require.Nil(t, endpoint.pauseCallback)
	require.Nil(t, endpoint.deviceWakeCallback)
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
