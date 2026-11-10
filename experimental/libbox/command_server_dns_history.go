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
// exclusive, and the output is bounded by both the record count and the byte budget.
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
	records, err := routeRouter.QueryDNSHistory(dns.HistoryQuery{
		Since:     query.Since,
		Limit:     int(query.Limit),
		Transport: query.Transport,
		Source:    dns.QuerySource(query.Source),
		MaxBytes:  int(query.MaxBytes),
	})
	if err != nil {
		return nil, err
	}
	history := routeRouter.DNSHistory()
	if history == nil {
		return nil, dns.ErrHistoryNotEnabled
	}
	result := commandServerDNSHistoryQueryResult{
		Records: make([]commandServerDNSHistoryRecord, 0, len(records)),
		Stats:   dnsHistoryStatsFields(history.Stats(), 0),
	}
	budget := dnsHistoryBindingBudget(query.MaxBytes)
	truncated := false
	for index, record := range records {
		presented, fieldCut := presentDNSHistoryRecord(record)
		// The size is measured on the ENCODED form, because that is what crosses the binding:
		// JSON escaping and the field names are part of the response the platform receives.
		candidate := commandServerDNSHistoryQueryResult{
			Records:      append(append([]commandServerDNSHistoryRecord(nil), result.Records...), presented),
			Stats:        result.Stats,
			Truncated:    truncated || fieldCut || index < len(records)-1,
			NextSequence: record.Sequence,
		}
		encoded, encodeErr := stdjson.Marshal(candidate)
		if encodeErr != nil {
			return nil, E.Cause(encodeErr, "encode dns history query")
		}
		if len(encoded) > budget {
			if index == 0 {
				// The first record is always carried so the caller's cursor advances; its fields are
				// already bounded, so the response stays under the server ceiling.
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
	return wrapString(marshalBindingJSON(result)), nil
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
	event := record.Event
	truncated := false
	name, cut := truncateJSONField(event.Name, maxDNSHistoryFieldBytes)
	truncated = truncated || cut
	transport, cut := truncateJSONField(event.TransportTag, maxDNSHistoryFieldBytes)
	truncated = truncated || cut
	ruleText, cut := truncateJSONField(event.Rule, maxDNSHistoryFieldBytes)
	truncated = truncated || cut
	errText, cut := truncateJSONField(event.Err, maxDNSHistoryFieldBytes)
	truncated = truncated || cut
	presented := commandServerDNSHistoryRecord{
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
		if len(answers) > maxDNSHistoryAnswers {
			answers = answers[:maxDNSHistoryAnswers]
			truncated = true
		}
		budget := maxDNSHistoryAnswersBytes
		for _, answer := range answers {
			bounded, cut := truncateJSONField(answer, maxDNSHistoryFieldBytes)
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
