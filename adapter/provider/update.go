package provider

import (
	"reflect"

	"github.com/sagernet/sing-box/adapter"
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
		nodes[outTags[i]] = updateNode{tag: outTags[i], kind: opt.Type, options: opt.Options, outbound: opt}
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
			if _, owned := a.appliedOutbounds[dep]; owned {
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
	replaced := make(map[string]bool, len(outOpts))
	var updateErr error
	for _, node := range ordered {
		ctx := adapter.WithContext(a.ctx, &adapter.InboundContext{Outbound: node.tag})
		current, exists := a.outbound.Outbound(node.tag)
		_, owned := a.appliedOutbounds[node.tag]
		// A rendered tag that already belongs to a static outbound (or to another provider) is not
		// this provider's to replace. Taking it would silently remove a node the user wrote in the
		// configuration, which is the one outcome the tag prefix exists to prevent.
		if exists && !owned {
			updateErr = E.Errors(updateErr, E.Cause(E.New("outbound tag is already in use outside this provider"), node.tag))
			continue
		}
		// A node that names a member replaced earlier in this update must be rebuilt even when its
		// own options are unchanged. Its dialer holds the member object it resolved through
		// `sync.Once`, so keeping it would keep sending that node's traffic through the object the
		// update just retired - the replacement would be published and never used.
		rebuildDependent := false
		for _, dependency := range node.dependencies {
			if replaced[dependency] {
				rebuildDependent = true
				break
			}
		}
		if !rebuildDependent && exists && active[node.tag] && reflect.DeepEqual(*node.outbound, a.appliedOutbounds[node.tag]) {
			// Unchanged: do not touch the live object, and do not count it as a failure.
			outByTag[node.tag] = current
			continue
		}
		err := a.outbound.Replace(ctx, a.router, a.logFactory.NewLogger("outbound/"+node.kind+"["+node.tag+"]"), node.tag, node.kind, node.options)
		if err != nil {
			updateErr = E.Errors(updateErr, E.Cause(err, node.tag))
			if exists && active[node.tag] {
				outByTag[node.tag] = current
			}
			continue
		}
		replaced[node.tag] = true
		current, _ = a.outbound.Outbound(node.tag)
		a.appliedOutbounds[node.tag] = *node.outbound
		outByTag[node.tag] = current
	}
	a.removeUseless(outTags)
	var outbounds []adapter.Outbound
	for _, tag := range outTags {
		if node := outByTag[tag]; node != nil {
			outbounds = append(outbounds, node)
		}
	}
	a.outboundsAccess.Lock()
	a.outbounds, a.outboundsByTag = outbounds, outByTag
	a.outboundsAccess.Unlock()
	for tag := range a.appliedOutbounds {
		if _, stillPublished := outByTag[tag]; !stillPublished {
			delete(a.appliedOutbounds, tag)
		}
	}
	if a.enabled && a.history != nil {
		a.run(func() { a.HealthCheck(a.ctx) })
	}
	return updateErr
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
