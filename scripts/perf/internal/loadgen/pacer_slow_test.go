package loadgen

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestPacerSlowCallbackSkipsInsteadOfBurst pins the blocking-write behaviour:
// when a callback takes longer than the interval, the schedule must skip slots
// (and count them), not fire a catch-up burst. The callback count is the write
// completion rate, which is why the pacer cannot report the configured rate as
// achieved.
func TestPacerSlowCallbackSkipsInsteadOfBurst(t *testing.T) {
	const rate = 1000 // 1 ms interval
	var calls atomic.Int64
	start := time.Now()
	stats := runPacer(context.Background(), start, start.Add(200*time.Millisecond), rate, func() {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
	})
	if calls.Load() >= 40 {
		t.Fatalf("callback ran %d times; the pacer burst instead of skipping", calls.Load())
	}
	if stats.scheduled != calls.Load()+stats.skipped {
		t.Fatalf("scheduled=%d attempts=%d skipped=%d", stats.scheduled, calls.Load(), stats.skipped)
	}
	if stats.skipped == 0 {
		t.Fatal("a slow callback should produce skipped slots")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("pacer overran: %v", elapsed)
	}
}
