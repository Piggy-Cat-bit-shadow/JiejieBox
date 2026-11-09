package route

// The management forwarding for the DNS history: the route layer already drives the DNS router, so
// a caller that holds the route router can reach the bounded history without a new Box accessor.
//
// It is the same shape as the DNS preview forwarding in route_preview.go: an OPTIONAL capability,
// type-asserted on the DNS router. A build or a test whose DNS side is a fake keeps working - the
// calls report "unsupported" rather than panicking - which is what lets the diagnostic history stay
// optional in every non-DNS harness.

import (
	"github.com/sagernet/sing-box/dns"
	E "github.com/sagernet/sing/common/exceptions"
)

// The production DNS router is the implementation this forwarding is written for. Pinned at compile
// time so a change to either side cannot silently drop one of the methods.
var _ dnsHistoryManager = (*dns.Router)(nil)

// ErrDNSHistoryUnsupported reports that this router's DNS side has no history capability.
var ErrDNSHistoryUnsupported = E.New("dns history: not supported by this router")

// dnsHistoryManager is the optional capability the DNS router exposes for the management entry.
type dnsHistoryManager interface {
	EnableHistory(options dns.HistoryOptions) *dns.History
	DisableHistory()
	DNSHistory() *dns.History
	QueryHistory(query dns.HistoryQuery) ([]dns.HistoryRecord, error)
	HistoryTopK(field dns.TopField, k int) ([]dns.TopEntry, error)
	HistoryStats() (dns.HistoryStats, uint64, error)
	ClearHistory() (int, error)
}

// dnsHistoryRouter returns the management capability, or nil when the DNS side has none.
func (r *Router) dnsHistoryRouter() (dnsHistoryManager, bool) {
	if r == nil || r.dns == nil {
		return nil, false
	}
	manager, isManager := r.dns.(dnsHistoryManager)
	return manager, isManager
}

// EnableDNSHistory attaches the bounded query history and returns it.
func (r *Router) EnableDNSHistory(options dns.HistoryOptions) (*dns.History, error) {
	manager, supported := r.dnsHistoryRouter()
	if !supported {
		return nil, ErrDNSHistoryUnsupported
	}
	return manager.EnableHistory(options), nil
}

// DisableDNSHistory detaches the subscription and drops the ring.
func (r *Router) DisableDNSHistory() error {
	manager, supported := r.dnsHistoryRouter()
	if !supported {
		return ErrDNSHistoryUnsupported
	}
	manager.DisableHistory()
	return nil
}

// DNSHistory returns the live history, or nil when it is not enabled.
func (r *Router) DNSHistory() *dns.History {
	manager, supported := r.dnsHistoryRouter()
	if !supported {
		return nil
	}
	return manager.DNSHistory()
}

// QueryDNSHistory reads the live history.
func (r *Router) QueryDNSHistory(query dns.HistoryQuery) ([]dns.HistoryRecord, error) {
	manager, supported := r.dnsHistoryRouter()
	if !supported {
		return nil, ErrDNSHistoryUnsupported
	}
	return manager.QueryHistory(query)
}

// DNSHistoryTopK aggregates the live history.
func (r *Router) DNSHistoryTopK(field dns.TopField, k int) ([]dns.TopEntry, error) {
	manager, supported := r.dnsHistoryRouter()
	if !supported {
		return nil, ErrDNSHistoryUnsupported
	}
	return manager.HistoryTopK(field, k)
}

// DNSHistoryStats reports the history's accounting and the bus-side drop count.
func (r *Router) DNSHistoryStats() (dns.HistoryStats, uint64, error) {
	manager, supported := r.dnsHistoryRouter()
	if !supported {
		return dns.HistoryStats{}, 0, ErrDNSHistoryUnsupported
	}
	return manager.HistoryStats()
}

// ClearDNSHistory empties the window and reports how many records it dropped.
func (r *Router) ClearDNSHistory() (int, error) {
	manager, supported := r.dnsHistoryRouter()
	if !supported {
		return 0, ErrDNSHistoryUnsupported
	}
	return manager.ClearHistory()
}
