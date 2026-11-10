package libbox

// The client half of the manual provider refresh, as a pure decision.
//
// Two facts a caller has to be able to rely on, and neither is visible from a successful call:
//
//   - a whole refresh is bounded even when the caller asked for no bound, because the alternative
//     is an app-side call that waits for every provider in sequence;
//   - a kernel that predates the RPC answers with a DOCUMENT, because a gomobile call that returns
//     an error loses its value in the generated bridge and the caller would only see a failure.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTheBatchDeadlineIsDerivedFromThePerProviderBound(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		provider time.Duration
		expected time.Duration
	}{
		{"unset falls back to the kernel default", 0, maxProviderRefreshBatch},
		{"the apple client's 15s becomes a bounded batch", 15 * time.Second, 60 * time.Second},
		{"a very small bound still allows a round trip", time.Second, minProviderRefreshBatch},
		{"the ceiling is not exceeded", 60 * time.Second, maxProviderRefreshBatch},
		{"an hour per provider does not become four hours", time.Hour, maxProviderRefreshBatch},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.expected, refreshBatchDeadline(testCase.provider))
		})
	}
}

func TestAMissingRPCEntsASACapabilityAnswerAndNotAnError(t *testing.T) {
	document := encodeUnsupportedProviderRefresh("the running kernel predates RefreshRemoteProviders")
	var decoded struct {
		Supported bool   `json:"supported"`
		Reason    string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal([]byte(document), &decoded), document)
	require.False(t, decoded.Supported)
	require.Contains(t, decoded.Reason, "predates")

	// The document must not look like a refresh report with no providers: a client that decoded it
	// as one would report "the running config has no remote providers", which is a different fact.
	var asReport struct {
		Results []json.RawMessage `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(document), &asReport))
	require.Empty(t, asReport.Results)
	require.Contains(t, document, `"supported":false`)
}
