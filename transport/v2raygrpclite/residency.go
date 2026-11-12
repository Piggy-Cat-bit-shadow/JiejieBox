package v2raygrpclite

// The transport's object-identity ledger (audit Q15), the same shape as protocol/v2rayhttp's.
//
// One object is one pool GENERATION: the `http2.Transport` the gRPC streams are handed out on. The
// generation is Active while it carries a stream, Idle while it carries none, Draining once a network
// change retired it while a stream was still running - and it is NOT subtracted at that point, which
// is the accounting error Draining exists to prevent - and Terminated only when the transport was
// really closed.
//
// Streams are not counted: a gRPC stream lives inside one generation and is that generation's
// Active/Idle distinction, not a second resident object.

import (
	"github.com/sagernet/sing-box/adapter"
)

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
		ledger.Note = "no pool was constructed; this transport cannot answer"
		ledger.Answered = false
		ledger.Exact = false
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
	ledger.Note = "pooled HTTP/2 generations (unit=generation); gRPC streams are the Active/Idle " +
		"distinction inside one generation and are not separate objects; a retired generation is " +
		"Draining until its last stream returns and only then counted in Terminated"
	return ledger
}
