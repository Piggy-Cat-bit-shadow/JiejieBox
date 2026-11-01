package dialer

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// TestAsISLeadingMappedIPv4KeepsTheIPv4Family covers both network entry points. A caller may hand
// the dialer an IPv4-mapped candidate before a native IPv6 one (a TUN metadata list or a resolver
// answer can both do this). AsIS means "follow the list's leading family" when there is no
// original; the leading family is IPv4 because ::ffff:a.b.c.d is an IPv4 destination. Reading
// netip.Addr.Is6() alone turns that mapped address into "IPv6" and flips the whole list to
// prefer_ipv6 - the stream and the packet walk then start on the wrong family.
func TestAsISLeadingMappedIPv4KeepsTheIPv4Family(t *testing.T) {
	mappedIPv4 := netip.MustParseAddr("::ffff:192.0.2.92")
	nativeIPv6 := netip.MustParseAddr("2001:db8::15")
	destination := M.ParseSocksaddrHostPort("rr1---sn-a5meknsy.googlevideo.com", 443)

	assertIPv4 := func(t *testing.T, address netip.Addr, what string) {
		t.Helper()
		require.True(t, address.Is4() || address.Is4In6(), "%s: got %s, want the IPv4 family", what, address)
	}

	t.Run("tcp", func(t *testing.T) {
		chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyAsIS)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		conn, err := chain.dialSerialNetwork(ctx, "tcp", destination, []netip.Addr{mappedIPv4, nativeIPv6})
		require.NoError(t, err)
		conn.Close()

		tried := chain.tried()
		require.NotEmpty(t, tried)
		assertIPv4(t, tried[0], "first TCP attempt")
		require.NotContains(t, tried[:1], nativeIPv6, "AsIS must not lead with the native IPv6 candidate")
	})

	t.Run("udp", func(t *testing.T) {
		chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyAsIS)
		chain.inner.packetConn = &countingPacketConn{}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		packetConn, destinationAddress, err := chain.listenSerialNetworkPacketAtTheOutbound(ctx, destination, []netip.Addr{mappedIPv4, nativeIPv6})
		require.NoError(t, err)
		require.NotNil(t, packetConn)
		packetConn.Close()

		assertIPv4(t, destinationAddress, "first UDP destination")
		tried := chain.tried()
		require.NotEmpty(t, tried)
		assertIPv4(t, tried[0], "first UDP attempt")
	})
}
