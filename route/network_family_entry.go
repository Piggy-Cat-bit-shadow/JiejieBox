package route

import (
	"net"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/control"
)

// This file is the ENTRY half of the family capability: who decides what applications are told
// about IPv6, and when.
//
// # Why it lives here, and not in each consumer
//
// The facts the decision needs are the manager's: which interfaces exist, which of them are this
// process's own, and whether the machine has a route to a global IPv6 destination. A TUN that read
// them itself would be a second observer with a second idea of "usable", and the DNS entry would be
// a third. The manager already produces exactly one observation per network event for the dial
// verdict; the entry decision is a projection of the SAME observation, so the two cannot describe
// different machines.
//
// # Why the decision is taken once, at start, and latched
//
// The dial verdict is live: it is republished whenever the interfaces change, and a dial reads the
// current one under an ownership check. The entry decision cannot work that way, because the entry
// it describes - the TUN's addressing - is fixed when the interface is opened and no platform
// supported here offers a cheap in-place update. So the decision is taken ONCE, before any entry
// exists, and it is read back unchanged for the life of the box.
//
// That is a deliberate trade, stated here rather than discovered later:
//
//   - there is no split state, because every consumer reads the same committed value;
//   - a network change that would flip the decision is NOT applied to the running TUN, and a box
//     that started on a network proving no local IPv6 keeps its IPv4-only interface until it is
//     restarted. It never leaks a false IPv6 presentment, and it never breaks IPv4;
//   - the alternative - recomputing live and letting DNS disagree with the interface - is the
//     failure the fork has already paid for once: a DNS layer that says "IPv6 is back" beside a TUN
//     that cannot carry it.

// SetIPv6EntryDemand records the two configuration facts the decision depends on. It must be called
// before Start; the box calls it with the values it derived from the assembled typed options.
//
// `required` is the demand summary: does any application-facing surface of this configuration
// present IPv6 at all? False is the fast path - Start makes no observation, no route table is read,
// no decision is committed and every consumer answers "present", which is the behaviour of a build
// without this feature.
//
// `allTunReducible` says whether EVERY configured TUN can be presented as IPv4 only. The decision is
// one value for the whole box, so it may only be "suppress" if every interface it describes can
// actually be reduced; deciding per TUN and rolling back afterwards is order-dependent and leaves a
// split state. See option.AllTunInboundsReducibleToIPv4Only.
func (r *NetworkManager) SetIPv6EntryDemand(required bool, allTunReducible bool) {
	r.entryDemand.Store(required)
	r.entryAllTunReducible.Store(allTunReducible)
}

// observeEntryPresentment takes the startup decision and publishes it.
//
// # Why a failed observation publishes nothing
//
// `observed == false` means the manager could not look: no finder, a failed enumeration, no usable
// interface. Publishing "present" would be a decision nobody asked for; publishing "suppress" would
// turn an observation failure into a family outage on a machine that may well have IPv6. Leaving the
// pointer nil is the third state - "no opinion" - and the reader answers PresentIPv6 for it, which
// is the required degradation.
//
// # Why this never fails the start
//
// The decision is an optimisation of what applications are told. A network that cannot be observed
// at start must not keep the tunnel from coming up; the dial gate that already consumes the live
// capability continues to refuse what it can prove.
func (r *NetworkManager) observeEntryPresentment() {
	// # Why this is safe to run more than once
	//
	// `Box.PreStart()` runs the initialize and first start stage, and `Box.Start()` runs them again
	// before the inbounds start (preStart is called by both). Running the observation twice is
	// therefore normal in that flow, and it is also harmless: no entry exists yet, the DNS router
	// reads the value per query rather than caching it, and no application traffic can reach it
	// before the TUN is opened. What the second run buys is a fresher answer - the observation that
	// counts is the one immediately before the entries start - and what it costs is one interface
	// enumeration, on a configuration that demanded one.
	presentment, observed := r.observeIPv6EntryPresentment()
	if !observed {
		r.logger.Debug("ipv6 entry presentment: the local network could not be observed, ",
			"so applications keep the configured ipv6 presentment")
		return
	}
	if presentment == netcap.SuppressIPv6 && !r.entryPresentmentDeliverable() {
		// The platform cannot say "this interface has no IPv6" without the platform layer claiming
		// otherwise. Committing the decision would make the DNS entry refuse AAAA for an interface
		// that still advertises the family, so the whole decision is dropped: the previous behaviour
		// is kept, which is the safe direction. See entryPresentmentDeliverable.
		r.logger.Debug("ipv6 entry presentment: this platform's tunnel delivery cannot express an ",
			"ipv4-only interface, so applications keep the configured ipv6 presentment")
		return
	}
	if presentment == netcap.SuppressIPv6 && !r.entryAllTunReducible.Load() {
		// The network proves no local IPv6, but at least one configured TUN cannot be presented as
		// IPv4 only (no IPv4 address, or a hijack DNS address list with no IPv4 entry). The decision
		// is shared by the whole box, and a per-TUN rollback would leave one interface stripped while
		// the DNS answer still presents IPv6 - so the box keeps IPv6 everywhere and says why.
		//
		// The commitment is explicit rather than absent: "present" is a decision that every consumer
		// reads, and a diagnostic can tell it apart from "nobody asked".
		r.logger.Info("ipv6 entry presentment: at least one tun cannot be presented ipv4-only ",
			"(no ipv4 address, or a hijack dns address list with no ipv4 entry), so every ",
			"application keeps the configured address families")
		r.commitIPv6EntryPresentment(netcap.PresentIPv6)
		return
	}
	r.commitIPv6EntryPresentment(presentment)
	if presentment == netcap.SuppressIPv6 {
		r.logger.Info("ipv6 entry presentment: the local network has no usable ipv6 path, ",
			"so applications are presented with ipv4 only until restart (the dial gate is unaffected)")
	} else {
		r.logger.Debug("ipv6 entry presentment: applications are presented with the configured ",
			"address families")
	}
}

// entryPresentmentDeliverable reports whether this platform's tunnel delivery can express "this
// interface presents no IPv6".
//
// # It is an allowlist, and that is the point
//
// The answer is enabled only where the delivery contract was actually read and verified, because
// the failure mode of guessing is an interface that claims IPv6 and cannot carry it - or a tunnel
// that will not start. Everything else keeps the previous behaviour, which the live dial gate
// already covers.
//
//	Windows, Linux (and Android, which C.IsLinux includes)
//	    CAN. The core opens the interface itself, and clearing `Inet6Address` removes the
//	    addressing, the automatic route range and the hijack DNS address in one step - for the
//	    kernel, the stack and the auto-redirect rules at the same time. Android's client derives
//	    every IPv6 fact from the same options (VPNService.kt), and sing-tun's automatic route range
//	    is gated on the address, so the reduction reaches the VPN builder too.
//
//	macOS, iOS and the tvOS flavour (C.IsDarwin covers darwin and ios)
//	    CAN, since sing-box-for-apple 6e559a4 - the revision `clients/apple` now pins. `openTun0`
//	    creates `NEIPv6Settings` only when the core sent at least one IPv6 address, and the other
//	    `setTunnelNetworkSettings` sites re-apply that same stored settings object, so there is no
//	    second writer that could reintroduce the family. C.IsTvOS is listed separately because the
//	    Apple project builds a tvOS flavour whose GOOS/tag combination is not guaranteed to satisfy
//	    IsDarwin; the constant is false on every non-tvOS build.
//
//	Everything else (the BSDs, js, any GOOS without a verified client contract)
//	    False. The list is positive rather than "not a platform I know is broken", precisely so an
//	    unverified client falls on the fail-open side.
func (r *NetworkManager) entryPresentmentDeliverable() bool {
	if r.entryDeliveryRemovesIPv6 != nil {
		return r.entryDeliveryRemovesIPv6()
	}
	return entryPresentmentDeliverableOn(C.IsLinux, C.IsWindows, C.IsDarwin, C.IsTvOS)
}

// entryPresentmentDeliverableOn is the pure platform rule, extracted so it can be tested on one
// runner for all platforms the fork builds.
func entryPresentmentDeliverableOn(linux, windows, darwin, tvos bool) bool {
	return linux || windows || darwin || tvos
}

// observeIPv6EntryPresentment reads the network read-only and projects it into an entry decision.
func (r *NetworkManager) observeIPv6EntryPresentment() (netcap.IPv6Presentment, bool) {
	interfaces, observed := r.entryObservationInterfaces()
	if !observed {
		return netcap.PresentIPv6, false
	}
	var ownInterfaces []string
	if r.interfaceMonitor != nil {
		ownInterfaces = r.interfaceMonitor.MyInterfaces()
	}
	// The route observation is read here for the same reason it is read in localFamilyCapability:
	// it answers a machine-level question, and it must belong to the same instant as the addressing.
	globalRoute := r.machineGlobalIPv6Route()
	usable, ok := netcap.UsableInterfaces(r.familyObservations(interfaces, globalRoute), ownInterfaces)
	if !ok {
		// No usable interface is an observation failure, not a negative: an interface list that is
		// momentarily empty during a handover must not decide that the machine has no IPv6.
		return netcap.PresentIPv6, false
	}
	capability := netcap.Evaluate(usable)
	return netcap.ProjectIPv6Presentment(capability, netcap.ClassifyIPv6Addressing(usable)), true
}

// entryObservationInterfaces reads the interface list for the startup decision, WITHOUT publishing
// it.
//
// # Why it cannot simply call UpdateInterfaces
//
// UpdateInterfaces republishes the environment fingerprint and may claim a transition, and this
// observation runs before the box has finished starting - the TUN is opened later inside Box.Start,
// and the manager's first environment boundary is established at the END of it. Claiming a
// transition from here would reset DNS and transports before the first query was ever answered, and
// it would establish a generation from an observation the ordinary startup path is about to repeat.
//
// # What it reads, in order
//
//  1. the list the manager already published, when there is one. That is the platform's own
//     filtered view on clients that supply interfaces, and it is already the same list the dial
//     verdict is computed from - the most consistent answer available.
//  2. otherwise the platform's list directly, on a client that supplies one. It is a read, not a
//     publish, and it carries the platform's idea of which interfaces exist.
//  3. otherwise the interface finder, refreshed once. This is the path every client without a
//     platform interface takes - the CLI and both desktop daemons - and it is the same source
//     UpdateInterfaces publishes from.
//
// A failure at any step is reported as "not observed", which the caller must read as "no opinion".
func (r *NetworkManager) entryObservationInterfaces() ([]adapter.NetworkInterface, bool) {
	if published := r.networkInterfaces.Load(); len(published) > 0 {
		return published, true
	}
	if r.platformInterface != nil && r.platformInterface.UsePlatformNetworkInterfaces() {
		interfaces, err := r.platformInterface.NetworkInterfaces()
		if err != nil || len(interfaces) == 0 {
			return nil, false
		}
		if C.IsDarwin {
			// NEInterface only provides name, index and type; the addressing comes from the system's
			// own view. This mirrors UpdateInterfaces deliberately: the entry decision must be
			// computed from the same addressing the published list would carry, or the two would
			// disagree on the one platform where the platform's list has no addresses at all.
			if r.interfaceFinder == nil {
				return nil, false
			}
			err = r.interfaceFinder.Update()
			if err != nil {
				return nil, false
			}
			interfaces = common.Map(interfaces, func(it adapter.NetworkInterface) adapter.NetworkInterface {
				iif, _ := r.interfaceFinder.ByIndex(it.Index)
				if iif != nil {
					it.Interface = *iif
				}
				return it
			})
		}
		return filterUpInterfaces(interfaces), true
	}
	if r.interfaceFinder == nil {
		return nil, false
	}
	err := r.interfaceFinder.Update()
	if err != nil {
		return nil, false
	}
	return filterUpInterfaces(common.Map(r.interfaceFinder.Interfaces(), func(it control.Interface) adapter.NetworkInterface {
		return adapter.NetworkInterface{Interface: it}
	})), true
}

// filterUpInterfaces keeps the interfaces that can currently carry traffic.
//
// It is a copy of the filter UpdateInterfaces applies, kept separate because that function is on
// the publish path and this one must not touch it. The two must agree on the fact that matters to
// the model - an interface that is not up contributes no addressing - and both read the same flag.
func filterUpInterfaces(interfaces []adapter.NetworkInterface) []adapter.NetworkInterface {
	return common.Filter(interfaces, func(it adapter.NetworkInterface) bool {
		return it.Flags&net.FlagUp != 0
	})
}

// commitIPv6EntryPresentment records the decision now in force.
//
// # Why it is unexported, and why there is only one writer
//
// The decision is taken and published in one place - this file's startup observation - because it is
// a property of the INTERFACE the box opens, not of any consumer. An earlier design let the TUN
// correct the value when its own reduction was refused; that produced exactly the multi-TUN split it
// was meant to avoid (one interface stripped, the shared value rolled back by the other, order
// dependent), and it has been replaced by the box-level pre-check above: the manager never publishes
// "suppress" unless every configured TUN can honour it, so no consumer has anything to correct.
//
// A test double that never commits leaves the pointer nil, and the reader answers PresentIPv6.
func (r *NetworkManager) commitIPv6EntryPresentment(presentment netcap.IPv6Presentment) {
	value := presentment
	r.entryPresentment.Store(&value)
}

// IPv6EntryPresentment returns the committed decision, or PresentIPv6 when nothing was committed.
//
// A nil load is not a missing value to be guessed at: it means no decision was taken for this box -
// the configuration has no surface that presents IPv6, or the network could not be observed. The
// zero-value answer keeps the previous behaviour exactly.
func (r *NetworkManager) IPv6EntryPresentment() netcap.IPv6Presentment {
	if committed := r.entryPresentment.Load(); committed != nil {
		return *committed
	}
	return netcap.PresentIPv6
}
