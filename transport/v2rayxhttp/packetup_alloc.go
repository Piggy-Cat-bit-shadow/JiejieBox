package v2rayxhttp

// The allocation ledger: the packet-up queue's ARRAYS, charged by capacity.
//
// # Why logical bytes are not a memory bound
//
// uplink_budget.go bounds logical payload bytes: a stream is charged for what it
// has queued and the charge is returned when the POST that carried those bytes
// returns. The audit's P1-B showed that this is not the same thing as the memory the
// stream retains:
//
//   - the queue is a slice, and the flusher hands a POST a SUB-SLICE of it. When the
//     POST's slice is released (len(payload) bytes), the suffix still queued can be
//     the rest of the SAME array, so the allocation that remains alive is the whole
//     array, not the logical remainder;
//   - growing the queue with append allocates a new array while a POST may still own
//     a slice of the old one, so for a while TWO arrays are alive.
//
// At 64 KiB of main pool, 16 KiB POSTs and the audit's workload, the logical ledger
// said 64 KiB while the two streams' arrays covered at least 80 KiB. The fix is a
// second, capacity-based ledger with owner identity: charge the array before it is
// allocated, count one array once while any reference remains (queue, POST, retry),
// and release it when the last reference leaves.
//
// # Ownership
//
// A queue array is one *packetUpAllocation:
//
//	pending ref      the queue's suffix, when the queue is non-empty;
//	post ref         the batch a POST currently owns, until that POST returns;
//	retry            HTTP transport replays ride the same POST and therefore the same
//	                 reference: no second charge, and no second release.
//
// refs counts them. charged is what the pool charged for the array; it moves to the
// replacement on a transfer (see below) and becomes zero on release, which makes a
// double release impossible to write: dropping a reference to a zero-charged
// allocation is a no-op.
//
// # Growth without a reservation that can deadlock
//
// Capacity is reserved BEFORE make() is called, so the ledger never sees an array it
// has not charged. A writer that needs to grow its queue therefore needs either the
// full new array (the old one is still owned by a POST) or, when the queue is the old
// array's only owner, only the difference: the old charge is TRANSFERRED to the new
// array, so a stream never holds its old quota while waiting for a new one it could
// never be granted. During the copy both arrays exist and the pool records that in
// AllocPeak — the live figure is settled capacity, which is why the peak is the number
// a memory reviewer wants.
//
// The flusher never reserves allocation capacity to post: it slices the queue it
// already owns. So a full allocation ledger delays NEW arrays, never the drain of
// bytes already in flight, which is the "already in flight keeps draining" rule.

const (
	// packetUpAllocGranule rounds a queue array's capacity up to a multiple of this.
	// It bounds the rounding waste per array: small writes (a VLESS header is tens of
	// bytes) still get a small array, while capacity stays a round number the growth
	// policy and the ledger can reason about.
	packetUpAllocGranule = 4 << 10
	// packetUpAllocMaxStep caps how far past the current capacity one growth step may
	// go beyond the requested length. Without it, 1.5x growth on a nearly full 4 MiB
	// queue would reserve 2 MiB of unused capacity; with it, large queues grow in
	// 256 KiB steps while small ones still grow geometrically.
	packetUpAllocMaxStep = 256 << 10
	// packetUpAllocOverCommit sizes the allocation ledger's bound relative to the
	// logical aggregate. 1.5x geometric growth plus a fixed step means an array can
	// hold up to half again the bytes it carries, so twice the logical aggregate is a
	// safe bound for the controlled capacity while still being a real bound.
	packetUpAllocOverCommit = 2
)

// packetUpAllocation is one backing array of the packet-up queue: the unit the
// allocation ledger charges.
type packetUpAllocation struct {
	// capacity is the array's capacity, the bytes it retains.
	capacity int
	// charged is what the pool charged for it. It moves to a replacement on a
	// transfer, and is zero once released, so a stale reference cannot release twice.
	charged int
	// refs counts the holders: the pending queue and every POST that owns a slice.
	refs int
}

// packetUpQueueCapacity returns the capacity to allocate for a queue that needs
// `need` bytes and currently has `current` (0 for a new queue).
//
// Geometric growth keeps the total copying linear in the bytes queued; the fixed step
// keeps the reservation close to the need for large queues; the granule keeps the
// waste bounded. It is a plain function so the policy can be pinned by a test.
func packetUpQueueCapacity(current, need int) int {
	if need <= 0 {
		return 0
	}
	target := need
	if current > 0 {
		grown := current + current/2
		if limit := current + packetUpAllocMaxStep; grown > limit {
			grown = limit
		}
		if grown > target {
			target = grown
		}
	}
	if remainder := target % packetUpAllocGranule; remainder != 0 {
		target += packetUpAllocGranule - remainder
	}
	return target
}

// yieldMainWaiter removes u from the MAIN pool's waiters list without touching the
// allocation waiters list.
//
// It exists because the two waits must not block each other: only the head of the
// main waiters may draw on the main pool, so a stream that is parked for allocation
// capacity must not sit at that head — the streams behind it could satisfy themselves
// from the main pool, and the allocation it waits for is held by arrays whose owners
// are exactly those streams. The probe that found this had three writers, two empty
// queue arrays and no logical charge at all: a textbook head-of-line deadlock across
// two resources.
func (b *sharedUplinkBudget) yieldMainWaiter(u *packetUplink) {
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

// maxArrayBytes returns the largest single append the pool will put in one queue
// array, or 0 when there is no pool (no bound to keep).
func (b *sharedUplinkBudget) maxArrayBytes() int {
	if b == nil {
		return 0
	}
	b.access.Lock()
	defer b.access.Unlock()
	return b.maxArray
}

// reserveAllocation reserves want capacity bytes for u's next queue array. It is
// all-or-nothing: the caller either gets exactly want and allocates, or gets 0 and
// waits. All-or-nothing keeps the ledger simple (no partially installed arrays) and
// makes the wait/release cycle the only progress path.
//
// protected marks a reservation made for an OPENING (control) grant. Data may only use
// allocDataLimit, which is AllocCapacity minus the control pool's worth; an opening may
// use the whole bound. That split is what makes the opening guarantee survive a
// genuinely data-saturated ledger: without it a Box whose bulk streams hold every
// array could refuse a new stream's first packet the memory it needs even though the
// control quota is free.
//
// Caller must hold u.access, which is the documented lock order: uplink then pool.
func (b *sharedUplinkBudget) reserveAllocation(u *packetUplink, want int, protected bool) int {
	if b == nil || want <= 0 {
		return want
	}
	b.access.Lock()
	charge, loaded := b.charges[u]
	if !loaded {
		charge = &uplinkBudgetCharge{}
		b.charges[u] = charge
	}
	limit := b.allocDataLimit
	if protected {
		limit = b.allocCapacity
	}
	if available := limit - b.allocated; want > available {
		b.addAllocWaiterLocked(u)
		b.access.Unlock()
		return 0
	}
	charge.alloc += want
	b.allocated += want
	b.allocGranted += uint64(want)
	if total := b.allocated + b.allocOverlap; total > b.allocPeak {
		b.allocPeak = total
	}
	b.removeAllocWaiterLocked(u)
	b.access.Unlock()
	return want
}

// noteAllocationOverlapStart records that a replacement is now copying from an old
// array whose charge already moved to the new one, so the physical footprint is
// `Allocated + AllocOverlap`. Both start and end are called under u.access, and the
// end is deferred before the copy so a panic cannot leave the peak accounting stuck.
func (b *sharedUplinkBudget) noteAllocationOverlapStart(charge int) {
	if b == nil || charge <= 0 {
		return
	}
	b.access.Lock()
	b.allocOverlap += charge
	if total := b.allocated + b.allocOverlap; total > b.allocPeak {
		b.allocPeak = total
	}
	b.access.Unlock()
}

// noteAllocationOverlapEnd releases an overlap recorded by noteAllocationOverlapStart.
func (b *sharedUplinkBudget) noteAllocationOverlapEnd(charge int) {
	if b == nil || charge <= 0 {
		return
	}
	b.access.Lock()
	b.allocOverlap -= charge
	if b.allocOverlap < 0 {
		// Defensive: an unbalanced overlap is a bookkeeping defect, not a state.
		b.allocOverlap = 0
	}
	b.access.Unlock()
}

// releaseAllocation returns capacity charged for one released array.
func (b *sharedUplinkBudget) releaseAllocation(u *packetUplink, capacity int) {
	if b == nil || capacity <= 0 {
		return
	}
	b.access.Lock()
	charge, loaded := b.charges[u]
	if !loaded || charge.alloc < capacity {
		// A release with no owner is a defect: count it rather than driving the pool
		// negative, exactly like the logical ledger's excessReleases.
		b.excessReleases += uint64(capacity)
		b.access.Unlock()
		return
	}
	charge.alloc -= capacity
	b.allocated -= capacity
	b.allocReleased += uint64(capacity)
	b.access.Unlock()
	b.wakeWaiters()
}

// allocatedFor returns the capacity u's live arrays currently hold. Safe to call
// while holding u.access.
func (b *sharedUplinkBudget) allocatedFor(u *packetUplink) int {
	if b == nil || u == nil {
		return 0
	}
	b.access.Lock()
	defer b.access.Unlock()
	charge := b.charges[u]
	if charge == nil {
		return 0
	}
	return charge.alloc
}

// addAllocWaiterLocked appends u to the allocation waiters once. Caller holds
// b.access.
func (b *sharedUplinkBudget) addAllocWaiterLocked(u *packetUplink) {
	for _, waiter := range b.allocWaiters {
		if waiter == u {
			return
		}
	}
	b.allocWaiters = append(b.allocWaiters, u)
}

// removeAllocWaiterLocked removes u from the allocation waiters. Caller holds
// b.access.
func (b *sharedUplinkBudget) removeAllocWaiterLocked(u *packetUplink) {
	for index, waiter := range b.allocWaiters {
		if waiter != u {
			continue
		}
		b.allocWaiters = append(b.allocWaiters[:index], b.allocWaiters[index+1:]...)
		return
	}
}

// ensureQueueCapacityLocked makes room for want more bytes in the queue, growing the
// array through the allocation ledger if needed. It reports false when the ledger has
// no capacity right now; the caller then rolls back its logical grant (if any), waits,
// and retries.
//
// Caller must hold u.access. protecting marks a grant that came from the opening pool:
// its reservation may use the whole allocation bound, not only the data share (see
// reserveAllocation). The append that follows must not need a new allocation.
func (u *packetUplink) ensureQueueCapacityLocked(want int, protecting bool) bool {
	if want <= 0 {
		return true
	}
	if spare := cap(u.pending) - len(u.pending); spare >= want {
		return true
	}
	target := packetUpQueueCapacity(cap(u.pending), len(u.pending)+want)
	// A replacement whose old array the queue owns alone only needs the difference:
	// the old charge moves to the new array. Without that, a stream at its own
	// ceiling could wait for capacity that only its own old array is holding — the
	// hold-old-wait-for-new cycle this ledger exists to rule out.
	transfer := 0
	if old := u.pendingAlloc; old != nil && old.refs == 1 && old.charged > 0 && old.capacity < target {
		transfer = old.charged
	}
	reserve := target - transfer
	if reserve < 0 {
		reserve = 0
	}
	if granted := u.budget.reserveAllocation(u, reserve, protecting); granted < reserve {
		return false
	}
	buffer := make([]byte, len(u.pending), target)
	if transfer > 0 {
		// Both arrays exist during the copy: record the overlap for its whole duration,
		// including the copy itself, so AllocPeak is the physical peak rather than a
		// post-copy approximation, and so concurrent replacements add up.
		u.budget.noteAllocationOverlapStart(transfer)
	}
	copy(buffer, u.pending)
	allocation := &packetUpAllocation{capacity: target, charged: target, refs: 1}
	if old := u.pendingAlloc; old != nil {
		if transfer > 0 {
			// The old array's charge moves into the new one instead of being released.
			old.charged = 0
		}
		u.dropAllocationRefLocked(old)
	}
	if transfer > 0 {
		u.budget.noteAllocationOverlapEnd(transfer)
	}
	u.pending = buffer
	u.pendingAlloc = allocation
	return true
}

// dropAllocationRefLocked releases one reference to an allocation and, when it was
// the last, returns the array's capacity to the ledger. Caller holds u.access.
func (u *packetUplink) dropAllocationRefLocked(allocation *packetUpAllocation) {
	if allocation == nil {
		return
	}
	allocation.refs--
	if allocation.refs > 0 {
		return
	}
	if charge := allocation.charged; charge > 0 {
		allocation.charged = 0
		u.budget.releaseAllocation(u, charge)
	}
}

// takeInFlightAllocationLocked binds the POST that is about to carry payload to the
// queue's array. When the POST consumes the rest of the queue it becomes the array's
// only owner (the queue's reference moves to it); otherwise it shares the array and a
// second reference is taken. Caller holds u.access.
func (u *packetUplink) takeInFlightAllocationLocked(remaining int) {
	allocation := u.pendingAlloc
	if allocation == nil {
		return
	}
	if remaining == 0 {
		u.pendingAlloc = nil
		u.inFlightAlloc = allocation
		return
	}
	allocation.refs++
	u.inFlightAlloc = allocation
}

// releaseInFlightAllocationLocked drops the POST's reference after it returned. Caller
// holds u.access.
func (u *packetUplink) releaseInFlightAllocationLocked() {
	allocation := u.inFlightAlloc
	if allocation == nil {
		return
	}
	u.inFlightAlloc = nil
	u.dropAllocationRefLocked(allocation)
}

// dropPendingAllocationLocked drops the queue's reference, used when the queue is
// abandoned (failed, aborted, closed). Caller holds u.access.
func (u *packetUplink) dropPendingAllocationLocked() {
	allocation := u.pendingAlloc
	u.pendingAlloc = nil
	u.dropAllocationRefLocked(allocation)
}
