package dns

// O03 — the history's read path under concurrent readers.
//
// The record path is drained on the subscription's goroutine while management callers query,
// aggregate and read stats from their own goroutines. The mutex makes that safe, but "safe by
// construction" is not evidence: this case runs readers against a live recording storm, under
// -race, and asserts every read is well formed and that the history still answers afterwards.

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHistoryServesConcurrentReadersWhileRecording(t *testing.T) {
	bus := NewEventBus(256)
	history := NewHistory(HistoryOptions{})
	detach := history.Subscribe(bus)
	defer detach()

	const (
		readerCount = 8
		readRounds  = 200
		eventCount  = 500
	)
	var (
		done        atomic.Bool
		failure     atomic.Value
		readerGroup sync.WaitGroup
	)
	readers := func() {
		defer readerGroup.Done()
		for round := 0; round < readRounds; round++ {
			records, err := history.Query(HistoryQuery{Limit: 8})
			if err != nil {
				failure.Store(fmt.Sprintf("Query returned %v", err))
				return
			}
			if len(records) > 8 {
				failure.Store(fmt.Sprintf("Query returned %d records for Limit 8", len(records)))
				return
			}
			if entries := history.TopK(TopFieldSource, 4); len(entries) > 4 {
				failure.Store(fmt.Sprintf("TopK returned %d buckets for k=4", len(entries)))
				return
			}
			_ = history.Stats()
			if done.Load() {
				return
			}
		}
	}
	for index := 0; index < readerCount; index++ {
		readerGroup.Add(1)
		go readers()
	}

	for index := 0; index < eventCount; index++ {
		bus.emit(QueryEvent{
			Time:         time.Now(),
			TransportTag: "udp-main",
			Name:         fmt.Sprintf("host-%03d.example.org.", index%40),
			QType:        1,
			Source:       QuerySourceUpstream,
			RCode:        0,
			Final:        true,
		})
	}
	done.Store(true)
	readerGroup.Wait()

	if recorded, loaded := failure.Load().(string); loaded {
		t.Fatalf("a concurrent read failed: %s", recorded)
	}

	// The history is still usable after the storm, and it is still bounded: the read path never
	// grew the ring past its configured limits.
	require.Eventually(t, func() bool {
		records, err := history.Query(HistoryQuery{Limit: 4})
		require.NoError(t, err)
		return len(records) == 4
	}, 5*time.Second, time.Millisecond, "the history stopped answering after concurrent reads")
	stats := history.Stats()
	require.LessOrEqual(t, stats.Entries, defaultHistoryEntries, "the count bound held under concurrent readers")
}
