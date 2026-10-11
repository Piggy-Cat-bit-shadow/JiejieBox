package transport

import (
	"slices"
	"strings"

	"github.com/sagernet/sing-box/adapter"

	E "github.com/sagernet/sing/common/exceptions"
)

// CheckDetourCapability reports whether the outbound this transport dials through can carry what
// the transport provably needs, and when it cannot, why.
//
// # The rule
//
// Refuse only when NO query can be served:
//
//	AllOf  every element must be carried          (a conjunctive requirement)
//	AnyOf  at least one element must be carried   (a disjunctive requirement)
//
// `Conditional` is never tested. It names a network that some runtime branch needs, and refusing on
// it would reject a resolver that answers every ordinary query - the false-deny direction this model
// exists to remove.
//
// # What this deliberately does NOT do
//
// It does not resolve the detour itself. The caller passes the object its dialer already resolved
// (see common/dialer.CheckDetourCapability, which reads the DetourDialer's memoised answer), so this
// stays a pure predicate over an `adapter.Outbound` and is unit-testable without a Box.
//
// A detour that carries NOTHING is accepted, because "reports nothing" is not a claim - treating it
// as "carries nothing" would refuse objects that simply do not implement `Network()`.
func CheckDetourCapability(consumerName string, detour adapter.Outbound, requirement TransportRequirement) error {
	if detour == nil {
		return nil
	}
	carries := detour.Network()
	if len(carries) == 0 {
		return nil
	}
	for _, network := range requirement.AllOf {
		if !slices.Contains(carries, network) {
			return E.New(consumerName, " dials through outbound ", detour.Tag(),
				", which does not carry ", network, " (it carries ", strings.Join(carries, ","),
				"); this transport has no other path, so no query can succeed")
		}
	}
	if len(requirement.AnyOf) > 0 && !slices.ContainsFunc(requirement.AnyOf, func(network string) bool {
		return slices.Contains(carries, network)
	}) {
		return E.New(consumerName, " dials through outbound ", detour.Tag(), ", which carries ",
			strings.Join(carries, ","), " and therefore neither ", strings.Join(requirement.AnyOf, ","),
			"; this transport needs at least one of them, so no query can succeed")
	}
	return nil
}
