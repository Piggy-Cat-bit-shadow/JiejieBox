package route

// Q07: what a device pause actually does to an on-demand tunnel.
//
// # What this file records, and what it deliberately does not decide
//
// The audit's F-01 was a POLICY question (`docs/coupling/DECISIONS.md` D-4): the eligibility pass
// suspended a referenced on-demand endpoint whenever the device was paused, and the five endpoint
// kinds implement that as "tear the tunnel down now", whatever the tunnel is carrying. The two
// promised behaviours could not both hold - `docs/configuration/endpoint/*.md` says an on-demand
// endpoint may be disconnected "when necessary", while `common/runtimecoord/platform_events.go` says
// a device pause "does not stop forwarding, and it never terminates an active flow", and
// `experimental/libbox/platform_events.go` narrows that to "makes IDLE on-demand resources eligible
// for suspension".
//
// The decision taken is option (a) of that record: a tunnel that is CARRYING traffic is not
// suspended by a device pause, and an idle one still is. "Carrying" is payload that moved inside
// `power.Policy.OnDemandIdleAfter`, measured by the ledger in route/traffic_activity.go; the tests
// for that contract are in route/reference_activity_test.go.
//
// This file keeps the two facts that decision has to be read against:
//
//   - the teardown still happens for a tunnel with no traffic, so what the five implementations do
//     when they are told keep=false is unchanged and is asserted here rather than assumed;
//   - the verdict comes from the pause manager's callbacks, not from a re-read of the manager's
//     level. A test cannot decide the policy, and the policy did not change that rule either.
//
// # One candidate fix that was written, measured and WITHDRAWN
//
// The first version of this change made the pass re-read `pause.Manager.IsDevicePaused()` on every
// pass, on the theory that the cached flag could lag the manager after a wake the platform never
// delivered. Two measurements killed it:
//
//  1. The theory is wrong. The cache is fed by the manager's callbacks, which the manager invokes
//     synchronously from `DevicePause`/`DeviceWake`, and `Start` seeds it from the manager. A wake
//     the PLATFORM never delivers does not move `IsDevicePaused()` either - the level itself is
//     still paused - so the extra read cannot repair that case. The only divergence is a
//     registration race, which the Start seeding already covers.
//  2. It breaks a real contract. `experimental/libbox`'s device-axis test holds the reload
//     nil-instance window open with the ONE level read `box.New` performs; a second read from this
//     pass fires first while the old instance is still published, and the test fails with "the
//     service has an instance while the reload is in flight" (measured in CI run 43126821928 and
//     reproduced locally). The pass therefore keeps reading the cache, and that is asserted below.
//
// The activity exemption does not reintroduce that read: it asks the pause manager nothing at all,
// and the deadline its recheck timer is armed on is a duration, not a second opinion about the
// level.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/power"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"

	"github.com/stretchr/testify/require"
)

// fakePauseManager is the pause authority the reference manager talks to. It exposes the two facts
// separately on purpose: `paused` is what the platform reports, and the callbacks are how a fact
// usually arrives. A test can therefore stage "the fact is true but no event was delivered", which
// is the missed-wake case.
type fakePauseManager struct {
	paused atomic.Bool

	access    sync.Mutex
	callbacks list.List[pause.Callback]
}

func (m *fakePauseManager) DevicePause()                      { m.set(true, pause.EventDevicePaused) }
func (m *fakePauseManager) DeviceWake()                       { m.set(false, pause.EventDeviceWake) }
func (m *fakePauseManager) NetworkPause()                     {}
func (m *fakePauseManager) NetworkWake()                      {}
func (m *fakePauseManager) IsDevicePaused() bool              { return m.paused.Load() }
func (m *fakePauseManager) IsNetworkPaused() bool             { return false }
func (m *fakePauseManager) IsPaused() bool                    { return m.paused.Load() }
func (m *fakePauseManager) WaitActive()                       {}
func (m *fakePauseManager) WaitActiveContext(context.Context) {}

// set records the fact and, when notify is not an empty event, delivers it the way the real
// manager does.
func (m *fakePauseManager) set(paused bool, event int) {
	m.paused.Store(paused)
	if event < 0 {
		return
	}
	m.access.Lock()
	callbacks := make([]pause.Callback, 0, m.callbacks.Len())
	for element := m.callbacks.Front(); element != nil; element = element.Next() {
		callbacks = append(callbacks, element.Value)
	}
	m.access.Unlock()
	for _, callback := range callbacks {
		callback(event)
	}
}

// setWithoutNotification changes the authority's answer WITHOUT delivering an event: the platform
// woke the device but the wake callback never arrived.
func (m *fakePauseManager) setWithoutNotification(paused bool) { m.paused.Store(paused) }

func (m *fakePauseManager) RegisterCallback(callback pause.Callback) *list.Element[pause.Callback] {
	m.access.Lock()
	defer m.access.Unlock()
	return m.callbacks.PushBack(callback)
}

func (m *fakePauseManager) UnregisterCallback(element *list.Element[pause.Callback]) {
	m.access.Lock()
	defer m.access.Unlock()
	m.callbacks.Remove(element)
}

// onDemandTestEndpoint models an on-demand tunnel the way all five production kinds behave when
// they are told keep=false: the tunnel's own transport is destroyed under whatever flows it
// carries. The stream counter is the observation: a suspend that reaches an endpoint carrying
// streams is a suspend that cuts live traffic.
type onDemandTestEndpoint struct {
	adapter.Outbound

	tag string

	access      sync.Mutex
	keep        bool
	verdicts    []bool
	streams     int
	streamKills int
}

func (e *onDemandTestEndpoint) Type() string           { return "on-demand-test" }
func (e *onDemandTestEndpoint) Tag() string            { return e.tag }
func (e *onDemandTestEndpoint) Network() []string      { return nil }
func (e *onDemandTestEndpoint) Dependencies() []string { return nil }
func (e *onDemandTestEndpoint) OnDemand() bool         { return true }
func (e *onDemandTestEndpoint) Start(adapter.StartStage, *adapter.Scope) error {
	return nil
}

func (e *onDemandTestEndpoint) SetKeepIdleConnections(keep bool) {
	e.access.Lock()
	e.keep = keep
	e.verdicts = append(e.verdicts, keep)
	if !keep && e.streams > 0 {
		// What the real implementations do: MASQUE cancels the session, WireGuard downs the
		// device, OpenVPN/OpenConnect fail the session, Tailscale stops WantRunning. None of them
		// drains, and none of them closes the application's sockets, so the flow is cut.
		e.streamKills += e.streams
		e.streams = 0
	}
	e.access.Unlock()
}

func (e *onDemandTestEndpoint) openStream() {
	e.access.Lock()
	e.streams++
	e.access.Unlock()
}

func (e *onDemandTestEndpoint) snapshot() (keep bool, verdicts []bool, streams, kills int) {
	e.access.Lock()
	defer e.access.Unlock()
	return e.keep, append([]bool(nil), e.verdicts...), e.streams, e.streamKills
}

type onDemandEndpointManager struct {
	adapter.EndpointManager

	endpoint *onDemandTestEndpoint
}

func (m *onDemandEndpointManager) Endpoints() []adapter.Endpoint {
	return []adapter.Endpoint{m.endpoint}
}

func (m *onDemandEndpointManager) Get(tag string) (adapter.Endpoint, bool) {
	if tag == m.endpoint.Tag() {
		return m.endpoint, true
	}
	return nil, false
}

// pauseFixture wires the REAL reference manager over a real outbound manager, the real pause
// manager interface and one on-demand endpoint that traffic can reach.
type pauseFixture struct {
	t        *testing.T
	endpoint *onDemandTestEndpoint
	pause    *fakePauseManager
	manager  *outbound.Manager
	// reference is the manager under test. The activity tests drive its clock and read its deadlines
	// through it, so the fixture hands it out rather than keeping it private.
	reference *ReferenceManager
	// clock is the fake time source installed before Start: the activity window is a duration, and a
	// test that waited for it would be measuring the scheduler instead of the policy.
	clock *pauseClock
	// pass runs one eligibility pass deterministically: a graph publication is a real event the
	// manager emits, so the pass it triggers is the production trigger rather than a direct call.
	pass func()
}

// pauseClock is a hand-driven clock. It is safe for concurrent use because the eligibility pass reads
// it from its own goroutine while the test advances it.
type pauseClock struct {
	access sync.Mutex
	at     time.Time
}

func newPauseClock() *pauseClock {
	return &pauseClock{at: time.Date(2026, 11, 12, 3, 0, 0, 0, time.UTC)}
}

func (c *pauseClock) Now() time.Time {
	c.access.Lock()
	defer c.access.Unlock()
	return c.at
}

func (c *pauseClock) Advance(delta time.Duration) {
	c.access.Lock()
	defer c.access.Unlock()
	c.at = c.at.Add(delta)
}

func newPauseFixture(t *testing.T) *pauseFixture {
	t.Helper()
	return newPauseFixtureWithGovernor(t, nil, newPauseClock())
}

// newPauseFixtureWithGovernor is the same fixture with a hand-built power policy and, optionally, a
// nil clock - which means real time.
//
// Both halves exist because the two kinds of assertion need different tools: the window and the
// deadline are durations, so a test that waited for them with the shipped policy would measure the
// scheduler, while the TIMER that expires the exemption is real time and cannot be driven from a
// fake clock.
func newPauseFixtureWithGovernor(t *testing.T, governor *power.Governor, clock *pauseClock) *pauseFixture {
	t.Helper()
	registry := outbound.NewRegistry()
	outbound.Register[publishKeeperOptions](registry, "publish-test",
		func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, options publishKeeperOptions) (adapter.Outbound, error) {
			options.keeper.tag = tag
			return options.keeper, nil
		})
	endpoint := &onDemandTestEndpoint{tag: "wg0", keep: true}
	endpointManager := &onDemandEndpointManager{endpoint: endpoint}
	manager := outbound.NewManager(registry, endpointManager, "")
	// A default node, so the endpoint is referenced through the inbound below and not by accident
	// of being the first object published.
	require.NoError(t, manager.Create(context.Background(), nil, log.NewNOPFactory().NewLogger("test"),
		"direct", "publish-test", &publishKeeperOptions{keeper: &publishKeeper{}}))

	pauseManager := &fakePauseManager{}
	inbound := &publishReferrerInbound{}
	inbound.setReferences(endpoint.Tag())
	ctx := service.ContextWith[adapter.OutboundManager](context.Background(), manager)
	ctx = service.ContextWith[pause.Manager](ctx, pauseManager)
	ctx = service.ContextWith[adapter.EndpointManager](ctx, endpointManager)
	ctx = service.ContextWith[adapter.InboundManager](ctx, &publishInboundManager{inbound: inbound})
	ctx = service.ContextWith[adapter.ServiceManager](ctx, &reuseTestServiceManager{})
	ctx = service.ContextWith[adapter.DNSTransportManager](ctx, &reuseTestDNSTransportManager{})
	ctx = service.ContextWith[adapter.NetworkManager](ctx, &reuseTestNetworkManager{})
	if governor != nil {
		ctx = service.ContextWith[*power.Governor](ctx, governor)
	}

	reference := NewReferenceManager(ctx, log.NewNOPFactory().NewLogger("reference"), option.Options{})
	// Install the clock BEFORE Start: the first pass runs inside Start, and a clock installed after
	// it would leave the initial verdict on the system clock.
	if clock != nil {
		reference.now = clock.Now
	}
	scope := adapter.NewScope(ctx, log.NewNOPFactory().Logger())
	require.NoError(t, reference.Start(adapter.StartStateStarted, scope))
	t.Cleanup(func() {
		require.NoError(t, scope.Close())
		if governor != nil {
			governor.Close()
		}
	})

	// The first pass runs inside Start; the endpoint is already referenced there, so it has been
	// told keep=true once. Drain that observation so each test asserts only what it causes.
	_, initialVerdicts, _, _ := endpoint.snapshot()
	require.Equal(t, []bool{true}, initialVerdicts,
		"a referenced on-demand endpoint must start allowed to keep its tunnel")

	return &pauseFixture{
		t:         t,
		endpoint:  endpoint,
		pause:     pauseManager,
		manager:   manager,
		reference: reference,
		clock:     clock,
		pass: func() {
			// A publication is the cheapest real trigger the manager emits. The keeper's identity is
			// irrelevant to the endpoint verdict; what matters is that the pass runs asynchronously
			// exactly like it does after a provider round.
			require.NoError(t, manager.Replace(context.Background(), nil, log.NewNOPFactory().NewLogger("test"),
				"direct", "publish-test", &publishKeeperOptions{keeper: &publishKeeper{}}))
		},
	}
}

// TestADevicePauseStillSuspendsATunnelThatMovedNoTraffic is the negative control for the Q07
// exemption, and it preserves the fact the audit recorded: a suspend reaches the endpoint, the
// endpoint implements it as a teardown, and the flows on the tunnel die.
//
// What changed is which tunnels a pause reaches. A flow that is open but has moved no payload is
// IDLE by the policy's definition - the connection count is exactly the proxy the decision rejects,
// because a banking app's silent TLS session and a download are both "one stream" - so this case
// still ends in the teardown the five implementations perform. The positive case, where payload did
// move, is TestADevicePauseKeepsATunnelThatIsCarryingTraffic in route/reference_activity_test.go.
func TestADevicePauseStillSuspendsATunnelThatMovedNoTraffic(t *testing.T) {
	fixture := newPauseFixture(t)
	// A flow is open on the tunnel and nothing has been counted for it: this is the idle case the
	// exemption must NOT cover.
	fixture.endpoint.openStream()
	_, _, streams, kills := fixture.endpoint.snapshot()
	require.Equal(t, 1, streams)
	require.Zero(t, kills)

	fixture.pause.DevicePause()
	require.Eventually(t, func() bool {
		keep, _, _, _ := fixture.endpoint.snapshot()
		return !keep
	}, 5*time.Second, time.Millisecond, "the pause never reached the tunnel")

	_, verdicts, streams, kills := fixture.endpoint.snapshot()
	require.Equal(t, []bool{true, false}, verdicts)
	require.Zero(t, streams, "the suspended tunnel still reports a flow")
	require.Equal(t, 1, kills,
		"an idle tunnel must still be suspended at a pause: the exemption is for traffic, not for "+
			"the existence of a flow, and with no payload the pre-Q07 behaviour is correct")
}

// TestThePauseVerdictComesFromTheManagersCallbacks is the contract the withdrawn fix would have
// replaced, pinned so the replacement cannot come back unnoticed.
//
// The pass reads the cache, and the cache is fed by the pause manager's callbacks: a level that the
// manager was told about moves the tunnel, and a level change with NO event does not (the event is
// the trigger). That is not a defect being preserved - it is the same rule the pause manager itself
// follows, where the level and the notification are one action (`defaultManager.DevicePause` stores
// the level and invokes the callbacks), so "cache and manager disagree" is not a state production
// can reach.
func TestThePauseVerdictComesFromTheManagersCallbacks(t *testing.T) {
	fixture := newPauseFixture(t)

	// A level with no delivered event: the manager reports paused, and NOTHING is suspended,
	// because nothing told this pass that anything changed.
	fixture.pause.setWithoutNotification(true)
	fixture.pass()
	_, verdicts, _, _ := fixture.endpoint.snapshot()
	require.Equal(t, []bool{true}, verdicts,
		"a level change with no pause event suspended a tunnel: the pass is not reading a cache")

	// The event arrives: the tunnel is told, exactly once.
	fixture.pause.DevicePause()
	require.Eventually(t, func() bool {
		keep, _, _, _ := fixture.endpoint.snapshot()
		return !keep
	}, 5*time.Second, time.Millisecond)
	_, verdicts, _, _ = fixture.endpoint.snapshot()
	require.Equal(t, []bool{true, false}, verdicts)

	// And the wake is the same rule in the other direction: one event, one resume.
	fixture.pause.DeviceWake()
	require.Eventually(t, func() bool {
		keep, _, _, _ := fixture.endpoint.snapshot()
		return keep
	}, 5*time.Second, time.Millisecond)
	_, verdicts, _, _ = fixture.endpoint.snapshot()
	require.Equal(t, []bool{true, false, true}, verdicts)
}
