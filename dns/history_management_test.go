package dns

// O04 management — the consumer side of the DNS history: an output budget, an explicit clear, a
// restart that starts a fresh window, and concurrent readers.
//
// The ring's own bounds are covered by the history tests and the production entry by
// history_access_test.go. What a management consumer adds is a caller that pages, clears and reads
// from several goroutines, and those must be bounded by the history rather than by the caller's good
// behaviour.

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// sizedHistoryEvent builds an event whose retained size is predictable.
func sizedHistoryEvent(name string, size int) QueryEvent {
	base := name
	for len(base) < size {
		base += name
	}
	return QueryEvent{
		Time:         time.Now(),
		TransportTag: "remote-a",
		Name:         base[:size],
		QType:        1,
		Source:       QuerySourceUpstream,
		RCode:        0,
	}
}

// TestTheReadBudgetBoundsTheCopiedBytes pins the OUTPUT budget: the record count alone is not a
// bound on a history whose records carry names and answers.
func TestTheReadBudgetBoundsTheCopiedBytes(t *testing.T) {
	history := NewHistory(HistoryOptions{MaxEntries: 64, MaxBytes: 1 << 20})
	for index := 0; index < 16; index++ {
		history.Record(sizedHistoryEvent("example.com.", 512))
	}

	records, err := history.Query(HistoryQuery{Limit: 64, MaxBytes: 4096})
	require.NoError(t, err)
	require.NotEmpty(t, records)
	copied := 0
	for _, record := range records {
		copied += historyRecordSize(record.Event)
	}
	require.LessOrEqual(t, copied, 4096,
		"one read must not copy more than the caller's byte budget")
	require.Less(t, len(records), 16, "the budget stopped the read before the whole window")

	// The budget is not a way to hide the first record: a single oversized record is still readable.
	oversized := NewHistory(HistoryOptions{MaxEntries: 4})
	oversized.Record(sizedHistoryEvent("big.example.", 8192))
	records, err = oversized.Query(HistoryQuery{Limit: 4, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, records, 1, "a record larger than the budget is returned on its own")
}

// TestClearEmptiesTheWindowAndKeepsTheSequence hands the consumer a fresh window without reusing an
// identity: a cursor from before the clear must not be served old records after a later append.
func TestClearEmptiesTheWindowAndKeepsTheSequence(t *testing.T) {
	history := NewHistory(HistoryOptions{MaxEntries: 8})
	for index := 0; index < 4; index++ {
		history.Record(sizedHistoryEvent("example.com.", 32))
	}
	records, err := history.Query(HistoryQuery{Limit: 8})
	require.NoError(t, err)
	require.Len(t, records, 4)
	cursor := records[len(records)-1].Sequence

	cleared := history.Clear()
	require.Equal(t, 4, cleared)
	require.Empty(t, mustQuery(t, history, HistoryQuery{Limit: 8}))
	stats := history.Stats()
	require.Zero(t, stats.Entries)
	require.Zero(t, stats.Bytes)
	require.EqualValues(t, 1, stats.Cleared)

	history.Record(sizedHistoryEvent("after-clear.example.", 32))
	records = mustQuery(t, history, HistoryQuery{Limit: 8})
	require.Len(t, records, 1)
	require.Greater(t, records[0].Sequence, cursor,
		"the sequence keeps counting across a clear, so an old cursor cannot match a new record")

	// A caller still paging from the old cursor sees only what came after it - which is exactly the
	// new record, not the four that were cleared.
	records = mustQuery(t, history, HistoryQuery{Limit: 8, Since: cursor})
	require.Len(t, records, 1)
}

func mustQuery(t *testing.T, history *History, query HistoryQuery) []HistoryRecord {
	t.Helper()
	records, err := history.Query(query)
	require.NoError(t, err)
	return records
}

// TestAReenabledRouterHistoryStartsAFreshWindow is the restart shape: disable and enable again must
// produce a new ring rather than resurrect the old records, and the old ring must stop receiving.
func TestAReenabledRouterHistoryStartsAFreshWindow(t *testing.T) {
	router := newHistoryTestRouter(t)
	first := router.EnableHistory(HistoryOptions{MaxEntries: 8})
	emitTestQuery(router, "remote-a", QuerySourceUpstream)
	require.Eventually(t, func() bool {
		return len(mustQuery(t, first, HistoryQuery{Limit: 8})) == 1
	}, time.Second, time.Millisecond)

	router.DisableHistory()
	require.Nil(t, router.DNSHistory())

	second := router.EnableHistory(HistoryOptions{MaxEntries: 8})
	require.NotSame(t, first, second, "a restart must not resurrect the previous window")
	require.Empty(t, mustQuery(t, second, HistoryQuery{Limit: 8}))

	emitTestQuery(router, "remote-b", QuerySourceCache)
	require.Eventually(t, func() bool {
		return len(mustQuery(t, second, HistoryQuery{Limit: 8})) == 1
	}, time.Second, time.Millisecond)
	detached := mustQuery(t, first, HistoryQuery{Limit: 8})
	require.Len(t, detached, 1, "the detached ring keeps what it had and stops receiving new events")
	require.Equal(t, "remote-a", detached[0].Event.TransportTag,
		"the event emitted after the re-enable must not reach the detached ring")

	router.DisableHistory()
}

// TestTheRouterClearEntryReportsTheClearedCount drives the management entry.
func TestTheRouterClearEntryReportsTheClearedCount(t *testing.T) {
	router := newHistoryTestRouter(t)
	_, err := router.ClearHistory()
	require.ErrorIs(t, err, ErrHistoryNotEnabled,
		"clearing a feature that is not running must say so, not report an empty window")

	router.EnableHistory(HistoryOptions{MaxEntries: 8})
	emitTestQuery(router, "remote-a", QuerySourceUpstream)
	require.Eventually(t, func() bool {
		_, _, statsErr := router.HistoryStats()
		return statsErr == nil
	}, time.Second, time.Millisecond)
	require.Eventually(t, func() bool {
		cleared, clearErr := router.ClearHistory()
		if clearErr != nil {
			return false
		}
		if cleared > 0 {
			require.Empty(t, mustQuery(t, router.DNSHistory(), HistoryQuery{Limit: 8}))
			return true
		}
		return false
	}, time.Second, time.Millisecond)

	_, _, err = router.HistoryStats()
	require.NoError(t, err)
	router.DisableHistory()
}

// TestConcurrentReadersShareOneBoundedScan pins the read side under concurrency: N readers and a
// writer on one ring, each read inside its own budget, with no race and no unbounded copy.
func TestConcurrentReadersShareOneBoundedScan(t *testing.T) {
	history := NewHistory(HistoryOptions{MaxEntries: 32, MaxBytes: 1 << 16})
	stop := make(chan struct{})
	var writers sync.WaitGroup
	writers.Add(1)
	go func() {
		defer writers.Done()
		index := 0
		for {
			select {
			case <-stop:
				return
			default:
				history.Record(sizedHistoryEvent(fmt.Sprintf("name-%d.example.", index), 64))
				index++
			}
		}
	}()

	var readers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for round := 0; round < 50; round++ {
				records, err := history.Query(HistoryQuery{Limit: 32, MaxBytes: 2048})
				if err != nil {
					t.Errorf("query: %v", err)
					return
				}
				copied := 0
				for _, record := range records {
					copied += historyRecordSize(record.Event)
				}
				if copied > 2048 && len(records) > 1 {
					t.Errorf("read copied %d bytes over the budget with %d records", copied, len(records))
					return
				}
			}
		}()
	}
	readers.Wait()
	close(stop)
	writers.Wait()
	require.LessOrEqual(t, history.Stats().Entries, 32)
}
