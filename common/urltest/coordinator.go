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
	limit int

	access         sync.Mutex
	inFlight       int
	queue          []*measurementWaiter
	priorityStreak int
}

// measurementWaiter is one queued acquisition. The channel is closed exactly once, under access,
// when the slot is granted; a canceled waiter is removed from the queue under the same lock.
type measurementWaiter struct {
	priority bool
	ready    chan struct{}
	granted  bool
}

// NewCoordinator returns a coordinator allowing limit concurrent measurements.
//
// A non-positive limit disables coordination rather than blocking every measurement, because a
// limit of zero is more likely to be a misconfiguration than a request to measure nothing.
func NewCoordinator(limit int) *Coordinator {
	if limit <= 0 {
		return &Coordinator{}
	}
	return &Coordinator{limit: limit}
}

// Acquire reserves one measurement slot for background work, or fails if ctx is done first.
func (c *Coordinator) Acquire(ctx context.Context) (func(), error) {
	return c.acquire(ctx, false)
}

// AcquirePriority reserves one measurement slot for interactive work. It is identical to Acquire
// except for its queue position; the slot bound is the same.
func (c *Coordinator) AcquirePriority(ctx context.Context) (func(), error) {
	return c.acquire(ctx, true)
}

func (c *Coordinator) acquire(ctx context.Context, priority bool) (func(), error) {
	if c == nil || c.limit <= 0 {
		return func() {}, nil
	}

	c.access.Lock()
	if c.inFlight < c.limit && len(c.queue) == 0 {
		c.inFlight++
		c.access.Unlock()
		return c.releaseFunc(), nil
	}
	waiter := &measurementWaiter{priority: priority, ready: make(chan struct{})}
	c.queue = append(c.queue, waiter)
	c.access.Unlock()

	select {
	case <-waiter.ready:
		return c.releaseFunc(), nil
	case <-ctx.Done():
		c.access.Lock()
		if waiter.granted {
			// The slot was granted at the same moment the context ended. The caller will not use
			// it, so the slot must go back to the queue instead of leaking for the Box's lifetime.
			c.access.Unlock()
			c.release()
			return nil, ctx.Err()
		}
		c.removeWaiterLocked(waiter)
		c.access.Unlock()
		return nil, ctx.Err()
	}
}

// releaseFunc returns an idempotent slot release for one acquisition.
//
// The closure is called by the goroutine that acquired the slot (Measure defers it immediately).
// The once guard keeps a double call from freeing the slot twice, which would hand it to a second
// measurement while the first still believed it owned it.
func (c *Coordinator) releaseFunc() func() {
	var once sync.Once
	return func() {
		once.Do(c.release)
	}
}

func (c *Coordinator) release() {
	c.access.Lock()
	if c.inFlight > 0 {
		c.inFlight--
	}
	c.dispatchLocked()
	c.access.Unlock()
}

// dispatchLocked grants free slots to queued waiters in priority order with aging. The caller holds
// access.
func (c *Coordinator) dispatchLocked() {
	for c.inFlight < c.limit && len(c.queue) > 0 {
		index := c.nextWaiterIndexLocked()
		waiter := c.queue[index]
		c.queue = append(c.queue[:index], c.queue[index+1:]...)
		if waiter.priority {
			c.priorityStreak++
		} else {
			c.priorityStreak = 0
		}
		waiter.granted = true
		c.inFlight++
		close(waiter.ready)
	}
}

// nextWaiterIndexLocked picks the first interactive waiter, except that a continuous interactive
// burst is cut after priorityAgingEvery grants so the oldest background waiter runs. The caller
// holds access.
func (c *Coordinator) nextWaiterIndexLocked() int {
	if c.priorityStreak >= priorityAgingEvery {
		for index, waiter := range c.queue {
			if !waiter.priority {
				return index
			}
		}
	}
	for index, waiter := range c.queue {
		if waiter.priority {
			return index
		}
	}
	return 0
}

func (c *Coordinator) removeWaiterLocked(waiter *measurementWaiter) {
	for index, queued := range c.queue {
		if queued == waiter {
			c.queue = append(c.queue[:index], c.queue[index+1:]...)
			return
		}
	}
}

// InFlight reports how many slots are currently held. It exists for tests and diagnostics.
func (c *Coordinator) InFlight() int {
	if c == nil {
		return 0
	}
	c.access.Lock()
	defer c.access.Unlock()
	return c.inFlight
}

// Queued reports how many acquisitions are waiting for a slot. It exists for tests and diagnostics;
// no policy reads it.
func (c *Coordinator) Queued() int {
	if c == nil {
		return 0
	}
	c.access.Lock()
	defer c.access.Unlock()
	return len(c.queue)
}

// Limit reports the configured bound, or zero when coordination is disabled.
func (c *Coordinator) Limit() int {
	if c == nil {
		return 0
	}
	return c.limit
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
