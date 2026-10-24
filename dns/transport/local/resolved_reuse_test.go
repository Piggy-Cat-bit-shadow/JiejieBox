//go:build linux

package local

import (
	"net/netip"
	"testing"
)

func testResolvedSpecification() resolvedScopeSpecification {
	return resolvedScopeSpecification{
		interfaceName:  "eth0",
		dnsOverTLSMode: "opportunistic",
		domains:        []string{"example.com"},
		defaultRoute:   true,
		fallback:       false,
		servers: []resolvedServerSpecification{
			{address: netip.MustParseAddr("192.0.2.53"), port: 53},
		},
	}
}

// The reuse decision is this comparison. An unchanged configuration must compare equal so
// updateStatus returns before createResolvedServerSet (which closes and rebuilds the scopes);
// any material field must compare unequal so the rebuild still happens.
func TestResolvedScopeSpecificationEqual(t *testing.T) {
	base := testResolvedSpecification()
	if !base.equal(testResolvedSpecification()) {
		t.Fatal("an identical scope specification did not compare equal, so the reuse path would never trigger")
	}

	changed := testResolvedSpecification()
	changed.servers[0].port = 5353
	if base.equal(changed) {
		t.Fatal("a changed server port compared equal; a real DNS change would be ignored")
	}

	changed = testResolvedSpecification()
	changed.domains = []string{"example.com", "corp.example.com"}
	if base.equal(changed) {
		t.Fatal("a changed search domain list compared equal")
	}

	changed = testResolvedSpecification()
	changed.defaultRoute = false
	if base.equal(changed) {
		t.Fatal("a changed default-route flag compared equal")
	}
}
