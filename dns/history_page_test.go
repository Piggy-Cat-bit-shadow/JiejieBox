package dns

// The pagination signal at the layer that produces it.
//
// `Query` returns at most Limit records; when it stops at the limit the caller cannot tell "the
// window ended" from "there is more". `QueryPage` carries that fact. These tests pin the three
// reasons a page can be short - the limit, the read byte budget, and the filters - plus the two
// cases where there is nothing more.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func pageEvent(name string) QueryEvent {
	event := QueryEvent{Time: time.Now(), TransportTag: "local", Name: name, QType: 1, Source: QuerySource("rule")}
	return event
}

// TestThePageKnowsWhetherAnotherMatchingRecordExists is the regression for the review: a limit=1
// read over two records reported one record and no signal that the second existed.
func TestThePageKnowsWhetherAnotherMatchingRecordExists(t *testing.T) {
	history := NewHistory(HistoryOptions{MaxEntries: 16, MaxBytes: 1 << 20})
	history.Record(pageEvent("one."))
	history.Record(pageEvent("two."))

	first, err := history.QueryPage(HistoryQuery{Limit: 1})
	require.NoError(t, err)
	require.Len(t, first.Records, 1)
	require.True(t, first.HasMore, "a second matching record exists behind the limit")
	require.Equal(t, 2, first.Stats.Entries, "the stats describe the same instant as the records")

	second, err := history.QueryPage(HistoryQuery{Limit: 1, Since: first.Records[0].Sequence})
	require.NoError(t, err)
	require.Len(t, second.Records, 1)
	require.False(t, second.HasMore, "the window ended; claiming more would make the cursor loop")

	none, err := history.QueryPage(HistoryQuery{Limit: 1, Since: second.Records[0].Sequence})
	require.NoError(t, err)
	require.Empty(t, none.Records)
	require.False(t, none.HasMore)
}

// TestThePageKnowsWhenTheByteBudgetStoppedIt: the read budget is a second reason to stop, and it
// must also set HasMore.
func TestThePageKnowsWhenTheByteBudgetStoppedIt(t *testing.T) {
	history := NewHistory(HistoryOptions{MaxEntries: 16, MaxBytes: 1 << 20})
	history.Record(sizedHistoryEvent("first.example.", 512))
	history.Record(sizedHistoryEvent("second.example.", 512))

	page, err := history.QueryPage(HistoryQuery{Limit: 8, MaxBytes: 600})
	require.NoError(t, err)
	require.Len(t, page.Records, 1, "the second record does not fit the read budget")
	require.True(t, page.HasMore, "the record that did not fit is still there for the next page")
}

// TestHasMoreCountsOnlyMatchingRecords: the signal must respect the filters, or a caller paging a
// filtered view would be told there is more forever.
func TestHasMoreCountsOnlyMatchingRecords(t *testing.T) {
	history := NewHistory(HistoryOptions{MaxEntries: 16, MaxBytes: 1 << 20})
	local := pageEvent("local.")
	local.TransportTag = "local"
	other := pageEvent("other.")
	other.TransportTag = "other"
	history.Record(local)
	history.Record(other)

	page, err := history.QueryPage(HistoryQuery{Limit: 1, Transport: "local"})
	require.NoError(t, err)
	require.Len(t, page.Records, 1)
	require.False(t, page.HasMore,
		"the other record does not match the filter, so the filtered window ended at the first page")
}

// TestAnEmptyWindowHasNothingMore keeps the degenerate case honest.
func TestAnEmptyWindowHasNothingMore(t *testing.T) {
	history := NewHistory(HistoryOptions{MaxEntries: 4})
	page, err := history.QueryPage(HistoryQuery{})
	require.NoError(t, err)
	require.Empty(t, page.Records)
	require.False(t, page.HasMore)
	require.Zero(t, page.Stats.Entries)

	var nilHistory *History
	nilPage, err := nilHistory.QueryPage(HistoryQuery{})
	require.NoError(t, err)
	require.False(t, nilPage.HasMore)
	records, err := nilHistory.Query(HistoryQuery{})
	require.NoError(t, err)
	require.Empty(t, records)
}
