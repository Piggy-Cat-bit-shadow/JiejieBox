package httpclient

import (
	"testing"

	"github.com/sagernet/sing-box/option"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

// TestHTTPClientTransportRequirement pins the configured transport selection, which is the only
// thing that may decide a consumer's demand.
//
// The case that matters most is `version: 3` with and without `disable_version_fallback`, because it
// is the one place in this tree where an HTTP client does NOT need TCP. A demand model that said
// "an HTTP client needs TCP" - the obvious rule, and the one the closure prompt forbids hardcoding -
// would refuse a UDP-only detour that works, and would simultaneously miss that the fallback case
// needs BOTH.
func TestHTTPClientTransportRequirement(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		options option.HTTPClientOptions
		want    []string
	}{
		"version 1": {
			options: option.HTTPClientOptions{Version: 1},
			want:    []string{N.NetworkTCP},
		},
		"version 2": {
			options: option.HTTPClientOptions{Version: 2},
			want:    []string{N.NetworkTCP},
		},
		"version 0 takes the version 2 default": {
			options: option.HTTPClientOptions{},
			want:    []string{N.NetworkTCP},
		},
		"version 3 with the fallback needs UDP and TCP": {
			options: option.HTTPClientOptions{Version: 3},
			want:    []string{N.NetworkUDP, N.NetworkTCP},
		},
		"version 3 with disable_version_fallback needs UDP only": {
			options: option.HTTPClientOptions{Version: 3, DisableVersionFallback: true},
			want:    []string{N.NetworkUDP},
		},
		"the flag does not change version 2": {
			options: option.HTTPClientOptions{Version: 2, DisableVersionFallback: true},
			want:    []string{N.NetworkTCP},
		},
	} {
		name, testCase := name, testCase
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requirement := HTTPClientTransportRequirement(testCase.options)
			require.ElementsMatch(t, testCase.want, requirement.Required,
				"the requirement must come from the configured selection, not the protocol label")
		})
	}

	// The UDP-only case must NOT also claim TCP: that is the difference between "H3 alone" and
	// "H3 with an H2 fallback", and collapsing it would refuse a working UDP-only detour.
	udpOnly := HTTPClientTransportRequirement(option.HTTPClientOptions{Version: 3, DisableVersionFallback: true})
	require.NotContains(t, udpOnly.Required, N.NetworkTCP,
		"with the fallback disabled nothing reaches TCP, so requiring it would be a false deny")
}
