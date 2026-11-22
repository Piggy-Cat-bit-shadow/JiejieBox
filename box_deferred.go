// The deferred-construction seam, installed for the node kinds that can honour it.
//
// # Why this is on and not behind a switch
//
// A configuration that names ten thousand nodes and dials three pays for all ten thousand at Start.
// The deferred seam makes a node cost its shared rebuild record and a small descriptor until
// something asks for it, and the reclaim policy in adapter/outbound/reclaim.go bounds how many
// expansions may exist at once (64 by default), so a full url-test round cannot leave the whole
// library expanded. That combination is the feature: the seam without the reclaim policy, or the
// policy without the seam, is not something an operator should have to assemble.
//
// # Why only some nodes
//
// The seam is installed unconditionally, but the constructor it installs admits a node only when a
// protocol proves that its option SHAPE can be rebuilt from a static record without moving an error
// stage and without a construction-time resource. Today that is Trojan (the existing Lazy recipe),
// SOCKS and plain/standard-TLS HTTP. Everything else returns "not mine" and takes the eager path
// unchanged, including its error stage.
package box

import (
	"context"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/adapter/outbound/catalog"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/protocol/http"
	"github.com/sagernet/sing-box/protocol/trojan"
)

// withDeferredOutbounds installs the deferred-construction seam on this box's outbound manager.
//
// The store is the Box's shared record space: static nodes (owner "") and provider nodes (owner =
// provider tag) both use it, but a record is keyed by owner, so the two can never share mutable
// ownership. The manager owns the budget and the reclaim sweep; this function only says which
// constructor the manager may consult.
func withDeferredOutbounds(manager *outbound.Manager, store *catalog.Store) {
	catalogConstructor := catalog.NewConstructor(store,
		http.CatalogProtocol(),
	)
	// Trojan keeps its existing Lazy recipe: the measured B->C increment for a unique-node
	// Trojan corpus is negative, so it is not admitted to the record store; the existing Lazy
	// constructor stays installed as the fallback and the two paths cannot both claim a node.
	trojanConstructor := trojan.DeferredConstructor()
	manager.SetDeferredOutboundConstructor(func(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any, owner string) (adapter.Outbound, bool, error) {
		object, handled, err := catalogConstructor.Deferred(ctx, router, logger, tag, outboundType, options, owner)
		if handled || err != nil {
			return object, handled, err
		}
		return trojanConstructor(ctx, router, logger, tag, outboundType, options, owner)
	})
}

// DeferredExpansionCount reports how many deferred nodes of this box are expanded right now.
//
// It exists because the number is the difference between the feature working and the feature
// costing more than it saves: a configuration whose every node is expanded after a url-test round is
// the +42% case the prototype measured, and a count that stays under the budget is the bounded case.
// A diagnostic or a test asks this instead of inferring it from a heap profile.
func (s *Box) DeferredExpansionCount() int {
	if s == nil || s.outbound == nil {
		return 0
	}
	return s.outbound.DeferredExpansionCount()
}

// CatalogStats reports the Box's shared record space. It is a diagnostic for the same reason as
// DeferredExpansionCount: the record bytes and reference count are part of the net cost, not a
// detail to be inferred from a heap profile.
func (s *Box) CatalogStats() catalog.Stats {
	if s == nil {
		return catalog.Stats{}
	}
	return s.catalogStore.Stats()
}
