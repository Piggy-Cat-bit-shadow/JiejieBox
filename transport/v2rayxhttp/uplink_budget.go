package v2rayxhttp

import "sync"

// Shared packet-up upload budget.
//
// # Why a per-stream bound is not a bound
//
// packetUpPendingBound bounds ONE uplink's queue, and until this file existed that
// was the only byte bound in the packet-up path. Every dial of the same XHTTP
// outbound creates its own packetConn and therefore its own packetUplink, so N
// concurrent streams of one transport retained N times the per-stream bound. That
// is not a small correction: with the default sc_max_each_post_bytes the per-stream
// queue is 2,000,000 B, and a gated-post measurement (the probe this file's tests
// grew out of) showed 6 streams retaining 12,000,000 B with nothing in the tree
// objecting. On a phone, where this transport actually runs, that is a memory
// regression waiting for a slow path.
//
// The pool below is that missing bound. It is per Client 鈥?one XHTTP transport,
// i.e. one outbound 鈥?because that is the ownership level at which the streams
// exist: Client owns the xmux pool, the dials and the conns. A Box with several
// XHTTP outbounds consequently has one pool per such outbound, not one pool for
// the process. Aggregating across outbounds is a Box-level ledger question and
// deliberately not invented here: see the stage-03 record (a process-wide ledger
// with no production reader would be an interface only tests consume).
//
// # Two pools, one charge
//
// The pool is split into a main pool and a reserve:
//
//	main    keeps bulk uploads in flight. Its capacity is
//	        packetUpSharedCapacity(per-stream bound): four streams' worth,
//	        capped so the aggregate stays modest on mobile.
//	reserve keeps SMALL writes moving while the main pool is exhausted, so a
//	        new stream's opening exchange (the VLESS header plus the first
//	        payload) does not queue behind somebody's 8 MiB burst. It is what
//	        "the control flow can make progress" means concretely in packet-up,
//	        which has no separate control frames: the handshake writes are the
//	        control path.
//
// Both pools charge LOGICAL payload bytes and bound the aggregate retained in the
// queue plus the slice a POST owns. The queue's array capacity can exceed the
// logical count after a burst; that is reported per stream as RetainedCapacity and
// is not what this pool bounds. Bytes are charged when they enter the queue and
// released when the POST that owns them returns (posted) or when the queue is
// abandoned (failed/aborted/closed), so a charge lives exactly as long as the
// memory it stands for.
//
// # Ownership and waiting
//
// The rules that make the accounting provable:
//
//   - A stream is charged ONLY for bytes it has already appended to its own
//     queue. take() grants and the append happens in the same critical section,
//     so a writer never holds an unspent charge across a wait (nothing to leak
//     when it gives up).
//   - Every release goes through release(); a post's success, its failure, the
//     queue abandoned by fail/abort and the pending bytes dropped by Close all end
//     at that one call, with the count taken from the bytes themselves rather than
//     from a second bookkeeping field.
//   - The pool's totals are the sum of per-stream charges by construction:
//     Used = 危 charge.main and ReservedUsed = 危 charge.reserve. Tests assert that
//     identity against the per-stream snapshots, so a stream cannot retain bytes
//     the pool has forgotten.
//   - Lock order is uplink.access then budget.access. The reverse never happens:
//     wake-ups are broadcast through the uplink's own leaf mutex (see
//     packetUplink.broadcastWake), not by taking uplink.access from a pool
//     caller.
//
// Waiting is monotone: a writer that cannot be granted adds itself to waiters and
// sleeps until any release or room change wakes it. Only the head of waiters may
// draw from the main pool, so a stream that churns cannot take every release
// before the others see it; a stream that is not head may still draw from the
// reserve, which cannot starve the main pool and is capped per stream.
//
// Fairness is best-effort rather than strict FIFO: a head that is granted part of
// what it wants leaves the queue (progress is the point) and rejoins at the tail
// if it needs more, which interleaves the bulk writers on successive posts.
type sharedUplinkBudget struct {
	access sync.Mutex

	// capacity bounds the main pool; reserve bounds the small-write headroom.
	capacity int
	reserve  int
	// used and reservedUsed are what the two pools have charged right now.
	used         int
	reservedUsed int
	// peak is the worst Used+ReservedUsed observed, the answer to "what did the
	// worst moment look like" that a snapshot after the burst cannot give.
	peak int
	// charges is the per-stream breakdown: one entry per live uplink.
	charges map[*packetUplink]*uplinkBudgetCharge
	// waiters holds the streams waiting for main-pool capacity, oldest first.
	waiters []*packetUplink
	// granted and released are the pool's lifetime flow, and the conservation
	// check: granted - released == used + reservedUsed at every quiescent point.
	granted  uint64
	released uint64
	// excessReleases counts bytes a caller asked to release beyond what the
	// stream held. It must stay zero; a non-zero value means a release path is
	// double-counting, which is exactly the bug the ledger tests would otherwise
	// only see as a missing total.
	excessReleases uint64
	// leftover is what unregister still found charged on a stream whose flusher
	// had exited. It must stay zero as well: a non-zero value is memory the pool
	// was still holding for a stream that stopped.
	leftover uint64
}

// uplinkBudgetCharge is how much of each pool one stream holds.
type uplinkBudgetCharge struct {
	main    int
	reserve int
}

// uplinkBudgetStats is a consistent snapshot of the shared pool.
//
// The identities a reader can rely on:
//
//	Used            == 危 stream.main        ReservedUsed == 危 stream.reserve
//	GrantedBytes - ReleasedBytes == Used + ReservedUsed
//	Used <= Capacity
//	Peak <= Capacity + Reserve
type uplinkBudgetStats struct {
	Capacity      int
	Reserve       int
	Used          int
	ReservedUsed  int
	Peak          int
	Streams       int
	Waiters       int
	GrantedBytes  uint64
	ReleasedBytes uint64
	// ExcessReleases and LeftoverBytes are defect counters, not policy: both
	// must be zero. ExcessReleases counts over-releases; LeftoverBytes counts
	// charges still held by a stream whose flusher exited.
	ExcessReleases uint64
	LeftoverBytes  uint64
}

// Pool sizing. These are policy values, not protocol constants: they trade a
// burst of parallel streams against the aggregate a phone may hold, and they are
// derived from the configured per-stream bound rather than being a second config
// surface.
const (
	// packetUpSharedStreams is how many streams' per-stream queue bound the main
	// pool admits at once before writers queue.
	packetUpSharedStreams = 4
	// packetUpSharedCeiling caps the main pool for a large configured
	// sc_max_each_post_bytes. Eight MiB is already four streams at the default
	// size; beyond this, more streams mean they queue, which is the intended
	// backpressure rather than more retention.
	packetUpSharedCeiling = 8 << 20
	// packetUpReserveOpeningBytes is how much a stream may have written before it stops
	// being eligible for the reserve at all. Reserve eligibility is a FLOW STATE, not a
	// write size: a relay's read blocks can be small too, and the audit reproduced a long
	// stream eating the control headroom with 8 KiB writes until a new stream's 1 KiB
	// first write had to wait. A stream's opening (first packets, handshake) is the state
	// the transport can recognise on the production path; beyond it, only the main pool
	// applies and the flow queues like any bulk stream.
	packetUpReserveOpeningBytes = 64 << 10
	// packetUpSmallWrite is the largest write that may draw on the reserve. It is a
	// SHORT FLOW's ceiling, not just a handshake's: at 16 KiB the reserve only saved
	// the opening exchange, and a measured burst of long streams pushed 96-256 KiB
	// flows from one round trip to about three (see
	// _next/REPORT_01_璺ㄦ祦瀛楄妭棰勭畻瀵圭収.md). A short flow writes its whole body in one
	// call, so the size class has to reach past it for the reserve to mean anything.
	packetUpSmallWrite = 256 << 10
	// packetUpSharedReserve is the reserve pool: a quarter of the main capacity at the
	// default size, so several short flows can pass while the main pool is full. It is
	// part of the aggregate bound (capacity+reserve), which is why it is not larger:
	// the point is a bounded headroom for latency-sensitive flows, not a second pool.
	packetUpSharedReserve = 2 << 20
	// packetUpStreamReserve caps what one stream may hold in the reserve, so a stream
	// with a steady trickle of small writes cannot own the whole headroom. Two short
	// flows' worth at the default size.
	packetUpStreamReserve = 512 << 10
)

// packetUpSharedCapacity returns the main pool's capacity for a per-stream bound.
//
// Four streams' worth is the default: enough that a normal connection burst (a
// page load's parallel streams, or one stream queueing while another posts) does
// not serialize, and small enough that the aggregate stays bounded on a phone.
// packetUpSharedCeiling caps the worst case for a large configured
// sc_max_each_post_bytes; the floor is the per-stream bound, so the pool is never
// smaller than one stream is allowed to queue.
func packetUpSharedCapacity(perStreamBound int) int {
	capacity := perStreamBound * packetUpSharedStreams
	if capacity > packetUpSharedCeiling {
		capacity = packetUpSharedCeiling
	}
	if capacity < perStreamBound {
		capacity = perStreamBound
	}
	return capacity
}

// newSharedUplinkBudget builds a pool. The reserve is clamped to half the main
// capacity: the two pools are separate, but a reserve that could exceed the main
// pool would let the headroom path outgrow the bound it is meant to protect. At the
// production size the configured reserve is a quarter of the capacity, so the clamp
// only binds for unusually small sc_max_each_post_bytes.
func newSharedUplinkBudget(capacity, reserve int) *sharedUplinkBudget {
	if reserve < 0 {
		reserve = 0
	}
	if limit := capacity / 2; reserve > limit {
		reserve = limit
	}
	return &sharedUplinkBudget{
		capacity: capacity,
		reserve:  reserve,
		charges:  make(map[*packetUplink]*uplinkBudgetCharge),
	}
}

// register adds an uplink to the pool. It is called once, from newPacketUplink,
// before the flusher can post and before a writer can charge.
func (b *sharedUplinkBudget) register(u *packetUplink) {
	if b == nil || u == nil {
		return
	}
	b.access.Lock()
	if _, loaded := b.charges[u]; !loaded {
		b.charges[u] = &uplinkBudgetCharge{}
	}
	b.access.Unlock()
}

// take charges up to want logical bytes for u and returns how many it charged.
//
// Caller must hold u.access: the granted bytes are appended to the stream's queue
// in the same critical section, so the charge and the memory it stands for are
// created together. want must already be bounded by the room the stream's own
// queue has left (see packetUplink.write); the per-stream bound stays in force
// independently of this pool.
//
// small marks a write at or below packetUpSmallWrite, the only writes that may
// draw on the reserve.
func (b *sharedUplinkBudget) take(u *packetUplink, want int, small bool) int {
	if b == nil || want <= 0 {
		return want
	}
	b.access.Lock()
	charge, loaded := b.charges[u]
	if !loaded {
		charge = &uplinkBudgetCharge{}
		b.charges[u] = charge
	}
	granted := 0
	if b.isHeadLocked(u) {
		available := b.capacity - b.used
		if available > 0 {
			granted = want
			if granted > available {
				granted = available
			}
			charge.main += granted
			b.used += granted
		}
	}
	if granted < want && small {
		// The reserve is the control path: it is not subject to the main pool's
		// queue discipline and it cannot grow without bound, because it is
		// drained by the same posts and capped per stream.
		reservedAvailable := b.reserve - b.reservedUsed
		streamAvailable := packetUpStreamReserve - charge.reserve
		chunk := want - granted
		if chunk > reservedAvailable {
			chunk = reservedAvailable
		}
		if chunk > streamAvailable {
			chunk = streamAvailable
		}
		if chunk > 0 {
			charge.reserve += chunk
			b.reservedUsed += chunk
			granted += chunk
		}
	}
	if granted == 0 {
		b.addWaiterLocked(u)
	} else {
		b.removeWaiterLocked(u)
		b.granted += uint64(granted)
		if total := b.used + b.reservedUsed; total > b.peak {
			b.peak = total
		}
	}
	b.access.Unlock()
	return granted
}

// release returns n logical bytes of u's charge to the pools they came from.
//
// It is the single settlement call: a posted slice, a failed post's slice, the
// queue abandoned by fail/abort and a closed uplink's remnants all arrive here
// with the byte count of what actually left the accounting. Releasing beyond what
// the stream holds is clamped and counted (excessReleases); it never drives a pool
// negative.
func (b *sharedUplinkBudget) release(u *packetUplink, n int) {
	if b == nil || n <= 0 {
		return
	}
	b.access.Lock()
	charge, loaded := b.charges[u]
	if !loaded {
		b.excessReleases += uint64(n)
		b.access.Unlock()
		return
	}
	fromReserve := n
	if fromReserve > charge.reserve {
		fromReserve = charge.reserve
	}
	fromMain := n - fromReserve
	if fromMain > charge.main {
		b.excessReleases += uint64(fromMain - charge.main)
		fromMain = charge.main
	}
	charge.reserve -= fromReserve
	charge.main -= fromMain
	b.reservedUsed -= fromReserve
	b.used -= fromMain
	b.released += uint64(fromReserve + fromMain)
	b.access.Unlock()
	b.wakeWaiters()
}

// withdraw removes u from the waiters list. A writer calls it when it leaves
// without a charge (closed, failed), so a dead stream cannot sit at the head and
// block the others.
func (b *sharedUplinkBudget) withdraw(u *packetUplink) {
	if b == nil {
		return
	}
	b.access.Lock()
	moved := b.removeWaiterLocked(u)
	b.access.Unlock()
	if moved {
		b.wakeWaiters()
	}
}

// unregister drops a stream whose flusher exited, releasing anything it still
// held (which must be nothing) and returning the leftover for the caller to
// report. Leftover bytes are counted in the snapshot's LeftoverBytes.
func (b *sharedUplinkBudget) unregister(u *packetUplink) int {
	if b == nil {
		return 0
	}
	b.access.Lock()
	charge, loaded := b.charges[u]
	if !loaded {
		b.access.Unlock()
		return 0
	}
	leftover := charge.main + charge.reserve
	b.used -= charge.main
	b.reservedUsed -= charge.reserve
	b.released += uint64(leftover)
	b.leftover += uint64(leftover)
	delete(b.charges, u)
	b.removeWaiterLocked(u)
	b.access.Unlock()
	b.wakeWaiters()
	return leftover
}

// held returns the logical bytes u currently holds across both pools. It is safe
// to call while holding u.access (that is the documented lock order); it must not
// be called while holding b.access.
func (b *sharedUplinkBudget) held(u *packetUplink) int {
	if b == nil || u == nil {
		return 0
	}
	b.access.Lock()
	defer b.access.Unlock()
	charge := b.charges[u]
	if charge == nil {
		return 0
	}
	return charge.main + charge.reserve
}

// stats snapshots the pool.
func (b *sharedUplinkBudget) stats() uplinkBudgetStats {
	if b == nil {
		return uplinkBudgetStats{}
	}
	b.access.Lock()
	defer b.access.Unlock()
	return uplinkBudgetStats{
		Capacity:       b.capacity,
		Reserve:        b.reserve,
		Used:           b.used,
		ReservedUsed:   b.reservedUsed,
		Peak:           b.peak,
		Streams:        len(b.charges),
		Waiters:        len(b.waiters),
		GrantedBytes:   b.granted,
		ReleasedBytes:  b.released,
		ExcessReleases: b.excessReleases,
		LeftoverBytes:  b.leftover,
	}
}

// wakeWaiters wakes every stream currently waiting for capacity. It is called
// after a release, after a grant (the head moved) and after a waiter left. The
// broadcast happens after the pool lock is dropped, so a wake-up can never
// participate in a lock cycle even when the caller still holds its own uplink's
// lock.
func (b *sharedUplinkBudget) wakeWaiters() {
	if b == nil {
		return
	}
	b.access.Lock()
	waiters := make([]*packetUplink, len(b.waiters))
	copy(waiters, b.waiters)
	b.access.Unlock()
	for _, waiter := range waiters {
		waiter.broadcastWake()
	}
}

// isHeadLocked reports whether u may draw from the main pool: nobody is waiting,
// or u is the oldest waiter. Caller must hold b.access.
func (b *sharedUplinkBudget) isHeadLocked(u *packetUplink) bool {
	return len(b.waiters) == 0 || b.waiters[0] == u
}

// addWaiterLocked appends u once. Caller must hold b.access.
func (b *sharedUplinkBudget) addWaiterLocked(u *packetUplink) {
	for _, waiter := range b.waiters {
		if waiter == u {
			return
		}
	}
	b.waiters = append(b.waiters, u)
}

// removeWaiterLocked removes u and reports whether the head changed. Caller must
// hold b.access.
func (b *sharedUplinkBudget) removeWaiterLocked(u *packetUplink) bool {
	for index, waiter := range b.waiters {
		if waiter != u {
			continue
		}
		b.waiters = append(b.waiters[:index], b.waiters[index+1:]...)
		return index == 0
	}
	return false
}
