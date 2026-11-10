package libbox

// The DNS history management surface on the command server.
//
// The bounded history and its router-level entry already exist (EnableHistory/QueryHistory/
// HistoryStats/ClearHistory). What was missing is a consumer surface, and there are two: the daemon
// gRPC service, whose methods need proto fields and generated code (integration window), and
// gomobile, which binds Go directly. This file is the gomobile registration: the same management
// calls, with JSON strings crossing the binding - the shape ConnectionTest and ExplainPath already
// use - so a client can read, page, aggregate and clear the history without a wire change.
//
// Everything here is bounded by the history itself: the query carries its own output budget, the
// ring enforces entries/bytes/TTL, and a disabled history reports "not enabled" rather than an empty
// window.

import (
	stdjson "encoding/json"
	"time"

	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/route"
	E "github.com/sagernet/sing/common/exceptions"
)

// commandServerDNSHistoryOptions is the JSON body of DNSHistoryEnable.
type commandServerDNSHistoryOptions struct {
	MaxEntries      int32 `json:"maxEntries,omitempty"`
	MaxBytes        int32 `json:"maxBytes,omitempty"`
	TTLMilliseconds int64 `json:"ttlMs,omitempty"`
}

// commandServerDNSHistoryQuery is the JSON body of DNSHistoryQuery.
type commandServerDNSHistoryQuery struct {
	Since     uint64 `json:"since,omitempty"`
	Limit     int32  `json:"limit,omitempty"`
	Transport string `json:"transport,omitempty"`
	Source    string `json:"source,omitempty"`
	MaxBytes  int32  `json:"maxBytes,omitempty"`
}

// commandServerDNSHistoryRecord is one record as the binding presents it.
type commandServerDNSHistoryRecord struct {
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

// commandServerDNSHistoryStats is the history's accounting as the binding presents it.
type commandServerDNSHistoryStats struct {
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

// commandServerDNSHistoryQueryResult is the JSON body returned by DNSHistoryQuery.
//
// Truncated and NextSequence are part of the pagination contract: when the response could not carry
// every selected record, `truncated` is true and `nextSequence` is the last sequence that WAS
// carried - passing it back as `since` continues the scan. A response that stopped without either
// would leave the caller unable to tell "nothing more" from "more than fits".
type commandServerDNSHistoryQueryResult struct {
	Records      []commandServerDNSHistoryRecord `json:"records"`
	Stats        commandServerDNSHistoryStats    `json:"stats"`
	Truncated    bool                            `json:"truncated,omitempty"`
	NextSequence uint64                          `json:"nextSequence,omitempty"`
}

// The encoded-response bounds.
//
// # Why retention accounting cannot size the output
//
// The ring charges RETAINED bytes; the binding hands the platform one JSON string, and escaping,
// field names, the envelope and the stats all add to it. A read sized by the retained estimate was
// therefore always under-sized: two records charged 154 bytes encoded to 80,485 bytes.
//
// maxDNSHistoryBindingBytes is the hard ceiling of one response; the caller's `maxBytes` is applied
// INSIDE it (clamped to [min, max]). Per-field budgets bound one record even when it must be carried
// alone: the first record is always returned so the cursor advances, with its long text fields cut
// rune-safely and marked truncated.
const (
	maxDNSHistoryBindingBytes = 64 << 10
	minDNSHistoryBindingBytes = 1 << 10
	maxDNSHistoryFieldBytes   = 4 << 10
	maxDNSHistoryAnswersBytes = 4 << 10
	maxDNSHistoryAnswers      = 8
)

type commandServerDNSHistoryStatsResult struct {
	Stats commandServerDNSHistoryStats `json:"stats"`
}

type commandServerDNSHistoryTopKEntry struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type commandServerDNSHistoryTopKResult struct {
	Field   string                             `json:"field"`
	Entries []commandServerDNSHistoryTopKEntry `json:"entries"`
}

type commandServerDNSHistoryClearResult struct {
	Cleared int `json:"cleared"`
}

type commandServerDNSHistoryEnableResult struct {
	Enabled bool `json:"enabled"`
}

// dnsHistoryRouter is the running box's route router seen through the management forwarding.
func (s *CommandServer) dnsHistoryRouter() (*route.Router, error) {
	if s == nil || s.StartedService == nil {
		return nil, E.New("command server is not started")
	}
	instance := s.Instance()
	if instance == nil {
		return nil, E.New("service is not running")
	}
	boxInstance := instance.Box()
	if boxInstance == nil {
		return nil, E.New("box is not running")
	}
	routeRouter, isRouteRouter := boxInstance.Router().(*route.Router)
	if !isRouteRouter || routeRouter == nil {
		return nil, E.New("the running box has no route router")
	}
	return routeRouter, nil
}

// statsFields converts the history's accounting to its binding form.
func dnsHistoryStatsFields(stats dns.HistoryStats, dropped uint64) commandServerDNSHistoryStats {
	fields := commandServerDNSHistoryStats{
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

// DNSHistoryEnable attaches the bounded query history, creating it on first use.
//
// optionsJSON is `{"maxEntries":N,"maxBytes":N,"ttlMs":N}`; empty or partial fields take the
// history's defaults. A second call returns the same ring rather than replacing it, so two consumers
// on the platform share one window.
func (s *CommandServer) DNSHistoryEnable(optionsJSON string) (*StringBox, error) {
	routeRouter, err := s.dnsHistoryRouter()
	if err != nil {
		return nil, err
	}
	var options commandServerDNSHistoryOptions
	if optionsJSON != "" {
		if err = stdjson.Unmarshal([]byte(optionsJSON), &options); err != nil {
			return nil, E.Cause(err, "parse dns history options")
		}
	}
	historyOptions := dns.HistoryOptions{
		MaxEntries: int(options.MaxEntries),
		MaxBytes:   int(options.MaxBytes),
	}
	if options.TTLMilliseconds > 0 {
		historyOptions.TTL = timeMilliseconds(options.TTLMilliseconds)
	}
	if _, err = routeRouter.EnableDNSHistory(historyOptions); err != nil {
		return nil, err
	}
	return wrapString(marshalBindingJSON(commandServerDNSHistoryEnableResult{Enabled: true})), nil
}

// DNSHistoryDisable detaches the subscription and drops the ring. Idempotent.
func (s *CommandServer) DNSHistoryDisable() error {
	routeRouter, err := s.dnsHistoryRouter()
	if err != nil {
		return err
	}
	return routeRouter.DisableDNSHistory()
}

// DNSHistoryQuery reads the live window.
//
// queryJSON is `{"since":N,"limit":N,"transport":"...","source":"...","maxBytes":N}`; the cursor is
// exclusive. Three budgets are separate and all three are enforced:
//
//   - RETENTION: HistoryOptions bounds what the ring keeps (see DNSHistoryEnable);
//   - READ: the history's own MaxBytes bounds the retained bytes one call copies, and its Limit the
//     record count (above maxHistoryQueryLimit it is an error, not a clamp);
//   - OUTPUT: this binding's `maxBytes` bounds the ENCODED JSON that crosses the platform
//     interface, including field names, escaping, the envelope and the stats.
//
// The response says `truncated` when there is more to read, whatever stopped it: the history's
// `hasMore` (more matching records exist), a record dropped to fit the output budget, or a field
// cut. `nextSequence` is always the last sequence actually carried, so passing it back as `since`
// resumes exactly after it. A page that stopped at the limit and a page that reached the end are
// therefore distinguishable - which they are NOT by the record count alone.
func (s *CommandServer) DNSHistoryQuery(queryJSON string) (*StringBox, error) {
	routeRouter, err := s.dnsHistoryRouter()
	if err != nil {
		return nil, err
	}
	var query commandServerDNSHistoryQuery
	if queryJSON != "" {
		if err = stdjson.Unmarshal([]byte(queryJSON), &query); err != nil {
			return nil, E.Cause(err, "parse dns history query")
		}
	}
	page, err := routeRouter.QueryDNSHistoryPage(dns.HistoryQuery{
		Since:     query.Since,
		Limit:     int(query.Limit),
		Transport: query.Transport,
		Source:    dns.QuerySource(query.Source),
		MaxBytes:  int(query.MaxBytes),
	})
	if err != nil {
		return nil, err
	}
	// The bus-side drop counter is not part of the ring snapshot: it is a monotonic total reported
	// by the subscription, so it is read separately and presented as "by the time of this read".
	// The ring's own accounting (entries, bytes, rejected, expired, cleared) comes from the page, so
	// it describes the same window as the records.
	_, dropped, err := routeRouter.DNSHistoryStats()
	if err != nil {
		return nil, err
	}
	result := commandServerDNSHistoryQueryResult{
		Records: make([]commandServerDNSHistoryRecord, 0, len(page.Records)),
		Stats:   dnsHistoryStatsFields(page.Stats, dropped),
	}
	budget := dnsHistoryBindingBudget(query.MaxBytes)
	truncated := page.HasMore
	for index, record := range page.Records {
		presented, fieldCut := presentDNSHistoryRecord(record)
		candidate := commandServerDNSHistoryQueryResult{
			Records:      appendDNSHistoryRecord(result.Records, presented),
			Stats:        result.Stats,
			Truncated:    truncated || fieldCut,
			NextSequence: record.Sequence,
		}
		size := encodedDNSHistorySize(candidate)
		carried := size <= budget
		if !carried {
			truncated = true
			if index == 0 {
				// The first record is always carried so the cursor advances, but it must still fit
				// the budget the caller asked for: its text fields are cropped until it does, and
				// an identity-only record is the last resort.
				candidate, fieldCut, size = fitFirstDNSHistoryRecord(record, result.Records, result.Stats, budget)
				carried = size > 0 && size <= budget
				if carried && fieldCut {
					truncated = true
				}
			}
		}
		if !carried {
			break
		}
		result.Records = candidate.Records
		result.NextSequence = candidate.NextSequence
		if fieldCut {
			truncated = true
		}
	}
	if len(result.Records) == 0 && truncated {
		// Only reachable if even an identity-only record does not fit the floor, which would mean
		// the floor is below the envelope. Reporting it is better than a page that claims there is
		// more to read while carrying no cursor to read it with.
		return nil, E.New("dns history: the response budget cannot carry one record")
	}
	result.Truncated = truncated
	if !fitDNSHistoryEnvelope(&result, page.Records, budget) {
		return nil, E.New("dns history: the response budget cannot carry one record")
	}
	return wrapString(marshalBindingJSON(result)), nil
}

// fitDNSHistoryEnvelope makes the bytes that LEAVE the server fit the budget.
//
// The per-record acceptance above measures a candidate whose `truncated` flag is the one known at
// that moment. When a LATER record does not fit, the flag turns true after the carried page was
// already measured - and `omitempty` then adds its bytes to the envelope that is actually sent.
// That is the review counterexample: 2,504 requested, 2,521 delivered.
//
// The trim keeps the contract intact: dropping a trailing record moves the cursor back to the last
// one actually carried; dropping everything leaves the FIRST record cropped (identity and cursor
// survive), which is what lets the caller continue. False is returned only when even the
// identity-only form cannot fit.
func fitDNSHistoryEnvelope(result *commandServerDNSHistoryQueryResult, records []dns.HistoryRecord, budget int) bool {
	for encodedDNSHistorySize(*result) > budget {
		if len(result.Records) == 0 {
			return false
		}
		result.Records = result.Records[:len(result.Records)-1]
		result.Truncated = true
		if len(result.Records) > 0 {
			result.NextSequence = result.Records[len(result.Records)-1].Sequence
			continue
		}
		result.NextSequence = 0
		if len(records) == 0 {
			return false
		}
		cropped, _, size := fitFirstDNSHistoryRecord(records[0], nil, result.Stats, budget)
		if size <= 0 || size > budget {
			return false
		}
		result.Records = cropped.Records
		result.NextSequence = cropped.NextSequence
		result.Truncated = true
	}
	return true
}

// appendDNSHistoryRecord appends one presented record without aliasing the caller's slice.
func appendDNSHistoryRecord(records []commandServerDNSHistoryRecord, presented commandServerDNSHistoryRecord) []commandServerDNSHistoryRecord {
	next := make([]commandServerDNSHistoryRecord, len(records), len(records)+1)
	copy(next, records)
	return append(next, presented)
}

// encodedDNSHistorySize is the exact byte length of the response the platform would receive.
func encodedDNSHistorySize(result commandServerDNSHistoryQueryResult) int {
	encoded, err := stdjson.Marshal(result)
	if err != nil {
		return int(^uint(0) >> 1)
	}
	return len(encoded)
}

// fitFirstDNSHistoryRecord crops the first record's fields until the whole response fits budget.
//
// It returns the candidate, whether anything was cut, and its encoded size. The identity fields
// (sequence, time, source, rcode) and the cursor are never removed: a page whose first record lost
// its identity could not be continued.
func fitFirstDNSHistoryRecord(record dns.HistoryRecord, kept []commandServerDNSHistoryRecord, stats commandServerDNSHistoryStats, budget int) (commandServerDNSHistoryQueryResult, bool, int) {
	var best commandServerDNSHistoryQueryResult
	bestSize := 0
	for _, fieldBudget := range []int{maxDNSHistoryFieldBytes, maxDNSHistoryFieldBytes / 2, maxDNSHistoryFieldBytes / 4, 512, 128, 32, 0} {
		presented, cut := presentDNSHistoryRecordWithLimits(record, fieldBudget, fieldBudget)
		candidate := commandServerDNSHistoryQueryResult{
			Records:      appendDNSHistoryRecord(kept, presented),
			Stats:        stats,
			Truncated:    true,
			NextSequence: record.Sequence,
		}
		size := encodedDNSHistorySize(candidate)
		if size <= budget {
			// Anything below the default field budget means text was cut even if this particular
			// record had short fields: the caller must not read "truncated" as "the text is whole".
			return candidate, cut || fieldBudget < maxDNSHistoryFieldBytes, size
		}
		best, bestSize = candidate, size
	}
	// Even the identity-only form did not fit; hand back the smallest attempt so the caller can
	// report the floor problem instead of silently dropping the record.
	return best, true, bestSize
}

// dnsHistoryBindingBudget applies the caller's requested output budget inside the server's limits.
//
// The caller controls it, the server owns the floor and the ceiling: a request below the floor would
// make a single record unreadable (no pagination progress), and one above the ceiling would let a
// response exceed what the binding is prepared to hand the platform.
func dnsHistoryBindingBudget(requested int32) int {
	budget := int(requested)
	if budget <= 0 {
		return maxDNSHistoryBindingBytes
	}
	if budget < minDNSHistoryBindingBytes {
		return minDNSHistoryBindingBytes
	}
	if budget > maxDNSHistoryBindingBytes {
		return maxDNSHistoryBindingBytes
	}
	return budget
}

// presentDNSHistoryRecord converts one record and bounds every variable field by its ENCODED size.
func presentDNSHistoryRecord(record dns.HistoryRecord) (commandServerDNSHistoryRecord, bool) {
	return presentDNSHistoryRecordWithLimits(record, maxDNSHistoryFieldBytes, maxDNSHistoryAnswersBytes)
}

// presentDNSHistoryRecordWithLimits presents one record with caller-chosen field and answer budgets.
//
// A non-positive budget OMITS the text fields entirely instead of leaving the ellipsis marker: the
// marker itself costs bytes, and the reason this mode exists is a record that must fit a response
// budget it would otherwise exceed. Identity (sequence, time, source, rcode) and the cursor are
// never omitted.
func presentDNSHistoryRecordWithLimits(record dns.HistoryRecord, fieldBudget, answersBudget int) (commandServerDNSHistoryRecord, bool) {
	event := record.Event
	truncated := false
	presentField := func(value string) string {
		if value == "" || fieldBudget <= 0 {
			if value != "" {
				truncated = true
			}
			return ""
		}
		bounded, cut := truncateJSONField(value, fieldBudget)
		truncated = truncated || cut
		return bounded
	}
	presented := commandServerDNSHistoryRecord{
		Sequence:   record.Sequence,
		TimeMs:     event.Time.UnixMilli(),
		Transport:  presentField(event.TransportTag),
		Name:       presentField(event.Name),
		QType:      event.QType,
		Source:     string(event.Source),
		RCode:      event.RCode,
		DurationMs: event.DurationMs,
		Shared:     event.Shared,
		Final:      event.Final,
		Rule:       presentField(event.Rule),
		Err:        presentField(event.Err),
	}
	if len(event.Answers) > 0 {
		if answersBudget <= 0 {
			truncated = true
			return presented, truncated
		}
		answers := event.Answers
		if len(answers) > maxDNSHistoryAnswers {
			answers = answers[:maxDNSHistoryAnswers]
			truncated = true
		}
		budget := answersBudget
		for _, answer := range answers {
			bounded, cut := truncateJSONField(answer, fieldBudget)
			if cut {
				truncated = true
			}
			if budget <= 0 {
				truncated = true
				break
			}
			if len(bounded) > budget {
				bounded, _ = truncateJSONField(bounded, budget)
				truncated = true
			}
			presented.Answers = append(presented.Answers, bounded)
			budget -= len(bounded)
		}
	}
	return presented, truncated
}

// truncateJSONField cuts value so that its ENCODED JSON form fits budget, and reports whether it
// was cut.
//
// The budget is on the encoded form, not on the input: a rune can become a six-byte `\uXXXX`
// escape, and an input-length check would let a field of control characters multiply the response.
// The cut is rune-safe and marked with an ellipsis so a reader can tell the text is incomplete.
func truncateJSONField(value string, budget int) (string, bool) {
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

// DNSHistoryStats reports the history's accounting plus the bus-side drop count.
func (s *CommandServer) DNSHistoryStats() (*StringBox, error) {
	routeRouter, err := s.dnsHistoryRouter()
	if err != nil {
		return nil, err
	}
	stats, dropped, err := routeRouter.DNSHistoryStats()
	if err != nil {
		return nil, err
	}
	return wrapString(marshalBindingJSON(commandServerDNSHistoryStatsResult{
		Stats: dnsHistoryStatsFields(stats, dropped),
	})), nil
}

// DNSHistoryTopK aggregates the retained records by transport or source.
func (s *CommandServer) DNSHistoryTopK(field string, limit int32) (*StringBox, error) {
	routeRouter, err := s.dnsHistoryRouter()
	if err != nil {
		return nil, err
	}
	topField := dns.TopField(field)
	switch topField {
	case dns.TopFieldTransport, dns.TopFieldSource:
	default:
		return nil, E.New("dns history: unknown top-k field: ", field)
	}
	entries, err := routeRouter.DNSHistoryTopK(topField, int(limit))
	if err != nil {
		return nil, err
	}
	result := commandServerDNSHistoryTopKResult{Field: string(topField), Entries: make([]commandServerDNSHistoryTopKEntry, 0, len(entries))}
	for _, entry := range entries {
		result.Entries = append(result.Entries, commandServerDNSHistoryTopKEntry{Value: entry.Value, Count: entry.Count})
	}
	return wrapString(marshalBindingJSON(result)), nil
}

// DNSHistoryClear empties the window and reports how many records it dropped.
func (s *CommandServer) DNSHistoryClear() (*StringBox, error) {
	routeRouter, err := s.dnsHistoryRouter()
	if err != nil {
		return nil, err
	}
	cleared, err := routeRouter.ClearDNSHistory()
	if err != nil {
		return nil, err
	}
	return wrapString(marshalBindingJSON(commandServerDNSHistoryClearResult{Cleared: cleared})), nil
}

// marshalBindingJSON encodes a binding result. A marshal failure cannot happen for these shapes
// (no maps, channels or funcs), and returning "{}" keeps the binding form valid rather than handing
// the platform a nil string.
func marshalBindingJSON(value any) string {
	encoded, err := stdjson.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// timeMilliseconds converts a millisecond count to a duration for the history's TTL.
func timeMilliseconds(milliseconds int64) time.Duration {
	return time.Duration(milliseconds) * time.Millisecond
}
