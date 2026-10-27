package dialer

import (
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
)

// TestPreferIPv4ForServerLeavesExplicitPoliciesAlone pins the boundary of the AnyTLS server
// preference: it fills the default (AsIS) and nothing else. A resolver that explicitly asked for
// IPv6-only, or for either single family, must not be silently overridden by the proxy hop.
func TestPreferIPv4ForServerLeavesExplicitPoliciesAlone(t *testing.T) {
	testCases := []struct {
		name     string
		strategy C.DomainStrategy
		expected C.DomainStrategy
	}{
		{"default AsIS is filled", C.DomainStrategyAsIS, C.DomainStrategyPreferIPv4},
		{"prefer IPv4 is unchanged", C.DomainStrategyPreferIPv4, C.DomainStrategyPreferIPv4},
		{"prefer IPv6 is unchanged", C.DomainStrategyPreferIPv6, C.DomainStrategyPreferIPv6},
		{"IPv4-only is unchanged", C.DomainStrategyIPv4Only, C.DomainStrategyIPv4Only},
		{"IPv6-only is unchanged", C.DomainStrategyIPv6Only, C.DomainStrategyIPv6Only},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := preferIPv4ForServer(adapter.DNSQueryOptions{Strategy: testCase.strategy})
			if got.Strategy != testCase.expected {
				t.Fatalf("strategy %v: got %v, want %v", testCase.strategy, got.Strategy, testCase.expected)
			}
		})
	}
}
