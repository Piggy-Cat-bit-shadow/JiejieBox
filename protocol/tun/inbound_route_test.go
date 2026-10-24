package tun

import (
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-tun"

	"github.com/stretchr/testify/require"
)

// With auto_route off, a tun inbound installs only the user's explicit route addresses. Forcing the
// shared default table there put them in a table an auto_route instance also owns, and combined with
// sing-tun's IPv6 interface rule (which pointed at the same table) an IPv6-only instance resolved in
// an empty or foreign table. Upstream 18aa2cdfe74 gates the default; these tests pin the mapping.
func TestIPRoute2TableIndexWithoutAutoRoute(t *testing.T) {
	require.Zero(t, ipRoute2TableIndex(option.TunInboundOptions{AutoRoute: false}),
		"auto_route=false must not force the shared default table")
	require.Equal(t, 2022, ipRoute2TableIndex(option.TunInboundOptions{AutoRoute: false, IPRoute2TableIndex: 2022}),
		"an explicit table index must be honoured")
}

func TestIPRoute2TableIndexWithAutoRoute(t *testing.T) {
	require.Equal(t, tun.DefaultIPRoute2TableIndex, ipRoute2TableIndex(option.TunInboundOptions{AutoRoute: true}),
		"auto_route=true keeps the default table")
	require.Equal(t, 2022, ipRoute2TableIndex(option.TunInboundOptions{AutoRoute: true, IPRoute2TableIndex: 2022}),
		"an explicit table index must win over the default")
}

func TestIPRoute2RuleIndexKeepsDefault(t *testing.T) {
	require.Equal(t, tun.DefaultIPRoute2RuleIndex, ipRoute2RuleIndex(option.TunInboundOptions{}))
	require.Equal(t, 7, ipRoute2RuleIndex(option.TunInboundOptions{IPRoute2RuleIndex: 7}))
}
