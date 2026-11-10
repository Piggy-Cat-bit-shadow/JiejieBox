package libbox

// The gomobile registration of the DNS history management surface: a real CommandServer, a real
// Box, and the JSON the binding hands the platform.
//
// # What this test does NOT do, and why
//
// It does not issue a DNS query through the Box. The only in-tree way to drive one from a test is
// `Router.LookupDomain`, which arrives with the unmerged diagnostic change; taking it here would
// drag that feature in just to get a test vehicle. The behaviour WITH records is covered where the
// history lives - `dns/history_management_test.go` drives real queries, pagination, budgets and
// TTL through the history's own entry points. What is only testable here is the binding: the JSON
// envelopes, the lifecycle, and the fact that "disabled" is an error rather than an empty window.

import (
	stdjson "encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

const dnsHistoryBindingConfig = `{"log":{"level":"error"},
	"dns":{"servers":[{"type":"hosts","tag":"local","predefined":{"history.test":["127.0.0.9"]}}],"final":"local"},
	"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`

type dnsHistoryQueryResult struct {
	Records []struct {
		Sequence  uint64   `json:"sequence"`
		Name      string   `json:"name"`
		Transport string   `json:"transport"`
		Source    string   `json:"source"`
		Answers   []string `json:"answers"`
	} `json:"records"`
	Stats struct {
		Entries int    `json:"entries"`
		Bytes   int    `json:"bytes"`
		Cleared uint64 `json:"cleared"`
		Dropped uint64 `json:"dropped"`
	} `json:"stats"`
}

func decodeDNSHistoryQuery(t *testing.T, encoded string) dnsHistoryQueryResult {
	t.Helper()
	var result dnsHistoryQueryResult
	require.NoError(t, stdjson.Unmarshal([]byte(encoded), &result), "query envelope: %s", encoded)
	return result
}

// TestTheCommandServerExposesTheDNSHistory drives enable → query → top-k → stats → clear → disable
// through the binding entry points.
func TestTheCommandServerExposesTheDNSHistory(t *testing.T) {
	server := startBoxTestCommandServer(t, dnsHistoryBindingConfig)

	// Disabled is reported as "not enabled", not as an empty window.
	_, err := server.DNSHistoryQuery("")
	require.Error(t, err)

	enabled, err := server.DNSHistoryEnable(`{"maxEntries":16,"maxBytes":65536,"ttlMs":60000}`)
	require.NoError(t, err)
	require.Contains(t, enabled.Value, `"enabled":true`)

	// An enabled history with no traffic answers with a well-formed empty envelope; the error
	// above must not leak into the enabled state.
	encoded, err := server.DNSHistoryQuery("")
	require.NoError(t, err)
	result := decodeDNSHistoryQuery(t, encoded.Value)
	require.Empty(t, result.Records)
	require.Equal(t, 0, result.Stats.Entries)

	// A paginated read is the same window with a budget.
	encoded, err = server.DNSHistoryQuery(`{"limit":4,"maxBytes":1024}`)
	require.NoError(t, err)
	require.Empty(t, decodeDNSHistoryQuery(t, encoded.Value).Records)

	stats, err := server.DNSHistoryStats()
	require.NoError(t, err)
	require.Contains(t, stats.Value, `"entries":0`)
	require.Contains(t, stats.Value, `"bytes":`)

	top, err := server.DNSHistoryTopK("transport", 3)
	require.NoError(t, err)
	require.True(t, stdjson.Valid([]byte(top.Value)), "top-k envelope is JSON: %s", top.Value)
	require.Contains(t, top.Value, `"field":"transport"`)
	_, err = server.DNSHistoryTopK("bogus", 3)
	require.Error(t, err, "an unknown aggregation field is refused")

	cleared, err := server.DNSHistoryClear()
	require.NoError(t, err)
	require.Contains(t, cleared.Value, `"cleared":`)

	require.NoError(t, server.DNSHistoryDisable())
	_, err = server.DNSHistoryQuery("")
	require.Error(t, err, "a disabled history is not an empty window")

	// Re-enabling starts a fresh window rather than resurrecting the old records.
	_, err = server.DNSHistoryEnable("")
	require.NoError(t, err)
	encoded, err = server.DNSHistoryQuery("")
	require.NoError(t, err)
	require.Empty(t, decodeDNSHistoryQuery(t, encoded.Value).Records)
}

// TestADisabledCommandServerIsAnError keeps the binding's nil/stopped path honest.
func TestADisabledCommandServerIsAnError(t *testing.T) {
	var nilServer *CommandServer
	_, err := nilServer.DNSHistoryQuery("")
	require.Error(t, err)
	_, err = (&CommandServer{}).DNSHistoryStats()
	require.Error(t, err)
	require.Error(t, nilServer.DNSHistoryDisable())
}
