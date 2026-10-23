package route

import (
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/service/powerreport"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// The eligibility gate as a single-variable inversion matrix.
//
// # Why the existing tests were not enough
//
// fast_bypass_test.go proves each condition refuses, but almost every row asserts
// `NotEqual(PreMatchBypass)` - a boolean. The verdict is a diagnostic that the brief asks to be
// exact: an operator reading "the outbound chain is not a single leaf" for a FakeIP flow has been
// told the wrong thing, and a condition inserted in the wrong order would leave every one of those
// tests green. This file changes exactly one input per row and asserts the exact BypassVerdict, then
// pins the order of the conditions with combinations.
//
// The baseline is asserted first: if it stopped being eligible, every refusal row below would pass
// for the wrong reason.

// eligibilityRow is one single-variable inversion.
type eligibilityRow struct {
	name      string
	mutate    func(metadata *adapter.InboundContext)
	configure func(t *testing.T, router *Router)
	want      BypassVerdict
}

func TestEveryBypassConditionRefusesWithItsOwnVerdict(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")
	strategy := C.NetworkStrategyFallback

	rows := []eligibilityRow{
		{
			name: "baseline: nothing changed",
			want: BypassAllowed,
		},
		{
			name:   "inbound is not TUN",
			mutate: func(m *adapter.InboundContext) { m.InboundType = "socks-in" },
			want:   BypassRefusedInboundType,
		},
		{
			name:   "network is ICMP",
			mutate: func(m *adapter.InboundContext) { m.Network = N.NetworkICMP },
			want:   BypassRefusedNetwork,
		},
		{
			name:   "network is unknown",
			mutate: func(m *adapter.InboundContext) { m.Network = "udplite" },
			want:   BypassRefusedNetwork,
		},
		{
			name:   "destination is a domain",
			mutate: func(m *adapter.InboundContext) { m.Destination = M.ParseSocksaddr("example.com:443") },
			want:   BypassRefusedDomainDestination,
		},
		{
			name:   "destination is FakeIP",
			mutate: func(m *adapter.InboundContext) { m.FakeIP = true },
			want:   BypassRefusedFakeIP,
		},
		{
			name:   "a domain was sniffed",
			mutate: func(m *adapter.InboundContext) { m.Domain = "example.com" },
			want:   BypassRefusedSniffedDomain,
		},
		{
			name: "a resolved candidate list was carried",
			mutate: func(m *adapter.InboundContext) {
				m.DestinationAddresses = []netip.Addr{netip.MustParseAddr("93.184.216.34")}
			},
			want: BypassRefusedResolvedCandidates,
		},
		{
			name: "a rule rewrote the destination",
			mutate: func(m *adapter.InboundContext) {
				m.Destination = M.ParseSocksaddr("93.184.216.35:443")
			},
			want: BypassRefusedDestinationRewritten,
		},
		{
			name: "a rule recorded an original destination",
			mutate: func(m *adapter.InboundContext) {
				m.RouteOriginalDestination = M.ParseSocksaddr("93.184.216.35:443")
			},
			want: BypassRefusedRouteOriginalDestination,
		},
		{
			name:   "UoT carries per-datagram destinations",
			mutate: func(m *adapter.InboundContext) { m.UoTDatagramDestinations = true },
			want:   BypassRefusedUoTDatagramDestinations,
		},
		{
			name:   "connected UDP",
			mutate: func(m *adapter.InboundContext) { m.UDPConnect = true },
			want:   BypassRefusedUDPConnect,
		},
		{
			name:   "domain unmapping is enabled",
			mutate: func(m *adapter.InboundContext) { m.UDPDisableDomainUnmapping = true },
			want:   BypassRefusedUDPDomainUnmapping,
		},
		{
			name:   "a custom UDP timeout is configured",
			mutate: func(m *adapter.InboundContext) { m.UDPTimeout = 30 * time.Second },
			want:   BypassRefusedCustomUDPTimeout,
		},
		{
			name:   "a network strategy is configured",
			mutate: func(m *adapter.InboundContext) { m.NetworkStrategy = &strategy },
			want:   BypassRefusedNetworkOptions,
		},
		{
			name: "a network type is configured",
			mutate: func(m *adapter.InboundContext) {
				m.NetworkType = []C.InterfaceType{C.InterfaceTypeWIFI}
			},
			want: BypassRefusedNetworkOptions,
		},
		{
			name: "a fallback network type is configured",
			mutate: func(m *adapter.InboundContext) {
				m.FallbackNetworkType = []C.InterfaceType{C.InterfaceTypeEthernet}
			},
			want: BypassRefusedNetworkOptions,
		},
		{
			name:   "a fallback delay is configured",
			mutate: func(m *adapter.InboundContext) { m.FallbackDelay = time.Second },
			want:   BypassRefusedNetworkOptions,
		},
		{
			name:   "TLS fragmentation is configured",
			mutate: func(m *adapter.InboundContext) { m.TLSFragment = true },
			want:   BypassRefusedTLSOptions,
		},
		{
			name:   "TLS record fragmentation is configured",
			mutate: func(m *adapter.InboundContext) { m.TLSRecordFragment = true },
			want:   BypassRefusedTLSOptions,
		},
		{
			name:   "TLS spoofing is configured",
			mutate: func(m *adapter.InboundContext) { m.TLSSpoof = "chrome" },
			want:   BypassRefusedTLSOptions,
		},
		{
			name: "a tracker is attached",
			configure: func(t *testing.T, router *Router) {
				router.trackers = []adapter.ConnectionTracker{noopTracker{}}
			},
			want: BypassRefusedTracker,
		},
		{
			name: "a power report recorder is counting",
			configure: func(t *testing.T, router *Router) {
				router.powerReport = powerreport.NewManager()
				require.NoError(t, router.powerReport.Start(powerreport.Options{BasePath: t.TempDir()}))
				t.Cleanup(func() { router.powerReport.Close() })
			},
			want: BypassRefusedPowerReport,
		},
		{
			name: "process metadata could not be proven",
			configure: func(t *testing.T, router *Router) {
				router.processSearcher = &flakyProcessSearcher{failFor: 1 << 20}
				router.platformInterface = &localSourcePlatform{address: netip.MustParseAddr("192.168.1.2")}
			},
			want: BypassRefusedProcessMetadataUnproven,
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			router, outbound := eligibleRouter(t)
			if row.configure != nil {
				row.configure(t, router)
			}
			metadata := fastBypassMetadata(N.NetworkTCP, destination)
			if row.mutate != nil {
				row.mutate(&metadata)
			}
			verdict := router.canFastBypass(&metadata, destination,
				[]adapter.Outbound{outbound}, outbound, false)
			require.Equal(t, row.want, verdict,
				"%s: expected %q, got %q", row.name, row.want, verdict)
		})
	}
}

// TestBypassChainAndLeafConditionsRefuseWithTheirOwnVerdict covers the three conditions that are
// about the chain rather than the metadata, so they cannot share the single-variable table above.
func TestBypassChainAndLeafConditionsRefuseWithTheirOwnVerdict(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")

	t.Run("a group chain without authorisation", func(t *testing.T) {
		leaf := eligibleLeaf()
		group := &transparentGroup{tag: "group", selected: leaf}
		router := fastPathRouter(t, group, FastPathPolicies{})
		metadata := fastBypassMetadata(N.NetworkTCP, destination)
		chain, err := resolveOutbound(group, &metadata, N.NetworkTCP, true)
		require.NoError(t, err)
		require.Equal(t, BypassRefusedOutboundChain,
			router.canFastBypass(&metadata, destination, chain, leaf, false))
	})

	t.Run("the selected leaf is not bypassable", func(t *testing.T) {
		leaf := &nonBypassableOutbound{}
		router, _ := eligibleRouter(t)
		metadata := fastBypassMetadata(N.NetworkTCP, destination)
		require.Equal(t, BypassRefusedOutboundNotBypassable,
			router.canFastBypass(&metadata, destination, []adapter.Outbound{leaf}, leaf, false))
	})

	t.Run("the leaf cannot reproduce its own dial semantics", func(t *testing.T) {
		leaf := &configuredDirectOutbound{}
		router, _ := eligibleRouter(t)
		metadata := fastBypassMetadata(N.NetworkTCP, destination)
		require.Equal(t, BypassRefusedOutboundSemantics,
			router.canFastBypass(&metadata, destination, []adapter.Outbound{leaf}, leaf, false))
	})
}

// TestTheBypassConditionsAreOrdered is the ordering half. Each row sets two conditions that both
// refuse and asserts the verdict of the FIRST one in canFastBypass's order.
//
// The order is part of the product: the verdict tells an operator what to change, and "a tracker is
// attached" is actionable while "the destination was rewritten" is not, for a flow that has both.
// It is also the order the brief asks to be cheap-and-selective first, so the assertion is on the
// reason rather than on a boolean.
func TestTheBypassConditionsAreOrdered(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")

	rows := []struct {
		name      string
		mutate    func(*adapter.InboundContext)
		configure func(t *testing.T, router *Router)
		want      BypassVerdict
	}{
		{
			name: "inbound type precedes everything else",
			mutate: func(m *adapter.InboundContext) {
				m.InboundType = "socks-in"
				m.Network = N.NetworkICMP
				m.FakeIP = true
				m.Domain = "example.com"
				m.TLSFragment = true
			},
			want: BypassRefusedInboundType,
		},
		{
			name: "network precedes the destination shape",
			mutate: func(m *adapter.InboundContext) {
				m.Network = N.NetworkICMP
				m.Destination = M.ParseSocksaddr("example.com:443")
				m.FakeIP = true
			},
			want: BypassRefusedNetwork,
		},
		{
			name: "domain destination precedes FakeIP",
			mutate: func(m *adapter.InboundContext) {
				m.Destination = M.ParseSocksaddr("example.com:443")
				m.FakeIP = true
			},
			want: BypassRefusedDomainDestination,
		},
		{
			name: "FakeIP precedes a sniffed domain",
			mutate: func(m *adapter.InboundContext) {
				m.FakeIP = true
				m.Domain = "example.com"
			},
			want: BypassRefusedFakeIP,
		},
		{
			name: "TLS rewrite precedes the tracker",
			mutate: func(m *adapter.InboundContext) {
				m.TLSFragment = true
			},
			configure: func(t *testing.T, router *Router) {
				router.trackers = []adapter.ConnectionTracker{noopTracker{}}
			},
			want: BypassRefusedTLSOptions,
		},
		{
			name: "the tracker precedes the power report",
			configure: func(t *testing.T, router *Router) {
				router.trackers = []adapter.ConnectionTracker{noopTracker{}}
				router.powerReport = powerreport.NewManager()
				require.NoError(t, router.powerReport.Start(powerreport.Options{BasePath: t.TempDir()}))
				t.Cleanup(func() { router.powerReport.Close() })
			},
			want: BypassRefusedTracker,
		},
		{
			name: "the chain check precedes the leaf capability check",
			want: BypassRefusedOutboundChain,
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			router, outbound := eligibleRouter(t)
			if row.configure != nil {
				row.configure(t, router)
			}
			metadata := fastBypassMetadata(N.NetworkTCP, destination)
			if row.mutate != nil {
				row.mutate(&metadata)
			}

			chain := []adapter.Outbound{outbound}
			var leaf adapter.Outbound = outbound
			if row.want == BypassRefusedOutboundChain {
				// A two-element chain with no authorisation: the chain gate must fire before the
				// leaf is asked anything, so a non-bypassable leaf here would be the wrong reason.
				group := &transparentGroup{tag: "group", selected: &nonBypassableOutbound{}}
				resolved, err := resolveOutbound(group, &metadata, N.NetworkTCP, true)
				require.NoError(t, err)
				chain, leaf = resolved, resolved[len(resolved)-1]
			}

			require.Equal(t, row.want, router.canFastBypass(&metadata, destination, chain, leaf, false),
				"%s: the first failing condition must be the one reported", row.name)
		})
	}
}
