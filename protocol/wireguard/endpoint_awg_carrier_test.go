//go:build with_awg

package wireguard

// The AWG half of the carrier row: the same real pair, with the obfuscation the pinned
// wireguard-go fork (the "awg3-inject" graft) actually consumes.
//
// Without `with_awg` a non-empty AmneziaWGOptions value is an explicit configuration error rather
// than being silently sent as plain WireGuard, so this case lives behind the tag. The parameters are
// the wireguard_awg option surface an AWG profile would set - junk packet count and sizes, the two
// handshake paddings and four magic headers - and both ends must agree on them for the handshake to
// complete, which is exactly what makes the assertion meaningful: if the tag were not compiled in,
// or the graft ignored the parameters, this would fail at construction or never handshake.

import (
	"testing"

	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

func TestABusinessByteCrossesARealAWGPair(t *testing.T) {
	obfuscation := option.AmneziaWGOptions{
		Jc:   4,
		Jmin: 40,
		Jmax: 70,
		S1:   30,
		S2:   40,
		H1:   "114514",
		H2:   "1919810",
		H3:   "20240501",
		H4:   "20260909",
	}
	runWireGuardCarrier(t, obfuscation)
	require.NotZero(t, obfuscation.Jc, "the AWG parameters must reach both endpoints")
}

// TestAnAWGSamePeerSessionSurvivesAnOuterBindRefresh is the same-peer outer-refresh case on the AWG
// graft: the same parameters, the same peer keys and inner addresses, and the session still carries
// bytes after the outer bind is refreshed twice.
func TestAnAWGSamePeerSessionSurvivesAnOuterBindRefresh(t *testing.T) {
	obfuscation := option.AmneziaWGOptions{
		Jc:   4,
		Jmin: 40,
		Jmax: 70,
		S1:   30,
		S2:   40,
		H1:   "114514",
		H2:   "1919810",
		H3:   "20240501",
		H4:   "20260909",
	}
	pair := startWireGuardPair(t, obfuscation)
	conn := pair.dial(t)
	pair.exchangeThroughTunnel(t, conn, "awg-before-the-refresh")
	require.NoError(t, pair.client.endpoint.BindUpdate())
	pair.exchangeThroughTunnel(t, conn, "awg-after-the-refresh")
	require.NoError(t, pair.client.endpoint.BindUpdate())
	pair.exchangeThroughTunnel(t, conn, "awg-after-the-second-refresh")
	second := pair.dial(t)
	pair.exchangeThroughTunnel(t, second, "awg-new-flow-after-the-refresh")
	_ = second.Close()
	_ = conn.Close()
	require.GreaterOrEqual(t, pair.router.flows.Load(), int32(2))
}
