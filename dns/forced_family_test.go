package dns

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"

	mDNS "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// TestForcedFamilySurvivesAnOppositeRouterDefault pins the strict branch's internal consistency.
//
// A caller can force a family through LookupStrategy while leaving Strategy at AsIS - that is the
// field pair the dual-stack router itself uses. If the strict branch rewrites only LookupStrategy,
// the router's DEFAULT strategy is filled into Strategy downstream (the explicit-transport path), and
// an opposite default silently turns the forced family into a conflict that issues nothing.
func TestForcedFamilySurvivesAnOppositeRouterDefault(t *testing.T) {
	transport := &queryRecordingTransport{addressA: "192.0.2.1", address6: "2001:db8::1"}
	router := legacyRuleRouter(t, transport, nil)
	router.defaultDomainStrategy = C.DomainStrategyIPv6Only
	router.legacyDNSMode = false

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var published familyResults
	err := router.LookupFamilies(ctx, "forced.test.", adapter.DNSQueryOptions{
		Transport:      transport,
		LookupStrategy: C.DomainStrategyIPv4Only,
	}, published.publish)
	require.NoError(t, err, "the forced family must still be queried")
	observed := transport.observed()
	require.Contains(t, observed, mDNS.TypeA)
	require.NotContains(t, observed, mDNS.TypeAAAA)

	requireAddressFamily(t, published.published(), false, "192.0.2.1")
}

// TestModernModeIgnoresTheLegacyStrategyField is the control: the fix must not make modern rule mode
// interpret a field it deliberately skips.
func TestModernModeIgnoresTheLegacyStrategyField(t *testing.T) {
	transport := &queryRecordingTransport{addressA: "192.0.2.1", address6: "2001:db8::1"}
	router := legacyRuleRouter(t, transport, []option.DNSRule{
		legacyFamilyStrategyRule("final", C.DomainStrategyIPv4Only),
	})
	router.legacyDNSMode = false

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var published familyResults
	err := router.LookupFamilies(ctx, "modern.test.", adapter.DNSQueryOptions{}, published.publish)
	require.NoError(t, err)
	observed := transport.observed()
	require.Contains(t, observed, mDNS.TypeA)
	require.Contains(t, observed, mDNS.TypeAAAA)
}

var _ = netip.Addr{}
