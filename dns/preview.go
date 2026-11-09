package dns

// O01 — the DNS-rule half of the explanation: which rule decides this query, and which server.
//
// The route side answers the same question for connections (see route.Router.PreviewRoute). This is
// the DNS equivalent, and it is built on the SAME matcher the exchange path uses (matchDNS), so the
// preview cannot drift into a second implementation of the rule walk.
//
// What it deliberately does not do: no query is sent, no cache is read or written, no rule action's
// side effects run, and the per-query metadata is a fresh copy - the real request's metadata is
// never touched. The only reads are the rule list and the transport registry.

import (
	"context"
	"net/netip"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"

	mDNS "github.com/miekg/dns"
)

// PreviewQuery is the query a preview explains.
type PreviewQuery struct {
	// Name is the question name, with or without a trailing dot.
	Name  string
	QType uint16
	// Source is the client address when the caller knows it.
	Source netip.AddrPort
	// ClientSubnet and DNSSEC are the EDNS facts a rule may match on.
	ClientSubnet netip.Prefix
	DNSSEC       bool
	// AllowFakeIP mirrors the exchange path's flag: the main exchange allows a fakeip rule, the
	// single-record lookup path does not. A preview that guessed would disagree with production.
	AllowFakeIP bool
}

// PreviewResult is the read-only answer.
type PreviewResult struct {
	// Matched reports that a rule decided the query. False means the default transport answers.
	Matched    bool
	RuleIndex  int
	Rule       string
	ActionType string
	// Server is the transport tag that would answer: the rule's server, or the default server when
	// no rule matched.
	Server string
	// ServerType is that transport's type (a fakeip rule is visible as such).
	ServerType string
	// Evaluated is how many rules the walk considered. It is the consumer-cost fact and needs no
	// I/O measurement: the walk is bounded by the rule list.
	Evaluated int
}

// PreviewLookup explains which rule decides the query and which server would answer it.
func (r *Router) PreviewLookup(ctx context.Context, query PreviewQuery) PreviewResult {
	result := PreviewResult{RuleIndex: -1}
	if r == nil {
		return result
	}
	r.rulesAccess.RLock()
	rules := r.rules
	r.rulesAccess.RUnlock()
	if len(rules) == 0 {
		if transport := r.transport.Default(); transport != nil {
			result.Server = transport.Tag()
			result.ServerType = transport.Type()
		}
		return result
	}

	// The same metadata preparation the exchange path performs, on a COPY of the caller's context.
	// ExtendContext is what keeps the real request's metadata out of this evaluation.
	previewContext, metadata := adapter.ExtendContext(ctx)
	metadata.Destination = M.Socksaddr{}
	metadata.QueryType = query.QType
	switch query.QType {
	case mDNS.TypeA:
		metadata.IPVersion = 4
	case mDNS.TypeAAAA:
		metadata.IPVersion = 6
	}
	metadata.Domain = FqdnToDomain(query.Name)
	metadata.QueryClientSubnet = query.ClientSubnet
	metadata.QueryDNSSEC = query.DNSSEC
	metadata.DestinationAddressMatchFromResponse = false
	if query.Source.IsValid() {
		metadata.Source = M.SocksaddrFrom(query.Source.Addr(), query.Source.Port())
	}

	options := adapter.DNSQueryOptions{}
	isAddress := query.QType == mDNS.TypeA || query.QType == mDNS.TypeAAAA || query.QType == mDNS.TypeHTTPS
	transport, rule, index := r.matchDNS(previewContext, rules, query.AllowFakeIP, -1, isAddress, &options)
	result.Evaluated = len(rules)
	if index >= 0 && rule != nil {
		result.Matched = true
		result.RuleIndex = index
		result.Rule = rule.String()
		result.ActionType = rule.Action().Type()
	}
	if transport != nil {
		result.Server = transport.Tag()
		result.ServerType = transport.Type()
	}
	return result
}
