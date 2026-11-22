package catalog

import (
	"context"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
)

// Facts are the static facts the descriptor must answer without expanding the node.
type Facts struct {
	Network        []string
	Dependencies   []string
	ServerIsDomain bool
	Multiplex      bool
}

// Protocol is one admitting protocol. Encode decides whether a shape is covered and turns the
// normalized options into the rebuild payload; Build reconstructs the runtime by calling the SAME
// constructor the eager path uses.
type Protocol interface {
	Type() string
	Encode(ctx context.Context, options any) (payload []byte, facts Facts, ok bool)
	Build(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, payload []byte) (adapter.Outbound, error)
}

// Handle is the LazyOutbound recipe: a reference into the shared store, not the node's recipe.
type Handle struct {
	ref Ref
}

// Retain returns a handle whose reference the caller must release.
func (h Handle) Retain() Handle {
	return Handle{ref: h.ref.Retain()}
}

func (h Handle) Release() {
	h.ref.Release()
}

func (h Handle) Payload() []byte {
	return h.ref.Payload()
}

func (h Handle) Protocol() string {
	return h.ref.Protocol()
}

// Constructor is the outbound manager's deferred seam. It owns no lifecycle: descriptors are closed
// by the manager's existing Scope, and the Box owns the store.
type Constructor struct {
	store     *Store
	protocols map[string]Protocol
}

// NewConstructor builds the seam from the Box store and the admitted protocols.
func NewConstructor(store *Store, protocols ...Protocol) *Constructor {
	byType := make(map[string]Protocol, len(protocols))
	for _, protocol := range protocols {
		byType[protocol.Type()] = protocol
	}
	return &Constructor{store: store, protocols: byType}
}

// Deferred implements outbound.DeferredOutboundConstructor. A shape that is not admitted returns
// handled=false, and the manager's eager path keeps the node, including its error stage.
func (c *Constructor) Deferred(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any, owner string) (adapter.Outbound, bool, error) {
	if c == nil || c.store == nil {
		return nil, false, nil
	}
	protocol, admitted := c.protocols[outboundType]
	if !admitted {
		return nil, false, nil
	}
	payload, facts, covered := protocol.Encode(ctx, options)
	if !covered || len(payload) == 0 {
		return nil, false, nil
	}
	ref, err := c.store.Put(owner, outboundType, payload)
	if err != nil {
		// A closed store or an empty record means "no catalog for this node": the eager path keeps
		// the node rather than turning a storage refusal into a configuration error.
		return nil, false, nil
	}
	handle := Handle{ref: ref}
	// An admitted shape has no detour/resolver dependency (eligibility excludes them), so an
	// empty dialer options value is exact here. The ServerIsDomain fact MUST be published: the
	// cross-kind cycle and static validation read it, and a false answer hides a resolver edge.
	ownerAdapter := outbound.NewAdapterWithDialerOptions(outboundType, tag, facts.Network, option.DialerOptions{}, facts.ServerIsDomain)
	deferred := outbound.NewLazyOutbound[Handle](ownerAdapter, logger, ctx, handle,
		func(buildContext context.Context, buildHandle Handle) (adapter.Outbound, error) {
			// A build can outlive the descriptor's Close (Close cancels but does not wait for an
			// in-flight build), so the builder owns a reference for as long as it touches bytes.
			retained := buildHandle.Retain()
			defer retained.Release()
			if retained.Protocol() != outboundType {
				return nil, ErrWrongProtocol
			}
			return protocol.Build(buildContext, router, logger, tag, retained.Payload())
		})
	deferred.SetExpansionEstimate(expansionEstimate)
	return &descriptor{LazyOutbound: deferred, ref: ref, multiplex: facts.Multiplex}, true, nil
}

// ErrWrongProtocol is returned if a retained handle ever names another protocol. It cannot happen
// through the constructor (the key includes the protocol); it is a fail-closed check on the decode.
var ErrWrongProtocol = errWrongProtocol{}

type errWrongProtocol struct{}

func (errWrongProtocol) Error() string { return "node catalog: record is for another protocol" }

const expansionEstimate = 3 * 1024

var (
	_ adapter.Outbound                = (*descriptor)(nil)
	_ adapter.Lifecycle               = (*descriptor)(nil)
	_ adapter.OutboundWithMultiplex   = (*descriptor)(nil)
	_ adapter.InterfaceUpdateListener = (*descriptor)(nil)
	_ adapter.IdleConnectionKeeper    = (*descriptor)(nil)
	_ adapter.ResidencyLedgerReporter = (*descriptor)(nil)
)

// descriptor is the stable logical node. Type/Tag/Network/Start/DialContext come from the existing
// LazyOutbound; this wrapper adds the record release and the optional interfaces the manager and
// the route layer consult WITHOUT expanding the node.
type descriptor struct {
	*outbound.LazyOutbound[Handle]
	ref       Ref
	once      sync.Once
	multiplex bool
}

// Start runs the embedded lifecycle AND registers the descriptor's own Close on the same scope.
// The embedded LazyOutbound registers only its own Close; the manager releases a component by
// closing its scope, so without this the record reference would never be released in production
// (the unit tests that call Close directly would not see it). The two cleanups are idempotent and
// run in reverse registration order: the descriptor releases the record, then the Lazy close is a
// no-op.
func (d *descriptor) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if err := d.LazyOutbound.Start(stage, scope); err != nil {
		return err
	}
	if stage == adapter.StartStateInitialize && scope != nil {
		scope.AddNamed("outbound/catalog["+d.Tag()+"]", d.Close)
	}
	return nil
}

// Close releases the descriptor's record reference after the state machine has stopped publishing.
// A build in flight keeps its own retained reference, so freeing the record here is safe.
func (d *descriptor) Close() error {
	err := d.LazyOutbound.Close()
	d.once.Do(func() { d.ref.Release() })
	return err
}

func (d *descriptor) MultiplexEnabled() bool {
	return d.multiplex
}

func (d *descriptor) InterfaceUpdated(ctx context.Context) {
	d.WithRuntime(func(runtime adapter.Outbound) {
		if listener, ok := runtime.(adapter.InterfaceUpdateListener); ok {
			listener.InterfaceUpdated(ctx)
		}
	})
}

func (d *descriptor) SetKeepIdleConnections(keep bool) {
	d.SetIdleConnectionsPolicy(keep)
	d.WithRuntime(func(runtime adapter.Outbound) {
		if keeper, ok := runtime.(adapter.IdleConnectionKeeper); ok {
			keeper.SetKeepIdleConnections(keep)
		}
	})
}

func (d *descriptor) CloseIdleConnections() {
	d.WithRuntime(func(runtime adapter.Outbound) {
		if keeper, ok := runtime.(adapter.IdleConnectionKeeper); ok {
			keeper.CloseIdleConnections()
		}
	})
	d.NotifyIdleWalk()
}

func (d *descriptor) ResidencyLedger() adapter.ResidencyLedger {
	var ledger adapter.ResidencyLedger
	expanded := d.WithRuntime(func(runtime adapter.Outbound) {
		if reporter, ok := runtime.(adapter.ResidencyLedgerReporter); ok {
			ledger = reporter.ResidencyLedger()
			return
		}
		ledger = adapter.ResidencyLedger{Answered: false, Note: "the expanded outbound exposes no object ledger"}
	})
	if expanded {
		return ledger
	}
	return adapter.ResidencyLedger{Answered: false, Note: "catalog node: not expanded, so it owns no pooled object to report"}
}
