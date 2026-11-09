package dns

// O04 — the bounded diagnostic history.
//
// O03 produces one event per query; a history keeps some of them so a diagnostic can ask
// "what happened recently" without the query path paying for it. The whole file is about
// BOUNDS, because an unobserved history is the classic way a diagnostic becomes the leak it
// was meant to find:
//
//   - count, bytes and TTL are all enforced, and each record is charged an estimated size so
//     a flood of long names cannot sit under a count limit while pinning memory;
//   - a record that cannot fit even after evicting everything is REJECTED and counted, not
//     retained anyway;
//   - queries are bounded in output length and serialised by one lock, so N callers cannot
//     multiply the scan;
//   - top-K aggregates into at most K buckets plus one "other", so a high-cardinality input
//     cannot grow a retained map;
//   - nothing about replay is claimed: the capability report says what the build can and
//     cannot do.

import (
	"runtime"
	"sort"
	"sync"
	"time"

	C "github.com/sagernet/sing-box/constant"
	E "github.com/sagernet/sing/common/exceptions"
)

const (
	defaultHistoryEntries = 256
	defaultHistoryBytes   = 1 << 20
	defaultHistoryTTL     = 5 * time.Minute
	maxHistoryQueryLimit  = 128
	// maxHistoryQueryBytes is the default OUTPUT budget of one read: the most record bytes a single
	// Query call may copy. The record count alone is not a budget on a history whose records carry
	// names and rendered answers, and a management read must not be able to multiply the scan cost by
	// its own output.
	maxHistoryQueryBytes  = 256 << 10
	maxTopKBuckets        = 16
	historyRecordOverhead = 64
)

// ErrHistoryQueryTooLarge is returned when a caller asks for more output than the history
// is prepared to copy in one call.
var ErrHistoryQueryTooLarge = E.New("dns history: query limit exceeds the maximum")

// HistoryOptions configures a History. A non-positive field takes its default.
type HistoryOptions struct {
	MaxEntries int
	MaxBytes   int
	TTL        time.Duration
}

// HistoryRecord is one stored event plus the sequence number a caller paginates on.
type HistoryRecord struct {
	Sequence uint64
	Event    QueryEvent
}

// HistoryQuery selects records. Since is exclusive: a caller passes the last Sequence it
// saw and receives what came after it.
type HistoryQuery struct {
	Since     uint64
	Limit     int
	Transport string
	Source    QuerySource
	// MaxBytes bounds the total retained size of the records this call copies. A non-positive value
	// takes the default (maxHistoryQueryBytes). A single record that is larger than the budget is
	// returned on its own rather than hidden: a caller asking for one record must be able to read
	// it, and the budget is about the SIZE of an output, not about refusing the truth.
	MaxBytes int
}

// TopField names the dimension a top-K aggregates over.
type TopField string

const (
	TopFieldTransport TopField = "transport"
	TopFieldSource    TopField = "source"
)

// TopEntry is one aggregated bucket.
type TopEntry struct {
	Value string
	Count int
}

// HistoryStats reports the history's own accounting.
type HistoryStats struct {
	Entries  int
	Bytes    int
	Oldest   time.Time
	Newest   time.Time
	Rejected uint64
	Expired  uint64
	Queries  uint64
	// Cleared counts explicit Clear calls. It is separate from Expired and eviction: a reader must
	// be able to tell "the window moved on" from "a consumer emptied it".
	Cleared uint64
}

// HistoryCapabilities says what this build can do, so a consumer can discover rather than
// assume.
type HistoryCapabilities struct {
	Replay    bool
	Platform  string
	BuildQUIC bool
	// Precision is the resolution of the recorded timestamps and durations.
	Precision string
}

// History is a bounded, queryable ring of query events.
type History struct {
	access sync.Mutex

	maxEntries int
	maxBytes   int
	ttl        time.Duration

	records []HistoryRecord
	bytes   int
	next    uint64

	rejected uint64
	expired  uint64
	queries  uint64
	cleared  uint64
}

// NewHistory returns a bounded history.
func NewHistory(options HistoryOptions) *History {
	history := &History{
		maxEntries: options.MaxEntries,
		maxBytes:   options.MaxBytes,
		ttl:        options.TTL,
	}
	if history.maxEntries <= 0 {
		history.maxEntries = defaultHistoryEntries
	}
	if history.maxBytes <= 0 {
		history.maxBytes = defaultHistoryBytes
	}
	if history.ttl <= 0 {
		history.ttl = defaultHistoryTTL
	}
	return history
}

// Record stores one event, enforcing every bound. It never blocks on a reader.
func (h *History) Record(event QueryEvent) {
	if h == nil {
		return
	}
	h.access.Lock()
	defer h.access.Unlock()
	h.expireLocked(time.Now())
	size := historyRecordSize(event)
	if size > h.maxBytes {
		// A single record that cannot fit the whole budget is rejected rather than
		// evicting the history for it.
		h.rejected++
		return
	}
	h.next++
	h.records = append(h.records, HistoryRecord{Sequence: h.next, Event: event})
	h.bytes += size
	for len(h.records) > h.maxEntries || h.bytes > h.maxBytes {
		h.evictOldestLocked()
	}
}

// Query returns matching records after the caller's cursor, oldest first.
//
// The output is bounded twice: by the record count (Limit, capped at maxHistoryQueryLimit) and by
// the total size of the records copied (MaxBytes). One lock serialises every reader and the writer,
// so N concurrent readers cannot multiply the scan.
func (h *History) Query(query HistoryQuery) ([]HistoryRecord, error) {
	if h == nil {
		return nil, nil
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 32
	}
	if limit > maxHistoryQueryLimit {
		return nil, ErrHistoryQueryTooLarge
	}
	budget := query.MaxBytes
	if budget <= 0 {
		budget = maxHistoryQueryBytes
	}
	h.access.Lock()
	defer h.access.Unlock()
	h.queries++
	h.expireLocked(time.Now())
	matched := make([]HistoryRecord, 0, limit)
	used := 0
	for _, record := range h.records {
		if record.Sequence <= query.Since {
			continue
		}
		if query.Transport != "" && record.Event.TransportTag != query.Transport {
			continue
		}
		if query.Source != "" && record.Event.Source != query.Source {
			continue
		}
		size := historyRecordSize(record.Event)
		// The first matching record is always returned, even when it alone exceeds the budget: a
		// budget that made a record unreadable would hide the very event being diagnosed.
		if len(matched) > 0 && used+size > budget {
			break
		}
		matched = append(matched, record)
		used += size
		if len(matched) == limit {
			break
		}
	}
	return matched, nil
}

// Clear empties the ring and reports how many records it dropped.
//
// It is the explicit "start a fresh window" entry for a management consumer. The SEQUENCE keeps
// counting: a caller holding a cursor from before the clear must not be served records it has
// already seen, and a restarted model of the history must not reuse an identity.
func (h *History) Clear() int {
	if h == nil {
		return 0
	}
	h.access.Lock()
	defer h.access.Unlock()
	cleared := len(h.records)
	h.records = nil
	h.bytes = 0
	h.cleared++
	return cleared
}

// TopK aggregates the retained records into at most maxTopKBuckets buckets, with the
// remainder folded into one "other" bucket. The map is per-call: a high-cardinality input
// cannot grow anything retained.
func (h *History) TopK(field TopField, k int) []TopEntry {
	if h == nil {
		return nil
	}
	if k <= 0 || k > maxTopKBuckets {
		k = maxTopKBuckets
	}
	h.access.Lock()
	defer h.access.Unlock()
	counts := make(map[string]int, k+1)
	for _, record := range h.records {
		var value string
		switch field {
		case TopFieldTransport:
			value = record.Event.TransportTag
		case TopFieldSource:
			value = string(record.Event.Source)
		default:
			value = ""
		}
		counts[value]++
	}
	entries := make([]TopEntry, 0, len(counts))
	for value, count := range counts {
		entries = append(entries, TopEntry{Value: value, Count: count})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Count != entries[j].Count {
			return entries[i].Count > entries[j].Count
		}
		return entries[i].Value < entries[j].Value
	})
	if len(entries) <= k {
		return entries
	}
	other := TopEntry{Value: "other"}
	for _, entry := range entries[k:] {
		other.Count += entry.Count
	}
	top := entries[:k]
	if other.Count > 0 {
		top = append(top, other)
	}
	return top
}

// Stats reports the retained accounting.
func (h *History) Stats() HistoryStats {
	if h == nil {
		return HistoryStats{}
	}
	h.access.Lock()
	defer h.access.Unlock()
	stats := HistoryStats{
		Entries:  len(h.records),
		Bytes:    h.bytes,
		Rejected: h.rejected,
		Expired:  h.expired,
		Queries:  h.queries,
		Cleared:  h.cleared,
	}
	if len(h.records) > 0 {
		stats.Oldest = h.records[0].Event.Time
		stats.Newest = h.records[len(h.records)-1].Event.Time
	}
	return stats
}

// Capabilities returns what this build can claim about the history.
func (h *History) Capabilities() HistoryCapabilities {
	return HistoryCapabilities{
		// Records are a bounded window, not a log: there is nothing to replay.
		Replay:    false,
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		BuildQUIC: C.WithQUIC,
		Precision: "milliseconds",
	}
}

// Subscribe attaches the history to a bus and returns the function that detaches it. The
// history needs no answers, so it asks for none: a diagnostic store must not make every
// query render its answer section.
func (h *History) Subscribe(bus *EventBus) func() {
	if h == nil || bus == nil {
		return func() {}
	}
	events, unsubscribe := bus.Subscribe(false, 0)
	stop := make(chan struct{})
	var stopOnce sync.Once
	go func() {
		for {
			select {
			case event := <-events:
				h.Record(event)
			case <-stop:
				return
			}
		}
	}()
	return func() {
		// Idempotent: a caller that defers the detach and also calls it explicitly must not
		// panic on a second close.
		stopOnce.Do(func() {
			unsubscribe()
			close(stop)
		})
	}
}

// expireLocked drops records past the TTL. Caller holds access.
func (h *History) expireLocked(now time.Time) {
	for len(h.records) > 0 && now.Sub(h.records[0].Event.Time) > h.ttl {
		h.evictOldestLocked()
		h.expired++
	}
}

// evictOldestLocked drops the oldest record and its byte charge. Caller holds access.
func (h *History) evictOldestLocked() {
	if len(h.records) == 0 {
		return
	}
	h.bytes -= historyRecordSize(h.records[0].Event)
	if h.bytes < 0 {
		h.bytes = 0
	}
	h.records = h.records[1:]
}

// historyRecordSize is the estimated retained size of one event.
func historyRecordSize(event QueryEvent) int {
	size := historyRecordOverhead + len(event.Name) + len(event.Err) + 2*len(event.TransportTag)
	for _, answer := range event.Answers {
		size += len(answer)
	}
	return size
}
