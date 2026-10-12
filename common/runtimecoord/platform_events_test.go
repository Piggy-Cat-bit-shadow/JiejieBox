package runtimecoord

import (
	"go/ast"
	"go/parser"
	"go/token"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// # What this file is
//
// The policy in platform_events.go decides what a platform fact means; this file drives it with an
// in-process model of the core it acts on. There is no device, no network and no clock involved, so
// every behaviour the Android work must guarantee - screen-off costs nothing, a trim cannot dial, a
// background round cannot wake a suspended endpoint, a Close wins over a late callback - is provable
// here rather than on a phone.
//
// # Why the model is a model and not a mock
//
// A mock that records "TrimMemory was called" proves only that the policy called a method. The
// counters that matter are the ones the PRODUCT observes: how many dials happened, how many sessions
// were reconnected, whether a suspended endpoint woke. So the fake implements the same semantics the
// real actors have - a progressive trim closes reusable pools and cannot dial, a reset-shaped release
// retires every transport, a background round fails against a suspended endpoint - and the assertions
// are about those consequences.

// fakePlatform is the in-process core the policy acts on.
//
// Every method is safe under the policy's lock discipline (the policy never calls a target while
// holding its own lock), and the mutex here is only so the stress test can read counters from the
// test goroutine while callbacks run.
type fakePlatform struct {
	access sync.Mutex

	// idlePools is reusable state that a progressive trim may drop.
	idlePools int
	// liveSessions is resource carrying user traffic. A trim and a pause must never touch it; a
	// reset-shaped release retires it, which is why that action costs reconnects.
	liveSessions int
	// suspended is the count of on-demand endpoints that the idle policy released.
	suspended int
	// devicePaused mirrors the core's pause state, so a demand path can tell whether the resource it
	// wants was released.
	devicePaused bool

	dials      int
	reconnects int
	urlTests   int
	trims      int
	releases   int
	pauses     int
	wakes      int
}

func (f *fakePlatform) TrimMemory(level TrimLevel) {
	f.access.Lock()
	defer f.access.Unlock()
	f.trims++
	f.idlePools = 0
	// Deliberately nothing else. A trim that dialled, resumed a suspended endpoint or touched a live
	// session would be a reconnect trigger wearing a memory-management name.
}

func (f *fakePlatform) ReleaseMemory(level TrimLevel) {
	f.access.Lock()
	defer f.access.Unlock()
	f.releases++
	f.idlePools = 0
	// The reset-shaped pass retires every transport, so each live session is re-dialled. That is the
	// cost that makes this action only justifiable against pressure that is about to end the process.
	f.reconnects += f.liveSessions
	f.dials += f.liveSessions
}

func (f *fakePlatform) DevicePause() {
	f.access.Lock()
	defer f.access.Unlock()
	f.pauses++
	f.devicePaused = true
	// Idle ON-DEMAND endpoints are eligible for suspension; a flow that is carrying traffic is not.
	// The model makes that explicit: liveSessions is untouched.
	if f.idlePools > 0 {
		f.suspended += f.idlePools
		f.idlePools = 0
	}
}

func (f *fakePlatform) DeviceWake() {
	f.access.Lock()
	defer f.access.Unlock()
	f.wakes++
	f.devicePaused = false
	// Marking eligible is not dialling: nothing is re-dialled here. The model's suspended endpoints
	// stay suspended until a real demand, which is the "rebuild lazily" rule.
}

// demand is a real flow: it wakes a suspended endpoint, and only that one.
func (f *fakePlatform) demand() {
	f.access.Lock()
	defer f.access.Unlock()
	if f.suspended > 0 {
		f.suspended--
		f.dials++
		return
	}
	f.dials++
}

// backgroundRound is a periodic health check. Against a suspended endpoint it must fail without
// waking it - the same contract adapter.ErrResourceSuspended gives the real health layer.
func (f *fakePlatform) backgroundRound() (measured bool) {
	f.access.Lock()
	defer f.access.Unlock()
	f.urlTests++
	if f.suspended > 0 {
		return false
	}
	f.dials++
	return true
}

// manualRound is a measurement a person asked for. It is demand in the product sense and may wake a
// suspended resource.
func (f *fakePlatform) manualRound() {
	f.access.Lock()
	defer f.access.Unlock()
	f.urlTests++
	if f.suspended > 0 {
		f.suspended--
	}
	f.dials++
}

// openPool adds reusable state for a later trim to drop.
func (f *fakePlatform) openPool(count int) {
	f.access.Lock()
	defer f.access.Unlock()
	f.idlePools += count
}

// platformSnapshot is a lock-free copy of the counters, so a test can compare two observations
// with require.Equal without copying the mutex (which vet rightly refuses).
type platformSnapshot struct {
	idlePools    int
	liveSessions int
	suspended    int
	devicePaused bool
	dials        int
	reconnects   int
	urlTests     int
	trims        int
	releases     int
	pauses       int
	wakes        int
}

func (f *fakePlatform) snapshot() platformSnapshot {
	f.access.Lock()
	defer f.access.Unlock()
	return platformSnapshot{
		idlePools:    f.idlePools,
		liveSessions: f.liveSessions,
		suspended:    f.suspended,
		devicePaused: f.devicePaused,
		dials:        f.dials,
		reconnects:   f.reconnects,
		urlTests:     f.urlTests,
		trims:        f.trims,
		releases:     f.releases,
		pauses:       f.pauses,
		wakes:        f.wakes,
	}
}

func (f *fakePlatform) counts() (dials, reconnects, urlTests, pools int) {
	s := f.snapshot()
	return s.dials, s.reconnects, s.urlTests, s.idlePools + s.suspended
}

// TestTrimLevelClassifiesOnlyGenuinePressureAndLifecycleFacts is the mapping table as a test.
//
// It is written as a table rather than as prose because the mapping is the deliverable: a reader must
// be able to see, for each level, whether it trims, releases or is ignored, and a new level added to
// the platform must be a visible new row.
func TestTrimLevelClassifiesOnlyGenuinePressureAndLifecycleFacts(t *testing.T) {
	for _, testCase := range []struct {
		level  TrimLevel
		action TrimAction
		why    string
	}{
		{TrimRunningModerate, TrimIgnore, "advisory: the system is low and this process is not a kill candidate"},
		{TrimRunningLow, TrimProgressive, "the process is asked to release; only reusable state goes"},
		{TrimRunningCritical, TrimRelease, "documented as kill-if-not-released for this process"},
		{TrimUIHidden, TrimIgnore, "a lifecycle fact, not a memory reading"},
		{TrimBackground, TrimProgressive, "background LRU pressure"},
		{TrimModerate, TrimProgressive, "background LRU pressure"},
		{TrimComplete, TrimProgressive, "about to be killed anyway; a reset would shed nothing worth keeping"},
		{TrimLevelUnspecified, TrimIgnore, "no trim was reported"},
		{TrimLevel(999), TrimIgnore, "an unknown level is not a fact we can act on"},
	} {
		require.Equal(t, testCase.action, testCase.level.Action(), "level %d: %s", testCase.level, testCase.why)
	}
}

// TestPlatformTrimClosesPoolsWithoutDialling is the first invariant.
func TestPlatformTrimClosesPoolsWithoutDialling(t *testing.T) {
	platform := &fakePlatform{liveSessions: 3}
	policy := NewPlatformEvents(platform)
	platform.openPool(4)

	dialsBefore, _, _, poolsBefore := platform.counts()
	policy.MemoryTrim(TrimRunningLow)

	dialsAfter, reconnectsAfter, _, poolsAfter := platform.counts()
	require.Less(t, poolsAfter, poolsBefore, "a trim must reduce the reusable resource count")
	require.Equal(t, dialsBefore, dialsAfter, "a trim must never dial")
	require.Zero(t, reconnectsAfter, "a trim must never reconnect a live session")
	require.Equal(t, 3, platform.snapshot().liveSessions, "a trim must not touch a flow that is carrying traffic")
}

// TestPlatformCriticalTrimIsTheOnlyRelease proves the severe row actually reaches the reset-shaped
// action, and that it is coalesced so a repeated system-state reading cannot become a reconnect storm.
func TestPlatformCriticalTrimIsTheOnlyRelease(t *testing.T) {
	platform := &fakePlatform{liveSessions: 2}
	policy := NewPlatformEvents(platform)

	policy.MemoryTrim(TrimRunningCritical)
	require.EqualValues(t, 1, policy.Stats().Releases)
	require.Equal(t, 2, platform.snapshot().reconnects, "the reset-shaped pass retires the live sessions")

	// RUNNING_CRITICAL is a state the system stays in, not an instant: the platform repeats it. Each
	// repeat must not advance the network generation again.
	for range 10 {
		policy.MemoryTrim(TrimRunningCritical)
	}
	require.EqualValues(t, 1, policy.Stats().Releases, "one emergency is one release")
	require.EqualValues(t, 10, policy.Stats().Coalesced)
	require.Equal(t, 2, platform.snapshot().reconnects, "a repeated reading must not reconnect again")
}

// TestPlatformRepeatedProgressiveTrimCoalesces: identical repeats are one logical event, and a
// different (escalating or de-escalating) level is a new fact.
func TestPlatformRepeatedProgressiveTrimCoalesces(t *testing.T) {
	policy := NewPlatformEvents(&fakePlatform{})
	policy.MemoryTrim(TrimRunningLow)
	policy.MemoryTrim(TrimRunningLow)
	require.EqualValues(t, 1, policy.Stats().Trims)
	require.EqualValues(t, 1, policy.Stats().Coalesced)

	policy.MemoryTrim(TrimBackground)
	require.EqualValues(t, 2, policy.Stats().Trims, "a different level is a new fact, not a repeat")
}

// TestPlatformLifecycleScenario is the Task 7 scenario end to end:
//
//	foreground -> traffic -> screen off -> background -> memory trim -> network change -> screen on -> traffic
func TestPlatformLifecycleScenario(t *testing.T) {
	platform := &fakePlatform{liveSessions: 1}
	policy := NewPlatformEvents(platform)

	// Foreground: the user is in the app.
	policy.SetScreenOn(true)
	policy.SetAppForeground(true)
	require.EqualValues(t, 0, policy.Stats().Pauses)

	// Real traffic starts a flow through a suspended on-demand endpoint.
	platform.openPool(3)
	platform.demand()

	// Screen off, then the app is backgrounded.
	policy.SetScreenOn(false)
	policy.SetAppForeground(false)
	require.EqualValues(t, 1, policy.Stats().Pauses)
	platform.openPool(3)

	// The platform reports memory pressure while everything is idle.
	policy.MemoryTrim(TrimRunningLow)

	// A network change is the network manager's business; the policy has no part in it, and the point
	// of the step is that no lifecycle fact is fabricated by it.
	platform.openPool(2)

	// Screen on, then the app comes forward: eligible again, lazily.
	policy.SetScreenOn(true)
	policy.SetAppForeground(true)

	// Real traffic at the end.
	platform.demand()

	dials, reconnects, urlTests, pools := platform.counts()
	t.Logf("scenario counters: dials=%d reconnects=%d urlTests=%d pools=%d", dials, reconnects, urlTests, pools)
	require.Zero(t, reconnects, "no step in this scenario may reconnect a session")
	require.Zero(t, urlTests, "no health round ran")
	require.Equal(t, 1, platform.snapshot().liveSessions, "the flow carrying traffic survived the whole scenario")
	require.Equal(t, 1, platform.snapshot().trims, "the trim ran exactly once")
}

// TestPlatformScreenOffAndOnIsNearZeroActivity is the "screen off + no traffic -> near-zero activity"
// assertion, with the activity being counted rather than described.
func TestPlatformScreenOffAndOnIsNearZeroActivity(t *testing.T) {
	platform := &fakePlatform{liveSessions: 1}
	policy := NewPlatformEvents(platform)
	policy.SetScreenOn(true)
	policy.SetAppForeground(true)

	dialsBefore, reconnectsBefore, urlTestsBefore, _ := platform.counts()

	policy.SetScreenOn(false)
	// Repeated screen-off facts are coalesced: an Android receiver can deliver the same broadcast
	// twice, and the second must not be a second pause.
	policy.SetScreenOn(false)
	policy.SetScreenOn(false)

	midDials, midReconnects, midURLTests, _ := platform.counts()
	require.Equal(t, dialsBefore, midDials)
	require.Equal(t, reconnectsBefore, midReconnects)
	require.Equal(t, urlTestsBefore, midURLTests)
	require.EqualValues(t, 1, policy.Stats().Pauses, "three screen-off facts are one pause")

	policy.SetScreenOn(true)
	policy.SetAppForeground(true)
	policy.SetScreenOn(true)

	_, _, _, _ = platform.counts()
	require.Zero(t, platform.snapshot().dials-dialsBefore,
		"a screen off/on cycle with no traffic must not dial: %d", platform.snapshot().dials)
	require.Zero(t, platform.snapshot().reconnects-reconnectsBefore)
	require.EqualValues(t, 1, policy.Stats().Wakes, "screen-on may nudge once, and only once")
}

// TestPlatformBackgroundRoundCannotWakeASuspendedEndpoint is the Phase 1.5 marker held from the
// lifecycle side: an automatic round under a screen-off/background policy still cannot wake.
func TestPlatformBackgroundRoundCannotWakeASuspendedEndpoint(t *testing.T) {
	platform := &fakePlatform{}
	policy := NewPlatformEvents(platform)
	policy.SetScreenOn(true)
	policy.SetAppForeground(true)
	platform.openPool(2)
	policy.SetScreenOn(false)
	policy.SetAppForeground(false)
	require.Equal(t, 2, platform.snapshot().suspended)

	dialsBefore, _, _, _ := platform.counts()
	// The real chain marks the round with adapter.ContextWithBackgroundProbe; the lifecycle state
	// must not be what decides it, and conversely must not accidentally make it demand.
	require.False(t, platform.backgroundRound(), "a background round must report 'not measured'")
	require.False(t, platform.backgroundRound())
	dialsAfter, _, urlTests, _ := platform.counts()
	require.Equal(t, dialsBefore, dialsAfter, "a background round against a suspended endpoint must not wake it")
	require.Equal(t, 2, urlTests)

	// A measurement a person asked for is demand, and may wake it - once.
	platform.manualRound()
	require.Equal(t, 1, platform.snapshot().suspended, "a manual round wakes the resource it measures")
	dialsManual, _, _, _ := platform.counts()
	require.Equal(t, dialsAfter+1, dialsManual)
}

// TestPlatformScreenOnDefersTheWakeUntilTheAppIsInFront proves the two axes are not one boolean.
//
// Screen on with the app still in the background is the normal state of a VPN - the user is in
// another app - and it must not run a rebind nudge on behalf of an app nobody is looking at. The
// nudge is deferred, not dropped, and is released once when the user comes back.
func TestPlatformScreenOnDefersTheWakeUntilTheAppIsInFront(t *testing.T) {
	platform := &fakePlatform{}
	policy := NewPlatformEvents(platform)
	policy.SetScreenOn(true)
	policy.SetAppForeground(true)
	policy.SetScreenOn(false)
	policy.SetAppForeground(false)
	require.EqualValues(t, 1, policy.Stats().Pauses)

	// Screen back on, app still backgrounded.
	policy.SetScreenOn(true)
	require.EqualValues(t, 0, policy.Stats().Wakes, "no nudge for an app that is not in front")
	require.False(t, policy.AppForeground())

	// The app comes forward: exactly one deferred nudge.
	policy.SetAppForeground(true)
	require.EqualValues(t, 1, policy.Stats().Wakes)
	policy.SetAppForeground(true)
	policy.SetScreenOn(true)
	require.EqualValues(t, 1, policy.Stats().Wakes, "a repeated foreground fact is not a second nudge")

	// And with the app already in front, a later screen-on is immediate.
	policy.SetScreenOn(false)
	policy.SetScreenOn(true)
	require.EqualValues(t, 2, policy.Stats().Wakes)
}

// TestPlatformExplicitDeviceWakeIsAuthoritative: an unlock event is the LX 041 trigger, so it forces
// the nudge even when the app axis has not caught up.
func TestPlatformExplicitDeviceWakeIsAuthoritative(t *testing.T) {
	platform := &fakePlatform{}
	policy := NewPlatformEvents(platform)
	policy.SetScreenOn(false)

	policy.DeviceWake()
	policy.DeviceWake()
	require.EqualValues(t, 1, platform.snapshot().wakes, "the nudge runs for the wake, not for every repeated fact")
	require.EqualValues(t, 1, policy.Stats().Coalesced)
	on, known := policy.ScreenOn()
	require.True(t, on)
	require.True(t, known)
}

// TestPlatformFirstUserPresentIsNotSwallowedByASeededScreenOn is the defect the edge latch exists for.
//
// # The sequence that failed
//
//	attach -> the platform seeds setScreenOn(true)     (a LEVEL fact: the screen is interactive)
//	then the FIRST real USER_PRESENT arrives -> reportDeviceWake -> DeviceWake
//
// The old guard asked the level whether the resume edge had already been consumed:
//
//	if p.screenKnown && p.screenOn && !p.paused && !p.wakePending
//
// Every one of those four terms is already true after the seed - the platform stated a level, nothing
// had been paused and nothing was pending - so the first real user-present was coalesced away and no
// nudge was ever published. A screen-on may be nothing but a lock-screen notification lighting up; a
// level fact cannot answer whether an EDGE happened.
func TestPlatformFirstUserPresentIsNotSwallowedByASeededScreenOn(t *testing.T) {
	platform := &fakePlatform{}
	policy := NewPlatformEvents(platform)

	// Attach: the platform seeds the screen level. A level fact owes no resume nudge by itself.
	policy.SetScreenOn(true)
	require.Zero(t, platform.snapshot().wakes, "a seeded screen-on is a level: it must not nudge on its own")
	require.EqualValues(t, 0, policy.Stats().Wakes)

	// The first real USER_PRESENT, with the app axis not caught up yet - the case DeviceWake is
	// documented as authoritative for.
	require.False(t, policy.AppForeground())
	policy.DeviceWake()

	require.EqualValues(t, 1, platform.snapshot().wakes,
		"the first user-present never reached the target: a seeded screen-on swallowed the resume edge")
	require.EqualValues(t, 1, policy.Stats().Wakes, "the published nudge is counted once")
	require.Zero(t, policy.Stats().Coalesced, "nothing was repeated, so nothing may be coalesced away")
	require.True(t, nudgedForThisAwakePeriod(policy), "the published nudge must be the edge the period spent")
}

// TestPlatformRepeatedUserPresentCoalesces: the same user-present delivered twice is one logical
// event, because a receiver re-registration replays USER_PRESENT and a second nudge would be a second
// rebind opportunity for no new event. The reason the repeat may be dropped is that the FIRST one
// published a nudge - not that the screen level happens to read "awake".
func TestPlatformRepeatedUserPresentCoalesces(t *testing.T) {
	platform := &fakePlatform{}
	policy := NewPlatformEvents(platform)
	policy.SetScreenOn(true)

	policy.DeviceWake()
	policy.DeviceWake()

	require.EqualValues(t, 1, platform.snapshot().wakes, "two user-present facts in one awake period are one nudge")
	require.EqualValues(t, 1, policy.Stats().Wakes)
	require.EqualValues(t, 1, policy.Stats().Coalesced, "the repeat is counted as coalesced, not silently dropped")
}

// TestPlatformScreenOffStartsANewWakePeriod: the edge is spent per awake period, not per process. A
// screen-off ends the period, so the next use owes its own nudge; a latch that survived the sleep
// would leave every later wake unnudged, which is the C01 defect seen from the other end.
func TestPlatformScreenOffStartsANewWakePeriod(t *testing.T) {
	platform := &fakePlatform{}
	policy := NewPlatformEvents(platform)
	policy.SetScreenOn(false)

	// First awake period: one nudge, and the repeat is coalesced.
	policy.DeviceWake()
	policy.DeviceWake()
	require.EqualValues(t, 1, platform.snapshot().wakes, "the first awake period publishes one nudge")
	require.EqualValues(t, 1, policy.Stats().Coalesced)

	// Screen off: the period ends. The pause is the device axis' own statement of the same fact.
	policy.SetScreenOn(false)
	require.EqualValues(t, 2, platform.snapshot().pauses, "the second screen-off is a real transition into sleep, not a repeat")
	require.EqualValues(t, 2, policy.Stats().Pauses)

	// Second awake period: the device is used again and owes its own nudge.
	policy.DeviceWake()
	policy.DeviceWake()
	require.EqualValues(t, 2, platform.snapshot().wakes, "the second awake period owes its own resume nudge")
	require.EqualValues(t, 2, policy.Stats().Wakes)
	require.EqualValues(t, 2, policy.Stats().Coalesced)

	// A third period through the level route, so "per period" is not a property of DeviceWake alone:
	// the user is in front of the app, the device sleeps, and the screen comes back.
	policy.SetAppForeground(true)
	require.EqualValues(t, 2, platform.snapshot().wakes, "a foreground fact with no debt owed publishes nothing")
	policy.SetScreenOn(false)
	policy.SetScreenOn(true)
	require.EqualValues(t, 3, platform.snapshot().wakes, "the level route back from sleep owes a nudge too")
	require.EqualValues(t, 3, policy.Stats().Wakes)
}

// TestPlatformLevelNudgeSpendsTheEdgeForTheAwakePeriod: the level path CAN publish a resume nudge -
// screen off with the user in front of the app, screen back on, and recomputeDeviceLocked decides
// deviceActionWake - and when it does, it spends the awake period's edge at that decision. A
// user-present arriving afterwards is the same period and owes nothing; without the arm at the
// decision the one wake would earn two nudges.
func TestPlatformLevelNudgeSpendsTheEdgeForTheAwakePeriod(t *testing.T) {
	platform := &fakePlatform{}
	policy := NewPlatformEvents(platform)
	policy.SetScreenOn(true)
	policy.SetAppForeground(true)

	policy.SetScreenOn(false)
	// The screen returns with the user already in front of this app: the level path wakes.
	policy.SetScreenOn(true)

	require.EqualValues(t, 1, platform.snapshot().wakes,
		"the level path reached no nudge at all, so this test could not tell whether the edge was spent")
	require.EqualValues(t, 1, policy.Stats().Wakes)

	// The unlock a real user-present delivers afterwards.
	policy.DeviceWake()
	require.EqualValues(t, 1, platform.snapshot().wakes,
		"one awake period earned a second resume nudge: the level path published one without spending the edge")
	require.EqualValues(t, 1, policy.Stats().Wakes)
	require.EqualValues(t, 1, policy.Stats().Coalesced)
	require.True(t, nudgedForThisAwakePeriod(policy), "the level path published a nudge, so it must have spent the edge")
}

// TestPlatformForegroundIsNotAResumeEdge: the app axis has no resume edge of its own. It can RELEASE
// a wake the device axis deferred, and that release is a nudge - but a foreground transition that
// published nothing must not spend the awake period's edge, or the real user-present that follows is
// coalesced away by a fact nobody asked for.
func TestPlatformForegroundIsNotAResumeEdge(t *testing.T) {
	// Half one: foreground with nothing deferred. The platform seeded "screen on" and the user opens
	// this app; the device axis owes nothing, so this publishes nothing and must spend nothing.
	platform := &fakePlatform{}
	policy := NewPlatformEvents(platform)
	policy.SetScreenOn(true)
	policy.SetAppForeground(true)
	require.Zero(t, platform.snapshot().wakes, "foreground released nothing, so it may not nudge")
	require.Zero(t, platform.snapshot().pauses)

	policy.DeviceWake()
	require.EqualValues(t, 1, platform.snapshot().wakes,
		"a foreground transition that published nothing spent the awake period's resume edge")

	// Half two: foreground releasing a deferred wake did publish a nudge, so the edge IS spent for
	// this period - it is the period's one nudge, not a second one on top of a user-present.
	deferred := &fakePlatform{}
	deferredPolicy := NewPlatformEvents(deferred)
	deferredPolicy.SetScreenOn(false)
	deferredPolicy.SetScreenOn(true) // screen back on, nobody in front: deferred, not dropped
	require.Zero(t, deferred.snapshot().wakes, "a deferred wake waits for the user")

	deferredPolicy.SetAppForeground(true)
	require.EqualValues(t, 1, deferred.snapshot().wakes, "the deferred nudge is released exactly once")

	deferredPolicy.DeviceWake()
	require.EqualValues(t, 1, deferred.snapshot().wakes,
		"the released deferred wake is this period's one edge: a user-present in the same period owes nothing")
	require.EqualValues(t, 1, deferredPolicy.Stats().Coalesced)

	// Half three, the mirror image of the wakePending mirror: an explicit user-present SUPERSEDES a
	// deferred wake - it publishes the nudge itself and clears the debt - so the foreground that
	// follows must not publish the deferred one a second time.
	superseded := &fakePlatform{}
	supersededPolicy := NewPlatformEvents(superseded)
	supersededPolicy.SetScreenOn(false)
	supersededPolicy.SetScreenOn(true) // deferred
	supersededPolicy.DeviceWake()      // the user is present: this publishes the nudge
	require.EqualValues(t, 1, superseded.snapshot().wakes)

	supersededPolicy.SetAppForeground(true)
	require.EqualValues(t, 1, superseded.snapshot().wakes,
		"the foreground released a deferred wake the explicit user-present had already superseded")
}

// nudgedForThisAwakePeriod reads the edge latch, under the policy's own lock.
//
// Every other assertion in this file is about what the TARGET observed, because a private field only
// proves what the code that wrote it happened to leave behind. This read is here for the opposite
// reason: the latch and the published nudge must not drift, and reading the latch directly is the only
// way to assert that the arm happens at the publish rather than in some second place that agrees by
// accident today. It is always checked alongside, never instead of, the target's own count.
func nudgedForThisAwakePeriod(policy *PlatformEvents) bool {
	policy.access.Lock()
	defer policy.access.Unlock()
	return policy.deviceWakeNudged
}

// TestPlatformCloseIsAFloor proves a fact after Close is a counted no-op rather than a resurrection,
// and that Close waits for a target call that is already in flight.
func TestPlatformCloseIsAFloor(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	platform := &blockingTarget{entered: entered, release: release}
	policy := NewPlatformEvents(platform)

	done := make(chan struct{})
	go func() {
		// Passes the closed check and blocks inside the target call.
		policy.MemoryTrim(TrimRunningLow)
		close(done)
	}()
	<-entered

	closed := make(chan struct{})
	go func() {
		policy.Close()
		close(closed)
	}()

	select {
	case <-closed:
		t.Fatal("Close returned while a target call was still in flight: the core could be mutated after teardown returned")
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	<-done
	<-closed

	before := policy.Stats()
	policy.MemoryTrim(TrimRunningLow)
	policy.SetScreenOn(false)
	policy.SetAppForeground(true)
	policy.DeviceWake()
	after := policy.Stats()
	require.Equal(t, before.Trims, after.Trims, "a fact after Close must not trim")
	require.Equal(t, before.Releases, after.Releases, "a fact after Close must not release")
	require.Equal(t, before.Pauses, after.Pauses, "a fact after Close must not pause")
	require.Equal(t, before.Wakes, after.Wakes, "a fact after Close must not wake")
	require.Equal(t, before.AfterClose+4, after.AfterClose, "each ignored fact is counted")
	policy.Close() // idempotent
}

type blockingTarget struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingTarget) TrimMemory(TrimLevel) {
	close(b.entered)
	<-b.release
}
func (b *blockingTarget) ReleaseMemory(TrimLevel) {}
func (b *blockingTarget) DevicePause()            {}
func (b *blockingTarget) DeviceWake()             {}

// TestPlatformStaleCallbackFromAnOldBoxCannotTouchANewOne is the resurrection case.
//
// Two policies stand for two Boxes. The old one is closed, the new one is live, and a callback that
// was already on its way from the old platform is delivered. It must touch neither the new core nor
// the old one.
func TestPlatformStaleCallbackFromAnOldBoxCannotTouchANewOne(t *testing.T) {
	oldPlatform := &fakePlatform{liveSessions: 1}
	newPlatform := &fakePlatform{liveSessions: 1}
	oldPolicy := NewPlatformEvents(oldPlatform)
	newPolicy := NewPlatformEvents(newPlatform)

	oldPolicy.SetScreenOn(false)
	oldPolicy.Close()

	newPolicy.SetScreenOn(true)
	newPolicy.SetAppForeground(true)
	newBefore := newPlatform.snapshot()

	// The stale callback: the old platform drops a screen-on and a trim on us after teardown.
	oldPolicy.SetScreenOn(true)
	oldPolicy.MemoryTrim(TrimRunningLow)
	oldPolicy.SetAppForeground(true)

	require.Equal(t, newBefore, newPlatform.snapshot(), "a stale callback mutated a Box it does not own")
	require.GreaterOrEqual(t, oldPolicy.Stats().AfterClose, uint64(3))
	require.Zero(t, newPolicy.Stats().AfterClose)
}

// TestPlatformStress runs the cycles the Android lifecycle produces in the field and asserts the
// invariants that a per-event bug would break: no reconnect storm, no resurrection, no unbounded
// growth.
func TestPlatformStress(t *testing.T) {
	runtime.GC()
	goroutinesBefore := runtime.NumGoroutine()

	platform := &fakePlatform{liveSessions: 2}
	policy := NewPlatformEvents(platform)
	policy.SetScreenOn(true)
	policy.SetAppForeground(true)

	for range 100 {
		policy.SetScreenOn(false)
		policy.SetScreenOn(true)
		policy.SetAppForeground(true)
	}
	for range 50 {
		policy.SetAppForeground(false)
		policy.SetAppForeground(true)
	}
	policy.SetScreenOn(false)
	policy.SetAppForeground(false)
	for range 50 {
		policy.MemoryTrim(TrimRunningLow)
		platform.openPool(2)
	}

	// A burst of repeated screen-on facts, which is what a ConnectivityManager burst looks like from
	// the lifecycle side: several deliveries for one real transition. The policy has no connectivity
	// API at all - a network change is the network manager's generation machinery, and that machinery
	// is what absorbs the burst. What is asserted here is the negative: the repeated facts neither
	// fabricate a pause nor dial, and the deferred wake they owe is released exactly ONCE when the
	// user actually comes back.
	pausesBefore := policy.Stats().Pauses
	wakesBefore := policy.Stats().Wakes
	dialsBefore, reconnectsBefore, _, _ := platform.counts()
	for range 25 {
		policy.SetScreenOn(true)
	}
	require.Equal(t, pausesBefore, policy.Stats().Pauses)
	require.Equal(t, wakesBefore, policy.Stats().Wakes, "a repeated screen-on fact is not a nudge")
	require.Equal(t, dialsBefore, platform.snapshot().dials)
	require.Equal(t, reconnectsBefore, platform.snapshot().reconnects)

	policy.SetAppForeground(true)
	require.Equal(t, wakesBefore+1, policy.Stats().Wakes,
		"the deferred nudge is released once, not once per repeated fact")

	// A trim during a transition: the trim still only reduces.
	platform.openPool(4)
	policy.MemoryTrim(TrimBackground)
	require.Zero(t, platform.snapshot().reconnects)

	// Close while an event callback is in flight, from a second policy.
	entered := make(chan struct{})
	release := make(chan struct{})
	closingPolicy := NewPlatformEvents(&blockingTarget{entered: entered, release: release})
	go closingPolicy.MemoryTrim(TrimRunningLow)
	<-entered
	closed := make(chan struct{})
	go func() {
		closingPolicy.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close did not wait for the in-flight release")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	<-closed

	// No reconnect storm: the only resets were the ones the reset-shaped levels earned, and each
	// stress phase coalesced its repeats.
	require.LessOrEqual(t, platform.snapshot().releases, 1)

	policy.Close()
	runtime.GC()
	require.LessOrEqual(t, runtime.NumGoroutine(), goroutinesBefore+2,
		"the policy must not leave a goroutine behind: %d -> %d", goroutinesBefore, runtime.NumGoroutine())
}

// TestPlatformPolicyCreatesNoGoroutineOrTimer is the structural half of the no-polling requirement.
//
// Go exposes no timer count through runtime/metrics, so a numerical assertion would be a guess. The
// policy's source is parsed instead: no `go` statement, no time.AfterFunc/NewTimer/NewTicker and no
// package-level channel that a worker could be reading. That is a property of the file, so it cannot
// regress silently the way a claim in a comment can.
func TestPlatformPolicyCreatesNoGoroutineOrTimer(t *testing.T) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "platform_events.go", nil, parser.SkipObjectResolution)
	require.NoError(t, err)

	var banned []string
	ast.Inspect(file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.GoStmt:
			banned = append(banned, "go statement")
		case *ast.CallExpr:
			selector, isSelector := typed.Fun.(*ast.SelectorExpr)
			if !isSelector {
				return true
			}
			packageName, isIdent := selector.X.(*ast.Ident)
			if !isIdent || packageName.Name != "time" {
				return true
			}
			switch selector.Sel.Name {
			case "AfterFunc", "NewTimer", "NewTicker", "Tick", "After", "Sleep":
				banned = append(banned, "time."+selector.Sel.Name)
			}
		}
		return true
	})
	require.Empty(t, banned, "the platform policy must stay event-driven: %s", strings.Join(banned, ", "))
}

// TestPlatformNoTargetIsInert: a platform with no runtime must not panic.
func TestPlatformNoTargetIsInert(t *testing.T) {
	policy := NewPlatformEvents(nil)
	policy.SetScreenOn(false)
	policy.SetScreenOn(true)
	policy.SetAppForeground(true)
	policy.MemoryTrim(TrimRunningCritical)
	policy.DeviceWake()
	policy.Close()
	require.EqualValues(t, 1, policy.Stats().Releases)
}
