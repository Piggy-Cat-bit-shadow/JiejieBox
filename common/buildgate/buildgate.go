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
// # Why this wraps resourceguard.Gate
//
// The queue, the fair hand-off, the bounded wait and the per-waiter cancellation already exist
// there and are covered by the package's attack tests. This type adds only the vocabulary a
// build needs: one reservation, a deferral error, and counters that distinguish admitted,
// deferred and failed builds.

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
	inner    *resourceguard.Gate
	deferred atomic.Uint64
	admitted atomic.Uint64
}

// Stats is one consistent snapshot of the reservation's accounting.
type Stats struct {
	resourceguard.Stats
	Deferred       uint64
	AdmittedBuilds uint64
}

// New returns a gate that allows limit concurrent builds, with queue callers allowed to wait
// up to wait for one.
func New(limit int, queue int, wait time.Duration) *Gate {
	if limit <= 0 {
		limit = 1
	}
	return &Gate{inner: resourceguard.NewBoundedGate(limit, queue, wait)}
}

// Acquire reserves one build slot, or returns ErrDeferred when the reservation is busy.
//
// The returned release is safe to call once; it is not idempotent by design, because a build
// that released twice would hand its slot to a second build while the first still believed it
// owned it - the caller structure (defer release) makes the double call a bug, not a case.
func (g *Gate) Acquire(ctx context.Context) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	permit, err := g.inner.Acquire(ctx, resourceguard.ClassNormal)
	if err != nil {
		if E.IsMulti(err, resourceguard.ErrQueueFull, resourceguard.ErrWaitTimeout) {
			g.deferred.Add(1)
			return nil, ErrDeferred
		}
		return nil, err
	}
	g.admitted.Add(1)
	return permit.Release, nil
}

// Stats reports the reservation's accounting.
func (g *Gate) Stats() Stats {
	if g == nil {
		return Stats{}
	}
	return Stats{
		Stats:          g.inner.Stats(),
		Deferred:       g.deferred.Load(),
		AdmittedBuilds: g.admitted.Load(),
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
