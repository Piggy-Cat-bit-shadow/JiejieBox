package route

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
)

// ReclaimReason says WHY managed connections are being reclaimed. The reason is not cosmetic: the
// callers that used to share one entry point (CloseAll) mean very different things, and treating a
// path change like a shutdown is what made a Wi-Fi roam kill every stream the device was running.
//
// Audit, before this file existed:
//
//	ResetNetwork / interface update -> connectionManager.CloseAll()   every stream, unconditionally
//	Pause                           -> CloseIdleConnections()          idle only, already correct
//	ReleaseMemory (OOM killer)      -> ResetNetwork + idle            aggressive, and deliberate
//	Close (shutdown)                -> CloseAll()                      correct
//
// So the reason-aware split already existed for pause and memory pressure, and the ONE place that
// was wrong was the transition path, which reached for the shutdown primitive. That is what
// ReclaimNetworkTransition changes; the other reasons are kept so the distinction is explicit at the
// call site rather than implied by which method somebody happened to call.
type ReclaimReason uint8

const (
	// ReclaimNetworkTransition is a path change: a new interface, a new gateway, a new SSID.
	//
	// Drain, do not kill. A connection that is still moving bytes is left alone and given the chance
	// to finish on the old path while new connections use the new one. Only connections that are
	// PROVEN IDLE are reclaimed immediately, and the rest are followed by a sweep so that "drain"
	// cannot mean "forever".
	ReclaimNetworkTransition ReclaimReason = iota
	// ReclaimIdle is pause, background or a gentle memory release: reclaim what is idle, never a
	// stream that is in use.
	ReclaimIdle
	// ReclaimDeadPath is a HARD transition: the device has no default interface at all, so no
	// connection can still be reaching anywhere. There is nothing to drain and nothing to preserve.
	//
	// This is what distinguishes hard from medium in practice. A medium transition - a new gateway,
	// a new SSID, a different interface - leaves open the possibility that a socket still works, so
	// it drains and lets the socket prove otherwise. A hard one has already answered that question,
	// and holding the connections would only keep memory and file descriptors alive on a device that
	// is, by definition, likely to be under pressure.
	ReclaimDeadPath
	// ReclaimShutdown is teardown. Everything goes, including active streams, because there is
	// nothing left to serve them.
	ReclaimShutdown
)

// How long a connection must have been silent before a transition may reclaim it.
//
// This is deliberately longer than any interactive gap a user would notice and far shorter than the
// lifetime of a blackholed socket, which is the failure this bounds: a stream whose path is gone
// stops moving bytes, and without a bound it would sit in the list until shutdown.
const drainIdleGrace = 20 * time.Second

// How often the drain sweep re-examines connections that a transition left behind. It only runs
// while old-generation connections exist, and stops as soon as they are gone.
const drainSweepInterval = 15 * time.Second

// The longest the sweep may wait between passes once it has stopped making progress.
//
// Without this, one kernel-owned connection pins the sweep to its base interval for the life of the
// tunnel. That connection can never be reclaimed by the sweep - userspace cannot observe whether it
// is alive - so `remaining` stays true, the timer reschedules, and the process wakes every fifteen
// seconds until the VPN is turned off. On a phone that is a battery bug wearing a correctness
// argument.
//
// Backing off is safe because the sweep's only job is to notice a connection that has BECOME idle,
// and the connections it is waiting on are the ones it cannot judge. A new transition resets the
// interval, so nothing that is actually reclaimable waits long.
const drainSweepMaxInterval = 5 * time.Minute

// managedConnState is the per-connection lifecycle record the reclaim policies decide on.
//
// It is embedded in both tracked connection types, which is the only reason its methods are
// pointers: the enclosing value must implement managedConn.
type managedConnState struct {
	// createdAt is when the connection entered the list.
	createdAt time.Time
	// generation is the transition counter at that moment. A connection whose generation is behind
	// the manager's belongs to a path the device has left - a drain candidate, NOT a corpse.
	generation uint64
	// lastActive is the last observed successful byte transfer, in Unix seconds.
	//
	// Zero means no transfer has been observed YET, which is not the same as idle. How long that is
	// allowed to stand is what activityObservable decides.
	lastActive atomic.Int64
	// activityObservable is false once the connection has been handed to the kernel.
	//
	// It separates two states that "lastActive == 0" used to conflate, and they have opposite
	// reclaim policies:
	//
	//	true  (an ordinary connection)  nothing has been transferred, so the clock runs from
	//	                                createdAt. A flow that is never used is idle after the
	//	                                grace and may be reclaimed.
	//	false (kernel-owned)            bytes move between descriptors in the kernel and NOTHING in
	//	                                userspace will ever observe this connection again. Its
	//	                                silence is not evidence of idleness, so it is protected on a
	//	                                transition and reclaimed only by pause or shutdown.
	//
	// Conflating them protected every never-used UDP flow for the life of the tunnel, which turned
	// the drain into a leak.
	activityObservable atomic.Bool
	// packet marks a datagram flow.
	//
	// A packet flow has no migration and no acknowledgement: its local address and port ARE the
	// flow's identity, and the kernel will happily keep sending datagrams from an address the device
	// no longer holds. Waiting for it to look idle therefore waits for the application to stop
	// sending, which a game, a QUIC session or a keepalive never does. A transition closes it
	// immediately instead - the same class as a protocol transport bound to the old path - and the
	// application re-opens it on the new network. Same-IP roams lose their UDP flows too; that is the
	// deliberate cost of not leaving a black hole behind.
	//
	// Set once, before the connection is published, and never mutated; ReclaimIdle and shutdown keep
	// using the ordinary idleness rule, so memory pressure still spares a fresh flow.
	packet bool
}

// markKernelOwned records that this connection's bytes no longer pass through userspace.
//
// It is set ONLY on a confirmed handover - the splice call returning success - never on the
// possibility of one. A connection that merely implements SyscallConn has not been spliced, and
// treating it as unobservable would exempt it from the drain for no reason.
func (s *managedConnState) markKernelOwned() {
	s.activityObservable.Store(false)
}

func (s *managedConnState) reclaimState() *managedConnState { return s }

// touch records that this connection moved bytes.
//
// One atomic load on the common path and a store at most once a second: the reclaim policies decide
// on a scale of seconds, so sub-second precision would buy nothing and would put a store in the
// forwarding path of every single write.
func (s *managedConnState) touch() {
	now := time.Now().Unix()
	if s.lastActive.Load() != now {
		s.lastActive.Store(now)
	}
}

// countBytes is the N.CountFunc form of touch, for the packet copy paths.
//
// It exists so that packet activity is observed through the copy engine's own counting protocol
// rather than by a wrapper type in the connection chain: see packetConnectionCopy.
func (s *managedConnState) countBytes(n int64) {
	if n > 0 {
		s.touch()
	}
}

// managedConnStateOf walks a connection's upstream chain looking for the lifecycle record this
// manager attaches, and reports nil when the connection is not one of ours.
//
// The walk is necessary rather than a plain type assertion: by the time a connection reaches the
// copy loops it may sit under the dialer's power counters or a protocol's own wrapper, and asserting
// only on the outermost value would observe nothing for exactly those connections.
func managedConnStateOf(conn any) *managedConnState {
	for depth := 0; conn != nil && depth < 32; depth++ {
		if state, isManaged := conn.(interface {
			reclaimState() *managedConnState
		}); isManaged {
			return state.reclaimState()
		}
		upstream, hasUpstream := conn.(common.WithUpstream)
		if !hasUpstream {
			return nil
		}
		next := upstream.Upstream()
		if next == conn {
			return nil
		}
		conn = next
	}
	return nil
}

// idleFor reports how long the connection has been silent, and whether that is knowable at all.
//
// The false return is the important one: it says "no transfer was ever observed", which the callers
// must read as "cannot be proven idle" rather than "idle since creation".
func (s *managedConnState) idleFor(now time.Time) (time.Duration, bool) {
	if !s.activityObservable.Load() {
		// Kernel-owned: silence here is not idleness, it is the absence of an instrument.
		return 0, false
	}
	last := s.lastActive.Load()
	if last == 0 {
		// Observable and not yet used. The clock runs from creation, so a flow that never carries
		// anything becomes reclaimable once it is older than the grace rather than being protected
		// forever.
		return now.Sub(s.createdAt), true
	}
	return now.Sub(time.Unix(last, 0)), true
}

// managedConn is what the manager keeps in its list. CloseAll only ever needed io.Closer; the
// reclaim policies need to ask a connection about itself.
type managedConn interface {
	Close() error
	reclaimState() *managedConnState
}

// reclaimPolicy is the decision table, kept in one place so the reasons are comparable by reading
// rather than by tracing four call sites.
type reclaimPolicy struct {
	// provenIdleFor > 0 reclaims connections silent for at least that long.
	provenIdleFor time.Duration
	// closeAll reclaims everything, observed activity or not.
	closeAll bool
	// releaseFlows releases shaper-parked flows first, which is required whenever the sockets they
	// write to may be closed: a parked flow is not inside a write, so closing its socket would not
	// wake it.
	releaseFlows bool
	// drain marks the transition boundary and starts the sweep.
	drain bool
}

// drainGrace and sweepEvery resolve the production windows, with the test overrides applied.
func (m *ConnectionManager) drainGrace() time.Duration {
	if m.drainIdleGraceOverride > 0 {
		return m.drainIdleGraceOverride
	}
	return drainIdleGrace
}

func (m *ConnectionManager) sweepEvery() time.Duration {
	if m.drainSweepIntervalOverride > 0 {
		return m.drainSweepIntervalOverride
	}
	return drainSweepInterval
}

func reclaimPolicyFor(reason ReclaimReason) reclaimPolicy {
	switch reason {
	case ReclaimNetworkTransition:
		return reclaimPolicy{provenIdleFor: drainIdleGrace, releaseFlows: false, drain: true}
	case ReclaimIdle:
		return reclaimPolicy{provenIdleFor: drainIdleGrace, releaseFlows: false}
	case ReclaimDeadPath:
		return reclaimPolicy{closeAll: true, releaseFlows: true}
	default:
		return reclaimPolicy{closeAll: true, releaseFlows: true}
	}
}

// Reclaim applies the policy for reason and reports how many connections it reclaimed.
//
// "Reclaimed" means their admission is over: the dial gate has advanced, a draining pass has
// bumped the stale generation, and every returned connection has left the tracked set. Their
// PHYSICAL close runs on a disposer owned by this pass (see disposeConnections), because a
// socket's Close can block and the generation boundary must not queue behind the kernel.
//
// Note what a transition does NOT do: it does not close a connection whose activity has been
// observed recently, and it does not close one whose activity cannot be observed at all. It closes
// what it can prove is idle, marks the rest as belonging to the previous path, and leaves a sweep
// running to reclaim them as they fall silent.
func (m *ConnectionManager) Reclaim(reason ReclaimReason) int {
	policy := reclaimPolicyFor(reason)
	if policy.releaseFlows && m.scheduler != nil {
		m.scheduler.ReleaseAll()
	}
	if policy.closeAll {
		if reason == ReclaimShutdown {
			// Teardown is the one close-all promise that stays synchronous end to end: the
			// caller asked for the sockets to be gone, not commissioned.
			count := m.Count()
			m.CloseAll()
			return count
		}
		return m.reclaimAll()
	}

	now := time.Now()
	var staleGeneration uint64
	if policy.drain {
		// The counter bump and the gate advance are ONE step, run inside the gate's critical section.
		//
		// They used to be two:
		//
		//	staleGeneration = m.generation.Add(1)
		//	m.dialSetups.advance()
		//
		// and a dial beginning between them captured the OLD setup context - so this transition cancelled
		// it, treating it as an old-path dial - while the connection it produced was stamped with the NEW
		// counter, which equals `staleGeneration`, so the drain scan below classified it as current. Killed
		// as old, classified as new. Measured with a forced interleaving: 200 of 200 trials; unforced, 6 of
		// 20 000.
		//
		// The two halves are joined rather than reordered because reordering cannot help: there are three
		// instants in play - the gate's capture, the network epoch the epoch predicate compares, and the
		// manager stamp written when the connection is tracked - so moving two of them only changes which
		// pair disagrees. Making the bump part of the gate's own transition reduces it to one clock.
		//
		// The user-visible consequence is currently nil, and that is why this was left alone before: the
		// only production caller of a draining Reclaim is `resetNetworkLocked`, and every path into it marks
		// the network unsettled BEFORE the reset body, so a dial started in this window records
		// `startedStable=false` and is refused at hand-over anyway. The invariant violation is real, its
		// blast radius is contained by a second mechanism, and depending on that second mechanism for
		// correctness is what this change removes.
		m.dialSetups.advanceAndBump(func() {
			staleGeneration = m.generation.Add(1)
		})
	}

	m.access.Lock()
	var (
		closers []managedConn
		// seen and retained are counted in THIS scan and only for the generation being drained.
		//
		// The transition used to report m.Count() as "drained", which is not the same number and
		// overstates it: Count() is the whole manager, so any connection dialled on the NEW path
		// while the scan was running was counted as an old-path connection that had been spared. On
		// a real device, where reconnections begin immediately, that inflated exactly the figure the
		// drain is judged by.
		seen     int
		retained int
	)
	for element := m.connections.Front(); element != nil; {
		nextElement := element.Next()
		conn := element.Value
		state := conn.reclaimState()
		// A drained connection is only a candidate once it belongs to a path the device has left.
		// Without this check the sweep would also reclaim connections dialled AFTER the transition,
		// which are on the network the device is actually using.
		eligible := true
		if policy.drain {
			eligible = state.generation < staleGeneration
			if eligible {
				seen++
			}
		}
		// A datagram flow is class A at a transition: its 4-tuple is the flow's identity, so the
		// idleness grace cannot make it safe - only the application's next send can, and that send
		// goes to the address the device has left. Idle-reclaim (memory pressure) and shutdown still
		// use the grace; only the draining policy skips it.
		if eligible && policy.provenIdleFor > 0 && !(state.packet && policy.drain) {
			if idle, known := state.idleFor(now); !known || idle < m.drainGrace() {
				eligible = false
			}
		}
		if eligible {
			closers = append(closers, conn)
			m.connections.Remove(element)
		} else if policy.drain && state.generation < staleGeneration {
			retained++
		}
		element = nextElement
	}
	if m.disposalStageHook != nil {
		m.disposalStageHook()
	}
	// The removal and the ownership transfer are one step: shutdown takes m.access through
	// CloseAll, so it cannot observe an object between the tracked set and the disposer.
	m.stageDisposals(closers)
	m.access.Unlock()
	if policy.drain {
		m.transitions.recordTransition(retained, len(closers), seen)
		// Open the reconnect governor's window. The connections that were just invalidated are about
		// to be re-dialled by their applications, all at once; this is the burst it smooths.
		m.dialGovernor.open(time.Now())
		// Only worth starting when the pass left something behind; otherwise there is nothing for a
		// sweep to find.
		if retained > 0 {
			m.beginDrainSweep()
		}
		if m.reclaimLog != nil {
			m.reclaimLog(reason, len(closers), staleGeneration)
		}
	}
	return len(closers)
}

// reclaimAll is the close-all policy's admission half followed by a disposer instead of inline
// closes: cancel in-flight dials, release shaped flows, and remove every tracked connection under
// the lock. A dead-path reclaim runs inside the network-change body, so the same rule as a drain
// applies - the new generation must be able to start before the old sockets finish closing.
func (m *ConnectionManager) reclaimAll() int {
	m.dialSetups.advance()
	if m.scheduler != nil {
		m.scheduler.ReleaseAll()
	}
	m.access.Lock()
	closers := make([]managedConn, 0, m.connections.Len())
	for element := m.connections.Front(); element != nil; {
		nextElement := element.Next()
		closers = append(closers, element.Value)
		m.connections.Remove(element)
		element = nextElement
	}
	if m.disposalStageHook != nil {
		m.disposalStageHook()
	}
	m.stageDisposals(closers)
	m.access.Unlock()
	return len(closers)
}

// AcquireConnection reserves one admission slot, atomically with the manager's lifecycle.
//
// It is the replacement for the two-step "wait, then track": the ticket is the caller's proof that
// it may hand over a socket, and Close waits until every outstanding ticket has settled. A dial that
// was authorized before shutdown but finishes after it is rejected: the socket is put in the
// pending ledger (so the final Close still waits for the physical close) and the dial returns
// net.ErrClosed.
func (m *ConnectionManager) AcquireConnection(ctx context.Context) (adapter.ConnectionTicket, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	priority := adapter.IsControlPlaneAdmission(ctx)
	d := &m.disposal
	d.mu.Lock()
	m.initDisposalLocked()
	for {
		// A cancelled dial must not be admitted just because room exists: the caller has already
		// withdrawn, and installing a socket for it would charge the manager for work nobody owns.
		if ctxErr := ctx.Err(); ctxErr != nil {
			d.mu.Unlock()
			return nil, ctxErr
		}
		if d.shutdown || m.closed.Load() {
			d.mu.Unlock()
			return nil, net.ErrClosed
		}
		if priority {
			if d.priorityAdmitted < controlPlaneAdmissionReservation {
				d.priorityAdmitted++
				d.admitted++
				m.signalRoomLocked()
				d.cond.Broadcast()
				d.mu.Unlock()
				return &admissionTicket{manager: m, priority: true}, nil
			}
		} else if m.disposalBacklogLocked() < m.disposalBudget() {
			d.admitted++
			m.signalRoomLocked()
			d.cond.Broadcast()
			d.mu.Unlock()
			return &admissionTicket{manager: m}, nil
		}
		signal := d.roomSignal
		d.mu.Unlock()
		select {
		case <-signal:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		d.mu.Lock()
	}
}

type admissionTicket struct {
	manager  *ConnectionManager
	priority bool
	// settled is the once-only consumption latch: the admission slot is returned by exactly one
	// of Release, a successful Track, or the rejection path, and every later call is inert. It is
	// atomic because Track and Release are not promised to be the same goroutine once a dial is
	// cancelled while it is being tracked.
	settled atomic.Bool
}

// consume claims this ticket's one consumption and reports whether this call owns it. Returning
// the slot twice is the defect this function exists to make impossible: the second decrement would
// land on whichever ticket happens to be outstanding at the time.
func (t *admissionTicket) consume() bool {
	if t == nil || !t.settled.CompareAndSwap(false, true) {
		return false
	}
	t.manager.settleAdmission(t.priority)
	return true
}

func (t *admissionTicket) Release() {
	t.consume()
}

// settleAdmissionLocked returns the slot. Caller holds d.mu.
func (m *ConnectionManager) settleAdmissionLocked(priority bool) {
	d := &m.disposal
	if d.admitted > 0 {
		d.admitted--
	}
	if priority && d.priorityAdmitted > 0 {
		d.priorityAdmitted--
	}
	m.signalRoomLocked()
	d.cond.Broadcast()
}

func (m *ConnectionManager) settleAdmission(priority bool) {
	d := &m.disposal
	d.mu.Lock()
	m.settleAdmissionLocked(priority)
	d.mu.Unlock()
}

// rejectAdmissionLocked puts a socket the manager will not track into the pending ledger, then
// releases the ticket. Caller holds m.access; this takes d.mu (the same order as stageDisposals).
func (m *ConnectionManager) rejectAdmissionLocked(ticket *admissionTicket, closer io.Closer) {
	d := &m.disposal
	d.mu.Lock()
	d.pending++
	d.queue = append(d.queue, &shutdownSocket{closer: closer})
	if !d.worker {
		d.worker = true
		go m.disposalWorker()
	}
	// The rejection owns the consumption only if nobody else claimed it first.
	if ticket.settled.CompareAndSwap(false, true) {
		m.settleAdmissionLocked(ticket.priority)
	}
	d.mu.Unlock()
}

func (t *admissionTicket) TrackConn(conn net.Conn) (net.Conn, error) {
	if t == nil || t.settled.Load() {
		return nil, net.ErrClosed
	}
	manager := t.manager
	tracked := manager.newTrackedConn(conn)
	manager.access.Lock()
	if manager.closed.Load() {
		manager.rejectAdmissionLocked(t, conn)
		manager.access.Unlock()
		return nil, net.ErrClosed
	}
	tracked.element = manager.connections.PushBack(tracked)
	manager.access.Unlock()
	// The successful hand-over consumes the ticket. Settling here without latching it is what let a
	// later Release(A) decrement an unrelated outstanding ticket B.
	t.consume()
	return tracked, nil
}

func (t *admissionTicket) TrackPacketConn(conn net.PacketConn) (net.PacketConn, error) {
	if t == nil || t.settled.Load() {
		return nil, net.ErrClosed
	}
	manager := t.manager
	tracked := manager.newTrackedPacketConn(conn)
	manager.access.Lock()
	if manager.closed.Load() {
		manager.rejectAdmissionLocked(t, conn)
		manager.access.Unlock()
		return nil, net.ErrClosed
	}
	tracked.element = manager.connections.PushBack(tracked)
	manager.access.Unlock()
	t.consume()
	return tracked, nil
}

// shutdownSocket is a socket the manager rejected after shutdown. It has no tracked element; it only
// needs to be owned by the disposal ledger so the final Close waits for its physical close.
type shutdownSocket struct {
	closer io.Closer
}

func (c *shutdownSocket) Close() error { return c.closer.Close() }

func (c *shutdownSocket) reclaimState() *managedConnState { return &managedConnState{} }

// disposalConcurrency is the number of physical closes one manager may run at once.
// It is one, deliberately: the sockets being closed are already unreachable by the new
// generation, so parallelism buys nothing, while one worker makes the bound a constant
// that does not grow with the number of transitions.
const disposalConcurrency = 1

// disposalBacklogBudget is how many connections may be waiting for their physical close
// before a reclaim must wait for room. It is backpressure, not a drop policy: the objects
// are already owned by the disposer, and the caller waits instead of the backlog growing
// with every change. A budget in connections (not in batches) also bounds a single
// close-all reclaim of thousands of sockets.
const disposalBacklogBudget = 64

// disposalState is the ownership ledger for physical closes.
type disposalState struct {
	once sync.Once
	mu   sync.Mutex
	// cond signals both the worker (work arrived, shutdown) and budget/shutdown waiters.
	cond *sync.Cond
	// queue holds the exact connections whose close has been commissioned but not started.
	queue []managedConn
	// pending counts every commissioned connection that has not been physically closed yet,
	// including the one the worker is inside right now. Shutdown waits for it to reach zero.
	pending int
	// admitted counts authorization tickets that have not been consumed yet: a dial is in
	// progress and owns a socket that the manager has not seen. Shutdown waits for them too.
	admitted int
	// priorityAdmitted is the subset of admitted that came from the control-plane pool.
	priorityAdmitted int
	// syncing counts objects a synchronous caller (CloseAll) is inside Close on right now.
	// They left the tracked/condemned ledger, so without this account the final Close could
	// return while a synchronous closer still holds a socket.
	syncing int
	// condemned holds retired connections that do not fit in the queue's budget. They are
	// still owned by the manager (CloseAll and Close see them), but they are not tracked and
	// not admissible; the worker promotes them as physical closes free room. This is what
	// keeps the queue bounded without making the network-change body wait.
	condemned []managedConn
	// roomSignal is the cancelable form of cond for admission: WaitForConnectionRoom selects
	// on it AND the caller's context, which a sync.Cond cannot do. It is closed and replaced
	// on every ledger change (broadcast).
	roomSignal chan struct{}
	worker     bool
	shutdown   bool
}

// controlPlaneAdmissionReservation is the number of concurrent cold control-plane dials (DNS)
// that may proceed while the normal admission budget is exhausted. It is small on purpose:
// the resolver needs to reach its server, not to become a second data path.
const controlPlaneAdmissionReservation = 4

// connectionRoomWaiter is the legacy wait-only capability. The dialer prefers
// adapter.ConnectionAdmission, which is atomic with tracking; this stays for managers and
// tests that only need the pressure signal.
//
// It is optional rather than part of adapter.ConnectionManager so that every other
// implementation keeps the previous behaviour: no budget, no admission gate.
type connectionRoomWaiter interface {
	WaitForConnectionRoom(ctx context.Context) error
}

func (m *ConnectionManager) initDisposalLocked() {
	m.disposal.once.Do(func() {
		m.disposal.cond = sync.NewCond(&m.disposal.mu)
		m.disposal.roomSignal = make(chan struct{})
	})
}

// signalRoomLocked wakes every admission waiter. Caller holds d.mu.
func (m *ConnectionManager) signalRoomLocked() {
	if m.disposal.roomSignal == nil {
		return
	}
	close(m.disposal.roomSignal)
	m.disposal.roomSignal = make(chan struct{})
}

// disposalBacklogLocked is the total owned-but-not-closed set: the queue (and the close in
// flight) plus the condemned. Caller holds d.mu.
func (m *ConnectionManager) disposalBacklogLocked() int {
	d := &m.disposal
	return d.pending + len(d.condemned) + d.syncing + d.admitted
}

// WaitForConnectionRoom is the saturation admission point. The dialer calls it before it
// tracks a freshly dialled connection: while the disposal backlog is at or over the budget,
// a new connection would only add another object to a set the manager cannot close, so the
// dial waits - and can be cancelled by its context - until a physical close frees room.
//
// Shutdown releases every waiter: Close's own contract is the stronger one.
func (m *ConnectionManager) WaitForConnectionRoom(ctx context.Context) error {
	d := &m.disposal
	d.mu.Lock()
	m.initDisposalLocked()
	for !d.shutdown && m.disposalBacklogLocked() >= m.disposalBudget() {
		signal := d.roomSignal
		d.mu.Unlock()
		select {
		case <-signal:
		case <-ctx.Done():
			return ctx.Err()
		}
		d.mu.Lock()
	}
	shutdown := d.shutdown
	d.mu.Unlock()
	if shutdown {
		// A wake-up from shutdown is not room. Admitting here would track a connection
		// into a manager whose CloseAll has already drained the list.
		return E.New("connection manager is closed")
	}
	return nil
}

// promoteCondemnedLocked moves condemned objects into the queue up to the budget. Caller
// holds d.mu. Promotion stops during shutdown: CloseAll takes the condemned set itself, so
// shutdown still closes everything synchronously.
func (m *ConnectionManager) promoteCondemnedLocked() {
	d := &m.disposal
	if d.shutdown {
		return
	}
	room := m.disposalBudget() - d.pending
	promoted := false
	for room > 0 && len(d.condemned) > 0 {
		d.queue = append(d.queue, d.condemned[0])
		d.condemned[0] = nil
		d.condemned = d.condemned[1:]
		d.pending++
		room--
		promoted = true
	}
	if len(d.condemned) == 0 {
		d.condemned = nil
	}
	if promoted {
		m.signalRoomLocked()
	}
}

// synchronousCloses reports objects a CloseAll caller is closing inline right now.
func (m *ConnectionManager) synchronousCloses() int {
	d := &m.disposal
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.syncing
}

// admittedConnections reports outstanding admission tickets (authorized dials not yet tracked).
func (m *ConnectionManager) admittedConnections() int {
	d := &m.disposal
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.admitted
}

// priorityAdmittedConnections reports the control-plane subset of admittedConnections.
func (m *ConnectionManager) priorityAdmittedConnections() int {
	d := &m.disposal
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.priorityAdmitted
}

// condemnedDisposals reports the deferred set: owned, not tracked, not yet queued.
func (m *ConnectionManager) condemnedDisposals() int {
	d := &m.disposal
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.condemned)
}

// takeCondemnedLocked removes and returns the whole condemned set for a synchronous close.
// Caller holds d.mu.
func (m *ConnectionManager) takeCondemnedLocked() []managedConn {
	condemned := m.disposal.condemned
	m.disposal.condemned = nil
	// The caller is about to close these inline; count them so the final Close can see them.
	m.disposal.syncing += len(condemned)
	return condemned
}

func (m *ConnectionManager) disposalBudget() int {
	if m.disposalBudgetOverride > 0 {
		return m.disposalBudgetOverride
	}
	return disposalBacklogBudget
}

// stageDisposals takes ownership of closers. The caller holds m.access, which is what makes
// the transfer atomic with shutdown: CloseAll takes the same lock, so once shutdown can
// proceed, every object that already left the tracked set is owned by the disposal ledger.
//
// Only as many objects as the budget has room for enter the queue. The rest become condemned:
// still owned, still on the books, but not queued and not admissible. The call never waits,
// which is what keeps the DNS pin and the generation commit off the close path.
func (m *ConnectionManager) stageDisposals(closers []managedConn) {
	if len(closers) == 0 {
		return
	}
	d := &m.disposal
	d.mu.Lock()
	m.initDisposalLocked()
	if d.shutdown {
		// Shutdown owns the remainder: queue everything (the worker still drains after
		// shutdown) so Close's pending wait covers it. Nothing may be condemned after the
		// flag is set, or CloseAll could take an empty condemned set and still return.
		d.queue = append(d.queue, closers...)
		d.pending += len(closers)
		if !d.worker {
			d.worker = true
			go m.disposalWorker()
		}
		m.signalRoomLocked()
		d.cond.Broadcast()
		d.mu.Unlock()
		return
	}
	room := m.disposalBudget() - d.pending
	if room > len(closers) {
		room = len(closers)
	}
	if room > 0 {
		d.queue = append(d.queue, closers[:room]...)
		d.pending += room
	}
	if room < len(closers) {
		d.condemned = append(d.condemned, closers[room:]...)
	}
	if !d.worker {
		d.worker = true
		go m.disposalWorker()
	}
	m.signalRoomLocked()
	d.cond.Broadcast()
	d.mu.Unlock()
}

// disposalWorker is the single owner of the physical close. It drains the queue even after
// shutdown began, until the queue is empty; shutdown's wait is on pending, which the worker
// decrements after each close returns.
func (m *ConnectionManager) disposalWorker() {
	d := &m.disposal
	for {
		d.mu.Lock()
		for len(d.queue) == 0 && !d.shutdown {
			d.cond.Wait()
		}
		if len(d.queue) == 0 {
			d.worker = false
			d.mu.Unlock()
			return
		}
		closer := d.queue[0]
		d.queue[0] = nil
		d.queue = d.queue[1:]
		if len(d.queue) == 0 {
			d.queue = nil
		}
		d.mu.Unlock()
		common.Close(closer)
		d.mu.Lock()
		d.pending--
		m.promoteCondemnedLocked()
		m.signalRoomLocked()
		d.cond.Broadcast()
		d.mu.Unlock()
	}
}

// beginSynchronousCloses accounts for objects a synchronous caller is closing inline.
func (m *ConnectionManager) beginSynchronousCloses(count int) {
	if count == 0 {
		return
	}
	d := &m.disposal
	d.mu.Lock()
	m.initDisposalLocked()
	d.syncing += count
	m.signalRoomLocked()
	d.cond.Broadcast()
	d.mu.Unlock()
}

// endSynchronousCloses releases the synchronous account as the closes complete.
func (m *ConnectionManager) endSynchronousCloses(count int) {
	if count == 0 {
		return
	}
	d := &m.disposal
	d.mu.Lock()
	d.syncing -= count
	m.signalRoomLocked()
	d.cond.Broadcast()
	d.mu.Unlock()
}

// waitForTrackedCloses is CloseAll's wait: the queue and synchronous closes, but not dials
// that only hold a ticket (the caller did not ask CloseAll to cancel those).
func (m *ConnectionManager) waitForTrackedCloses() {
	d := &m.disposal
	d.mu.Lock()
	m.initDisposalLocked()
	for d.pending > 0 || d.syncing > 0 {
		d.cond.Wait()
	}
	d.mu.Unlock()
}

// waitForDisposals is shutdown's completion contract: it returns only when every commissioned
// connection has been physically closed and every authorized dial has settled.
func (m *ConnectionManager) waitForDisposals() {
	d := &m.disposal
	d.mu.Lock()
	m.initDisposalLocked()
	for d.pending > 0 || d.syncing > 0 || d.admitted > 0 {
		d.cond.Wait()
	}
	d.mu.Unlock()
}

// pendingDisposals reports the ownership ledger. Tests use it to tell "removed from the
// tracked set" from "physically closed".
func (m *ConnectionManager) pendingDisposals() int {
	d := &m.disposal
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.pending
}

// beginDrainSweep keeps reclaiming connections the transition left behind until none are left.
//
// Drain that never ends is a leak, and drain that ends on a timer kills working connections. The
// sweep is how both are avoided at once: it re-asks the same question as the immediate pass - is
// this connection provably idle? - so a stream that is still transferring is never reclaimed no
// matter how long ago the transition was, and one that has fallen silent is reclaimed promptly.
func (m *ConnectionManager) beginDrainSweep() {
	m.sweepAccess.Lock()
	defer m.sweepAccess.Unlock()
	// A new transition is new information: whatever the last drain was waiting on, this one starts
	// its own accounting at the base interval.
	m.sweepBackoff = 0
	if m.sweepTimer != nil {
		return
	}
	m.scheduleDrainSweepLocked()
}

// sweepDelayLocked is the delay the next pass would use. Caller holds sweepAccess.
func (m *ConnectionManager) sweepDelayLocked() time.Duration {
	shift := m.sweepBackoff
	if shift > 8 {
		shift = 8
	}
	delay := m.sweepEvery() * time.Duration(1<<shift)
	if delay > drainSweepMaxInterval || delay <= 0 {
		delay = drainSweepMaxInterval
	}
	return delay
}

// scheduleDrainSweepLocked arms the sweep, doubling the delay for each pass that reclaimed nothing.
// Caller holds sweepAccess.
func (m *ConnectionManager) scheduleDrainSweepLocked() {
	m.sweepTimer = time.AfterFunc(m.sweepDelayLocked(), func() {
		m.sweepAccess.Lock()
		m.sweepTimer = nil
		m.sweepAccess.Unlock()
		remaining := m.sweepStaleConnections()
		if remaining {
			m.sweepAccess.Lock()
			// Only if nothing else restarted it, and only while the manager is alive.
			if m.sweepTimer == nil && !m.closed.Load() {
				m.scheduleDrainSweepLocked()
			}
			m.sweepAccess.Unlock()
		}
	})
}

// sweepStaleConnections reclaims provably idle connections from previous generations and reports
// whether any old-generation connection is still held.
func (m *ConnectionManager) sweepStaleConnections() bool {
	now := time.Now()
	current := m.generation.Load()
	m.access.Lock()
	var closers []managedConn
	remaining := false
	for element := m.connections.Front(); element != nil; {
		nextElement := element.Next()
		conn := element.Value
		state := conn.reclaimState()
		if state.generation < current {
			if idle, known := state.idleFor(now); known && idle >= m.drainGrace() {
				closers = append(closers, conn)
				m.connections.Remove(element)
				element = nextElement
				continue
			}
			remaining = true
		}
		element = nextElement
	}
	if m.disposalStageHook != nil {
		m.disposalStageHook()
	}
	m.stageDisposals(closers)
	m.access.Unlock()
	if len(closers) > 0 {
		m.transitions.recordSweep(len(closers))
		// Progress. Stay at the base interval while connections are still being found, because the
		// next one may fall silent soon.
		m.sweepAccess.Lock()
		m.sweepBackoff = 0
		m.sweepAccess.Unlock()
		if m.reclaimLog != nil {
			m.reclaimLog(ReclaimNetworkTransition, len(closers), current)
		}
	} else if remaining {
		// Nothing reclaimed and something left. If what is left cannot be judged, this pass produced
		// no information at all, and repeating it at the same rate produces none either.
		m.sweepAccess.Lock()
		m.sweepBackoff++
		m.sweepAccess.Unlock()
	}
	return remaining
}

// stopDrainSweep is called from Close: a timer that fires into a torn-down manager would be a use
// after the lifecycle that owns it has ended.
func (m *ConnectionManager) stopDrainSweep() {
	m.sweepAccess.Lock()
	defer m.sweepAccess.Unlock()
	if m.sweepTimer != nil {
		m.sweepTimer.Stop()
		m.sweepTimer = nil
	}
}

// networkTransitionReclaimer is the capability resetNetworkLocked asks for.
//
// It lives here rather than on adapter.ConnectionManager because the reason is a route-level policy
// decision: the adapter interface describes what a connection manager IS, and this describes what
// this fork's network lifecycle additionally does with one. Asking for the capability keeps the
// drain from becoming a breaking change for every implementation that has no reclaim policy - those
// keep the previous, conservative "close everything" semantics.
type networkTransitionReclaimer interface {
	Reclaim(reason ReclaimReason) int
}

// sweepInterval is the delay the next drain pass would use.
//
// It exists because the property that matters here is not observable from the outside: a sweep that
// has stopped making progress must stop asking at the base rate, and on a phone the difference is
// wakeups. Reading it takes sweepAccess and allocates nothing.
func (m *ConnectionManager) sweepInterval() time.Duration {
	m.sweepAccess.Lock()
	defer m.sweepAccess.Unlock()
	return m.sweepDelayLocked()
}
