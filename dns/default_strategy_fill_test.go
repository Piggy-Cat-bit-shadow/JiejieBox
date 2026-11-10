package dns

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	mDNS "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// A caller-forced family must not be cancelled by the router's opposite default (§E complete Lookup).
//
// # The defect these pin
//
// service/resolved's ResolveHostname maps the D-Bus caller's address family to a strict strategy and
// calls Router.Lookup with ONLY LookupStrategy set:
//
//	responseAddresses, lookupErr := t.dnsRouter.Lookup(ctx, hostname, adapter.DNSQueryOptions{
//	    LookupStrategy: strategy,
//	})
//
// The router then filled its configured defaultDomainStrategy into Strategy because Strategy was
// AsIS. When the default was the opposite strict family the client saw two disagreeing strict
// families, treated the item as forbidden, issued NOTHING, and the complete lookup returned
// `empty result` - a D-Bus hostname resolution failing on a configuration that is internally
// consistent. The default is a default: it may fill an empty Strategy, it may not overrule a
// decision the caller already made.
//
// A genuine rule restriction still wins: a legacy rule writes Strategy during the walk, after the
// fill, and the forbidden-family rule still stops the query before the cache and before the wire.

// resolvedLookupOptions is the exact option shape service/resolved uses.
func resolvedLookupOptions(family C.DomainStrategy) adapter.DNSQueryOptions {
	return adapter.DNSQueryOptions{LookupStrategy: family}
}

// TestResolvedHostnameShapeSurvivesAnOppositeDefault covers the three lookup paths that can reach a
// complete lookup, both family directions.
func TestResolvedHostnameShapeSurvivesAnOppositeDefault(t *testing.T) {
	for _, mode := range []struct {
		name   string
		legacy bool
		modern bool
	}{
		{name: "legacy rule mode", legacy: true},
		{name: "modern rule mode", modern: true},
	} {
		for _, direction := range []struct {
			name     string
			forced   C.DomainStrategy
			def      C.DomainStrategy
			qtype    uint16
			expected string
		}{
			{"ipv4 forced over an ipv6 default", C.DomainStrategyIPv4Only, C.DomainStrategyIPv6Only, mDNS.TypeA, "192.0.2.1"},
			{"ipv6 forced over an ipv4 default", C.DomainStrategyIPv6Only, C.DomainStrategyIPv4Only, mDNS.TypeAAAA, "2001:db8::1"},
		} {
			t.Run(mode.name+"/"+direction.name, func(t *testing.T) {
				transport := &queryRecordingTransport{addressA: "192.0.2.1", address6: "2001:db8::1"}
				router := legacyRuleRouter(t, transport, nil)
				router.legacyDNSMode = mode.legacy
				router.defaultDomainStrategy = direction.def

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				addresses, err := router.Lookup(ctx, "resolved.test.", resolvedLookupOptions(direction.forced))
				require.NoError(t, err,
					"the caller's forced family must still be queried; observed %v", transport.observed())
				require.Equal(t, []uint16{direction.qtype}, transport.observed(),
					"only the forced family may be sent")
				require.Len(t, addresses, 1)
				require.Equal(t, direction.expected, addresses[0].String())
			})
		}
	}
}

// TestResolvedHostnameShapeWithAnExplicitTransport is the domain_resolver path: the same shape, but
// the transport is named, which bypasses the rule walk entirely.
func TestResolvedHostnameShapeWithAnExplicitTransport(t *testing.T) {
	transport := &queryRecordingTransport{addressA: "192.0.2.1", address6: "2001:db8::1"}
	router := legacyRuleRouter(t, transport, nil)
	router.defaultDomainStrategy = C.DomainStrategyIPv6Only

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	options := resolvedLookupOptions(C.DomainStrategyIPv4Only)
	options.Transport = transport
	addresses, err := router.Lookup(ctx, "explicit.test.", options)
	require.NoError(t, err)
	require.Equal(t, []uint16{mDNS.TypeA}, transport.observed())
	require.Len(t, addresses, 1)
	require.Equal(t, "192.0.2.1", addresses[0].String())
}

// TestDefaultStrategyStillFillsAnEmptyQuery is the control: with no caller decision at all the
// router's default must still apply, or the guard would have disabled the default.
func TestDefaultStrategyStillFillsAnEmptyQuery(t *testing.T) {
	transport := &queryRecordingTransport{addressA: "192.0.2.1", address6: "2001:db8::1"}
	router := legacyRuleRouter(t, transport, nil)
	router.defaultDomainStrategy = C.DomainStrategyIPv4Only

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	addresses, err := router.Lookup(ctx, "default.test.", adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.Equal(t, []uint16{mDNS.TypeA}, transport.observed(),
		"a query that expressed no preference must still follow the router's default")
	require.Len(t, addresses, 1)
	require.Equal(t, "192.0.2.1", addresses[0].String())
}

// TestRuleStrictFamilyStillForbidsBeforeTheCache keeps the restriction side of the contract: a real
// legacy rule that forbids a family must stop the item even when the family is warm in the cache,
// and it must do so without sending the query.
func TestRuleStrictFamilyStillForbidsBeforeTheCache(t *testing.T) {
	transport := &queryRecordingTransport{addressA: "192.0.2.1", address6: "2001:db8::1"}
	client := NewClient(ClientOptions{Context: context.Background(), Logger: log.NewNOPFactory().Logger()})
	client.Start()
	manager := &fakeDNSTransportManager{
		transports:       map[string]adapter.DNSTransport{"final": transport},
		defaultTransport: transport,
	}
	build := func(rules []option.DNSRule, legacy bool) *Router {
		return &Router{
			ctx:           context.Background(),
			logger:        log.NewNOPFactory().Logger(),
			client:        client,
			transport:     manager,
			rules:         raceTestRules(t, rules),
			legacyDNSMode: legacy,
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Warm BOTH families in the shared client.
	warm := build(nil, false)
	var warmResults familyResults
	require.NoError(t, warm.LookupFamilies(ctx, "cache.test.", adapter.DNSQueryOptions{}, warmResults.publish))
	require.Contains(t, transport.observed(), mDNS.TypeA)
	require.Contains(t, transport.observed(), mDNS.TypeAAAA)
	queriesAfterWarm := len(transport.observed())

	// The same client, now behind a rule that forbids IPv6 for the name.
	ruled := build([]option.DNSRule{legacyFamilyStrategyRule("final", C.DomainStrategyIPv4Only)}, true)
	var ruledResults familyResults
	require.NoError(t, ruled.LookupFamilies(ctx, "cache.test.", adapter.DNSQueryOptions{}, ruledResults.publish))

	observed := transport.observed()
	for _, qType := range observed[queriesAfterWarm:] {
		require.NotEqual(t, mDNS.TypeAAAA, qType,
			"the forbidden family must not be sent, cached or not; observed %v", observed)
	}
	for _, result := range ruledResults.published() {
		if result.IPv6 {
			require.Empty(t, result.Addresses,
				"a forbidden family must not be served from the warm cache")
		}
	}
	requireAddressFamily(t, ruledResults.published(), false, "192.0.2.1")
}
