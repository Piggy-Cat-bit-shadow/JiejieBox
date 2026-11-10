package dns

// The presentation of the bounded query history on a management surface.
//
// It lives in this package, not in a binding, because there are two producers of the SAME document:
// gomobile (the command server, JSON over the binding) and the daemon's gRPC service (the same JSON
// inside a response message). Keeping the shaping here is what makes "the two transports agree" a
// fact rather than a promise - the hard cap, the clamping, the rune-safe cut and the pagination
// fields cannot drift between them.
//
// # Why retention accounting cannot size the output
//
// The ring charges RETAINED bytes; a binding hands the platform one JSON string, and escaping,
// field names, the envelope and the stats all add to it. A read sized by the retained estimate was
// therefore always under-sized: two records charged 154 bytes encoded to 80,485 bytes.
//
// MaxHistoryBindingBytes is the hard ceiling of one response; the caller's budget is applied INSIDE
// it (clamped to [min, max]). Per-field budgets bound one record even when it must be carried
// alone: the first record is always returned so the cursor advances, with its long text fields cut
// rune-safely and marked truncated.

import (
	stdjson "encoding/json"
)

const (
	// MaxHistoryBindingBytes is the server ceiling for one encoded response.
	MaxHistoryBindingBytes = 64 << 10
	// MinHistoryBindingBytes is the floor: below it a single record would not be readable, so
	// pagination could never make progress.
	MinHistoryBindingBytes = 1 << 10
	// MaxHistoryFieldBytes bounds one variable-length field by its ENCODED size.
	MaxHistoryFieldBytes = 4 << 10
	// MaxHistoryAnswersBytes bounds the encoded answers of one record.
	MaxHistoryAnswersBytes = 4 << 10
	// MaxHistoryAnswers bounds the number of answers of one record.
	MaxHistoryAnswers = 8
)

// HistoryRecordView is one record as a management surface presents it.
type HistoryRecordView struct {
	Sequence   uint64   `json:"sequence"`
	TimeMs     int64    `json:"timeMs"`
	Transport  string   `json:"transport,omitempty"`
	Name       string   `json:"name"`
	QType      uint16   `json:"qType"`
	Source     string   `json:"source"`
	RCode      int      `json:"rcode"`
	DurationMs int64    `json:"durationMs"`
	Shared     bool     `json:"shared,omitempty"`
	Final      bool     `json:"final,omitempty"`
	Rule       string   `json:"rule,omitempty"`
	Err        string   `json:"err,omitempty"`
	Answers    []string `json:"answers,omitempty"`
}

// HistoryStatsView is the history's accounting as a management surface presents it.
type HistoryStatsView struct {
	Entries  int    `json:"entries"`
	Bytes    int    `json:"bytes"`
	OldestMs int64  `json:"oldestMs,omitempty"`
	NewestMs int64  `json:"newestMs,omitempty"`
	Rejected uint64 `json:"rejected"`
	Expired  uint64 `json:"expired"`
	Queries  uint64 `json:"queries"`
	Cleared  uint64 `json:"cleared"`
	Dropped  uint64 `json:"dropped"`
}

// HistoryQueryView is the JSON body returned by a history query.
//
// Truncated and NextSequence are part of the pagination contract: when the response could not carry
// every selected record, `truncated` is true and `nextSequence` is the last sequence that WAS
// carried - passing it back as `since` continues the scan. A response that stopped without either
// would leave the caller unable to tell "nothing more" from "more than fits".
type HistoryQueryView struct {
	Records      []HistoryRecordView `json:"records"`
	Stats        HistoryStatsView    `json:"stats"`
	Truncated    bool                `json:"truncated,omitempty"`
	NextSequence uint64              `json:"nextSequence,omitempty"`
}

// HistoryStatsResultView is the JSON body returned by a stats request.
type HistoryStatsResultView struct {
	Stats HistoryStatsView `json:"stats"`
}

// HistoryTopKEntryView is one aggregated bucket.
type HistoryTopKEntryView struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// HistoryTopKResultView is the JSON body returned by a top-k request.
type HistoryTopKResultView struct {
	Field   string                 `json:"field"`
	Entries []HistoryTopKEntryView `json:"entries"`
}

// HistoryClearView is the JSON body returned by a clear request.
type HistoryClearView struct {
	Cleared int `json:"cleared"`
}

// HistoryEnableView is the JSON body returned by an enable request.
type HistoryEnableView struct {
	Enabled bool `json:"enabled"`
}

// HistoryStatsViewFor converts the history's accounting to its presentation.
func HistoryStatsViewFor(stats HistoryStats, dropped uint64) HistoryStatsView {
	fields := HistoryStatsView{
		Entries:  stats.Entries,
		Bytes:    stats.Bytes,
		Rejected: stats.Rejected,
		Expired:  stats.Expired,
		Queries:  stats.Queries,
		Cleared:  stats.Cleared,
		Dropped:  dropped,
	}
	if !stats.Oldest.IsZero() {
		fields.OldestMs = stats.Oldest.UnixMilli()
	}
	if !stats.Newest.IsZero() {
		fields.NewestMs = stats.Newest.UnixMilli()
	}
	return fields
}

// HistoryBindingBudget applies a caller's requested output budget inside the server's limits.
//
// The caller controls it, the server owns the floor and the ceiling: a request below the floor
// would make a single record unreadable (no pagination progress), and one above the ceiling would
// let a response exceed what the management surface is prepared to hand the platform.
func HistoryBindingBudget(requested int) int {
	if requested <= 0 {
		return MaxHistoryBindingBytes
	}
	if requested < MinHistoryBindingBytes {
		return MinHistoryBindingBytes
	}
	if requested > MaxHistoryBindingBytes {
		return MaxHistoryBindingBytes
	}
	return requested
}

// BuildHistoryQueryView converts a query's records and stats into the bounded presentation.
//
// The budget is measured on the growing ENCODED document, because that is what the caller receives:
// JSON escaping and the field names are part of it. A record is added only while the whole document
// fits; the first record is always carried so the cursor advances (its fields are already bounded,
// so the response stays under the server ceiling).
func BuildHistoryQueryView(records []HistoryRecord, stats HistoryStats, dropped uint64, requestedMaxBytes int) HistoryQueryView {
	result := HistoryQueryView{
		Records: make([]HistoryRecordView, 0, len(records)),
		Stats:   HistoryStatsViewFor(stats, dropped),
	}
	budget := HistoryBindingBudget(requestedMaxBytes)
	truncated := false
	for index, record := range records {
		presented, fieldCut := PresentHistoryRecord(record)
		candidate := HistoryQueryView{
			Records:      append(append([]HistoryRecordView(nil), result.Records...), presented),
			Stats:        result.Stats,
			Truncated:    truncated || fieldCut || index < len(records)-1,
			NextSequence: record.Sequence,
		}
		encoded, encodeErr := stdjson.Marshal(candidate)
		if encodeErr != nil {
			// These shapes cannot fail to marshal (no maps, channels or funcs); if it somehow
			// happens, stop with what was already carried rather than returning a broken document.
			truncated = true
			break
		}
		if len(encoded) > budget {
			if index == 0 {
				truncated = true
				result.Records = candidate.Records
				result.NextSequence = record.Sequence
			} else {
				truncated = true
			}
			break
		}
		result.Records = candidate.Records
		result.NextSequence = record.Sequence
		if fieldCut {
			truncated = true
		}
	}
	result.Truncated = truncated
	return result
}

// BuildHistoryStatsView converts the accounting for a stats response.
func BuildHistoryStatsView(stats HistoryStats, dropped uint64) HistoryStatsResultView {
	return HistoryStatsResultView{Stats: HistoryStatsViewFor(stats, dropped)}
}

// BuildHistoryTopKView converts aggregated entries for a top-k response.
func BuildHistoryTopKView(field TopField, entries []TopEntry) HistoryTopKResultView {
	result := HistoryTopKResultView{
		Field:   string(field),
		Entries: make([]HistoryTopKEntryView, 0, len(entries)),
	}
	for _, entry := range entries {
		result.Entries = append(result.Entries, HistoryTopKEntryView{Value: entry.Value, Count: entry.Count})
	}
	return result
}

// PresentHistoryRecord converts one record and bounds every variable field by its ENCODED size.
func PresentHistoryRecord(record HistoryRecord) (HistoryRecordView, bool) {
	event := record.Event
	truncated := false
	name, cut := TruncateJSONField(event.Name, MaxHistoryFieldBytes)
	truncated = truncated || cut
	transport, cut := TruncateJSONField(event.TransportTag, MaxHistoryFieldBytes)
	truncated = truncated || cut
	ruleText, cut := TruncateJSONField(event.Rule, MaxHistoryFieldBytes)
	truncated = truncated || cut
	errText, cut := TruncateJSONField(event.Err, MaxHistoryFieldBytes)
	truncated = truncated || cut
	presented := HistoryRecordView{
		Sequence:   record.Sequence,
		TimeMs:     event.Time.UnixMilli(),
		Transport:  transport,
		Name:       name,
		QType:      event.QType,
		Source:     string(event.Source),
		RCode:      event.RCode,
		DurationMs: event.DurationMs,
		Shared:     event.Shared,
		Final:      event.Final,
		Rule:       ruleText,
		Err:        errText,
	}
	if len(event.Answers) > 0 {
		answers := event.Answers
		if len(answers) > MaxHistoryAnswers {
			answers = answers[:MaxHistoryAnswers]
			truncated = true
		}
		budget := MaxHistoryAnswersBytes
		for _, answer := range answers {
			bounded, cut := TruncateJSONField(answer, MaxHistoryFieldBytes)
			if cut {
				truncated = true
			}
			if budget <= 0 {
				truncated = true
				break
			}
			if len(bounded) > budget {
				bounded, _ = TruncateJSONField(bounded, budget)
				truncated = true
			}
			presented.Answers = append(presented.Answers, bounded)
			budget -= len(bounded)
		}
	}
	return presented, truncated
}

// TruncateJSONField cuts value so that its ENCODED JSON form fits budget, and reports whether it
// was cut.
//
// The budget is on the encoded form, not on the input: a rune can become a six-byte `\uXXXX`
// escape, and an input-length check would let a field of control characters multiply the response.
// The cut is rune-safe and marked with an ellipsis so a reader can tell the text is incomplete.
func TruncateJSONField(value string, budget int) (string, bool) {
	if value == "" {
		return "", false
	}
	encoded, err := stdjson.Marshal(value)
	if err == nil && len(encoded) <= budget {
		return value, false
	}
	runes := []rune(value)
	low, high := 0, len(runes)
	for low < high {
		middle := (low + high + 1) / 2
		candidate, candidateErr := stdjson.Marshal(string(runes[:middle]))
		if candidateErr == nil && len(candidate)+len("\\u2026") <= budget {
			low = middle
		} else {
			high = middle - 1
		}
	}
	if low <= 0 {
		return "…", true
	}
	return string(runes[:low]) + "…", true
}
