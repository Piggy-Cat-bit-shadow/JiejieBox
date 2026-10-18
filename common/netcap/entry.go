package netcap

// This file is the ENTRY half of the family model.
//
// # The question is different from the dial question, and that is why it is a separate answer
//
// `HardUndialable` answers "may this attempt be made", per destination, at the moment a socket is
// about to be created. It is a refusal, it is cheap, and a refusal costs one candidate.
//
// An ENTRY answers a different question: what does the system tell applications about IPv6 before
// any attempt exists? A TUN that carries a global IPv6 address and a ::/0 route, a DNS server that
// answers AAAA, a Fake-IP range that hands out synthetic IPv6 addresses - each of those is a claim
// that IPv6 works, made to software this process does not control, on every connection and often
// for the whole lifetime of the process. A claim cannot be withdrawn per attempt, and an
// application that has already chosen IPv6 does not necessarily retry.
//
// So the entry decision is: should this process present IPv6 to applications at all, on this
// network? It is answered once, from the same observed facts as the dial verdict, and consumed by
// every entry (the TUN's effective options and the application-facing DNS answer).
//
// # Why the entry answer is allowed to be less informed than the dial answer would like
//
// Because the alternative is a per-application, per-connection model of what "works", which no
// entry can compute: a name's AAAA is a fact about a NAME, and whether the address it carries is
// reachable depends on the route a connection that does not exist yet will take. The entry layer is
// therefore deliberately coarser than the dial layer: it suppresses a whole address family from
// applications only when the machine can prove that NO local IPv6 destination of the presented
// class is reachable, and it leaves every finer question to the dial gate that already answers it.
//
// The direction of the error is deliberate and is the same direction as the rest of this package:
// an unproven family is PRESENTED. A missed suppression costs an attempt the dial gate refuses on
// the next line; a wrong suppression deletes a working family from every application at once, and
// no later layer can put it back.

// IPv6Presentment is what the entry layer should do about IPv6 for the network the decision was
// made on.
//
// It is not a per-destination verdict and must never be used as one: `HardUndialable` remains the
// single gate for an attempt. This value only decides whether the entries expose IPv6 at all.
type IPv6Presentment uint8

const (
	// PresentIPv6 keeps the behaviour of a build without this feature: the entries carry whatever
	// IPv6 the configuration gave them, and the dial gate decides per destination.
	//
	// It is the zero value, so a decision that was never computed, a manager that cannot report
	// one, a test double that does not implement the capability, and every entry constructed
	// before the decision exists all behave exactly as they did before.
	PresentIPv6 IPv6Presentment = iota
	// SuppressIPv6 means no local IPv6 destination is reachable, so the entries must not advertise
	// it: the TUN is opened without IPv6 addressing or routes, and an application-facing AAAA query
	// is answered with an empty NOERROR.
	//
	// It is only ever produced from evidence that satisfies the rules in ProjectIPv6Presentment.
	SuppressIPv6
)

func (p IPv6Presentment) String() string {
	switch p {
	case PresentIPv6:
		return "present"
	case SuppressIPv6:
		return "suppress"
	default:
		return "invalid"
	}
}

// IPv6Addressing is the SHAPE of the machine's IPv6 addressing, which the family state alone
// cannot describe.
//
// # Why the state is not enough
//
// `FamilyCapability.IPv6 = Provisioned` is true for a machine with a global address, for a campus
// machine with a unique-local address, and for a Windows box whose only IPv6 is the fe80:: address
// every adapter carries. Those three machines can reach completely different sets of destinations:
//
//	global       the public Internet, and anything on-link
//	site (ULA)   the site, which a network that blackholes public IPv6 routinely still serves
//	link-local   the link, and nothing that an entry could carry: a link-local destination names
//	             the interface it is on, and no TUN is that interface
//
// A presentment decision that treated "IPv6 exists" as one fact would have to call the fe80::
// machine "IPv6 capable" - which is exactly the false advertisement this round exists to remove -
// or call the ULA machine "not capable" and delete working LAN IPv6. So the shape is classified
// separately, from the same addresses the state was derived from, and the decision reads both.
type IPv6Addressing uint8

const (
	// AddressingUnknown means there was nothing to look at, or the machine's addressing could not
	// be observed. It fails open: see ProjectIPv6Presentment.
	AddressingUnknown IPv6Addressing = iota
	// AddressingNone means no usable interface carries an IPv6 address.
	//
	// It is NOT sufficient on its own to conclude that IPv6 is impossible: an interface that is up
	// and has not been addressed yet - the ordinary state during a handover - produces the same
	// shape, and there it proves nothing. ProjectIPv6Presentment is where the two are told apart,
	// through the family state, which is a hard negative only when some interface did produce an
	// address and none of them was IPv6.
	AddressingNone
	// AddressingLinkOnly means every IPv6 address the machine has is link-local. A link-local
	// destination names the interface it lives on, so a TUN's IPv6 addressing is not what makes one
	// reachable - the physical link is - and an entry that presents IPv6 on the strength of a
	// link-local address is presenting an address family nothing on the public path can carry.
	AddressingLinkOnly
	// AddressingSite means at least one unique-local (fc00::/7) address exists. Such a machine can
	// reach LAN services over IPv6 whether or not the public path exists, which is why the entry
	// keeps IPv6 presented even when the public egress term is a hard negative.
	AddressingSite
	// AddressingGlobal means at least one global IPv6 address exists and no unique-local one does.
	// This is the only shape whose presentment additionally depends on the public egress term.
	AddressingGlobal
)

func (a IPv6Addressing) String() string {
	switch a {
	case AddressingNone:
		return "none"
	case AddressingLinkOnly:
		return "link-local-only"
	case AddressingSite:
		return "site"
	case AddressingGlobal:
		return "global"
	default:
		return "unknown"
	}
}

// ClassifyIPv6Addressing reduces the interfaces an entry decision may believe to the shape of
// their IPv6 addressing.
//
// It must be given the SAME list Evaluate was given - the usable interfaces - so that the state and
// the shape describe one observation of one network. Passing the raw interface list would let a
// down interface or this process's own tunnel contribute a global address while contributing
// nothing to the state, and the two halves of one decision would then disagree about the machine.
//
// The precedence is deliberate:
//
//	site beats global   a unique-local address means LAN IPv6 works, and the entry must not delete
//	                    it because the public path is missing
//	global beats link   a global address is what makes the public path possible at all
//
// An IPv4-mapped IPv6 address is not IPv6 addressing: ::ffff:192.0.2.1 is a representation of an
// IPv4 address, and counting it would report a global IPv6 address on a machine that has none.
func ClassifyIPv6Addressing(interfaces []InterfaceObservation) IPv6Addressing {
	if len(interfaces) == 0 {
		return AddressingUnknown
	}
	var (
		linkLocal bool
		global    bool
		site      bool
	)
	for _, observation := range interfaces {
		for _, prefix := range observation.Addresses {
			address := prefix.Addr()
			if !address.IsValid() {
				continue
			}
			address = address.Unmap()
			if !address.Is6() {
				continue
			}
			switch {
			case address.IsLinkLocalUnicast():
				linkLocal = true
			case address.IsPrivate():
				// Unique-local: fc00::/7. A campus that blackholes public IPv6 routinely still
				// serves this, and an entry that presented IPv4 only would break it.
				site = true
			default:
				global = true
			}
		}
	}
	switch {
	case site:
		return AddressingSite
	case global:
		return AddressingGlobal
	case linkLocal:
		return AddressingLinkOnly
	default:
		return AddressingNone
	}
}

// ProjectIPv6Presentment derives the entry decision from one observation of one network.
//
// # The three hard facts, and nothing else
//
// It reads only facts the machine can prove about itself:
//
//	no IPv6 address anywhere              no IPv6 destination can be reached, at any scope
//	link-local addressing only            the same, for every destination an entry could carry
//	global addressing, no global route    the public path is structurally missing
//
// and it suppresses only on one of those. Everything else is `PresentIPv6`, including every
// unknown: an unread route table, a platform whose route reader is a stub, a network that is
// changing, an empty interface list.
//
// # Why AddressingNone is not decided by itself
//
// AddressingNone is produced both by "the machine has IPv4 addressing and no IPv6" - which proves
// the family impossible - and by "the interfaces are up and none has been addressed yet", which is
// what a handover looks like for a few seconds and proves NOTHING. The two are the same address
// shape and must not be the same verdict, and the family state is what tells them apart: the state
// is StateUnavailable only when at least one usable interface produced an address and none of them
// was IPv6. So the family term carries the AddressingNone case, and this function never suppresses
// on the shape alone.
//
// # Why a site-local address keeps IPv6 presented even with no public egress
//
// fc00::/7 is the IPv6 counterpart of RFC 1918 space, and a network that blackholes public IPv6
// routinely still serves it. The cost of the two errors is not symmetric: presenting IPv6 on a
// ULA-only network exposes applications to AAAA answers for public names that will fail (the dial
// gate refuses each one immediately), while suppressing it deletes working access to LAN services
// for every application at once, and nothing later can tell an application to try again.
//
// # Why the global-address-without-a-route case IS suppressed
//
// It is the shape the field report is made of: an adapter carrying a global IPv6 address whose
// prefix is on-link only, so every global IPv6 destination dies in the kernel and no local IPv6
// destination exists either. There is nothing for the entry to advertise, and this term is the
// only one that catches it - the family state is Provisioned, because the address is real.
//
// # Why the address-shape tests come before the egress term
//
// The egress term is a statement about the public Internet, and it is the one term a platform may
// legitimately be unable to read. If it were consulted first, a platform with no route reader
// would be decided by a term it cannot produce; ordering it last behind the address facts keeps
// the "no IPv6 at all" and "link-local only" cases decidable from addressing alone, on every
// platform, while an unreadable route table leaves every other case presented.
func ProjectIPv6Presentment(capability FamilyCapability, addressing IPv6Addressing) IPv6Presentment {
	if addressing == AddressingUnknown {
		// Nothing was observed, so there is nothing to conclude. Refusing to present IPv6 on an
		// observation failure would turn a transient enumeration problem into a family outage.
		return PresentIPv6
	}
	if capability.IPv6 == StateUnavailable {
		// The family verdict is the model's own hard negative: at least one usable interface
		// produced an address, and none of them carries an IPv6 one. It agrees with AddressingNone
		// by construction, and is checked first for the reason in the note above: it is the term
		// that distinguishes "no IPv6" from "not addressed yet".
		return SuppressIPv6
	}
	switch addressing {
	case AddressingLinkOnly:
		// Every IPv6 address is link-local. A link-local destination names the interface it lives
		// on, and a TUN is never that interface, so there is nothing here an entry could carry.
		return SuppressIPv6
	case AddressingGlobal:
		if capability.GlobalIPv6Egress == StateUnavailable {
			return SuppressIPv6
		}
	}
	return PresentIPv6
}
