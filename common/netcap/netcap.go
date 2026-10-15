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
	// reachable is the interface count the evaluation was able to look at, excluding this process's
	// own tunnels and loopback. Zero means nothing could be observed, and it is why the zero value
	// of this struct reports both families as Unknown rather than as Unavailable.
	reachable int
}

// HardUnavailable reports whether a candidate of this family must not be spent an attempt on.
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
	// Addresses are the interface's own addresses. Whether they are global, unique-local or
	// link-local does not matter here; see Evaluate.
	Addresses []netip.Prefix
}

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
	var hasIPv4, hasIPv6 bool
	for _, observation := range interfaces {
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
	capability := FamilyCapability{
		// An-interface-exists-but-carries-nothing-of-this-family is decisive, which is what
		// separates this from the empty-list case above.
		IPv4:      stateFor(hasIPv4),
		IPv6:      stateFor(hasIPv6),
		reachable: len(interfaces),
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
//   - an interface that is not up cannot carry traffic, so it is not evidence of addressing;
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
