package libbox

// C01C02 — AnalyzeConfig's declaration checks, and the line it must not cross.
//
// The analysis answers "is this document well formed and are its references resolvable"
// without building a box. These cases pin the four categories the task names:
//   - parsable and declaration-clean;
//   - parsable but a declaration is wrong (a dangling reference is found before construction);
//   - parsable but construction would fail (a group member that a fetch would provide is
//     PENDING, not an error, and an unreachable provider is never contacted);
//   - values that are simply defaulted or absent, which must not be reported as problems.

import (
	stdjson "encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// analyse decodes the analysis JSON back into the struct the app sees.
func analyse(t *testing.T, configContent string) ConfigAnalysis {
	t.Helper()
	boxed, err := AnalyzeConfig(configContent)
	require.NoError(t, err)
	var analysis ConfigAnalysis
	require.NoError(t, stdjson.Unmarshal([]byte(boxed.Value), &analysis))
	return analysis
}

func findingCodes(analysis ConfigAnalysis) []string {
	codes := make([]string, 0, len(analysis.Findings))
	for _, finding := range analysis.Findings {
		codes = append(codes, finding.Code)
	}
	return codes
}

func TestAnalysisIsParsedButNotRuntimeChecked(t *testing.T) {
	analysis := analyse(t, `{"outbounds": [{"type": "direct", "tag": "direct"}]}`)
	require.True(t, analysis.Parsed)
	require.True(t, analysis.Checks.Parsed)
	require.True(t, analysis.Checks.DeclarationsChecked, "a parsed document has had its declarations checked")
	require.False(t, analysis.Checks.RuntimeChecked, "analyze never builds a box, and must not claim it did")
	require.NotEmpty(t, analysis.BuildID, "a report must name the build that produced it")
	require.Len(t, analysis.BuildID, 16)
	require.NotEmpty(t, analysis.ConfigRevision)
	require.Empty(t, analysis.Findings)
}

func TestAnalysisRevisionFollowsTheDocument(t *testing.T) {
	first := analyse(t, `{"outbounds": [{"type": "direct", "tag": "direct"}]}`)
	second := analyse(t, `{"outbounds": [{"type": "direct", "tag": "direct"}]}`)
	edited := analyse(t, `{"outbounds": [{"type": "direct", "tag": "other"}]}`)
	require.Equal(t, first.ConfigRevision, second.ConfigRevision, "the same bytes are the same revision")
	require.NotEqual(t, first.ConfigRevision, edited.ConfigRevision, "an edit is a different revision")
}

func TestAnalysisFindsADanglingGroupMember(t *testing.T) {
	analysis := analyse(t, `{"outbounds": [{"type": "selector", "tag": "select", "outbounds": ["missing"]}]}`)
	require.True(t, analysis.Parsed)
	require.Contains(t, findingCodes(analysis), findingDanglingReference,
		"a member that cannot exist is a declaration problem, found before construction")
	require.True(t, analysis.Checks.DeclarationsChecked)
	require.False(t, analysis.Checks.RuntimeChecked)
}

func TestAnalysisFindsADanglingFinal(t *testing.T) {
	analysis := analyse(t, `{"outbounds": [{"type": "direct", "tag": "direct"}], "route": {"final": "missing"}}`)
	require.Contains(t, findingCodes(analysis), findingDanglingReference)
}

// TestAnalysisRejectsDuplicateTagsAtParseTime records where duplicates are caught: the
// config decoder already refuses them, so the analysis never sees such a document and must
// not claim to have found it later.
func TestAnalysisRejectsDuplicateTagsAtParseTime(t *testing.T) {
	_, err := AnalyzeConfig(`{"outbounds": [{"type": "direct", "tag": "same"}, {"type": "direct", "tag": "same"}]}`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate")
}

func TestAnalysisTreatsProviderRenderedMembersAsPendingNotBroken(t *testing.T) {
	configContent := `{
		"providers": [{"type": "remote", "tag": "p1", "url": "http://127.0.0.1:1/sub"}],
		"outbounds": [{"type": "selector", "tag": "select", "outbounds": ["p1/node-a"], "providers": ["p1"]}]
	}`
	analysis := analyse(t, configContent)
	require.Empty(t, findingCodes(analysis), "a provider-rendered member is not a dangling reference")
	require.Len(t, analysis.PendingProviders, 1)
	require.Equal(t, "p1", analysis.PendingProviders[0].Tag)
	require.Equal(t, "remote", analysis.PendingProviders[0].Type)
}

// TestAnalysisNeverFetchesAProvider is the "analyze must not build" boundary: the provider
// points at a closed port, and the analysis still returns promptly with the provider pending.
func TestAnalysisNeverFetchesAProvider(t *testing.T) {
	configContent := `{
		"providers": [{"type": "remote", "tag": "p1", "url": "http://127.0.0.1:1/sub"}],
		"outbounds": [{"type": "selector", "tag": "select", "outbounds": ["p1/x"], "providers": ["p1"]}]
	}`
	start := time.Now()
	boxed, err := AnalyzeConfig(configContent)
	require.NoError(t, err)
	require.Less(t, time.Since(start), 2*time.Second, "analyze must not wait on a provider download")
	require.Contains(t, boxed.Value, `"p1"`)
}

func TestAnalysisDoesNotLeakCredentials(t *testing.T) {
	configContent := `{
		"providers": [{"type": "remote", "tag": "p1", "url": "https://user:sup3rsecret@example.invalid/sub"}],
		"outbounds": [{"type": "selector", "tag": "select", "outbounds": ["p1/x"], "providers": ["p1"]}]
	}`
	boxed, err := AnalyzeConfig(configContent)
	require.NoError(t, err)
	require.NotContains(t, strings.ToLower(boxed.Value), "sup3rsecret",
		"the analysis output must never carry a credential from the document")
}
