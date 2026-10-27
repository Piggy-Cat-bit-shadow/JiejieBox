package route

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
)

// Half-close observation diagnostics.
//
// # What this is for
//
// The half-close observer (conn_halfclose.go) claims that a flow whose peer has gone is released
// shortly after that happens, instead of being held until close-all. A claim like that is only worth
// something if a real-device run can show it firing, and the device cannot be instrumented for the
// purpose. So the mechanism counts itself and reports ONE line when the tunnel stops, exactly like
// the splice and transition summaries next to it.
//
// A phone run therefore needs no instrumentation at all: start the tunnel, use the apps, close them,
// let the phone sit, stop the tunnel, read one line.
//
// # The state contract
//
// Every observation that was armed is counted once in `observed`, and then reaches exactly one final
// state, decided by a compare-and-swap in `halfClosedObserver.settle`:
//
//	released   a terminal error was read from the finished side and reclamation was invoked; split
//	           into `peer reset` (the peer's stack answered a probe) and `keepalive timeout`
//	           (nothing answered).
//	stopped    no peer-gone verdict: the surviving direction finished the flow first, this side was
//	           closed locally, an unexpected byte broke the premise, or the tunnel shut down while
//	           the observation was pending.
//	pending    no final state yet. DERIVED, never stored: observed - released - stopped.
//
// The invariant `observed == released + stopped + pending` is what a reader should check, and it is
// asserted by the tests. It holds at any snapshot because `observed` is incremented before the probe
// is scheduled, each observation settles at most once, and the counters are read under ONE lock: a
// settle in flight is either entirely before the snapshot or entirely after it, so a reader can never
// see half of it. The same lock is what makes the other promised partition,
// `released == peer reset + keepalive timeout`, hold while a release is being recorded - two atomic
// adds can be split by a reader, and the macOS gate did split them on 2026-10-27.
//
// `pending` at a runtime snapshot is normal - the probe runs on a timer, so at any instant some
// observations have not had their next read. At the shutdown summary it should be zero, because
// `ConnectionManager.Close` settles every live observation before printing: the flows were closed by
// the tunnel, not by a verdict, and "stopped" is the truthful outcome. A device reader should check
// the invariant and the scenario, not `released == observed` - a flow whose peer is still alive is a
// correct "observed but never released".
//
// # Skipped flows, which are NOT part of the invariant
//
// Two classes never become observations, and both are counted so a report cannot mistake them for
// coverage:
//
//	no probe capability   nothing in the connection chain can be asked to probe.
//	unobservable read     it can be asked, but a later read cannot report the verdict - a kernel
//	                      socket after its peer's FIN (measured; see conn_halfclose.go).
//
// Skipped flows are not `pending`: there is no observation to settle, and counting them as pending
// would break the invariant above.
//
// # What these numbers do and do not say
//
// They cannot say that a released flow was certainly dead in the user's sense: the release follows a
// socket error, which only the peer's own stack or the userspace stack's own probe countdown can
// produce, but "the transport decided the peer is unreachable" is not the same claim as "the user's
// application exited". A report must state it as the former.
//
// # Cost
//
// One atomic add per observation, per final state and per skipped flow. Never per probe, per byte or
// per connection that has not half-closed.
type halfCloseDiagnostics struct {
	// access makes the settle counters and `observed` ONE observation.
	//
	// `recordReleased` has to publish two facts - that the flow settled, and which verdict settled
	// it - and a reader could previously land between them: released=1 with reset=0 and timeout=0,
	// which violates `released == peer reset + keepalive timeout` that every snapshot promises. The
	// macOS gate caught exactly that window on 2026-10-27 (client-macos attempt 1, route
	// TestHalfCloseVerdictAndTeardownRaceSettleOnce); an atomic per counter cannot make a
	// multi-counter invariant hold mid-update. The lock is taken once per settled flow and once per
	// snapshot, never per probe or per byte.
	access              sync.Mutex
	observed            uint64
	released            uint64
	reset               uint64
	timeout             uint64
	stopped             uint64
	skippedUnarmable    atomic.Uint64
	skippedUnobservable atomic.Uint64
}

func (d *halfCloseDiagnostics) recordObserved() {
	d.access.Lock()
	d.observed++
	d.access.Unlock()
}

func (d *halfCloseDiagnostics) recordReleased(cause error) {
	d.access.Lock()
	if halfCloseCauseIsTimeout(cause) {
		d.timeout++
	} else {
		d.reset++
	}
	d.released++
	d.access.Unlock()
}

func (d *halfCloseDiagnostics) recordStopped() {
	d.access.Lock()
	d.stopped++
	d.access.Unlock()
}

func (d *halfCloseDiagnostics) recordSkippedUnarmable() {
	d.skippedUnarmable.Add(1)
}

func (d *halfCloseDiagnostics) recordSkippedUnobservable() {
	d.skippedUnobservable.Add(1)
}

// halfCloseCauseIsTimeout separates "the probe countdown ran out" from "the peer reset us".
//
// The distinction is the one an operator needs: a reset means the wire worked and the far end is
// gone, while a timeout means nothing came back at all, which on a phone usually means the path
// died. Both are terminal, and both release the flow.
func halfCloseCauseIsTimeout(cause error) bool {
	var timeout interface{ Timeout() bool }
	if errors.As(cause, &timeout) && timeout.Timeout() {
		return true
	}
	return errors.Is(cause, syscall.ETIMEDOUT)
}

// HalfCloseSnapshot is an immutable view of the half-close observation diagnostics.
type HalfCloseSnapshot struct {
	// Observed is the number of half-closed flows taken under observation.
	Observed uint64
	// Released is the part of Observed that ended with a peer-gone verdict.
	Released uint64
	// PeerReset is the part of Released where the peer answered a probe with a reset.
	PeerReset uint64
	// KeepaliveTimeout is the part of Released where nothing answered at all.
	KeepaliveTimeout uint64
	// Stopped is the part of Observed that ended without a verdict.
	Stopped uint64
	// Pending is the part of Observed that has no final state yet. Derived, never stored.
	Pending uint64
	// SkippedUnarmable is flows that could not be asked to probe at all.
	SkippedUnarmable uint64
	// SkippedUnobservable is flows that could be asked, but whose read side cannot report the answer.
	SkippedUnobservable uint64
}

// HalfCloseSummary is one line, written once per tunnel lifetime, in the same shape as the splice
// and transition summaries.
func (s HalfCloseSnapshot) HalfCloseSummary() string {
	return fmt.Sprintf(
		"half-closed flows: %d observed, released %d (peer reset %d, keepalive timeout %d), stopped %d, pending %d; skipped %d (no probe capability %d, unobservable read side %d)",
		s.Observed, s.Released, s.PeerReset, s.KeepaliveTimeout, s.Stopped, s.Pending,
		s.SkippedUnarmable+s.SkippedUnobservable, s.SkippedUnarmable, s.SkippedUnobservable)
}

// HalfCloseDiagnostics returns the current half-close observation diagnostics.
//
// Reading it takes the diagnostics lock once and has no effect on forwarding. The snapshot is one
// observation of the counters: a settle that is in flight is either before it or after it, so both
// promised partitions hold in the returned value.
func (m *ConnectionManager) HalfCloseDiagnostics() HalfCloseSnapshot {
	m.halfCloseDiag.access.Lock()
	released := m.halfCloseDiag.released
	peerReset := m.halfCloseDiag.reset
	keepaliveTimeout := m.halfCloseDiag.timeout
	stopped := m.halfCloseDiag.stopped
	observed := m.halfCloseDiag.observed
	m.halfCloseDiag.access.Unlock()
	pending := uint64(0)
	if settled := released + stopped; observed > settled {
		pending = observed - settled
	}
	return HalfCloseSnapshot{
		Observed:            observed,
		Released:            released,
		PeerReset:           peerReset,
		KeepaliveTimeout:    keepaliveTimeout,
		Stopped:             stopped,
		Pending:             pending,
		SkippedUnarmable:    m.halfCloseDiag.skippedUnarmable.Load(),
		SkippedUnobservable: m.halfCloseDiag.skippedUnobservable.Load(),
	}
}
