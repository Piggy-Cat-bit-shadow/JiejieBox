package urltest

import (
	"context"
	"sync"

	"github.com/sagernet/sing/service"
)

// priorityAgingEvery bounds how many interactive measurements may jump the queue before the oldest
// background waiter is served. It is deliberately small: a phone's periodic health checks must finish,
// so interactive traffic cannot monopolise the coordinator indefinitely, while a user pressing "test"
// should still not wait behind a whole background round.
const priorityAgingEvery = 3

// maxConcurrentRounds is the Box-wide bound on measurement ROUNDS that may be in progress at once.
//
// # Why a round budget is needed at all
//
// The measurement budget bounds the probes that are running, but it does not bound what PRODUCES
// them. Each round starts a fixed worker set and a fixed submitted-but-not-started queue (see
// protocol/group/probeBatch); with no round budget, every group, provider republish and manual entry
// that fires at the same moment creates its own workers and queue. MEASURED on the pre-budget tree,
// against a coordinator with limit 10: 4 rounds produced 40 coordinator waiters and 44 extra
// goroutines, 32 rounds produced 320 and 352. That is growth with the number of concurrent rounds,
// which is exactly what the audit's "全 Box 测速任务生产/等待队列没有独立硬上界" names.
//
// A hard limit on the MEASUREMENT QUEUE cannot fix it: a 10,000-node round legitimately wants every
// node measured, and refusing a waiter there silently drops members. MEASURED in round 08: a queue
// cap of max(limit*4, 8) made TestTheBoxBudgetHoldsAtProviderScale answer 9,976 of 10,000 nodes.
// The bound therefore belongs one level up: how many rounds may be PRODUCING at once. With the round
// budget, the created resources are
//
//	2 rounds × 10 workers = 20 worker goroutines
//	2 rounds × 20 queued tasks = 40 submitted tasks
//	≤ 20 coordinator waiters
//
// all constants, while every submitted member is still measured (a round waits, it never drops).
//
// # Why two and not one
//
// The measurement share merges two concurrent callers asking about the same node, and the pair it
// exists for is a background round and an interactive one. Allowing exactly one round would make
// that pair impossible - the second round would wait for the first to finish and the share would
// never have two participants, which several tests assert directly. A third round adds queue depth
// but no new admissible work, because two rounds already fill or exceed the measurement budget.
const maxConcurrentRounds = 2

// Coordinator bounds how many measurements one Box may have in flight, and orders the waiters.
//
// # Why a Box-wide bound is needed
//
// Each URLTest batch already limits itself to ten concurrent probes, but that limit is per batch.
// A configuration with several URLTest groups - or a group plus Clash delay probes plus a manual
// refresh - can therefore run several times ten measurements at once. On a phone running inside a
// NetworkExtension with roughly 50 MiB of budget, each measurement holding a socket, a TLS session
// and buffers, that is the difference between a check that fits and one that is killed.
//
// # Why it is per Box and not a package global
//
// A process can hold more than one Box at a time: the running one, a temporary Box built to validate
// a new configuration, and test instances. A global limiter would make those compete, so validating
// a configuration could stall the live one, and a leaked temporary Box would permanently consume
// slots. The limit belongs to whichever Box is doing the measuring, so it is carried on that Box's
// context.
//
// # Two budgets, one rule
//
// The coordinator owns two pools of the same shape: the MEASUREMENT budget (how many probes may be
// in flight, from the configured limit) and the ROUND budget (how many rounds may be producing, see
// maxConcurrentRounds). Both use the same queue and the same ordering rule, so a caller-triggered
// round is admitted ahead of queued background rounds for exactly the reason a foreground probe is
// admitted ahead of queued background probes.
//
// # Ordering: interactive first, background aged in
//
// A waiter carries the probe origin it already declares (see adapter.IsBackgroundProbe): a
// foreground measurement - a user test, a native command, a diagnostic - is granted ahead of queued
// background work when a slot frees. Background is not starved: after priorityAgingEvery consecutive
// foreground grants, the oldest background waiter is served next. The rule is deterministic and
// needs no timers, so it is testable and costs one integer.
//
// The queue itself is not a second copy of the candidate list: a waiter is one small struct with a
// channel, created only when the caller must wait, and removed when its own context ends.
type Coordinator struct {
	measurement slotPool
	rounds      slotPool
}

// slotPool is one bounded pool of slots, with the interactive-first ordering above.
//
// The zero value allows nothing, which is how a coordinator created for a non-positive limit
// disables coordination instead of blocking every caller.
type slotPool struct {
	limit int

	access         sync.Mutex
	inFlight       int
	queue          []*slotWaiter
	priorityStreak int
}

// slotWaiter is one queued acquisition. The channel is closed exactly once, under access, when the
// slot is granted; a canceled waiter is removed from the queue under the same lock.
type slotWaiter struct {
	priority bool
	ready    chan struct{}
	granted  bool
}

// NewCoordinator returns a coordinator allowing limit concurrent measurements, and
// maxConcurrentRounds rounds in progress.
//
// A non-positive limit disables coordination - both budgets - rather than blocking every
// measurement, because a limit of zero is more likely to be a misconfiguration than a request to
// measure nothing.
func NewCoordinator(limit int) *Coordinator {
	if limit <= 0 {
		return &Coordinator{}
	}
	return &Coordinator{
		measurement: slotPool{limit: limit},
		rounds:      slotPool{limit: maxConcurrentRounds},
	}
}

// Acquire reserves one measurement slot for background work, or fails if ctx is done first.
func (c *Coordinator) Acquire(ctx context.Context) (func(), error) {
	if c == nil {
		return func() {}, nil
	}
	return c.measurement.acquire(ctx, false)
}

// AcquirePriority reserves one measurement slot for interactive work. It is identical to Acquire
// except for its queue position; the slot bound is the same.
func (c *Coordinator) AcquirePriority(ctx context.Context) (func(), error) {
	if c == nil {
		return func() {}, nil
	}
	return c.measurement.acquire(ctx, true)
}

// AcquireRound reserves one round slot for background work, or fails if ctx is done first.
//
// A round takes this BEFORE it creates its workers or its submitted-task queue, so the number of
// rounds producing at once - and therefore the number of goroutines and queue entries they create -
// is bounded by the Box rather than by how many groups, providers and manual entries fire together.
// A round that cannot take a slot WAITS; it never drops members, because "not measured" is not a
// fact about a node.
func (c *Coordinator) AcquireRound(ctx context.Context) (func(), error) {
	if c == nil {
		return func() {}, nil
	}
	return c.rounds.acquire(ctx, false)
}

// AcquireRoundPriority reserves one round slot for interactive work. It is identical to AcquireRound
// except for its queue position: a round a person is waiting on is served before queued background
// rounds when a slot frees.
func (c *Coordinator) AcquireRoundPriority(ctx context.Context) (func(), error) {
	if c == nil {
		return func() {}, nil
	}
	return c.rounds.acquire(ctx, true)
}

// acquire waits for one slot, or for ctx to end. The ordering rule is the pool's, so the measurement
// budget and the round budget cannot drift apart.
func (p *slotPool) acquire(ctx context.Context, priority bool) (func(), error) {
	if p == nil || p.limit <= 0 {
		return func() {}, nil
	}

	p.access.Lock()
	if p.inFlight < p.limit && len(p.queue) == 0 {
		p.inFlight++
		p.access.Unlock()
		return p.releaseFunc(), nil
	}
	waiter := &slotWaiter{priority: priority, ready: make(chan struct{})}
	p.queue = append(p.queue, waiter)
	p.access.Unlock()

	select {
	case <-waiter.ready:
		return p.releaseFunc(), nil
	case <-ctx.Done():
		p.access.Lock()
		if waiter.granted {
			// The slot was granted at the same moment the context ended. The caller will not use
			// it, so the slot must go back to the queue instead of leaking for the Box's lifetime.
			p.access.Unlock()
			p.release()
			return nil, ctx.Err()
		}
		p.removeWaiterLocked(waiter)
		p.access.Unlock()
		return nil, ctx.Err()
	}
}

// releaseFunc returns an idempotent slot release for one acquisition.
//
// The closure is called by the goroutine that acquired the slot (Measure defers it immediately).
// The once guard keeps a double call from freeing the slot twice, which would hand it to a second
// measurement while the first still believed it owned it.
func (p *slotPool) releaseFunc() func() {
	var once sync.Once
	return func() {
		once.Do(p.release)
	}
}

func (p *slotPool) release() {
	p.access.Lock()
	if p.inFlight > 0 {
		p.inFlight--
	}
	p.dispatchLocked()
	p.access.Unlock()
}

// dispatchLocked grants free slots to queued waiters in priority order with aging. The caller holds
// access.
func (p *slotPool) dispatchLocked() {
	for p.inFlight < p.limit && len(p.queue) > 0 {
		index := p.nextWaiterIndexLocked()
		waiter := p.queue[index]
		p.queue = append(p.queue[:index], p.queue[index+1:]...)
		if waiter.priority {
			p.priorityStreak++
		} else {
			p.priorityStreak = 0
		}
		waiter.granted = true
		p.inFlight++
		close(waiter.ready)
	}
}

// nextWaiterIndexLocked picks the first interactive waiter, except that a continuous interactive
// burst is cut after priorityAgingEvery grants so the oldest background waiter runs. The caller
// holds access.
func (p *slotPool) nextWaiterIndexLocked() int {
	if p.priorityStreak >= priorityAgingEvery {
		for index, waiter := range p.queue {
			if !waiter.priority {
				return index
			}
		}
	}
	for index, waiter := range p.queue {
		if waiter.priority {
			return index
		}
	}
	return 0
}

func (p *slotPool) removeWaiterLocked(waiter *slotWaiter) {
	for index, queued := range p.queue {
		if queued == waiter {
			p.queue = append(p.queue[:index], p.queue[index+1:]...)
			return
		}
	}
}

// InFlight reports how many measurement slots are currently held. It exists for tests and
// diagnostics.
func (c *Coordinator) InFlight() int {
	if c == nil {
		return 0
	}
	c.measurement.access.Lock()
	defer c.measurement.access.Unlock()
	return c.measurement.inFlight
}

// Queued reports how many measurements are waiting for a slot. It exists for tests and diagnostics;
// no policy reads it.
func (c *Coordinator) Queued() int {
	if c == nil {
		return 0
	}
	c.measurement.access.Lock()
	defer c.measurement.access.Unlock()
	return len(c.measurement.queue)
}

// Limit reports the configured measurement bound, or zero when coordination is disabled.
func (c *Coordinator) Limit() int {
	if c == nil {
		return 0
	}
	return c.measurement.limit
}

// RoundsInFlight reports how many rounds hold a round slot. It exists for tests and diagnostics.
func (c *Coordinator) RoundsInFlight() int {
	if c == nil {
		return 0
	}
	c.rounds.access.Lock()
	defer c.rounds.access.Unlock()
	return c.rounds.inFlight
}

// RoundsQueued reports how many rounds are waiting for a round slot. It exists for tests and
// diagnostics.
func (c *Coordinator) RoundsQueued() int {
	if c == nil {
		return 0
	}
	c.rounds.access.Lock()
	defer c.rounds.access.Unlock()
	return len(c.rounds.queue)
}

// RoundLimit reports the configured round bound, or zero when coordination is disabled.
func (c *Coordinator) RoundLimit() int {
	if c == nil {
		return 0
	}
	return c.rounds.limit
}

// ContextWithCoordinator attaches a coordinator to a Box context.
//
// It is stored by pointer under its own type, so it needs no lifecycle and takes no part in service
// startup. It owns no goroutines and has nothing to close: its whole state is the in-flight count and
// the waiters' own channels.
func ContextWithCoordinator(ctx context.Context, coordinator *Coordinator) context.Context {
	return service.ContextWithPtr(ctx, coordinator)
}

// CoordinatorFromContext returns the Box's measurement coordinator, or nil when there is none.
//
// A nil result means "unbounded", which is the correct behaviour for an isolated unit test or a
// library caller that never built a Box. It deliberately does NOT fall back to a package-level
// limiter: that would reintroduce the cross-Box coupling the per-Box design exists to avoid.
func CoordinatorFromContext(ctx context.Context) *Coordinator {
	return service.PtrFromContext[Coordinator](ctx)
}
