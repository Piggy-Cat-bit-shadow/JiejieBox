package outbound

import (
	"context"
	"os"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound/batch"
	"github.com/sagernet/sing-box/log"
)

// ReplaceOwnedBatch publishes many owner-managed replacements with one graph validation, one table
// swap and one graph-update notification.
//
// # Why a batch is not just N ReplaceOwned calls
//
// Every ReplaceOwned validates the whole prospective graph before it swaps: it copies the node
// table, rebuilds the edge set (a provider-fed group copies its whole membership), walks the
// acyclic check and the cross-kind check, and only then publishes. For a subscription refresh that
// replaces K nodes that is K whole-graph walks on a graph of N nodes, i.e. O(K*(N+E)) allocations
// that the final graph never needed - measured on the 1000-node provider fixture as roughly 900 MB
// of cumulative allocation for one all-nodes-changed refresh. Nothing about the VERDICT needs to be
// recomputed per node: the graph is validated once, against the same union of declared edges and
// published membership, with every replacement of the batch applied at once.
//
// # Why validation happens before any start, and why that is sound here
//
// A new object's edges are read from the object itself: Dependencies() is fixed at construction,
// while a group's published membership only exists after Start. Validating before Start would miss
// a new group's membership edges. The batch therefore takes only items whose constructed object
// carries NO graph edges at all (no declared dependencies, not a group, no references, no resolver
// edge). A node with no outgoing edges cannot close a cycle: it can only be an edge TARGET, and the
// tags that target it are governed by memberships the batch does not change. Any item that does
// carry edges is handled by the exact per-node prepare-then-swap path, in the caller's dependency
// order, while the edge-free items around it are batched. That keeps the cycle check (and the
// cross-kind check) running on every mutation - it is not deleted, it is run once for a set whose
// verdict cannot differ per member.
//
// # Failure and retirement semantics
//
//   - A construction or Start failure affects only its item; the rest of the batch proceeds, and
//     the failed item's previous object stays published.
//   - A validation failure publishes NOTHING: every object built for that set is released and the
//     previous objects, records and default selection are untouched.
//   - Replaced objects are parked as RETIRED with the owner, exactly like ReplaceOwned; the caller
//     ends the wait with CompletePublication after its group callbacks.
//   - The default outbound follows the same rule per item as ReplaceOwned.
//   - The caller holds m.mutation for the whole batch, so the lock order throughout is
//     mutation -> graphAccess -> access; construction and Start run outside access, and the
//     returned close list is released after every lock is dropped.
func (m *Manager) ReplaceOwnedBatch(ctx context.Context, router adapter.Router, logger log.ContextLogger, items []batch.Replacement, owner string) []error {
	outcomes := make([]error, len(items))
	if owner == "" {
		for index := range outcomes {
			outcomes[index] = os.ErrInvalid
		}
		return outcomes
	}
	m.mutation.Lock()
	defer m.mutation.Unlock()

	type preparedReplacement struct {
		index  int
		item   batch.Replacement
		object adapter.Outbound
	}
	var pending []preparedReplacement
	var deferredClose []adapter.Outbound
	published := false

	// flush starts and publishes every edge-free replacement collected so far as one transaction.
	flush := func() {
		if len(pending) == 0 {
			return
		}
		ready := make([]preparedReplacement, 0, len(pending))
		for _, prepared := range pending {
			if err := m.startReplacement(prepared.object, prepared.item.Tag); err != nil {
				// A failed Start is that item's failure only. The object registered its partial
				// start through the lifecycle group, which m.startReplacement already drained;
				// publishing it is what must not happen.
				outcomes[prepared.index] = err
				continue
			}
			ready = append(ready, prepared)
		}
		pending = pending[:0]
		if len(ready) == 0 {
			return
		}
		proposal := graphProposal{replace: make(map[string]adapter.Outbound, len(ready))}
		for _, prepared := range ready {
			proposal.replace[prepared.item.Tag] = prepared.object
		}
		m.graphAccess.Lock()
		if err := m.validateGraphLocked(proposal); err != nil {
			// All-or-nothing: the verdict is about the union of the replacements, so publishing a
			// subset would publish a graph that was never validated. Every built object is released
			// and every previous object stays published.
			m.graphAccess.Unlock()
			for _, prepared := range ready {
				outcomes[prepared.index] = err
				m.release(prepared.object)
			}
			return
		}
		m.access.Lock()
		replacements := make(map[string]adapter.Outbound, len(ready))
		order := make([]string, 0, len(ready))
		previous := make(map[string]adapter.Outbound, len(ready))
		for _, prepared := range ready {
			tag := prepared.item.Tag
			if old := m.outboundByTag[tag]; old != nil {
				previous[tag] = old
			}
			m.outboundByTag[tag] = prepared.object
			m.ownedTags[tag] = owner
			replacements[tag] = prepared.object
			order = append(order, tag)
		}
		// ONE copy-on-write publication of the slice: the Outbounds contract reserves the slice for
		// readers, so the batch substitutes every element in a single pass instead of one O(N) copy
		// per node.
		m.outbounds = replaceOutbounds(m.outbounds, replacements, order)
		for _, prepared := range ready {
			tag := prepared.item.Tag
			old := previous[tag]
			if old != nil {
				m.retired = append(m.retired, &retiredOutbound{outbound: old, tag: tag, owner: owner})
			}
			if old != nil && m.defaultOutbound == old {
				m.defaultOutbound = prepared.object
			} else if tag == m.defaultTag || (m.defaultTag == "" && m.defaultOutbound == nil) {
				m.defaultOutbound = prepared.object
			}
		}
		deferredClose = append(deferredClose, m.takeSweepableRetiredLocked()...)
		m.access.Unlock()
		m.graphAccess.Unlock()
		published = true
	}

	for index, item := range items {
		if item.Tag == "" {
			outcomes[index] = os.ErrInvalid
			continue
		}
		itemContext := item.Context
		if itemContext == nil {
			itemContext = ctx
		}
		if err := itemContext.Err(); err != nil {
			// The provider cancels its context when it closes; a cancelled batch constructs nothing.
			outcomes[index] = err
			continue
		}
		itemLogger := item.Logger
		if itemLogger == nil {
			itemLogger = logger
		}
		object, err := m.constructOutbound(itemContext, router, itemLogger, item.Tag, item.Type, item.Options, owner)
		if err != nil {
			outcomes[index] = err
			continue
		}
		if !m.replacementHasGraphEdges(object) {
			pending = append(pending, preparedReplacement{index: index, item: item, object: object})
			continue
		}
		// An edge-bearing replacement must be started before the graph can describe it, so it takes
		// the exact per-node path. Flushing the edge-free set first preserves the caller's
		// dependency order: a group that names one of those tags is started after it is published.
		flush()
		if err = m.startReplacement(object, item.Tag); err != nil {
			outcomes[index] = err
			continue
		}
		closeNow, sweep, err := m.commitReplacement(item.Tag, object, owner)
		if err != nil {
			outcomes[index] = err
			m.release(object)
			continue
		}
		deferredClose = append(deferredClose, closeNow...)
		deferredClose = append(deferredClose, sweep...)
		published = true
	}
	flush()
	for _, object := range deferredClose {
		m.release(object)
	}
	if published {
		// One notification for the batch: the hooks coalesce a burst anyway, and a consumer's
		// answer is a full re-read, so per-item emissions would only mean identical re-reads.
		m.notifyGraphUpdated()
	}
	return outcomes
}

// replacementHasGraphEdges reports whether a constructed replacement can have an outgoing edge in
// either graph the manager validates.
//
// It is deliberately conservative: anything it cannot prove edge-free (a group whose membership is
// only published at Start, a Referrer, a node with an explicit resolver, a node that would read the
// network default resolver) is handed to the per-node path, which validates after Start. Only a
// leaf - a plain protocol outbound with no dependencies and no resolver - is batched.
func (m *Manager) replacementHasGraphEdges(object adapter.Outbound) bool {
	if object == nil {
		return true
	}
	if len(object.Dependencies()) > 0 {
		return true
	}
	if _, isGroup := object.(adapter.OutboundGroup); isGroup {
		return true
	}
	if _, isReferrer := object.(adapter.Referrer); isReferrer {
		return true
	}
	if resolver, isResolver := object.(interface{ DomainResolverReference() string }); isResolver {
		if resolver.DomainResolverReference() != "" {
			return true
		}
	}
	if reader, readsDefault := object.(interface{ ResolvesDomainServer() bool }); readsDefault && reader.ResolvesDomainServer() {
		return true
	}
	return false
}

// replaceOutbounds publishes one new slice with every replacement applied. order names the tags in
// batch order, so an appended tag (one that was not in the live slice) lands deterministically
// instead of wherever map iteration put it.
func replaceOutbounds(current []adapter.Outbound, replacements map[string]adapter.Outbound, order []string) []adapter.Outbound {
	next := make([]adapter.Outbound, len(current), len(current)+len(replacements))
	copy(next, current)
	applied := make(map[string]bool, len(replacements))
	for index, object := range next {
		if replacement, exists := replacements[object.Tag()]; exists {
			next[index] = replacement
			applied[object.Tag()] = true
		}
	}
	for _, tag := range order {
		if !applied[tag] {
			next = append(next, replacements[tag])
		}
	}
	return next
}
