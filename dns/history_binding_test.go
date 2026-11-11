package dns

// The shared presentation of the query history.
//
// Two producers hand the same document to different transports (gomobile's JSON string, the
// daemon's gRPC response), so the bounds live here and are pinned here: clamping, the encoded-size
// measurement, the rune-safe field cut, and the truncated/nextSequence pagination contract.

import (
	stdjson "encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func bindingTestRecord(sequence uint64, name string, rule string) HistoryRecord {
	return HistoryRecord{
		Sequence: sequence,
		Event: QueryEvent{
			Time:         time.UnixMilli(1_700_000_000_000 + int64(sequence)),
			Name:         name,
			QType:        1,
			Source:       QuerySourceUpstream,
			TransportTag: "remote",
			RCode:        0,
			DurationMs:   5,
			Rule:         rule,
		},
	}
}

func TestTruncateJSONFieldMeasuresTheEncodedForm(t *testing.T) {
	// A control character becomes a six-byte \uXXXX escape, so an input-length budget would let a
	// short string produce a response six times its size.
	value := strings.Repeat("\x01", 200)
	bounded, cut := TruncateJSONField(value, 64)
	require.True(t, cut)
	encoded, err := stdjson.Marshal(bounded)
	require.NoError(t, err)
	require.LessOrEqual(t, len(encoded), 64)
	require.True(t, utf8.ValidString(bounded))
	require.True(t, strings.HasSuffix(bounded, "…"))
}

func TestTruncateJSONFieldIsRuneSafe(t *testing.T) {
	value := strings.Repeat("测", 100)
	bounded, cut := TruncateJSONField(value, 30)
	require.True(t, cut)
	require.True(t, utf8.ValidString(bounded))
	encoded, err := stdjson.Marshal(bounded)
	require.NoError(t, err)
	require.LessOrEqual(t, len(encoded), 30)
}

func TestHistoryBindingBudgetClamps(t *testing.T) {
	require.Equal(t, MaxHistoryBindingBytes, HistoryBindingBudget(0))
	require.Equal(t, MinHistoryBindingBytes, HistoryBindingBudget(1))
	require.Equal(t, 4096, HistoryBindingBudget(4096))
	require.Equal(t, MaxHistoryBindingBytes, HistoryBindingBudget(MaxHistoryBindingBytes*10))
}

func TestBuildHistoryQueryViewAlwaysCarriesTheFirstRecord(t *testing.T) {
	records := []HistoryRecord{bindingTestRecord(7, "history.test.", "domain_keyword: ["+strings.Repeat("a", 40_000)+"]")}
	view := BuildHistoryQueryView(records, HistoryStats{Entries: 1, Bytes: 80_000}, 0, 4096)

	require.Len(t, view.Records, 1, "the first record must be carried so the cursor advances")
	require.EqualValues(t, 7, view.NextSequence)
	require.True(t, view.Truncated)
	require.LessOrEqual(t, len(view.Records[0].Rule), MaxHistoryFieldBytes+8)
	require.True(t, strings.HasSuffix(view.Records[0].Rule, "…"))

	encoded, err := stdjson.Marshal(view)
	require.NoError(t, err)
	require.LessOrEqual(t, len(encoded), MaxHistoryBindingBytes)
}

func TestBuildHistoryQueryViewStopsBeforeTheBudget(t *testing.T) {
	records := make([]HistoryRecord, 0, 8)
	for index := 0; index < 8; index++ {
		records = append(records, bindingTestRecord(uint64(index+1), "q.example.", strings.Repeat("r", 900)))
	}
	view := BuildHistoryQueryView(records, HistoryStats{Entries: 8}, 0, 2048)

	require.NotEmpty(t, view.Records)
	require.Less(t, len(view.Records), len(records), "a small budget must stop the response")
	require.True(t, view.Truncated)
	require.Equal(t, view.Records[len(view.Records)-1].Sequence, view.NextSequence)

	encoded, err := stdjson.Marshal(view)
	require.NoError(t, err)
	require.LessOrEqual(t, len(encoded), 2048)
}

func TestBuildHistoryQueryViewCarriesStatsAndDropped(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	view := BuildHistoryQueryView(nil, HistoryStats{
		Entries:  3,
		Bytes:    500,
		Rejected: 4,
		Expired:  2,
		Queries:  9,
		Cleared:  1,
		Oldest:   now,
		Newest:   now.Add(time.Second),
	}, 5, 0)

	require.Equal(t, 3, view.Stats.Entries)
	require.Equal(t, 500, view.Stats.Bytes)
	require.EqualValues(t, 4, view.Stats.Rejected)
	require.EqualValues(t, 5, view.Stats.Dropped)
	require.EqualValues(t, now.UnixMilli(), view.Stats.OldestMs)
	require.EqualValues(t, now.Add(time.Second).UnixMilli(), view.Stats.NewestMs)
	require.False(t, view.Truncated)
}

func TestBuildHistoryTopKViewKeepsTheFieldAndBuckets(t *testing.T) {
	view := BuildHistoryTopKView(TopFieldSource, []TopEntry{{Value: "cached", Count: 2}, {Value: "upstream", Count: 1}})
	require.Equal(t, "source", view.Field)
	require.Len(t, view.Entries, 2)
	require.Equal(t, "cached", view.Entries[0].Value)
}
