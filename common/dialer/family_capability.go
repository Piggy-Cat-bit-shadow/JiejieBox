package dialer

import (
	"net/netip"

	"github.com/sagernet/sing-box/common/netcap"
	E "github.com/sagernet/sing/common/exceptions"
)

// localFamilyPolicy is what this dialer learned, at the START of a dial, about which address
// families the local machine can actually dial.
//
// # Scope
//
// It describes the LOCAL dial path only. It is therefore applied where a socket is created on this
// machine and nowhere else: the destination a remote proxy is asked to reach is the remote peer's
// business, and this machine's addressing must never rewrite it. That distinction is structural
// rather than a rule someone has to remember - a detour never reaches DefaultDialer.DialContext,
// because the detour chain hands the request to another outbound instead of dialling it, so a
// capability read here cannot reach a remote target even in principle.
//
// The same reasoning makes this correct for the proxy's OWN address, which is the case that looks
// like an exception and is not one: the A/AAAA of a proxy server is dialled by this machine, so it
// is a local dial and this machine's capability is exactly the right question to ask about it.
//
// # Why it is a captured value and not a live query
//
// It is read once, atomically with the network ownership pair, at the moment the dial begins. A dial
// that started on one network and was answered on another is rejected by the epoch check that
// already exists; re-reading the capability mid-dial would only add a second, weaker opinion about
// the same interval.
//
// A change to the capability is deliberately NOT a network transition. Losing an IPv6 address does
// not move the reset epoch, invalidate DNS caches or tear down live streams, and making it do so
// would turn an ordinary short outage into a connection storm. What it does mean is that a dial in
// flight may be allowed to start on evidence that has since changed - and that is safe, because the
// address it produces is a real connection on a real socket, and the case this file is about (no
// address of that family anywhere) fails immediately on its own if the structure really is gone.
type localFamilyPolicy struct {
	capability netcap.FamilyCapability
}

// LocalFamilyDialer is implemented by a dialer that can answer whether the local machine has a
// usable path for an address's family.
//
// # Why the native-bypass path needs it
//
// A bypassed flow is a connect performed by the platform, so it never passes through the candidate
// scheduler that enforces the verdict. That is immaterial on every stack except Linux redirect mode,
// where an ActionBypass really does leave this process - and there, a provably undialable family
// would be handed to the operating system to spend the whole connect timeout rediscovering what the
// network manager already knew.
//
// A refusal here is therefore a restoration of the userspace behaviour rather than a new policy: the
// flow falls back to the path that knows the family is unavailable and fails it immediately, with a
// reason that names the machine instead of a bare timeout.
type LocalFamilyDialer interface {
	LocalFamilyBlocked(address netip.Addr) bool
}

// snapshotLocalFamilyPolicy reads the current ownership pair and family capability as one
// observation.
//
// A manager that cannot report a capability yields the zero value, which restricts nothing: the
// dialer keeps its previous behaviour exactly rather than losing a family because a struct was left
// empty.
func (d *DefaultDialer) snapshotLocalFamilyPolicy() localFamilyPolicy {
	if d.familySnapshot == nil {
		return localFamilyPolicy{}
	}
	_, _, capability := d.familySnapshot()
	return localFamilyPolicy{capability: capability.Capability}
}

// newLocalFamilyPolicy builds a policy from a capability, for callers that already hold one.
func newLocalFamilyPolicy(capability netcap.FamilyCapability) localFamilyPolicy {
	return localFamilyPolicy{capability: capability}
}

// excluded reports whether a candidate of this family must not be spent an attempt on.
//
// The test is on the PLAN's classification rather than on the address, so that the one place that
// decides which family an address belongs to stays classifyAddress - including its treatment of an
// IPv4-mapped IPv6 address as IPv4.
func (p localFamilyPolicy) excluded(family addressFamily) bool {
	switch family {
	case familyIPv4:
		return p.capability.HardUnavailable(true)
	case familyIPv6:
		return p.capability.HardUnavailable(false)
	default:
		return false
	}
}

// excludedAddress is excluded for a caller that holds an address and no classification.
func (p localFamilyPolicy) excludedAddress(address netip.Addr) bool {
	return p.excluded(classifyAddress(address))
}

// LocalFamilyBlocked implements LocalFamilyDialer.
//
// It reports whether this machine has no usable path for the address's family, and it answers false
// for a local-scope destination for the same reason the dial path does: a link-local, unique-local,
// loopback or unspecified address is not reached over the public family the verdict describes.
func (d *DefaultDialer) LocalFamilyBlocked(address netip.Addr) bool {
	if !address.IsValid() || isLocalScopeAddress(address) {
		return false
	}
	return d.snapshotLocalFamilyPolicy().excludedAddress(address)
}

// hardUnavailableError reports the refusal for a family the machine cannot dial.
//
// # Why the message names the family and the reason, and no address
//
// The refusal is identical for every candidate of that family, so the address would be noise - and
// a caller that logs one refusal per address would look like a per-destination failure when it is a
// property of the machine. The reason matters for the opposite purpose: an operator who sees IPv6
// attempts skipped needs to be able to tell "this device has no IPv6 addressing at all" from "your
// configuration asked for IPv4 only", which is a different message produced elsewhere
// (AddressAllowedByStrategy).
//
// # Why the values are stored rather than built on each call
//
// A refusal is a property of the machine, so two refusals for the same family ARE the same refusal.
// Storing them makes that comparison available to callers and tests - errors.Is is the ordinary way
// to ask "was this refused because the machine has no IPv6", and rebuilding the error on each call
// would make every such check fail while looking correct.
var (
	errLocalIPv4Unavailable = E.New("local network has no usable ipv4 path: no interface on this machine carries an ipv4 address")
	errLocalIPv6Unavailable = E.New("local network has no usable ipv6 path: no interface on this machine carries an ipv6 address")
)

func hardUnavailableError(family addressFamily) error {
	switch family {
	case familyIPv4:
		return errLocalIPv4Unavailable
	case familyIPv6:
		return errLocalIPv6Unavailable
	default:
		// No family was identified, so the refusal cannot name one. This is reachable only from a
		// caller that lost the classification, and it must not silently look like a family verdict.
		return E.New("local network has no usable path for this destination")
	}
}

// isLocalScopeAddress reports whether a destination is reached over something other than the public
// address family this file makes a verdict about.
//
// # Why the capability must not touch these
//
// The structural check says "this machine has no address of family F anywhere", which is a claim
// about reaching remote destinations of F. The destinations below are not reached that way:
//
//   - loopback never leaves the host;
//   - link-local (fe80::/10, 169.254/16) is scoped to one link, and an interface can carry a
//     link-local address of a family while carrying no global one of it - which is the normal
//     state of an interface whose IPv6 is not routed. Refusing it would delete a working local
//     service on the strength of a verdict about the Internet;
//   - unique-local (fc00::/7) is the IPv6 counterpart of RFC 1918 space, and a campus or hospital
//     network that blackholes PUBLIC IPv6 routinely still serves ULA internally. This is the
//     concrete case the design is required not to collapse;
//   - the unspecified address means "let the kernel choose", which a caller does when it is
//     deliberately indifferent to the family - so there is no family to have a verdict about.
//
// # Why this is a scope test and not a public/private guess
//
// It tests the SCOPE of the destination address, which is a property of the address itself and is
// decidable without a route lookup. It deliberately does not attempt to classify "is this target on
// the public Internet", which would be a guess about routing dressed up as a fact about an address.
func isLocalScopeAddress(address netip.Addr) bool {
	if !address.IsValid() {
		return false
	}
	return address.IsLoopback() ||
		address.IsLinkLocalUnicast() ||
		address.IsPrivate() ||
		address.IsUnspecified()
}
