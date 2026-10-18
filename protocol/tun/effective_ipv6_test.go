package tun

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	"github.com/sagernet/sing-box/log"
	singTun "github.com/sagernet/sing-tun"

	"github.com/stretchr/testify/require"
	"go4.org/netipx"
)

// The effective-options half of the entry decision. Its tests are the two things a wrong
// implementation would do silently:
//
//   - leave one IPv6 fact behind, so the platform still learns the family while the decision says
//     it was removed - the route_address_set bypass is the concrete case;
//   - remove one IPv6 fact too many, so the interface has no address (or no resolver) and the box
//     cannot start at all.

func mustPrefix(address string) netip.Prefix {
	return netip.MustParsePrefix(address)
}

func mustAddr(address string) netip.Addr {
	return netip.MustParseAddr(address)
}

// TestTunOptionsPresentIPv6IsTheDemandGate pins the cheap check that decides whether the observation
// is worth doing at all. A false positive here costs one interface enumeration at startup; a false
// negative leaves the whole entry decision unapplied.
func TestTunOptionsPresentIPv6IsTheDemandGate(t *testing.T) {
	testCases := []struct {
		name     string
		options  singTun.Options
		expected bool
	}{
		{
			name:     "empty options",
			options:  singTun.Options{},
			expected: false,
		},
		{
			name: "ipv4 only with auto route",
			options: singTun.Options{
				Inet4Address: []netip.Prefix{mustPrefix("172.19.0.1/30")},
				AutoRoute:    true,
			},
			// auto_route alone installs no IPv6 route: every stack in this tree derives the
			// automatic range from the address list.
			expected: false,
		},
		{
			name: "address",
			options: singTun.Options{
				Inet4Address: []netip.Prefix{mustPrefix("172.19.0.1/30")},
				Inet6Address: []netip.Prefix{mustPrefix("fdfe:dcba:9876::1/126")},
			},
			expected: true,
		},
		{
			name: "route only",
			options: singTun.Options{
				Inet4RouteAddress: []netip.Prefix{mustPrefix("0.0.0.0/1")},
				Inet6RouteAddress: []netip.Prefix{mustPrefix("::/1")},
			},
			expected: true,
		},
		{
			name: "exclude route only",
			options: singTun.Options{
				Inet6RouteExcludeAddress: []netip.Prefix{mustPrefix("2001:db8::/32")},
			},
			expected: true,
		},
		{
			name: "loopback address only",
			options: singTun.Options{
				Inet6LoopbackAddress: []netip.Addr{mustAddr("fdfe:dcba:9876::2")},
			},
			expected: true,
		},
		{
			name: "ipv6 dns address only",
			options: singTun.Options{
				Inet4Address: []netip.Prefix{mustPrefix("172.19.0.1/30")},
				DNSAddress:   []netip.Addr{mustAddr("fdfe:dcba:9876::2")},
			},
			expected: true,
		},
		{
			name: "ipv4 dns address only",
			options: singTun.Options{
				Inet4Address: []netip.Prefix{mustPrefix("172.19.0.1/30")},
				DNSAddress:   []netip.Addr{mustAddr("172.19.0.2")},
			},
			expected: false,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.expected, tunOptionsPresentIPv6(&testCase.options))
		})
	}
}

// TestApplyIPv4OnlyPresentmentRemovesEveryField is the completeness rule, field by field.
func TestApplyIPv4OnlyPresentmentRemovesEveryField(t *testing.T) {
	options := singTun.Options{
		Inet4Address:             []netip.Prefix{mustPrefix("172.19.0.1/30")},
		Inet6Address:             []netip.Prefix{mustPrefix("fdfe:dcba:9876::1/126")},
		Inet4Gateway:             mustAddr("172.19.0.1"),
		Inet6Gateway:             mustAddr("fdfe:dcba:9876::1"),
		DNSAddress:               []netip.Addr{mustAddr("172.19.0.2"), mustAddr("fdfe:dcba:9876::2")},
		Inet4RouteAddress:        []netip.Prefix{mustPrefix("0.0.0.0/1")},
		Inet6RouteAddress:        []netip.Prefix{mustPrefix("::/0")},
		Inet4RouteExcludeAddress: []netip.Prefix{mustPrefix("10.0.0.0/8")},
		Inet6RouteExcludeAddress: []netip.Prefix{mustPrefix("2001:db8::/32")},
		Inet4LoopbackAddress:     []netip.Addr{mustAddr("172.19.0.2")},
		Inet6LoopbackAddress:     []netip.Addr{mustAddr("fdfe:dcba:9876::2")},
	}

	// The configuration's own slice, kept to prove the reduction did not write through the alias.
	configuredDNS := []netip.Addr{mustAddr("172.19.0.2"), mustAddr("fdfe:dcba:9876::2")}

	require.True(t, applyIPv4OnlyPresentment(&options))

	require.Empty(t, options.Inet6Address)
	require.Empty(t, options.Inet6RouteAddress)
	require.Empty(t, options.Inet6RouteExcludeAddress)
	require.Empty(t, options.Inet6LoopbackAddress)
	require.False(t, options.Inet6Gateway.IsValid())
	require.Equal(t, []netip.Addr{mustAddr("172.19.0.2")}, []netip.Addr(options.DNSAddress),
		"the IPv4 hijack resolver must survive; it is the only resolver the interface has left")

	// The IPv4 half of every field is untouched.
	require.Equal(t, []netip.Prefix{mustPrefix("172.19.0.1/30")}, options.Inet4Address)
	require.Equal(t, []netip.Prefix{mustPrefix("0.0.0.0/1")}, options.Inet4RouteAddress)
	require.Equal(t, []netip.Prefix{mustPrefix("10.0.0.0/8")}, options.Inet4RouteExcludeAddress)
	require.Equal(t, []netip.Addr{mustAddr("172.19.0.2")}, options.Inet4LoopbackAddress)
	require.Equal(t, mustAddr("172.19.0.1"), options.Inet4Gateway)

	require.Equal(t, configuredDNS, []netip.Addr{mustAddr("172.19.0.2"), mustAddr("fdfe:dcba:9876::2")},
		"the typed configuration's slice must not be modified: the decision owns the effective "+
			"options, and a reload rebuilds from the same configuration")
	require.NotEqual(t, configuredDNS, []netip.Addr(options.DNSAddress),
		"and the effective options must be a new slice, not the configuration's")
}

// TestApplyIPv4OnlyPresentmentRefusesToBreakTheInterface is the fail-safe half.
func TestApplyIPv4OnlyPresentmentRefusesToBreakTheInterface(t *testing.T) {
	t.Run("no ipv4 address", func(t *testing.T) {
		options := singTun.Options{
			Inet6Address: []netip.Prefix{mustPrefix("2001:db8::1/64")},
		}
		before := options
		require.False(t, applyIPv4OnlyPresentment(&options),
			"deleting the last address would leave an interface that cannot be opened")
		require.Equal(t, before, options, "a refused reduction must not modify anything")
	})

	t.Run("dns address with no ipv4 entry", func(t *testing.T) {
		options := singTun.Options{
			Inet4Address: []netip.Prefix{mustPrefix("172.19.0.1/30")},
			Inet6Address: []netip.Prefix{mustPrefix("fdfe:dcba:9876::1/126")},
			DNSAddress:   []netip.Addr{mustAddr("fdfe:dcba:9876::2")},
		}
		before := options
		require.False(t, applyIPv4OnlyPresentment(&options),
			"removing the only named resolver would leave applications with no DNS server")
		require.Equal(t, before, options)
	})
}

// TestAppendRouteSetPrefixesSkipsIPv6WhenSuppressed is the second-entry-point rule: the leading
// address lists are not the only way IPv6 reaches the platform.
func TestAppendRouteSetPrefixesSkipsIPv6WhenSuppressed(t *testing.T) {
	var routeSetBuilder netipx.IPSetBuilder
	routeSetBuilder.AddPrefix(mustPrefix("0.0.0.0/1"))
	routeSetBuilder.AddPrefix(mustPrefix("::/1"))
	routeSet, err := routeSetBuilder.IPSet()
	require.NoError(t, err)

	var excludeSetBuilder netipx.IPSetBuilder
	excludeSetBuilder.AddPrefix(mustPrefix("10.0.0.0/8"))
	excludeSetBuilder.AddPrefix(mustPrefix("2001:db8::/32"))
	excludeSet, err := excludeSetBuilder.IPSet()
	require.NoError(t, err)

	suppressed := singTun.Options{}
	appendRouteSetPrefixes(&suppressed, []*netipx.IPSet{routeSet}, []*netipx.IPSet{excludeSet}, true)
	require.Equal(t, []netip.Prefix{mustPrefix("0.0.0.0/1")}, suppressed.Inet4RouteAddress)
	require.Equal(t, []netip.Prefix{mustPrefix("10.0.0.0/8")}, suppressed.Inet4RouteExcludeAddress)
	require.Empty(t, suppressed.Inet6RouteAddress,
		"an IPv6 route from a route_address_set is the bypass this decision exists to close")
	require.Empty(t, suppressed.Inet6RouteExcludeAddress)

	presented := singTun.Options{}
	appendRouteSetPrefixes(&presented, []*netipx.IPSet{routeSet}, []*netipx.IPSet{excludeSet}, false)
	require.Equal(t, []netip.Prefix{mustPrefix("::/1")}, presented.Inet6RouteAddress,

		"and with the decision present, the same set applies exactly what it always did")
	require.Equal(t, []netip.Prefix{mustPrefix("2001:db8::/32")}, presented.Inet6RouteExcludeAddress)
}

// entryPresenterStub is the network manager's entry presenter, reduced to the two methods the
// inbound uses.
//
// It embeds the interface so it satisfies adapter.NetworkManager without implementing the rest: the
// inbound reaches it only through the optional interface, and a hand-written full fake would be a
// page of methods no test in this file depends on.
type entryPresenterStub struct {
	adapter.NetworkManager
	presentment netcap.IPv6Presentment
	reads       int
	commits     []netcap.IPv6Presentment
}

func (s *entryPresenterStub) IPv6EntryPresentment() netcap.IPv6Presentment {
	s.reads++
	return s.presentment
}

func (s *entryPresenterStub) CommitIPv6EntryPresentment(presentment netcap.IPv6Presentment) {
	s.commits = append(s.commits, presentment)
}

func entryDecisionInbound(t *testing.T, manager adapter.NetworkManager, options singTun.Options) *Inbound {
	t.Helper()
	return &Inbound{
		logger:         log.NewNOPFactory().Logger(),
		networkManager: manager,
		tunOptions:     options,
	}
}

// TestAPresentDecisionDoesNotTouchTheOptions is the ordinary dual-stack start.
func TestAPresentDecisionDoesNotTouchTheOptions(t *testing.T) {
	presenter := &entryPresenterStub{presentment: netcap.PresentIPv6}
	options := singTun.Options{
		Inet4Address: []netip.Prefix{mustPrefix("172.19.0.1/30")},
		Inet6Address: []netip.Prefix{mustPrefix("fdfe:dcba:9876::1/126")},
	}
	inbound := entryDecisionInbound(t, presenter, options)

	inbound.applyEntryIPv6Presentment()

	require.Equal(t, options, inbound.tunOptions)
	require.False(t, inbound.entryIPv4Only)
	require.Empty(t, presenter.commits, "a 'present' decision needs no correction")
	require.Equal(t, 1, presenter.reads, "the decision is read once, not per field")
}

// TestASuppressedDecisionReducesTheEffectiveOptions is the applied path.
//
// Nothing is committed here: the manager published the decision before any inbound started, and the
// inbound only commits when it has to CORRECT it.
func TestASuppressedDecisionReducesTheEffectiveOptions(t *testing.T) {
	presenter := &entryPresenterStub{presentment: netcap.SuppressIPv6}
	inbound := entryDecisionInbound(t, presenter, singTun.Options{
		Inet4Address:             []netip.Prefix{mustPrefix("172.19.0.1/30")},
		Inet6Address:             []netip.Prefix{mustPrefix("fdfe:dcba:9876::1/126")},
		DNSAddress:               []netip.Addr{mustAddr("172.19.0.2"), mustAddr("fdfe:dcba:9876::2")},
		Inet6RouteAddress:        []netip.Prefix{mustPrefix("::/0")},
		Inet6RouteExcludeAddress: []netip.Prefix{mustPrefix("2001:db8::/32")},
		Inet6LoopbackAddress:     []netip.Addr{mustAddr("fdfe:dcba:9876::2")},
	})
	inbound.dnsHijackAddress = []netip.Addr{mustAddr("172.19.0.2"), mustAddr("fdfe:dcba:9876::2")}

	inbound.applyEntryIPv6Presentment()

	require.True(t, inbound.entryIPv4Only)
	require.Empty(t, inbound.tunOptions.Inet6Address)
	require.Empty(t, inbound.tunOptions.Inet6RouteAddress)
	require.Empty(t, inbound.tunOptions.Inet6RouteExcludeAddress)
	require.Empty(t, inbound.tunOptions.Inet6LoopbackAddress)
	require.Equal(t, []netip.Addr{mustAddr("172.19.0.2")}, []netip.Addr(inbound.tunOptions.DNSAddress))
	require.Equal(t, []netip.Addr{mustAddr("172.19.0.2")}, inbound.dnsHijackAddress,
		"the DNS hijack list is derived from the addresses; a removed address must not stay there")
	require.Empty(t, presenter.commits,
		"the applied decision is already the manager's; a second commit of the same value would only "+
			"widen the window a reader could see)")
}

// TestARefusedReductionCorrectsTheDecision is the one case the inbound writes back, and the reason
// the write must exist: an interface that still presents IPv6 cannot sit beside a DNS entry that
// refuses AAAA.
func TestARefusedReductionCorrectsTheDecision(t *testing.T) {
	presenter := &entryPresenterStub{presentment: netcap.SuppressIPv6}
	options := singTun.Options{
		// An IPv6-only TUN, which cannot be reduced to IPv4 without deleting its only address.
		Inet6Address: []netip.Prefix{mustPrefix("2001:db8::1/64")},
	}
	inbound := entryDecisionInbound(t, presenter, options)

	inbound.applyEntryIPv6Presentment()

	require.Equal(t, options, inbound.tunOptions)
	require.False(t, inbound.entryIPv4Only)
	require.Equal(t, []netcap.IPv6Presentment{netcap.PresentIPv6}, presenter.commits,
		"the interface still presents IPv6, so the recorded decision must be corrected to say so")
}

// TestAManagerWithoutTheCapabilityChangesNothing covers the optional-interface degradation.
func TestAManagerWithoutTheCapabilityChangesNothing(t *testing.T) {
	var manager adapter.NetworkManager // a manager that does not implement the presenter
	options := singTun.Options{
		Inet4Address: []netip.Prefix{mustPrefix("172.19.0.1/30")},
		Inet6Address: []netip.Prefix{mustPrefix("fdfe:dcba:9876::1/126")},
	}
	inbound := entryDecisionInbound(t, manager, options)

	require.NotPanics(t, inbound.applyEntryIPv6Presentment)
	require.Equal(t, options, inbound.tunOptions)
	require.False(t, inbound.entryIPv4Only)
}
