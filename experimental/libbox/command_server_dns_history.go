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
type commandServerDNSHistoryQueryResult struct {
	Records []commandServerDNSHistoryRecord `json:"records"`
	Stats   commandServerDNSHistoryStats    `json:"stats"`
}

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
	result := commandServerDNSHistoryQueryResult{
		Records: make([]commandServerDNSHistoryRecord, 0, len(records)),
		Stats:   dnsHistoryStatsFields(routeRouter.DNSHistory().Stats(), 0),
	}
	for _, record := range records {
		event := record.Event
		presented := commandServerDNSHistoryRecord{
			Sequence:   record.Sequence,
			TimeMs:     event.Time.UnixMilli(),
			Transport:  event.TransportTag,
			Name:       event.Name,
			QType:      event.QType,
			Source:     string(event.Source),
			RCode:      event.RCode,
			DurationMs: event.DurationMs,
			Shared:     event.Shared,
			Final:      event.Final,
			Rule:       event.Rule,
			Err:        event.Err,
		}
		if len(event.Answers) > 0 {
			presented.Answers = append([]string(nil), event.Answers...)
		}
		result.Records = append(result.Records, presented)
	}
	return wrapString(marshalBindingJSON(result)), nil
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
