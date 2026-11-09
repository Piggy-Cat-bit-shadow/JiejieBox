package v2rayxhttp

import (
	E "github.com/sagernet/sing/common/exceptions"
)

// The Box-facing side of the packet-up budget.
//
// # Why an API at all
//
// A Client owns one budget (see uplink_budget.go), so a Box with N XHTTP outbounds has
// N pools, and N pools bound N times the memory: the audit's P1 named that as the
// remaining XHTTP gap after the per-Client share was fixed. Closing it is a BOX-level
// decision — which outbounds should share a bound, and what the bound is — so the
// wiring belongs to the Box assembly (box.go, integration window), not here. What this
// file provides is the minimum that assembly needs:
//
//   - one UploadBudget that several Clients can draw from;
//   - a way to size it like a Client's own pool, without duplicating the policy;
//   - a snapshot with the same numbers the diagnostics report;
//   - and a documented lifecycle: bind BEFORE dialing, one budget per Box, a replaced
//     Box gets its own budget so the old streams' late returns settle against the old
//     ledger only.
//
// # Limits, stated for the wiring
//
//   - A Client's per-stream queue bound (sc_max_each_post_bytes x2, clamped) stays in
//     force on every stream; the shared budget adds the aggregate bound across the
//     clients bound to it.
//   - The shared budget's capacity/control/reserve/allocation capacity become the BOX
//     aggregate; there is no per-client slice of it. A client with more streams can
//     therefore use more of the Box bound, which is the same first-come fairness the
//     per-Client pool has across streams.
//   - Bind must happen before the first DialContext. Binding a client that already has
//     live streams would leave those streams' charges in the old pool while they keep
//     running: the returned error is there to make that a caller bug, not a silent
//     split ledger.

// UploadBudgetSnapshot is a consistent view of an UploadBudget. It is the same
// accounting the per-Client diagnostics report, so a Box can log one line per budget
// and get the logical charge, the retained capacity and the defect counters together.
type UploadBudgetSnapshot = uplinkBudgetStats

// UploadBudget is a packet-up upload budget that one or more XHTTP Clients share.
//
// It is safe for concurrent use, and its zero value is not usable: create it with
// Client.NewUploadBudget or NewUploadBudgetFor.
type UploadBudget struct {
	pool *sharedUplinkBudget
}

// NewUploadBudget returns an empty budget sized exactly like this Client's own pool:
// same main capacity, control pool, reserve and allocation bound. Use it when a Box
// wants one bound across several XHTTP outbounds; bind the client that produced it
// too, or that client keeps its own pool.
func (c *Client) NewUploadBudget() *UploadBudget {
	if c == nil {
		return nil
	}
	source := c.budget
	if source == nil {
		return nil
	}
	stats := source.stats()
	return &UploadBudget{pool: newSharedUplinkBudgetWithControl(stats.Capacity, stats.Reserve, stats.Control)}
}

// NewUploadBudgetFor builds a budget from a per-stream queue bound, applying the same
// policy NewClient does. It exists for assemblies that know the transport settings but
// do not have a Client yet.
func NewUploadBudgetFor(perStreamBound int) *UploadBudget {
	return &UploadBudget{pool: newSharedUplinkBudget(packetUpSharedCapacity(perStreamBound), packetUpSharedReserve)}
}

// Bind makes client draw on b instead of its own pool. It must be called before the
// client's first dial; a client that already has live streams is refused, because its
// charges would stay in the old pool while its streams keep running.
func (b *UploadBudget) Bind(client *Client) error {
	if b == nil || b.pool == nil {
		return E.New("v2ray-xhttp: upload budget is not initialized")
	}
	if client == nil {
		return E.New("v2ray-xhttp: cannot bind a nil client")
	}
	if client.budget != nil && client.budget != b.pool && client.budget.stats().Streams > 0 {
		return E.New("v2ray-xhttp: client already has live streams on its own upload budget")
	}
	client.budget = b.pool
	return nil
}

// Snapshot returns the budget's current accounting.
func (b *UploadBudget) Snapshot() UploadBudgetSnapshot {
	if b == nil || b.pool == nil {
		return UploadBudgetSnapshot{}
	}
	return b.pool.stats()
}
