package rule

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// TestIPVersionItemTreatsMappedAsIPv4 pins the family semantics the dual-stack planner and the
// strategy filter already use: an IPv4-mapped IPv6 address is an IPv4 destination. netip's Is6()
// alone reports true for ::ffff:192.0.2.1, which would make an `ip_version: 6` rule match an IPv4
// flow.
func TestIPVersionItemTreatsMappedAsIPv4(t *testing.T) {
	item4 := NewIPVersionItem(false)
	item6 := NewIPVersionItem(true)

	mapped := &adapter.InboundContext{
		Destination: M.SocksaddrFrom(netip.MustParseAddr("::ffff:192.0.2.1"), 443),
	}
	require.True(t, item4.Match(mapped), "a mapped address is IPv4 for ip_version")
	require.False(t, item6.Match(mapped), "a mapped address is not IPv6 for ip_version")

	ipv4 := &adapter.InboundContext{
		Destination: M.SocksaddrFrom(netip.MustParseAddr("192.0.2.1"), 443),
	}
	require.True(t, item4.Match(ipv4))
	require.False(t, item6.Match(ipv4))

	ipv6 := &adapter.InboundContext{
		Destination: M.SocksaddrFrom(netip.MustParseAddr("2001:db8::1"), 443),
	}
	require.False(t, item4.Match(ipv6))
	require.True(t, item6.Match(ipv6))
}
