package libbox

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/route"

	"github.com/stretchr/testify/require"
)

// The resolve stage of the connection diagnostic has to tell two different outcomes apart that
// share every visible field:
//
//	`resolve` (default)  the answer became the dial candidates, so it IS the route decision
//	`resolve.match_only` the answer selected the rule, and the dial kept the name
//
// Rendering both through the dial-candidate list made the match-only case report "no address" for a
// resolution that succeeded - a report that contradicts its own DNS stage, which shows the answers.
// The distinction now lives in one function so it can be asserted without standing up a whole
// diagnostic.
func TestResolveActionStageTellsMatchingEvidenceFromDialCandidates(t *testing.T) {
	t.Run("match only shows the answer and says it is not the dial target", func(t *testing.T) {
		detail := resolveActionStageDetail("match.example", route.RouteDiagnoseResult{
			Resolve: route.RouteDiagnoseResolve{
				Performed: true,
				MatchOnly: true,
				Addresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")},
			},
		})
		require.Contains(t, detail, "[198.51.100.10]",
			"the answers must be shown: the resolution succeeded")
		require.Contains(t, detail, "match only",
			"and the report must say the answers were not used for dialling")
		require.NotContains(t, detail, "route decision",
			"it must not claim the answers are the route decision's destination")
		require.NotContains(t, detail, "no address",
			"and must not read as a failed resolution")
	})

	t.Run("the default action is the route decision", func(t *testing.T) {
		detail := resolveActionStageDetail("dial.example", route.RouteDiagnoseResult{
			Resolve: route.RouteDiagnoseResolve{
				Performed: true,
				Addresses: []netip.Addr{netip.MustParseAddr("203.0.113.9")},
			},
			DestinationAddresses: []netip.Addr{netip.MustParseAddr("203.0.113.9")},
		})
		require.Contains(t, detail, "[203.0.113.9]")
		require.Contains(t, detail, "route decision")
		require.NotContains(t, detail, "match only")
	})

	t.Run("a match only action with an empty answer does not borrow the candidate rendering", func(t *testing.T) {
		// The shape a resolver that answered with nothing would produce. The stage still has to say
		// that the action ran for matching only rather than implying the default mode produced no
		// dial candidate.
		detail := resolveActionStageDetail("empty.example", route.RouteDiagnoseResult{
			Resolve: route.RouteDiagnoseResolve{Performed: true, MatchOnly: true},
		})
		require.Contains(t, detail, "match only")
		require.Contains(t, detail, "no address")
	})
}
