package urltest

// P02B — the in-flight share: one measurement per equivalent question.
//
// MeasurementKey decides when two probes are the same question. This type decides what happens
// next: the first caller becomes the leader and performs the measurement, an equivalent caller
// waits for the leader's answer instead of opening a second socket, and a waiter that is
// cancelled leaves on its own without touching the shared entry.
//
// Three rules are structural, not conventions:
//
//   - only HEALTH probes are shared. A display or diagnostic probe always becomes its own
//     leader, because its answer belongs to its caller and must never become a health verdict.
//
//   - the probe outlives the caller that happened to arrive first. Its lifetime is the set of
//     participants, not any one caller's context: the leader is only the goroutine that performs
//     the dial, and it may stop waiting (its own request was cancelled, its deadline expired)
//     while an equivalent caller is still waiting for the answer. Cancelling the probe when the
//     first caller leaves would hand that caller the leader's cancellation instead of a result.
//
//   - when the LAST participant leaves there is no reason left to hold a socket, so the probe is
//     cancelled and the question is forgotten. A later caller then starts a fresh question rather
//     than joining work whose result nobody will store.
//
// Publication is by identity, not by key. A leader that was abandoned while dialling may finish
// later, after a new leader has registered the same key; publishing by key would let it close the
// NEW question with an older answer and delete the entry the new leader still owns.

import (
	"context"
	"sync"

	"github.com/sagernet/sing/service"
)

// SharedWait is one in-flight question, owned by its participants.
//
// The leader publishes the outcome and closes done; a waiter reads the outcome AFTER observing the
// close, which is the happens-before edge that makes the read safe without a lock.
type SharedWait struct {
	// key is the question this wait answers. It is fixed at creation, so a publication can find
	// (and only find) the registry entry it belongs to.
	key MeasurementKey

	done        chan struct{}
	measurement Measurement
	err         error

	// The fields below are guarded by the registry's access lock.
	closed       bool
	participants int
	probeCtx     context.Context
	cancelProbe  context.CancelFunc
}

// Done is closed when the leader published the outcome.
func (w *SharedWait) Done() <-chan struct{} { return w.done }

// Outcome returns the leader's answer. It is valid only after Done is closed.
func (w *SharedWait) Outcome() (Measurement, error) { return w.measurement, w.err }

// Context is the lifetime of the probe itself: the caller's context VALUES without the caller's
// cancellation. Only the leader of a shared wait has one; it is nil for a question that is never
// shared, and no waiter may run work on it.
func (w *SharedWait) Context() context.Context { return w.probeCtx }

// MeasurementShare tracks equivalent in-flight measurements.
type MeasurementShare struct {
	access   sync.Mutex
	inflight map[MeasurementKey]*SharedWait
}

// NewMeasurementShare returns an empty share registry.
func NewMeasurementShare() *MeasurementShare {
	return &MeasurementShare{inflight: make(map[MeasurementKey]*SharedWait)}
}

// Join registers ctx as a participant of key's measurement and reports whether the caller leads.
//
// A leader must perform the measurement on the wait's own Context and then call Publish. Every
// caller - leader and waiter alike - must finish by calling Await, which is what decides when the
// last participant has left.
//
// A non-health key is never registered: the caller is told it leads and receives an already-closed
// wait, because its answer belongs to it alone.
func (s *MeasurementShare) Join(ctx context.Context, key MeasurementKey) (*SharedWait, bool) {
	if s == nil || key.Purpose != PurposeHealth {
		return &SharedWait{key: key, done: closedChannel()}, true
	}
	s.access.Lock()
	defer s.access.Unlock()
	if existing, loaded := s.inflight[key]; loaded {
		existing.participants++
		return existing, false
	}
	// The probe keeps the context's VALUES - the Box, the outbound manager, the network epoch,
	// the logger, the probe origin - and drops its cancellation. Anything derived from the
	// leader's deadline would otherwise be inherited by every equivalent caller.
	probeCtx, cancelProbe := context.WithCancel(context.WithoutCancel(ctx))
	wait := &SharedWait{
		key:          key,
		done:         make(chan struct{}),
		probeCtx:     probeCtx,
		cancelProbe:  cancelProbe,
		participants: 1,
	}
	s.inflight[key] = wait
	return wait, true
}

// leave removes one participant and, when it was the last, stops the probe and forgets the
// question. It is a no-op for a wait that was never shared.
func (s *MeasurementShare) leave(wait *SharedWait) {
	if s == nil || wait == nil {
		return
	}
	s.access.Lock()
	if wait.participants <= 0 {
		s.access.Unlock()
		return
	}
	wait.participants--
	if wait.participants > 0 {
		s.access.Unlock()
		return
	}
	if s.inflight[wait.key] == wait {
		delete(s.inflight, wait.key)
	}
	cancelProbe := wait.cancelProbe
	wait.cancelProbe = nil
	s.access.Unlock()
	if cancelProbe != nil {
		cancelProbe()
	}
}

// Await waits for the answer as one participant.
//
// It stops on either the answer or the caller's own context, and either way it leaves the
// question. The distinction matters: a caller that stops waiting never cancels a probe another
// caller is still waiting for, and a probe is only stopped once nobody is waiting at all.
func (s *MeasurementShare) Await(ctx context.Context, wait *SharedWait) (Measurement, error) {
	if wait == nil {
		return Measurement{}, context.Canceled
	}
	select {
	case <-wait.Done():
		s.leave(wait)
		return wait.Outcome()
	case <-ctx.Done():
		s.leave(wait)
		return Measurement{}, ctx.Err()
	}
}

// closedChannel is already-closed, so a non-shared caller's Done never blocks.
func closedChannel() chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

// Publish stores the leader's outcome, forgets the question and closes the wait.
//
// It publishes to ONE wait, the one the leader was given by Join, never to "whatever currently
// has this key": an abandoned leader must not answer a question it no longer owns. Calling it
// twice for the same wait is a no-op.
func (s *MeasurementShare) Publish(wait *SharedWait, measurement Measurement, err error) {
	if s == nil || wait == nil {
		return
	}
	s.access.Lock()
	defer s.access.Unlock()
	if wait.closed {
		return
	}
	wait.closed = true
	wait.measurement = measurement
	wait.err = err
	if s.inflight[wait.key] == wait {
		delete(s.inflight, wait.key)
	}
	close(wait.done)
}

// InFlight reports how many questions are being measured right now.
func (s *MeasurementShare) InFlight() int {
	if s == nil {
		return 0
	}
	s.access.Lock()
	defer s.access.Unlock()
	return len(s.inflight)
}

// Participants reports how many callers are currently waiting on a shared question, the leader
// included. It exists for tests and diagnostics: it is how "the probe was cancelled because the
// last participant left" is asserted as a fact rather than inferred from timing.
func (s *MeasurementShare) Participants() int {
	if s == nil {
		return 0
	}
	s.access.Lock()
	defer s.access.Unlock()
	total := 0
	for _, wait := range s.inflight {
		total += wait.participants
	}
	return total
}

// ContextWithMeasurementShare attaches the Box's in-flight registry.
//
// It travels on the context for the same reason the coordinator does: the sharing belongs to
// the Box that is measuring, and a temporary validation Box must not share answers with the
// live one.
func ContextWithMeasurementShare(ctx context.Context, share *MeasurementShare) context.Context {
	return service.ContextWithPtr(ctx, share)
}

// MeasurementShareFromContext returns the Box's registry, or nil when there is none.
//
// Nil means "do not share", which is the correct behaviour for an isolated unit test or a
// library caller: without a Box there is no owner for the shared evidence.
func MeasurementShareFromContext(ctx context.Context) *MeasurementShare {
	return service.PtrFromContext[MeasurementShare](ctx)
}
