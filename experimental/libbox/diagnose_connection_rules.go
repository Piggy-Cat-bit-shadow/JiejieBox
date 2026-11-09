package libbox

// O02/O01 — the RULE half of the connection test, taken from the running box.
//
// The active test walks: passive path -> the rule that decides the request -> the resolved address
// -> the dial. The passive path comes from the published status model; the rule cannot, because a
// rule match has to be evaluated against the request. That evaluation exists as
// route.Router.PreviewRoute, and this adapter is what lets the test reach it.
//
// It is an OPTIONAL capability of the path, deliberately: the runner is also driven by fakes and by
// builds that do not expose a route router, and those must get a skipped stage with a reason rather
// than a guessed decision. The preview itself is read-only (no /proc walk, no dial, no DNS, no
// cache or selection write), so adding this stage cannot change what the test then measures.

import (
	"context"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/physicalpath"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/route"
	M "github.com/sagernet/sing/common/metadata"

	mDNS "github.com/miekg/dns"
)

// boxConnectionTestPath is the running box seen through the slices the runner needs.
type boxConnectionTestPath struct {
	box *box.Box
}

func newBoxConnectionTestPath(boxInstance *box.Box) *boxConnectionTestPath {
	return &boxConnectionTestPath{box: boxInstance}
}

func (p *boxConnectionTestPath) Status(root string, network string) physicalpath.PathStatus {
	return p.box.Status(root, network)
}

func (p *boxConnectionTestPath) Outbound() adapter.OutboundManager {
	return p.box.Outbound()
}

// PreviewRouteRule explains which route rule decides a request for this host, if this build can.
func (p *boxConnectionTestPath) PreviewRouteRule(network string, domain string, port uint16) (connectionTestRulePreview, bool) {
	routeRouter, isRouteRouter := p.box.Router().(*route.Router)
	if !isRouteRouter || routeRouter == nil {
		return connectionTestRulePreview{}, false
	}
	result := routeRouter.PreviewRoute(context.Background(), route.RoutePreviewRequest{
		Network:     network,
		Destination: M.Socksaddr{Fqdn: domain, Port: port},
		Domain:      domain,
		UserId:      -1,
	})
	return connectionTestRulePreview{
		Matched:    result.Matched,
		RuleIndex:  result.RuleIndex,
		Rule:       result.Rule,
		ActionType: result.ActionType,
		Outbound:   result.ActionOutbound,
		Unknown:    result.Unknown,
	}, true
}

// PreviewDNSRule explains which DNS rule and server would answer the target's address query.
//
// It goes through the ROUTE router, which already drives the DNS router: a Box accessor for the DNS
// router would be a shared-interface change for one explanation stage. The preview itself is
// read-only (it runs the same rule matcher on a private metadata copy, with no query, no walk and
// no cache write), so adding this stage cannot change what the test then measures.
func (p *boxConnectionTestPath) PreviewDNSRule(network string, domain string, port uint16) (connectionTestDNSPreview, bool) {
	routeRouter, isRouteRouter := p.box.Router().(*route.Router)
	if !isRouteRouter || routeRouter == nil {
		return connectionTestDNSPreview{}, false
	}
	// The eventual dial resolves an address record. `network` and `port` describe the CONNECTION,
	// not the query, so they are deliberately not passed into the DNS question.
	_ = network
	_ = port
	result, previewed := routeRouter.PreviewDNSLookup(context.Background(), dns.PreviewQuery{
		Name:  domain,
		QType: mDNS.TypeA,
	})
	if !previewed {
		return connectionTestDNSPreview{}, false
	}
	unknown := ""
	if result.Unknown {
		// The preview states a BOOLEAN plus the name of the missing fact; this report wants the
		// sentence a reader can act on.
		unknown = "the DNS decision depends on " + result.DependsOn + " this preview was not given"
		if result.DependsOn == "" {
			unknown = "the DNS decision depends on a fact this preview was not given"
		}
	}
	return connectionTestDNSPreview{
		Matched:    result.Matched,
		RuleIndex:  result.RuleIndex,
		Rule:       result.Rule,
		ActionType: result.ActionType,
		Server:     result.Server,
		ServerType: result.ServerType,
		Unknown:    unknown,
	}, true
}
