package dns

// The cost of the diagnostic history on both sides of its contract.
//
// The window-07 performance question was "what does enabling the DNS history cost the query path,
// and what does reading it cost?" - and the proxy benchmark harness cannot answer it, because it
// pushes bytes through a tunnel and never issues a DNS query.
//
// These benchmarks answer both halves directly:
//
//   - Record is what the query path pays per query while the history is enabled: one event build
//     and one ring insert. Nothing else on the query path changes.
//   - QueryPage is what a diagnostic read pays per page, with filters and the copied records.
//
// Both are reported in ns/op so the cost can be compared with a DNS round trip instead of guessed.

import (
	"fmt"
	"testing"
	"time"

	mDNS "github.com/miekg/dns"
)

func benchHistory(b *testing.B, entries int) *History {
	b.Helper()
	history := NewHistory(HistoryOptions{MaxEntries: entries, MaxBytes: 4 << 20, TTL: time.Hour})
	for index := 0; index < entries; index++ {
		history.Record(benchEvent(index))
	}
	if stats := history.Stats(); stats.Entries != entries {
		b.Fatalf("fixture recorded %d of %d entries (rejected=%d)", stats.Entries, entries, stats.Rejected)
	}
	return history
}

func benchEvent(index int) QueryEvent {
	return QueryEvent{
		// Recent, because the ring expires by TTL: records dated 1970 would be swept while the fixture is being built.
		Time:         time.Now().Add(-time.Duration(5000-index) * time.Millisecond),
		TransportTag: "local",
		Name:         fmt.Sprintf("host-%04d.example.test", index),
		QType:        mDNS.TypeA,
		Source:       QuerySourceLocal,
		RCode:        0,
		DurationMs:   3,
		Rule:         "rule-匹配-示例",
		Answers:      []string{"192.0.2.1", "192.0.2.2"},
	}
}

// BenchmarkHistoryRecord measures the per-query cost paid while the history is on.
func BenchmarkHistoryRecord(b *testing.B) {
	history := benchHistory(b, 512)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		event := benchEvent(index)
		event.Time = time.Now()
		history.Record(event)
	}
}

// BenchmarkHistoryQueryPageOneThousand measures one page read over a full window.
func BenchmarkHistoryQueryPageOneThousand(b *testing.B) {
	history := benchHistory(b, 1000)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		page, err := history.QueryPage(HistoryQuery{Limit: 32})
		if err != nil || len(page.Records) == 0 {
			b.Fatalf("page=%d err=%v", len(page.Records), err)
		}
	}
}

// BenchmarkHistoryQueryPageFiltered measures the same read when the filter has to walk the window:
// nothing matches, so this is the worst case for a diagnostic that asks the wrong question.
func BenchmarkHistoryQueryPageFiltered(b *testing.B) {
	history := benchHistory(b, 1000)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		page, err := history.QueryPage(HistoryQuery{Limit: 32, Transport: "other"})
		if err != nil {
			b.Fatal(err)
		}
		if len(page.Records) != 0 {
			b.Fatalf("the filter matched %d records", len(page.Records))
		}
	}
}

// BenchmarkHistoryStats measures the accounting read behind the manager's counters.
func BenchmarkHistoryStats(b *testing.B) {
	history := benchHistory(b, 1000)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = history.Stats()
	}
}

// BenchmarkEventBusPublish measures what a subscriber costs the query path: the bus does a
// non-blocking send per subscriber, so this is the shape of "history enabled" at the bus.
func BenchmarkEventBusPublish(b *testing.B) {
	bus := NewEventBus(64)
	_, unsubscribe := bus.Subscribe(false, 0)
	defer unsubscribe()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		bus.emit(benchEvent(index))
	}
}
