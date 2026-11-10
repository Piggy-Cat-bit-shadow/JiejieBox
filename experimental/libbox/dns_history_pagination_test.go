package libbox

// The pagination and output-budget contract of the DNS history binding.
//
// # The two reviews this file pins
//
// 1. A `limit=1` read over a window with TWO matching records answered one record and
//    `truncated=false`. The caller therefore stopped, and the second record was never seen. The
//    storage layer stopped at the limit and the binding had no way to know that more matched, so it
//    could not say so.
//
// 2. A `maxBytes=4096` read answered 4,454 bytes. The first record is always carried so the cursor
//    advances, but its fields were bounded by a fixed per-field budget rather than by the response
//    budget the caller asked for.
//
// The fixes are separate and both are asserted here: the history reports `HasMore` (a page that
// stopped at the limit is distinguishable from one that reached the end), and the binding crops the
// first record until the ENCODED response fits the requested budget.
//
// Events are recorded directly so the record bodies are deterministic; the production query path is
// covered by TestTheRealQueryPathReachesTheHistory.

import (
	stdjson "encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/route"

	"github.com/stretchr/testify/require"
)

// shortRecordEvent is a small, complete event: no long text, so any response growth is envelope.
func shortRecordEvent(name string) dns.QueryEvent {
	return dns.QueryEvent{
		Time:         time.Now(),
		TransportTag: "local",
		Name:         name + ".test",
		QType:        1,
		Source:       dns.QuerySource("rule"),
		RCode:        3,
		Err:          "rejected",
		Final:        true,
		Rule:         "domain_keyword:[" + name + "]",
	}
}

func historyOf(t *testing.T, server *CommandServer) *dns.History {
	t.Helper()
	instance := server.Instance()
	require.NotNil(t, instance)
	router, isRouter := instance.Box().Router().(*route.Router)
	require.True(t, isRouter)
	history := router.DNSHistory()
	require.NotNil(t, history, "the history must be enabled before recording")
	return history
}

// TestAPageTellsTheCallerThatMoreMatchesExist is review 1.
func TestAPageTellsTheCallerThatMoreMatchesExist(t *testing.T) {
	server := startBoxTestCommandServer(t, historyDNSConfig)
	_, err := server.DNSHistoryEnable(`{"maxEntries":16,"maxBytes":65536,"ttlMs":60000}`)
	require.NoError(t, err)
	history := historyOf(t, server)
	history.Record(shortRecordEvent("first"))
	history.Record(shortRecordEvent("second"))

	first, firstBytes := queryBinding(t, server, `{"limit":1}`)
	require.Len(t, first.Records, 1, "the limit bounds the page")
	require.True(t, first.Truncated,
		"a page that stopped at the limit must say there is more; without it the caller stops and "+
			"never sees the second record")
	require.NotZero(t, first.NextSequence)
	require.LessOrEqual(t, firstBytes, 4096)

	second, _ := queryBinding(t, server, fmt.Sprintf(`{"since":%d,"limit":1}`, first.NextSequence))
	require.Len(t, second.Records, 1, "the cursor is exclusive and resumes exactly after the page")
	require.Greater(t, second.Records[0].Sequence, first.Records[0].Sequence)
	require.False(t, second.Truncated,
		"the last matching record must not claim there is more; that would be a cursor that never ends")

	empty, _ := queryBinding(t, server, fmt.Sprintf(`{"since":%d,"limit":1}`, second.Records[0].Sequence))
	require.Empty(t, empty.Records)
	require.False(t, empty.Truncated)
}

// TestTheOutputBudgetIsTheEncodedResponse is review 2.
func TestTheOutputBudgetIsTheEncodedResponse(t *testing.T) {
	server := startBoxTestCommandServer(t, historyDNSConfig)
	_, err := server.DNSHistoryEnable(`{"maxEntries":32,"maxBytes":1048576,"ttlMs":60000}`)
	require.NoError(t, err)
	history := historyOf(t, server)
	event := shortRecordEvent("big")
	event.Rule = strings.Repeat("x", 40_000)
	history.Record(event)

	for _, budget := range []int{1024, 2048, 4096, 8192} {
		result, encodedBytes := queryBinding(t, server, fmt.Sprintf(`{"maxBytes":%d}`, budget))
		require.LessOrEqual(t, encodedBytes, budget,
			"the response must fit the budget the caller asked for, not the server ceiling")
		require.Len(t, result.Records, 1, "the first record is always carried so the cursor advances")
		require.True(t, result.Truncated, "a cropped record must say so")
		require.NotZero(t, result.NextSequence)
		require.Equal(t, "big.test", result.Records[0].Name, "identity survives the crop")
		require.Equal(t, uint64(1), result.Records[0].Sequence)
		require.Less(t, len(result.Records[0].Rule), 40_000, "the rule text was cropped")
	}

	// The exact boundary: 4,096 passes, 4,095 is also honoured (the crop adapts), and the floor
	// below 1 KiB is clamped up rather than producing an unreadable record.
	for _, budget := range []int{4095, 4096, 4097} {
		_, encodedBytes := queryBinding(t, server, fmt.Sprintf(`{"maxBytes":%d}`, budget))
		require.LessOrEqual(t, encodedBytes, budget, "boundary budget %d", budget)
	}
	_, floorBytes := queryBinding(t, server, `{"maxBytes":1}`)
	require.LessOrEqual(t, floorBytes, minDNSHistoryBindingBytes,
		"a request below the floor is served at the floor, not below the envelope")
	_, ceilingBytes := queryBinding(t, server, `{"maxBytes":100000000}`)
	require.LessOrEqual(t, ceilingBytes, maxDNSHistoryBindingBytes,
		"a request above the ceiling is clamped, not honoured")
}

// TestPaginationVisitsEveryMatchingRecordExactlyOnce walks a window with a mix of transports and a
// tiny per-page budget, following nextSequence to the end.
func TestPaginationVisitsEveryMatchingRecordExactlyOnce(t *testing.T) {
	server := startBoxTestCommandServer(t, historyDNSConfig)
	_, err := server.DNSHistoryEnable(`{"maxEntries":64,"maxBytes":1048576,"ttlMs":60000}`)
	require.NoError(t, err)
	history := historyOf(t, server)
	const total = 12
	for index := 0; index < total; index++ {
		event := shortRecordEvent(fmt.Sprintf("node-%02d", index))
		if index%3 == 0 {
			event.TransportTag = "other"
		}
		event.Rule = strings.Repeat("r", 600)
		history.Record(event)
	}

	// Unfiltered: every record exactly once. The invariant that matters is that a page which does
	// NOT say "truncated" is followed by an empty page - if the caller stopped there, nothing was
	// lost; and a page that does say it must be followed by more.
	cursor := uint64(0)
	seen := make(map[uint64]bool)
	sawFinalPage := false
	for page := 0; page < total+2; page++ {
		result, encodedBytes := queryBinding(t, server, fmt.Sprintf(`{"since":%d,"maxBytes":1024}`, cursor))
		require.LessOrEqual(t, encodedBytes, 1024)
		if len(result.Records) == 0 {
			require.False(t, result.Truncated, "an empty page must not claim there is more")
			break
		}
		require.False(t, sawFinalPage, "a page after the final one must be empty")
		for _, record := range result.Records {
			require.False(t, seen[record.Sequence], "a record must not be returned twice")
			seen[record.Sequence] = true
			require.Greater(t, record.Sequence, cursor, "the cursor advances")
			cursor = record.Sequence
		}
		if !result.Truncated {
			sawFinalPage = true
		}
	}
	require.Len(t, seen, total)

	// Filtered: HasMore counts only records matching the filter, so a page that ends the filtered
	// window is NOT truncated even though other transports are retained behind it.
	filteredCursor := uint64(0)
	filtered := 0
	for page := 0; page < total+2; page++ {
		result, _ := queryBinding(t, server, fmt.Sprintf(`{"since":%d,"transport":"other","maxBytes":1024}`, filteredCursor))
		if len(result.Records) == 0 {
			require.False(t, result.Truncated, "the filtered window ended; claiming more would loop")
			break
		}
		for _, record := range result.Records {
			filtered++
			filteredCursor = record.Sequence
		}
	}
	require.Equal(t, total/3, filtered, "only the three 'other' records match the filter")
}

// TestTheWindowLifecycleKeepsTheCursorHonest covers Clear and Disable/Enable interleavings: a cursor
// from before a clear must not resurrect records, and a disabled history is an error, not an empty
// page.
func TestTheWindowLifecycleKeepsTheCursorHonest(t *testing.T) {
	server := startBoxTestCommandServer(t, historyDNSConfig)
	_, err := server.DNSHistoryEnable(`{"maxEntries":16,"maxBytes":65536,"ttlMs":60000}`)
	require.NoError(t, err)
	history := historyOf(t, server)
	history.Record(shortRecordEvent("before"))
	result, _ := queryBinding(t, server, `{"limit":1}`)
	require.Len(t, result.Records, 1)
	cursor := result.Records[0].Sequence

	_, err = server.DNSHistoryClear()
	require.NoError(t, err)
	history = historyOf(t, server)
	history.Record(shortRecordEvent("after"))
	result, _ = queryBinding(t, server, fmt.Sprintf(`{"since":%d}`, cursor))
	require.Len(t, result.Records, 1, "the record written after the clear is visible")
	require.Greater(t, result.Records[0].Sequence, cursor,
		"the sequence keeps counting across a clear, so an old cursor sees only newer records")

	require.NoError(t, server.DNSHistoryDisable())
	_, err = server.DNSHistoryQuery(`{"limit":1}`)
	require.Error(t, err, "a disabled history is not an empty page")

	_, err = server.DNSHistoryEnable(`{"maxEntries":16,"maxBytes":65536,"ttlMs":60000}`)
	require.NoError(t, err)
	result, _ = queryBinding(t, server, `{}`)
	require.Empty(t, result.Records, "re-enabling starts a fresh window")
	require.False(t, result.Truncated)

	// A cursor carried across the disable is meaningless in the NEW window: the sequence space
	// restarted, so nothing is after it. Pinned rather than accidental: the page is empty AND the
	// window is empty, so a client that tracks the stats can tell "the window restarted" from
	// "I reached the end of a window that still has records".
	stale, _ := queryBinding(t, server, fmt.Sprintf(`{"since":%d,"limit":8}`, cursor))
	require.Empty(t, stale.Records, "a cursor from the previous window sees nothing in the new one")
	require.False(t, stale.Truncated)
	require.Zero(t, stale.Stats.Entries, "the new window is empty, which is how a client detects the restart")
}

// TestARecordMissingFromTheRingIsNotAHoleInThePage: a record rejected by the retention budget is
// counted and skipped, and the page still advances past it.
func TestARecordRejectedByRetentionDoesNotStallPagination(t *testing.T) {
	server := startBoxTestCommandServer(t, historyDNSConfig)
	_, err := server.DNSHistoryEnable(`{"maxEntries":16,"maxBytes":4096,"ttlMs":60000}`)
	require.NoError(t, err)
	history := historyOf(t, server)
	history.Record(shortRecordEvent("kept"))
	huge := shortRecordEvent("dropped")
	huge.Rule = strings.Repeat("z", 40_000)
	history.Record(huge) // rejected by the ring: cannot fit 4 KiB at all
	history.Record(shortRecordEvent("kept-too"))

	result, _ := queryBinding(t, server, `{"maxBytes":4096}`)
	require.GreaterOrEqual(t, result.Stats.Rejected, uint64(1), "the rejected record is counted")
	require.Len(t, result.Records, 2, "both retained records are readable")
	for _, record := range result.Records {
		require.NotEqual(t, "dropped.test", record.Name, "the rejected record is not in the window")
	}
	encoded, err := stdjson.Marshal(result)
	require.NoError(t, err)
	require.LessOrEqual(t, len(encoded), 4096)
}

// TestACroppedFieldIsValidUTF8: the crop is measured on the ENCODED form and must not split a
// multi-byte rune, or the platform would receive a string its JSON decoder rejects.
func TestACroppedFieldIsValidUTF8(t *testing.T) {
	server := startBoxTestCommandServer(t, historyDNSConfig)
	_, err := server.DNSHistoryEnable(`{"maxEntries":8,"maxBytes":1048576,"ttlMs":60000}`)
	require.NoError(t, err)
	history := historyOf(t, server)
	event := shortRecordEvent("utf8")
	// Multi-byte runes AND a control character: the control character escapes to six bytes, which is
	// exactly the case an input-length budget would under-count.
	event.Rule = strings.Repeat("拒绝解释-", 2000) + "\x01"
	history.Record(event)

	for _, budget := range []int{1024, 2048, 4096} {
		boxed, queryErr := server.DNSHistoryQuery(fmt.Sprintf(`{"maxBytes":%d}`, budget))
		require.NoError(t, queryErr)
		require.LessOrEqual(t, len(boxed.Value), budget)
		require.True(t, utf8.ValidString(boxed.Value), "the binding must hand the platform valid UTF-8")
		require.Contains(t, boxed.Value, "拒绝", "the crop keeps the head of the field")
	}
}

// TestRetentionOptionsKeepTheirOwnContract: TTL expiry, the top-k bound and the stats snapshot are
// properties of the ring, not of the page. A caller must not be able to break them through the
// binding.
func TestRetentionOptionsKeepTheirOwnContract(t *testing.T) {
	server := startBoxTestCommandServer(t, historyDNSConfig)
	_, err := server.DNSHistoryEnable(`{"maxEntries":4,"maxBytes":65536,"ttlMs":20}`)
	require.NoError(t, err)
	history := historyOf(t, server)
	for index := 0; index < 6; index++ {
		history.Record(shortRecordEvent(fmt.Sprintf("entry-%d", index)))
	}

	// The entry cap holds: six writes into four slots leave four.
	result, _ := queryBinding(t, server, `{"limit":32}`)
	require.LessOrEqual(t, result.Stats.Entries, 4, "maxEntries is enforced by the ring")
	require.Len(t, result.Records, result.Stats.Entries)

	// The TTL holds: after it elapses the window is empty and the expiry is counted.
	time.Sleep(60 * time.Millisecond)
	result, _ = queryBinding(t, server, `{"limit":32}`)
	require.Empty(t, result.Records)
	require.Zero(t, result.Stats.Entries)
	require.GreaterOrEqual(t, result.Stats.Expired, uint64(1))

	// Top-k over an expired window is empty and still bounded.
	top, err := server.DNSHistoryTopK("transport", 1000)
	require.NoError(t, err)
	require.Contains(t, top.Value, `"entries":[]`)
	_, err = server.DNSHistoryTopK("not-a-field", 3)
	require.Error(t, err)

	// A fresh record after the expiry is visible again, and the read budget still bounds the page.
	history.Record(shortRecordEvent("after-ttl"))
	result, encodedBytes := queryBinding(t, server, `{"maxBytes":1024}`)
	require.Len(t, result.Records, 1)
	require.LessOrEqual(t, encodedBytes, 1024)
}
