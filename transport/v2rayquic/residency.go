//go:build with_quic

package v2rayquic

// The pool's object-identity ledger (audit Q15).
//
// # Why this transport is the first owner to answer
//
// A residency ledger needs objects with a real identity and a real lifecycle. This transport has
// exactly one resident object per generation - the shared QUIC connection - and its states are
// already distinct in the code: an offer in flight is CREATING, a published connection with streams
// is ACTIVE, a published connection with none is IDLE, a generation detached because the network
// moved is DRAINING until its last stream ends, and closeConn is the one place a release completes.
// Reporting them costs a few atomics and cannot drift from the state machine, because each number
// is read from the same fields the state machine acts on.
//
// # What it deliberately does not count
//
// Streams are not residency units at this layer: they live inside one connection and are the
// connection's Active/Idle distinction, not separate objects. Packets, buffers and goroutine stacks
// are not counted either - they are not objects with an owner here. The ledger therefore says what
// it counts in Note, and a reader must not read "two objects" as "two connections' worth of memory".
//
// # Exact, not estimated
//
// Every number is a count of real objects, so Exact is true. Only the TERMINATED total is cumulative
// (it is the owner's lifetime release count and is not part of Live()).

import (
	"sync/atomic"

	"github.com/sagernet/sing-box/adapter"
)

// clientResidency is the shared counter set every connection of one client reports into.
type clientResidency struct {
	draining   atomic.Int64
	terminated atomic.Uint64
}

// noteDraining records that a connection left the pool and is waiting for its last stream.
func (r *clientResidency) noteDraining() {
	if r != nil {
		r.draining.Add(1)
	}
}

// noteReleased records one completed physical release.
//
// A released connection that was draining leaves the draining count here, which is what keeps
// Live() equal to "objects the owner still holds": the draining state is a count of objects, not a
// graveyard total, and a release is reported in Terminated instead.
func (r *clientResidency) noteReleased(wasDraining bool) {
	if r == nil {
		return
	}
	if wasDraining {
		r.draining.Add(-1)
	}
	r.terminated.Add(1)
}

// ResidencyLedger implements adapter.ResidencyLedgerReporter.
//
// It takes the connection pool's lock for the two fields that live under it and each connection's
// own lock for the stream count, in that order only - the same order every other path in this file
// uses (connAccess -> conn.access), so the report cannot deadlock against a dial or a retirement.
func (c *Client) ResidencyLedger() adapter.ResidencyLedger {
	if c == nil {
		return adapter.ResidencyLedger{}
	}
	c.connAccess.Lock()
	pending := c.pending
	conn := c.conn.Load()
	c.connAccess.Unlock()

	var ledger adapter.ResidencyLedger
	ledger.Unit = adapter.ResidencyUnitConnection
	if pending != nil {
		ledger.Creating = 1
	}
	if conn != nil {
		conn.access.Lock()
		streams := conn.streams
		closed := conn.closed
		conn.access.Unlock()
		if !closed {
			if streams > 0 {
				ledger.Active = 1
			} else {
				ledger.Idle = 1
			}
		}
	}
	if draining := c.residency.draining.Load(); draining > 0 {
		ledger.Draining = uint64(draining)
		// Exactly one shared connection per generation, so the draining connections ARE the retired
		// generations still holding a stream. Reporting them separately is what makes the
		// cross-generation account readable (audit Q15): a retired generation is not a number that
		// disappeared, it is an object that has not been released yet.
		ledger.RetiredGenerations = int(draining)
	}
	ledger.Terminated = c.residency.terminated.Load()
	ledger.Exact = true
	ledger.Answered = true
	ledger.Note = "shared QUIC connections of this transport (unit=connection); streams are the " +
		"Active/Idle distinction inside one connection, not separate objects; a retired generation " +
		"is Draining until its last stream ends and only then counted in Terminated"
	return ledger
}
