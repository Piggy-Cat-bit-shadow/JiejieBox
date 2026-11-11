package dns

// The first record of a history response must obey the SAME effective budget as every other record.
//
// # The reviewed failure
//
// The first record was carried unconditionally "so the cursor advances". A record whose rule text is
// 40,000 characters therefore produced a 4,378-byte response for a 1 KiB request - under the 64 KiB
// server ceiling, so the ceiling check passed, while the caller's effective budget was exceeded by
// four times. The gomobile binding and the daemon RPC share this presentation, so both were wrong.
//
// The contract now: the document is measured after encoding, the first record's variable fields are
// shrunk until it fits, and the response is always one that can make progress (one record, with a
// nextSequence) and never exceeds the effective budget.

import (
	stdjson "encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func longRuleRecord(sequence uint64, ruleLength int) HistoryRecord {
	record := bindingTestRecord(sequence, "history.test.", strings.Repeat("r", ruleLength))
	record.Event.Source = QuerySourceBlocked
	return record
}

func marshaledBytes(t *testing.T, view HistoryQueryView) int {
	t.Helper()
	encoded, err := stdjson.Marshal(view)
	require.NoError(t, err)
	require.True(t, utf8.Valid(encoded))
	return len(encoded)
}

func TestTheFirstRecordFitsAOneKilobyteBudget(t *testing.T) {
	records := []HistoryRecord{longRuleRecord(1, 40_000)}
	view := BuildHistoryQueryView(records, HistoryStats{Entries: 1, Bytes: 40_000}, 0, 1024)

	encodedBytes := marshaledBytes(t, view)
	require.LessOrEqual(t, encodedBytes, 1024,
		"the first record must fit the caller's effective budget, not only the server ceiling")
	require.Len(t, view.Records, 1, "the response must still advance the cursor")
	require.EqualValues(t, 1, view.NextSequence)
	require.True(t, view.Truncated)
	require.Equal(t, "history.test.", view.Records[0].Name, "the short identifying field survives")
	require.LessOrEqual(t, len(view.Records[0].Rule), 1024)
}

func TestTheFirstRecordFitsAFourKilobyteBudget(t *testing.T) {
	records := []HistoryRecord{longRuleRecord(1, 40_000)}
	view := BuildHistoryQueryView(records, HistoryStats{Entries: 1, Bytes: 40_000}, 0, 4096)

	require.LessOrEqual(t, marshaledBytes(t, view), 4096)
	require.True(t, view.Truncated)
	require.LessOrEqual(t, len(view.Records[0].Rule), MaxHistoryFieldBytes+8,
		"a 4 KiB budget can carry the normal per-field cap")
}

// TestTheFirstRecordFitsWhenEscapingInflatesIt: control characters become six-byte \uXXXX escapes,
// so the budget must be measured on the encoded form, not on the input length.
func TestTheFirstRecordFitsWhenEscapingInflatesIt(t *testing.T) {
	record := bindingTestRecord(1, "escape.test.", strings.Repeat("\x01", 2_000))
	view := BuildHistoryQueryView([]HistoryRecord{record}, HistoryStats{Entries: 1}, 0, 1024)

	require.LessOrEqual(t, marshaledBytes(t, view), 1024)
	require.True(t, view.Truncated)
}

// TestTheFirstRecordPaginationAdvancesWithoutDuplicates pages through long-rule records with a 1 KiB
// budget: every page fits, the cursor advances, and no record is returned twice or skipped.
func TestTheFirstRecordPaginationAdvancesWithoutDuplicates(t *testing.T) {
	records := make([]HistoryRecord, 0, 5)
	for index := 0; index < 5; index++ {
		records = append(records, longRuleRecord(uint64(index+1), 8_000))
	}
	stats := HistoryStats{Entries: 5, Bytes: 5 * 8_000}

	seen := make(map[uint64]bool)
	cursor := uint64(0)
	pages := 0
	for pages < 10 {
		var pageRecords []HistoryRecord
		for _, record := range records {
			if record.Sequence > cursor {
				pageRecords = append(pageRecords, record)
			}
		}
		view := BuildHistoryQueryView(pageRecords, stats, 0, 1024)
		pages++
		require.LessOrEqual(t, marshaledBytes(t, view), 1024)
		require.NotEmpty(t, view.Records, "every page must advance the cursor")
		for _, record := range view.Records {
			require.False(t, seen[record.Sequence], "record %d was returned twice", record.Sequence)
			seen[record.Sequence] = true
			require.Greater(t, record.Sequence, cursor)
			cursor = record.Sequence
		}
		if cursor >= 5 {
			break
		}
	}
	require.Len(t, seen, 5, "pagination must visit every record")
	require.LessOrEqual(t, pages, 5)
}

// TestTheDefaultAndCeilingBudgetsStayBounded keeps the clamp contract: below the floor the floor
// applies, above the ceiling the ceiling applies, and neither path exceeds what was applied.
func TestTheDefaultAndCeilingBudgetsStayBounded(t *testing.T) {
	records := make([]HistoryRecord, 0, 20)
	for index := 0; index < 20; index++ {
		records = append(records, longRuleRecord(uint64(index+1), 20_000))
	}
	defaultView := BuildHistoryQueryView(records, HistoryStats{Entries: 20}, 0, 0)
	require.LessOrEqual(t, marshaledBytes(t, defaultView), MaxHistoryBindingBytes)

	hugeView := BuildHistoryQueryView(records, HistoryStats{Entries: 20}, 0, 10*MaxHistoryBindingBytes)
	require.LessOrEqual(t, marshaledBytes(t, hugeView), MaxHistoryBindingBytes)

	tinyView := BuildHistoryQueryView(records, HistoryStats{Entries: 20}, 0, 1)
	require.LessOrEqual(t, marshaledBytes(t, tinyView), MinHistoryBindingBytes,
		"a request below the floor is answered within the floor")
	require.NotEmpty(t, tinyView.Records)
}

// TestTheFirstRecordStaysUsefulWhenShrunk records what survives shrinking: the identifying fields and
// the cursor, with the long text marked as cut. A caller must be able to tell that the record is
// incomplete rather than reading a silently mangled rule.
func TestTheFirstRecordStaysUsefulWhenShrunk(t *testing.T) {
	view := BuildHistoryQueryView([]HistoryRecord{longRuleRecord(42, 40_000)}, HistoryStats{Entries: 1}, 0, 1024)
	require.EqualValues(t, 42, view.NextSequence)
	require.EqualValues(t, 42, view.Records[0].Sequence)
	require.Equal(t, "blocked", view.Records[0].Source)
	require.NotZero(t, view.Records[0].TimeMs)
	require.True(t, strings.HasSuffix(view.Records[0].Rule, "…"),
		"a cut field is marked, never silently truncated")
}
