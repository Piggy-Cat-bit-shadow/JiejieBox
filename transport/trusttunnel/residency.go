package trusttunnel

// The client's session-identity ledger (audit Q15).
//
// # What the object is
//
// The resident object of this transport is one client session: the HTTP/2 or QUIC connection the
// shared offer publishes. Streams are not separate objects here; they are the session's Active/Idle
// distinction, the same rule transport/v2rayquic uses for its shared QUIC connection. A session is
// Creating while the shared offer is in flight, Active while it carries at least one stream, Idle
// while it carries none, Draining once RetireSuspect removed it while streams were still finishing,
// and Terminated when the session's close body actually ran.
//
// # Why the counters are the state machine's own
//
// Draining is incremented where the session is detached and decremented where it closes, so it can
// never be a second opinion about whether the session is still held. Terminated is the session's
// lifetime release count, not a state. Exact is true because every number is a count of sessions.

import (
	"sync/atomic"

	"github.com/sagernet/sing-box/adapter"
)

type clientResidency struct {
	draining   atomic.Int64
	terminated atomic.Uint64
}

// noteRetired records that a session was detached for the new generation and is draining.
func (r *clientResidency) noteRetired() {
	r.draining.Add(1)
}

// noteReleased records one completed session release; a draining session leaves the draining count.
func (r *clientResidency) noteReleased(wasDraining bool) {
	if wasDraining {
		r.draining.Add(-1)
	}
	r.terminated.Add(1)
}

// ResidencyLedger implements adapter.ResidencyLedgerReporter.
func (c *Client) ResidencyLedger() adapter.ResidencyLedger {
	if c == nil {
		return adapter.ResidencyLedger{}
	}
	c.access.Lock()
	pending := c.pending != nil
	session := c.session
	c.access.Unlock()

	var ledger adapter.ResidencyLedger
	if pending {
		ledger.Creating = 1
	}
	if session != nil {
		if session.streams.Load() > 0 {
			ledger.Active = 1
		} else {
			ledger.Idle = 1
		}
	}
	if draining := c.residency.draining.Load(); draining > 0 {
		ledger.Draining = uint64(draining)
	}
	ledger.Terminated = c.residency.terminated.Load()
	ledger.Exact = true
	ledger.Answered = true
	ledger.Note = "client sessions of this transport; streams are the Active/Idle distinction " +
		"inside one session, not separate objects"
	return ledger
}
