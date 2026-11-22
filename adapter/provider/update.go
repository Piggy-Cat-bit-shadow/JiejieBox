package provider

import (
	"errors"
	"os"
	"reflect"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound/batch"
	"github.com/sagernet/sing-box/adapter/provider/nodestore"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
)

type updateNode struct {
	tag          string
	kind         string
	options      any
	outbound     *option.Outbound
	dependencies []string
}

// UpdateNodes publishes a whole subscription atomically enough to be safe: every node is prepared,
// the dependency graph is ordered, each changed node is replaced through the outbound manager's
// prepare-then-swap path, and only then are the nodes the subscription no longer names removed.
//
// # What "atomic enough" means here, precisely
//
// It is not a single transaction across N nodes. A node whose construction fails keeps its previous
// object (the manager's Replace leaves the old one published), and the remaining nodes still
// update; the failure is returned as an aggregate so the caller logs one line per apply rather
// than one per node. What the order guarantees is stronger than "best effort" for the case that
// matters: the published map only ever contains nodes that exist in the manager, and a dependency
// is never removed while a surviving node still names it.
func (a *Adapter) UpdateNodes(outOpts []option.Outbound) error {
	a.updateAccess.Lock()
	defer a.updateAccess.Unlock()
	if !a.beginApply() {
		// The provider reached its terminal state before this call was even registered. This is the
		// fast refusal; the authoritative decisions are the phase tests below, under the same gate
		// Close takes for its terminal transition.
		return E.New("provider ", a.providerTag, ": ", errProviderClosed)
	}
	defer a.endApply()
	if a.outbound == nil {
		return E.New("outbound manager does not support dynamic outbounds")
	}
	if len(outOpts) == 0 {
		// An empty apply is never how a provider becomes empty: the document readers report "no
		// servers found" instead of returning zero nodes, precisely so a garbage or empty body
		// cannot be read as "remove every node". A caller that really wants to empty a provider
		// removes it from the configuration.
		return E.New("refusing an empty node set: an unrecognised or empty document must not wipe the published nodes")
	}
	outTags := a.resolveOutboundTags(outOpts)
	nodes := make(map[string]updateNode, len(outOpts))
	for i := range outOpts {
		opt := &outOpts[i]
		// An embedder may hand us an interface that holds a typed nil pointer (the document
		// readers never do: they always allocate the options value). Method calls on a typed nil
		// pointer - TakeDialerOptions, the group member switches - would dereference it and panic
		// before the node is even constructed. Normalise it to a nil interface, which the manager
		// already accepts as "zero options"; the original option value is still kept for change
		// detection, where nil and typed nil are different values and must stay different.
		nodes[outTags[i]] = updateNode{tag: outTags[i], kind: opt.Type, options: nonNilOptions(opt.Options), outbound: opt}
	}
	// Order by declared dependencies (a detour, or a group's members). A subscription may list a
	// chain in either direction; constructing the dependency first is what lets a node's Start
	// resolve everything it names.
	var ordered []updateNode
	visiting, visited := make(map[string]bool), make(map[string]bool)
	var visit func(string) error
	visit = func(tag string) error {
		if visited[tag] {
			return nil
		}
		if visiting[tag] {
			return E.New("circular provider dependency: ", tag)
		}
		visiting[tag] = true
		node := nodes[tag]
		var dependencies []string
		if opts, ok := node.options.(option.DialerOptionsWrapper); ok {
			if detour := opts.TakeDialerOptions().Detour; detour != "" {
				dependencies = append(dependencies, detour)
			}
		}
		switch opts := node.options.(type) {
		case *option.SelectorOutboundOptions:
			dependencies = append(dependencies, opts.Outbounds...)
		case *option.LoadBalanceOutboundOptions:
			dependencies = append(dependencies, opts.Outbounds...)
		case *option.URLTestOutboundOptions:
			dependencies = append(dependencies, opts.Outbounds...)
		}
		for _, dep := range dependencies {
			if _, internal := nodes[dep]; internal {
				if err := visit(dep); err != nil {
					return err
				}
				continue
			}
			if _, found := a.outbound.Outbound(dep); !found {
				return E.New("provider dependency not found: ", dep)
			}
			// A node from the previous subscription that is about to be removed is not a usable
			// dependency; naming it would make this update depend on something it is deleting.
			if a.hasAppliedRecord(dep) {
				if _, stillPublished := nodes[dep]; !stillPublished {
					return E.New("provider dependency removed: ", dep)
				}
			}
		}
		visiting[tag] = false
		visited[tag] = true
		node.dependencies = dependencies
		ordered = append(ordered, node)
		return nil
	}
	for _, tag := range outTags {
		if err := visit(tag); err != nil {
			return err
		}
	}
	active := a.activeOutboundTags()
	outByTag := make(map[string]adapter.Outbound, len(outOpts))
	// records is the canonical payload of every tag whose object this apply freshly published. A
	// tag that keeps its previous object (unchanged, or a failed replacement) carries its previous
	// record forward instead of being re-encoded.
	records := make(map[string]nodestore.Payload, len(outOpts))
	// nextRefs starts as the previous snapshot's ownership map. It is edited into the next
	// snapshot, so a node that is no longer named but is still LIVE as some other node's
	// dependency keeps both its object and its ownership record.
	nextRefs := make(map[string]nodestore.Ref, len(outOpts))
	if previous := a.currentSnapshot(); previous != nil {
		for tag, ref := range previous.entries {
			nextRefs[tag] = ref
		}
	}
	var updateErr error
	// installed are the tags whose manager objects this apply has installed but not yet committed to
	// the provider. They are the apply's PROVISIONAL candidates: the manager swap that created them
	// is not a provider publication until the terminal gate below accepts it, so an apply that is
	// refused there reclaims them here instead of publishing them.
	installed := make([]string, 0, len(ordered))
	abort := func(cause error) error {
		a.abandonUpdate(installed, cause)
		return E.Errors(updateErr, cause)
	}

	// plannedNode is one node this apply must rebuild, with the decision material already computed by
	// the decision pass below. The payload is computed at most once per node: it is both the
	// comparison key and the retained record, and encoding it twice would charge the refresh for a
	// second conversion the product itself does not do.
	type plannedNode struct {
		node        updateNode
		current     adapter.Outbound
		exists      bool
		payload     nodestore.Payload
		payloadOK   bool
		payloadRead bool
	}
	payloadFor := func(planned *plannedNode) (nodestore.Payload, bool) {
		if !planned.payloadRead {
			planned.payloadRead = true
			if encoded, err := nodestore.Encode(*planned.node.outbound); err == nil {
				planned.payload, planned.payloadOK = encoded, true
			}
		}
		return planned.payload, planned.payloadOK
	}

	// finish performs the provider-side bookkeeping for one successfully installed candidate. The
	// manager has already published the object; this is where the provider decides whether that
	// publication may become its own.
	finish := func(planned *plannedNode) error {
		node := planned.node
		if hook := a.testAfterNodeInstall; hook != nil {
			hook(node.tag)
		}
		// The manager already published the object, but the provider has not. This test is the
		// authoritative decision for every candidate: if Close moved the phase, the candidate is
		// reclaimed and nothing about the provider changes.
		if !a.publicationOpen() {
			return E.New("provider ", a.providerTag, ": update abandoned: ", errProviderClosed)
		}
		if planned.exists {
			// Same-tag replacement: the manager parked the previous object. It may only be closed
			// after the group callbacks below rebuilt their memberships from this provider's list.
			a.markPendingReplacement(node.tag)
		}
		// A measurement is a fact about ONE object, and the history is keyed by TAG. A subscription
		// that keeps the tag and points it at a different server is the ordinary case, so the delay
		// stored for the replaced object must go with it (see replaceOutbound).
		a.history.DeleteURLTestHistory(node.tag)
		current, _ := a.outbound.Outbound(node.tag)
		if encoded, ok := payloadFor(planned); ok {
			records[node.tag] = encoded
		} else {
			// The value cannot be canonically compared. Keep ownership with a record that can never
			// match: every refresh will rebuild this node instead of mistaking the next document
			// for a foreign outbound, and no stale record can ever be a false "unchanged".
			//
			// This is a SAFE fallback, not a silent one: an encoder bug would otherwise turn into
			// "every provider node rebuilds on every refresh" with no signal at all. The count and
			// the first cause are reported once per apply, and stay queryable for a test.
			a.opaqueRecords.Add(1)
			if a.opaqueCause.Load() == "" {
				if _, encodeErr := nodestore.Encode(*node.outbound); encodeErr != nil {
					a.opaqueCause.Store(node.tag + ": " + encodeErr.Error())
				}
			}
			records[node.tag] = nodestore.Opaque()
		}
		outByTag[node.tag] = current
		return nil
	}

	// replaced records the tags this apply ACTUALLY replaced. It is what makes a dependent rebuild:
	// a node that names a replaced member must be rebuilt even when its own options are unchanged,
	// because its dialer resolved the member object once and keeping it would keep sending traffic
	// through the object the update just retired.
	replaced := make(map[string]bool, len(ordered))
	// pending is the CURRENT batch segment: candidates whose replacement has been decided but not
	// published. A dependency that is still pending has no outcome yet, so the segment is published
	// BEFORE the dependent is even decided. That keeps two contracts at once: a dependent is rebuilt
	// only when its dependency really was replaced (a failed replacement leaves the dependent
	// untouched), and a constructor that resolves a detour at construction time sees the object that
	// is actually published - which is what the per-node path guaranteed.
	pending := make([]plannedNode, 0, len(ordered))
	pendingTags := make(map[string]bool, len(ordered))

	// flush prepares, validates and publishes the current segment. For an outbound manager that
	// supports it, the whole segment is ONE provider publication: one graph validation, one table
	// swap, one graph-update notification. The per-item error handling below is the same either way.
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		if replacer, canBatch := a.outbound.(batch.OwnedReplacer); canBatch {
			items := make([]batch.Replacement, len(pending))
			for index := range pending {
				planned := &pending[index]
				items[index] = batch.Replacement{
					Tag:     planned.node.tag,
					Type:    planned.node.kind,
					Options: planned.node.options,
					// The per-node context and logger are the ones the edge-bearing path would use:
					// a constructor must not be able to tell whether it was part of a batch.
					Context: adapter.WithContext(a.ctx, &adapter.InboundContext{Outbound: planned.node.tag}),
					Logger:  a.logFactory.NewLogger("outbound/" + planned.node.kind + "[" + planned.node.tag + "]"),
				}
			}
			outcomes := replacer.ReplaceOwnedBatch(a.ctx, a.router, a.logger, items, a.providerTag)
			// EVERY candidate the manager published is provisional, including the ones whose
			// provider-side bookkeeping below never runs because the phase closed mid-batch. The
			// reclaim list therefore has to be complete before the first phase test, not appended
			// one successful node at a time.
			for index := range pending {
				if outcomes[index] == nil {
					installed = append(installed, pending[index].node.tag)
				}
			}
			for index := range pending {
				planned := &pending[index]
				if err := outcomes[index]; err != nil {
					updateErr = E.Errors(updateErr, E.Cause(err, planned.node.tag))
					// A failed replacement keeps the previous object published. Its previous record
					// is carried forward by publishAppliedSnapshot, so the next refresh compares
					// against the configuration that actually produced the live object.
					if planned.exists && active[planned.node.tag] {
						outByTag[planned.node.tag] = planned.current
					}
					continue
				}
				if err := finish(planned); err != nil {
					return err
				}
				replaced[planned.node.tag] = true
			}
		} else {
			for index := range pending {
				planned := &pending[index]
				node := planned.node
				err := a.replaceOutbound(adapter.WithContext(a.ctx, &adapter.InboundContext{Outbound: node.tag}),
					a.router, a.logFactory.NewLogger("outbound/"+node.kind+"["+node.tag+"]"),
					node.tag, node.kind, node.options)
				if err != nil {
					updateErr = E.Errors(updateErr, E.Cause(err, node.tag))
					if planned.exists && active[node.tag] {
						outByTag[node.tag] = planned.current
					}
					continue
				}
				installed = append(installed, node.tag)
				if err = finish(planned); err != nil {
					return err
				}
				replaced[node.tag] = true
			}
		}
		pending = pending[:0]
		clear(pendingTags)
		return nil
	}

	for _, node := range ordered {
		current, exists := a.outbound.Outbound(node.tag)
		owned := a.hasAppliedRecord(node.tag)
		// A rendered tag that already belongs to a static outbound (or to another provider) is not
		// this provider's to replace. Taking it would silently remove a node the user wrote in the
		// configuration, which is the one outcome the tag prefix exists to prevent.
		if exists && !owned {
			updateErr = E.Errors(updateErr, E.Cause(E.New("outbound tag is already in use outside this provider"), node.tag))
			continue
		}
		// A dependency still waiting in this segment has no outcome yet. Publish the segment first,
		// then decide this node against what was actually installed.
		waiting := false
		for _, dependency := range node.dependencies {
			if pendingTags[dependency] {
				waiting = true
				break
			}
		}
		if waiting {
			if err := flush(); err != nil {
				return abort(err)
			}
		}
		planned := plannedNode{node: node, current: current, exists: exists}
		rebuildDependent := false
		for _, dependency := range node.dependencies {
			if replaced[dependency] {
				rebuildDependent = true
				break
			}
		}
		unchanged := false
		if !rebuildDependent && exists && active[node.tag] {
			if encoded, ok := payloadFor(&planned); ok && a.appliedMatches(node.tag, encoded) {
				unchanged = true
			}
		}
		if unchanged {
			// Unchanged: do not touch the live object, and do not count it as a failure.
			outByTag[node.tag] = current
			records[node.tag] = planned.payload
			continue
		}
		if ctxErr := a.ctx.Err(); ctxErr != nil {
			// Close cancelled the provider before this node was constructed. The whole batch is
			// abandoned: the nodes installed so far are reclaimed, nothing is published.
			return abort(E.Cause(ctxErr, "provider update cancelled"))
		}
		pending = append(pending, planned)
		pendingTags[node.tag] = true
	}
	if err := flush(); err != nil {
		return abort(err)
	}

	if count := a.opaqueRecords.Load(); count > 0 {
		// One line per apply, not per node: a persistent encoder refusal is a bug, and a log flood
		// would hide it. The count is cumulative; the cause is the first one observed.
		cause, _ := a.opaqueCause.Load().(string)
		if a.logger != nil {
			a.logger.Warn("provider ", a.providerTag, ": ", count,
				" node record(s) could not be canonically encoded and will be rebuilt on every refresh; first: ", cause)
		}
	}
	if hook := a.testBeforeCommit; hook != nil {
		hook()
	}
	// The publication gate. Everything the provider publishes as one apply - the removals of the
	// previous set, the retired hand-off, the outbounds map and the record snapshot - happens under
	// this lock, and the phase is tested in the same critical section: Close's terminal transition
	// takes the write side, so a batch either commits entirely before it (and Close tears it down)
	// or is refused entirely and its candidates are reclaimed. There is no window in which a check
	// and a publication can straddle the transition.
	a.publishAccess.Lock()
	if a.phase != providerPhaseOpen {
		a.publishAccess.Unlock()
		return abort(E.New("provider ", a.providerTag, ": update refused: ", errProviderClosed))
	}
	// Remember what could not be unlinked. A group that still names a retired node keeps it LIVE:
	// closing it under the selector that may still dial it is the defect this hand-off removes.
	// UpdateGroups retries after the callbacks have rebuilt the memberships.
	retired := a.removeUseless(outTags)
	a.retiredAccess.Lock()
	a.retired = retired
	a.retiredAccess.Unlock()
	var outbounds []adapter.Outbound
	for _, tag := range outTags {
		if node := outByTag[tag]; node != nil {
			outbounds = append(outbounds, node)
		}
	}
	a.outboundsAccess.Lock()
	a.outbounds, a.outboundsByTag = outbounds, outByTag
	a.outboundsAccess.Unlock()
	a.publishAppliedSnapshot(nextRefs, records, outByTag)
	a.publishAccess.Unlock()
	if a.enabled && a.history != nil {
		a.run(func() { a.HealthCheck(a.ctx) })
	}
	if !a.hasUpdateCallbacks() {
		// No group subscribed to this provider, so there is no callback phase to wait for: nothing
		// can still resolve a retired object. Completing here keeps the historical UpdateNodes-only
		// behaviour for an embedder that drives the adapter directly.
		a.completePublication()
	}
	return updateErr
}

// abandonUpdate reclaims the manager objects an apply installed before the terminal gate refused
// the publication. Nothing about the provider changes: the published outbounds map and the record
// snapshot still describe the previous successful apply, and the candidates never enter either.
//
// A missing tag is expected, not an error: Close may have released the same tag concurrently
// because the tag was already part of the provider's published set. The manager's unlink critical
// section decides which caller actually removed the object, and only that caller closes it, so
// this path cannot close an object twice. The ownership barrier afterwards releases the previous
// object the manager parked for a same-tag replacement; a lease still held by a dial keeps that
// object open until the lease ends, exactly as during a refresh.
func (a *Adapter) abandonUpdate(installed []string, cause error) {
	if len(installed) == 0 {
		return
	}
	for _, tag := range installed {
		if err := a.releasePublishedTag(tag); err != nil && !errors.Is(err, os.ErrInvalid) && a.logger != nil {
			a.logger.Error(err, "reclaim abandoned provider outbound [", tag, "]: ", cause)
		}
	}
	a.finishRetiredPublication(installed)
}

// nonNilOptions normalises an interface holding a typed nil pointer to a nil interface. Dependency
// discovery calls methods on the options value, and a typed nil pointer would make those calls
// dereference nil before construction. Nil and typed nil remain DIFFERENT values for the record
// comparison, which is why only the construction/dependency view is normalised, never the original
// option tree that is encoded.
func nonNilOptions(options any) any {
	if options == nil {
		return nil
	}
	value := reflect.ValueOf(options)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return nil
	}
	return options
}

func (a *Adapter) activeOutboundTags() map[string]bool {
	a.outboundsAccess.RLock()
	defer a.outboundsAccess.RUnlock()
	tags := make(map[string]bool, len(a.outbounds))
	for _, outbound := range a.outbounds {
		tags[outbound.Tag()] = true
	}
	return tags
}
