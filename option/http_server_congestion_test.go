package option

import (
	"context"
	"testing"

	"github.com/sagernet/sing/common/json"

	"github.com/stretchr/testify/require"
)

// TestHTTPInboundDecodesServerCongestionKnobs pins the real decode path.
//
// For version 3 the inbound's remaining keys are decoded into HTTP3Options by
// unmarshalHTTPVersionsOptions, so quic_congestion_control lands in
// HTTP3Options.CongestionControl while bbr_profile stays a top-level field. The
// server used to read only the latter while the former was silently ignored;
// this test is what proves the option arrives where the listener can see it.
func TestHTTPInboundDecodesServerCongestionKnobs(t *testing.T) {
	ctx := context.Background()

	var options HTTPInboundOptions
	err := json.UnmarshalContext(ctx, []byte(`{
		"listen": "127.0.0.1",
		"listen_port": 443,
		"version": 3,
		"bbr_profile": "standard",
		"quic_congestion_control": "cubic",
		"users": [{"username": "u", "password": "p"}]
	}`), &options)
	require.NoError(t, err)
	require.Equal(t, "standard", options.BBRProfile,
		"bbr_profile is a top-level inbound field")
	require.Equal(t, "cubic", options.HTTP3Options.CongestionControl,
		"quic_congestion_control must decode into the QUIC option set the listener reads")
}

// TestHTTPInboundServerCongestionControlValidation covers the construction-time
// rules shared with the masque endpoint:
//   - bbr_profile and quic_congestion_control are mutually exclusive;
//   - unknown algorithms are refused;
//   - every documented value passes when bbr_profile is unset.
func TestHTTPInboundServerCongestionControlValidation(t *testing.T) {
	resolve := func(bbrProfile string, quic QUICOptions) error {
		options := HTTPInboundOptions{BBRProfile: bbrProfile}
		options.HTTP3Options = quic
		_, err := options.ResolveServerResources()
		return err
	}

	t.Run("mutually exclusive", func(t *testing.T) {
		err := resolve("standard", QUICOptions{CongestionControl: "cubic"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "mutually exclusive")
	})

	t.Run("bbr_profile alone is unchanged", func(t *testing.T) {
		for _, profile := range []string{"standard", "conservative", "aggressive"} {
			require.NoError(t, resolve(profile, QUICOptions{}),
				"bbr_profile %q must keep working", profile)
		}
	})

	t.Run("accepted algorithms", func(t *testing.T) {
		for _, name := range []string{"", "default", "cubic", "bbr", "reno"} {
			require.NoError(t, resolve("", QUICOptions{CongestionControl: name}),
				"quic_congestion_control %q must be accepted", name)
		}
	})

	t.Run("unknown algorithm is refused", func(t *testing.T) {
		err := resolve("", QUICOptions{CongestionControl: "BBR"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "unknown quic_congestion_control")
	})

	t.Run("empty congestion control does not conflict with bbr_profile", func(t *testing.T) {
		require.NoError(t, resolve("standard", QUICOptions{CongestionControl: ""}))
	})
}
