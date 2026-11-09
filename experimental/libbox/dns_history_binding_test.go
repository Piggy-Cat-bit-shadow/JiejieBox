package libbox

// The gomobile registration of the DNS history management surface: a real CommandServer, a real
// Box, a real query, and the JSON the binding hands the platform.

import (
	"context"
	stdjson "encoding/json"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/route"

	"github.com/stretchr/testify/require"
)

const dnsHistoryBindingConfig = `{"log":{"level":"error"},
	"dns":{"servers":[{"type":"hosts","tag":"local","predefined":{"history.test":["127.0.0.9"]}}],"final":"local"},
	"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`

func dnsHistoryLookup(t *testing.T, server *CommandServer, name string) {
	t.Helper()
	instance := server.Instance()
	require.NotNil(t, instance)
	routeRouter, isRouteRouter := instance.Box().Router().(*route.Router)
	require.True(t, isRouteRouter)
	addresses, err := routeRouter.LookupDomain(context.Background(), name, adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, addresses)
}

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

// TestTheCommandServerExposesTheDNSHistory drives enable → query → top-k → stats → clear → disable
// through the binding entry points.
func TestTheCommandServerExposesTheDNSHistory(t *testing.T) {
	server := startDiagnoseTestCommandServer(t, dnsHistoryBindingConfig)

	// Disabled is reported as "not enabled", not as an empty window.
	_, err := server.DNSHistoryQuery("")
	require.Error(t, err)

	enabled, err := server.DNSHistoryEnable(`{"maxEntries":16,"maxBytes":65536,"ttlMs":60000}`)
	require.NoError(t, err)
	require.Contains(t, enabled.Value, `"enabled":true`)

	dnsHistoryLookup(t, server, "history.test")

	var result dnsHistoryQueryResult
	require.Eventually(t, func() bool {
		encoded, queryErr := server.DNSHistoryQuery("")
		if queryErr != nil {
			return false
		}
		var candidate dnsHistoryQueryResult
		if jsonErr := stdjson.Unmarshal([]byte(encoded.Value), &candidate); jsonErr != nil {
			return false
		}
		result = candidate
		return len(candidate.Records) >= 1
	}, 5*time.Second, time.Millisecond, "the real query must appear in the history")

	found := false
	for _, record := range result.Records {
		if record.Name == "history.test." || record.Name == "history.test" {
			found = true
			require.Equal(t, "local", record.Transport)
			require.NotEmpty(t, record.Source)
			require.NotZero(t, record.Sequence)
		}
	}
	require.True(t, found, "the record names the queried host: %+v", result.Records)

	stats, err := server.DNSHistoryStats()
	require.NoError(t, err)
	require.Contains(t, stats.Value, `"entries":`)

	top, err := server.DNSHistoryTopK("transport", 3)
	require.NoError(t, err)
	require.Contains(t, top.Value, `"local"`)
	_, err = server.DNSHistoryTopK("bogus", 3)
	require.Error(t, err)

	cleared, err := server.DNSHistoryClear()
	require.NoError(t, err)
	require.Contains(t, cleared.Value, `"cleared":`)

	require.Eventually(t, func() bool {
		encoded, queryErr := server.DNSHistoryQuery("")
		if queryErr != nil {
			return false
		}
		var candidate dnsHistoryQueryResult
		if jsonErr := stdjson.Unmarshal([]byte(encoded.Value), &candidate); jsonErr != nil {
			return false
		}
		return len(candidate.Records) == 0 && candidate.Stats.Entries == 0
	}, 5*time.Second, time.Millisecond, "clearing empties the window")

	require.NoError(t, server.DNSHistoryDisable())
	_, err = server.DNSHistoryQuery("")
	require.Error(t, err, "a disabled history is not an empty window")

	// Re-enabling starts a fresh window rather than resurrecting the old records.
	_, err = server.DNSHistoryEnable("")
	require.NoError(t, err)
	encoded, err := server.DNSHistoryQuery("")
	require.NoError(t, err)
	var restarted dnsHistoryQueryResult
	require.NoError(t, stdjson.Unmarshal([]byte(encoded.Value), &restarted))
	require.Empty(t, restarted.Records)
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
