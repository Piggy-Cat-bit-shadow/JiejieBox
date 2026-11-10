package dns

// The interleaving the review asks about: a read racing a Clear.
//
// `QueryPage` copies under the history's lock and `Clear` empties under the same lock, so a reader
// must see one of exactly two worlds - the window as it was, or the empty window - and never a
// half-cleared slice. The test holds that lock itself to force the race, then asserts the ONLY
// possible outcomes. It is deliberately order-agnostic: which of the two wins is not the property.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAReadRacingAClearSeesOneWholeWorld(t *testing.T) {
	history := NewHistory(HistoryOptions{MaxEntries: 8, MaxBytes: 1 << 20, TTL: time.Hour})
	for index := 0; index < 3; index++ {
		history.Record(QueryEvent{
			Time:         time.Now(),
			TransportTag: "local",
			Name:         "race.example.test",
			Source:       QuerySourceLocal,
		})
	}

	// Hold the lock a reader would hold, so both goroutines start blocked behind it and the release
	// decides their order. The sleep only creates contention; the assertions below do not depend on
	// which one runs first.
	history.access.Lock()
	pageResult := make(chan int, 1)
	clearResult := make(chan int, 1)
	go func() {
		page, err := history.QueryPage(HistoryQuery{Limit: 8})
		if err != nil {
			pageResult <- -1
			return
		}
		pageResult <- len(page.Records)
	}()
	go func() {
		clearResult <- history.Clear()
	}()
	time.Sleep(50 * time.Millisecond)
	history.access.Unlock()

	count := <-pageResult
	cleared := <-clearResult

	require.Contains(t, []int{0, 3}, count,
		"a reader must see the whole window or the empty window, never a partial one (saw %d)", count)
	require.Equal(t, 3, cleared, "the clear removes exactly what was there")
	// Cross-check the world the reader saw against the window's own accounting.
	require.Equal(t, count, history.Stats().Entries,
		"after both operations the window holds exactly the records the reader kept")
}

func TestASecondReaderCannotSeeAHalfWrittenRecord(t *testing.T) {
	history := NewHistory(HistoryOptions{MaxEntries: 64, MaxBytes: 1 << 20, TTL: time.Hour})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for index := 0; index < 200; index++ {
			history.Record(QueryEvent{
				Time:         time.Now(),
				TransportTag: "local",
				Name:         "writer.example.test",
				Source:       QuerySourceLocal,
				Answers:      []string{"192.0.2.1", "192.0.2.2"},
			})
		}
	}()
	for {
		select {
		case <-done:
			return
		default:
		}
		page, err := history.QueryPage(HistoryQuery{Limit: 64})
		require.NoError(t, err)
		for _, record := range page.Records {
			require.NotEmpty(t, record.Event.Name, "no reader may observe a record that is still being built")
			require.Len(t, record.Event.Answers, 2, "the answer slice must be complete when it is visible")
		}
	}
}
