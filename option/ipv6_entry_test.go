package option

import (
	"net/netip"
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common/json/badjson"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
)

// The demand summary is the gate that decides whether the adaptive entry system does any work at
// all, so its tests are about the two ways it can be wrong:
//
//   - a false negative: a configuration whose entries DO present IPv6 is classified as having no
//     demand, and the whole feature silently does nothing;
//   - a false positive on a surface that cannot exist: a configuration that presents no IPv6 pays
//     for an interface enumeration and a route-table read it does not need.

func tunInboundForTest(options *TunInboundOptions) Inbound {
	return Inbound{Type: C.TypeTun, Tag: "tun-in", Options: options}
}

// ipv4OnlyTun is the TUN every DNS-side case is attached to: the DNS surfaces only matter when a
// TUN exists, because the application-facing DNS gate is keyed on that inbound as the query source.
func ipv4OnlyTun() Inbound {
	return tunInboundForTest(&TunInboundOptions{
		Address:   badoption.Listable[netip.Prefix]{netip.MustParsePrefix("172.19.0.1/30")},
		AutoRoute: true,
	})
}

func dnsOptionsForTest(servers ...DNSServerOptions) *DNSOptions {
	return &DNSOptions{RawDNSOptions: RawDNSOptions{Servers: servers}}
}

func dnsOptionsWithStrategyForTest(strategy DomainStrategy, servers ...DNSServerOptions) *DNSOptions {
	options := dnsOptionsForTest(servers...)
	options.Strategy = strategy
	return options
}

func fakeIPServer(inet4Range string, inet6Range string) DNSServerOptions {
	options := &FakeIPDNSServerOptions{}
	if inet4Range != "" {
		prefix := badoption.Prefix(netip.MustParsePrefix(inet4Range))
		options.Inet4Range = &prefix
	}
	if inet6Range != "" {
		prefix := badoption.Prefix(netip.MustParsePrefix(inet6Range))
		options.Inet6Range = &prefix
	}
	return DNSServerOptions{Type: C.DNSTypeFakeIP, Tag: "fakeip", Options: options}
}

func hostsServer(predefined map[string][]netip.Addr, paths ...string) DNSServerOptions {
	options := &HostsDNSServerOptions{Path: paths}
	if predefined != nil {
		var typed badjson.TypedMap[string, badoption.Listable[netip.Addr]]
		for key, addresses := range predefined {
			typed.Put(key, badoption.Listable[netip.Addr](addresses))
		}
		options.Predefined = &typed
	}
	return DNSServerOptions{Type: C.DNSTypeHosts, Tag: "hosts", Options: options}
}

func simpleDNSServer() DNSServerOptions {
	return DNSServerOptions{
		Type: C.DNSTypeUDP,
		Tag:  "local",
		Options: &RemoteDNSServerOptions{
			DNSServerAddressOptions: DNSServerAddressOptions{
				Server:     "203.0.113.53",
				ServerPort: 53,
			},
		},
	}
}

// TestResolveIPv6EntryDemandIdentifiesEverySurface is the positive table: each application-facing
// surface that can present IPv6 must set its own flag.
func TestResolveIPv6EntryDemandIdentifiesEverySurface(t *testing.T) {
	testCases := []struct {
		name     string
		options  Options
		expected IPv6EntryDemand
	}{
		{
			name:    "empty configuration",
			options: Options{},
		},
		{
			name: "a proxy-only box has no entry this process controls",
			options: Options{
				DNS: dnsOptionsForTest(
					simpleDNSServer(),
					fakeIPServer("198.19.0.0/16", "fc00::/18"),
					hostsServer(map[string][]netip.Addr{"example.com": {netip.MustParseAddr("2001:db8::7")}}),
				),
			},
			// The application-facing DNS gate is keyed on the TUN as the query source
			// (dns/entry_ipv6.go), so without a TUN there is no entry to suppress: an observation
			// here would be paid for and never consumed.
		},
		{
			name: "tun with an ipv6 address",
			options: Options{Inbounds: []Inbound{tunInboundForTest(&TunInboundOptions{
				Address: badoption.Listable[netip.Prefix]{
					netip.MustParsePrefix("172.19.0.1/30"),
					netip.MustParsePrefix("fdfe:dcba:9876::1/126"),
				},
			})}},
			expected: IPv6EntryDemand{TUNPresentsIPv6: true},
		},
		{
			name: "tun with only an ipv4 address and auto route",
			options: Options{Inbounds: []Inbound{tunInboundForTest(&TunInboundOptions{
				Address:   badoption.Listable[netip.Prefix]{netip.MustParsePrefix("172.19.0.1/30")},
				AutoRoute: true,
			})}},
		},
		{
			name: "tun with an ipv6 route",
			options: Options{Inbounds: []Inbound{tunInboundForTest(&TunInboundOptions{
				Address:      badoption.Listable[netip.Prefix]{netip.MustParsePrefix("172.19.0.1/30")},
				RouteAddress: badoption.Listable[netip.Prefix]{netip.MustParsePrefix("::/0")},
			})}},
			expected: IPv6EntryDemand{TUNPresentsIPv6: true},
		},
		{
			name: "tun with a route address set",
			options: Options{Inbounds: []Inbound{tunInboundForTest(&TunInboundOptions{
				Address:         badoption.Listable[netip.Prefix]{netip.MustParsePrefix("172.19.0.1/30")},
				RouteAddressSet: badoption.Listable[string]{"direct"},
			})}},
			// The set's contents are not known here, and a set is exactly how an IPv6 route can
			// reach an otherwise IPv4-only option list.
			expected: IPv6EntryDemand{TUNPresentsIPv6: true},
		},
		{
			name: "tun with an ipv6 dns address",
			options: Options{Inbounds: []Inbound{tunInboundForTest(&TunInboundOptions{
				Address:    badoption.Listable[netip.Prefix]{netip.MustParsePrefix("172.19.0.1/30")},
				DNSAddress: badoption.Listable[netip.Addr]{netip.MustParseAddr("fdfe:dcba:9876::2")},
			})}},
			expected: IPv6EntryDemand{TUNPresentsIPv6: true},
		},
		{
			name: "tun with an ipv6 loopback address",
			options: Options{Inbounds: []Inbound{tunInboundForTest(&TunInboundOptions{
				Address:         badoption.Listable[netip.Prefix]{netip.MustParsePrefix("172.19.0.1/30")},
				LoopbackAddress: badoption.Listable[netip.Addr]{netip.MustParseAddr("fdfe:dcba:9876::2")},
			})}},
			expected: IPv6EntryDemand{TUNPresentsIPv6: true},
		},
		{
			name: "fakeip with an ipv6 range",
			options: Options{
				Inbounds: []Inbound{ipv4OnlyTun()},
				DNS:      dnsOptionsForTest(fakeIPServer("198.19.0.0/16", "fc00::/18")),
			},
			expected: IPv6EntryDemand{FakeIPAllocatesIPv6: true, DNSCanAnswerAAAA: true},
		},
		{
			name: "fakeip with only an ipv4 range",
			options: Options{
				Inbounds: []Inbound{ipv4OnlyTun()},
				DNS:      dnsOptionsForTest(fakeIPServer("198.19.0.0/16", "")),
			},
			expected: IPv6EntryDemand{DNSCanAnswerAAAA: true},
		},
		{
			name: "hosts with a predefined ipv6 address",
			options: Options{
				Inbounds: []Inbound{ipv4OnlyTun()},
				DNS: dnsOptionsForTest(hostsServer(map[string][]netip.Addr{
					"example.com": {netip.MustParseAddr("203.0.113.7"), netip.MustParseAddr("2001:db8::7")},
				})),
			},
			expected: IPv6EntryDemand{HostsPublishesIPv6: true, DNSCanAnswerAAAA: true},
		},
		{
			name: "hosts with only ipv4 predefined entries still loads a file",
			options: Options{
				Inbounds: []Inbound{ipv4OnlyTun()},
				DNS: dnsOptionsForTest(hostsServer(map[string][]netip.Addr{
					"example.com": {netip.MustParseAddr("203.0.113.7")},
				})),
			},
			// The transport loads the SYSTEM hosts file even when predefined entries are set, and
			// that file is not read here: deciding it has no AAAA would be the configuration-time
			// scan this round forbids.
			expected: IPv6EntryDemand{HostsPublishesIPv6: true, DNSCanAnswerAAAA: true},
		},
		{
			name: "hosts with a file whose contents are not read here",
			options: Options{
				Inbounds: []Inbound{ipv4OnlyTun()},
				DNS:      dnsOptionsForTest(hostsServer(nil, "/etc/hosts")),
			},
			expected: IPv6EntryDemand{HostsPublishesIPv6: true, DNSCanAnswerAAAA: true},
		},
		{
			name: "a bare hosts server",
			options: Options{
				Inbounds: []Inbound{ipv4OnlyTun()},
				DNS:      dnsOptionsForTest(DNSServerOptions{Type: C.DNSTypeHosts, Tag: "hosts", Options: &HostsDNSServerOptions{}}),
			},
			expected: IPv6EntryDemand{HostsPublishesIPv6: true, DNSCanAnswerAAAA: true},
		},
		{
			name: "a plain dns server",
			options: Options{
				Inbounds: []Inbound{ipv4OnlyTun()},
				DNS:      dnsOptionsForTest(simpleDNSServer()),
			},
			expected: IPv6EntryDemand{DNSCanAnswerAAAA: true},
		},
		{
			name: "prefer_ipv4 is not an IPv4-only resolver",
			options: Options{
				Inbounds: []Inbound{ipv4OnlyTun()},
				DNS:      dnsOptionsWithStrategyForTest(DomainStrategy(C.DomainStrategyPreferIPv4), simpleDNSServer()),
			},
			// prefer_ipv4 still returns AAAA to an application that asks for it.
			expected: IPv6EntryDemand{DNSCanAnswerAAAA: true},
		},
		{
			name: "ipv4_only is not a demand suppressor either",
			options: Options{
				Inbounds: []Inbound{ipv4OnlyTun()},
				DNS:      dnsOptionsWithStrategyForTest(DomainStrategy(C.DomainStrategyIPv4Only), simpleDNSServer()),
			},
			// `dns.strategy` narrows the address family a LOOKUP asks for. An application that asks
			// AAAA itself is still answered by the transport, so the entry surface exists. Treating
			// it as "no demand" would leave the application receiving an answer the dial path will
			// refuse - the failure this feature exists to move earlier.
			expected: IPv6EntryDemand{DNSCanAnswerAAAA: true},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.expected, ResolveIPv6EntryDemand(testCase.options))
		})
	}
}

// TestAProxyOnlyBoxPaysNothingForTheEntryDecision is I-03 stated as the concrete configuration that
// used to trip it: DNS servers (and even a Fake-IP IPv6 range) with no TUN present a DNS surface
// this process cannot gate, so the summary must stay empty.
func TestAProxyOnlyBoxPaysNothingForTheEntryDecision(t *testing.T) {
	options := Options{
		Inbounds: []Inbound{{
			Type:    C.TypeMixed,
			Tag:     "mixed-in",
			Options: &HTTPMixedInboundOptions{},
		}},
		DNS: dnsOptionsForTest(
			simpleDNSServer(),
			fakeIPServer("198.19.0.0/16", "fc00::/18"),
		),
	}
	demand := ResolveIPv6EntryDemand(options)
	require.False(t, demand.Required(),
		"no TUN means no application-facing entry this process controls, so the observation would "+
			"be paid for and never consumed: %+v", demand)
}

// TestRequiredIsTheDisjunction keeps the fast path honest: only a configuration with none of the
// four surfaces is allowed to skip the work.
func TestRequiredIsTheDisjunction(t *testing.T) {
	require.False(t, IPv6EntryDemand{}.Required())
	require.True(t, IPv6EntryDemand{TUNPresentsIPv6: true}.Required())
	require.True(t, IPv6EntryDemand{FakeIPAllocatesIPv6: true}.Required())
	require.True(t, IPv6EntryDemand{HostsPublishesIPv6: true}.Required())
	require.True(t, IPv6EntryDemand{DNSCanAnswerAAAA: true}.Required())
}

// TestTheDemandIsDerivedFromTypedOptionsNotRawText is the defect this file exists to prevent: the
// absence of the string "ipv6" from the configuration says nothing about what the entries present.
func TestTheDemandIsDerivedFromTypedOptionsNotRawText(t *testing.T) {
	// A TUN written with a unique-local prefix and a Fake-IP server that only sets inet6_range: no
	// field is called "ipv6", and both present the family to applications.
	options := Options{
		Inbounds: []Inbound{tunInboundForTest(&TunInboundOptions{
			Address: badoption.Listable[netip.Prefix]{
				netip.MustParsePrefix("172.19.0.1/30"),
				netip.MustParsePrefix("fdfe:dcba:9876::1/126"),
			},
		})},
		DNS: dnsOptionsForTest(fakeIPServer("", "fc00::/18")),
	}
	demand := ResolveIPv6EntryDemand(options)
	require.True(t, demand.TUNPresentsIPv6)
	require.True(t, demand.FakeIPAllocatesIPv6)
	require.True(t, demand.Required())
}

// TestTheDemandDoesNotReadOrWriteTheConfiguration is I-04 at the summary itself: the analysis is a
// projection, not a rewrite. A hosts table with a large IPv6 address set is the case where a scan
// would be both expensive and visible.
func TestTheDemandDoesNotReadOrWriteTheConfiguration(t *testing.T) {
	predefined := make(map[string][]netip.Addr, 64)
	for index := 0; index < 64; index++ {
		predefined[netip.AddrFrom4([4]byte{192, 0, 2, byte(index + 1)}).String()] = []netip.Addr{
			netip.MustParseAddr("203.0.113.53"),
			netip.MustParseAddr("2001:db8::53"),
		}
	}
	options := Options{
		Inbounds: []Inbound{tunInboundForTest(&TunInboundOptions{
			Address: badoption.Listable[netip.Prefix]{
				netip.MustParsePrefix("172.19.0.1/30"),
				netip.MustParsePrefix("fdfe:dcba:9876::1/126"),
			},
		})},
		DNS: dnsOptionsForTest(hostsServer(predefined)),
	}
	before := make([]netip.Prefix, len(options.Inbounds[0].Options.(*TunInboundOptions).Address))
	copy(before, options.Inbounds[0].Options.(*TunInboundOptions).Address)
	require.True(t, ResolveIPv6EntryDemand(options).Required())
	require.Equal(t, before, []netip.Prefix(options.Inbounds[0].Options.(*TunInboundOptions).Address),
		"the summary must not rewrite the typed configuration it reads")
	for key, addresses := range predefined {
		require.Equal(t, []netip.Addr{netip.MustParseAddr("203.0.113.53"), netip.MustParseAddr("2001:db8::53")}, addresses,
			"hosts data must be untouched (", key, ")")
	}
}

// TestAFourInSixAddressIsDemand is the predicate-drift test, and it is the reason the demand uses
// `Is6` rather than the un-mapped form.
//
// The TUN splits its address lists with `Is4`/`Is6` (protocol/tun/inbound.go), so ::ffff:172.19.0.1
// becomes an `Inet6Address` and the platform installs it as an IPv6 address. A demand that
// un-mapped it would classify the same configuration as IPv4-only and never take a decision, while
// the interface advertised IPv6 - the false presentment this round removes, reached through the
// predicate instead of through the network.
func TestAFourInSixAddressIsDemand(t *testing.T) {
	t.Run("address", func(t *testing.T) {
		demand := ResolveIPv6EntryDemand(Options{Inbounds: []Inbound{tunInboundForTest(&TunInboundOptions{
			Address: badoption.Listable[netip.Prefix]{
				netip.MustParsePrefix("172.19.0.1/30"),
				netip.MustParsePrefix("::ffff:172.19.0.1/120"),
			},
		})}})
		require.True(t, demand.TUNPresentsIPv6,
			"the TUN would install ::ffff:172.19.0.1 as an Inet6Address, so the demand must see it")
	})
	t.Run("dns address", func(t *testing.T) {
		demand := ResolveIPv6EntryDemand(Options{Inbounds: []Inbound{tunInboundForTest(&TunInboundOptions{
			Address:    badoption.Listable[netip.Prefix]{netip.MustParsePrefix("172.19.0.1/30")},
			DNSAddress: badoption.Listable[netip.Addr]{netip.MustParseAddr("::ffff:172.19.0.2")},
		})}})
		require.True(t, demand.TUNPresentsIPv6,
			"Inet6DNSAddress filters with Is6, so this address reaches the v6 list too")
	})
}

// TestAllTunInboundsReducibleToIPv4OnlyIsTheBoxAnswer is the pre-check the box commits on.
//
// It is an AND over every TUN inbound, because the entry decision is one value for the whole box:
// a single interface that cannot be reduced keeps IPv6 presented everywhere. The order of the two
// shapes must not matter, which is the property the old per-TUN rollback failed.
func TestAllTunInboundsReducibleToIPv4OnlyIsTheBoxAnswer(t *testing.T) {
	reducible := tunInboundForTest(&TunInboundOptions{
		Address: badoption.Listable[netip.Prefix]{
			netip.MustParsePrefix("172.19.0.1/30"),
			netip.MustParsePrefix("fdfe:dcba:9876::1/126"),
		},
	})
	reducibleWithMixedDNS := tunInboundForTest(&TunInboundOptions{
		Address: badoption.Listable[netip.Prefix]{netip.MustParsePrefix("172.19.0.2/30")},
		DNSAddress: badoption.Listable[netip.Addr]{
			netip.MustParseAddr("172.19.0.2"),
			netip.MustParseAddr("fdfe:dcba:9876::2"),
		},
	})
	v6Only := tunInboundForTest(&TunInboundOptions{
		Address: badoption.Listable[netip.Prefix]{netip.MustParsePrefix("2001:db8::1/64")},
	})
	v6OnlyDNS := tunInboundForTest(&TunInboundOptions{
		Address:    badoption.Listable[netip.Prefix]{netip.MustParsePrefix("172.19.0.3/30")},
		DNSAddress: badoption.Listable[netip.Addr]{netip.MustParseAddr("fdfe:dcba:9876::2")},
	})

	testCases := []struct {
		name     string
		inbounds []Inbound
		expected bool
	}{
		{name: "no tun at all", inbounds: nil, expected: true},
		{name: "single reducible", inbounds: []Inbound{reducible}, expected: true},
		{name: "single with mixed dns", inbounds: []Inbound{reducibleWithMixedDNS}, expected: true},
		{name: "single ipv6-only", inbounds: []Inbound{v6Only}, expected: false},
		{name: "single with ipv6-only dns list", inbounds: []Inbound{v6OnlyDNS}, expected: false},
		{name: "both reducible", inbounds: []Inbound{reducible, reducibleWithMixedDNS}, expected: true},
		{name: "reducible then irreducible", inbounds: []Inbound{reducible, v6Only}, expected: false},
		{name: "irreducible then reducible", inbounds: []Inbound{v6Only, reducible}, expected: false},
		{name: "dns list decides the box", inbounds: []Inbound{reducible, v6OnlyDNS}, expected: false},
		{
			name: "non-tun inbounds are ignored",
			inbounds: []Inbound{reducible, {
				Type: C.TypeMixed, Tag: "mixed-in", Options: &HTTPMixedInboundOptions{},
			}},
			expected: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.expected, AllTunInboundsReducibleToIPv4Only(testCase.inbounds))
		})
	}
}

// TestTunReducibleToIPv4OnlyIsTheSharedRule pins the primitive itself, including the two reasons a
// reduction is refused. protocol/tun asserts that its reduction agrees with this function for the
// same shapes; this test is the rule at its source.
func TestTunReducibleToIPv4OnlyIsTheSharedRule(t *testing.T) {
	inet4 := []netip.Prefix{netip.MustParsePrefix("172.19.0.1/30")}
	require.True(t, TunReducibleToIPv4Only(inet4, nil), "no explicit dns list derives a v4 server")
	require.True(t, TunReducibleToIPv4Only(inet4, []netip.Addr{netip.MustParseAddr("172.19.0.2")}))
	require.True(t, TunReducibleToIPv4Only(inet4, []netip.Addr{
		netip.MustParseAddr("172.19.0.2"), netip.MustParseAddr("fdfe:dcba:9876::2"),
	}), "one v4 entry is enough to leave a resolver")
	require.False(t, TunReducibleToIPv4Only(inet4, []netip.Addr{netip.MustParseAddr("fdfe:dcba:9876::2")}),
		"the only named resolver is removed by the reduction")
	require.False(t, TunReducibleToIPv4Only(nil, nil), "an interface must keep at least one address")
}
