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
// The pool below is that missing bound. It is per Client — one XHTTP transport,
// i.e. one outbound — because that is the ownership level at which the streams
// exist: Client owns the xmux pool, the dials and the conns. A Box with several
// XHTTP outbounds consequently has one pool per such outbound, not one pool for the
// process. Aggregating across outbounds is a Box-level ledger question and
// deliberately not invented here: see the stage-03 record (a process-wide ledger
// with no production reader would be an interface only tests consume).
//
// # Three pools, one charge
//
// The pool is split into a main pool, a control pool and a reserve:
//
//	main    keeps bulk uploads in flight. Its capacity is
//	        packetUpSharedCapacity(per-stream bound): four streams' worth,
//	        capped so the aggregate stays modest on mobile.
//	control is the OPENING guarantee: the bytes a session must deliver before the
//	        server can answer (the VLESS header and first payload) may draw on it
//	        while the main pool is exhausted, and nothing else may. It is the
//	        answer to the audit's P1-A: with only one reserve, a cold-start long
//	        flow's 8 KiB blocks and one oversized opening write could each drain
//	        the headroom the next stream's first packet needed.
//	reserve keeps SHORT-FLOW DATA moving while the main pool is exhausted: a
//	        small request body that writes in one or two calls should not queue
//	        behind somebody's 8 MiB burst.
//
// The three are separate because they answer different questions. main bounds
// throughput; reserve bounds short-flow data's latency; control bounds the latency
// of a NEW stream's first packet. A single pool asked to be control AND short-flow
// data (the state the audit reproduced) lets data spend the guarantee, and the
// guarantee is the one part that must not be spendable: without it a new stream
// cannot even reach the server while a cold-start long flow is opening.
//
// Both main and reserve charge LOGICAL payload bytes; control charges the same
// logical bytes and is bounded by a per-flow ceiling, so no single opening can own
// the whole guarantee. The bound is on the aggregate of the three
// (capacity+control+reserve). The queue's array capacity can exceed the logical
// count after a burst; that is reported separately (see RetainedCapacity) and is
// bounded by the per-stream queue bound, not by this pool. Bytes
// are charged when they enter the queue and released when the POST that owns them
// returns (posted) or when the queue is abandoned (failed/aborted/closed), so a
// charge lives exactly as long as the memory it stands for.
//
// # Ownership and waiting
//
// The rules that make the accounting provable:
//
//   - A stream is charged ONLY for bytes it has already appended to its own
//     queue. take() grants and the append happen in the same critical section,
//     so a writer never holds an unspent charge across a wait (nothing to leak
//     when it gives up).
//   - Every release goes through release(); a post's success, its failure, the
//     queue abandoned by fail/abort and the pending bytes dropped by Close all end
//     at that one call, with the count taken from the bytes themselves rather than
//     from a second bookkeeping field.
//   - The pool's totals are the sum of per-stream charges by construction:
//     Used = Σ charge.main, ControlUsed = Σ charge.control,
//     ReservedUsed = Σ charge.reserve. Tests assert that identity against the
//     per-stream snapshots, so a stream cannot retain bytes the pool has forgotten.
//   - Lock order is uplink.access then budget.access. The reverse never happens:
//     wake-ups are broadcast through the uplink's own leaf mutex (see
//     packetUplink.broadcastWake), not by taking uplink.access from a pool
//     caller.
//
// Waiting is monotone: a writer that cannot be granted adds itself to waiters and
// sleeps until any release or room change wakes it. Only the head of waiters may
// draw from the main pool, so a stream that churns cannot take every release
// before the others see it; a stream that is not head may still draw from the
// control pool (that is the whole point of the guarantee) and from the reserve,
// which cannot starve the main pool and is capped per stream.
//
// Fairness is best-effort rather than strict FIFO: a head that is granted part of
// what it wants leaves the queue (progress is the point) and rejoins at the tail
// if it needs more, which interleaves the bulk writers on successive posts.
type sharedUplinkBudget struct {
	access sync.Mutex

	// capacity bounds the main pool; control bounds the opening guarantee; reserve
	// bounds the short-flow headroom.
	capacity int
	control  int
	reserve  int
	// used, controlUsed and reservedUsed are what the three pools have charged right
	// now.
	used         int
	controlUsed  int
	reservedUsed int
	// peak is the worst (used+controlUsed+reservedUsed) observed, the answer to
	// "what did the worst moment look like" that a snapshot after the burst cannot
	// give.
	peak int
	// The allocation ledger: the queue's ARRAYS, charged by capacity before they are
	// allocated and released when their last reference leaves. It is a separate
	// ledger from the logical byte pools because the two measure different things:
	// the pools bound what is queued, this bounds what is retained. See
	// packetup_alloc.go for why that distinction is a correctness requirement and
	// not a reporting detail.
	//
	// maxArray is the largest array one stream can need: the logical aggregate, since
	// no stream can queue more than every pool together can charge. A single append
	// is chunked to that size, which keeps one stream's array inside the ledger's
	// bound while the whole write still completes.
	allocCapacity int
	allocated     int
	allocPeak     int
	allocGranted  uint64
	allocReleased uint64
	allocLeftover uint64
	maxArray      int
	// charges is the per-stream breakdown: one entry per live uplink.
	charges map[*packetUplink]*uplinkBudgetCharge
	// waiters holds the streams waiting for main-pool capacity, oldest first.
	waiters []*packetUplink
	// allocWaiters holds the streams waiting for allocation capacity. It is a
	// separate list on purpose: sharing one list would let a stream waiting for
	// memory become the main pool's head and stall capacity it is not asking for.
	allocWaiters []*packetUplink
	// granted and released are the pool's lifetime flow, and the conservation
	// check: granted - released == used + controlUsed + reservedUsed at every
	// quiescent point.
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
	control int
	reserve int
	// alloc is the capacity this stream's live queue arrays have charged to the
	// allocation ledger. It is tracked per stream so a snapshot can name the owner;
	// the individual allocations each carry their own charge as well, because one
	// array can be shared by the queue and a post (see packetup_alloc.go).
	alloc int
}

// uplinkBudgetStats is a consistent snapshot of the shared pool.
//
// The identities a reader can rely on:
//
//	Used            == Σ stream.main
//	ControlUsed     == Σ stream.control      ReservedUsed == Σ stream.reserve
//	Allocated       == Σ stream.alloc
//	GrantedBytes - ReleasedBytes == Used + ControlUsed + ReservedUsed
//	AllocGrantedBytes - AllocReleasedBytes == Allocated
//	Used <= Capacity
//	ControlUsed <= Control                   ReservedUsed <= Reserve
//	Allocated <= AllocCapacity
//	Peak <= Capacity + Control + Reserve
//	AllocPeak >= Allocated
type uplinkBudgetStats struct {
	Capacity     int
	Control      int
	Reserve      int
	Used         int
	ControlUsed  int
	ReservedUsed int
	Peak         int
	Streams      int
	Waiters      int
	// The allocation ledger. AllocCapacity bounds Allocated, the capacity of the queue
	// arrays the streams retain right now: a different number from
	// Used+ControlUsed+ReservedUsed whenever an array is larger than its payload, and
	// the one that answers "how much memory does the queue hold". AllocPeak is the
	// worst (Allocated plus the old array a replacement still had to copy from), i.e.
	// the real overlap peak rather than the settled one.
	AllocCapacity int
	Allocated     int
	AllocPeak     int
	AllocWaiters  int
	// GrantedBytes and ReleasedBytes count every logical pool's grants and releases.
	GrantedBytes  uint64
	ReleasedBytes uint64
	// AllocGrantedBytes and AllocReleasedBytes are the same flow for the capacity
	// ledger.
	AllocGrantedBytes  uint64
	AllocReleasedBytes uint64
	// ExcessReleases and LeftoverBytes are defect counters, not policy: both
	// must be zero. ExcessReleases counts over-releases; LeftoverBytes counts
	// charges still held by a stream whose flusher exited.
	ExcessReleases uint64
	LeftoverBytes  uint64
	// LeftoverAllocBytes is LeftoverBytes for the allocation ledger.
	LeftoverAllocBytes uint64
}

// Total is the aggregate the three pools have charged, the number that must return
// to zero after a session ends.
func (s uplinkBudgetStats) Total() int {
	return s.Used + s.ControlUsed + s.ReservedUsed
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
	// being eligible for the opening pools at all. Opening eligibility is a FLOW STATE, not a
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
	// _next/REPORT_01_跨流字节预算对照.md). A short flow writes its whole body in one
	// call, so the size class has to reach past it for the reserve to mean anything.
	packetUpSmallWrite = 256 << 10
	// packetUpSharedReserve is the reserve pool: a quarter of the main capacity at the
	// default size, so several short flows can pass while the main pool is full. It is
	// part of the aggregate bound (capacity+control+reserve), which is why it is not
	// larger: the point is a bounded headroom for latency-sensitive short flows, not a
	// second bulk pool.
	packetUpSharedReserve = 2 << 20
	// packetUpStreamReserve caps what one stream may hold in the reserve, so a stream
	// with a steady trickle of small writes cannot own the whole headroom. Two short
	// flows' worth at the default size.
	packetUpStreamReserve = 512 << 10
	// packetUpStreamControl is the most one flow may hold in the control pool at once.
	// A quarter of the flow's opening budget: the opening pair of a packet-up session is
	// a VLESS header and the first payload, and a flow that has queued this much has
	// already had the chance to reach the server. Without this ceiling one cold-start
	// long flow's 8 KiB blocks could take the entire control pool, which is exactly the
	// audit's P1-A with the pool renamed.
	packetUpStreamControl = packetUpReserveOpeningBytes / 4
	// packetUpControlOpenings is how many concurrent openings the control pool
	// guarantees. It bounds the guarantee's cost: beyond this many simultaneous
	// cold starts an opening waits like any other charge, which the tests pin.
	packetUpControlOpenings = 16
	// packetUpControlBytes is the control pool: packetUpControlOpenings openings at the
	// per-flow ceiling. It sits ON TOP of the main and reserve pools rather than being
	// carved out of them, because its job is a guarantee: data must not be able to
	// spend it, and short-flow data must not lose headroom to it.
	packetUpControlBytes = packetUpControlOpenings * packetUpStreamControl
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

// newSharedUplinkBudget builds a pool for a main capacity and a reserve, deriving
// the control pool the way production does.
func newSharedUplinkBudget(capacity, reserve int) *sharedUplinkBudget {
	return newSharedUplinkBudgetWithControl(capacity, reserve, packetUpControlBytes)
}

// newSharedUplinkBudgetWithControl builds a pool with an explicit control pool, for
// the tests that need to fill it. Production uses newSharedUplinkBudget.
//
// The reserve is clamped to half the main capacity and the control pool to half the
// main capacity as well: the two headroom pools are separate pools, but a headroom
// that could exceed the main pool would let the latency path outgrow the bound it
// is meant to protect. At the production size neither clamp binds (the configured
// control pool is a thirty-second of the default capacity, the reserve a quarter),
// so they only shape unusually small sc_max_each_post_bytes.
func newSharedUplinkBudgetWithControl(capacity, reserve, control int) *sharedUplinkBudget {
	if reserve < 0 {
		reserve = 0
	}
	if limit := capacity / 2; reserve > limit {
		reserve = limit
	}
	if control < 0 {
		control = 0
	}
	if limit := capacity / 2; control > limit {
		control = limit
	}
	return &sharedUplinkBudget{
		capacity: capacity,
		control:  control,
		reserve:  reserve,
		// The allocation ledger's bound: the queue arrays are grown geometrically
		// (see packetUpQueueCapacity), so the controlled capacity can exceed the
		// logical aggregate by at most that growth factor. The bound is expressed as a
		// multiple of the logical aggregate so the two scale together with
		// sc_max_each_post_bytes instead of drifting apart. The extra granule covers
		// the rounding of a replacement that is sized from the logical aggregate
		// (pktUpMaxArray), so one stream's array always fits the bound on its own.
		allocCapacity: packetUpAllocOverCommit*(capacity+control+reserve) + 2*packetUpAllocGranule,
		maxArray:      pktUpMaxArray(capacity, control, reserve),
		charges:       make(map[*packetUplink]*uplinkBudgetCharge),
	}
}

// pktUpMaxArray is the largest single append the pool will accept into one queue
// array: the logical aggregate. A stream cannot queue more than all three pools
// together can charge, so a larger array could never be filled; chunking a huge Write
// to this size keeps every array inside the allocation bound while Write still accepts
// the whole payload over successive chunks.
func pktUpMaxArray(capacity, control, reserve int) int {
	total := capacity + control + reserve
	if total < packetUpAllocGranule {
		return packetUpAllocGranule
	}
	return total
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
// The pools are tried in the order main, control, reserve:
//
//   - small marks a write in the short class (at or below packetUpSmallWrite), the
//     only writes that may draw on the control or reserve pools at all;
//   - openLeft is how much of the flow's opening budget is left. The control pool is
//     the opening guarantee, so a single grant and the sum of a write's chunks are
//     both bounded by it, and by packetUpStreamControl per flow, so one opening can
//     never own the whole pool.
//
// Main is tried first on purpose: a stream that can be satisfied by the bulk pool
// leaves the guarantee untouched, and the guarantee is only spent when the bulk pool
// cannot admit the bytes.
func (b *sharedUplinkBudget) take(u *packetUplink, want int, small bool, openLeft int) int {
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
	if granted < want && small && openLeft > 0 {
		// The opening guarantee: not subject to the main pool's queue discipline,
		// because a new stream has no history and must not wait for one.
		chunk := want - granted
		if chunk > openLeft {
			chunk = openLeft
		}
		if available := b.control - b.controlUsed; chunk > available {
			chunk = available
		}
		if room := packetUpStreamControl - charge.control; chunk > room {
			chunk = room
		}
		if chunk > 0 {
			charge.control += chunk
			b.controlUsed += chunk
			granted += chunk
		}
	}
	if granted < want && small {
		// Short-flow data headroom. It cannot grow without bound: it is drained by
		// the same posts and capped per stream.
		chunk := want - granted
		if available := b.reserve - b.reservedUsed; chunk > available {
			chunk = available
		}
		if room := packetUpStreamReserve - charge.reserve; chunk > room {
			chunk = room
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
		if total := b.used + b.controlUsed + b.reservedUsed; total > b.peak {
			b.peak = total
		}
	}
	b.access.Unlock()
	return granted
}

// release returns n charged bytes of u's charge to the pools they came from.
//
// It is the single settlement call: a posted slice, a failed post's slice, the
// queue abandoned by fail/abort and a closed uplink's remnants all arrive here
// with the byte count of what actually left the accounting. Releasing beyond what
// the stream holds is clamped and counted (excessReleases); it never drives a pool
// negative.
//
// A release does not remember which pool a byte came from — a batch can mix them —
// so it settles the headroom pools first and the bulk pool last. The per-stream
// total is what the identity needs; the split is an attribution detail, and the
// headroom pools are the ones whose availability the next opening depends on.
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
	released := 0
	fromReserve := n
	if fromReserve > charge.reserve {
		fromReserve = charge.reserve
	}
	fromControl := n - fromReserve
	if fromControl > charge.control {
		fromControl = charge.control
	}
	fromMain := n - fromReserve - fromControl
	if fromMain > charge.main {
		b.excessReleases += uint64(fromMain - charge.main)
		fromMain = charge.main
	}
	charge.reserve -= fromReserve
	charge.control -= fromControl
	charge.main -= fromMain
	b.reservedUsed -= fromReserve
	b.controlUsed -= fromControl
	b.used -= fromMain
	released = fromReserve + fromControl + fromMain
	b.released += uint64(released)
	b.access.Unlock()
	b.wakeWaiters()
}

// withdraw removes u from both waiters lists. A writer calls it when it leaves
// without a charge (closed, failed), so a dead stream cannot sit at the head and
// block the others.
func (b *sharedUplinkBudget) withdraw(u *packetUplink) {
	if b == nil {
		return
	}
	b.access.Lock()
	moved := b.removeWaiterLocked(u)
	b.removeAllocWaiterLocked(u)
	b.access.Unlock()
	if moved {
		b.wakeWaiters()
	}
}

// unregister drops a stream whose flusher exited, releasing anything it still
// held (which must be nothing) and returning the leftover for the caller to
// report. Leftover bytes are counted in the snapshot's LeftoverBytes, and leftover
// allocation capacity in LeftoverAllocBytes.
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
	leftover := charge.main + charge.control + charge.reserve
	leftoverAlloc := charge.alloc
	b.used -= charge.main
	b.controlUsed -= charge.control
	b.reservedUsed -= charge.reserve
	b.allocated -= leftoverAlloc
	b.released += uint64(leftover)
	b.allocReleased += uint64(leftoverAlloc)
	b.leftover += uint64(leftover)
	if leftoverAlloc > 0 {
		b.allocLeftover += uint64(leftoverAlloc)
	}
	delete(b.charges, u)
	b.removeWaiterLocked(u)
	b.removeAllocWaiterLocked(u)
	b.access.Unlock()
	b.wakeWaiters()
	return leftover
}

// held returns the logical bytes u currently holds across all pools. It is safe
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
	return charge.main + charge.control + charge.reserve
}

// stats snapshots the pool.
func (b *sharedUplinkBudget) stats() uplinkBudgetStats {
	if b == nil {
		return uplinkBudgetStats{}
	}
	b.access.Lock()
	defer b.access.Unlock()
	return uplinkBudgetStats{
		Capacity:           b.capacity,
		Control:            b.control,
		Reserve:            b.reserve,
		Used:               b.used,
		ControlUsed:        b.controlUsed,
		ReservedUsed:       b.reservedUsed,
		Peak:               b.peak,
		Streams:            len(b.charges),
		Waiters:            len(b.waiters),
		AllocCapacity:      b.allocCapacity,
		Allocated:          b.allocated,
		AllocPeak:          b.allocPeak,
		AllocWaiters:       len(b.allocWaiters),
		GrantedBytes:       b.granted,
		ReleasedBytes:      b.released,
		AllocGrantedBytes:  b.allocGranted,
		AllocReleasedBytes: b.allocReleased,
		ExcessReleases:     b.excessReleases,
		LeftoverBytes:      b.leftover,
		LeftoverAllocBytes: b.allocLeftover,
	}
}

// wakeWaiters wakes every stream currently waiting for capacity. It is called
// after a release, after a grant (the head moved), after an allocation release and
// after a waiter left. AllocWaiters is included: a stream parked for a new array
// waits on the same per-uplink wake channel as a stream parked for main-pool room,
// and only this broadcast can reach it. The broadcast happens after the pool lock is
// dropped, so a wake-up can never participate in a lock cycle even when the caller
// still holds its own uplink's lock.
func (b *sharedUplinkBudget) wakeWaiters() {
	if b == nil {
		return
	}
	b.access.Lock()
	waiters := make([]*packetUplink, 0, len(b.waiters)+len(b.allocWaiters))
	waiters = append(waiters, b.waiters...)
	waiters = append(waiters, b.allocWaiters...)
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
