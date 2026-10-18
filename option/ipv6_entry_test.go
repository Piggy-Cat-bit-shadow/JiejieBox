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

func dnsOptionsForTest(servers ...DNSServerOptions) *DNSOptions {
	return &DNSOptions{RawDNSOptions: RawDNSOptions{Servers: servers}}
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
				DNS: dnsOptionsForTest(fakeIPServer("198.19.0.0/16", "fc00::/18")),
			},
			expected: IPv6EntryDemand{FakeIPAllocatesIPv6: true, DNSCanAnswerAAAA: true},
		},
		{
			name: "fakeip with only an ipv4 range",
			options: Options{
				DNS: dnsOptionsForTest(fakeIPServer("198.19.0.0/16", "")),
			},
			expected: IPv6EntryDemand{DNSCanAnswerAAAA: true},
		},
		{
			name: "hosts with a predefined ipv6 address",
			options: Options{
				DNS: dnsOptionsForTest(hostsServer(map[string][]netip.Addr{
					"example.com": {netip.MustParseAddr("203.0.113.7"), netip.MustParseAddr("2001:db8::7")},
				})),
			},
			expected: IPv6EntryDemand{HostsPublishesIPv6: true, DNSCanAnswerAAAA: true},
		},
		{
			name: "hosts with only ipv4 addresses",
			options: Options{
				DNS: dnsOptionsForTest(hostsServer(map[string][]netip.Addr{
					"example.com": {netip.MustParseAddr("203.0.113.7")},
				})),
			},
			expected: IPv6EntryDemand{DNSCanAnswerAAAA: true},
		},
		{
			name: "hosts with a file whose contents are not read here",
			options: Options{
				DNS: dnsOptionsForTest(hostsServer(nil, "/etc/hosts")),
			},
			// Proving the file has no AAAA would mean reading and scanning it at configuration
			// time, which is the per-configuration cost this round must not add.
			expected: IPv6EntryDemand{HostsPublishesIPv6: true, DNSCanAnswerAAAA: true},
		},
		{
			name: "a plain dns server",
			options: Options{
				DNS: dnsOptionsForTest(simpleDNSServer()),
			},
			expected: IPv6EntryDemand{DNSCanAnswerAAAA: true},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.expected, ResolveIPv6EntryDemand(testCase.options))
		})
	}
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
