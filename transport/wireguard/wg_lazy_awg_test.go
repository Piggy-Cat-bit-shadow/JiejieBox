//go:build with_awg

package wireguard

// W5 proof 6, AmneziaWG half: a real AWG pair through the production endpoints, the obfuscation
// parameters applied on both ends, a real handshake and data plane, and the same suspend/release/
// rebuild lifecycle as the plain WireGuard test. Plus the replay proof on an AWG session, where the
// recorded messages are not necessarily the plain transport type.
//
// The parameters are the wireguard_awg option surface an AWG profile sets. Both ends must agree on
// them for the handshake to complete, which is what makes the assertion meaningful: if the release
// dropped or failed to reapply them on rebuild, the rebuilt handshake would not complete.

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

func awgTestObfuscation() option.AmneziaWGOptions {
	return option.AmneziaWGOptions{
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
}

// TestARealAWGPairSurvivesASuspendReleaseAndRebuild is the AWG data-plane proof across the release.
func TestARealAWGPairSurvivesASuspendReleaseAndRebuild(t *testing.T) {
	pair := lazyStartPair(t, awgTestObfuscation())
	first := pair.dial(t)
	pair.exchange(t, first, "awg-before-suspend", 10*time.Second)

	pair.client.SetIdle(true)
	published, released := pair.client.lazyState()
	require.False(t, published, "a suspended eligible AWG endpoint must release its Device")
	require.True(t, released)

	pair.client.SetIdle(false)
	second := pair.dial(t)
	pair.exchange(t, second, "awg-after-rebuild", 10*time.Second)
	published, released = pair.client.lazyState()
	require.True(t, published)
	require.False(t, released)

	// The IpcSet on rebuild must have reapplied the same obfuscation parameters: the rebuilt
	// handshake completing is the observable proof.
	require.GreaterOrEqual(t, pair.handler.flows.Load(), int32(2))
	_ = second.Close()
	_ = first.Close()
}

// TestAReplayedAWGTransportMessageIsRejected runs the replay proof with the obfuscation lines applied
// to both devices: the same pinned replay filter must reject a byte-identical AWG datagram.
func TestAReplayedAWGTransportMessageIsRejected(t *testing.T) {
	obfuscation := awgTestObfuscation()
	awgLines, err := awgIpcLines(obfuscation)
	require.NoError(t, err)
	require.NotEmpty(t, awgLines)
	runReplayProof(t, awgLines)
}
