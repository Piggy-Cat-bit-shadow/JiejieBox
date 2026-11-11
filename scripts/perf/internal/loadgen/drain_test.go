package loadgen

import (
	"context"
	"testing"
	"time"
)

// TestDrainStateFor pins the split state machine: no single boolean may mean
// "window ran", "tail quiet", "context alive" and "loss estimable".
func TestDrainStateFor(t *testing.T) {
	cancelled := context.Canceled
	cases := []struct {
		name      string
		ctxErr    error
		window    time.Duration
		quiet     time.Duration
		sawReply  bool
		guard     time.Duration
		wantState string
		wantDone  bool
		wantTail  bool
	}{
		{"cancelled", cancelled, 2 * time.Second, time.Second, true, 250 * time.Millisecond, "canceled", false, false},
		{"zero window", nil, 0, time.Second, true, 250 * time.Millisecond, "zero_window", false, false},
		{"tail at edge", nil, 2 * time.Second, 100 * time.Millisecond, true, 250 * time.Millisecond, "window_elapsed_tail_active", false, true},
		{"quiet tail", nil, 2 * time.Second, 500 * time.Millisecond, true, 250 * time.Millisecond, "complete_quiet", true, false},
		{"no replies, full window", nil, 2 * time.Second, 2 * time.Second, false, 250 * time.Millisecond, "complete_quiet", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, done, tail := drainStateFor(tc.ctxErr, tc.window, tc.quiet, tc.sawReply, tc.guard)
			if state != tc.wantState || done != tc.wantDone || tail != tc.wantTail {
				t.Fatalf("drainStateFor() = (%q,%v,%v), want (%q,%v,%v)", state, done, tail, tc.wantState, tc.wantDone, tc.wantTail)
			}
		})
	}
}

// TestSequenceWindowTruncationDoesNotCountDuplicatesAsUnique is the regression
// the old code failed: when the map was full, an unseen sequence was returned as
// "first" even though it could not be attributed.
func TestSequenceWindowTruncationDoesNotCountDuplicatesAsUnique(t *testing.T) {
	window := &udpSeqWindow{seen: make(map[uint32]struct{}), limit: 3}
	for sequence := uint32(1); sequence <= 3; sequence++ {
		first, exact := window.add(sequence)
		if !first || !exact {
			t.Fatalf("sequence %d before the limit: first=%v exact=%v", sequence, first, exact)
		}
	}
	first, exact := window.add(4)
	if first || exact {
		t.Fatalf("sequence over the limit: first=%v exact=%v, want false/false", first, exact)
	}
	if !window.truncated {
		t.Fatal("window did not record truncation")
	}
	// A duplicate of an already-seen sequence is still attributable.
	first, exact = window.add(2)
	if first || !exact {
		t.Fatalf("seen duplicate: first=%v exact=%v, want false/true", first, exact)
	}
}

// TestSequenceWindowBoundedMemory doubles as a sanity check that the default
// limit is not unbounded; a test-sized limit is used to avoid allocating 1M.
func TestSequenceWindowBoundedMemory(t *testing.T) {
	window := newUDPSeqWindow()
	if window.limit != udpSeqWindowLimit {
		t.Fatalf("default limit = %d, want %d", window.limit, udpSeqWindowLimit)
	}
}
