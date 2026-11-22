// Package batch defines the request shape of one owner-managed BATCH publication, and the optional
// capability an outbound manager exposes to serve it.
//
// # Why this is its own package
//
// The provider (the caller) and the outbound manager (the implementation) are separate adapter
// packages, and the provider must be able to ask "do you support a batch?" WITHOUT importing the
// manager implementation. A structural interface plus one shared request type in a leaf package
// keeps the dependency pointing at the contract instead of at the concrete manager: the manager
// imports this package to implement the method, the provider imports it to type-assert the
// capability and to describe the request, and neither imports the other.
//
// # Why the request carries per-item context and logger
//
// The per-node path constructs each replacement with a context that names the node
// (adapter.InboundContext{Outbound: tag}) and a logger named after the node's kind and tag. A batch
// that silently used one context and one logger for every node would change what the constructors
// observe, so both travel with the item. Nil means "use the batch-level value", which keeps a test
// or an embedder from having to supply them.
package batch

import (
	"context"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
)

// Replacement is one tag's prospective replacement in a batch publication.
type Replacement struct {
	// Tag is the outbound tag to publish. It must be non-empty.
	Tag string
	// Type is the registered outbound type name (the constructor registry key).
	Type string
	// Options is the decoded option value handed to that constructor.
	Options any
	// Context is the construction context for THIS item, normally the provider's per-node context.
	// Nil means the batch context.
	Context context.Context
	// Logger is the constructor's logger for THIS item. Nil means the batch logger.
	Logger log.ContextLogger
}

// OwnedReplacer is implemented by an outbound manager that can prepare, validate and publish many
// owner-managed replacements as one transaction.
//
// # The contract, precisely
//
// The returned slice is aligned with items: outcomes[i] is nil when items[i] was published, and is
// the error that kept it out of the graph otherwise. A single item's construction or Start failure
// does not stop the others; that is the same per-node contract a sequence of ReplaceOwned calls has.
// A VALIDATION failure is all-or-nothing: no item of the failing set is published, every object
// built for it is released, and the previous objects stay exactly as they were.
//
// The owner label has the same meaning as ReplaceOwned's: replaced objects are parked as retired and
// are closed only after CompletePublication(owner, tags) says the owner finished its callback
// round. A whole batch emits at most one graph-update notification, after its last publication.
type OwnedReplacer interface {
	ReplaceOwnedBatch(ctx context.Context, router adapter.Router, logger log.ContextLogger, items []Replacement, owner string) []error
}
