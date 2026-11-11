package route

// O02/O01 — the DNS half of the explanation, reached through the object a management caller holds.
//
// A connection test holds the ROUTE router (Box.Router). It does not hold the DNS router, and adding
// an accessor for it to the Box would be a shared-interface change for one explanation stage. The
// route layer already drives the DNS router, so it exposes the read-only DNS preview through itself.

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/dns"

	mDNS "github.com/miekg/dns"

	"github.com/stretchr/testify/require"
)

// fakeDNSPreviewer is a DNS router that can explain a query, and records what it was asked.
type fakeDNSPreviewer struct {
	adapter.DNSRouter
	result dns.PreviewResult
	got    dns.PreviewQuery
}

func (f *fakeDNSPreviewer) PreviewLookup(_ context.Context, query dns.PreviewQuery) dns.PreviewResult {
	f.got = query
	return f.result
}

func TestTheRouteRouterExposesTheDNSPreview(t *testing.T) {
	previewer := &fakeDNSPreviewer{result: dns.PreviewResult{
		Matched:    true,
		RuleIndex:  1,
		Rule:       "domain_suffix:example.com",
		ActionType: "route",
		Server:     "dns-main",
		ServerType: "udp",
	}}
	router := &Router{dns: previewer}

	result, ok := router.PreviewDNSLookup(context.Background(), dns.PreviewQuery{Name: "example.com", QType: mDNS.TypeA})
	require.True(t, ok)
	require.Equal(t, "dns-main", result.Server)
	require.Equal(t, 1, result.RuleIndex)
	require.Equal(t, "example.com", previewer.got.Name, "the query is passed through unchanged")
	require.Equal(t, uint16(mDNS.TypeA), previewer.got.QType)

	// A DNS router without the capability answers false instead of a zero result that looks like
	// "no rule matched".
	plain := &Router{dns: &verdictDNSRouter{}}
	_, ok = plain.PreviewDNSLookup(context.Background(), dns.PreviewQuery{Name: "example.com"})
	require.False(t, ok)

	// And a router with no DNS router at all answers false too.
	_, ok = (&Router{}).PreviewDNSLookup(context.Background(), dns.PreviewQuery{Name: "example.com"})
	require.False(t, ok)

	// A nil router is not a panic.
	var nilRouter *Router
	_, ok = nilRouter.PreviewDNSLookup(context.Background(), dns.PreviewQuery{})
	require.False(t, ok)
}
