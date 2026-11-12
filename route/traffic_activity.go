package route

// The activity ledger: which on-demand tunnel is carrying payload right now.
//
// # The decision this implements
//
// Audit F-01/Q07 was a policy question with two answers. The decision taken is (a): a device pause
// must NOT suspend an on-demand tunnel that is carrying traffic, while an idle one is still suspended
// exactly as before. That is the only answer that keeps the two sentences this fork has already
// published true - `common/runtimecoord/platform_events.go` ("it never terminates an active flow")
// and `experimental/libbox/platform_events.go` ("makes IDLE on-demand resources eligible for
// suspension") - and it is implemented here rather than by narrowing them.
//
// # Why "carrying traffic" is measured rather than inferred
//
// The obvious proxies are wrong in both directions:
//
//   - a REFERENCE is not activity. A referenced tunnel is one the routing graph can reach; on a
//     phone with a WireGuard endpoint as the default outbound that is true at 03:00 and while a
//     download runs. Suspending on unreferenced-only would never suspend anything.
//   - a CONNECTION or STREAM COUNT is not activity either. A banking app holds a TLS connection
//     open and silent, and a download is one connection that moves megabytes a second. The first is
//     idle traffic-wise, the second must survive a lock screen, and a count cannot tell them apart.
//
// So the ledger records the last instant PAYLOAD MOVED on each tunnel, and the eligibility pass asks
// whether that was recent. `power.Policy.OnDemandIdleAfter` is the window; it is the same kind of
// threshold as the governor's other values and lives there for the same reason.
//
// # Why one ledger in the router rather than a method on each endpoint
//
// The five endpoint kinds agree on nothing except `OnDemandEndpoint`, and giving each an activity
// counter would mean five implementations, five review surfaces and five ways to be wrong about
// optional interfaces on the data path. The router is the one place that knows a flow's payload and
// the on-demand tunnel it is going through, and it is already the place that distinguishes business
// traffic from the core's own liveness traffic for the power governor (see Router.observeTraffic).
// One ledger therefore covers every kind, including a future one.
//
// The honest limit of that placement: traffic that reaches a tunnel as another outbound's DETOUR does
// not pass through the router's chain walk, so it is not counted here. The tunnel is still
// referenced in that configuration and is still suspended at a pause; what is missing is the
// exemption, not the suspend. A per-endpoint counter is what that case would need, and it is
// deliberately not invented here - see DECISIONS.md D-4 for the recorded scope of the decision.

import (
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/bufio"
	N "github.com/sagernet/sing/common/network"
)

// tunnelActivity is the ledger. Its zero value is not usable; call newTunnelActivity.
type tunnelActivity struct {
	// window is how recently payload must have moved for the tunnel to count as carrying traffic.
	// Zero means the exemption is off: every referenced tunnel is idle, which is the behaviour this
	// tree had before the decision.
	window time.Duration
	// now is the clock, and it is the manager's clock rather than a second one: a test drives the
	// whole pause decision from one fake time source, and two clocks would let the window and the
	// recheck deadline disagree about what "now" is.
	now func() time.Time

	access sync.Mutex
	slots  map[string]*tunnelActivitySlot
}

// tunnelActivitySlot is one tunnel's activity record. It is handed to the forwarding path at flow
// setup, so the per-read and per-write cost is one atomic store and nothing else.
type tunnelActivitySlot struct {
	// now is the ledger's clock, held here so an observation does not have to reach back through the
	// ledger (and its lock) for the time. It is the same function the manager uses, so a test's fake
	// clock drives both the window and the deadline.
	now func() time.Time
	// last is the UnixNano of the last observed payload, and zero means "never observed". Wall time
	// is the correct clock here and the monotonic one is not: the only interval that matters is the
	// one that contains a sleep, and the monotonic clock does not advance while the device is asleep
	// (see common/power), so it would report an eight hour sleep as a few seconds of activity.
	last atomic.Int64
}

func newTunnelActivity(window time.Duration, now func() time.Time) *tunnelActivity {
	return &tunnelActivity{
		window: window,
		now:    now,
		slots:  make(map[string]*tunnelActivitySlot),
	}
}

// slot returns the activity record for one on-demand tunnel tag, creating it on first use.
//
// It is called once per flow setup and once per eligibility pass, never per byte: a slot is stable
// for the life of the process, and the hot path holds the pointer.
func (a *tunnelActivity) slot(tag string) *tunnelActivitySlot {
	if a == nil {
		return nil
	}
	a.access.Lock()
	defer a.access.Unlock()
	slot, loaded := a.slots[tag]
	if loaded {
		return slot
	}
	slot = &tunnelActivitySlot{now: a.now}
	a.slots[tag] = slot
	return slot
}

// active reports whether the tunnel moved payload within the window, measured at now.
//
// An unknown tag, a window that is off, and a record that has never been written are all "not
// active": the exemption is an exception that has to be earned by observed traffic, never a default.
func (a *tunnelActivity) active(tag string, now time.Time) bool {
	if a == nil || a.window <= 0 {
		return false
	}
	return a.slot(tag).activeAt(now, a.window)
}

// lastActivity returns the instant payload last moved on the tunnel, and whether it ever did.
func (a *tunnelActivity) lastActivity(tag string) (time.Time, bool) {
	if a == nil {
		return time.Time{}, false
	}
	return a.slot(tag).observedAt()
}

// observe records that payload moved on the tunnel at the ledger's clock.
func (s *tunnelActivitySlot) observe() {
	if s == nil {
		return
	}
	s.last.Store(s.now().UnixNano())
}

// observedAt returns the instant of the last observation, and whether there was one.
func (s *tunnelActivitySlot) observedAt() (time.Time, bool) {
	if s == nil {
		return time.Time{}, false
	}
	nanos := s.last.Load()
	if nanos == 0 {
		return time.Time{}, false
	}
	return time.Unix(0, nanos), true
}

// activeAt is the window test, and it is deliberately strict: payload observed exactly `window` ago
// is no longer recent.
func (s *tunnelActivitySlot) activeAt(now time.Time, window time.Duration) bool {
	last, observed := s.observedAt()
	if !observed || window <= 0 {
		return false
	}
	return now.Before(last.Add(window))
}

// wrapConn counts a stream flow's payload in both directions.
//
// The wrapper is bufio.CounterConn, which is what the traffic-control recorder and the power-report
// dialer already put on this path: it preserves the extended-conn capability chain through
// N.ExtendedConn, so an outbound that replaces the reader or the writer - every protocol that
// splices, and the TUN stack itself - still sees a conn it can replace.
func (s *tunnelActivitySlot) wrapConn(conn net.Conn) net.Conn {
	if s == nil {
		return conn
	}
	return bufio.NewCounterConn(conn,
		[]N.CountFunc{func(n int64) { s.observe() }},
		[]N.CountFunc{func(n int64) { s.observe() }},
	)
}

// wrapPacketConn counts a packet flow's payload, and keeps the capability chain visible.
//
// bufio.CounterPacketConn embeds N.PacketConn, which does not carry Upstream(), so the wrapper is
// given one here - the same shape the traffic-control recorder uses on this same conn, and what
// keeps a zero-copy hand-out from silently degrading because a counter was put in the way.
func (s *tunnelActivitySlot) wrapPacketConn(conn N.PacketConn) N.PacketConn {
	if s == nil {
		return conn
	}
	return &activityPacketConn{
		PacketConn: bufio.NewCounterPacketConn(conn,
			[]N.CountFunc{func(n int64) { s.observe() }},
			[]N.CountFunc{func(n int64) { s.observe() }},
		),
	}
}

// activityPacketConn is the packet half of wrapConn: the counter plus the unwrap answer.
type activityPacketConn struct {
	N.PacketConn
}

func (c *activityPacketConn) Upstream() any {
	return c.PacketConn
}

func (c *activityPacketConn) ReaderReplaceable() bool {
	return true
}

func (c *activityPacketConn) WriterReplaceable() bool {
	return true
}

// activityFlowTracker counts a DIRECT-OFFLOAD flow's payload.
//
// A flow whose outbound is flow-capable never becomes a conn or a packet conn: the TUN stack hands
// its packets straight to the endpoint's device and reports the byte counts here. This tracker is
// therefore the third and least obvious half of the ledger, and on the platforms that do the handover
// it is the only half that will ever see a byte.
//
// It is passive by design: it holds no FlowHandle and does nothing on close. Closing belongs to the
// flow's own tracker fan-out (multiFlowTracker), and a counter that also closed would change which
// side owns the teardown.
type activityFlowTracker struct {
	slot *tunnelActivitySlot
}

func newActivityFlowTracker(slot *tunnelActivitySlot) *activityFlowTracker {
	return &activityFlowTracker{slot: slot}
}

func (t *activityFlowTracker) AttachFlow(tun.FlowHandle) {}

func (t *activityFlowTracker) CountForward(int) {
	t.slot.observe()
}

func (t *activityFlowTracker) CountReverse(int) {
	t.slot.observe()
}

func (t *activityFlowTracker) FlowEstablished() {}

func (t *activityFlowTracker) CloseFlow(tun.FlowCloseReason) {}

// onDemandEndpointOf returns the on-demand tunnel a resolved chain carries traffic through, or nil.
//
// The walk is the whole chain rather than its leaf because a group publishes the member it selected,
// not the leaf's identity, and an endpoint is always a leaf: the last match is therefore the tunnel
// the flow will enter. A chain with no on-demand endpoint is the common case and costs one failed
// type assertion per hop.
func onDemandEndpointOf(chain []adapter.Outbound) adapter.OnDemandEndpoint {
	for i := len(chain) - 1; i >= 0; i-- {
		onDemandEndpoint, isOnDemand := chain[i].(adapter.OnDemandEndpoint)
		if isOnDemand {
			return onDemandEndpoint
		}
	}
	return nil
}
