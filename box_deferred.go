// The deferred-construction seam, installed for the node kinds that can honour it.
//
// # Why this is on and not behind a switch
//
// A configuration that names ten thousand nodes and dials three pays for all ten thousand at Start.
// The deferred seam makes a node cost its recipe until something asks for it, and the reclaim policy
// in adapter/outbound/reclaim.go bounds how many expansions may exist at once (64 by default), so a
// full url-test round cannot leave the whole library expanded. That combination is the feature: the
// seam without the reclaim policy, or the policy without the seam, is not something an operator
// should have to assemble, and a switch nobody sets would leave the measured 40% on the table.
//
// # Why only some nodes
//
// The seam is installed unconditionally, but the constructor it installs admits a node only when a
// protocol says it can honour the lifecycle: today that is Trojan, and only when the options carry
// nothing the deferred path would have to lie about (a pooled transport, a multiplexer, a bind
// interface, a resolver the recipe cannot reproduce). Everything else returns "not mine" and takes
// the eager path unchanged. protocol/trojan's LazyEligible is the single place that decides, and it
// is a pure query: nothing about it dials, binds or builds.

package box

import (
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/protocol/trojan"
)

// withDeferredOutbounds installs the deferred-construction seam on this box's outbound manager.
//
// The manager owns the budget and the reclaim sweep; this function only says which constructor the
// manager may consult. Called once per Box, before any provider or outbound is created.
func withDeferredOutbounds(manager *outbound.Manager) {
	manager.SetDeferredOutboundConstructor(trojan.DeferredConstructor())
}
