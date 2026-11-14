//go:build with_naive_outbound

package naive

import (
	"testing"

	"github.com/sagernet/cronet-go"

	"github.com/stretchr/testify/require"
)

// TestResolveCronetCongestionControl pins the outbound default.
//
// Unset and explicit "cubic" must select the same Cronet tag (CQBC), not
// Cronet's build default: "unset" has to mean the same algorithm as the naive
// inbound, both trusttunnel halves and the HTTP/3 client, and a build default can
// move with a dependency bump. BBR and Reno stay available by name.
func TestResolveCronetCongestionControl(t *testing.T) {
	cases := []struct {
		name string
		want cronet.QUICCongestionControl
	}{
		{name: "", want: cronet.QUICCongestionControlCubic},
		{name: "cubic", want: cronet.QUICCongestionControlCubic},
		{name: "bbr", want: cronet.QUICCongestionControlBBR},
		{name: "bbr2", want: cronet.QUICCongestionControlBBRv2},
		{name: "reno", want: cronet.QUICCongestionControlReno},
	}
	for _, testCase := range cases {
		t.Run("value="+testCase.name, func(t *testing.T) {
			got, err := resolveCronetCongestionControl(testCase.name)
			require.NoError(t, err)
			require.Equal(t, testCase.want, got)
		})
	}
}

// TestResolveCronetCongestionControlRejectsUnknown keeps the exact matching.
func TestResolveCronetCongestionControlRejectsUnknown(t *testing.T) {
	for _, name := range []string{"BBR", "Cubic", " bbr", "bbr ", "default", "vegas", "bbr3"} {
		got, err := resolveCronetCongestionControl(name)
		require.Error(t, err, "value %q must be rejected", name)
		require.Empty(t, got)
		require.Contains(t, err.Error(), "unknown quic congestion control")
	}
}
