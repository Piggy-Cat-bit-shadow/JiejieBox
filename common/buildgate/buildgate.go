package buildgate

// A bounded reservation for BUILD work: download, parse, compile, and the window in which a
// new version and the old one overlap.
//
// # Why builds need their own reservation
//
// A provider refresh or a remote rule-set compile holds the old document, the new document,
// the encoded artifacts and any matcher index built from them at the same time. Two of them
// at once can double the peak a box already pays for one, and the failure mode is an OOM kill
// with no attribution. Dial admission is the wrong budget to charge them to: a build is not a
// connection, it outlives several dials, and it must be able to say "not now" without
// touching the version that is currently serving traffic.
//
// # Why the refusal is a first-class error
//
// A deferred build is not a failure: the old version is still valid, and the caller (a retry,
// a manual refresh, the next tick) can try again. It is reported as ErrDeferred so a caller
// can tell "pressure" from "the document is broken", and nothing is swapped or deleted on the
// way out.
//
// # Why this wraps resourceguard.Guard
//
// The queue, the fair hand-off, the bounded wait and the per-waiter cancellation already exist
// there and are covered by the package's attack tests. This type adds only the vocabulary a
// build needs: one reservation, a deferral error, and counters that distinguish admitted,
// deferred and failed builds.
//
// # Why the reservation is derived from pressure (audit Q20)
//
// A fixed pair of slots does not answer the question the reservation exists for: "how much build
// memory may overlap with the version that is serving traffic". The Gate therefore takes the
// process's ONE pressure fact - the same resourceguard.Level the dial admission guard and the
// reclaim path read, derived from the timer that already samples the footprint - and the shape of
// the reservation follows it through resourceguard's own derivation:
//
//	LevelNormal    the configured limit (2 in a box)
//	LevelArmed     the same limit, a halved wait and queue: parked work is shed first
//	LevelCritical  one build at a time, a 2-deep queue and a 100 ms wait
//
// # The honest boundary
//
// The numbers above are COUNTS OF BUILDS, and they are provable: at most Limit builds can overlap,
// so at most 2*Limit documents (one old, one new, per build) and Limit indices can be resident at
// once. The BYTES are not provable from here - a rule-set document's encoded size is known only
// after it is downloaded, and the matcher index is built by a dependency - so no byte estimate is
// invented. Budget() reports the countable part together with that boundary, and the degradation
// under pressure is a provable reduction of the count.

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	E "github.com/sagernet/sing/common/exceptions"
)

// ErrDeferred means the build did not start because the reservation is busy. The current
// version is untouched and the operation is safe to retry.
var ErrDeferred = E.New("build deferred: the build reservation is busy")

// Gate is one build reservation shared by every build owner in a box.
type Gate struct {
	inner    *resourceguard.Guard
	deferred atomic.Uint64
	admitted atomic.Uint64
	// The lifecycle counters. A permit is a permission, not an outcome: admitted counts permits
	// handed out, and the three terminal counters say how each of them ended. Their sum plus
	// Inflight is AdmittedBuilds, which is the invariant that makes "a failed build returns its
	// slot" a checked fact rather than a claim.
	succeeded atomic.Uint64
	failed    atomic.Uint64
	abandoned atomic.Uint64
	// Overlap shape: a build that replaces a resident version holds an old document and a new one
	// at the same time; a FIRST build has no predecessor and holds only the new one. The
	// reservation reports the two separately instead of assuming every build overlaps an old
	// version.
	withPrevious    atomic.Uint64
	withoutPrevious atomic.Uint64
	observer        atomic.Pointer[func(context.Context, Admission)]
}

// Admission is one completed Acquire, reported to an observer.
//
// It exists so a sampler can separate the time spent WAITING for the reservation from the time
// spent building after it: the gate is taken before the download, so "average fetch" folds the
// queue into the build and a change that only moves the queue would look like a build regression.
type Admission struct {
	// Wait is how long Acquire took, including the time spent queued.
	Wait time.Duration
	// Admitted is false when the attempt was deferred (queue full or wait timeout).
	Admitted bool
}

// Observe installs an optional observer, replacing any previous one. Nil removes it.
//
// The observer runs synchronously on the acquiring goroutine, inside Acquire, so it must be cheap
// and must not block. It receives the caller's context, which lets a sampler correlate the
// admission with the attempt that caused it without the gate having to know about workers. The
// default is nil: a gate that nobody observes pays one atomic load per build and nothing else.
func (g *Gate) Observe(observer func(context.Context, Admission)) {
	if g == nil {
		return
	}
	if observer == nil {
		g.observer.Store(nil)
		return
	}
	g.observer.Store(&observer)
}

func (g *Gate) notify(ctx context.Context, admission Admission) {
	if observer := g.observer.Load(); observer != nil {
		(*observer)(ctx, admission)
	}
}

// Stats is one consistent snapshot of the reservation's accounting.
//
// # Why it carries both shapes
//
// GuardStats adds what a pressure-derived reservation needs to be explained: the level it was
// shaped by and how often that moved. The embedded resourceguard.Stats is the SAME gate snapshot,
// promoted under the field names every existing consumer already reads (Inflight, Waiters, Limit,
// ...) - one call produces both, so they cannot disagree. The two build-specific counters sit
// beside them because "deferred" is this reservation's vocabulary and not the generic gate's.
type Stats struct {
	resourceguard.GuardStats
	resourceguard.Stats
	Deferred       uint64
	AdmittedBuilds uint64
	// Succeeded/Failed/Abandoned classify the permits AdmittedBuilds handed out. Abandoned is a
	// build that returned its slot without declaring an outcome; it is its own counter rather than
	// being folded into either, because an undeclared outcome is not evidence of success and not
	// evidence of failure.
	Succeeded uint64
	Failed    uint64
	Abandoned uint64
	// BuildsWithPreviousVersion/BuildsWithoutPreviousVersion are the ADMITTED builds by whether an
	// old version of the same document was resident while they ran - the two shapes the overlap
	// budget must not conflate.
	BuildsWithPreviousVersion    uint64
	BuildsWithoutPreviousVersion uint64
}

// New returns a gate that allows limit concurrent builds, with queue callers allowed to wait
// up to wait for one.
//
// It is the no-pressure constructor: the reservation keeps the shape it was given at every level.
// A caller that has the process's pressure fact should use NewWithPressure instead, which is what
// the box does.
func New(limit int, queue int, wait time.Duration) *Gate {
	return NewWithPressure(resourceguard.Config{Limit: limit, Queue: queue, Wait: wait}, nil)
}

// NewWithPressure returns a gate whose shape is derived from the process pressure level.
//
// pressure is the process's ONE pressure fact - on a Network Extension the oomkiller's published
// level, through resourceguard.FromContext. A nil pressure function means "always normal", which
// is the shape the old constructor had and the correct behaviour for an embedder with no pressure
// policy: a gate must not invent pressure.
//
// The configuration's Limit/Queue/Wait describe LevelNormal; resourceguard derives the other two
// levels from it, so the reservation cannot be tuned for one level and left stale at another.
func NewWithPressure(config resourceguard.Config, pressure func() resourceguard.Level) *Gate {
	if pressure == nil {
		pressure = func() resourceguard.Level { return resourceguard.LevelNormal }
	}
	return &Gate{inner: resourceguard.NewGuard(config, pressure)}
}

// Acquire reserves one build slot, or returns ErrDeferred when the reservation is busy or the
// current pressure level does not admit the build.
//
// The returned release is safe to call more than once: the permit behind it is idempotent, and a
// build path that releases on both a defer and an error path must not be able to hand the same
// slot to two builds.
func (g *Gate) Acquire(ctx context.Context) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	started := time.Now()
	permit, err := g.inner.Acquire(ctx, resourceguard.ClassNormal)
	wait := time.Since(started)
	if err != nil {
		if E.IsMulti(err, resourceguard.ErrQueueFull, resourceguard.ErrWaitTimeout, resourceguard.ErrRateLimited) {
			g.deferred.Add(1)
			g.notify(ctx, Admission{Wait: wait})
			return nil, ErrDeferred
		}
		return nil, err
	}
	g.admitted.Add(1)
	g.notify(ctx, Admission{Wait: wait, Admitted: true})
	return permit.Release, nil
}

// Stats reports the reservation's accounting.
func (g *Gate) Stats() Stats {
	if g == nil {
		return Stats{}
	}
	guardStats := g.inner.Stats()
	return Stats{
		GuardStats:                   guardStats,
		Stats:                        guardStats.Gate,
		Deferred:                     g.deferred.Load(),
		AdmittedBuilds:               g.admitted.Load(),
		Succeeded:                    g.succeeded.Load(),
		Failed:                       g.failed.Load(),
		Abandoned:                    g.abandoned.Load(),
		BuildsWithPreviousVersion:    g.withPrevious.Load(),
		BuildsWithoutPreviousVersion: g.withoutPrevious.Load(),
	}
}

// BuildOptions is what a build owner knows about the build it is about to start, and which the
// reservation cannot infer.
type BuildOptions struct {
	// HasPreviousVersion says a version of the SAME document is still resident when this build runs.
	// That is the overlap the reservation exists to bound: the old document must stay reachable
	// while the new one is downloaded, parsed and indexed. A FIRST build has no predecessor - it
	// holds one document, not two - so a budget that charges it an old document would be
	// overstating the resident set for the common case of a cold start.
	HasPreviousVersion bool
}

// Reservation is one admitted build: the permit plus the outcome, so "the build failed" and "the
// build's slot came back" are the same call and cannot disagree.
//
// Release/Succeed/Fail are all idempotent, so a defer and an error path may both end the same
// reservation without freeing the slot twice or counting the build twice.
type Reservation struct {
	gate      *Gate
	options   BuildOptions
	permit    func()
	released  atomic.Bool
	completed atomic.Bool
}

// Build marks the reservation successful and returns its slot.
func (r *Reservation) Succeed() {
	if r == nil || r.gate == nil {
		return
	}
	if r.completed.CompareAndSwap(false, true) {
		r.gate.succeeded.Add(1)
	}
	r.Release()
}

// Fail marks the reservation failed and returns its slot. The failure still returns the permit: a
// build that dies must not hold the reservation for the rest of the process's life.
func (r *Reservation) Fail() {
	if r == nil || r.gate == nil {
		return
	}
	if r.completed.CompareAndSwap(false, true) {
		r.gate.failed.Add(1)
	}
	r.Release()
}

// Release returns the slot. When no outcome was declared first, the build is counted as abandoned
// rather than being reported as either a success or a failure.
func (r *Reservation) Release() {
	if r == nil || r.gate == nil {
		return
	}
	if !r.released.CompareAndSwap(false, true) {
		return
	}
	if r.completed.CompareAndSwap(false, true) {
		r.gate.abandoned.Add(1)
	}
	r.permit()
}

// AcquireBuild reserves one build slot and records the shape of the build it reserved it for.
//
// It is Acquire plus the options, and it is what a build owner should call: the returned
// Reservation carries the outcome, so the report can compare admitted, deferred, succeeded and
// failed builds of the same gate.
func (g *Gate) AcquireBuild(ctx context.Context, options BuildOptions) (*Reservation, error) {
	release, err := g.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if options.HasPreviousVersion {
		g.withPrevious.Add(1)
	} else {
		g.withoutPrevious.Add(1)
	}
	return &Reservation{gate: g, options: options, permit: release}, nil
}

// AcquireBuildFromContext reserves a build slot when the context carries a gate, and returns a
// reservation that releases nothing otherwise.
func AcquireBuildFromContext(ctx context.Context, options BuildOptions) (*Reservation, error) {
	gate := FromContext(ctx)
	if gate == nil {
		return &Reservation{}, nil
	}
	return gate.AcquireBuild(ctx, options)
}

// Level reports the pressure level the reservation is currently shaped by.
func (g *Gate) Level() resourceguard.Level {
	if g == nil {
		return resourceguard.LevelNormal
	}
	return g.inner.Level()
}

// Budget is the reservation's explicable budget at one moment.
//
// # The admission limit is not the overlap
//
// Limit is what the gate admits from now on. It is NOT how many builds can be in flight: a permit
// already handed out is never revoked mid-build, so when pressure rises from normal to critical the
// two builds permitted under the old level keep running while the admission limit becomes one.
// Reporting Limit as "max overlapping builds" therefore understated the peak that was actually
// resident - the audit's counterexample, reproduced 3/3 - so the two numbers are reported
// separately:
//
//	Limit                 what the gate admits now
//	Inflight              what is running now
//	PeakOverlapping       the largest overlap observed since the gate was created
//	MaxOverlappingBuilds  max(Limit, Inflight): the bound that is still true at this instant
//
// # What is provable, and what is not
//
// MaxOldDocuments/MaxNewDocuments/MaxMatcherIndices are counts and are exact for as long as the level
// holds. Old documents are bounded by MaxOverlappingBuilds - the builds already running may each
// still hold the version they are replacing - while a FIRST build holds none of them;
// Stats.BuildsWithPreviousVersion / BuildsWithoutPreviousVersion say how many of each were admitted,
// so the same budget describes both shapes instead of assuming every build overlaps an old version.
//
// ByteBoundKnown is false on every platform, and that is a statement about the source of the numbers
// rather than about the build: the encoded size of a rule-set document is known only after its
// download, and the matcher index is built inside a dependency whose memory is not observable from
// here. Reporting a byte estimate would be inventing one, so the budget reports the countable part and
// this boundary.
//
// Note names the degradation: the count is what pressure is allowed to reduce, and at LevelCritical
// it is reduced to one ADMISSION at a time, which is the strongest bound this reservation can prove.
type Budget struct {
	Level resourceguard.Level
	// Limit is the current ADMISSION limit: how many new builds the gate admits.
	Limit int
	Queue int
	Wait  time.Duration
	// Inflight is how many builds hold a permit right now. It can exceed Limit, because a permit is
	// never revoked.
	Inflight int
	// PeakOverlapping is the largest number of builds that have overlapped since the gate was
	// created.
	PeakOverlapping int
	// MaxOverlappingBuilds is the overlap bound that holds at this instant: max(Limit, Inflight).
	MaxOverlappingBuilds int
	MaxOldDocuments      int
	MaxNewDocuments      int
	MaxMatcherIndices    int
	ByteBoundKnown       bool
	Note                 string
}

// Budget reports the reservation's current budget.
func (g *Gate) Budget() Budget {
	if g == nil {
		return Budget{ByteBoundKnown: false, Note: "no build reservation in this build"}
	}
	stats := g.inner.Stats()
	limit := stats.Gate.Limit
	inflight := stats.Gate.Inflight
	// The overlap bound must include the builds that are ALREADY running: they hold their permits
	// until they finish, so a level change cannot reduce their number and reporting the new
	// admission limit as the overlap would understate what is resident right now.
	maxOverlapping := limit
	if inflight > maxOverlapping {
		maxOverlapping = inflight
	}
	return Budget{
		Level:                stats.Level,
		Limit:                limit,
		Queue:                stats.Gate.QueueLimit,
		Wait:                 stats.Gate.WaitBudget,
		Inflight:             inflight,
		PeakOverlapping:      stats.Gate.MaxInflight,
		MaxOverlappingBuilds: maxOverlapping,
		MaxOldDocuments:      maxOverlapping,
		MaxNewDocuments:      maxOverlapping,
		MaxMatcherIndices:    maxOverlapping,
		ByteBoundKnown:       false,
		Note: "counts of builds and of the documents/indices they can overlap; a permit is never " +
			"revoked, so maxOverlappingBuilds is max(admission limit, builds already running) and " +
			"limit alone is only what the gate admits from now on. bytes are not provable here " +
			"because a document's encoded size is known only after download and its matcher index " +
			"is built inside a dependency. Pressure reduces the COUNT (one admission at critical), " +
			"which is the provable degradation.",
	}
}

// buildGateContextKey carries the box's gate. It is a private key rather than a service:
// the gate owns no lifecycle and nothing enumerates it.
type buildGateContextKey struct{}

// ContextWithGate attaches the box's build reservation.
func ContextWithGate(ctx context.Context, gate *Gate) context.Context {
	return context.WithValue(ctx, buildGateContextKey{}, gate)
}

// FromContext returns the box's build reservation, or nil when the caller has none (a unit
// test, an embedder, a process that never built a box). Nil means "ungated", which is the
// correct behaviour for an isolated caller.
func FromContext(ctx context.Context) *Gate {
	gate, _ := ctx.Value(buildGateContextKey{}).(*Gate)
	return gate
}

// AcquireFromContext reserves a build slot when the context carries a gate, and returns a
// no-op release otherwise.
func AcquireFromContext(ctx context.Context) (func(), error) {
	gate := FromContext(ctx)
	if gate == nil {
		return func() {}, nil
	}
	return gate.Acquire(ctx)
}
