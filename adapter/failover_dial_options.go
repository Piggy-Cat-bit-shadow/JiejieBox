package adapter

import (
	"context"
	"net/netip"
	"time"

	C "github.com/sagernet/sing-box/constant"
)

// FailoverDialOptions is the per-flow dial configuration a failover group must carry to the member it
// selects for this attempt.
//
// # Why it has to travel at all
//
// `routeDialer` wraps the leaf outbound in a shim so the GROUP owns the failover decision, and that shim
// embeds the narrow `Outbound` interface. A typed assertion on an embedded interface promotes only the
// methods that interface declares, so the shim hides `dialer.ParallelNetworkDialer` from
// `dialer.DialSerialNetwork`; that function then falls back to a plain serial loop, which calls
// `DialContext(ctx, network, destination)` - a signature with no parameter for any of these four fields.
//
// They arrive from the matching rule's `route_options`, so without this carrier a rule that steers
// connections onto a specific interface, or that sets a fallback cadence, governs a flow whose outbound is
// the leaf and does not govern the same flow when that outbound is a `failover: true` group. The packet
// path keeps them either way, because it passes the leaf rather than the shim - the same asymmetry the
// connected-UDP defect had, with the protocols swapped.
//
// # Why the context and not a wider interface
//
// The group's retry is recursive (a member may itself be a failover group), and the flow's attempt budget is
// already threaded through the context for exactly that reason - see `protocol/group`'s failoverBudget. A
// per-flow value that every nested level must see, and that no level may drop, is the same problem, so it
// uses the same mechanism. An extra interface would also have to be implemented by every group, and the
// shim would still need a fallback for groups that predate it.
//
// A zero value means "the caller supplied none", which every consumer treats as "apply nothing" - the
// behaviour before this carrier existed. That keeps the change additive: a dialer that never sets it, and a
// leaf that cannot use it, both behave exactly as they did.
type FailoverDialOptions struct {
	// DestinationAddresses is the candidate list the flow was given, so a member that can schedule across
	// candidates does so instead of dialling only the first address.
	DestinationAddresses []netip.Addr
	// NetworkStrategy is the per-connection interface strategy from `route_options`.
	NetworkStrategy *C.NetworkStrategy
	// NetworkType and FallbackNetworkType are the per-connection interface selections.
	NetworkType         []C.InterfaceType
	FallbackNetworkType []C.InterfaceType
	// FallbackDelay is the per-connection cadence between candidate attempts.
	FallbackDelay time.Duration
}

// IsZero reports whether no option was supplied at all.
func (o FailoverDialOptions) IsZero() bool {
	return len(o.DestinationAddresses) == 0 && o.NetworkStrategy == nil && len(o.NetworkType) == 0 &&
		len(o.FallbackNetworkType) == 0 && o.FallbackDelay == 0
}

type failoverDialOptionsKey struct{}

// ContextWithFailoverDialOptions attaches the per-flow dial configuration for a failover attempt.
func ContextWithFailoverDialOptions(ctx context.Context, options FailoverDialOptions) context.Context {
	if options.IsZero() {
		// Nothing to carry: leaving the context untouched keeps the fast path allocation-free and makes
		// "no options" indistinguishable from "no support", which is the correct reading for every
		// consumer.
		return ctx
	}
	return context.WithValue(ctx, failoverDialOptionsKey{}, options)
}

// FailoverDialOptionsFromContext returns the per-flow dial configuration, or the zero value when the caller
// supplied none.
func FailoverDialOptionsFromContext(ctx context.Context) FailoverDialOptions {
	options, _ := ctx.Value(failoverDialOptionsKey{}).(FailoverDialOptions)
	return options
}
