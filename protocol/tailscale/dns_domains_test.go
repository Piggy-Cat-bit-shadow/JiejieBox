//go:build with_tailscale

package tailscale

import (
	"net/netip"
	"testing"

	nDNS "github.com/sagernet/tailscale/net/dns"
	"github.com/sagernet/tailscale/types/dnstype"
	"github.com/sagernet/tailscale/util/dnsname"
)

// A single-label LAN host must be routed by the tailnet's search domains, while a name that
// only matches a MagicDNS suffix must not be swallowed merely because it has one label.
// This is the short-name / search-domain distinction that the old suffix scan conflated.
func TestTailnetDomainsMatch(t *testing.T) {
	hosts := map[dnsname.FQDN][]netip.Addr{
		"printer.example.com.": {netip.MustParseAddr("100.64.0.10")},
		"nas.corp.example.com.": {netip.MustParseAddr("100.64.0.11")},
	}
	config := &nDNS.Config{
		Hosts:         hosts,
		SearchDomains: []dnsname.FQDN{"example.com."},
		Routes: map[dnsname.FQDN][]*dnstype.Resolver{
			"corp.example.com.": nil, // local suffix
			"remote.example.":   {{Addr: "100.64.0.53:53"}},
		},
	}
	domains := newTailnetDomains(config)

	testCases := []struct {
		name     string
		domain   string
		expand   bool
		expected bool
	}{
		{"known host", "printer.example.com.", false, true},
		{"single label expanded by search domain", "printer.", true, true},
		{"single label without expansion is not a host", "printer.", false, false},
		{"local suffix", "nas.corp.example.com.", false, true},
		{"forwarded suffix", "anything.remote.example.", false, true},
		{"unknown single label", "unknown.", true, false},
		{"unknown multi-label", "unknown.other.example.", true, false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := domains.match(nil, testCase.domain, testCase.expand)
			if got != testCase.expected {
				t.Fatalf("match(%q, expand=%v) = %v, want %v", testCase.domain, testCase.expand, got, testCase.expected)
			}
		})
	}
}
