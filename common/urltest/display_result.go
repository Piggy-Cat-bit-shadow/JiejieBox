package urltest

// The display TERMINAL result: the newest completed probe outcome for a node, success OR genuine
// failure, for display only.
//
// # Why the existing two layers could not carry it
//
// displayHistory keeps the last SUCCESS per tag. It is read by the node list, by the Clash proxy
// entry and by the manual diagnostic, and a failure must not remove it: failing URL B does not
// disprove a success against URL A. That is the right rule for "how fast is this node", and it is
// exactly why a client could never be shown "the last probe failed" - the only record of a failure
// was its ABSENCE.
//
// healthHistory is selection evidence. Its presence means "this node answered against this target",
// and several policies test it for presence alone, so a delay-0 record there would read as health.
//
// # The convention this layer implements
//
//	delay > 0, valid time        last probe succeeded; show the delay
//	delay = 0, valid time        last probe genuinely failed; the client shows a timeout
//	no record at all             never probed; the client shows its untested placeholder
//
// The pair is written and read together, so a new failure can never be combined with an old delay,
// and a new delay can never inherit an old failure's time.
//
// # Ordering and ownership
//
// StartedAt is the probe's START, not the completion time. It is the ordering identity: a probe that
// was already running while a newer one ran must not describe the node afterwards, which is the same
// rule the evidence layers use (storeDisplayLocked, isNewerEvidence). CompletedAt is what a client
// sees, because "when did this result arrive" is the completion.
//
// The tag epoch and the target's scope epoch are checked under the storage lock, exactly like the
// owned evidence writes, so a result measured against a dropped node, or against a target that was
// invalidated afterwards, is refused rather than published.
//
// # Bounds
//
// The tag-level map is bounded by the number of nodes, like displayHistory. The scoped map is bounded
// by the targets the CONFIGURATION actually measures: it is written only for a scope the caller
// supplies from configuration (a URLTest group's own target), never for an arbitrary URL a user typed
// into a manual diagnostic. That is the same rule that keeps healthHistory bounded, and it is why a
// manual single-node test writes the tag-level entry only.

import (
	"time"
)

// DisplayResult is one node's newest completed probe outcome.
type DisplayResult struct {
	// Delay is the measured delay in milliseconds; 0 means the probe failed.
	Delay uint16
	// StartedAt orders results. Never serialized.
	StartedAt time.Time
	// CompletedAt is when the result became known, and is what a client displays as urlTestTime.
	CompletedAt time.Time
}

// HasResult reports whether a stored result exists. The zero DisplayResult has no CompletedAt.
func (r DisplayResult) HasResult() bool { return !r.CompletedAt.IsZero() }

// Failed reports whether this result is the timeout terminal state.
func (r DisplayResult) Failed() bool { return r.HasResult() && r.Delay == 0 }

// LoadDisplayResult returns the node's newest completed result, whatever target produced it.
//
// This is what a node list / outbound snapshot reads. It is NOT selection evidence: use
// LoadURLTestHistoryFor for that.
func (s *HistoryStorage) LoadDisplayResult(tag string) (DisplayResult, bool) {
	if s == nil {
		return DisplayResult{}, false
	}
	s.access.RLock()
	defer s.access.RUnlock()
	if s.closed {
		return DisplayResult{}, false
	}
	result, loaded := s.displayResults[tag]
	return result, loaded
}

// LoadDisplayResultFor returns the node's newest completed result for ONE target.
//
// A URLTest group reads this with its own scope: the group asked "how does this member perform
// against MY target", and a result against another target answers a different question. Falling back
// to the tag-level entry here would let one target's failure show up in another group.
func (s *HistoryStorage) LoadDisplayResultFor(tag string, scope MeasurementScope) (DisplayResult, bool) {
	if s == nil || scope.URL == "" {
		return DisplayResult{}, false
	}
	s.access.RLock()
	defer s.access.RUnlock()
	if s.closed {
		return DisplayResult{}, false
	}
	// Looked up by value: a scope stored through shareScopeLocked compares equal to this one.
	result, loaded := s.scopedDisplayResults[scopedHistoryKey{Tag: tag, Scope: scope}]
	return result, loaded
}

// StoreDisplayResult records one completed probe outcome for display and notifies subscribers.
//
// delay is the measured delay on success and ZERO on a genuine probe failure. completedAt is when the
// attempt finished. startedAt is the ordering identity (the attempt's start).
//
// scope is written as well when it is a configuration target (URLTest group check). Pass the zero
// MeasurementScope for a result that belongs to no configured target (a manual single-node probe
// against an arbitrary URL); the tag-level entry is still written, which is what the node list reads.
//
// ownership, when non-nil, must still hold: the tag and scope stamps, and the caller's StillCurrent
// check, are evaluated under the storage lock immediately before the write.
//
// It reports whether the result became the stored value. A result that lost the ordering race, or
// that no longer owns its evidence, is discarded - and the caller must then not treat it as this
// round's outcome.
func (s *HistoryStorage) StoreDisplayResult(tag string, scope MeasurementScope, delay uint16, startedAt time.Time, completedAt time.Time, ownership *EvidenceOwnership) bool {
	if s == nil || tag == "" || startedAt.IsZero() || completedAt.IsZero() {
		return false
	}
	s.access.Lock()
	if s.closed {
		s.access.Unlock()
		return false
	}
	scoped := scope.URL != ""
	if ownership != nil && !ownership.authorizesLocked(s, tag, scope) {
		s.access.Unlock()
		return false
	}
	result := DisplayResult{Delay: delay, StartedAt: startedAt, CompletedAt: completedAt}
	stored := false
	if existing, loaded := s.displayResults[tag]; !loaded || !existing.StartedAt.After(startedAt) {
		s.displayResults[tag] = result
		stored = true
	}
	if scoped {
		// shareScopeLocked keeps one copy of the target identity for every entry that names it,
		// exactly like healthHistory: a node's key must not retain its own URL copy.
		key := scopedHistoryKey{Tag: tag, Scope: s.shareScopeLocked(scope)}
		if existing, loaded := s.scopedDisplayResults[key]; !loaded || !existing.StartedAt.After(startedAt) {
			s.scopedDisplayResults[key] = result
			stored = true
		}
	}
	s.access.Unlock()
	if stored {
		s.notifyUpdated()
	}
	return stored
}

// DeleteDisplayResults removes every display terminal result for a node, in both the tag-level and
// the scoped map. Used when the node is gone.
//
// The epoch bump that makes in-flight probes unable to write is the caller's (DeleteURLTestHistory
// already performs it); this only drops the records.
func (s *HistoryStorage) DeleteDisplayResults(tag string) {
	if s == nil {
		return
	}
	s.access.Lock()
	if s.closed {
		s.access.Unlock()
		return
	}
	_, hadTag := s.displayResults[tag]
	delete(s.displayResults, tag)
	hadScoped := false
	for key := range s.scopedDisplayResults {
		if key.Tag == tag {
			delete(s.scopedDisplayResults, key)
			hadScoped = true
		}
	}
	s.access.Unlock()
	if hadTag || hadScoped {
		s.notifyUpdated()
	}
}

// DeleteDisplayResultFor removes one target's display terminal result.
//
// Retiring a target or a node must not leave a failure on screen that describes a measurement the
// configuration no longer performs.
func (s *HistoryStorage) DeleteDisplayResultFor(tag string, scope MeasurementScope) {
	if s == nil || scope.URL == "" {
		return
	}
	s.access.Lock()
	if s.closed {
		s.access.Unlock()
		return
	}
	key := scopedHistoryKey{Tag: tag, Scope: s.shareScopeLocked(scope)}
	_, loaded := s.scopedDisplayResults[key]
	delete(s.scopedDisplayResults, key)
	s.access.Unlock()
	if loaded {
		s.notifyUpdated()
	}
}

// DisplayResultEntryCount reports how many tag-level terminal results are stored, so a test can
// assert bounding as a fact.
func (s *HistoryStorage) DisplayResultEntryCount() int {
	if s == nil {
		return 0
	}
	s.access.RLock()
	defer s.access.RUnlock()
	return len(s.displayResults)
}

// ScopedDisplayResultEntryCount reports how many target-scoped terminal results are stored.
func (s *HistoryStorage) ScopedDisplayResultEntryCount() int {
	if s == nil {
		return 0
	}
	s.access.RLock()
	defer s.access.RUnlock()
	return len(s.scopedDisplayResults)
}
