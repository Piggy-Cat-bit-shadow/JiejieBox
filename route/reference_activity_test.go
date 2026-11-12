package route

// Q07, option (a): a device pause does not suspend an on-demand tunnel that is CARRYING traffic.
//
// The record of what a pause still does to an idle tunnel, and the rule that the verdict comes from
// the pause manager's callbacks, are in route/reference_pause_test.go. This file is the other half:
//
//   - a tunnel that moved payload inside the activity window keeps its transport across the pause;
//   - the exemption is not permanent - when the payload stops being recent, the next pass suspends
//     the tunnel, which is the timeout the audit's D-4 warning asks for;
//   - a wake restores normal management (the exemption stops applying and the deadline is disarmed);
//   - a flow that arrives while the device is asleep wakes the pass, so a tunnel a demand resumed is
//     not left holding a "suspended" verdict for the rest of the sleep;
//   - the counting is real: what makes the verdict "carrying traffic" is payload that moved through
//     the wrappers the forwarding path installs, not a reference and not a stream count.

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/power"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// observePayload is what the forwarding path does on the first byte of a flow through the tunnel.
func observePayload(fixture *pauseFixture) *tunnelActivitySlot {
	fixture.t.Helper()
	slot := fixture.reference.onDemandActivitySlot([]adapter.Outbound{fixture.endpoint})
	require.NotNil(fixture.t, slot, "a flow through an on-demand endpoint must resolve an activity record")
	return slot
}

// waitForDeadline waits for the pass to arm the activity deadline, which is the only observable
// consequence of a verdict reached with "this tunnel is carrying traffic": a kept tunnel that is
// already kept produces no other side effect.
func waitForDeadline(t *testing.T, fixture *pauseFixture) time.Time {
	t.Helper()
	var deadline time.Time
	require.Eventually(t, func() bool {
		armed, ok := fixture.reference.recheckDeadline()
		if ok {
			deadline = armed
		}
		return ok
	}, 5*time.Second, time.Millisecond,
		"a pause that kept a carrying tunnel did not arm the deadline that expires the exemption")
	return deadline
}

// TestADevicePauseKeepsATunnelThatIsCarryingTraffic is the contract the decision bought: the pause
// reaches the manager, the manager sees recent payload on a referenced tunnel, and the tunnel is NOT
// told to suspend.
//
// The assertions are deliberately about the absence of the teardown call as well as about the flows
// surviving: a keeper that was told keep=true again would be indistinguishable from one that was
// never asked, and it is the second that the five real implementations turn into a cancel/down.
func TestADevicePauseKeepsATunnelThatIsCarryingTraffic(t *testing.T) {
	fixture := newPauseFixture(t)
	fixture.endpoint.openStream()
	observePayload(fixture)

	fixture.pause.DevicePause()

	// The pass ran and reached the exemption: the deadline is armed from the activity it saw.
	deadline := waitForDeadline(t, fixture)
	require.WithinDuration(t,
		fixture.clock.Now().Add(power.DefaultPolicy().OnDemandIdleAfter), deadline, time.Second,
		"the deadline must be the end of the activity window, measured from the last payload")

	_, verdicts, streams, kills := fixture.endpoint.snapshot()
	require.Equal(t, []bool{true}, verdicts,
		"the paused pass told the tunnel to suspend even though it was carrying traffic")
	require.Equal(t, 1, streams, "the flow on the tunnel was terminated by the pause")
	require.Zero(t, kills, "the tunnel's transport was torn down under a carrying flow")
}

// TestTheActivityWindowExpiresAndTheTunnelIsSuspended is the timeout half of the decision.
//
// Without it the exemption would be permanent: nothing in the system produces an event when a
// transfer stops, so a tunnel that once carried a byte would be kept for the rest of the sleep - the
// battery regression D-4 warned about. The recheck deadline is what makes "idle" reachable again.
func TestTheActivityWindowExpiresAndTheTunnelIsSuspended(t *testing.T) {
	fixture := newPauseFixture(t)
	fixture.endpoint.openStream()
	observePayload(fixture)

	fixture.pause.DevicePause()
	waitForDeadline(t, fixture)

	// The payload stops. Time passes, and the deadline that was armed is now in the past.
	fixture.clock.Advance(2 * power.DefaultPolicy().OnDemandIdleAfter)
	fixture.pass()

	require.Eventually(t, func() bool {
		keep, _, _, _ := fixture.endpoint.snapshot()
		return !keep
	}, 5*time.Second, time.Millisecond,
		"a tunnel whose traffic window expired was kept: the exemption became permanent")

	_, verdicts, streams, kills := fixture.endpoint.snapshot()
	require.Equal(t, []bool{true, false}, verdicts)
	require.Zero(t, streams)
	require.Equal(t, 1, kills, "the idle tunnel's flows must be released when the window expires")

	require.Eventually(t, func() bool {
		_, armed := fixture.reference.recheckDeadline()
		return !armed
	}, 5*time.Second, time.Millisecond, "a suspended tunnel still has an activity deadline armed")
}

// TestADeviceWakeRestoresNormalSuspensionEligibility is the wake half: the exemption is a property of
// a pause, and once the device is awake the eligibility pass is back to its ordinary answer.
func TestADeviceWakeRestoresNormalSuspensionEligibility(t *testing.T) {
	fixture := newPauseFixture(t)
	fixture.endpoint.openStream()
	observePayload(fixture)

	fixture.pause.DevicePause()
	waitForDeadline(t, fixture)

	fixture.pause.DeviceWake()

	require.Eventually(t, func() bool {
		_, armed := fixture.reference.recheckDeadline()
		return !armed
	}, 5*time.Second, time.Millisecond,
		"the wake left the activity deadline armed, so a later pass would run against a device "+
			"that is no longer paused")

	_, verdicts, streams, kills := fixture.endpoint.snapshot()
	require.Equal(t, []bool{true}, verdicts,
		"the wake re-told a kept tunnel to keep: the verdict is unchanged, so it must not be called")
	require.Equal(t, 1, streams)
	require.Zero(t, kills)
}

// TestAFlowArrivingDuringAPauseWakesThePass covers the case the verdict alone cannot see: the tunnel
// was suspended while idle, and then demand arrived while the device was still asleep.
//
// The endpoint resumes itself on demand (every kind does; see resumeForCaller and waitReady), so the
// transport is up while the eligibility pass still holds "suspend". The flow setup is what tells the
// manager that the verdict is stale - without it the resumed tunnel would keep no exemption and, more
// importantly, no deadline, so nothing would ever reclaim it once the demand ended.
func TestAFlowArrivingDuringAPauseWakesThePass(t *testing.T) {
	fixture := newPauseFixture(t)

	// Idle at the pause: the tunnel is suspended, exactly as before the decision.
	fixture.pause.DevicePause()
	require.Eventually(t, func() bool {
		keep, _, _, _ := fixture.endpoint.snapshot()
		return !keep
	}, 5*time.Second, time.Millisecond)
	_, armed := fixture.reference.recheckDeadline()
	require.False(t, armed, "an idle paused tunnel must not arm an activity deadline")

	// Demand arrives on the sleeping device. This is the router's call, made when the flow is set up
	// and before it moves its first byte.
	fixture.reference.observeOnDemandFlow("wg0")

	require.Eventually(t, func() bool {
		keep, _, _, _ := fixture.endpoint.snapshot()
		return keep
	}, 5*time.Second, time.Millisecond,
		"a flow that arrived during a pause did not bring the tunnel's exemption back")
	waitForDeadline(t, fixture)

	_, verdicts, _, kills := fixture.endpoint.snapshot()
	require.Equal(t, []bool{true, false, true}, verdicts)
	require.Zero(t, kills)
}

// TestTheActivityLedgerCountsPayloadThroughTheWrappers is the measurement half.
//
// The decision is only as good as the signal: if the wrappers did not count, every tunnel would look
// idle and the pause would be back to cutting transfers. The stream wrapper, the packet wrapper and
// the direct-offload flow tracker are the three ways a byte reaches the ledger, and each is asserted
// to move the window and to lose it again when the window passes.
func TestTheActivityLedgerCountsPayloadThroughTheWrappers(t *testing.T) {
	clock := newPauseClock()
	activity := newTunnelActivity(time.Minute, clock.Now)

	t.Run("stream", func(t *testing.T) {
		slot := activity.slot("stream")
		require.False(t, activity.active("stream", clock.Now()), "an unobserved tunnel is not active")

		clientSide, peerSide := net.Pipe()
		defer clientSide.Close()
		defer peerSide.Close()
		wrapped := slot.wrapConn(clientSide)
		require.NotSame(t, clientSide, wrapped)

		var writeErr error
		var wait sync.WaitGroup
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, writeErr = peerSide.Write([]byte("payload"))
		}()
		_, err := io.ReadAll(io.LimitReader(wrapped, int64(len("payload"))))
		require.NoError(t, err)
		wait.Wait()
		require.NoError(t, writeErr)

		require.True(t, activity.active("stream", clock.Now()),
			"payload read through the wrapped conn did not reach the ledger")
		clock.Advance(2 * time.Minute)
		require.False(t, activity.active("stream", clock.Now()),
			"the activity window did not expire")
	})

	t.Run("packet session", func(t *testing.T) {
		slot := activity.slot("packet")
		wrapped := slot.wrapPacketConn(&activityTestPacketConn{})
		buffer := buf.As([]byte("datagram"))
		require.NoError(t, wrapped.WritePacket(buffer, M.Socksaddr{}))
		require.True(t, activity.active("packet", clock.Now()),
			"a datagram written through the wrapped packet conn did not reach the ledger")
	})

	t.Run("direct-offload flow", func(t *testing.T) {
		slot := activity.slot("flow")
		tracker := newActivityFlowTracker(slot)
		tracker.AttachFlow(nil)
		tracker.FlowEstablished()
		require.False(t, activity.active("flow", clock.Now()))
		tracker.CountForward(1500)
		require.True(t, activity.active("flow", clock.Now()),
			"a forwarded flow's byte count did not reach the ledger")
	})
}

// TestTheRouterAttributesAFlowToItsOnDemandTunnel is the wiring: the router resolves the tunnel from
// the chain it just routed, and only then wraps the conn.
//
// The negative controls matter as much as the positive one. A chain that does not end at an
// on-demand tunnel must not be wrapped (every ordinary flow would pay for a counter it can never
// use), and a Router with no reference manager must be inert (a Router built directly in a test has
// no ledger and must behave exactly as before).
func TestTheRouterAttributesAFlowToItsOnDemandTunnel(t *testing.T) {
	fixture := newPauseFixture(t)
	router := &Router{referenceManager: fixture.reference}
	conn := &activityTestConn{}

	wrapped := router.trackOnDemandActivity(conn, []adapter.Outbound{fixture.endpoint})
	require.NotEqual(t, conn, wrapped, "a flow into an on-demand tunnel was not counted")
	require.True(t, fixture.reference.activity.active("wg0", fixture.clock.Now()),
		"the flow setup was not recorded against the tunnel it enters")

	plain := router.trackOnDemandActivity(conn, []adapter.Outbound{&reuseTestKeeper{}})
	require.True(t, conn == plain, "a chain with no on-demand tunnel was wrapped anyway")

	bare := (&Router{}).trackOnDemandActivity(conn, []adapter.Outbound{fixture.endpoint})
	require.True(t, conn == bare, "a Router with no reference manager wrapped the conn")
}

// TestTheRecheckTimerSuspendsATunnelWhenTheTrafficStops is the automatic half of the timeout: the
// deadline is not only computed, it fires.
//
// The window here is milliseconds and the clock is the real one, because the thing under test is the
// timer. That is the opposite trade to the other tests in this file - a duration does not need a
// timer, and a timer cannot be driven by a fake clock - so the policy is injected instead of the
// time, and the assertion is about the transition rather than about the deadline's value.
func TestTheRecheckTimerSuspendsATunnelWhenTheTrafficStops(t *testing.T) {
	governor := power.NewGovernor(power.Policy{OnDemandIdleAfter: 250 * time.Millisecond})
	fixture := newPauseFixtureWithGovernor(t, governor, nil)

	// Idle at the pause: suspended, as before the decision.
	fixture.pause.DevicePause()
	require.Eventually(t, func() bool {
		keep, _, _, _ := fixture.endpoint.snapshot()
		return !keep
	}, 5*time.Second, time.Millisecond)

	// A flow arrives while the device is asleep and the tunnel is suspended: the endpoint resumes
	// itself and the manager is told, which keeps the tunnel and starts the window.
	fixture.endpoint.openStream()
	fixture.reference.observeOnDemandFlow("wg0")
	require.Eventually(t, func() bool {
		keep, _, streams, _ := fixture.endpoint.snapshot()
		return keep && streams == 1
	}, 5*time.Second, time.Millisecond,
		"a flow arriving during a pause did not keep the tunnel")

	// Nothing else happens, and no event will ever report that: the flow is still open, the device is
	// still asleep. The deadline is the only thing that can reclaim the tunnel, and it must.
	require.Eventually(t, func() bool {
		keep, _, _, _ := fixture.endpoint.snapshot()
		return !keep
	}, 5*time.Second, 5*time.Millisecond,
		"the recheck timer never fired: a tunnel that stopped carrying traffic was kept for the "+
			"rest of the pause")

	_, verdicts, _, kills := fixture.endpoint.snapshot()
	require.Equal(t, []bool{true, false, true, false}, verdicts,
		"expected start-kept, suspended-at-pause, kept-because-carrying, suspended-when-idle")
	require.Equal(t, 1, kills)
}

// activityTestConn is a net.Conn with no behaviour: the routing test asserts identity and attribution,
// and never reads or writes.
type activityTestConn struct{}

func (c *activityTestConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *activityTestConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *activityTestConn) Close() error                     { return nil }
func (c *activityTestConn) LocalAddr() net.Addr              { return nil }
func (c *activityTestConn) RemoteAddr() net.Addr             { return nil }
func (c *activityTestConn) SetDeadline(time.Time) error      { return nil }
func (c *activityTestConn) SetReadDeadline(time.Time) error  { return nil }
func (c *activityTestConn) SetWriteDeadline(time.Time) error { return nil }

var (
	_ net.Conn        = (*activityTestConn)(nil)
	_ tun.FlowTracker = (*activityFlowTracker)(nil)
)

// activityTestPacketConn is the packet-conn half of the same idea: it accepts a datagram so the
// wrapper's counter has something to observe.
type activityTestPacketConn struct{}

func (c *activityTestPacketConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	return M.Socksaddr{}, io.EOF
}

func (c *activityTestPacketConn) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	buffer.Release()
	return nil
}

func (c *activityTestPacketConn) Close() error                     { return nil }
func (c *activityTestPacketConn) LocalAddr() net.Addr              { return nil }
func (c *activityTestPacketConn) SetDeadline(time.Time) error      { return nil }
func (c *activityTestPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (c *activityTestPacketConn) SetWriteDeadline(time.Time) error { return nil }

var _ N.PacketConn = (*activityTestPacketConn)(nil)
