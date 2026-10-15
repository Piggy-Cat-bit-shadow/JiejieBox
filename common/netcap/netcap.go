// Package netcap models what the LOCAL dial path can do with each address family on the network
// it is currently on.
//
// # The question this answers, and the one it refuses to answer
//
// It answers:
//
//	"can a socket for this family be created and routed by this machine, right now, on the
//	 network generation it is currently on?"
//
// It deliberately does NOT answer "is the public Internet reachable over this family?".
//
// That distinction is the whole point. A campus, hospital, hotel or captive-portal network
// routinely presents a complete, healthy-looking IPv6 structure - a link, an address, a router
// advertisement, a default route, even a reachable IPv6 gateway - while carrying no working public
// IPv6 at all. None of those facts can be turned into "IPv6 works" without a probe, and a probe is
// both a new always-on cost and a new source of wrong answers. So the model keeps the two questions
// apart and lets the existing scheduler learn the second one from real traffic.
//
// # Why the negative is allowed to be hard and the positive is not
//
// A HARD verdict - "do not spend an attempt on this family" - is only ever produced from a fact the
// machine can prove about itself: no interface on the local dial path can carry an address of that
// family at all, so no socket for it can be created with a usable source. That is a structural
// impossibility, not a guess, and it is stable for as long as the observed structure is.
//
// Every other combination is PROVISIONED, which means "the structure exists and nothing more". A
// provisioned family takes part in ordinary Happy Eyeballs racing, exactly as it did before this
// package existed. There is no state here that removes a family because it is merely unproven,
// suspected, or recently unlucky - unproven must never be spelled the same way as impossible.
package netcap

import (
	"net/netip"
	"slices"
)

// DestinationScope is the part of a destination address that decides WHICH question has to be
// answered about it.
//
// The failure this exists for is asymmetric, and the asymmetry is entirely about the destination:
//
//	fe80::1        reached over the link, and a link-local address is what makes it reachable.
//	               An interface that carries ONLY link-local addressing can still dial it.
//	fd00::1        reached inside the site. A campus that blackholes public IPv6 still serves ULA.
//	2409:...       reached over the public Internet, and that needs a global source address and a
//	               route. Neither is implied by the two cases above.
//
// A single "IPv6 is available" answer cannot serve all three, and the gap between them is the real
// user-facing failure this round fixes: a Windows machine with an IPv4 address and a fe80:: link-local
// address has IPv6 "present" and cannot reach a single global IPv6 destination - every such attempt
// dies instantly in the kernel with WSAENETUNREACH.
//
// So the model answers twice, once per scope, and the decision for a destination is the decision for
// ITS scope. Declaring it here rather than at each call site is what stops the fifth caller from
// forgetting one of the terms.
type DestinationScope uint8

const (
	// ScopeUnknown is an invalid or unspecified address: there is nothing to have an opinion about,
	// so it must never be refused.
	ScopeUnknown DestinationScope = iota
	// ScopeLocal is loopback, link-local or unspecified: reachable without leaving the host or the
	// link.
	ScopeLocal
	// ScopeSite is a unique-local address (fc00::/7): reachable inside the site.
	ScopeSite
	// ScopeGlobal is everything else: reachable only over the public Internet, which is the only
	// scope that needs a global route to exist.
	ScopeGlobal
)

func (s DestinationScope) String() string {
	switch s {
	case ScopeLocal:
		return "local"
	case ScopeSite:
		return "site"
	case ScopeGlobal:
		return "global"
	default:
		return "unknown"
	}
}

// ClassifyScope reports which part of the network a destination address is reached over.
//
// # Why the capability must not touch local-scope destinations
//
// The structural check says "this machine has no address of family F anywhere", which is a claim about
// reaching REMOTE destinations of F. The scopes below are not reached that way:
//
//   - loopback never leaves the host;
//   - link-local (fe80::/10, 169.254/16) is scoped to one link, and an interface can carry a
//     link-local address of a family while carrying no global one of it - which is the normal state
//     of an interface whose IPv6 is not routed. Refusing it would delete a working local service on
//     the strength of a verdict about the Internet;
//   - unique-local (fc00::/7) is the IPv6 counterpart of RFC 1918 space, and a campus or hospital
//     network that blackholes PUBLIC IPv6 routinely still serves ULA internally. This is the concrete
//     case the design is required not to collapse, and it is why ScopeSite exists as a separate
//     answer rather than being folded in with either neighbour;
//   - the unspecified address means "let the kernel choose", which a caller does when it is
//     deliberately indifferent to the family - so there is no family to have a verdict about.
//
// # Why this is a scope test and not a public/private guess
//
// It tests the SCOPE of the address, which is a property of the address itself and is decidable
// without a route lookup. It deliberately does not attempt to classify "is this target on the public
// Internet", which would be a guess about routing dressed up as a fact about an address.
//
// An IPv4-mapped IPv6 address is classified by its IPv4 form, matching classifyAddress in the dialer:
// ::ffff:1.2.3.4 is a representation of an IPv4 address, and treating it as IPv6 would apply an IPv6
// question to an IPv4 destination.
func ClassifyScope(address netip.Addr) DestinationScope {
	if !address.IsValid() {
		return ScopeUnknown
	}
	address = address.Unmap()
	switch {
	case address.IsLoopback(), address.IsLinkLocalUnicast(), address.IsUnspecified():
		return ScopeLocal
	case address.IsPrivate():
		return ScopeSite
	default:
		return ScopeGlobal
	}
}

// FamilyState is what is known about one address family on the current network generation.
type FamilyState uint8

const (
	// StateUnknown means this generation has not produced enough information to say anything.
	//
	// It is the fail-open state, and the zero value, so a capability that was never computed - a
	// manager without a network monitor, a platform that cannot enumerate interfaces, a test double
	// that does not implement the capability at all - behaves exactly as the code did before this
	// package existed.
	StateUnknown FamilyState = iota
	// StateUnavailable means the family is provably unusable on the local dial path. The only
	// producer of this state is the structural check in Evaluate: the machine has interfaces to
	// look at, and not one of them carries an address of this family.
	StateUnavailable
	// StateProvisioned means the structure exists - addressing on an interface the local dial path
	// can use - and nothing more than that has been proven.
	//
	// It is NOT a weaker form of unavailable, and it must never be treated as one. A provisioned
	// family races.
	StateProvisioned
	// StateValidated is reserved: a family with reliable, current-generation evidence that it
	// carries traffic.
	//
	// Nothing in this round produces it, on purpose. The evidence available today is per
	// destination - one successful connection to one address says that address answered, not that
	// the family works for the next name, and promoting it would be exactly the
	// destination-specific-to-network-wide amplification this design is required to avoid. The
	// value exists so that the model does not have to grow a second meaning for "good" later, and
	// consumers already handle it as "no restriction".
	StateValidated
	// StateDegraded is reserved: strong evidence that this family is blackholed on this network,
	// short of proof that it is impossible.
	//
	// Its consumption is already implemented - it biases the scheduler towards starting both
	// families together and never removes a candidate - and that behaviour is currently produced by
	// the dialer-local family health, which is scoped to one dialer and one network environment.
	// A network-wide verdict needs destination-independence evidence that this round does not have.
	StateDegraded
)

func (s FamilyState) String() string {
	switch s {
	case StateUnknown:
		return "unknown"
	case StateUnavailable:
		return "unavailable"
	case StateProvisioned:
		return "provisioned"
	case StateValidated:
		return "validated"
	case StateDegraded:
		return "degraded"
	default:
		return "invalid"
	}
}

// FamilyCapability is what this machine can do with IPv4 and IPv6 on one network generation.
//
// It is a value with comparable fields, so a publisher can detect "nothing changed" with == rather
// than waking consumers for an update that carries no new information.
type FamilyCapability struct {
	IPv4 FamilyState
	IPv6 FamilyState
	// GlobalIPv6Egress is the SEPARATE question about the public Internet, and it is separate because
	// the two have different answers on the network this round exists for.
	//
	//	IPv6 = Provisioned  and  GlobalIPv6Egress = Unavailable
	//
	// is a Windows machine with an IPv4 address and a fe80:: link-local address: IPv6 EXISTS (a
	// link-local destination is genuinely reachable) and no global IPv6 destination is, which is what
	// every `dial tcp [2409:...]` in the real failure log proves. Collapsing the two into one IPv6
	// state would have to pick one of them and be wrong about the other - either refusing link-local
	// destinations that work, or spending attempts on global ones that cannot.
	//
	// # It only ever gates GLOBAL destinations
	//
	// A link-local destination is reached over the link and a ULA destination inside the site; neither
	// needs a global route, so neither may be refused because one is missing. That is the "Do not
	// enlarge the public failure into the LAN" rule, and it is enforced in HardUndialable rather than
	// left to each caller.
	//
	// # Why it is a state and not a bool
	//
	// Because the whole safety of the hard verdict rests on being able to say "not proven" - a
	// platform with no route-table access, an observation that failed, a network mid-transition. A
	// bool has no room for that and would have to spell "I do not know" as one of its two answers.
	GlobalIPv6Egress FamilyState
	// reachable is the interface count the evaluation was able to look at, excluding this process's
	// own tunnels and loopback. Zero means nothing could be observed, and it is why the zero value
	// of this struct reports both families as Unknown rather than as Unavailable.
	reachable int
}

// HardUnavailable reports whether a candidate of this family must not be spent an attempt on.
//
// It answers the FAMILY-EXISTENCE question only, and it is deliberately not what the dial path asks:
// a global IPv6 destination depends on GlobalIPv6Egress as well, and a link-local one must not depend
// on it at all. Callers decide with HardUndialable, which takes the destination and resolves both
// terms in one place.
//
// A zero FamilyCapability - one nobody computed - answers false for both families. That is the
// required degradation: an implementation that does not provide the capability keeps its previous
// behaviour exactly, rather than silently losing a family because a struct was left empty.
func (c FamilyCapability) HardUnavailable(ipv4 bool) bool {
	if ipv4 {
		return c.IPv4 == StateUnavailable
	}
	return c.IPv6 == StateUnavailable
}

// HardUndialable reports whether an attempt at this destination must not be made on the local dial
// path, on the network generation this capability was observed on.
//
// # This is the single decision point, and that is the point
//
// The composition was previously open-coded at five call sites - the literal gate, the packet gate,
// the candidate scheduler, the serial resolve path and the native-bypass profile - each spelling out
// "is this destination local-scope, and if not is its family unavailable". Five copies of a rule is
// five chances to omit a term, and omitting one is invisible: the gate simply does not fire, and the
// symptom is the outage this round is fixing rather than a test failure.
//
// So the rule lives here, once, and takes the destination rather than a family:
//
//	ScopeLocal  -> dialable. Loopback, link-local and unspecified are reached without a global route,
//	               and a link-local address is exactly what makes a link-local destination work. A
//	               machine whose IPv6 is a lone fe80:: must still reach its own link.
//	ScopeSite   -> dialable. A ULA address is reached inside the site, and a campus that blackholes
//	               public IPv6 routinely still serves ULA internally. The public failure must not be
//	               enlarged into the LAN.
//	ScopeGlobal -> needs BOTH: the family must exist AND global egress must not be provably absent.
//	               Either term being unknown or provisioned means dialable - fail open.
//
// # Why "unknown" can never be refused
//
// Every field defaults to StateUnknown, so a capability nobody computed refuses nothing, and a
// platform that cannot read its route table reports GlobalIPv6Egress as unknown rather than as a
// guess. A network mid-transition leaves ownership unowned, and the policy wrapper refuses nothing
// for that case before this function is reached.
func (c FamilyCapability) HardUndialable(destination netip.Addr) bool {
	switch ClassifyScope(destination) {
	case ScopeLocal, ScopeSite, ScopeUnknown:
		return false
	case ScopeGlobal:
	default:
		return false
	}
	if c.HardUnavailable(destination.Unmap().Is4()) {
		return true
	}
	// The second term applies to global IPv6 only: "the public Internet is not reachable over IPv6"
	// says nothing about an IPv4 destination, and there is no equivalent IPv6-side fact that would
	// gate one.
	if destination.Unmap().Is6() {
		return c.GlobalIPv6Egress == StateUnavailable
	}
	return false
}

// Observed reports how many interfaces the evaluation was able to look at.
//
// It is a diagnostic: two capabilities that differ only in state are what a consumer compares, and
// this is what tells a reader whether a negative came from "we looked and found nothing" or from
// "there was nothing to look at".
func (c FamilyCapability) Observed() int {
	return c.reachable
}

// InterfaceObservation is one interface the local dial path could use, reduced to the single fact
// this model needs.
type InterfaceObservation struct {
	Name string
	// Up reports whether the interface can currently carry traffic.
	//
	// It is a field rather than a caller-side filter because the two producers of this input do not
	// agree. One of them - the manager's own list for clients with no platform interface - is only
	// ever populated with interfaces that are up, so a caller-side filter there would be dead code
	// that looks like a guarantee. The other hands over whatever the PLATFORM reported, and a platform
	// list may legitimately contain a link that is administratively up in the list while carrying
	// stale addressing from before it went down. Reading it here makes "addressing counts only where
	// it can be used" true for both inputs instead of true for the one that happened to be checked.
	Up bool
	// Addresses are the interface's own addresses. Whether they are global, unique-local or
	// link-local does not matter here; see Evaluate.
	Addresses []netip.Prefix
	// GlobalIPv6Route reports whether this interface has a NAVIGABLE global IPv6 route: a default
	// route (::/0), or any non-default route whose destination is itself global.
	//
	// # Why a route and not an address
	//
	// A global address proves the machine could be ADDRESSED; it does not prove anything can be
	// routed. The two come apart on exactly the machines this round exists for - a Windows box whose
	// only IPv6 is link-local has neither, and a box with a Hyper-V or WSL adapter carrying a global
	// address can still have no global route at all, because the adapter's prefix is on-link only.
	// The real failure mode is the one with the address and no route.
	//
	// # Why it is per interface
	//
	// Because "does this machine have a global IPv6 route" is answered by OR over the interfaces, and
	// the OR has to be taken over routes rather than over addresses. A virtual adapter's address says
	// nothing about whether the egress the dial will actually use can carry the packet.
	//
	// # Why the caller may leave it unknown
	//
	// A platform with no route-table reader reports it as unknown, and unknown contributes nothing -
	// so the capability degrades to the address-only answer it had before this field existed, rather
	// than to a guess. A route observation that FAILED is unknown for the same reason.
	GlobalIPv6Route RouteObservation
}

// RouteObservation is what an interface can say about routing to global IPv6 destinations.
type RouteObservation uint8

const (
	// RouteUnobserved means this interface's routes were not read, or could not be. It contributes
	// nothing to the evaluation: a platform without a route-table reader keeps its previous
	// behaviour exactly.
	RouteUnobserved RouteObservation = iota
	// RouteAbsent means this interface has no navigable global IPv6 route.
	//
	// It is the observation that separates "IPv6 exists on this machine" from "a global IPv6
	// destination can be reached from it", and it is the one the real failure log is full of.
	RouteAbsent
	// RoutePresent means this interface has a global IPv6 default route, or a route to a global IPv6
	// prefix.
	RoutePresent
)

// Evaluate derives the capability from the interfaces the local dial path can use.
//
// # Why absence of addressing is the only hard negative
//
// The check is: the machine has interfaces to look at, and none of them carries an address of this
// family. A socket of that family then has no source address to select, so an attempt is a
// guaranteed failure - the fast-fail case this whole round exists for.
//
// # Why "resolved to nothing" is not one of the inputs
//
// DNS answering without a family is a statement about a NAME, not about the machine. An empty AAAA
// answer, a NODATA response, or a name that is genuinely IPv4-only all look identical to a broken
// IPv6 stack if they are allowed to reach this function, and every one of them would then delete
// IPv6 from the local model. The caller therefore only ever passes the machine's own addressing,
// and the model has no parameter through which a resolver result could arrive.
//
// # Why the caller removes its own tunnels first
//
// The address list of this process's own TUN is not evidence about the network underneath it. A
// sing-box TUN carrying ::/0 and a global IPv6 address while the physical link has none is the
// ordinary shape of an IPv4-only network behind a dual-stack tunnel, and counting that address
// would make the model declare IPv6 available on the strength of sing-box's own route. The removal
// belongs to the caller because only the caller knows which interfaces are its own; see
// route.NetworkManager.
//
// # Link-local is a usable address and is not evidence of anything else
//
// A link-local address is kept: it is exactly what makes a link-local DESTINATION dialable, so
// discarding it would produce a hard "unavailable" that is simply wrong for that target. It is not
// promoted either - the interface is Provisioned, which claims only that the structure exists. In
// particular, a link-local-only interface never yields StateValidated and never yields "the public
// IPv6 Internet works".
//
// # Why an empty interface list is Unknown rather than Unavailable
//
// A manager that cannot enumerate interfaces must not thereby conclude that neither family exists;
// that would turn an observation failure into a hard filter. The failure is reported as Unknown and
// the previous behaviour is kept.
func Evaluate(interfaces []InterfaceObservation) FamilyCapability {
	if len(interfaces) == 0 {
		return FamilyCapability{IPv4: StateUnknown, IPv6: StateUnknown}
	}
	var (
		hasIPv4, hasIPv6 bool
		routePresent     bool
		routeObserved    bool
	)
	for _, observation := range interfaces {
		switch observation.GlobalIPv6Route {
		case RoutePresent:
			routePresent = true
			routeObserved = true
		case RouteAbsent:
			routeObserved = true
		}
		for _, prefix := range observation.Addresses {
			address := prefix.Addr()
			if !address.IsValid() {
				continue
			}
			switch {
			case address.Is4() || address.Is4In6():
				hasIPv4 = true
			case address.Is6():
				hasIPv6 = true
			}
		}
	}
	// The egress term, in one rule: a route that EXISTS on any interface is enough, because the
	// question is whether this machine has a way out at all and the kernel picks the interface. Only
	// when routes were actually read and none of them was navigable is the term a hard negative; if
	// nobody could read them, the term stays unknown and gates nothing.
	globalEgress := StateUnknown
	switch {
	case routePresent:
		globalEgress = StateProvisioned
	case routeObserved:
		globalEgress = StateUnavailable
	}
	capability := FamilyCapability{
		// An-interface-exists-but-carries-nothing-of-this-family is decisive, which is what
		// separates this from the empty-list case above.
		IPv4:             stateFor(hasIPv4),
		IPv6:             stateFor(hasIPv6),
		GlobalIPv6Egress: globalEgress,
		reachable:        len(interfaces),
	}
	return capability
}

func stateFor(present bool) FamilyState {
	if present {
		return StateProvisioned
	}
	return StateUnavailable
}

// UsableInterfaces reduces an interface list to the ones the local dial path can actually use, and
// reports whether anything was left.
//
// It is exported so that the platform-specific observers - and their tests - agree on what "usable"
// means instead of each re-deriving it:
//
//   - an interface that is not up cannot carry traffic, so it is not evidence of addressing. A
//     disconnected link keeps its address in the platform's list and loses nothing by doing so, which
//     is exactly why the check cannot live in the caller;
//   - this process's own interfaces (its TUN, and any bridge it opened) are split horizon rather
//     than underlay, so their addresses say nothing about the network underneath;
//   - loopback is always up and, on every supported platform, carries an address of BOTH families.
//     Keeping it would make "no interface has this family" unreachable on every machine, silently
//     disabling the hard check entirely, while contributing no routing information: a dial cannot
//     reach a remote destination through it.
//
// The boolean is the difference between "this machine has no usable interface" and "this machine
// has interfaces and none of them carries this family". Only the second is a hard negative, so the
// caller must not collapse them.
func UsableInterfaces(interfaces []InterfaceObservation, ownInterfaces []string) ([]InterfaceObservation, bool) {
	usable := make([]InterfaceObservation, 0, len(interfaces))
	for _, observation := range interfaces {
		if observation.Name == "" {
			// An unnamed interface cannot be matched against the caller's own list, so it cannot
			// be proven to be underlay. Excluded rather than guessed at.
			continue
		}
		if !observation.Up {
			continue
		}
		if slices.Contains(ownInterfaces, observation.Name) {
			continue
		}
		if isLoopbackOnly(observation.Addresses) {
			continue
		}
		usable = append(usable, observation)
	}
	return usable, len(usable) > 0
}

// isLoopbackOnly reports whether every address on the interface is a loopback address.
//
// The test is on the ADDRESSES rather than on a name or a flag. Names differ per platform and the
// loopback flag is not set consistently by every source this codebase reads interfaces from; an
// interface whose entire address set is loopback is the same thing on all of them.
func isLoopbackOnly(addresses []netip.Prefix) bool {
	if len(addresses) == 0 {
		return false
	}
	for _, prefix := range addresses {
		if !prefix.Addr().IsLoopback() {
			return false
		}
	}
	return true
}
