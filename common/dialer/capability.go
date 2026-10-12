package dialer

import (
	"slices"
	"strings"

	"github.com/sagernet/sing-box/adapter"
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"
)

// CheckDetourCapability reports whether the outbound a consumer dials through can carry the
// transports that consumer provably needs, and when it cannot, why.
//
// # Why this lives in common/dialer
//
// It is a statement about a DIALER's detour, and every package that owns one already depends on this
// one: the DNS transport packages form a chain (`dns` -> `dns/transport` -> `dns/transport/quic`),
// so a helper they all need cannot live in any of them without a cycle. This is the lowest package
// that can see a `DetourDialer` and an `adapter.Outbound` at once.
//
// # Why the DETOUR is inspected rather than the tag re-looked-up
//
// `DetourDialer.init` already resolved the tag, and its accessor is memoised behind a `sync.Once`.
// Re-deriving the object from the manager would be a second lookup that could disagree with the one
// the dial actually uses; reading the resolved object means the check judges exactly the object the
// traffic will go through.
//
// # Why only PROVABLE mismatches are refused
//
// An object that reports NO networks is not making a claim, so nothing is provable and the
// configuration is accepted. Treating "reports nothing" as "carries nothing" would refuse objects
// that simply do not implement `Network()` - the false-deny direction this whole line of work
// exists to remove. The same reasoning applies when there is no detour at all: the dial goes out
// through the default path, which is not a declared object to judge.
func DetourOf(wrapped N.Dialer) (adapter.Outbound, bool) {
	return resolvedDetour(wrapped)
}

func CheckDetourCapability(consumerName string, wrapped N.Dialer, required []string) error {
	if len(required) == 0 {
		return nil
	}
	detour, resolved := resolvedDetour(wrapped)
	if !resolved || detour == nil {
		return nil
	}
	carries := detour.Network()
	if len(carries) == 0 {
		return nil
	}
	for _, network := range required {
		if !slices.Contains(carries, network) {
			// The carried set is JOINED rather than passed as a slice: MEASURED, formatting a
			// []string argument through this tree's exception builder panics with "unknown value",
			// and a diagnostic that crashes the start path is worse than no diagnostic - the same
			// lesson as the tag-clash message in adapter/outbound.
			return E.New(consumerName, " dials through outbound ", detour.Tag(),
				", which does not carry ", network, " (it carries ", strings.Join(carries, ","),
				") while this consumer provably needs it: every request would fail rather than ",
				"fall back")
		}
	}
	return nil
}

// resolvedDetour returns the outbound a detour dialer has resolved, and whether a detour was named
// at all.
//
// # Why it WALKS the wrapper chain rather than asserting once
//
// `dialer.NewWithOptions` returns the `DetourDialer` itself only when nothing else has to wrap it. A
// transport whose SERVER address is a domain gets a `ResolveDialer` around it (`RemoteIsDomain:
// true`), and a bare type assertion then finds no detour at all and silently reports "nothing to
// check" - MEASURED: the HTTPS DNS transport over a UDP-only detour STARTED, because
// `dns.NewRemoteDialer` sets `RemoteIsDomain` and the assertion missed the wrapper. A validator that
// skips a case without saying so is worse than one with no opinion, so the chain is unwrapped
// through `Upstream()` until a DetourDialer is found or the chain ends.
func resolvedDetour(wrapped N.Dialer) (adapter.Outbound, bool) {
	for current := wrapped; current != nil; {
		detourDialer, isDetour := current.(*DetourDialer)
		if !isDetour {
			upstream, isUpstream := current.(interface{ Upstream() any })
			if !isUpstream {
				return nil, false
			}
			next, isDialer := upstream.Upstream().(N.Dialer)
			if !isDialer {
				return nil, false
			}
			current = next
			continue
		}
		// The NAME is read first: `Dialer()` is what triggers the resolution, and a preflight must
		// not perform one as a side effect of merely asking. A dialer with no name dials the DEFAULT
		// outbound, which is not a declared object to judge.
		if detourDialer.DetourName() == "" {
			return nil, false
		}
		resolved, err := detourDialer.Dialer()
		if err != nil {
			return nil, false
		}
		// The manager hands back an adapter.Outbound, but the accessor is typed as N.Dialer because
		// that is all a dialer needs. An object that is not an outbound has no Network() to judge, so
		// it is reported as unresolved rather than as a mismatch.
		outbound, isOutbound := resolved.(adapter.Outbound)
		if !isOutbound {
			return nil, false
		}
		return outbound, true
	}
	return nil, false
}
