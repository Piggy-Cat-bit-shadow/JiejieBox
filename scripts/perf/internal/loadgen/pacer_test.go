package loadgen

import (
	"context"
	"testing"
	"time"
)

func TestPacerHonoursRate(t *testing.T) {
	start := time.Now()
	deadline := start.Add(400 * time.Millisecond)
	stats := runPacer(context.Background(), start, deadline, 500, func() {})
	if stats.aborted {
		t.Fatal("unexpected abort")
	}
	if stats.scheduled < 180 || stats.scheduled > 220 {
		t.Fatalf("scheduled = %d at 500 pps over 400 ms", stats.scheduled)
	}
	if stats.skipped > 20 {
		t.Fatalf("skipped = %d on an idle pacer", stats.skipped)
	}
}

func TestPacerSkipsMissedSlotsWithoutBurst(t *testing.T) {
	start := time.Now()
	deadline := start.Add(50 * time.Millisecond)
	attempts := 0
	stats := runPacer(context.Background(), start, deadline, 2000, func() {
		attempts++
		time.Sleep(2 * time.Millisecond) // each slot costs four intervals
	})
	if stats.skipped == 0 {
		t.Fatal("a slow send must produce skipped slots")
	}
	if stats.scheduled < int64(attempts) {
		t.Fatalf("scheduled (%d) < attempts (%d)", stats.scheduled, attempts)
	}
	if attempts > 30 {
		t.Fatalf("burst caught up: %d sends in 50 ms", attempts)
	}
	if stats.scheduled != int64(attempts)+stats.skipped {
		t.Fatalf("scheduled=%d attempts=%d skipped=%d", stats.scheduled, attempts, stats.skipped)
	}
}

func TestPacerStopsAtDeadline(t *testing.T) {
	start := time.Now()
	deadline := start.Add(40 * time.Millisecond)
	var last time.Time
	runPacer(context.Background(), start, deadline, 200, func() { last = time.Now() })
	if last.IsZero() {
		t.Fatal("pacer sent nothing")
	}
	if last.After(deadline.Add(2 * time.Millisecond)) {
		t.Fatalf("send at %v is after deadline %v", last.Sub(start), deadline.Sub(start))
	}
}

func TestPacerStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	done := make(chan struct{})
	var stats struct {
		scheduled int64
		skipped   int64
		aborted   bool
	}
	go func() {
		stats = runPacer(ctx, start, start.Add(time.Second), 200, func() {})
		close(done)
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("pacer ignored context cancellation")
	}
	if !stats.aborted {
		t.Fatal("aborted flag not set")
	}
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Fatalf("pacer ran %v after cancel", elapsed)
	}
}
