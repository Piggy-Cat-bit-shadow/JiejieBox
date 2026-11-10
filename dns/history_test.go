package dns

// O04 — the bounded history's own bounds, checked directly.
//
// The history is the one component whose failure mode is "it grew": an unbounded store of
// query events is a leak that looks like a feature. Each case below pins one bound and the
// accounting that makes it observable.

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// historyEvent builds an event with a controllable size and time.
func historyEvent(name string, transport string, source QuerySource, at time.Time) QueryEvent {
	return QueryEvent{
		Time:         at,
		TransportTag: transport,
		Name:         name,
		QType:        1,
		Source:       source,
		RCode:        0,
	}
}

func TestHistoryEnforcesTheCountBound(t *testing.T) {
	history := NewHistory(HistoryOptions{MaxEntries: 4})
	now := time.Now()
	for index := 0; index < 10; index++ {
		history.Record(historyEvent(fmt.Sprintf("q%d.example.", index), "t", QuerySourceUpstream, now))
	}
	stats := history.Stats()
	require.Equal(t, 4, stats.Entries, "the retained window is the count bound, not the input")
	records, err := history.Query(HistoryQuery{Limit: 10})
	require.NoError(t, err)
	require.Len(t, records, 4)
	require.Equal(t, "q6.example.", records[0].Event.Name, "the OLDEST records are the ones evicted")
}

func TestHistoryEnforcesTheByteBound(t *testing.T) {
	// Each record is ~64 + name bytes; this budget holds only a few long names.
	history := NewHistory(HistoryOptions{MaxBytes: 256})
	now := time.Now()
	longName := ""
	for range 100 {
		longName += "x"
	}
	for index := 0; index < 8; index++ {
		history.Record(historyEvent(longName, "t", QuerySourceUpstream, now))
	}
	stats := history.Stats()
	require.LessOrEqual(t, stats.Bytes, 256, "retained bytes must stay inside the budget")
	require.Positive(t, stats.Entries)
}

func TestARecordLargerThanTheBudgetIsRejectedNotRetained(t *testing.T) {
	// The budget must hold a small record under the FULL accounting (fixed overhead, five string
	// headers and the source string); 128 bytes no longer does, which is the point of counting every
	// retained field.
	history := NewHistory(HistoryOptions{MaxBytes: 512})
	now := time.Now()
	history.Record(historyEvent("small.example.", "t", QuerySourceUpstream, now))
	history.Record(historyEvent(string(make([]byte, 512)), "t", QuerySourceUpstream, now))
	stats := history.Stats()
	require.Equal(t, 1, stats.Entries, "the oversized record must not evict the history for itself")
	require.EqualValues(t, 1, stats.Rejected)
	records, err := history.Query(HistoryQuery{Limit: 4})
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, "small.example.", records[0].Event.Name, "the retained record is the one that fit")
}

func TestHistoryExpiresByTTL(t *testing.T) {
	history := NewHistory(HistoryOptions{MaxEntries: 8, TTL: 50 * time.Millisecond})
	now := time.Now()
	history.Record(historyEvent("old.example.", "t", QuerySourceUpstream, now.Add(-time.Minute)))
	history.Record(historyEvent("new.example.", "t", QuerySourceUpstream, now))
	records, err := history.Query(HistoryQuery{Limit: 8})
	require.NoError(t, err)
	require.Len(t, records, 1, "an expired record must not be returned")
	require.Equal(t, "new.example.", records[0].Event.Name)
	require.Positive(t, history.Stats().Expired)
}

func TestHistoryPaginationAndOutputBound(t *testing.T) {
	history := NewHistory(HistoryOptions{MaxEntries: 32})
	now := time.Now()
	for index := 0; index < 10; index++ {
		history.Record(historyEvent(fmt.Sprintf("q%d.example.", index), "t", QuerySourceUpstream, now))
	}
	first, err := history.Query(HistoryQuery{Limit: 4})
	require.NoError(t, err)
	require.Len(t, first, 4)
	second, err := history.Query(HistoryQuery{Since: first[len(first)-1].Sequence, Limit: 4})
	require.NoError(t, err)
	require.Len(t, second, 4)
	require.Equal(t, "q4.example.", second[0].Event.Name, "the cursor is exclusive and continues the window")

	_, err = history.Query(HistoryQuery{Limit: maxHistoryQueryLimit + 1})
	require.ErrorIs(t, err, ErrHistoryQueryTooLarge, "an unbounded output request must be refused")
}

func TestHistoryFiltersByTransportAndSource(t *testing.T) {
	history := NewHistory(HistoryOptions{MaxEntries: 8})
	now := time.Now()
	history.Record(historyEvent("a.example.", "one", QuerySourceUpstream, now))
	history.Record(historyEvent("b.example.", "two", QuerySourceCache, now))

	byTransport, err := history.Query(HistoryQuery{Transport: "two", Limit: 8})
	require.NoError(t, err)
	require.Len(t, byTransport, 1)
	require.Equal(t, QuerySourceCache, byTransport[0].Event.Source)

	bySource, err := history.Query(HistoryQuery{Source: QuerySourceUpstream, Limit: 8})
	require.NoError(t, err)
	require.Len(t, bySource, 1)
	require.Equal(t, "one", bySource[0].Event.TransportTag)
}

func TestTopKFoldsHighCardinalityIntoOneBucket(t *testing.T) {
	history := NewHistory(HistoryOptions{MaxEntries: 64})
	now := time.Now()
	for index := 0; index < 64; index++ {
		history.Record(historyEvent("q.example.", fmt.Sprintf("transport-%d", index), QuerySourceUpstream, now))
	}
	top := history.TopK(TopFieldTransport, 4)
	require.LessOrEqual(t, len(top), 5, "top-K must fold the tail into ONE bucket, not retain every key")
	require.Equal(t, "other", top[len(top)-1].Value, "the folded bucket is explicit")
	total := 0
	for _, entry := range top {
		total += entry.Count
	}
	require.Equal(t, history.Stats().Entries, total, "the folded buckets must still account for every record")
}

func TestCapabilitiesSayWhatTheBuildCanDo(t *testing.T) {
	history := NewHistory(HistoryOptions{})
	capabilities := history.Capabilities()
	require.False(t, capabilities.Replay, "a bounded window is not a replayable log")
	require.NotEmpty(t, capabilities.Platform)
	require.Equal(t, "milliseconds", capabilities.Precision)
}

func TestHistorySubscribesToTheBus(t *testing.T) {
	bus := NewEventBus(8)
	history := NewHistory(HistoryOptions{MaxEntries: 8})
	unsubscribe := history.Subscribe(bus)
	defer unsubscribe()

	transport := &herdTransport{}
	client := newEventClient(t, transport, bus, true)
	runEventExchange(t, client, transport, herdMessage())

	require.Eventually(t, func() bool { return history.Stats().Entries == 1 }, 5*time.Second, time.Millisecond,
		"the history must receive the event the bus delivered")
	records, err := history.Query(HistoryQuery{Limit: 4})
	require.NoError(t, err)
	require.Equal(t, "herd", records[0].Event.TransportTag)
	require.Equal(t, QuerySourceUpstream, records[0].Event.Source)

	unsubscribe()
	runEventExchange(t, client, transport, herdMessage())
	require.Eventually(t, func() bool { return history.Stats().Entries == 1 }, 5*time.Second, time.Millisecond,
		"a detached history must stop recording")
}

// TestHistoryKeepsNoStateForAHighCardinalityNameStream is the leak case: names that never
// repeat must not accumulate anything beyond the count bound.
func TestHistoryKeepsNoStateForAHighCardinalityNameStream(t *testing.T) {
	history := NewHistory(HistoryOptions{MaxEntries: 10, MaxBytes: 1 << 16})
	now := time.Now()
	for index := 0; index < 5000; index++ {
		history.Record(historyEvent(fmt.Sprintf("host-%d.example.", index), "t", QuerySourceUpstream, now))
	}
	stats := history.Stats()
	require.Equal(t, 10, stats.Entries)
	require.LessOrEqual(t, stats.Bytes, 1<<16)
	top := history.TopK(TopFieldTransport, 4)
	require.Len(t, top, 1, "one transport means one bucket regardless of how many names passed through")
	require.Equal(t, stats.Entries, top[0].Count)
}
