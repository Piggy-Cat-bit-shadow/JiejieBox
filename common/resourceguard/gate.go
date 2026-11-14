package resourceguard

import (
	"container/list"
	"context"
	"sync"
	"time"

	E "github.com/sagernet/sing/common/exceptions"
)

// ErrQueueFull is returned without waiting when the admission queue is already at its bound.
//
// It is a DISTINCT error from ErrWaitTimeout on purpose: "the queue was full" is a capacity
// decision the guard made immediately, while "the wait expired" is a slow path that ran out of
// time. An operator reading a connection log needs to tell them apart, because only the second
// one means the work was worth waiting for.
var ErrQueueFull = E.New("resource guard: admission queue is full")

// ErrWaitTimeout is returned when a waiter did not receive a slot inside its wait budget.
var ErrWaitTimeout = E.New("resource guard: admission wait timed out")

// plan is the admission shape for one pressure level.
//
// The three numbers move together: a tighter limit without a tighter wait would turn a burst
// into a queue that outlives the burst, and a tighter wait without a tighter queue would hand
// the pressure back to the caller as a storm of retries. See Config.planFor, the only place a
// plan is derived.
type plan struct {
	// limit is the number of permits that may exist at once.
	limit int
	// queue is the number of callers that may WAIT for a permit. Callers beyond it are
	// refused immediately instead of being made to wait for a slot that is not coming.
	queue int
	// wait is how long one waiter may wait for a permit.
	wait time.Duration
}

// Class separates work by how much its latency matters, not by what protocol it is.
//
// It exists because a background burst must not starve an interactive flow of the last few
// slots. It is deliberately NOT a per-protocol knob: protocol code asks for the class its
// traffic belongs to, and the policy that turns classes into limits lives in one place.
type Class uint8

const (
	// ClassNormal is bulk, probe, and background traffic.
	ClassNormal Class = iota
	// ClassInteractive may use the reserve on top of the limit, so the last few slots cannot
	// be consumed entirely by background work.
	ClassInteractive
)

// Gate is a bounded, fair, cancellable admission gate for one kind of expensive work.
//
// # What it bounds, and what it does not
//
// It bounds the number of operations IN FLIGHT at once, and the number of callers WAITING for
// one. It does not rate-limit steady state, it does not own a timer or a goroutine, and it does
// not decide how the work is done. That is the split this package is built on: the gate says
// "may this expensive creation start now", and the caller keeps its protocol semantics.
//
// # Why waiters are a queue and not a semaphore
//
// A plain channel semaphore cannot be resized, cannot report how many callers are waiting, and
// gives no place to refuse the caller BEYOND a bound - so a burst of 1000 creates 1000 blocked
// goroutines holding 1000 stacks instead of being refused in constant time. The queue here is
// explicit: bounded in length, fair (FIFO), and each waiter carries its own deadline and
// cancellation.
//
// # Why there is no goroutine per waiter
//
// A waiter parks on its OWN channel and is handed the slot by the releaser. Nothing is spawned,
// so a burst costs one small struct per waiting caller rather than one goroutine per waiting
// caller - which matters here, because the pressure this gate exists for is dominated by
// goroutine stacks.
//
// # Fairness and starvation
//
// The queue is FIFO and a released slot always goes to the OLDEST pending waiter, so a flow that
// arrived first cannot be pushed back by later arrivals. ClassInteractive may exceed plan.limit
// by the reserve, and those reserve slots are admitted even when the queue is busy; the reserve
// is what keeps an interactive flow from waiting behind a background flood.
type Gate struct {
	access sync.Mutex

	plan plan
	// reserve is the extra headroom ClassInteractive may use beyond plan.limit.
	reserve int

	inflight int
	// waiters holds pending *waiter values; a waiter that gave up is marked and skipped when
	// the list reaches it, so no removal is needed under contention.
	waiters *list.List
	pending int
	// aging is how long a waiter may be passed over for its class before it is allowed to use
	// the reserve. Zero means "never", which is only correct for tests that drive the gate by
	// hand; every production guard sets it (see Config.StarvationAfter).
	aging time.Duration
	// now is the clock the aging rule reads. It is a field, not a direct time.Now call, so a
	// test can prove the guarantee over a simulated minute instead of sleeping for one.
	now func() time.Time

	maxInflight int
	// maxWaiters is the highest number of callers that ever waited at once. It is the queue-side
	// counterpart of maxInflight: sampling Waiters can miss the peak between two reads, and the audit
	// had to estimate it. It is updated under access where pending changes.
	maxWaiters int

	// Counters are cumulative since construction. They are read by Stats and are the evidence
	// that a state change actually changed behaviour rather than only changing a field.
	requested uint64
	admitted  uint64
	queued    uint64
	refused   uint64
	timedOut  uint64
	canceled  uint64
	released  uint64
}

type waiterState uint8

const (
	waiterPending waiterState = iota
	waiterGranted
	waiterAbandoned
)

type waiter struct {
	channel chan struct{}
	class   Class
	state   waiterState
	element *list.Element
	// enqueued is when the waiter joined the queue. It is the input to the aging rule, which is
	// the only thing that stops a busy priority class from passing the other one over forever.
	enqueued time.Time
}

// NewGate builds a gate at the given plan. Limits are clamped to a usable shape rather than
// accepted as-is: a zero limit would make every acquisition wait forever, which is a
// configuration error, not a policy.
// NewBoundedGate is NewGate for a caller that owns one kind of expensive work and has only
// numbers, not a Config.
//
// It exists so a build path (a provider fetch, a remote rule-set compile) can share this
// package's queue, fair hand-off, bounded wait and per-waiter cancellation instead of growing
// a second semaphore with its own bugs. The resourceguard policy derived from Config is
// untouched: this gate is not the dial admission gate and is never resized by pressure.
func NewBoundedGate(limit int, queue int, wait time.Duration) *Gate {
	return NewGate(plan{limit: limit, queue: queue, wait: wait}, 0)
}

func NewGate(initial plan, reserve int) *Gate {
	if initial.limit < 1 {
		initial.limit = 1
	}
	if initial.queue < 0 {
		initial.queue = 0
	}
	if initial.wait <= 0 {
		initial.wait = time.Second
	}
	if reserve < 0 {
		reserve = 0
	}
	return &Gate{
		plan:    initial,
		reserve: reserve,
		waiters: list.New(),
		now:     time.Now,
	}
}

// limitFor is the admission ceiling for a class at the CURRENT plan.
func (g *Gate) limitFor(class Class) int {
	if class == ClassInteractive {
		return g.plan.limit + g.reserve
	}
	return g.plan.limit
}

// setPlan replaces the admission shape, normally because the pressure level changed.
//
// A shrink takes effect for NEW acquisitions only: work already in flight keeps its permit and
// waiters already queued keep waiting, because revoking either would break the ownership rule
// the permit represents. A queue longer than the new bound drains at its own pace and accepts
// no new arrivals meanwhile.
func (g *Gate) setPlan(next plan) {
	if next.limit < 1 {
		next.limit = 1
	}
	if next.queue < 0 {
		next.queue = 0
	}
	if next.wait <= 0 {
		next.wait = time.Second
	}
	g.access.Lock()
	g.plan = next
	g.access.Unlock()
}

// Acquire takes one permit, waiting inside the current plan if the limit is reached.
//
// It returns a non-nil permit and a nil error when admitted, or a nil permit and one of
// ErrQueueFull / ErrWaitTimeout / the context's error. The caller must Release the permit.
func (g *Gate) Acquire(ctx context.Context, class Class) (*Permit, error) {
	started := time.Now()

	// A caller whose context is already dead must not take a slot: admitting it would spend the
	// permit on a dial that cannot succeed, and under pressure the whole point of the gate is
	// that only useful work holds one. Cancellation is checked before the limit, not after, so a
	// dead caller can never be the reason a live one waits.
	if err := ctx.Err(); err != nil {
		g.access.Lock()
		g.requested++
		g.canceled++
		g.access.Unlock()
		return nil, err
	}

	g.access.Lock()
	g.requested++
	if g.inflight < g.limitFor(class) {
		g.inflight++
		g.admitted++
		if g.inflight > g.maxInflight {
			g.maxInflight = g.inflight
		}
		g.access.Unlock()
		return &Permit{gate: g, acquired: started, granted: started}, nil
	}
	if g.pending >= g.plan.queue {
		g.refused++
		g.access.Unlock()
		return nil, ErrQueueFull
	}
	created := &waiter{channel: make(chan struct{}, 1), class: class, enqueued: g.now()}
	created.element = g.waiters.PushBack(created)
	g.pending++
	if g.pending > g.maxWaiters {
		g.maxWaiters = g.pending
	}
	g.queued++
	waitBudget := g.plan.wait
	g.access.Unlock()

	timer := time.NewTimer(waitBudget)
	defer timer.Stop()

	select {
	case <-created.channel:
		return &Permit{gate: g, acquired: started, granted: time.Now()}, nil
	case <-ctx.Done():
		return nil, g.withdraw(created, ctx.Err(), false)
	case <-timer.C:
		return nil, g.withdraw(created, ErrWaitTimeout, true)
	}
}

// withdraw removes a waiter that stopped waiting and reports the outcome.
//
// The race this function exists for: the releaser may have already granted the slot between the
// waiter's timer firing and this lock being taken. The slot is a real resource, so giving up
// after being granted must HAND THE SLOT ON rather than leak it; otherwise a burst of timeouts
// permanently shrinks the gate.
func (g *Gate) withdraw(created *waiter, err error, timedOut bool) error {
	g.access.Lock()
	switch created.state {
	case waiterGranted:
		// The slot was granted but the waiter had already given up, so this caller was never
		// admitted. Both counters are undone rather than papered over: counting the grant as an
		// admission AND the withdrawal as a cancellation would put one caller in two outcome
		// buckets, and an operator reading "admitted 168" would be reading grants instead of
		// callers. released is deliberately NOT incremented - nothing was used and returned.
		g.inflight--
		g.admitted--
		if timedOut {
			g.timedOut++
		} else {
			g.canceled++
		}
		g.grantLocked()
		g.access.Unlock()
		// The slot has been passed on; the caller still missed it.
		return err
	case waiterPending:
		created.state = waiterAbandoned
		g.pending--
		if timedOut {
			g.timedOut++
		} else {
			g.canceled++
		}
		g.compactLocked()
		g.access.Unlock()
		return err
	default:
		// Already abandoned; nothing left to undo.
		g.access.Unlock()
		return err
	}
}

// release returns one permit.
func (g *Gate) release() {
	g.access.Lock()
	g.released++
	if g.inflight > 0 {
		g.inflight--
	}
	g.grantLocked()
	g.access.Unlock()
}

// grantLocked hands one free slot to the oldest pending waiter that can still use it.
//
// It must be called with the lock held and after inflight has been decremented. Skipping
// abandoned waiters here is what lets a waiter give up without paying for a list removal under
// contention.
//
// # Why the waiter's class is checked here
//
// Without the check, a NORMAL waiter would be granted a slot that an interactive caller is
// supposed to keep: after a release leaves inflight at plan.limit, a normal waiter does not fit
// (`inflight >= limitFor(normal)`), but granting it anyway would move inflight to limit+1 and
// hand the reserve to background work. The reserve would then be consumed by exactly the flood
// it exists to protect against. So a slot that frees at the limit is left EMPTY until an
// interactive caller takes it or the limit rises - which is the reserve working, not a stall.
//
// # Why an aged waiter is allowed to break that rule
//
// The class check alone makes the reserve a permanent lockout: as long as the interactive class
// keeps taking the reserve slot, a queued background caller never sees a moment when it fits.
// A waiter that has been passed over for `aging` may therefore use the reserve, up to the
// absolute cap of limit+reserve. That bounds the starvation instead of eliminating the priority:
// the interactive class still goes first, it just cannot go first forever.
func (g *Gate) grantLocked() {
	for element := g.waiters.Front(); element != nil; element = element.Next() {
		created := element.Value.(*waiter)
		if created.state != waiterPending {
			continue
		}
		if g.inflight >= g.limitFor(created.class) {
			if !g.agedFor(created) {
				continue
			}
			if g.inflight >= g.plan.limit+g.reserve {
				continue
			}
		}
		g.waiters.Remove(element)
		created.element = nil
		g.pending--
		created.state = waiterGranted
		g.inflight++
		g.admitted++
		if g.inflight > g.maxInflight {
			g.maxInflight = g.inflight
		}
		created.channel <- struct{}{}
		return
	}
}

// agedFor reports whether a waiter has been passed over long enough to claim the reserve.
//
// It reads the clock through time.Since on the waiter's own enqueue stamp, so the rule costs one
// timestamp read per candidate rather than a timer per waiter - the queue holds hundreds of them
// and must stay allocation- and goroutine-free.
func (g *Gate) agedFor(created *waiter) bool {
	return g.aging > 0 && g.now().Sub(created.enqueued) >= g.aging
}

// compactLocked drops abandoned waiters once they outnumber the live ones.
//
// Abandoned entries are normally skipped when the list reaches them, so this is only a memory
// bound: a cancel storm would otherwise leave one dead entry per cancelled waiter until the
// queue drained.
func (g *Gate) compactLocked() {
	if g.waiters.Len() <= 2*g.pending {
		return
	}
	compacted := make([]*waiter, 0, g.pending)
	for element := g.waiters.Front(); element != nil; element = element.Next() {
		created := element.Value.(*waiter)
		if created.state == waiterPending {
			compacted = append(compacted, created)
		}
	}
	g.waiters.Init()
	for _, created := range compacted {
		created.element = g.waiters.PushBack(created)
	}
}

// Permit is one admission slot. Release is idempotent and nil-safe, so a caller that holds a
// nil permit (no guard configured) does not need a branch.
type Permit struct {
	gate *Gate
	// acquired is when the caller STARTED asking; granted is when it owned the slot. The two are
	// separate fields rather than one timestamp because Wait reports the interval between them: a
	// permit that is used for real work must not report that work as queueing latency. An immediate
	// admission has granted == acquired, which is a wait of zero.
	acquired time.Time
	granted  time.Time
	once     sync.Once
}

// Release returns the slot. Calling it twice is a no-op.
func (p *Permit) Release() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		p.gate.release()
	})
}

// Wait returns how long the permit waited for admission, for callers that record latency.
//
// It is the interval from the start of Acquire to the moment the slot was granted - not
// time.Since(acquired), which would include however long the caller has been holding the permit and
// would report a handshake's duration as gate queueing.
func (p *Permit) Wait() time.Duration {
	if p == nil {
		return 0
	}
	return p.granted.Sub(p.acquired)
}

// Stats is a point-in-time view of one gate.
type Stats struct {
	Limit       int
	QueueLimit  int
	WaitBudget  time.Duration
	Inflight    int
	Waiters     int
	MaxInflight int
	MaxWaiters  int

	Requested uint64
	Admitted  uint64
	Queued    uint64
	Refused   uint64
	TimedOut  uint64
	Canceled  uint64
	Released  uint64
}

// Stats reads the gate's counters under its lock, so the numbers are one consistent snapshot
// rather than a set of individually current reads.
func (g *Gate) Stats() Stats {
	g.access.Lock()
	defer g.access.Unlock()
	return Stats{
		Limit:       g.plan.limit,
		QueueLimit:  g.plan.queue,
		WaitBudget:  g.plan.wait,
		Inflight:    g.inflight,
		Waiters:     g.pending,
		MaxInflight: g.maxInflight,
		MaxWaiters:  g.maxWaiters,
		Requested:   g.requested,
		Admitted:    g.admitted,
		Queued:      g.queued,
		Refused:     g.refused,
		TimedOut:    g.timedOut,
		Canceled:    g.canceled,
		Released:    g.released,
	}
}
