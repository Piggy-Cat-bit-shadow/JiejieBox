package urltest

import (
	"math"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/observable"
)

// scopedHistoryKey identifies one measurement: a node measured against a specific target.
//
// # Why the tag alone was wrong
//
// A delay belongs to a (node, target) pair. Keying by tag meant "Hong Kong to gstatic" and
// "Hong Kong to Cloudflare" were the same entry, so whichever was measured last decided what a
// URLTest group configured for the other target would select from.
type scopedHistoryKey struct {
	Tag   string
	Scope MeasurementScope
}

// displayRecord is one tag's most recent successful measurement.
type displayRecord struct {
	History adapter.URLTestHistory
	Scope   MeasurementScope
}

// HistoryStorage holds node measurements in two INDEPENDENT layers.
//
// # display
//
// One entry per tag: the most recent successful measurement of that node, whatever target produced
// it. This is what a node list, a Clash proxy entry, or a manual diagnostic reads.
//
// It is written by manual probes AND by automatic health checks, because both are legitimately "the
// last successful measurement". It is never removed by a failure: a failed probe against one URL
// does not invalidate a success against another.
//
// # health
//
// One entry per (tag, target), and the only thing selection, interval skipping and tolerance read.
// ONLY an automatic URLTest group health check may write or delete it.
//
// # Why they are separate
//
// A manual diagnostic asks "how fast is this node against the URL I typed". An automatic group asks
// "which member should carry traffic for MY configured target". Answering the second question with
// the first answer's data is wrong, and letting manual probes write selection evidence also lets a
// user grow that evidence without bound by testing arbitrary URLs.
//
// The layers were previously kept in step on every store, which is what made a manual probe against
// an arbitrary URL land in the health map.
type HistoryStorage struct {
	access sync.RWMutex
	// notifyAccess serialises notification against teardown.
	//
	// Copying the hook list removes the data race, but not the lifecycle boundary: without this, a
	// notification could take its snapshot, Close could return, and the sends would then happen
	// after the caller had already released whatever consumes them. Holding this across Close makes
	// "Close has returned" mean "no further event can be produced".
	notifyAccess sync.Mutex

	// displayHistory is the most recent successful measurement per tag, for display.
	//
	// It is maintained by BOTH manual probes and automatic health checks, because both are a
	// legitimate "last successful measurement". It is never removed by a failure: a failed probe
	// against one URL says nothing about the last success against another.
	displayHistory map[string]displayRecord

	// healthHistory is per (tag, target) and is what selection and skipping read.
	//
	// ONLY an automatic URLTest group health check may write or delete here. A manual diagnostic
	// against an arbitrary URL must never become selection evidence, and must never grow this map
	// without bound.
	healthHistory map[scopedHistoryKey]adapter.URLTestHistory

	// closed makes Close terminal. A measurement that finishes afterwards must not write.
	closed bool

	// tagCounts and scopeCounts count the evidence DROPS this storage has performed, scoped to what
	// the drop was actually about.
	//
	// A measurement captures the pair when its PROBE starts and may only store while both are
	// unchanged. The scope matters in both directions: retiring node A must not discard a valid
	// result for node B, and invalidating one target's health for a node must not invalidate another
	// target's measurement of it.
	//
	// # Why a count is not enough
	//
	// The tables are bounded (see maxEvidenceEpochEntries), so an entry can be RECLAIMED. Deleting
	// one used to be described as safe because "a stale probe holds a non-zero stamp", and that is
	// only true for a probe that started after its node was already dropped once. A probe that
	// started while the node had no record captures the ZERO stamp; after a drop and a reclaim the
	// record reads zero again and that probe's write is accepted, resurrecting evidence the drop
	// removed.
	//
	// The tables therefore live in an INCARNATION: reclaiming them moves epochIncarnation, and a
	// stamp carries the incarnation it was issued under. A stamp from before a reclaim cannot match
	// anything afterwards, whichever counter it happens to read, and the counts themselves only ever
	// grow within one incarnation - so a value is never reused while it can still be compared.
	tagCounts   map[string]uint32
	scopeCounts map[scopedHistoryKey]uint32
	// epochIncarnation names the current epoch book. It advances when the tables are reclaimed, and
	// it is what makes a reclaimed entry fail closed instead of matching the zero stamp again.
	epochIncarnation uint64

	updateHooks []*observable.Subscriber[struct{}]
}

// maxEvidenceEpochEntries bounds the two epoch tables TOGETHER. A device that replaces thousands of
// provider nodes must not grow them without limit; reclaiming them moves the incarnation, which is
// what keeps the reclamation safe (see epochIncarnation).
const maxEvidenceEpochEntries = 4096

// EvidenceOwnership is the authority a measurement holds to change one node's evidence.
//
// It is the probe's provenance, not the caller's: a caller that joined a probe receives the LEADER's
// ownership, so a drop that happened after the probe started invalidates the result even for a
// caller that arrived afterwards.
type EvidenceOwnership struct {
	// TagEpoch and ScopeEpoch are the stamps the probe was requested under (see EvidenceStamp).
	TagEpoch   uint64
	ScopeEpoch uint64
	// StillCurrent, when set, is evaluated INSIDE the storage lock immediately before the change.
	//
	// It is what closes the window between "the check said yes" and "the write happened": a state
	// transition that lands in between (a network reset, whose generation this callback reads) is
	// observed by the write itself rather than by a check that already passed. It must be a cheap,
	// non-blocking read of a value the caller already owns - never a call back into this storage.
	StillCurrent func() bool
}

// authorizesLocked reports whether this ownership still holds. Caller holds the storage lock.
func (o EvidenceOwnership) authorizesLocked(storage *HistoryStorage, tag string, scope MeasurementScope) bool {
	if storage.tagStampLocked(tag) != o.TagEpoch || storage.scopeStampLocked(tag, scope) != o.ScopeEpoch {
		return false
	}
	if o.StillCurrent != nil && !o.StillCurrent() {
		return false
	}
	return true
}

// EvidenceStillOwned reports whether a published result still describes the current state.
//
// It is the CONVERGENCE half of the publication contract: a result can be authorized when its probe
// writes and invalidated afterwards (the node is dropped while another member's probe is still in
// flight). A caller that is about to report or act on a result it published earlier re-checks the
// same provenance through this call, so a round cannot converge with a member the drop removed.
func (s *HistoryStorage) EvidenceStillOwned(tag string, scope MeasurementScope, ownership EvidenceOwnership) bool {
	if s == nil {
		return false
	}
	s.access.RLock()
	if s.closed {
		s.access.RUnlock()
		return false
	}
	owned := ownership.authorizesLocked(s, tag, scope)
	s.access.RUnlock()
	return owned
}

// CommitAgainstEvidence runs commit under the storage lock, and only while (tag, scope) still owns
// its health evidence: the record must exist, and when ownership is given the tag/scope stamps and
// the network reset generation must be unchanged.
//
// # Why the check and the commit share the lock
//
// The selection commit is the last consumer of a round's evidence. Checking validity and then
// changing the selection in a separate step leaves exactly the window a drop needs to land in
// between - the reviewed failure was a selection moving to a member whose evidence had just been
// removed. The caller's commit therefore runs INSIDE this storage's critical section.
//
// The callback must not call back into this storage (it runs while the write lock is held) and must
// be short: it exists to publish a decision the caller already computed.
func (s *HistoryStorage) CommitAgainstEvidence(tag string, scope MeasurementScope, ownership *EvidenceOwnership, commit func()) bool {
	if s == nil || commit == nil {
		return false
	}
	s.access.Lock()
	if s.closed {
		s.access.Unlock()
		return false
	}
	if _, loaded := s.healthHistory[scopedHistoryKey{Tag: tag, Scope: scope}]; !loaded {
		s.access.Unlock()
		return false
	}
	if ownership != nil && !ownership.authorizesLocked(s, tag, scope) {
		s.access.Unlock()
		return false
	}
	commit()
	s.access.Unlock()
	return true
}

func NewHistoryStorage() *HistoryStorage {
	return &HistoryStorage{
		displayHistory: make(map[string]displayRecord),
		healthHistory:  make(map[scopedHistoryKey]adapter.URLTestHistory),
		tagCounts:      make(map[string]uint32),
		scopeCounts:    make(map[scopedHistoryKey]uint32),
	}
}

func (s *HistoryStorage) AddUpdateHook(hook *observable.Subscriber[struct{}]) {
	s.access.Lock()
	defer s.access.Unlock()
	if s.closed {
		return
	}
	s.updateHooks = append(s.updateHooks, hook)
}

func (s *HistoryStorage) NotifyUpdated() {
	s.notifyUpdated()
}

// LoadURLTestHistory returns the tag's most recent successful measurement, whatever target
// produced it.
//
// This is the DISPLAY accessor: a node list and the Clash UI want "the last result for this node",
// and a result measured against any target answers that. It must not be used for selection.
func (s *HistoryStorage) LoadURLTestHistory(tag string) *adapter.URLTestHistory {
	if s == nil {
		return nil
	}
	s.access.RLock()
	defer s.access.RUnlock()
	record, loaded := s.displayHistory[tag]
	if !loaded {
		return nil
	}
	// A copy: the caller must not be able to mutate stored state through the pointer.
	history := record.History
	return &history
}

// LoadURLTestHistoryFor returns the tag's HEALTH measurement for one specific target.
//
// Selection, interval skipping and tolerance must use this: a result against a different target
// says nothing about the target being tested.
func (s *HistoryStorage) LoadURLTestHistoryFor(tag string, scope MeasurementScope) *adapter.URLTestHistory {
	if s == nil {
		return nil
	}
	s.access.RLock()
	defer s.access.RUnlock()
	if s.closed {
		return nil
	}
	history, loaded := s.healthHistory[scopedHistoryKey{Tag: tag, Scope: scope}]
	if !loaded {
		return nil
	}
	result := history
	return &result
}

// StoreDisplayHistory records a manual measurement for display only.
//
// A manual probe answers "how fast is this node against the URL I asked for". That is a useful thing
// to show and must NOT become selection evidence: it was not measured against the group's target,
// and letting it accumulate would also let a user grow the health map without bound by testing
// arbitrary URLs.
//
// A failure is simply not recorded. It must never remove the previous success, because failing URL B
// does not disprove a success against URL A.
func (s *HistoryStorage) StoreDisplayHistory(tag string, scope MeasurementScope, history *adapter.URLTestHistory) {
	if s == nil || history == nil {
		return
	}
	s.access.Lock()
	if s.closed {
		s.access.Unlock()
		return
	}
	s.storeDisplayLocked(tag, scope, history)
	s.access.Unlock()
	s.notifyUpdated()
}

// storeDisplayLocked writes one display record, rejecting older evidence. Caller holds the lock.
func (s *HistoryStorage) storeDisplayLocked(tag string, scope MeasurementScope, history *adapter.URLTestHistory) {
	if existing, loaded := s.displayHistory[tag]; loaded && existing.History.Time.After(history.Time) {
		// Older evidence for the same node: the newer entry wins.
		//
		// An attempt is stamped with the moment it STARTED, so a probe that was already running
		// while a newer one ran and finished cannot describe the node as it is now. Without this a
		// slow probe - the one most likely to be measuring a path that has since changed - would
		// be the last writer and would undo the newer measurement.
		return
	}
	s.displayHistory[tag] = displayRecord{History: *history, Scope: scope}
}

// StoreHealthHistory records an automatic health measurement, in both layers.
//
// The health layer is what the group selects from. The display layer is updated too, because the
// node has just been measured and that is by definition the most recent thing known about it.
//
// Both layers reject a result that is older than what they already hold (see isNewerEvidence), so
// an attempt cannot overwrite evidence produced by an attempt that started later.
func (s *HistoryStorage) StoreHealthHistory(tag string, scope MeasurementScope, history *adapter.URLTestHistory) {
	if s == nil || history == nil {
		return
	}
	s.access.Lock()
	if s.closed {
		s.access.Unlock()
		return
	}
	healthUpdated, displayUpdated := s.storeHealthLocked(tag, scope, history)
	s.access.Unlock()
	if healthUpdated || displayUpdated {
		s.notifyUpdated()
	}
}

// EvidenceStamp reports the current drop stamps for one (node, target) pair.
//
// A measurement captures this pair when its PROBE starts and passes it back, with the storage
// write, through EvidenceOwnership. A result whose stamps are stale belongs to evidence that a drop
// had already forgotten, even when the caller joined the probe afterwards.
func (s *HistoryStorage) EvidenceStamp(tag string, scope MeasurementScope) (tagEpoch uint64, scopeEpoch uint64) {
	if s == nil {
		return 0, 0
	}
	s.access.RLock()
	defer s.access.RUnlock()
	return s.tagStampLocked(tag), s.scopeStampLocked(tag, scope)
}

// tagStampLocked renders one node's stamp. Caller holds the storage lock.
func (s *HistoryStorage) tagStampLocked(tag string) uint64 {
	return s.epochIncarnation<<32 | uint64(s.tagCounts[tag])
}

// scopeStampLocked renders one (node, target) stamp. Caller holds the storage lock.
func (s *HistoryStorage) scopeStampLocked(tag string, scope MeasurementScope) uint64 {
	return s.epochIncarnation<<32 | uint64(s.scopeCounts[scopedHistoryKey{Tag: tag, Scope: scope}])
}

// evidenceEntryCountLocked reports how many epoch records exist across both tables.
func (s *HistoryStorage) evidenceEntryCountLocked() int {
	return len(s.tagCounts) + len(s.scopeCounts)
}

// reclaimEpochBookLocked drops every epoch record and opens a new incarnation.
//
// Reclaiming the tables wholesale is what makes the reclamation safe: a per-entry eviction leaves a
// probe that captured the zero stamp able to match the zero stamp again. The cost is that a
// measurement in flight at the moment of reclamation loses its right to publish even when its own
// node was not the reason for the reclaim - the failure-closed direction, and a rare event (the
// tables hold thousands of entries).
func (s *HistoryStorage) reclaimEpochBookLocked() {
	// 32 bits of incarnation. Wrapping is unreachable by construction: each step needs a full table
	// of drops, i.e. 2^32 * 4096 recorded drops. The wrap is written down anyway so the value can
	// never return to 0, which is the one value a probe could have captured before any reclaim.
	if s.epochIncarnation >= math.MaxUint32 {
		s.epochIncarnation = 1
	} else {
		s.epochIncarnation++
	}
	clear(s.tagCounts)
	clear(s.scopeCounts)
}

// bumpTagEpochLocked records a node-level evidence drop. Caller holds the write lock.
func (s *HistoryStorage) bumpTagEpochLocked(tag string) {
	if s.evidenceEntryCountLocked() >= maxEvidenceEpochEntries || s.tagCounts[tag] == math.MaxUint32 {
		s.reclaimEpochBookLocked()
	}
	s.tagCounts[tag]++
}

// bumpScopeEpochLocked records a (node, target) evidence drop. Caller holds the write lock.
func (s *HistoryStorage) bumpScopeEpochLocked(tag string, scope MeasurementScope) {
	key := scopedHistoryKey{Tag: tag, Scope: scope}
	if s.evidenceEntryCountLocked() >= maxEvidenceEpochEntries || s.scopeCounts[key] == math.MaxUint32 {
		s.reclaimEpochBookLocked()
	}
	s.scopeCounts[key]++
}

// StoreHealthHistoryOwned stores a health measurement only while the probe's ownership still holds.
//
// The ownership check and the write happen under the SAME lock, and the ownership's StillCurrent
// predicate is evaluated inside that lock immediately before the write. That is what stops a state
// transition - a network reset - from landing between a check that said "current" and the write it
// was meant to invalidate.
//
// It reports whether the measurement was AUTHORIZED, not whether the write changed the stored value:
// a result that is older than evidence already stored is authorized but loses on ordering. Those are
// different facts, and the caller needs the first one to decide whether to report the result at all.
func (s *HistoryStorage) StoreHealthHistoryOwned(tag string, scope MeasurementScope, history *adapter.URLTestHistory, ownership EvidenceOwnership) bool {
	if s == nil || history == nil {
		return false
	}
	s.access.Lock()
	if s.closed {
		s.access.Unlock()
		return false
	}
	if !ownership.authorizesLocked(s, tag, scope) {
		s.access.Unlock()
		return false
	}
	healthUpdated, displayUpdated := s.storeHealthLocked(tag, scope, history)
	s.access.Unlock()
	if healthUpdated || displayUpdated {
		s.notifyUpdated()
	}
	return true
}

// storeHealthLocked writes one health measurement into BOTH layers. Caller holds the write lock.
func (s *HistoryStorage) storeHealthLocked(tag string, scope MeasurementScope, history *adapter.URLTestHistory) (healthUpdated bool, displayUpdated bool) {
	if existing, loaded := s.healthHistory[scopedHistoryKey{Tag: tag, Scope: scope}]; !loaded || isNewerEvidence(history.Time, existing.Time) {
		s.healthHistory[scopedHistoryKey{Tag: tag, Scope: scope}] = *history
		healthUpdated = true
	}
	if existing, loaded := s.displayHistory[tag]; !loaded || isNewerEvidence(history.Time, existing.History.Time) {
		s.displayHistory[tag] = displayRecord{History: *history, Scope: scope}
		displayUpdated = true
	}
	return
}

// StoreDisplayHistoryOwned is the display-layer counterpart: a manual probe that started before an
// evidence drop must not recreate the entry the drop removed.
func (s *HistoryStorage) StoreDisplayHistoryOwned(tag string, scope MeasurementScope, history *adapter.URLTestHistory, ownership EvidenceOwnership) bool {
	if s == nil || history == nil {
		return false
	}
	s.access.Lock()
	if s.closed {
		s.access.Unlock()
		return false
	}
	if !ownership.authorizesLocked(s, tag, scope) {
		s.access.Unlock()
		return false
	}
	s.storeDisplayLocked(tag, scope, history)
	s.access.Unlock()
	s.notifyUpdated()
	return true
}

// isNewerEvidence reports whether a measurement is newer evidence than the one already stored.
//
// A result describes the path as it was while the probe ran, so the ordering fact is when the
// attempt STARTED, not when it finished: a probe that started earlier cannot produce fresher
// evidence than one that started later, however much later it completed. Equal timestamps are
// accepted - a coarser platform clock can return the same instant for two attempts, and dropping
// the second one would silently lose a real measurement - so "older" is strictly older.
func isNewerEvidence(candidate, stored time.Time) bool {
	return !stored.After(candidate)
}

// DeleteHealthHistory removes one target's health measurement for a tag.
//
// It deliberately does NOT touch the display entry. The layers are independent: a failed health
// check against this group's target is not a reason to stop showing the node's last successful
// measurement, which may well have been against another target.
func (s *HistoryStorage) DeleteHealthHistory(tag string, scope MeasurementScope) {
	if s == nil {
		return
	}
	s.access.Lock()
	if s.closed {
		s.access.Unlock()
		return
	}
	delete(s.healthHistory, scopedHistoryKey{Tag: tag, Scope: scope})
	// This is an evidence DROP for THIS node and target, not the failure path of one attempt: an
	// in-flight probe for them that started before it must not store its result afterwards. Other
	// targets of the same node are untouched on purpose.
	s.bumpScopeEpochLocked(tag, scope)
	s.access.Unlock()
	s.notifyUpdated()
}

// DeleteHealthHistoryOlderThanOwned removes one failed attempt's health entry, unless newer evidence
// has been stored since that attempt started or the attempt no longer owns the evidence.
//
// # Why a failure needs an ordering too
//
// A failed probe is evidence that the node was unusable while the attempt ran. It is not evidence
// about a LATER attempt: a probe that started before a network change and failed after it says
// nothing about the new network, and deleting the entry the newer attempt just wrote would remove
// correct evidence - and, because the group selects from this layer, could move the selection on
// the strength of a path that no longer exists.
//
// # Why it needs the ownership as well
//
// Ordering alone compares clock readings, and a failure from the PREVIOUS network can carry a later
// timestamp than evidence stored on the new one (coarse clocks, or a probe that started later and
// failed). The ownership check is what makes "this failure describes the current state" a fact
// rather than an inference from time.
//
// It reports whether this failure is the CURRENT verdict for the (node, target): true when the
// attempt owns the evidence and no newer result has been stored since it started. That is true even
// when there was nothing to delete - a member that has already failed must be able to fail again on
// the next round - and false when a newer success supersedes it or the attempt no longer owns the
// evidence.
func (s *HistoryStorage) DeleteHealthHistoryOlderThanOwned(tag string, scope MeasurementScope, attemptStart time.Time, ownership EvidenceOwnership) bool {
	if s == nil {
		return false
	}
	s.access.Lock()
	if s.closed {
		s.access.Unlock()
		return false
	}
	if !ownership.authorizesLocked(s, tag, scope) {
		s.access.Unlock()
		return false
	}
	key := scopedHistoryKey{Tag: tag, Scope: scope}
	if existing, loaded := s.healthHistory[key]; loaded && existing.Time.After(attemptStart) {
		s.access.Unlock()
		return false
	}
	_, loaded := s.healthHistory[key]
	delete(s.healthHistory, key)
	s.access.Unlock()
	if loaded {
		s.notifyUpdated()
	}
	return true
}

// DeleteURLTestHistory removes every measurement for a tag, in both layers.
//
// Used when the NODE is gone rather than when one target failed.
func (s *HistoryStorage) DeleteURLTestHistory(tag string) {
	if s == nil {
		return
	}
	s.access.Lock()
	if s.closed {
		s.access.Unlock()
		return
	}
	delete(s.displayHistory, tag)
	for key := range s.healthHistory {
		if key.Tag == tag {
			delete(s.healthHistory, key)
		}
	}
	// The node is gone or its evidence was dropped: any probe for this node that started before
	// this point describes a member the drop was meant to forget. A probe for a DIFFERENT node is
	// unaffected - that is why the epoch is per node rather than per storage.
	s.bumpTagEpochLocked(tag)
	s.access.Unlock()
	s.notifyUpdated()
}

// DeleteURLTestHistoryFor removes one target's HEALTH measurement.
//
// Retained as the health-layer name used by the measurement paths. The display layer is untouched:
// that is the whole point of the split, and it is what removes the need to re-point the display
// entry at some other scope when this one is deleted.
func (s *HistoryStorage) DeleteURLTestHistoryFor(tag string, scope MeasurementScope) {
	s.DeleteHealthHistory(tag, scope)
}

// StoreURLTestHistoryFor records a HEALTH measurement.
//
// Retained as the health-layer name used by the automatic group path. Use StoreDisplayHistory for a
// manual probe, which must not write selection evidence.
func (s *HistoryStorage) StoreURLTestHistoryFor(tag string, scope MeasurementScope, history *adapter.URLTestHistory) {
	s.StoreHealthHistory(tag, scope, history)
}

// StoreURLTestHistory records a health measurement against the default target.
//
// Kept for callers that measure the default target only.
func (s *HistoryStorage) StoreURLTestHistory(tag string, history *adapter.URLTestHistory) {
	scope, err := NewMeasurementScope(DefaultURLTestURL, nil)
	if err != nil {
		// The default target is a compile-time constant; if it ever fails to normalise the
		// storage would silently drop results, so it is recorded rather than swallowed.
		panic(err)
	}
	s.StoreHealthHistory(tag, scope, history)
}

// HealthEntryCount reports how many health entries are stored.
//
// It exists so a test can assert that manual probes do not grow the health map, as a fact rather
// than by inference.
func (s *HistoryStorage) HealthEntryCount() int {
	if s == nil {
		return 0
	}
	s.access.RLock()
	defer s.access.RUnlock()
	return len(s.healthHistory)
}

// DisplayEntryCount reports how many display entries are stored.
func (s *HistoryStorage) DisplayEntryCount() int {
	if s == nil {
		return 0
	}
	s.access.RLock()
	defer s.access.RUnlock()
	return len(s.displayHistory)
}

// notifyUpdated delivers one event to every registered hook.
//
// # Synchronisation
//
// The hook list is COPIED while holding the storage lock, and the sends happen after releasing it.
// Both halves are required:
//
//   - Copying is what removes the race. The list is appended to by AddUpdateHook and dropped by
//     Close, so walking it unlocked reads a slice header and backing array that another goroutine
//     writes.
//   - Sending outside the storage lock is what keeps this callable from a context that already
//     holds it, and keeps a slow consumer from blocking every other storage operation.
//
// notifyAccess is held across both, so Close cannot return while an event is still being delivered.
func (s *HistoryStorage) notifyUpdated() {
	s.notifyAccess.Lock()
	defer s.notifyAccess.Unlock()

	s.access.RLock()
	if s.closed {
		s.access.RUnlock()
		return
	}
	hooks := make([]*observable.Subscriber[struct{}], len(s.updateHooks))
	copy(hooks, s.updateHooks)
	s.access.RUnlock()

	for _, updateHook := range hooks {
		updateHook.Emit(struct{}{})
	}
}

// Close makes the storage terminal.
//
// Every later operation becomes a no-op, including a measurement that was already in flight when
// Close ran: its result is discarded rather than written into a storage that no longer exists.
func (s *HistoryStorage) Close() error {
	// Take the notification lock first, so Close blocks until any in-flight delivery finishes and
	// no new one can start afterwards.
	s.notifyAccess.Lock()
	defer s.notifyAccess.Unlock()

	s.access.Lock()
	defer s.access.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	// Release everything. A closed storage holds no measurements and notifies nobody; keeping the
	// maps would let a late writer - or a reader that raced Close - still observe live state.
	clear(s.displayHistory)
	clear(s.healthHistory)
	// The epoch book goes too. It carries no measurement, but a closed storage must not keep a
	// growing record of drops either, and a late writer is already refused by s.closed before any
	// stamp is compared.
	clear(s.tagCounts)
	clear(s.scopeCounts)
	s.updateHooks = nil
	return nil
}
