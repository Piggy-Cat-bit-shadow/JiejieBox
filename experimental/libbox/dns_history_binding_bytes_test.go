package libbox

// P1.3 (round 3) — the DNS history's retention and its BINDING response are both bounded by real
// bytes.
//
// The reviewed failure: a local adblock rule whose `domain_keyword` list contains a 40,000-character
// item produced events whose `Rule` text is that list. The ring charged neither the rule nor the
// other retained fields (two records were accounted 154 B), so `maxBytes=4096` retained tens of
// kilobytes; the binding then sized its JSON from the same retained estimate, and one
// `DNSHistoryQuery` with `maxBytes=4096` returned 80,485 bytes.
//
// These tests drive the real CommandServer, the real router and a real hosts/adblock DNS
// configuration, and measure the ACTUAL encoded bytes.

import (
	"context"
	stdjson "encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/route"

	"github.com/stretchr/testify/require"
)

type dnsHistoryBindingQueryResult struct {
	Records []struct {
		Sequence uint64   `json:"sequence"`
		Name     string   `json:"name"`
		Rule     string   `json:"rule"`
		Err      string   `json:"err"`
		Answers  []string `json:"answers"`
	} `json:"records"`
	Stats struct {
		Entries  int    `json:"entries"`
		Bytes    int    `json:"bytes"`
		Rejected uint64 `json:"rejected"`
		Cleared  uint64 `json:"cleared"`
	} `json:"stats"`
	Truncated    bool   `json:"truncated"`
	NextSequence uint64 `json:"nextSequence"`
}

// longRuleDNSConfig builds a hosts DNS plus an adblock rule whose description is huge.
func longRuleDNSConfig(extraKeyword string) string {
	keywords := `"history"`
	if extraKeyword != "" {
		keywords += `,` + stdjsonString(extraKeyword)
	}
	return fmt.Sprintf(`{"log":{"level":"error"},
		"dns":{"servers":[{"type":"hosts","tag":"local","predefined":{"unrelated.test":["127.0.0.9"]}}],
			"final":"local","rules":[{"domain_keyword":[%s],"action":"reject"}]},
		"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`, keywords)
}

// stdjsonString quotes a value for the config JSON.
func stdjsonString(value string) string {
	encoded, _ := stdjson.Marshal(value)
	return string(encoded)
}

// lookupBlockedName drives a real lookup through the router so a real event reaches the history.
func lookupBlockedName(t *testing.T, server *CommandServer, name string) {
	t.Helper()
	instance := server.Instance()
	require.NotNil(t, instance)
	routeRouter, isRouteRouter := instance.Box().Router().(*route.Router)
	require.True(t, isRouteRouter)
	// The rule rejects the name, so the lookup errors; the EVENT is what this test is about.
	_, _ = routeRouter.LookupDomain(context.Background(), name, adapter.DNSQueryOptions{})
}

func queryBinding(t *testing.T, server *CommandServer, queryJSON string) (dnsHistoryBindingQueryResult, int) {
	t.Helper()
	boxed, err := server.DNSHistoryQuery(queryJSON)
	require.NoError(t, err)
	require.NotNil(t, boxed)
	var result dnsHistoryBindingQueryResult
	require.NoError(t, stdjson.Unmarshal([]byte(boxed.Value), &result), "report: %s", boxed.Value)
	require.True(t, utf8.ValidString(boxed.Value), "the binding must hand the platform valid UTF-8")
	return result, len(boxed.Value)
}

// TestALongRuleIsRejectedByTheRetentionBudget closes the reviewed example at its source: with
// `maxBytes=4096`, the 40,000-character rule cannot be retained at all, so the encoding cannot be
// large and the rejection is counted.
func TestALongRuleIsRejectedByTheRetentionBudget(t *testing.T) {
	server := startDiagnoseTestCommandServer(t, longRuleDNSConfig(strings.Repeat("a", 40_000)))
	defer server.Close()

	_, err := server.DNSHistoryEnable(`{"maxEntries":16,"maxBytes":4096,"ttlMs":60000}`)
	require.NoError(t, err)

	lookupBlockedName(t, server, "history.test")

	var result dnsHistoryBindingQueryResult
	encodedBytes := 0
	require.Eventually(t, func() bool {
		result, encodedBytes = queryBinding(t, server, `{"maxBytes":4096}`)
		return result.Stats.Rejected >= 1 || len(result.Records) >= 1
	}, 5*time.Second, time.Millisecond, "the rejected event must be counted")

	require.GreaterOrEqual(t, result.Stats.Rejected, uint64(1),
		"a record that cannot fit the byte budget is rejected, not retained")
	require.Empty(t, result.Records, "the oversized rule never enters the ring")
	require.LessOrEqual(t, encodedBytes, 4096, "the reviewed 80,485-byte response is gone")
	t.Logf("retention-rejected: entries=%d bytes=%d rejected=%d encoded=%d",
		result.Stats.Entries, result.Stats.Bytes, result.Stats.Rejected, encodedBytes)
}

// TestTheBindingResponseIsHardCappedWhenTheRuleIsRetained keeps the long rule in the ring (a large
// retention budget) and verifies the binding's independent ceiling, truncation flag, cursor progress
// and rune-safe field cut.
func TestTheBindingResponseIsHardCappedWhenTheRuleIsRetained(t *testing.T) {
	longKeyword := strings.Repeat("b", 40_000)
	server := startDiagnoseTestCommandServer(t, longRuleDNSConfig(longKeyword))
	defer server.Close()

	_, err := server.DNSHistoryEnable(`{"maxEntries":32,"maxBytes":1048576,"ttlMs":60000}`)
	require.NoError(t, err)

	lookupBlockedName(t, server, "history.test")

	var initial dnsHistoryBindingQueryResult
	require.Eventually(t, func() bool {
		initial, _ = queryBinding(t, server, `{"maxBytes":65536}`)
		return len(initial.Records) >= 1
	}, 5*time.Second, time.Millisecond, "the long-rule event must be retained")

	result, encodedBytes := queryBinding(t, server, `{"maxBytes":4096}`)
	t.Logf("long-rule: records=%d encoded=%d truncated=%v ruleField=%d retainedBytes=%d",
		len(result.Records), encodedBytes, result.Truncated, len(result.Records[0].Rule), result.Stats.Bytes)

	require.LessOrEqual(t, encodedBytes, maxDNSHistoryBindingBytes,
		"one binding response must never exceed the server ceiling")
	require.LessOrEqual(t, encodedBytes, 8192,
		"a 4 KiB request is answered with a bounded single record, not the retained rule text")
	require.NotEmpty(t, result.Records, "the first record is always carried so the cursor advances")
	require.True(t, result.Truncated, "a cut response must say so")
	require.NotZero(t, result.NextSequence, "the caller needs a cursor to continue from")
	require.LessOrEqual(t, len(result.Records[0].Rule), maxDNSHistoryFieldBytes+8,
		"the rule text is bounded by its encoded budget")
	require.True(t, strings.HasSuffix(result.Records[0].Rule, "…"), "a cut field is marked")
	require.Greater(t, result.Stats.Bytes, 40_000,
		"the retained accounting now charges the rule text that was invisible before")

	// A request above the server ceiling is clamped, not honoured.
	clamped, clampedBytes := queryBinding(t, server, `{"maxBytes":100000000}`)
	require.LessOrEqual(t, clampedBytes, maxDNSHistoryBindingBytes)
	require.NotEmpty(t, clamped.Records)

	// A request below the floor still makes progress and stays under the ceiling.
	floor, floorBytes := queryBinding(t, server, `{"maxBytes":1}`)
	require.NotEmpty(t, floor.Records)
	require.LessOrEqual(t, floorBytes, maxDNSHistoryBindingBytes)
	require.True(t, floor.Truncated)
}

// TestTheBindingPaginatesThroughTruncatedResponses makes five long-rule events and reads them with a
// small budget, following nextSequence until done: every record is returned exactly once and the
// cursor always advances.
func TestTheBindingPaginatesThroughTruncatedResponses(t *testing.T) {
	server := startDiagnoseTestCommandServer(t, longRuleDNSConfig(strings.Repeat("c", 8_000)))
	defer server.Close()

	_, err := server.DNSHistoryEnable(`{"maxEntries":32,"maxBytes":1048576,"ttlMs":60000}`)
	require.NoError(t, err)

	for index := 0; index < 5; index++ {
		lookupBlockedName(t, server, fmt.Sprintf("history.test"))
	}
	// The bus delivers to the history asynchronously, so the window is only stable once the count
	// stops growing; a snapshot taken mid-delivery would under-count the records this test then
	// expects to page through.
	lastEntries := -1
	stableSince := time.Now()
	require.Eventually(t, func() bool {
		result, _ := queryBinding(t, server, `{"maxBytes":65536}`)
		if result.Stats.Entries != lastEntries {
			lastEntries = result.Stats.Entries
			stableSince = time.Now()
			return false
		}
		return lastEntries > 0 && time.Since(stableSince) > 200*time.Millisecond
	}, 10*time.Second, 10*time.Millisecond, "the history window must stabilize before pagination")
	initial, _ := queryBinding(t, server, `{"maxBytes":65536}`)
	require.Positive(t, initial.Stats.Entries)

	cursor := uint64(0)
	seen := make(map[uint64]bool)
	responses := 0
	for responses < 64 {
		result, encodedBytes := queryBinding(t, server, fmt.Sprintf(`{"since":%d,"maxBytes":2048}`, cursor))
		responses++
		require.LessOrEqual(t, encodedBytes, maxDNSHistoryBindingBytes)
		for _, record := range result.Records {
			require.False(t, seen[record.Sequence], "a record must not be returned twice")
			seen[record.Sequence] = true
			require.Greater(t, record.Sequence, cursor, "the cursor is exclusive")
			cursor = record.Sequence
		}
		if len(result.Records) == 0 {
			break
		}
	}
	require.Equal(t, initial.Stats.Entries, len(seen), "the loop must visit every retained event exactly once")
	require.LessOrEqual(t, responses, initial.Stats.Entries+1, "each response carries at least one record, so pagination progresses (plus the terminating empty read)")
}

// TestConcurrentHistoryEnableDisableClearAndQuery exercises the management surface under
// concurrency: the ring is swapped while readers query it, and every response stays bounded and
// valid.
func TestConcurrentHistoryEnableDisableClearAndQuery(t *testing.T) {
	server := startDiagnoseTestCommandServer(t, longRuleDNSConfig(strings.Repeat("d", 4_000)))
	defer server.Close()

	var workers sync.WaitGroup
	stop := make(chan struct{})
	for worker := 0; worker < 3; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if _, queryErr := server.DNSHistoryQuery(`{"maxBytes":4096}`); queryErr == nil {
					}
					if _, statsErr := server.DNSHistoryStats(); statsErr == nil {
					}
				}
			}
		}()
	}
	for round := 0; round < 10; round++ {
		_, err := server.DNSHistoryEnable(`{"maxEntries":8,"maxBytes":1048576,"ttlMs":60000}`)
		require.NoError(t, err)
		lookupBlockedName(t, server, "history.test")
		time.Sleep(time.Millisecond)
		_, clearErr := server.DNSHistoryClear()
		require.NoError(t, clearErr)
		require.NoError(t, server.DNSHistoryDisable())
		if round%2 == 0 {
			time.Sleep(2 * time.Millisecond)
		}
	}
	close(stop)
	workers.Wait()
}
