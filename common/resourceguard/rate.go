package resourceguard

import (
	"context"
	"math/rand"
	"time"

	E "github.com/sagernet/sing/common/exceptions"
)

// ErrRateLimited is returned when an admitted creation is withheld because the process has been
// failing expensive creations faster than the rate policy allows. It is a LOCAL rejection: the
// network said nothing, this process chose not to start more work yet.
var ErrRateLimited = E.New("resource guard: creation rate limited after fast failures")

// Fast-fail rate policy.
//
// # Why concurrency alone is not enough
//
// The gate bounds how many creations run AT ONCE. A failure that returns in microseconds releases
// its permit just as fast, so a client retrying in a loop can run thousands of doomed creations per
// second through a gate that never exceeds its limit. The measured shape is an authentication
// failure or a refused port on a node the user has selected: every attempt fails immediately, and
// the burst is visible in CPU and in goroutine churn rather than in resident memory.
//
// # Why it must not become a sleep in front of every dial
//
// A rate limit that applies to all work is indistinguishable from added latency: after one bad node
// every healthy flow would pause too. The policy therefore only engages after a STREAK of fast
// failures, backs off exponentially with jitter, caps the delay, and exempts the interactive class
// entirely - a person waiting on a page load must not be slowed by a background node's failure
// storm. Every wait is cancellable and bounded by the caller's context.
//
// # Relationship to the gate's limits
//
// This is a second, independent dimension: the gate answers "may this start now", the rate policy
// answers "is starting another one useful at all". Neither substitutes for the other, and the
// counters are kept separate (Gate.Stats vs GuardStats rate fields) because a status report must be
// able to tell a capacity refusal from a rate refusal.
type rateConfig struct {
	// FastFailWindow is how quickly a failure must return to count as "fast". A timeout after a
	// real attempt is evidence that work happened; a refusal in a microsecond is not.
	FastFailWindow time.Duration
	// RateLimitAfter is the number of consecutive fast failures that arm the first window.
	RateLimitAfter int
	// RateLimitBase is the first delay applied to new non-interactive creations.
	RateLimitBase time.Duration
	// RateLimitMax caps the delay, so a long outage cannot turn the client into a stalled queue.
	RateLimitMax time.Duration
	// RateLimitJitter is the fraction of the delay randomised per window (0..1), so a fleet
	// retrying in lockstep does not synchronise again after every backoff.
	RateLimitJitter float64
}

func defaultRateConfig() rateConfig {
	return rateConfig{
		FastFailWindow:  250 * time.Millisecond,
		RateLimitAfter:  4,
		RateLimitBase:   100 * time.Millisecond,
		RateLimitMax:    2 * time.Second,
		RateLimitJitter: 0.2,
	}
}

func (c rateConfig) normalized() rateConfig {
	if c.FastFailWindow <= 0 {
		c.FastFailWindow = 250 * time.Millisecond
	}
	if c.RateLimitAfter < 1 {
		c.RateLimitAfter = 1
	}
	if c.RateLimitBase <= 0 {
		c.RateLimitBase = 100 * time.Millisecond
	}
	if c.RateLimitMax < c.RateLimitBase {
		c.RateLimitMax = c.RateLimitBase
	}
	if c.RateLimitJitter < 0 {
		c.RateLimitJitter = 0
	}
	if c.RateLimitJitter > 1 {
		c.RateLimitJitter = 1
	}
	return c
}

// rateState is the guard's fast-fail state. It is protected by Guard.access, like the level.
type rateState struct {
	streak    int
	delay     time.Duration
	until     time.Time
	windows   uint64
	delayed   uint64
	refused   uint64
	failures  uint64
	recovered uint64
}

// rateGate applies the current rate window to one non-interactive creation.
//
// The decision has three outcomes, all visible in the counters: no window (return immediately), a
// window that fits in the caller's remaining budget (wait, cancellably), and a window longer than
// that budget (refuse with ErrRateLimited rather than make the caller wait for a deadline it will
// miss). The last one is deliberately an error instead of a silent wait: the caller then sees a
// local rejection immediately and can surface it, instead of discovering a timeout later.
//
// The no-window case is the hot path and reads the atomic mirror only; the mutex is taken only while
// a window is actually armed.
func (g *Guard) rateGate(ctx context.Context) error {
	armedUntil := g.rateUntil.Load()
	if armedUntil == 0 {
		return nil
	}
	now := g.clockNow()
	if !now.Before(time.Unix(0, armedUntil)) {
		return nil
	}
	g.access.Lock()
	if g.rate.until.IsZero() || !now.Before(g.rate.until) {
		g.access.Unlock()
		return nil
	}
	delay := g.rate.until.Sub(now)
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline && now.Add(delay).After(deadline) {
		g.rate.refused++
		g.access.Unlock()
		return ErrRateLimited
	}
	g.rate.delayed++
	g.access.Unlock()
	return g.pause(ctx, delay)
}

// observeOutcome records how one admitted creation ended.
//
// It is called by the seam that held the permit, with the error the creation returned. Local
// rejections are excluded: a queue refusal says nothing about whether the far side is healthy, and
// counting it would let this process's own backpressure arm a rate limit against a working node.
func (g *Guard) observeOutcome(started time.Time, err error) {
	if g == nil {
		return
	}
	elapsed := g.clockNow().Sub(started)
	g.access.Lock()
	defer g.access.Unlock()
	if err == nil {
		if g.rate.streak > 0 || !g.rate.until.IsZero() {
			g.rate.recovered++
		}
		g.rate.streak = 0
		g.rate.until = time.Time{}
		g.rate.delay = 0
		// A success means the far side is reachable again, so a later storm starts its backoff
		// from the base delay instead of resuming at whatever the previous outage reached.
		g.rate.windows = 0
		g.rateUntil.Store(0)
		return
	}
	if IsLocalRejection(err) {
		return
	}
	if elapsed > g.rateConfig.FastFailWindow {
		g.rate.streak = 0
		return
	}
	g.rate.failures++
	g.rate.streak++
	if g.rate.streak < g.rateConfig.RateLimitAfter {
		return
	}
	g.rate.streak = 0
	g.rate.delay = g.nextRateDelayLocked()
	g.rate.until = g.clockNow().Add(g.rate.delay)
	g.rateUntil.Store(g.rate.until.UnixNano())
	g.rate.windows++
}

// nextRateDelayLocked grows the delay exponentially and applies the configured jitter.
func (g *Guard) nextRateDelayLocked() time.Duration {
	base := g.rateConfig.RateLimitBase
	delay := base
	for index := uint64(0); index < g.rate.windows && delay < g.rateConfig.RateLimitMax; index++ {
		delay *= 2
	}
	if delay > g.rateConfig.RateLimitMax {
		delay = g.rateConfig.RateLimitMax
	}
	if jitter := g.rateConfig.RateLimitJitter; jitter > 0 {
		span := time.Duration(float64(delay) * jitter)
		if span > 0 {
			// Intentionally not crypto/rand: this scatters retries, it does not protect a secret.
			delay += time.Duration(rand.Int63n(int64(span)*2+1)) - span
		}
	}
	if delay < base {
		delay = base
	}
	if delay > g.rateConfig.RateLimitMax {
		delay = g.rateConfig.RateLimitMax
	}
	return delay
}

// pause waits for the rate window to pass or for the caller to give up.
//
// The default wait is a timer; tests install waitContext themselves. The function is a field so the
// rate tests can prove the delay is applied without sleeping for it, and so a future caller with its
// own scheduler can integrate without a second policy.
func (g *Guard) pause(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	if g.wait != nil {
		return g.wait(ctx, delay)
	}
	return waitContext(ctx, delay)
}

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// IsLocalRejection reports whether an admission error is this process's own decision rather than a
// far-side failure. Callers use it to keep local backpressure out of node health.
func IsLocalRejection(err error) bool {
	if err == nil {
		return false
	}
	if E.IsMulti(err, ErrQueueFull, ErrWaitTimeout, ErrRateLimited) {
		return true
	}
	return E.IsMulti(err, context.Canceled, context.DeadlineExceeded)
}

// RateStats is the observability surface of the fast-fail policy.
type RateStats struct {
	// Delay is how long a new non-interactive creation would wait right now.
	Delay time.Duration
	// Windows counts how many times a fast-fail streak armed a backoff window.
	Windows uint64
	// Delayed counts creations that waited on the rate policy.
	Delayed uint64
	// Refused counts creations rejected because the window outlasted their budget.
	Refused uint64
	// FastFailures counts failures that were fast enough to be evidence of a failing far side.
	FastFailures uint64
	// Recovered counts successes that cleared a streak or a window.
	Recovered uint64
}

// ObserveOutcome reports how one admitted creation ended, for the fast-fail policy.
//
// It is the only feedback the rate policy has: the gate reports what it admitted, never whether the
// work succeeded. Seams that hold a permit call this after the work returns. A nil guard, a local
// rejection, or a failure slower than the fast-fail window does not arm anything.
func (g *Guard) ObserveOutcome(started time.Time, err error) {
	g.observeOutcome(started, err)
}

// rateStats reads the policy's state under the guard lock.
func (g *Guard) rateStats(now time.Time) RateStats {
	g.access.Lock()
	defer g.access.Unlock()
	stats := RateStats{
		Windows:      g.rate.windows,
		Delayed:      g.rate.delayed,
		Refused:      g.rate.refused,
		FastFailures: g.rate.failures,
		Recovered:    g.rate.recovered,
	}
	if !g.rate.until.IsZero() && now.Before(g.rate.until) {
		stats.Delay = g.rate.until.Sub(now)
	}
	return stats
}
