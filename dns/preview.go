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
	// ProcessPaths and PackageNames are the process facts the CALLER can state.
	//
	// A preview must not run a /proc walk - that is the exchange's job, and doing it here would
	// both make a read-only explanation expensive and let it observe a process table the real
	// query never sees. A caller that already knows the process (an operator tool, a test, a
	// management API replaying a connection's facts) supplies it; otherwise a rule that reads one
	// is reported as Unknown with that dependency instead of being silently skipped.
	ProcessPaths []string
	PackageNames []string
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
	// Unknown reports that the walk stopped at a rule whose verdict depends on a fact the caller
	// did not supply. Matched is false and RuleIndex/Rule/ActionType name the rule that could not
	// be decided; Server is empty because naming one would be a guess.
	//
	// This is deliberately NOT "no match": reporting the default server when a process-path rule
	// might have answered would make the explanation disagree with the exchange it explains.
	Unknown bool
	// DependsOn names the missing fact ("process_path"), for a caller that wants to say why.
	DependsOn string
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
	// The process facts are the caller's own statement. They are copied onto the private metadata
	// only; the preview never runs a /proc walk to obtain them (that is the exchange's job, and it
	// would make a read-only explanation both expensive and dependent on a process table the real
	// query may never consult).
	if len(query.ProcessPaths) > 0 || len(query.PackageNames) > 0 {
		metadata.ProcessInfo = &adapter.ConnectionOwner{
			ProcessPaths: query.ProcessPaths,
			PackageNames: query.PackageNames,
		}
	}

	options := adapter.DNSQueryOptions{}
	isAddress := query.QType == mDNS.TypeA || query.QType == mDNS.TypeAAAA || query.QType == mDNS.TypeHTTPS
	transport, rule, index, unknown := r.matchDNS(previewContext, rules, query.AllowFakeIP, -1, isAddress, &options, false)
	if unknown {
		// The walk reached a rule it cannot decide from the stated facts. Reporting the default
		// server here would be a guess that disagrees with the exchange this explains, so the
		// dependency is reported instead.
		result.Unknown = true
		result.DependsOn = "process_path"
		result.RuleIndex = index
		if rule != nil {
			result.Rule = rule.String()
			result.ActionType = rule.Action().Type()
		}
		result.Evaluated = index + 1
		return result
	}
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
