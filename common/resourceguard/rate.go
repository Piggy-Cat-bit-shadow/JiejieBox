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

// maxRateBuckets bounds how many destination buckets the fast-fail policy remembers.
//
// The bucket key is supplied by the creation seam (a proxy server address). It is a
// configuration-shaped value, so the map would stay small in ordinary use; the bound exists
// because a process can be handed a large Provider set and the policy must not become an
// unbounded allocation in the path that is supposed to protect memory. When the bound is reached
// the least recently used bucket is forgotten: the process-wide gate still bounds concurrency, so
// a forgotten storm degrades isolation, not safety.
const maxRateBuckets = 256

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
// storm.
//
// # Why the state is bucketed by destination
//
// A single streak counter is process-wide, so a storm against one proxy server used to delay NEW
// creations to every other destination: a bad node made the good node look slow, and the delay
// survived as long as the bad node kept failing. The policy now keeps one window per destination
// key (the proxy server a creation is aimed at) and one process-global bucket for callers that do
// not name a destination (the build gate). A failure streak in one bucket arms only that bucket's
// window; a success in another bucket does not clear it. The interactive exemption, the
// cancellable bounded wait, and the exponential cap are unchanged.
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

// rateBucket is the per-destination fast-fail state. It is protected by Guard.access, like the
// level. Only the state that must differ per destination lives here; the counters are process-wide
// so that forgetting an evicted bucket never rewrites history in a status report.
type rateBucket struct {
	streak   int
	delay    time.Duration
	until    time.Time
	windows  uint64
	lastUsed time.Time
}

// rateCounters are the process-wide totals behind RateStats. They are aggregate on purpose: a
// bucket can be evicted under the bound above, and a diagnostic that silently lost the failures of
// evicted destinations would be lying about the process.
type rateCounters struct {
	windows   uint64
	delayed   uint64
	refused   uint64
	failures  uint64
	recovered uint64
}

// rateGate applies the current rate window to one non-interactive creation aimed at key.
//
// The decision has three outcomes, all visible in the counters: no window (return immediately), a
// window that fits in the caller's remaining budget (wait, cancellably), and a window longer than
// that budget (refuse with ErrRateLimited rather than make the caller wait for a deadline it will
// miss). The last one is deliberately an error instead of a silent wait: the caller then sees a
// local rejection immediately and can surface it, instead of discovering a timeout later.
//
// The no-window case is the hot path and reads the atomic mirror only; the mutex is taken only
// while SOME window is actually armed.
func (g *Guard) rateGate(ctx context.Context, key string) error {
	armedUntil := g.rateUntil.Load()
	if armedUntil == 0 {
		return nil
	}
	now := g.clockNow()
	if !now.Before(time.Unix(0, armedUntil)) {
		return nil
	}
	g.access.Lock()
	bucket := g.rateBuckets[key]
	if bucket == nil || bucket.until.IsZero() || !now.Before(bucket.until) {
		if bucket != nil {
			bucket.until = time.Time{}
			bucket.delay = 0
		}
		g.access.Unlock()
		return nil
	}
	delay := bucket.until.Sub(now)
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline && now.Add(delay).After(deadline) {
		g.rateCounters.refused++
		g.access.Unlock()
		return ErrRateLimited
	}
	g.rateCounters.delayed++
	g.access.Unlock()
	return g.pause(ctx, delay)
}

// observeOutcome records how one admitted creation aimed at key ended.
//
// It is called by the seam that held the permit, with the error the creation returned. Local
// rejections are excluded: a queue refusal says nothing about whether the far side is healthy, and
// counting it would let this process's own backpressure arm a rate limit against a working node.
func (g *Guard) observeOutcome(key string, started time.Time, err error) {
	if g == nil {
		return
	}
	now := g.clockNow()
	elapsed := now.Sub(started)
	g.access.Lock()
	defer g.access.Unlock()
	bucket := g.rateBucketLocked(key, now)
	if err == nil {
		if bucket.streak > 0 || !bucket.until.IsZero() {
			g.rateCounters.recovered++
		}
		bucket.streak = 0
		bucket.until = time.Time{}
		bucket.delay = 0
		// A success means the far side is reachable again, so a later storm starts its backoff
		// from the base delay instead of resuming at whatever the previous outage reached.
		bucket.windows = 0
		return
	}
	if IsLocalRejection(err) {
		return
	}
	if elapsed > g.rateConfig.FastFailWindow {
		bucket.streak = 0
		return
	}
	g.rateCounters.failures++
	bucket.streak++
	if bucket.streak < g.rateConfig.RateLimitAfter {
		return
	}
	bucket.streak = 0
	bucket.delay = g.nextRateDelayLocked(bucket.windows)
	bucket.until = g.clockNow().Add(bucket.delay)
	bucket.windows++
	g.rateCounters.windows++
	until := bucket.until.UnixNano()
	for {
		current := g.rateUntil.Load()
		if until <= current || g.rateUntil.CompareAndSwap(current, until) {
			break
		}
	}
}

// rateBucketLocked returns the bucket for key, creating it under the memory bound.
//
// An armed bucket that already expired is reusable; unarmed buckets with no streak and no window
// are the first eviction candidates because forgetting them cannot change a decision.
func (g *Guard) rateBucketLocked(key string, now time.Time) *rateBucket {
	if bucket, loaded := g.rateBuckets[key]; loaded {
		bucket.lastUsed = now
		return bucket
	}
	if len(g.rateBuckets) >= maxRateBuckets {
		g.pruneRateBucketsLocked(now)
	}
	if len(g.rateBuckets) >= maxRateBuckets {
		var (
			oldestKey   string
			oldestTime  time.Time
			oldestFound bool
		)
		for candidateKey, candidate := range g.rateBuckets {
			if !oldestFound || candidate.lastUsed.Before(oldestTime) {
				oldestKey = candidateKey
				oldestTime = candidate.lastUsed
				oldestFound = true
			}
		}
		delete(g.rateBuckets, oldestKey)
	}
	bucket := &rateBucket{lastUsed: now}
	g.rateBuckets[key] = bucket
	return bucket
}

// pruneRateBucketsLocked forgets buckets whose decision state is empty. It does not run on the
// hot path: only a new key at the bound pays for it.
func (g *Guard) pruneRateBucketsLocked(now time.Time) {
	for key, bucket := range g.rateBuckets {
		if bucket.streak == 0 && (bucket.until.IsZero() || !now.Before(bucket.until)) {
			delete(g.rateBuckets, key)
		}
	}
}

// nextRateDelayLocked grows the delay exponentially and applies the configured jitter.
func (g *Guard) nextRateDelayLocked(windows uint64) time.Duration {
	base := g.rateConfig.RateLimitBase
	delay := base
	for index := uint64(0); index < windows && delay < g.rateConfig.RateLimitMax; index++ {
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
	// Delay is how long a new non-interactive creation would wait right now (the longest armed
	// destination window; zero when no window is armed).
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
	// Buckets counts the destination buckets currently remembered. It is a diagnostic: a process
	// with many Provider servers is expected to have several, and the bound above caps it.
	Buckets int
	// ArmedBuckets counts the buckets with a live window right now. A single armed bucket is the
	// evidence that a storm is isolated to one destination rather than the whole process.
	ArmedBuckets int
}

// ObserveOutcome reports how one admitted creation ended, for the fast-fail policy.
//
// It is the only feedback the rate policy has: the gate reports what it admitted, never whether the
// work succeeded. Seams that hold a permit call this after the work returns. A nil guard, a local
// rejection, or a failure slower than the fast-fail window does not arm anything.
//
// The empty key is the process-global bucket. It is used by creations that are not aimed at a
// destination (the build gate); network seams that know their proxy server use ObserveOutcomeFor.
func (g *Guard) ObserveOutcome(started time.Time, err error) {
	g.ObserveOutcomeFor("", started, err)
}

// ObserveOutcomeFor is ObserveOutcome with the destination the creation was aimed at.
//
// One bad destination's fast-failure storm must not delay creations aimed at another: the bucket
// key is the proxy server address, and the window armed by one key is consulted only by
// acquisitions carrying the same key.
func (g *Guard) ObserveOutcomeFor(key string, started time.Time, err error) {
	g.observeOutcome(key, started, err)
}

// rateStats reads the policy's state under the guard lock.
func (g *Guard) rateStats(now time.Time) RateStats {
	g.access.Lock()
	defer g.access.Unlock()
	stats := RateStats{
		Windows:      g.rateCounters.windows,
		Delayed:      g.rateCounters.delayed,
		Refused:      g.rateCounters.refused,
		FastFailures: g.rateCounters.failures,
		Recovered:    g.rateCounters.recovered,
		Buckets:      len(g.rateBuckets),
	}
	for _, bucket := range g.rateBuckets {
		if bucket.until.IsZero() || !now.Before(bucket.until) {
			continue
		}
		stats.ArmedBuckets++
		if delay := bucket.until.Sub(now); delay > stats.Delay {
			stats.Delay = delay
		}
	}
	return stats
}
