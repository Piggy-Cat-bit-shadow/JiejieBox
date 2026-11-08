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

// sharedMeasurement is one in-flight question. done is closed exactly once.
type sharedMeasurement struct {
	done    chan struct{}
	closed  bool
	waiters int
}

// MeasurementShare tracks equivalent in-flight measurements.
type MeasurementShare struct {
	access   sync.Mutex
	inflight map[MeasurementKey]*sharedMeasurement
}

// NewMeasurementShare returns an empty share registry.
func NewMeasurementShare() *MeasurementShare {
	return &MeasurementShare{inflight: make(map[MeasurementKey]*sharedMeasurement)}
}

// Join registers the caller for key.
//
// It returns (nil, true) when the caller must perform the measurement itself - either it is the
// leader, or the key is not a health question and therefore never shared. Otherwise it returns
// the channel the leader closes when the answer exists.
func (s *MeasurementShare) Join(key MeasurementKey) (<-chan struct{}, bool) {
	if s == nil || key.Purpose != PurposeHealth {
		return nil, true
	}
	s.access.Lock()
	defer s.access.Unlock()
	if existing, loaded := s.inflight[key]; loaded {
		existing.waiters++
		return existing.done, false
	}
	measurement := &sharedMeasurement{done: make(chan struct{})}
	s.inflight[key] = measurement
	return measurement.done, true
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

// Finish publishes the answer: it closes the shared channel and removes the entry. It is
// idempotent, and finishing a key this registry is not serving is a no-op.
func (s *MeasurementShare) Finish(key MeasurementKey) {
	if s == nil {
		return
	}
	s.access.Lock()
	defer s.access.Unlock()
	measurement, loaded := s.inflight[key]
	if !loaded || measurement.closed {
		return
	}
	measurement.closed = true
	delete(s.inflight, key)
	close(measurement.done)
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
