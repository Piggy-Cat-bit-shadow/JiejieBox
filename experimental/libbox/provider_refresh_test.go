package libbox

// PP03 — the binding's own contract: argument parsing, the millisecond budget, the JSON
// result, and the explicit error when there is no running box to refresh.

import (
	stdjson "encoding/json"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
)

func TestFormatRefreshSummaryIsThePlatformReport(t *testing.T) {
	summary := &daemon.ProviderRefreshSummary{
		Results: []daemon.ProviderRefreshResult{
			{Tag: "p1", Status: daemon.ProviderRefreshPublished, RequestedRevision: 2, AppliedRevision: 2},
			{Tag: "p2", Status: daemon.ProviderRefreshSkipped, Reason: "not a remote provider"},
		},
	}
	encoded, err := formatRefreshSummary(summary)
	require.NoError(t, err)

	var decoded daemon.ProviderRefreshSummary
	require.NoError(t, stdjson.Unmarshal([]byte(encoded), &decoded))
	require.Len(t, decoded.Results, 2)
	require.Equal(t, "p1", decoded.Results[0].Tag)
	require.EqualValues(t, daemon.ProviderRefreshPublished, decoded.Results[0].Status)
	require.Equal(t, "not a remote provider", decoded.Results[1].Reason)
}

func TestRefreshProvidersWithoutARunningBoxFailsClearly(t *testing.T) {
	// A CommandServer with no started service must say so, not report a successful no-op.
	server := &CommandServer{}
	_, err := server.RefreshProviders("", 1000)
	require.Error(t, err, "a refresh without a running box is an error the platform can show")
}

// TestRefreshProvidersAcceptsAnEmptyAndACommaSeparatedSelection keeps the argument shape the
// platforms pass: an empty selection means everything, and "a, b" is two tags.
func TestRefreshProvidersAcceptsAnEmptyAndACommaSeparatedSelection(t *testing.T) {
	for _, tags := range []string{"", "p1", "p1, p2", " p1 , p2 , "} {
		selection := splitRefreshSelection(tags)
		switch tags {
		case "":
			require.Empty(t, selection)
		case "p1":
			require.Equal(t, []string{"p1"}, selection)
		default:
			require.Equal(t, []string{"p1", "p2"}, selection)
		}
	}
	require.Equal(t, 30*time.Second, refreshTimeout(0))
	require.Equal(t, 5*time.Second, refreshTimeout(5000))
}
