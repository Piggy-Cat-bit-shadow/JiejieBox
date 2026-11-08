package oomkiller

import (
	"sync"
	"time"
)

// reclaimActionRingSize bounds the per-action history kept for one report.
//
// # Why a ring and not a log
//
// The OOM report's metadata document is rewritten on every event, so an append-only list of reclaim
// actions would grow with the session and eventually cost more to serialise than the reclaim itself.
// The actions that CHANGE state already go to events.jsonl (start/stop/state/reset/snapshot); what a
// reader of one report needs from this structure is the recent shape - which layer ran, what it was
// credited with, whether it cooled down - plus totals. Eight is enough to see a cache/pool/runtime
// ladder and a few repeated passes, and it is a constant.
const reclaimActionRingSize = 8

// reclaimActionRecord is one completed reclaim action as measured by the executor that ran it.
//
// # What the numbers are, and what they are not
//
// Before and After are PROCESS FOOTPRINT samples taken immediately around the action (memory.Total()
// where the platform provides it), not allocations attributed to it. Between the two reads the
// runtime may collect, another subsystem may allocate, and the OS may reclaim pages, so GainBytes is
// the footprint the action is CREDITED with - the same quantity the executor's own low-yield rule
// uses - not bytes it provably freed. Protocol windows and candidate weights are never reported here
// as if they were RSS.
//
// CacheEntriesRemoved is the one field that is a real count: it is what the L1 capability reported it
// dropped (adapter.CacheMemoryReleaser.TrimCaches). CacheEntriesKnown exists because "this layer does
// not count entries" and "this layer removed none" are different facts, and a JSON zero for both
// would be a lie.
//
// The pool layer has no equivalent count today: adapter.Router.TrimIdleResources and
// adapter.NetworkManager.TrimMemory return nothing, so an idle pool pass can only be measured by
// footprint. A real per-resource termination count belongs to the residency ledger (the owner that
// will close individual resources and can say so); this record must not guess one.
type reclaimActionRecord struct {
	// Kind is the executor branch that ran: "idle", "pressure" or "crisis".
	Kind string `json:"kind"`
	// Stage is the layer inside that branch: "cache", "pools", "runtime", "network-reset" or
	// "report-only". The parenthetical "(crisis pending)" forms keep the reason the ladder stopped.
	Stage string `json:"stage"`
	// At is when the action finished, read through the timer's clock seam.
	At time.Time `json:"at"`
	// BeforeBytes and AfterBytes are footprint samples around the action.
	BeforeBytes uint64 `json:"beforeBytes"`
	AfterBytes  uint64 `json:"afterBytes"`
	// GainBytes is BeforeBytes - AfterBytes, floored at zero.
	GainBytes uint64 `json:"gainBytes"`
	// CacheEntriesRemoved is the L1 drop count, valid only when CacheEntriesKnown is true.
	CacheEntriesRemoved int  `json:"cacheEntriesRemoved"`
	CacheEntriesKnown   bool `json:"cacheEntriesKnown"`
	// LowYield records the executor's decision that this action did not reach the benefit floor,
	// which is what put the idle path into its cooldown.
	LowYield bool `json:"lowYield"`
	// CooldownUntil is set only when this action triggered the low-yield cooldown.
	CooldownUntil time.Time `json:"cooldownUntil,omitempty"`
	// Mode records the policy mode the sample was taken under ("network-extension",
	// "memory-limit", "available" or "none"), because the same bytes mean different things under
	// a fixed limit and under an available-memory policy.
	Mode string `json:"mode"`
}

// reclaimObserver aggregates what the reclaim executor actually did, for one process.
//
// # Ownership
//
// It owns no policy, no timer, no goroutine and no sample. The executor still decides when to run and
// whether to cool down; this type only remembers the decisions and their measured outcome, and is
// read at report-write time. That split is what keeps it from becoming a second reclaim control
// loop: nothing here can start, stop or reschedule a pass.
//
// Zero value is not usable for recording (it is nil-safe instead); construct with
// newReclaimObserver, which is what the owning Service does once.
type reclaimObserver struct {
	access sync.Mutex

	actions         uint64
	idleActions     uint64
	pressureActions uint64
	crisisActions   uint64
	lowYieldActions uint64
	totalGainBytes  uint64

	ring     [reclaimActionRingSize]reclaimActionRecord
	ringNext int
	ringLen  int

	lastCooldownUntil time.Time
}

func newReclaimObserver() *reclaimObserver {
	return &reclaimObserver{}
}

// reclaimSnapshot is a consistent point-in-time view of the observer, oldest action first.
type reclaimSnapshot struct {
	Actions         uint64
	IdleActions     uint64
	PressureActions uint64
	CrisisActions   uint64
	LowYieldActions uint64
	TotalGainBytes  uint64

	CoolingDown       bool
	CooldownRemaining time.Duration

	Recent []reclaimActionRecord
}

// record stores one completed action and updates the totals. It is nil-safe so a test fixture that
// drives the executor directly needs no observer.
func (o *reclaimObserver) record(action reclaimActionRecord) {
	if o == nil {
		return
	}
	o.access.Lock()
	defer o.access.Unlock()
	o.actions++
	switch action.Kind {
	case "idle":
		o.idleActions++
	case "pressure":
		o.pressureActions++
	case "crisis":
		o.crisisActions++
	}
	if action.LowYield {
		o.lowYieldActions++
	}
	o.totalGainBytes += action.GainBytes
	o.ring[o.ringNext] = action
	o.ringNext = (o.ringNext + 1) % reclaimActionRingSize
	if o.ringLen < reclaimActionRingSize {
		o.ringLen++
	}
	if action.CooldownUntil.After(o.lastCooldownUntil) {
		o.lastCooldownUntil = action.CooldownUntil
	}
}

// snapshot copies the totals and the ring in chronological order. The copy happens under the lock so
// a report cannot show a half-written ring; the returned slice is the caller's.
func (o *reclaimObserver) snapshot(now time.Time) reclaimSnapshot {
	if o == nil {
		return reclaimSnapshot{}
	}
	o.access.Lock()
	defer o.access.Unlock()
	snapshot := reclaimSnapshot{
		Actions:         o.actions,
		IdleActions:     o.idleActions,
		PressureActions: o.pressureActions,
		CrisisActions:   o.crisisActions,
		LowYieldActions: o.lowYieldActions,
		TotalGainBytes:  o.totalGainBytes,
	}
	snapshot.Recent = make([]reclaimActionRecord, 0, o.ringLen)
	start := o.ringNext - o.ringLen
	if start < 0 {
		start += reclaimActionRingSize
	}
	for index := 0; index < o.ringLen; index++ {
		snapshot.Recent = append(snapshot.Recent, o.ring[(start+index)%reclaimActionRingSize])
	}
	if !o.lastCooldownUntil.IsZero() && now.Before(o.lastCooldownUntil) {
		snapshot.CoolingDown = true
		snapshot.CooldownRemaining = o.lastCooldownUntil.Sub(now)
	}
	return snapshot
}

// reclaimMetadata renders the snapshot as the top-level object the Recorder writes under
// "memoryReclaim".
//
// # Why a map and not a struct
//
// The report schema belongs to the client (experimental/libbox), which does not know about this
// package's reclaim executor. Recorder.SetExtraMetadata splices top-level keys next to the client's
// document, and an unknown key is ignored by every existing reader - the same additive rule the
// admission guard's "resourceGuard" key follows. A struct with tags here would be a second schema to
// keep in step with the first.
func (o *reclaimObserver) metadata(now time.Time) any {
	if o == nil {
		return nil
	}
	snapshot := o.snapshot(now)
	recent := make([]any, 0, len(snapshot.Recent))
	for _, action := range snapshot.Recent {
		entry := map[string]any{
			"kind":        action.Kind,
			"stage":       action.Stage,
			"at":          action.At.UTC().Format(time.RFC3339Nano),
			"beforeBytes": action.BeforeBytes,
			"afterBytes":  action.AfterBytes,
			"gainBytes":   action.GainBytes,
			"lowYield":    action.LowYield,
			"mode":        action.Mode,
		}
		if action.CacheEntriesKnown {
			entry["cacheEntriesRemoved"] = action.CacheEntriesRemoved
		}
		if !action.CooldownUntil.IsZero() {
			entry["cooldownUntil"] = action.CooldownUntil.UTC().Format(time.RFC3339Nano)
		}
		recent = append(recent, entry)
	}
	document := map[string]any{
		"actions":         snapshot.Actions,
		"idleActions":     snapshot.IdleActions,
		"pressureActions": snapshot.PressureActions,
		"crisisActions":   snapshot.CrisisActions,
		"lowYieldActions": snapshot.LowYieldActions,
		"totalGainBytes":  snapshot.TotalGainBytes,
		"coolingDown":     snapshot.CoolingDown,
		"recent":          recent,
	}
	if snapshot.CoolingDown {
		document["cooldownRemainingMs"] = snapshot.CooldownRemaining.Milliseconds()
	}
	return document
}
