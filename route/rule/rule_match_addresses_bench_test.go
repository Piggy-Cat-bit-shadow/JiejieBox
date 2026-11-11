package rule

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
)

// Cost comparison for the hot path this feature touches: `ip_cidr` (and therefore every IP
// `rule_set`) is evaluated once per destination-IP rule per flow, so the third address source must
// not add an allocation to the paths that existed before it.
//
// The three shapes a flow can present:
//
//	literal      an IP destination - unchanged by this feature
//	candidates   what a resolve action publishes by default (match_only false)
//	match only   what match_only true publishes
//
// Recorded rather than asserted: a benchmark that fails on a nanosecond is a flaky gate, while the
// numbers here are what a reviewer compares against `benchstat` after a change.
func BenchmarkIPCIDRItemMatch(b *testing.B) {
	item, err := NewIPCIDRItem(false, []*badoption.Prefixable{prefixable("198.51.100.0/24")})
	if err != nil {
		b.Fatal(err)
	}
	address := netip.MustParseAddr("198.51.100.10")

	metadata := map[string]*adapter.InboundContext{
		"literal": {
			Destination: M.SocksaddrFrom(address, 443),
		},
		"candidates": {
			Destination:          M.Socksaddr{Fqdn: "bench.example", Port: 443},
			DestinationAddresses: []netip.Addr{address},
		},
		"match only": {
			Destination:    M.Socksaddr{Fqdn: "bench.example", Port: 443},
			MatchAddresses: []netip.Addr{address},
		},
	}
	for _, name := range []string{"literal", "candidates", "match only"} {
		context := metadata[name]
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for index := 0; index < b.N; index++ {
				if !item.Match(context) {
					b.Fatal("the fixture must match, otherwise the loop measures the wrong path")
				}
			}
		})
	}
}

func benchmarkPrefixable(cidr string) *badoption.Prefixable {
	prefix := badoption.Prefixable(netip.MustParsePrefix(cidr))
	return &prefix
}
