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
//   - a waiter never holds the registry: Join hands back a channel and the waiter selects on it
//     together with its own context, so cancellation does not need a cleanup call and cannot
//     strand a slot.
//   - Finish is idempotent and removes the entry under one lock, so a late leader cannot close
//     a channel a NEW leader is already serving.

import (
	"github.com/sagernet/sing/service"

	"context"
	"sync"
)

// SharedWait is one in-flight question. The leader publishes the outcome and closes done; a
// waiter reads the outcome AFTER observing the close, which is the happens-before edge that
// makes the read safe without a lock.
type SharedWait struct {
	done        chan struct{}
	measurement Measurement
	err         error
}

// Done is closed when the leader published the outcome.
func (w *SharedWait) Done() <-chan struct{} { return w.done }

// Outcome returns the leader's answer. It is valid only after Done is closed.
func (w *SharedWait) Outcome() (Measurement, error) { return w.measurement, w.err }

// MeasurementShare tracks equivalent in-flight measurements.
type MeasurementShare struct {
	access   sync.Mutex
	inflight map[MeasurementKey]*SharedWait
}

// NewMeasurementShare returns an empty share registry.
func NewMeasurementShare() *MeasurementShare {
	return &MeasurementShare{inflight: make(map[MeasurementKey]*SharedWait)}
}

// Join registers the caller for key and returns the channel the leader closes.
//
// It returns (nil, true) when the caller must perform the measurement itself - either it is the
// leader, or the key is not a health question and therefore never shared. Otherwise it returns
// the channel the leader closes when the answer exists; use JoinQuestion to read the outcome.
func (s *MeasurementShare) Join(key MeasurementKey) (<-chan struct{}, bool) {
	wait, isLeader := s.JoinQuestion(key)
	if isLeader {
		return wait.Done(), true
	}
	return wait.Done(), false
}

// JoinQuestion is Join with the answer handle, for a caller that wants the leader's outcome.
func (s *MeasurementShare) JoinQuestion(key MeasurementKey) (*SharedWait, bool) {
	if s == nil || key.Purpose != PurposeHealth {
		return &SharedWait{done: closedChannel()}, true
	}
	s.access.Lock()
	defer s.access.Unlock()
	if existing, loaded := s.inflight[key]; loaded {
		return existing, false
	}
	measurement := &SharedWait{done: make(chan struct{})}
	s.inflight[key] = measurement
	return measurement, true
}

// closedChannel is already-closed, so a non-shared caller's Done never blocks.
func closedChannel() chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

// Wait blocks until the shared measurement finishes, returning true, or until ctx ends,
// returning false. A waiter that leaves does not need to tell the registry: it holds no slot.
func (s *MeasurementShare) Wait(ctx context.Context, done <-chan struct{}) bool {
	if done == nil {
		return false
	}
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

// Publish stores the leader's outcome, removes the entry and closes the wait. It is idempotent:
// a late leader cannot close a wait a new leader is already serving.
func (s *MeasurementShare) Publish(key MeasurementKey, measurement Measurement, err error) {
	if s == nil {
		return
	}
	s.access.Lock()
	defer s.access.Unlock()
	wait, loaded := s.inflight[key]
	if !loaded {
		return
	}
	delete(s.inflight, key)
	wait.measurement = measurement
	wait.err = err
	close(wait.done)
}

// Finish publishes an empty outcome; it exists for callers that share the EVENT only.
func (s *MeasurementShare) Finish(key MeasurementKey) {
	s.Publish(key, Measurement{}, nil)
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
