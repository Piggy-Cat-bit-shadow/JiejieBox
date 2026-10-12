package httpclient

import (
	"slices"
	"strings"

	"github.com/sagernet/sing-box/adapter"
	E "github.com/sagernet/sing/common/exceptions"
)

// CheckDetourCapability reports whether an outbound can serve this client's transport requirement,
// and when it provably cannot, why.
//
// # The rule
//
// Refuse only when NO request can be served:
//
//	AllOf  every element must be carried           (a conjunctive requirement)
//	AnyOf  at least one element must be carried    (a disjunctive requirement)
//
// `Conditional` is never tested. It names a network that some request class needs, and refusing on
// it would reject a client that serves every ordinary request - the false-deny direction this model
// exists to remove.
//
// # Why absence of proof accepts
//
// An object that reports NO networks is not making a claim, so nothing is provable and the
// configuration is accepted. Treating "reports nothing" as "carries nothing" would refuse objects
// that simply do not implement `Network()`.
func CheckDetourCapability(clientTag string, detour adapter.Outbound, requirement TransportRequirement) error {
	if detour == nil {
		// A nil object is not a capability statement. MEASURED: `Outbound(tag)` can report
		// `loaded == true` while handing back a nil interface, and dereferencing it panicked the
		// start path - so this is a guard against a real shape, not defensive padding.
		return nil
	}
	carries := detour.Network()
	if len(carries) == 0 {
		return nil
	}
	for _, network := range requirement.AllOf {
		if !slices.Contains(carries, network) {
			return E.New("http_client[", clientTag, "] dials through outbound ", detour.Tag(),
				", which does not carry ", network, " (it carries ", joinNetworks(carries), "); ",
				"this client has no other transport, so no request can be made at all")
		}
	}
	if len(requirement.AnyOf) > 0 && !slices.ContainsFunc(requirement.AnyOf, func(network string) bool {
		return slices.Contains(carries, network)
	}) {
		return E.New("http_client[", clientTag, "] dials through outbound ", detour.Tag(),
			", which carries ", joinNetworks(carries), " and therefore neither ",
			joinNetworks(requirement.AnyOf), "; this client needs at least one of them, so no ",
			"request can be made at all")
	}
	return nil
}

// joinNetworks renders a network set for a diagnostic.
//
// It JOINS rather than passing the slice: MEASURED, formatting a []string argument through this
// tree's exception builder panics with "unknown value", and a diagnostic that crashes the start path
// is worse than no diagnostic - the same lesson as the tag-clash message in adapter/outbound.
func joinNetworks(networks []string) string {
	return strings.Join(networks, ",")
}
