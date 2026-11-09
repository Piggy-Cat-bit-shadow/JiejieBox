package group

import (
	"testing"
	"time"
)

// TestNextAttemptStartIsStrictlyIncreasingEvenOnACoarseClock pins the property the history store
// depends on: two attempts that begin in order must be orderable, even when the platform clock
// returns the same instant for both.
//
// The loop is the measurement, not decoration. On this host time.Now() advances in roughly 512
// microsecond steps, so any ten thousand consecutive raw readings collapse into a handful of
// distinct values; the producer must still hand out ten thousand distinct, increasing ones.
func TestNextAttemptStartIsStrictlyIncreasingEvenOnACoarseClock(t *testing.T) {
	previous := nextAttemptStart()
	rawEqual := 0
	rawPrevious := time.Now()
	for index := 0; index < 10000; index++ {
		rawNow := time.Now()
		if rawNow.Equal(rawPrevious) {
			rawEqual++
		}
		rawPrevious = rawNow

		next := nextAttemptStart()
		if !next.After(previous) {
			t.Fatalf("attempt %d: %v is not strictly after %v", index, next, previous)
		}
		previous = next
	}
	if rawEqual == 0 {
		t.Log("the raw clock advanced on every read in this run; the ordering assertion above still ran 10000 times")
	}
}

// TestNextAttemptStartStaysWallClockShaped keeps the stamp usable where the tree already uses it:
// display, and the freshness window compared with time.Since.
func TestNextAttemptStartStaysWallClockShaped(t *testing.T) {
	before := time.Now()
	stamp := nextAttemptStart()
	after := time.Now()
	if stamp.Before(before) {
		t.Fatalf("stamp %v is before the call that produced it (%v)", stamp, before)
	}
	if stamp.After(after.Add(time.Second)) {
		t.Fatalf("stamp %v ran more than a second past the call (%v)", stamp, after)
	}
}
