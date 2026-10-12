package httpclient

import (
	"testing"

	"github.com/sagernet/sing-box/option"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

// TestHTTPClientTransportRequirement pins the CONTRACT, not just the implementation.
//
// # The contract: H-PROVABLE
//
// Startup refuses only when NO successful request path can exist. A network reachable only through a
// fallback branch does not become a startup requirement.
//
// What follows, and why this test changed twice:
//
//	version 3 WITHOUT disable_version_fallback
//	    AnyOf = {UDP, TCP}   at least one, because roundTripHTTP3 can use either
//
//	version 3 WITH disable_version_fallback
//	    AllOf = {UDP}        H3 alone; there is no H2 transport at all
//
// `http3FallbackTransport` has three paths (http3_transport.go:138-160): a broken authority or a
// non-NoCachedConn H3 error goes straight to the H2 fallback (:139, :147); a NON-replayable request
// takes H3 alone and returns its error (:151-158); everything else races H3 against H2 (:164-261).
//
// So over a TCP-only detour a replayed request IS served by the H2 side, and over a UDP-only detour
// the H3 path serves. Neither transport is individually required; carrying NEITHER is the only
// provably fatal state. The first version of this model listed TCP as hard-required and refused the
// TCP-only case, which is the false deny this contract removes.
func TestHTTPClientTransportRequirement(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		options option.HTTPClientOptions
		allOf   []string
		anyOf   []string
	}{
		"version 1 needs TCP": {
			options: option.HTTPClientOptions{Version: 1},
			allOf:   []string{N.NetworkTCP},
		},
		"version 2 needs TCP": {
			options: option.HTTPClientOptions{Version: 2},
			allOf:   []string{N.NetworkTCP},
		},
		"version 0 takes the version 2 default": {
			options: option.HTTPClientOptions{},
			allOf:   []string{N.NetworkTCP},
		},
		"version 3 with the fallback needs at least one of UDP, TCP": {
			options: option.HTTPClientOptions{Version: 3},
			anyOf:   []string{N.NetworkUDP, N.NetworkTCP},
		},
		"version 3 with disable_version_fallback needs UDP alone": {
			options: option.HTTPClientOptions{Version: 3, DisableVersionFallback: true},
			allOf:   []string{N.NetworkUDP},
		},
		"the flag does not change version 2": {
			options: option.HTTPClientOptions{Version: 2, DisableVersionFallback: true},
			allOf:   []string{N.NetworkTCP},
		},
	} {
		name, testCase := name, testCase
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requirement := HTTPClientTransportRequirement(testCase.options)
			require.ElementsMatch(t, testCase.allOf, requirement.AllOf)
			require.ElementsMatch(t, testCase.anyOf, requirement.AnyOf)
		})
	}

	// The load-bearing assertions. M-A1 attacks the first, M-A2 the second.
	h3Only := HTTPClientTransportRequirement(option.HTTPClientOptions{Version: 3, DisableVersionFallback: true})
	require.Contains(t, h3Only.AllOf, N.NetworkUDP,
		"H3-only has no other transport, so UDP is required for any success")

	fallbackEnabled := HTTPClientTransportRequirement(option.HTTPClientOptions{Version: 3})
	require.ElementsMatch(t, []string{N.NetworkUDP, N.NetworkTCP}, fallbackEnabled.AnyOf,
		"either transport can serve a request, so the requirement is disjunctive")
	require.Empty(t, fallbackEnabled.AllOf,
		"and neither is individually required - promoting one refuses a working configuration")
	require.Contains(t, fallbackEnabled.Conditional, N.NetworkUDP,
		"a NON-replayable body takes the H3-only path (:151), which is recorded as conditional "+
			"rather than required")
}
