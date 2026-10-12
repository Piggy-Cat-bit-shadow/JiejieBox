package hysteria2

import (
	"testing"

	quic "github.com/sagernet/quic-go"
	"github.com/sagernet/sing-box/common/dialer"
	qtls "github.com/sagernet/sing-quic"

	"github.com/stretchr/testify/require"
)

// The two sizing facts a hysteria2 connection is given, enumerated rather than sampled.
//
// This test exists because the wire harness CANNOT see the difference. A direct dial with the
// operator's own `disable_path_mtu_discovery` and a direct dial without it produce the same first
// datagram - the fingerprint size wins in both, because neither has a ceiling - so a change that
// silently swallowed the flag again would leave every datagram assertion green. It was measured
// that way: the mutation `DisablePathMTUDiscovery: hasCeiling` (the old behaviour, which drops the
// operator's setting) passed the entire wire matrix and every other test in this package.
//
// So the assertion is made where the decision is made, on the real function the outbound calls,
// and it is made one field at a time: a change that starts deriving either fact from the other
// fails here even when the derived value suits the configuration under test.

func TestTheCeilingAndThePMTUDPolicyStayIndependent(t *testing.T) {
	const ceiling = uint32(1232)

	cases := []struct {
		name             string
		userDisablePMTUD bool
		hasCeiling       bool
		wantCeiling      int
		wantDisablePMTUD bool
		why              string
	}{
		{
			name: "a direct dial, nothing set",
			// The compatibility promise: no detour, no policy, so nothing changes and quic-go keeps
			// its own defaults for both fields.
			userDisablePMTUD: false, hasCeiling: false,
			wantCeiling: 0, wantDisablePMTUD: false,
			why: "an unknown path with no policy must leave both fields at quic-go's defaults",
		},
		{
			name: "a direct dial where the operator turned PMTUD off",
			// THE DETECTOR for this half of the round. Under the old design this combination WAS the
			// ceiling signal, and it was also swallowed: the operator asked for PMTUD off and got it
			// back on, while their configured size was read as a proven capacity.
			userDisablePMTUD: true, hasCeiling: false,
			wantCeiling: 0, wantDisablePMTUD: true,
			why: "a policy is not a measurement: it must turn PMTUD off and must NOT invent a ceiling",
		},
		{
			name:             "a proven ceiling, no policy",
			userDisablePMTUD: false, hasCeiling: true,
			wantCeiling: int(ceiling), wantDisablePMTUD: true,
			why: "the ceiling is forwarded as a ceiling, and it turns PMTUD off because growth past a " +
				"proven capacity is unsafe (measured 1232 -> 1438 with PMTUD on)",
		},
		{
			name:             "both, which is what a proven ceiling's caller sends",
			userDisablePMTUD: true, hasCeiling: true,
			wantCeiling: int(ceiling), wantDisablePMTUD: true,
			why: "the two reasons coincide here, and neither may cancel or overwrite the other",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			sizing := quicPathSizing(testCase.userDisablePMTUD, ceiling, testCase.hasCeiling)
			require.Equal(t, testCase.wantCeiling, sizing.HardCeiling, "%s", testCase.why)
			require.Equal(t, testCase.wantDisablePMTUD, sizing.DisablePMTUD, "%s", testCase.why)
		})
	}
}

// TestTheSizingReachesQuicConfigThroughThePassThroughLayer closes the last gap between the decision
// above and the library: it feeds the sizing through the SAME call the hysteria2 client uses
// (qtls.ApplyQUICOptions) and reads quic-go's config back.
//
// Without this, the two halves could each be right and still not meet: the outbound could decide
// correctly and the pass-through layer could drop a field, which is exactly the shape of the defect
// being fixed - the ceiling used to be expressed as a side effect of a boolean, and nothing checked
// that the boolean arrived as anything but a boolean.
func TestTheSizingReachesQuicConfigThroughThePassThroughLayer(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		userDisablePMTUD bool
		hasCeiling       bool
	}{
		{name: "direct, nothing set"},
		{name: "direct, operator policy", userDisablePMTUD: true},
		{name: "ceiling", hasCeiling: true},
		{name: "ceiling and policy", userDisablePMTUD: true, hasCeiling: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			sizing := quicPathSizing(testCase.userDisablePMTUD, 1232, testCase.hasCeiling)

			// The same call the hysteria2 client makes: a Config plus the options, applied.
			config := &quic.Config{}
			qtls.ApplyQUICOptions(config, qtls.QUICOptions{
				InitialPacketSize:        dialer.ClampToCeiling(1232, 1232, testCase.hasCeiling),
				InitialPacketSizeCeiling: sizing.HardCeiling,
				DisablePathMTUDiscovery:  sizing.DisablePMTUD,
			})

			require.Equal(t, sizing.HardCeiling, int(config.InitialPacketSizeCeiling),
				"the ceiling the outbound decided must be the ceiling quic-go receives")
			require.Equal(t, sizing.DisablePMTUD, config.DisablePathMTUDiscovery,
				"and the PMTUD policy it decided must be the policy quic-go receives")
		})
	}
}
