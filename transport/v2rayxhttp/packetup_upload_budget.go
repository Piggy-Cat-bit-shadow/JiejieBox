package v2rayxhttp

import (
	"context"

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
// wiring belongs to the Box assembly (box.go), not to the transport. What this file
// provides is the minimum that assembly needs:
//
//   - one UploadBudget that several Clients can draw from;
//   - a way to size it like a Client's own pool, without duplicating the policy;
//   - a context hook so the transport constructor (which the Box reaches only through
//     the v2ray transport registry) finds the Box's budget without changing the
//     registry ABI or the option structs;
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
//   - The FIRST XHTTP client constructed under a Box's context fixes the shared
//     budget's sizing (the pools are sized from sc_max_each_post_bytes). A Box whose
//     XHTTP outbounds are configured differently shares the first's aggregate; each
//     stream still keeps its own per-stream bound. Binding must happen before the
//     first DialContext: the transport does it at construction, which is before any
//     dial, and Bind refuses a client whose streams are already charged.

// UploadBudgetSnapshot is a consistent view of an UploadBudget. It is the same
// accounting the per-Client diagnostics report, so a Box can log one line per budget
// and get the logical charge, the retained capacity and the defect counters together.
type UploadBudgetSnapshot = uplinkBudgetStats

// UploadBudget is a packet-up upload budget that one or more XHTTP Clients share.
//
// It is safe for concurrent use, and its zero value is not usable: create it with
// Client.NewUploadBudget, NewUploadBudgetFor, or UploadBudgetSizing.NewBudget.
type UploadBudget struct {
	pool *sharedUplinkBudget
}

// UploadBudgetSizing is the pool sizing a client would use for its own budget: the main
// capacity, control pool, reserve and allocation bound. A Box that shares one budget
// across its XHTTP outbounds sizes it from the first client (see
// WithUploadBudgetProvider).
type UploadBudgetSizing struct {
	Capacity      int
	Control       int
	Reserve       int
	AllocCapacity int
}

// NewBudget builds a shared budget with exactly these pools.
func (s UploadBudgetSizing) NewBudget() *UploadBudget {
	if s.Capacity <= 0 {
		return nil
	}
	return &UploadBudget{pool: newSharedUplinkBudgetWithControl(s.Capacity, s.Reserve, s.Control)}
}

// UploadBudgetProvider hands out the Box's shared upload budget. The transport calls it
// once per XHTTP client, at construction; the provider returns the same budget for
// every client so one bound covers the Box, or nil to leave that client on its own pool.
type UploadBudgetProvider interface {
	UploadBudgetFor(sizing UploadBudgetSizing) *UploadBudget
}

// UploadBudgetProviderFunc adapts a function to UploadBudgetProvider.
type UploadBudgetProviderFunc func(UploadBudgetSizing) *UploadBudget

// UploadBudgetFor implements UploadBudgetProvider.
func (f UploadBudgetProviderFunc) UploadBudgetFor(sizing UploadBudgetSizing) *UploadBudget {
	return f(sizing)
}

type uploadBudgetProviderKey struct{}

// WithUploadBudgetProvider returns a context carrying the Box's upload-budget provider.
// Box assembly wraps the context it creates outbounds with; the XHTTP transport
// constructor reads it and binds the new client to the shared budget.
func WithUploadBudgetProvider(ctx context.Context, provider UploadBudgetProvider) context.Context {
	if provider == nil {
		return ctx
	}
	return context.WithValue(ctx, uploadBudgetProviderKey{}, provider)
}

// uploadBudgetFromContext returns the provider carried by ctx, if any.
func uploadBudgetFromContext(ctx context.Context) UploadBudgetProvider {
	if ctx == nil {
		return nil
	}
	provider, _ := ctx.Value(uploadBudgetProviderKey{}).(UploadBudgetProvider)
	return provider
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
	return UploadBudgetSizingFromStats(source.stats()).NewBudget()
}

// UploadBudgetSizingFromStats returns the sizing a snapshot describes, for a provider
// that has to construct the budget from a client's own numbers.
func UploadBudgetSizingFromStats(stats UploadBudgetSnapshot) UploadBudgetSizing {
	return UploadBudgetSizing{
		Capacity:      stats.Capacity,
		Control:       stats.Control,
		Reserve:       stats.Reserve,
		AllocCapacity: stats.AllocCapacity,
	}
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
	client.uploadBudget = b
	return nil
}

// UploadBudget returns the shared budget this client was bound to, or nil when it runs
// on its own pool. It is the identity a Box logs to prove two outbounds share one bound.
func (c *Client) UploadBudget() *UploadBudget {
	if c == nil {
		return nil
	}
	return c.uploadBudget
}

// Snapshot returns the budget's current accounting.
func (b *UploadBudget) Snapshot() UploadBudgetSnapshot {
	if b == nil || b.pool == nil {
		return UploadBudgetSnapshot{}
	}
	return b.pool.stats()
}
