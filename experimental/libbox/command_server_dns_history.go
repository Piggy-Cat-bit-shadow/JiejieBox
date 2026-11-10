package libbox

// The DNS history management surface on the command server.
//
// The bounded history and its router-level entry already exist (EnableHistory/QueryHistory/
// HistoryStats/ClearHistory). What was missing is a consumer surface, and this file is the
// gomobile one: the same management calls as the daemon's gRPC service, with JSON strings crossing
// the binding - the shape ConnectionTest and ExplainPath already use - so a client can read, page,
// aggregate and clear the history without a wire change.
//
// The document itself is built by the dns package (`HistoryQueryView` and friends), because the
// daemon's RPCs must return the SAME document: the hard cap, the clamping, the rune-safe cut and
// the pagination fields are one implementation, not two that happen to agree today.
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

// dnsHistoryEnableOptions is the JSON body of DNSHistoryEnable, shared by the gomobile surface and
// the daemon client (which forwards it as RPC fields).
type dnsHistoryEnableOptions struct {
	MaxEntries      int32 `json:"maxEntries,omitempty"`
	MaxBytes        int32 `json:"maxBytes,omitempty"`
	TTLMilliseconds int64 `json:"ttlMs,omitempty"`
}

// dnsHistoryQueryOptions is the JSON body of DNSHistoryQuery.
type dnsHistoryQueryOptions struct {
	Since     uint64 `json:"since,omitempty"`
	Limit     int32  `json:"limit,omitempty"`
	Transport string `json:"transport,omitempty"`
	Source    string `json:"source,omitempty"`
	MaxBytes  int32  `json:"maxBytes,omitempty"`
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
	options, err := parseDNSHistoryEnableOptions(optionsJSON)
	if err != nil {
		return nil, err
	}
	if _, err = routeRouter.EnableDNSHistory(historyOptionsFromEnable(options)); err != nil {
		return nil, err
	}
	return wrapString(marshalBindingJSON(dns.HistoryEnableView{Enabled: true})), nil
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
	query, err := parseDNSHistoryQueryOptions(queryJSON)
	if err != nil {
		return nil, err
	}
	history := routeRouter.DNSHistory()
	if history == nil {
		return nil, dns.ErrHistoryNotEnabled
	}
	records, err := routeRouter.QueryDNSHistory(historyQueryFromOptions(query))
	if err != nil {
		return nil, err
	}
	return wrapString(marshalBindingJSON(dns.BuildHistoryQueryView(records, history.Stats(), 0, int(query.MaxBytes)))), nil
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
	return wrapString(marshalBindingJSON(dns.BuildHistoryStatsView(stats, dropped))), nil
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
	return wrapString(marshalBindingJSON(dns.BuildHistoryTopKView(topField, entries))), nil
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
	return wrapString(marshalBindingJSON(dns.HistoryClearView{Cleared: cleared})), nil
}

// parseDNSHistoryEnableOptions parses the binding's enable body.
func parseDNSHistoryEnableOptions(optionsJSON string) (dnsHistoryEnableOptions, error) {
	var options dnsHistoryEnableOptions
	if optionsJSON != "" {
		if err := stdjson.Unmarshal([]byte(optionsJSON), &options); err != nil {
			return dnsHistoryEnableOptions{}, E.Cause(err, "parse dns history options")
		}
	}
	return options, nil
}

// historyOptionsFromEnable maps the binding body to the history's own options.
func historyOptionsFromEnable(options dnsHistoryEnableOptions) dns.HistoryOptions {
	historyOptions := dns.HistoryOptions{
		MaxEntries: int(options.MaxEntries),
		MaxBytes:   int(options.MaxBytes),
	}
	if options.TTLMilliseconds > 0 {
		historyOptions.TTL = time.Duration(options.TTLMilliseconds) * time.Millisecond
	}
	return historyOptions
}

// parseDNSHistoryQueryOptions parses the binding's query body.
func parseDNSHistoryQueryOptions(queryJSON string) (dnsHistoryQueryOptions, error) {
	var query dnsHistoryQueryOptions
	if queryJSON != "" {
		if err := stdjson.Unmarshal([]byte(queryJSON), &query); err != nil {
			return dnsHistoryQueryOptions{}, E.Cause(err, "parse dns history query")
		}
	}
	return query, nil
}

// historyQueryFromOptions maps the binding body to the history's own query.
func historyQueryFromOptions(query dnsHistoryQueryOptions) dns.HistoryQuery {
	return dns.HistoryQuery{
		Since:     query.Since,
		Limit:     int(query.Limit),
		Transport: query.Transport,
		Source:    dns.QuerySource(query.Source),
		MaxBytes:  int(query.MaxBytes),
	}
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
