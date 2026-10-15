package dialer

import (
	"net/netip"

	"github.com/sagernet/sing-box/common/netcap"
	E "github.com/sagernet/sing/common/exceptions"
)

// localDialOwnership is the network ownership pair a dial captured when it began, plus the means to
// ask whether that capture is still the live one.
//
// # Why this is a named type rather than two loose locals
//
// The pair (epoch, startedStable) and the predicate derived from it already existed inside
// captureEpoch. What did NOT exist was any way for a second consumer - the family verdict - to be
// bound to the same capture. Keeping them as loose locals meant the verdict was read on its own, at a
// different instant, and then applied to a decision that already belonged to a different network.
//
// # The three states it distinguishes
//
//	owns            captured stable and the epoch has not moved   -> the capability may restrict
//	startedUnstable captured during a transition                  -> the capability must not restrict
//	epochMoved      a transition began after the capture          -> the capability must not restrict
//
// The first two are what captureEpoch's predicate already distinguishes; `owns` collapses them into
// the one question a consumer has to ask, and the underlying predicate keeps the finer semantics for
// the hand-over check.
type localDialOwnership struct {
	// epoch is the reset epoch the dial captured. It is what the hand-over check compares against,
	// and it is kept here so a reader of a policy can see which generation it describes.
	epoch uint64
	// startedStable records whether the network was settled when the dial began. A dial that began
	// during a transition has no stable network for a verdict to be about.
	startedStable bool
	// predicate is the existing ownership check, or nil when no ordering constraint applies because
	// the manager cannot report an epoch.
	predicate func() bool
}

// owns reports whether the network generation this capture describes is still the live one.
//
// A capture with no predicate - a manager that cannot report an epoch - owns by construction: the
// distinction the check exists for does not arise, which is the same degradation every other optional
// capability in this codebase takes. It is also the shape a test-constructed policy has, so a test
// that is not about ownership does not have to simulate one.
func (o localDialOwnership) owns() bool {
	if o.predicate == nil {
		return true
	}
	return o.predicate()
}

// localFamilyPolicy is the family verdict a dial captured, bound to the network ownership of the same
// capture.
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
// # Why the verdict is useless without its ownership
//
// The verdict is a fact about ONE network. Applying it while the network is unsettled, or after it
// has changed, is how an old network's answer decides a new network's dial - the failure this round
// exists to close, and one the race detector cannot see, because every variable involved is correctly
// synchronized and only the ORDER is wrong.
//
// The verdict is therefore never read directly. It is consulted through excluded/excludedAddress,
// which refuse to restrict anything unless the capture still owns the network. That is Invariant A
// (an unstable snapshot cannot hard deny) and Invariant B (a superseded generation cannot hard deny)
// enforced in one place, so no caller can forget either: there is no exported field to read that
// skips the check.
//
// # Why a change to the capability is deliberately NOT a network transition
//
// Losing an IPv6 address does not move the reset epoch, invalidate DNS caches or tear down live
// streams, and making it do so would turn an ordinary short outage into a connection storm. What it
// does mean is that a dial in flight may be allowed to start on evidence that has since changed - and
// that is safe, because the address it produces is a real connection on a real socket, and the case
// this file is about (no address of that family anywhere) fails immediately on its own if the
// structure really is gone.
type localFamilyPolicy struct {
	ownership  localDialOwnership
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
//
// # Why an uncertain answer is NO BLOCK rather than a refusal
//
// This is a boolean about an OPTIMISATION, not about a connection. Answering "blocked" only disables
// the fast path and sends the flow down the userspace path, which is correct but slower - so a stale
// or unsettled capture must answer "not blocked" and leave the bypass available. That is the opposite
// polarity from a dial gate, where the conservative direction is to refuse, and it is why the two
// consume the same policy through the same accessor and still differ only in the polarity of their
// answer.
type LocalFamilyDialer interface {
	LocalFamilyBlocked(address netip.Addr) bool
}

// captureLocalFamilyPolicy reads the network ownership and the family verdict as ONE observation.
//
// This is the only production entry point, and it exists so that no caller can obtain a verdict
// without the ownership that makes it meaningful: the manager produces both inside its own critical
// section, and this drops neither. A caller that wants only the verdict has no way to ask for it.
//
// The ownership predicate is the existing one - captureEpoch - rather than a second implementation of
// "is this still my network". It already distinguishes BEFORE, DURING and AFTER correctly, including
// the DURING case that a bare epoch comparison cannot see, and reusing it is what keeps this file from
// growing a third transition protocol.
//
// A manager that cannot report a capability yields the zero value, which restricts nothing: the
// dialer keeps its previous behaviour exactly rather than losing a family because a struct was left
// empty.
func (d *DefaultDialer) captureLocalFamilyPolicy() localFamilyPolicy {
	var policy localFamilyPolicy
	// OWNERSHIP IS CAPTURED EVEN WHEN THE CAPABILITY IS NOT, and the order of these two blocks is
	// load-bearing.
	//
	// Ownership is not a property of this feature: it is the dial's existing claim on a network
	// generation, and the hand-over check depends on it. Returning early for a manager that cannot
	// report a capability - which is every manager but the real one - would silently take that claim
	// away and hand back connections dialled across a reset. That is exactly the defect this ordering
	// was written to fix, and it is why the capability is the OPTIONAL half and the ownership is not.
	if d.networkEpoch != nil {
		policy.ownership.predicate = d.captureEpoch()
	}
	if d.familySnapshot == nil {
		return policy
	}
	epoch, stable, capability := d.familySnapshot()
	policy.capability = capability.Capability
	policy.ownership.epoch = epoch
	policy.ownership.startedStable = stable
	return policy
}

// newLocalFamilyPolicy builds a policy that is OWNED BY CONSTRUCTION, for callers and tests that hold
// a verdict with no network ordering to bind it to.
//
// "Owned by construction" is the right default here for the same reason a manager without an epoch
// is: with no network lifecycle in the picture there is no transition for the verdict to be stale
// against, so the ownership check does not apply rather than passing by luck. A test that wants to
// attack ownership builds the ownership explicitly.
func newLocalFamilyPolicy(capability netcap.FamilyCapability) localFamilyPolicy {
	return localFamilyPolicy{capability: capability}
}

// excluded reports whether a candidate of this family must not be spent an attempt on, in the network
// generation this policy was captured for.
//
// The test is on the PLAN's classification rather than on the address, so that the one place that
// decides which family an address belongs to stays classifyAddress - including its treatment of an
// IPv4-mapped IPv6 address as IPv4.
func (p localFamilyPolicy) excluded(family addressFamily) bool {
	if family == familyInvalid {
		return false
	}
	// The ownership check comes FIRST and is not optional. A verdict captured on a network that has
	// since changed describes a network this attempt is not on; a verdict captured while the network
	// was unsettled describes none at all. In both cases the only safe reading is "no opinion", which
	// fails open.
	if !p.ownership.owns() {
		return false
	}
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

// restrictsNothing reports whether this policy cannot refuse anything, so the common path can skip
// the work of examining a candidate list at all.
//
// An unowned policy restricts nothing, because that is what excluded answers for it - the two must
// never disagree, or the fast path would skip a filter that would have removed something.
func (p localFamilyPolicy) restrictsNothing() bool {
	if !p.ownership.owns() {
		return true
	}
	return !p.capability.HardUnavailable(true) && !p.capability.HardUnavailable(false)
}

// LocalFamilyBlocked implements LocalFamilyDialer.
//
// It reports whether this machine has no usable path for the address's family, and it answers false
// for a local-scope destination for the same reason the dial path does: a link-local, unique-local,
// loopback or unspecified address is not reached over the public family the verdict describes.
//
// An unsettled or superseded capture answers false - no block - which leaves the bypass available and
// changes nothing about where the flow goes. See LocalFamilyDialer for why that direction is the
// conservative one here while it is the unsafe one on a dial gate.
func (d *DefaultDialer) LocalFamilyBlocked(address netip.Addr) bool {
	if !address.IsValid() || isLocalScopeAddress(address) {
		return false
	}
	return d.captureLocalFamilyPolicy().excludedAddress(address)
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
//
// # Why this error can only be produced by an OWNED policy
//
// The message makes a claim about the CURRENT network. A stale verdict that reached this error would
// be reporting a property of a network the device has left as though it were a property of the one it
// is on, which is worse than reporting nothing. The ownership check in excluded is what guarantees
// that: by the time this error is constructed, the capture was settled and still current.
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
