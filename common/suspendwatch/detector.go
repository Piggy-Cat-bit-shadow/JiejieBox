// Package suspendwatch detects that the host was suspended between two observations.
//
// # Why this exists on a desktop
//
// A suspended process is not told that its sockets died. On macOS the NetworkExtension provider
// receives sleep()/wake(), but the headless client does not, and a provider whose platform path did
// not change after wake never produces an interface update at all. The result is the state
// docs/archive/2026-10-dev-cycle/ACTIVE_TCP_STALL_INVESTIGATION.md describes: every socket still
// looks established, the kernel keeps accepting writes, and nothing ends the flow.
//
// # Why two clocks
//
// Go's monotonic clock on Darwin is mach_absolute_time, which does not advance while the machine is
// asleep. The wall clock does. A reading where the wall advanced materially further than the
// monotonic clock therefore means the process was not running for the difference - a suspend, not a
// scheduling delay. A backward wall step (NTP, a manual clock change) is ignored: it is not evidence
// of a suspend, and a reset caused by a clock correction would be a false positive.
//
// The detector is deliberately pure and driven by its caller. It has no timer and no goroutine, so
// its decision can be tested without sleeping.
package suspendwatch

import "time"

// Detector compares a wall clock reading against a monotonic reading taken at the same instant.
type Detector struct {
	// Threshold is the smallest divergence treated as a suspend. It must be larger than the
	// ordinary jitter between the two readings and larger than the tick interval the caller uses,
	// or a normally scheduled callback would be reported as a resume.
	Threshold time.Duration

	primed   bool
	lastWall time.Time
	lastMono time.Duration
}

// Observe records one pair of readings and reports whether the host resumed from a suspend since
// the previous pair.
//
// gap is the part of the wall-clock advance the monotonic clock did not see. It is zero when the
// call returns false.
func (d *Detector) Observe(wall time.Time, mono time.Duration) (resumed bool, gap time.Duration) {
	if !d.primed {
		d.primed = true
		d.lastWall = wall
		d.lastMono = mono
		return false, 0
	}
	wallDelta := wall.Sub(d.lastWall)
	monoDelta := mono - d.lastMono
	d.lastWall = wall
	d.lastMono = mono
	if wallDelta <= 0 || monoDelta < 0 {
		// The wall clock went backwards, or the caller's monotonic reading did. Neither is a
		// suspend. The base is discarded rather than kept: a forward correction after a backward
		// step would otherwise look exactly like a suspend on the next reading (a large positive
		// wall delta against a small monotonic one), and the next observation primes cleanly.
		d.Reset()
		return false, 0
	}
	gap = wallDelta - monoDelta
	if gap < d.Threshold {
		return false, 0
	}
	return true, gap
}

// Reset forgets the previous reading, so the next observation primes the detector instead of being
// compared against a stale base. It is for a caller that knows the clock relationship changed for a
// reason that is not a suspend (for example, re-arming after a close and a restart).
func (d *Detector) Reset() {
	d.primed = false
	d.lastWall = time.Time{}
	d.lastMono = 0
}
