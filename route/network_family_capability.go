package route

import (
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	"github.com/sagernet/sing/common"
)

// localFamilyCapability derives the local dial path's per-family verdict from the interface snapshot
// the manager already holds.
//
// # What is observed
//
// The manager's own filtered interface list - the same list a network strategy selects from - plus
// the one piece of knowledge only this process has: which of those interfaces are ITS OWN.
//
// # Why the manager's list, and not a fresh enumeration
//
// The list is refreshed by the monitor's own event, on the platform's own schedule, and it is what
// every other interface-driven decision in this file reads. Enumerating again here would produce a
// second opinion about the same instant, taken at a different moment, and could disagree with the
// list the dial path is about to bind to. Observing on the event and reading the result is also what
// keeps the whole thing free of per-dial work.
//
// # The TUN exclusion, and what it does not cover
//
// This process's own interfaces are removed because their addressing is not evidence about the
// network underneath them: a sing-box TUN carrying ::/0 and a global IPv6 address on an IPv4-only
// physical link is the ordinary shape of a tunnelled client, and counting that address would make
// the model report IPv6 as available on the strength of sing-box's own route.
//
// The exclusion is complete for the platform-interface clients, where sing-box registers its TUN
// with the monitor it also reads. On a client that creates its own TUN without registering it, a TUN
// interface can still appear here and be counted as underlay addressing. That errs in the
// observable direction - the family is reported as present, so it is attempted normally, and a TUN
// client's dial is bound to the physical interface where the attempt fails on its own - so it costs
// a missed fast-fail rather than a refused connection. Detecting a tunnel by interface name would be
// a guess about another program's naming, which is why it is not done.
//
// Reading `networkInterfaces` (an atomic) rather than calling NetworkInterfaces() keeps this usable
// from the recompute, which is the one place that must not take extra locks: it is called with
// environmentUpdateAccess held and, on the interface path, with resetRunAccess held as well.
func (r *NetworkManager) localFamilyCapability() netcap.FamilyCapability {
	interfaces := r.networkInterfaces.Load()
	var ownInterfaces []string
	if r.interfaceMonitor != nil {
		ownInterfaces = r.interfaceMonitor.MyInterfaces()
	}
	observations := common.Map(interfaces, func(it adapter.NetworkInterface) netcap.InterfaceObservation {
		return netcap.InterfaceObservation{
			Name:      it.Interface.Name,
			Addresses: it.Interface.Addresses,
		}
	})
	usable, ok := netcap.UsableInterfaces(observations, ownInterfaces)
	if !ok {
		// There is no usable interface to look at, so there is nothing to conclude. Reporting
		// Unavailable here would turn "could not observe" into a hard filter on a machine that may
		// well have the family - for instance while the interface list is momentarily empty during
		// a handover.
		return netcap.FamilyCapability{}
	}
	return netcap.Evaluate(usable)
}

// storeFamilyCapability writes the verdict published with the current network generation.
//
// It must be called with stateAccess held, immediately beside the environment fingerprint write or
// the transition claim. That pairing is the whole reason this is not a separate event: the capability
// belongs to a network, the fingerprint is how this file names a network, and a consumer that saw one
// network's fingerprint or epoch beside another's verdict would be deciding a dial on the wrong
// network's evidence.
//
// The value is always stored, even when it equals the previous one. A store is a pointer swap, and
// skipping it would mean the published value is the one from an older generation - which is
// indistinguishable from "unchanged" only as long as nothing else moved, and that is exactly the
// assumption this function exists to avoid making.
func (r *NetworkManager) storeFamilyCapability(capability netcap.FamilyCapability) {
	r.familyCapability.Store(&adapter.NetworkFamilyCapability{Capability: capability})
}
