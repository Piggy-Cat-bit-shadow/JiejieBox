package dns

// O03 — the production side of the DNS history: a subscription and a management entry.
//
// History and EventBus are bounded components with their own contracts, but nothing in the product
// ever created them: the bus had no producer-facing owner and the history had no subscriber, so the
// only evidence was component tests. This file is the entry the daemon and the client APIs reach.
//
// Three decisions are deliberate:
//
//   - The bus is ALWAYS created, the history is NOT. The bus costs one atomic check per query when
//     nobody is subscribed (that is why events are assembled only when Active), so creating it
//     unconditionally is free. The history costs one goroutine, an allocation per query and a
//     bounded ring, so a process that never asks for history pays nothing for it.
//   - Enabling is IDEMPOTENT. Two consumers asking for history must share one ring and one
//     subscription; a second subscription would double the goroutines and split the records.
//   - Disabling DETACHES. The drain goroutine belongs to the subscription, so a box that stops
//     asking for history must not keep consuming events, and a closed box must not keep a
//     subscriber that outlives it.

import (
	"sync"

	E "github.com/sagernet/sing/common/exceptions"
)

// ErrHistoryNotEnabled reports that no history is attached to this router.
//
// It is distinct from an empty query result: a quiet window and a disabled feature must not look
// the same to the caller trying to explain a query storm.
var ErrHistoryNotEnabled = E.New("dns history: not enabled")

// historyAccessor is the management entry point a diagnostic consumer uses.
//
// It is a separate interface so a caller can discover the capability instead of assuming every
// build has it, and so the generated client surface can bind one method instead of a whole router.
type historyAccessor interface {
	EnableHistory(options HistoryOptions) *History
	DisableHistory()
	DNSHistory() *History
}

// historyState is the router's ownership of the optional history.
type historyState struct {
	access sync.Mutex
	// history is non-nil exactly while a subscription is attached.
	history *History
	detach  func()
}

// EnableHistory attaches the router's query events to a bounded history and returns it.
//
// The history is created on first use and returned unchanged afterwards: a second caller shares the
// ring rather than replacing it. Records are bounded by HistoryOptions (entries, bytes, TTL) - see
// the defaults in history.go - so enabling this cannot grow without limit.
func (r *Router) EnableHistory(options HistoryOptions) *History {
	r.historyState.access.Lock()
	defer r.historyState.access.Unlock()
	if r.historyState.history != nil {
		return r.historyState.history
	}
	history := NewHistory(options)
	r.historyState.history = history
	r.historyState.detach = history.Subscribe(r.events)
	return history
}

// DisableHistory detaches the subscription and drops the ring.
//
// It is the release half of EnableHistory: after it returns the bus has one fewer subscriber, the
// drain goroutine is stopping, and a box that has just been asked to stop reporting does not keep
// consuming events.
func (r *Router) DisableHistory() {
	r.historyState.access.Lock()
	detach := r.historyState.detach
	r.historyState.detach = nil
	r.historyState.history = nil
	r.historyState.access.Unlock()
	if detach != nil {
		detach()
	}
}

// DNSHistory returns the live history, or nil when history is not enabled.
func (r *Router) DNSHistory() *History {
	if r == nil {
		return nil
	}
	r.historyState.access.Lock()
	defer r.historyState.access.Unlock()
	return r.historyState.history
}

// QueryHistory reads the live history. A nil history means the feature is off, which is reported
// as such rather than as an empty result: an empty list would be indistinguishable from a quiet
// window, and a caller trying to diagnose a query storm would draw the wrong conclusion.
func (r *Router) QueryHistory(query HistoryQuery) ([]HistoryRecord, error) {
	history := r.DNSHistory()
	if history == nil {
		return nil, ErrHistoryNotEnabled
	}
	return history.Query(query)
}

// HistoryTopK aggregates the live history.
func (r *Router) HistoryTopK(field TopField, k int) ([]TopEntry, error) {
	history := r.DNSHistory()
	if history == nil {
		return nil, ErrHistoryNotEnabled
	}
	return history.TopK(field, k), nil
}

// HistoryStats reports the live history's accounting, plus the bus-side drops.
//
// The drops are the fact a consumer cannot otherwise see: a subscriber that could not keep up loses
// events instead of blocking a query, and "the history is missing an entry" must be distinguishable
// from "the query never happened".
func (r *Router) HistoryStats() (HistoryStats, uint64, error) {
	history := r.DNSHistory()
	if history == nil {
		return HistoryStats{}, 0, ErrHistoryNotEnabled
	}
	return history.Stats(), r.events.Dropped(), nil
}

// closeHistory is the box-shutdown hook: the subscription belongs to the box, so it must not
// outlive it.
func (r *Router) closeHistory() error {
	r.DisableHistory()
	return nil
}
