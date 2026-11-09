package route

// O01 — the ROUTE-rule half of the explanation: which rule decides this request, and why.
//
// ExplainPath (libbox) projects the box's published status model: control descent, hops, readiness.
// It deliberately does not say which rule matched, because a rule match cannot be read off a status
// value - it has to be evaluated against the request. This is that evaluation, and it is READ-ONLY
// by construction:
//
//   - it never calls prepareMatchMetadata, so no process walk, no neighbor lookup, no FakeIP
//     mapping, no reverse-mapping DNS lookup happens;
//   - it never dials, resolves, sniffs, writes a cache or touches a selection;
//   - the process facts it may use are the ones the CALLER supplied, which is why a path rule
//     without them is reported as unknown instead of being guessed at or walked for.
//
// The loop mirrors matchRule's decision points, so "the model says" and "the production decision"
// are comparable for the same request. A rule that would need a fact the request does not carry (a
// process path, or a sniffed domain when the caller supplied none) ends the preview as UNKNOWN: a
// confident wrong answer is worse than an honest "cannot tell here".

import (
	"context"
	"net/netip"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/dns"
	R "github.com/sagernet/sing-box/route/rule"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
)

// RoutePreviewRequest is the request a preview explains.
//
// It is intentionally the facts a caller can state, not the box's live state: "where would this go"
// is answerable without a connection, while "why did this connection go there" is a different
// question answered from the metadata the real request already produced.
type RoutePreviewRequest struct {
	Inbound     string
	Network     string
	Source      netip.AddrPort
	Destination M.Socksaddr
	// Domain and Protocol are the sniffed facts when the caller already knows them. Supplying them
	// is what lets a preview continue past a sniff rule; without them, a sniff rule makes every
	// later decision unknown.
	Domain   string
	Protocol string
	// UserId is the socket owner when the caller knows it (-1 means unknown).
	UserId int32
	// ProcessPaths and PackageNames are the process facts when the caller knows them. A preview
	// never resolves them itself.
	ProcessPaths []string
	PackageNames []string
}

// RoutePreviewResult is the read-only answer.
type RoutePreviewResult struct {
	// Matched reports that a rule decided the request. False with an empty Unknown means the
	// request fell through to the final outbound.
	Matched    bool
	RuleIndex  int
	Rule       string
	ActionType string
	// ActionOutbound is the target of a route action (and of a bypass action that names one).
	ActionOutbound string
	// Unknown states, when non-empty, that the preview could not decide: the request would be
	// decided by a rule that needs a fact this preview does not have.
	Unknown          string
	UnknownRuleIndex int
	// FinalDestination is where production would actually DIAL: the request's destination after
	// every matched action's pure metadata transformations (override_address, override_port, ...).
	// A diagnostic that dials the request's original text instead cannot reproduce what the route
	// decision chose - the reviewed failure was exactly that: the rule rewrote the port and the
	// test dialled the old one.
	FinalDestination M.Socksaddr
	// OriginalDestination is the destination the request entered with.
	OriginalDestination M.Socksaddr
	// Evaluated counts how many rules were matched against the request before the result was
	// reached. It is the consumer-cost fact: a preview over a large rule set reports what it
	// walked, and that walking is bounded by the rule list rather than by any I/O.
	Evaluated int
}

// LookupDomain resolves a name through this router's REAL DNS path.
//
// It is the active counterpart of PreviewDNSLookup: a diagnostic that must dial an address resolves
// the same way production would (rules, strategy, cache and TTL included). Unlike the preview it is
// allowed to query and to populate the cache - that is what "actual observation" means, and the
// caller is responsible for bounding it with its own budget.
func (r *Router) LookupDomain(ctx context.Context, domain string, options adapter.DNSQueryOptions) ([]netip.Addr, error) {
	if r == nil || r.dns == nil {
		return nil, E.New("this router has no DNS router")
	}
	return r.dns.Lookup(ctx, domain, options)
}

// PreviewDNSLookup explains which DNS rule and server would answer a query, by delegating to the
// DNS router this route layer already drives.
//
// # Why the route layer is the way in
//
// A management caller holds the route router (Box.Router). It does not hold the DNS router, and
// adding an accessor for it to the Box would be a shared-interface change for one explanation
// stage. The route layer already has the DNS router, so it exposes the READ-ONLY preview through
// the object the caller has.
//
// The second result reports whether this build can explain a DNS query at all: a router whose DNS
// side is a fake, an older build, or a box with no DNS router answers false, and the caller reports
// a skipped step instead of a guessed decision.
func (r *Router) PreviewDNSLookup(ctx context.Context, query dns.PreviewQuery) (dns.PreviewResult, bool) {
	if r == nil || r.dns == nil {
		return dns.PreviewResult{}, false
	}
	previewer, isPreviewer := r.dns.(dnsQueryPreviewer)
	if !isPreviewer {
		return dns.PreviewResult{}, false
	}
	return previewer.PreviewLookup(ctx, query), true
}

// dnsQueryPreviewer is the optional capability a DNS router exposes for the read-only explanation.
type dnsQueryPreviewer interface {
	PreviewLookup(ctx context.Context, query dns.PreviewQuery) dns.PreviewResult
}

// PreviewRoute explains which rule decides the request, without any of the work a real request does.
func (r *Router) PreviewRoute(ctx context.Context, request RoutePreviewRequest) (result RoutePreviewResult) {
	_ = ctx // the preview performs no I/O, so it takes no cancellation and no deadline
	result = RoutePreviewResult{RuleIndex: -1, UnknownRuleIndex: -1, FinalDestination: request.Destination, OriginalDestination: request.Destination}
	if r == nil || len(r.rules) == 0 {
		return result
	}
	metadata := r.previewMetadata(request)
	// The destination a diagnostic must dial is the one production would see AFTER the matched
	// actions' options were applied, which the loop below mutates on this private copy.
	defer func() { result.FinalDestination = metadata.Destination }()
	for index, rule := range r.rules {
		metadata.ResetRuleCache()
		result.Evaluated++
		if r.previewNeedsUnknownFact(index, &metadata) {
			result.RuleIndex = -1
			result.Rule = rule.String()
			result.UnknownRuleIndex = index
			result.Unknown = "the decision depends on a process path this preview was not given"
			return result
		}
		if !rule.Match(&metadata) {
			continue
		}
		// Apply the action's pure metadata transformations to the PRIVATE copy, through the same
		// function the production walk uses. They are not decoration: an override_port or
		// override_address decides what every LATER rule sees, so a rule that matches the rewritten
		// destination is part of the real decision. Skipping them made the preview report "no rule
		// matched" for a request production rejected one rule later.
		applyActionRouteOptions(&metadata, rule.Action())
		actionType := rule.Action().Type()
		decided := actionType == C.RuleActionTypeRoute ||
			actionType == C.RuleActionTypeReject ||
			actionType == C.RuleActionTypeHijackDNS
		if bypassAction, isBypass := rule.Action().(*R.RuleActionBypass); isBypass {
			// Production keeps matching past a bypass that names no outbound; a preview that
			// stopped there would report a decision the router never makes.
			decided = bypassAction.Outbound != ""
		}
		if decided {
			result.Matched = true
			result.RuleIndex = index
			result.Rule = rule.String()
			result.ActionType = actionType
			switch action := rule.Action().(type) {
			case *R.RuleActionRoute:
				result.ActionOutbound = action.Outbound
			case *R.RuleActionBypass:
				result.ActionOutbound = action.Outbound
			}
			return result
		}
		if actionType == C.RuleActionTypeSniff && request.Domain == "" {
			// A sniff action is what would have produced the domain later rules may match on, and a
			// preview does not sniff. Reporting the next rule as a decision would be a guess.
			result.RuleIndex = -1
			result.Rule = rule.String()
			result.UnknownRuleIndex = index
			result.Unknown = "the decision depends on a sniffed domain this preview was not given"
			return result
		}
	}
	return result
}

// previewMetadata builds the request metadata from the caller's facts only.
func (r *Router) previewMetadata(request RoutePreviewRequest) adapter.InboundContext {
	metadata := adapter.InboundContext{
		Inbound:     request.Inbound,
		Network:     request.Network,
		Destination: request.Destination,
		Domain:      request.Domain,
		Protocol:    request.Protocol,
	}
	if request.Source.IsValid() {
		metadata.Source = M.SocksaddrFrom(request.Source.Addr(), request.Source.Port())
	}
	if request.UserId >= 0 || len(request.ProcessPaths) > 0 || len(request.PackageNames) > 0 {
		metadata.ProcessInfo = &adapter.ConnectionOwner{
			UserId:       request.UserId,
			ProcessPaths: append([]string(nil), request.ProcessPaths...),
			PackageNames: append([]string(nil), request.PackageNames...),
		}
	}
	return metadata
}

// previewNeedsUnknownFact reports whether evaluating rule index would need a process path the
// request did not supply.
//
// The distinction that keeps this honest: a rule that reads a path is not "not matching" without
// one, it is UNDECIDABLE. Production would have walked /proc before reaching it; a preview must
// not, so it says so.
func (r *Router) previewNeedsUnknownFact(index int, metadata *adapter.InboundContext) bool {
	if index >= len(r.ruleNeedsPaths) || !r.ruleNeedsPaths[index] {
		return false
	}
	if metadata.ProcessInfo == nil {
		return true
	}
	return len(metadata.ProcessInfo.ProcessPaths) == 0 && len(metadata.ProcessInfo.PackageNames) == 0
}
