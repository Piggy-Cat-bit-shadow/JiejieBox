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
// This is the only production entry point, and it exists so that no caller can obtain a verdict without
// the ownership that makes it meaningful.
//
// # What changed, and why the previous shape could not be fixed in place
//
// It used to be a branch tree: `familySnapshot` if present, otherwise `captureEpoch`. And `captureEpoch`
// was ITSELF a branch tree over three other optional interfaces, with its own precedence. Two
// resolutions, two precedence orders, one manager - so which interface an answer came from depended on
// which of the two functions was asked. The same defect was found and fixed in both of them, in
// different commits, in the same shape: a manager that reported a fact was read through an accessor
// that could not carry it, and the answer it gave was dropped.
//
// There is now one resolution, taken at construction (see networkObserver), and this function is a
// projection of it. It cannot prefer a weaker source than captureEpoch does, because there is only one
// source for it to prefer.
//
// A manager that cannot report a capability yields the zero value, which restricts nothing: the dialer
// keeps its previous behaviour exactly rather than losing a family because a struct was left empty.
func (d *DefaultDialer) captureLocalFamilyPolicy() localFamilyPolicy {
	observation := d.network.observe()
	return localFamilyPolicy{
		ownership:  d.network.ownership(observation),
		capability: observation.family.Capability,
	}
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

// localFamilyGate is ONE evaluation of a policy, for a caller that must judge several candidates inside a
// single decision.
//
// # Why the batch form exists
//
// `hardUndialable` consults network ownership on every call, which is correct for a single destination:
// the ownership check is what makes the verdict meaningful, and it cannot be pre-computed for a caller
// that asks once. A candidate FILTER is not that caller. It asks the same question of every address in one
// plan, and reading ownership per address means the plan's first candidate and its last can be judged
// against two different generations - a transition landing mid-filter would have the head of the list
// dropped for a verdict that no longer applies while the tail is kept under "no opinion". Every variable
// is correctly synchronised; the plan is simply composed from two network generations.
//
// The gate fixes both halves at once: ownership is read ONCE, and the whole list is judged against it. It
// also removes N-1 manager reads per plan, which is the difference between one short lock per connection
// and one per candidate.
type localFamilyGate struct {
	policy localFamilyPolicy
	// owned is the single ownership read this gate was built from. False means the capture has been
	// superseded or was taken during a transition, and then the gate refuses nothing - the direction the
	// design requires, because a stale verdict must not deny a candidate it no longer describes.
	owned bool
}

// gate evaluates the policy's ownership once.
func (p localFamilyPolicy) gate() localFamilyGate {
	return localFamilyGate{policy: p, owned: p.ownership.owns()}
}

// undialable reports whether an attempt at this destination must not be made, in the generation this gate
// was built from.
func (g localFamilyGate) undialable(destination netip.Addr) bool {
	if !g.owned || !destination.IsValid() {
		return false
	}
	return g.policy.capability.HardUndialable(destination)
}

// restrictsNothing reports whether this gate cannot refuse anything, so the common path can skip the work
// of examining a candidate list at all.
//
// It must agree with undialable exactly: a fast path that said "nothing is restricted" while the slow path
// would have dropped a candidate is the one disagreement these two functions cannot have.
func (g localFamilyGate) restrictsNothing() bool {
	if !g.owned {
		return true
	}
	return !g.policy.capability.HardUnavailable(true) &&
		!g.policy.capability.HardUnavailable(false) &&
		g.policy.capability.GlobalIPv6Egress != netcap.StateUnavailable
}

// hardUndialable reports whether an attempt at this destination must not be made, in the network
// generation this policy was captured for.
//
// # This is the single gate, and it is the only thing callers ask
//
// The destination goes in and a yes/no comes out. Everything that decides it - address scope, family
// existence, global IPv6 egress, and network ownership - is resolved inside, and deliberately:
//
//	scope        local / site / global, from the address itself
//	family       does the machine have addressing of this family at all
//	egress       for a GLOBAL IPv6 destination, is the public path provably absent
//	ownership    is this capture still the live network generation
//
// Those terms used to be composed at five call sites - the literal gate, the packet gate, the
// candidate scheduler, the serial resolve path and the native-bypass profile - each spelling out
// "is this destination local-scope, and if not is its family unavailable". That composition was
// complete for the family question and blind to the egress one, which is exactly how the reported
// Windows outage got through: every copy asked the right question and none of them knew the second
// question existed.
//
// Taking the destination rather than a family is what makes a single gate possible. A caller that
// holds only a family classification cannot express "global IPv6", so it cannot be asked to remember
// the egress term - and a rule that callers must remember is a rule that will be forgotten.
//
// # The ownership check is not optional and comes first
//
// A verdict captured on a network that has since changed describes a network this attempt is not on;
// a verdict captured while the network was unsettled describes none at all. In both cases the only
// safe reading is "no opinion", which fails open.
func (p localFamilyPolicy) hardUndialable(destination netip.Addr) bool {
	if !destination.IsValid() {
		return false
	}
	if !p.ownership.owns() {
		return false
	}
	return p.capability.HardUndialable(destination)
}

// hardUndialableFamily is the same question for a caller that holds a FAMILY and no address.
//
// It exists for the plan-level fast path, which asks whether a policy could refuse anything at all
// before walking a candidate list. It deliberately answers the family question only: a plan is
// filtered candidate by candidate through hardUndialable, so the egress term is applied to the
// addresses that actually carry it.
func (p localFamilyPolicy) hardUndialableFamily(family addressFamily) bool {
	if family == familyInvalid {
		return false
	}
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

// restrictsNothing reports whether this policy cannot refuse anything, so the common path can skip
// the work of examining a candidate list at all.
//
// An unowned policy restricts nothing, because that is what the gate answers for it - the two must
// never disagree, or the fast path would skip a filter that would have removed something.
//
// It includes the egress term. A policy that can refuse a global IPv6 destination restricts
// something, and leaving it out here would let the fast path walk past a filter that would have
// removed candidates - the one disagreement between these two functions that is not merely
// redundant.
func (p localFamilyPolicy) restrictsNothing() bool {
	if !p.ownership.owns() {
		return true
	}
	return !p.capability.HardUnavailable(true) &&
		!p.capability.HardUnavailable(false) &&
		p.capability.GlobalIPv6Egress != netcap.StateUnavailable
}

// LocalFamilyBlocked implements LocalFamilyDialer.
//
// It reports whether an attempt at this destination must not be made on the local dial path, and it
// answers false for a local-scope or site-local destination because neither is reached over the
// public path this verdict describes: a link-local address is what makes a link-local destination
// work, and a campus that blackholes public IPv6 still serves ULA.
//
// An unsettled or superseded capture answers false - no block - which leaves the bypass available and
// changes nothing about where the flow goes. See LocalFamilyDialer for why that direction is the
// conservative one here while it is the unsafe one on a dial gate.
func (d *DefaultDialer) LocalFamilyBlocked(address netip.Addr) bool {
	return d.captureLocalFamilyPolicy().hardUndialable(address)
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
// The third refusal is the one this round added, and it is NOT the same fact as the second.
//
// "No interface carries an ipv6 address" and "ipv6 exists on the link and nothing routes it to the
// public Internet" are different states of the machine with different remedies, and collapsing them
// into one message would make the log say the machine has no IPv6 on a machine whose IPv6 is fine and
// merely local. The reported outage is the second case, so its message has to be able to say so.
var (
	errLocalIPv4Unavailable       = E.New("local network has no usable ipv4 path: no interface on this machine carries an ipv4 address")
	errLocalIPv6Unavailable       = E.New("local network has no usable ipv6 path: no interface on this machine carries an ipv6 address")
	errLocalIPv6EgressUnavailable = E.New(
		"local network has no public ipv6 path: this machine has ipv6 addressing but no route to a global ipv6 destination " +
			"(link-local and unique-local destinations are unaffected)")
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

// hardUndialableError is the refusal reason for a destination this policy refuses, or nil.
//
// It is the gate and the explanation in one call, so a caller cannot refuse a destination and then
// report a family verdict that does not describe why - which is what would happen if the reason were
// derived from classifyAddress alone.
func (p localFamilyPolicy) hardUndialableError(destination netip.Addr) error {
	if !p.hardUndialable(destination) {
		return nil
	}
	family := classifyAddress(destination)
	// A global IPv6 destination is refused by one of two facts, and only the first is a statement
	// about the family.
	if family == familyIPv6 && !p.capability.HardUnavailable(false) {
		return errLocalIPv6EgressUnavailable
	}
	return hardUnavailableError(family)
}
