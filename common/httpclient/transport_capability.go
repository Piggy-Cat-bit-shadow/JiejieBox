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
// # Why this is a check about PROVABLE impossibility, not about capability in general
//
// `Network()` is what an object says it can carry. When it reports a non-empty set that does not
// include a network this client provably needs, the mismatch is unavoidable and the configuration is
// unusable - that is the case this refuses. When it reports NOTHING, the object is not claiming
// anything, so there is no proof either way and the configuration is left alone: treating "reports
// nothing" as "carries nothing" would refuse objects that simply do not implement the method.
//
// # What this deliberately does NOT model
//
// A GROUP's `Network()` is an advertisement, not a per-member guarantee, and a non-filtering group
// may hand the flow to a member that cannot carry it. That is the same limitation the reachable-leaf
// dry run documents, and duplicating a member walk here would be a second, weaker answer to a
// question physicalpath already owns. So a group passes this check on its own advertisement and is
// judged by the dry run for its members.
func CheckDetourCapability(clientTag string, detour adapter.Outbound, requirement TransportRequirement) error {
	if detour == nil {
		// A nil object is not a capability statement. MEASURED: `Outbound(tag)` can report
		// `loaded == true` while handing back a nil interface, and dereferencing it panicked the
		// start path - so this is a guard against a real shape, not defensive padding.
		return nil
	}
	carries := detour.Network()
	if len(carries) == 0 {
		// The object claims nothing, so nothing is provable. Accepting is the honest answer.
		return nil
	}
	for _, network := range requirement.Required {
		if !slices.Contains(carries, network) {
			// The carried set is JOINED rather than passed as a slice: MEASURED, formatting a []string
			// argument through this tree's exception builder panics with "unknown value", and a
			// diagnostic that crashes the start path is worse than no diagnostic - the same lesson as
			// the tag-clash message in adapter/outbound.
			return E.New("http_client[", clientTag, "] dials through outbound ", detour.Tag(),
				", which does not carry ", network, " (it carries ", strings.Join(carries, ","),
				") while this client provably needs it: with version ",
				clientVersionLabel(requirement), " every request would fail rather than fall back")
		}
	}
	return nil
}

// clientVersionLabel renders WHICH configured selection produced the requirement, so the refusal
// names the fact the operator has to change rather than only the network that is missing.
func clientVersionLabel(requirement TransportRequirement) string {
	if slices.Contains(requirement.Required, "udp") {
		if slices.Contains(requirement.Required, "tcp") {
			return "3 (HTTP/3 with the HTTP/2 fallback, which reaches TCP as well as UDP)"
		}
		return "3 with disable_version_fallback (HTTP/3 alone, which is UDP)"
	}
	return "1/2 (HTTP over TCP)"
}
