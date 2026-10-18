package tun

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
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
//
// The third property, which the multi-TUN tests below pin, is that the decision is the BOX's: every
// interface either honours it or the box never commits it. An interface must never roll the shared
// value back after another interface has already applied it.

func mustPrefix(address string) netip.Prefix {
	return netip.MustParsePrefix(address)
}

func mustAddr(address string) netip.Addr {
	return netip.MustParseAddr(address)
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

// TestTheBoxPredicateAndTheReductionAgree is the anti-drift test for the shared safety rule.
//
// The box decides whether ANY tun may be reduced (option.AllTunInboundsReducibleToIPv4Only, built on
// option.TunReducibleToIPv4Only) and the reduction applies its own guard. If the two disagree for a
// single shape, one interface is stripped while the shared decision says otherwise - the split this
// design exists to prevent - so the two are asserted against the same table here.
func TestTheBoxPredicateAndTheReductionAgree(t *testing.T) {
	testCases := []struct {
		name       string
		inet4      []netip.Prefix
		dnsAddress []netip.Addr
		reducible  bool
	}{
		{
			name:      "ipv4 and ipv6 address",
			inet4:     []netip.Prefix{mustPrefix("172.19.0.1/30")},
			reducible: true,
		},
		{
			name:      "ipv4 only",
			inet4:     []netip.Prefix{mustPrefix("172.19.0.1/30")},
			reducible: true,
		},
		{
			name:       "mixed dns address list",
			inet4:      []netip.Prefix{mustPrefix("172.19.0.1/30")},
			dnsAddress: []netip.Addr{mustAddr("172.19.0.2"), mustAddr("fdfe:dcba:9876::2")},
			reducible:  true,
		},
		{
			name:       "ipv6-only dns address list",
			inet4:      []netip.Prefix{mustPrefix("172.19.0.1/30")},
			dnsAddress: []netip.Addr{mustAddr("fdfe:dcba:9876::2")},
			reducible:  false,
		},
		{
			name:       "ipv4 dns address only",
			inet4:      []netip.Prefix{mustPrefix("172.19.0.1/30")},
			dnsAddress: []netip.Addr{mustAddr("172.19.0.2")},
			reducible:  true,
		},
		{
			name:      "no ipv4 address",
			inet4:     nil,
			reducible: false,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.reducible,
				option.TunReducibleToIPv4Only(testCase.inet4, testCase.dnsAddress),
				"the box predicate")
			options := singTun.Options{
				Inet4Address: testCase.inet4,
				Inet6Address: []netip.Prefix{mustPrefix("fdfe:dcba:9876::1/126")},
				DNSAddress:   testCase.dnsAddress,
			}
			require.Equal(t, testCase.reducible, applyIPv4OnlyPresentment(&options),
				"the reduction")
		})
	}
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

// entryReaderStub is the network manager's reader, reduced to the one method the inbound uses.
//
// It embeds the interface so it satisfies adapter.NetworkManager without implementing the rest: the
// inbound reaches it only through the optional interface, and a hand-written full fake would be a
// page of methods no test in this file depends on.
//
// It is deliberately READ-ONLY from the inbound's point of view: there is no commit method, because
// the interface has none. A test that wanted to observe a rollback would have to invent the API this
// design removed.
type entryReaderStub struct {
	adapter.NetworkManager
	presentment netcap.IPv6Presentment
	reads       int
}

func (s *entryReaderStub) IPv6EntryPresentment() netcap.IPv6Presentment {
	s.reads++
	return s.presentment
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
	reader := &entryReaderStub{presentment: netcap.PresentIPv6}
	options := singTun.Options{
		Inet4Address: []netip.Prefix{mustPrefix("172.19.0.1/30")},
		Inet6Address: []netip.Prefix{mustPrefix("fdfe:dcba:9876::1/126")},
	}
	inbound := entryDecisionInbound(t, reader, options)

	inbound.applyEntryIPv6Presentment()

	require.Equal(t, options, inbound.tunOptions)
	require.False(t, inbound.entryIPv4Only)
	require.Equal(t, 1, reader.reads, "the decision is read once, not per field")
}

// TestASuppressedDecisionReducesTheEffectiveOptions is the applied path.
func TestASuppressedDecisionReducesTheEffectiveOptions(t *testing.T) {
	reader := &entryReaderStub{presentment: netcap.SuppressIPv6}
	inbound := entryDecisionInbound(t, reader, singTun.Options{
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
}

// TestARefusedReductionDoesNotRollBackTheDecision is the multi-TUN invariant at the interface that
// used to break it.
//
// With the box pre-check in place this state is a defect, not a configuration - but the DIRECTION
// still matters, because the shared value is read by the DNS entry: rolling it back to "present"
// would make applications receive AAAA for an IPv6 this very interface was just stripped of. The
// interface keeps its configured IPv6 and the shared value stays as the box committed it.
func TestARefusedReductionDoesNotRollBackTheDecision(t *testing.T) {
	reader := &entryReaderStub{presentment: netcap.SuppressIPv6}
	options := singTun.Options{
		// An IPv6-only TUN, which cannot be reduced to IPv4 without deleting its only address.
		Inet6Address: []netip.Prefix{mustPrefix("2001:db8::1/64")},
	}
	inbound := entryDecisionInbound(t, reader, options)

	inbound.applyEntryIPv6Presentment()

	require.Equal(t, options, inbound.tunOptions)
	require.False(t, inbound.entryIPv4Only)
	require.Equal(t, netcap.SuppressIPv6, reader.presentment,
		"the interface must not roll the shared decision back after another interface may have applied it")
}

// TestAManagerWithoutTheCapabilityChangesNothing covers the optional-interface degradation.
func TestAManagerWithoutTheCapabilityChangesNothing(t *testing.T) {
	var manager adapter.NetworkManager // a manager that does not implement the reader
	options := singTun.Options{
		Inet4Address: []netip.Prefix{mustPrefix("172.19.0.1/30")},
		Inet6Address: []netip.Prefix{mustPrefix("fdfe:dcba:9876::1/126")},
	}
	inbound := entryDecisionInbound(t, manager, options)

	require.NotPanics(t, inbound.applyEntryIPv6Presentment)
	require.Equal(t, options, inbound.tunOptions)
	require.False(t, inbound.entryIPv4Only)
}

// tunConfig is one TUN inbound of a test box, as both the typed configuration (for the box
// pre-check) and the effective options (for the inbound's application).
type tunConfig struct {
	inet4      []string
	inet6      []string
	dnsAddress []netip.Addr
}

func (c tunConfig) inboundOptions() option.Inbound {
	typed := &option.TunInboundOptions{}
	for _, prefix := range c.inet4 {
		typed.Address = append(typed.Address, mustPrefix(prefix))
	}
	for _, prefix := range c.inet6 {
		typed.Address = append(typed.Address, mustPrefix(prefix))
	}
	typed.DNSAddress = c.dnsAddress
	return option.Inbound{Type: C.TypeTun, Options: typed}
}

func (c tunConfig) effectiveOptions() singTun.Options {
	options := singTun.Options{DNSAddress: c.dnsAddress}
	for _, prefix := range c.inet4 {
		options.Inet4Address = append(options.Inet4Address, mustPrefix(prefix))
	}
	for _, prefix := range c.inet6 {
		options.Inet6Address = append(options.Inet6Address, mustPrefix(prefix))
	}
	return options
}

// TestMultipleTunInboundsShareOneDecision is the multi-TUN regression, run in BOTH orders.
//
// A box may configure more than one TUN (the fork does not reject it, and the platform layer is what
// refuses a second one). The entry decision is one value: the box pre-checks that every TUN can be
// reduced and commits "suppress" only then. Whatever the order, the outcome must be the same - every
// interface reduced, or none - and the shared value must be the box's verdict, never one interface's
// opinion of itself.
func TestMultipleTunInboundsShareOneDecision(t *testing.T) {
	reducible := tunConfig{inet4: []string{"172.19.0.1/30"}, inet6: []string{"fdfe:dcba:9876::1/126"}}
	reducibleWithDNS := tunConfig{
		inet4:      []string{"172.19.0.2/30"},
		inet6:      []string{"fdfe:dcba:9876::5/126"},
		dnsAddress: []netip.Addr{mustAddr("172.19.0.2"), mustAddr("fdfe:dcba:9876::6")},
	}
	v6Only := tunConfig{inet6: []string{"2001:db8::1/64"}}
	v6DNSOnly := tunConfig{
		inet4:      []string{"172.19.0.3/30"},
		inet6:      []string{"fdfe:dcba:9876::9/126"},
		dnsAddress: []netip.Addr{mustAddr("fdfe:dcba:9876::10")},
	}

	testCases := []struct {
		name        string
		tuns        []tunConfig
		wantPresent bool
	}{
		{name: "single reducible", tuns: []tunConfig{reducible}},
		{name: "single reducible with mixed dns", tuns: []tunConfig{reducibleWithDNS}},
		{name: "single ipv6-only", tuns: []tunConfig{v6Only}, wantPresent: true},
		{name: "single ipv6-only dns list", tuns: []tunConfig{v6DNSOnly}, wantPresent: true},
		{name: "both reducible", tuns: []tunConfig{reducible, reducibleWithDNS}},
		{
			name:        "reducible then irreducible",
			tuns:        []tunConfig{reducible, v6Only},
			wantPresent: true,
		},
		{
			name:        "irreducible then reducible",
			tuns:        []tunConfig{v6Only, reducible},
			wantPresent: true,
		},
		{
			name:        "dns list decides the box",
			tuns:        []tunConfig{reducible, v6DNSOnly},
			wantPresent: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			inbounds := make([]option.Inbound, 0, len(testCase.tuns))
			for _, tun := range testCase.tuns {
				inbounds = append(inbounds, tun.inboundOptions())
			}
			boxReducible := option.AllTunInboundsReducibleToIPv4Only(inbounds)
			require.Equal(t, !testCase.wantPresent, boxReducible,
				"the box pre-check must describe the configuration")

			// The manager commits the box verdict; every interface reads that one value.
			boxVerdict := netcap.SuppressIPv6
			if !boxReducible {
				boxVerdict = netcap.PresentIPv6
			}
			orders := [][]int{{0, 1}}
			if len(testCase.tuns) > 1 {
				orders = append(orders, []int{1, 0})
			}
			for _, order := range orders {
				reader := &entryReaderStub{presentment: boxVerdict}
				inboundsInOrder := make([]*Inbound, 0, len(order))
				for _, index := range order {
					if index >= len(testCase.tuns) {
						continue
					}
					inboundsInOrder = append(inboundsInOrder,
						entryDecisionInbound(t, reader, testCase.tuns[index].effectiveOptions()))
				}
				for _, inbound := range inboundsInOrder {
					inbound.applyEntryIPv6Presentment()
				}
				for index, inbound := range inboundsInOrder {
					if boxReducible {
						require.True(t, inbound.entryIPv4Only,
							"order %v, tun %d: every interface must be reduced", order, index)
						require.Empty(t, inbound.tunOptions.Inet6Address)
					} else {
						require.False(t, inbound.entryIPv4Only,
							"order %v, tun %d: no interface may be reduced when one cannot be", order, index)
						require.NotEmpty(t, inbound.tunOptions.Inet6Address)
					}
				}
				require.Equal(t, boxVerdict, reader.presentment,
					"order %v: an interface changed the shared decision", order)
			}
		})
	}
}
