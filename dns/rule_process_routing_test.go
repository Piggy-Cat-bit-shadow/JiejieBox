package dns

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
)

// §E3's remaining flow: process matching and rule routing in the same lookup.
//
// A rule that requires BOTH a name and a process must select its own transport; a request from a
// different process must fall through to the default transport; a name routed by domain only must
// ignore the process; and a repeat must be a cache hit for the transport that answered. This drives
// the real legacy rule walk, the real ProcessItem matcher (filepath.Base of the reported process
// paths) and the real DNS client.

func processRoutingRule(server string, domainSuffix string, processName string) option.DNSRule {
	raw := option.RawDefaultDNSRule{DomainSuffix: badoption.Listable[string]{domainSuffix}}
	if processName != "" {
		raw.ProcessName = badoption.Listable[string]{processName}
	}
	return option.DNSRule{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultDNSRule{
			RawDefaultDNSRule: raw,
			DNSRuleAction: option.DNSRuleAction{
				Action: C.RuleActionTypeRoute,
				RouteOptions: option.DNSRouteActionOptions{
					Server: server,
				},
			},
		},
	}
}

func TestProcessMatchingAndRuleRoutingInOneFlow(t *testing.T) {
	generation := &atomic.Uint64{}
	generation.Store(1)
	processAddress := netip.MustParseAddr("192.0.2.10")
	defaultAddress := netip.MustParseAddr("192.0.2.20")
	// Repeated answers keep the fixture's index fallback from producing a different address when
	// the legacy walk queries a transport more than once.
	processTransport := newCountingSourceTransport(processAddress, processAddress, processAddress)
	processTransport.tag = "process"
	defaultTransport := newCountingSourceTransport(defaultAddress, defaultAddress, defaultAddress)
	defaultTransport.tag = "default"

	client := NewClient(ClientOptions{
		Context:           context.Background(),
		Logger:            log.NewNOPFactory().NewLogger("process-routing"),
		NetworkGeneration: func() uint64 { return generation.Load() },
	})
	client.Start()
	router := &Router{
		ctx:    context.Background(),
		logger: log.NewNOPFactory().Logger(),
		client: client,
		transport: &fakeDNSTransportManager{
			transports: map[string]adapter.DNSTransport{
				"process": processTransport,
				"default": defaultTransport,
			},
			defaultTransport: defaultTransport,
		},
		rules: raceTestRules(t, []option.DNSRule{
			processRoutingRule("process", "proc.test", "curl"),
			processRoutingRule("default", "plain.test", ""),
		}),
		legacyDNSMode: true,
	}

	lookup := func(processPath string, domain string) adapter.DNSFamilyResult {
		t.Helper()
		ctx, metadata := adapter.ExtendContext(context.Background())
		metadata.ProcessInfo = &adapter.ConnectionOwner{ProcessPaths: []string{processPath}}
		var results []adapter.DNSFamilyResult
		err := router.LookupFamilies(ctx, domain, adapter.DNSQueryOptions{
			LookupStrategy: C.DomainStrategyIPv4Only,
		}, func(result adapter.DNSFamilyResult) { results = append(results, result) })
		require.NoError(t, err)
		require.Len(t, results, 1)
		return results[0]
	}

	// The matching process selects its own transport, and the source is the exchange's.
	matched := lookup("/usr/bin/curl", "proc.test.")
	require.Equal(t, []netip.Addr{processAddress}, matched.Addresses)
	require.True(t, matched.HasSourceGeneration)
	require.EqualValues(t, 1, matched.SourceGeneration)
	require.Equal(t, 1, processTransport.exchangeCount())

	// A different process does not match the process rule; the name has no other route, so the
	// default transport answers instead.
	other := lookup("/usr/bin/wget", "proc.test.")
	require.Equal(t, []netip.Addr{defaultAddress}, other.Addresses)
	require.True(t, other.HasSourceGeneration)
	require.EqualValues(t, 1, other.SourceGeneration)
	require.Equal(t, 1, processTransport.exchangeCount(), "the process transport must not be used")

	// A name routed by domain only goes to its own transport, regardless of the process.
	byDomain := lookup("/usr/bin/curl", "plain.test.")
	require.Equal(t, []netip.Addr{defaultAddress}, byDomain.Addresses)
	require.Equal(t, 1, processTransport.exchangeCount())

	// A repeat of the first lookup is a hit for ITS transport and does not touch the process one.
	repeat := lookup("/usr/bin/curl", "proc.test.")
	require.Equal(t, []netip.Addr{processAddress}, repeat.Addresses)
	require.Equal(t, 1, processTransport.exchangeCount(), "the repeat must be a cache hit")
}
