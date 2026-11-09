package group

import (
	"sync/atomic"
	"time"
)

// attemptStartSequence hands out strictly increasing attempt-start timestamps.
//
// # The problem this exists for
//
// Health history is ordered by when a probe STARTED, not when it finished, so that a probe which
// began before a path change cannot replace the result a later probe already stored. The store
// compares those timestamps directly and deliberately ACCEPTS equal ones: a platform with a coarse
// clock must not lose a real measurement just because two attempts read the same instant.
//
// That rule is only safe when two attempts that really began in order can be told apart. On this
// host they cannot always be: time.Now() advances in roughly 512 microsecond steps (measured:
// 100,000 consecutive calls returned 99,996 equal values), so a probe that began first and a probe
// that began a few hundred microseconds later can carry the same timestamp. The store then sees
// "equal" and keeps the LAST writer, which is the older probe - exactly the overwrite the ordering
// rule exists to prevent.
//
// The producer therefore hands out the ordering fact rather than reading the raw clock: each
// attempt that begins gets a value strictly greater than every value handed out before it, even
// when the clock has not moved. The comparison in the store is unchanged; the tie is removed at
// the point where the real order is known.
//
// # Why a package-level sequence and not a per-group one
//
// Two groups can measure the same tag against the same target, and URLTestOutboundsWithTarget is
// itself a producer (its attempts are not owned by a group object). A per-group counter would let
// two producers collide again. One process-wide sequence makes "started later" total across every
// producer in this package.
//
// # Why the timestamp stays wall-clock-shaped
//
// The stored value is still shown to callers and compared with time.Since for the freshness
// window, so it must remain a real timestamp, not an opaque id. The sequence only nudges it by the
// smallest amount needed (one nanosecond past the previous value), which keeps it within a
// negligible distance of the wall clock while making it strictly ordered.
var lastAttemptStartNanos atomic.Int64

// nextAttemptStart stamps one attempt with a start time strictly later than every stamp handed
// out before it.
func nextAttemptStart() time.Time {
	started := time.Now().UnixNano()
	for {
		previous := lastAttemptStartNanos.Load()
		candidate := started
		if candidate <= previous {
			candidate = previous + 1
		}
		if lastAttemptStartNanos.CompareAndSwap(previous, candidate) {
			return time.Unix(0, candidate)
		}
	}
}
