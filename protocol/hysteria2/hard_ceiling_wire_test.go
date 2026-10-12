package hysteria2

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The wire matrix for the round's central split: PMTUD policy and a hard packet-size
// ceiling are two different statements, and each now has its own field.
//
// Every row is MEASURED - the first datagram a real loopback socket receives from the
// real pinned QUIC stack - rather than read out of a configuration struct. A
// config-level assertion cannot tell a correct design from the defect this replaces:
// the defect was precisely that a field MEANT one thing while the library READ another,
// and both readings agreed at the config level.
//
// hysteria2's default is ChromeParrot ON, so rows B through G are the default path and
// row A is the control that shows the fingerprint override is real.

func TestHardCeilingWireMatrix(t *testing.T) {
	cases := []struct {
		name string
		req  measureRequest
		want int
		why  string
	}{
		{
			name: "A: ChromeParrot off keeps the configured size",
			req: measureRequest{
				chromeParrot: false, configuredInitialPacketSize: 1232,
			},
			want: 1232,
			why: "the control: with no fingerprint override in play, the configured value is what " +
				"goes out, so a later row that changes it is changing something real",
		},
		{
			name: "B: ChromeParrot on with no ceiling still emits the fingerprint size",
			req: measureRequest{
				chromeParrot: true, configuredInitialPacketSize: 1232,
			},
			want: chromeParrotInitialPacketSize,
			why: "the DIRECT-PATH contract. An operator's initial_packet_size is discarded in favour " +
				"of the fingerprint, and that predates this round; a ceiling is what may override the " +
				"fingerprint, and there is no ceiling here",
		},
		{
			name: "C: a 1232 ceiling clamps the fingerprint size",
			req: measureRequest{
				chromeParrot: true, configuredInitialPacketSize: 1232, hardCeiling: 1232,
			},
			want: 1232,
			why: "the case the whole change exists for: a path proven to carry 1232 bytes must not be " +
				"asked to carry Chrome's 1250",
		},
		{
			name: "D: a 1232 ceiling supplies the size when nothing was configured",
			req: measureRequest{
				chromeParrot: true, configuredInitialPacketSize: 0, hardCeiling: 1232,
			},
			want: 1232,
			why:  "zero means no preference, and the ceiling is the only size information available",
		},
		{
			name: "E: a ceiling above the fingerprint size does not raise it",
			req: measureRequest{
				chromeParrot: true, configuredInitialPacketSize: 1250, hardCeiling: 1252,
			},
			want: 1250,
			why: "a ceiling is a MAXIMUM, not a target: 1252 is capacity the path has, not a size " +
				"anything should be inflated to",
		},
		{
			name: "F: a 1200 ceiling clamps down to the QUIC minimum",
			req: measureRequest{
				chromeParrot: true, configuredInitialPacketSize: 1250, hardCeiling: 1200,
			},
			want: 1200,
			why: "1200 is the smallest an Initial packet may be, so it is a usable ceiling and must be " +
				"honoured exactly",
		},
		{
			name: "G: a direct dial with the user's own PMTUD policy keeps the fingerprint size",
			req: measureRequest{
				chromeParrot: true, configuredInitialPacketSize: 1232, disablePMTUD: true,
			},
			want: chromeParrotInitialPacketSize,
			why: "THE DETECTOR for this round's design. The operator turned PMTUD off and configured " +
				"1232; there is no proven ceiling, so the fingerprint size must still win. Under the old " +
				"design this pair of fields WAS the ceiling marker, so this row would have emitted 1232 - " +
				"their preference read as a capacity constraint, and their fingerprint silently traded away",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			observation := measureFirstDatagram(t, testCase.req)
			require.Equal(t, testCase.want, observation.size, "%s", testCase.why)
		})
	}
}

// TestHardCeilingDoesNotCostTheChromeFingerprint is the side proof: a ceiling is a size
// constraint, NOT a way to turn the fingerprint off, and the two must not be conflated in
// the other direction either. The 1232 ceiling case is measured with the two connection-ID
// side effects the ChromeParrot branch is responsible for - a fixed 8-byte initial
// destination connection ID and a zero-length source connection ID - so a future change
// that "fixes" the ceiling by disabling ChromeParrot fails here.
func TestHardCeilingDoesNotCostTheChromeFingerprint(t *testing.T) {
	withCeiling := measureFirstDatagram(t, measureRequest{
		chromeParrot: true, configuredInitialPacketSize: 1232, hardCeiling: 1232,
	})
	require.Equal(t, 1232, withCeiling.size)

	dcidLen, scidLen, ok := parseInitialHeaderLengths(withCeiling.head)
	require.True(t, ok, "could not parse the connection ID lengths of the first datagram: % x", withCeiling.head)
	require.Equal(t, 8, dcidLen,
		"a ceiling must not cost the Chrome fingerprint: the initial destination connection ID is "+
			"Chrome's fixed 8 bytes whenever that branch runs")
	require.Equal(t, 0, scidLen,
		"a ceiling must not cost the Chrome fingerprint: the source connection ID is zero-length "+
			"whenever that branch runs")

	// And the same fingerprint is present when the ceiling is what clamps: the two runs
	// differ in size only.
	t.Logf("1232 ceiling with ChromeParrot on: first datagram %d bytes, destination connection ID %d, "+
		"source connection ID %d", withCeiling.size, dcidLen, scidLen)
}
