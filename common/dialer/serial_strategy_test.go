package dialer

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// The serial resolution branch must honour the strict family strategy.
//
// # The violation
//
// `DialContext` has two hostname branches. The parallel one goes through `raceResolvedName` → the planner and
// the scheduler, where the strategy filter lives. The serial one (`parallel == false`) resolves and calls
// `N.DialSerial` directly, and it applied only the local family CAPABILITY:
//
//	policy := d.dialerLocalFamilyPolicy()
//	refused := refusedCandidateIn(addresses, policy)
//	addresses = withoutUndialable(addresses, policy)
//
// Measured before the fix: with `ipv4_only` and a resolver answering AAAA, this branch returned a working
// connection on the family the operator had forbidden - `err = nil`, the excluded address dialled. Every
// other resolution path filtered the strategy; this one was the only one that did not, because it never
// reaches the planner or the scheduler where that filter lives.
//
// It is reachable from configuration alone: `parallel == false` is the serial contract for callers that dial
// one bootstrap address at a time, and such an outbound with `domain_strategy: ipv4_only` is an ordinary
// setup.

// serialNameRouter answers every lookup with a fixed list and ignores the query type, which is what makes it
// able to hand a strict caller an address of the forbidden family - the shape a non-conforming resolver has,
// and the shape this filter must not depend on the resolver for.
//
// Only Lookup is implemented: the serial branch calls nothing else on the router, and every test here sets an
// explicit strategy so `effectiveFamilyStrategy` returns before it would ask the router to resolve one. The
// embedded interface is nil so an unexpected call fails loudly instead of silently answering.
type serialNameRouter struct {
	adapter.DNSRouter
	addresses []netip.Addr
	calls     int
}

func (r *serialNameRouter) Lookup(ctx context.Context, domain string, options adapter.DNSQueryOptions) ([]netip.Addr, error) {
	r.calls++
	return r.addresses, nil
}

// TestSerialHostnameBranchHonoursTheFamilyStrategy is the regression.
func TestSerialHostnameBranchHonoursTheFamilyStrategy(t *testing.T) {
	forbiddenIPv6 := netip.MustParseAddr("2001:db8::2c")

	leaf := newCapabilityLeaf(0, 0)
	leaf.inner.packetConn = &countingPacketConn{}
	router := &serialNameRouter{addresses: []netip.Addr{forbiddenIPv6}}

	dialer := &resolveDialer{
		router:       router,
		dialer:       leaf,
		parallel:     false,
		queryOptions: adapter.DNSQueryOptions{Strategy: C.DomainStrategyIPv4Only},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := dialer.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddrHostPort("bootstrap.example", 443))
	if conn != nil {
		conn.Close()
	}

	require.Error(t, err,
		"the only answer belongs to the family ipv4_only forbids, so there is nothing this flow may dial")
	require.Contains(t, err.Error(), "excluded by strategy",
		"and the refusal must name the policy: a configuration decision has to read as one")
	require.Contains(t, err.Error(), "ipv4_only")
	require.Empty(t, leaf.tried(),
		"the excluded family must never become a socket attempt - this branch used to dial it and report success")
}

// TestSerialHostnameBranchAdmitsTheAllowedFamily is the control.
//
// Without it, a fix that refused every serial hostname lookup would pass the regression.
func TestSerialHostnameBranchAdmitsTheAllowedFamily(t *testing.T) {
	allowedIPv4 := netip.MustParseAddr("192.0.2.92")

	leaf := newCapabilityLeaf(0, 0)
	leaf.inner.packetConn = &countingPacketConn{}
	router := &serialNameRouter{addresses: []netip.Addr{allowedIPv4}}

	dialer := &resolveDialer{
		router:       router,
		dialer:       leaf,
		parallel:     false,
		queryOptions: adapter.DNSQueryOptions{Strategy: C.DomainStrategyIPv4Only},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := dialer.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddrHostPort("bootstrap.example", 443))
	if conn != nil {
		conn.Close()
	}

	require.NoError(t, err, "the policy admits this family, so the serial branch must dial it")
	require.Contains(t, leaf.tried(), allowedIPv4)
	require.Equal(t, 1, router.calls, "and it resolves exactly once")
}

// TestSerialHostnameBranchStillReportsTheResolverOrder keeps the serial contract: the filter removes
// addresses, it does not reorder the ones that survive.
func TestSerialHostnameBranchStillReportsTheResolverOrder(t *testing.T) {
	first := netip.MustParseAddr("192.0.2.92")
	second := netip.MustParseAddr("192.0.2.93")
	forbidden := netip.MustParseAddr("2001:db8::2c")

	leaf := newCapabilityLeaf(0, 0)
	leaf.inner.packetConn = &countingPacketConn{}
	// The forbidden family sits in the MIDDLE, so an implementation that stopped at the first admitted
	// address would still pass; this pins that the survivors keep the resolver's relative order.
	router := &serialNameRouter{addresses: []netip.Addr{first, forbidden, second}}

	dialer := &resolveDialer{
		router:       router,
		dialer:       leaf,
		parallel:     false,
		queryOptions: adapter.DNSQueryOptions{Strategy: C.DomainStrategyIPv4Only},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := dialer.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddrHostPort("bootstrap.example", 443))
	if conn != nil {
		conn.Close()
	}

	require.NoError(t, err)
	require.NotContains(t, leaf.tried(), forbidden, "the excluded address is removed")
	require.Contains(t, leaf.tried(), first, "and the resolver's first admitted address is the one dialled")
}

// Keep the buf import honest for the package's shared helpers.
var _ = buf.NewPacket
