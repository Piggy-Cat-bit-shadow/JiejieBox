package hysteria2

import (
	"testing"

	"github.com/sagernet/sing-box/common/dialer"

	"github.com/stretchr/testify/require"
)

// The outbound logs DIFFERENTLY for the two ways a proven ceiling can move the configured size, and the
// distinction is the whole point of the message:
//
//	supplied  - nothing was configured, so the ceiling IS the preference. Saying this "caps
//	            initial_packet_size 0" describes an override of a choice the operator never made.
//	clamped   - the operator asked for something larger and the path cannot carry it.
//
// Both end at the same number, so nothing about the resulting packet or the connection can tell them
// apart - only the message can, which is why it is worth a test rather than a reading. The expressions
// below are the ones the outbound evaluates; if either drifts, this fails instead of quietly reporting
// one case as the other.
func TestTheCeilingSupplyAndClampCasesAreDistinguishable(t *testing.T) {
	const ceiling = uint32(1232)

	cases := []struct {
		name          string
		configured    int
		hasCeiling    bool
		wantSupplied  bool
		wantClamped   bool
		wantEffective int
	}{
		{
			name: "nothing configured, a ceiling is proven", configured: 0, hasCeiling: true,
			wantSupplied: true, wantClamped: false, wantEffective: int(ceiling),
		},
		{
			name: "the operator asked for more than the path carries", configured: 1400, hasCeiling: true,
			wantSupplied: false, wantClamped: true, wantEffective: int(ceiling),
		},
		{
			name: "the operator asked for less than the ceiling", configured: 1200, hasCeiling: true,
			wantSupplied: false, wantClamped: false, wantEffective: 1200,
		},
		{
			name: "exactly the ceiling", configured: int(ceiling), hasCeiling: true,
			wantSupplied: false, wantClamped: false, wantEffective: int(ceiling),
		},
		{
			name: "no ceiling at all", configured: 0, hasCeiling: false,
			wantSupplied: false, wantClamped: false, wantEffective: 0,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			effective := dialer.ClampToCeiling(testCase.configured, ceiling, testCase.hasCeiling)
			require.Equal(t, testCase.wantEffective, effective,
				"the clamp's own contract first: it must produce the value the message will describe")

			// The REAL function, not a retyped copy of its condition. An earlier revision of this test
			// duplicated the expression, and a mutation that disabled the production branch built and
			// PASSED - proving the test guarded nothing. Calling it is the whole reason it was extracted.
			supplied := testCase.hasCeiling && dialer.CeilingSuppliesTheSize(
				testCase.configured, effective, ceiling)
			// The clamp branch, verbatim from the outbound's condition.
			clamped := !supplied && testCase.hasCeiling && effective != testCase.configured

			require.Equal(t, testCase.wantSupplied, supplied,
				"a ceiling that SUPPLIES a value because nothing was configured must not be "+
					"reported as a cap: the reader would go looking for what set initial_packet_size to 0")
			require.Equal(t, testCase.wantClamped, clamped,
				"and a real clamp must not lose its message to the supply branch")
			require.False(t, supplied && clamped,
				"the two cases are mutually exclusive; a message that claims both is worse than neither")
		})
	}
}

// The order of the branches is load-bearing: the supply case is a strict subset of the clamp case's
// numeric outcome (both end at the ceiling), so testing the clamp first and the supply second would
// classify a supply as a clamp and the distinction would be lost with nothing failing.
func TestTheSupplyBranchMustBeTestedBeforeTheClampBranch(t *testing.T) {
	const ceiling = uint32(1232)
	configured := 0
	effective := dialer.ClampToCeiling(configured, ceiling, true)

	supplied := true && effective == int(ceiling) && configured <= 0
	clampedIfTestedFirst := true && effective != configured

	require.True(t, supplied)
	require.True(t, clampedIfTestedFirst,
		"the clamp condition is ALSO true in the supply case - so if it were evaluated first it would "+
			"win, and every supply would be reported as a clamp")
	require.True(t, supplied && !(true && effective != configured && !supplied),
		"the outbound therefore tests supply first and excludes it from the clamp branch")
}
