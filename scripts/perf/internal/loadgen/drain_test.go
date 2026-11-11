package loadgen

import (
	"context"
	"testing"
	"time"
)

// TestDrainCompleteDefinition pins the only definition a loss figure may rely
// on: the tail was actually observed. "The context was not cancelled" is not
// enough, and a zero-length drain cannot rule out a late tail when replies are
// missing.
func TestDrainCompleteDefinition(t *testing.T) {
	cancelled := context.Canceled
	cases := []struct {
		name      string
		ctxErr    error
		drain     time.Duration
		written   int64
		unique    int64
		lastReply time.Duration
		sawReply  bool
		want      bool
	}{
		{"cancelled", cancelled, 500 * time.Millisecond, 100, 90, 100 * time.Millisecond, true, false},
		{"no drain, loss", nil, 0, 100, 90, 100 * time.Millisecond, true, false},
		{"no drain, all returned", nil, 0, 100, 100, 100 * time.Millisecond, true, true},
		{"no drain, nothing sent", nil, 0, 0, 0, 0, false, true},
		{"full drain, no replies", nil, 500 * time.Millisecond, 100, 0, 0, false, true},
		{"tail at window edge", nil, 500 * time.Millisecond, 100, 90, 480 * time.Millisecond, true, false},
		{"tail with room", nil, 500 * time.Millisecond, 100, 90, 100 * time.Millisecond, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := drainCompleteFor(tc.ctxErr, tc.drain, tc.written, tc.unique, tc.lastReply, tc.sawReply); got != tc.want {
				t.Fatalf("drainCompleteFor() = %v, want %v", got, tc.want)
			}
		})
	}
}
