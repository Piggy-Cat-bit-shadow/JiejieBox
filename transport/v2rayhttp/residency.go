package v2rayhttp

// The transport's object-identity ledger (audit Q15).
//
// # What the resident object is
//
// This transport is a POOL, so one object is one pool GENERATION: the `http2.Transport` the pool hands
// streams out on. The generation is in exactly one of the four states at every moment:
//
//	Active    the current generation holds at least one request.
//	Idle      the current generation holds none - it is what the retirement walks may reclaim.
//	Draining  the generation was retired because the network moved and it still carries a request.
//	          It is NOT released: subtracting it on retirement is the specific error Draining exists
//	          to prevent, and the pool keeps it until its last request returns.
//
// Creating is always zero here, and that is a fact about the object rather than an omission: a
// generation's transport is constructed by Retire before it becomes reachable, so it is never
// observable in a half-built state. Terminated is the count of completed releases, and it is
// incremented where the transport is really closed.
//
// # Why streams are not counted
//
// A stream lives inside one generation and is that generation's Active/Idle distinction, the same
// rule transport/v2rayquic applies to its shared QUIC connection. Counting streams as resident objects
// as well would add a stream to a generation, which is the "stream/session/device in one physical
// connection count" the audit forbids.
//
// # Why the ledger hooks nothing
//
// Every number is read from the pool's own state under its own lock (see GenerationPool.Snapshot), so
// the ledger cannot drift from the state machine and cannot hide an optional interface on a conn: no
// conn is wrapped at all. HTTP/1 has no pooled object - every demand opens its own socket - and the
// ledger says so instead of reporting a precise zero for a pool it does not have.

import (
	"github.com/sagernet/sing-box/adapter"
)

// ResidencyLedger implements adapter.ResidencyLedgerReporter.
func (c *Client) ResidencyLedger() adapter.ResidencyLedger {
	if c == nil {
		return adapter.ResidencyLedger{}
	}
	ledger := adapter.ResidencyLedger{
		Unit:     adapter.ResidencyUnitGeneration,
		Answered: true,
		Exact:    true,
	}
	if c.pool == nil {
		// HTTP/1: there is no pooled connection to lend, so this owner holds no generation object.
		// It is an answered zero OF ITS OWN SHAPE, not an unknown: the transport really holds
		// nothing between demands.
		ledger.Note = "HTTP/1 transport: no pooled connection is held between demands, so this owner " +
			"has no generation object (unit=generation)"
		return ledger
	}
	snapshot := c.pool.Snapshot()
	ledger.Generation = snapshot.Sequence
	ledger.RetiredGenerations = snapshot.RetiredDraining
	ledger.Draining = uint64(snapshot.RetiredDraining)
	ledger.Terminated = snapshot.Terminated
	if snapshot.CurrentInflight > 0 {
		ledger.Active = 1
	} else {
		ledger.Idle = 1
	}
	ledger.Note = "pooled HTTP/2 generations (unit=generation); streams are the Active/Idle " +
		"distinction inside one generation and are not separate objects; a retired generation is " +
		"Draining until its last request returns and only then counted in Terminated"
	return ledger
}
