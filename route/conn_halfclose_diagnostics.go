package route

import (
	"errors"
	"fmt"
	"sync/atomic"
	"syscall"
)

// Half-close observation diagnostics.
//
// # What this is for
//
// The half-close observer (conn_halfclose.go) claims that a flow whose peer has gone is released
// within a minute or so of that happening, instead of being held until close-all. A claim like that
// is only worth something if a real-device run can show it firing, and the device cannot be
// instrumented for the purpose. So the mechanism counts itself and reports ONE line when the tunnel
// stops, exactly like the splice and transition summaries next to it.
//
// A phone run therefore needs no instrumentation at all: start the tunnel, use the apps, close them,
// let the phone sit, stop the tunnel, read one line.
//
// # What these numbers do and do not say
//
// `observed` is how many half-closed flows were taken under observation. `released` is how many were
// then released because their finished side reported a terminal error, split by what that error was:
// a reset (the peer's stack answered a probe with RST) or a keepalive timeout (nothing answered).
// `stopped` is observations that ended without a verdict - normally because the surviving direction
// ended the flow first, which is the ordinary, healthy path and is why it is not an error.
//
// They cannot say that a released flow was certainly dead: the release follows a socket error, which
// only the peer's own stack or the userspace stack's own probe countdown can produce, but "the stack
// said the peer is gone" is not the same claim as "the user's application was closed". A report must
// state it as the former.
//
// # Cost
//
// One atomic add per observation, per release and per verdict-less stop. Never per probe, per byte or
// per connection that has not half-closed.
type halfCloseDiagnostics struct {
	observed atomic.Uint64
	released atomic.Uint64
	reset    atomic.Uint64
	timeout  atomic.Uint64
	stopped  atomic.Uint64
}

func (d *halfCloseDiagnostics) recordObserved() {
	d.observed.Add(1)
}

func (d *halfCloseDiagnostics) recordReleased(cause error) {
	d.released.Add(1)
	if halfCloseCauseIsTimeout(cause) {
		d.timeout.Add(1)
	} else {
		d.reset.Add(1)
	}
}

func (d *halfCloseDiagnostics) recordStopped() {
	d.stopped.Add(1)
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
	// Released is the number of those released because the finished side reported a terminal error.
	Released uint64
	// PeerReset is the part of Released where the peer answered a probe with a reset.
	PeerReset uint64
	// KeepaliveTimeout is the part of Released where nothing answered at all.
	KeepaliveTimeout uint64
	// Stopped is the number of observations that ended without a verdict - normally because the
	// surviving direction ended the flow first.
	Stopped uint64
}

// HalfCloseSummary is one line, written once per tunnel lifetime, in the same shape as the splice
// and transition summaries.
func (s HalfCloseSnapshot) HalfCloseSummary() string {
	return fmt.Sprintf(
		"half-closed flows: %d observed, released %d (peer reset %d, keepalive timeout %d), stopped without a verdict %d",
		s.Observed, s.Released, s.PeerReset, s.KeepaliveTimeout, s.Stopped)
}

// HalfCloseDiagnostics returns the current half-close observation diagnostics.
//
// Reading it is a handful of atomic loads and has no effect on forwarding.
func (m *ConnectionManager) HalfCloseDiagnostics() HalfCloseSnapshot {
	return HalfCloseSnapshot{
		Observed:         m.halfCloseDiag.observed.Load(),
		Released:         m.halfCloseDiag.released.Load(),
		PeerReset:        m.halfCloseDiag.reset.Load(),
		KeepaliveTimeout: m.halfCloseDiag.timeout.Load(),
		Stopped:          m.halfCloseDiag.stopped.Load(),
	}
}
