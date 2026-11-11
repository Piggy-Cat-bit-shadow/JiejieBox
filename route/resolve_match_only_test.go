package route

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Tests for the PUBLICATION half of `resolve` with `match_only: true`.
//
// The lookup itself is shared with the historical action, so nothing here re-tests DNS: these cases
// pin the one decision the option makes - which per-flow list the answer lands in - and the
// lifecycle that keeps that list describing the flow's current target.

// countingDNS records what the action asked it, so "did this actually run" is measurable.
type countingDNS struct {
	adapter.DNSRouter
	addresses []netip.Addr
	err       error
	calls     int
	domains   []string
}

func (r *countingDNS) Lookup(ctx context.Context, domain string, options adapter.DNSQueryOptions) ([]netip.Addr, error) {
	r.calls++
	r.domains = append(r.domains, domain)
	return r.addresses, r.err
}

func newResolveTestRouter(resolver adapter.DNSRouter) *Router {
	return &Router{
		ctx:    context.Background(),
		logger: log.NewNOPFactory().Logger(),
		dns:    resolver,
	}
}

func resolvedAddresses() []netip.Addr {
	return []netip.Addr{netip.MustParseAddr("198.51.100.10")}
}

// The core of the option: the answer selects rules and is NOT a dial candidate.
func TestResolveMatchOnlyPublishesMatchingEvidenceOnly(t *testing.T) {
	resolver := &countingDNS{addresses: resolvedAddresses()}
	router := newResolveTestRouter(resolver)
	metadata := &adapter.InboundContext{Destination: M.Socksaddr{Fqdn: "match.example", Port: 443}}

	err := router.actionResolve(context.Background(), metadata, &R.RuleActionResolve{MatchOnly: true})
	require.NoError(t, err)

	require.Equal(t, 1, resolver.calls, "the lookup must actually run; matching evidence cannot be invented")
	require.Equal(t, []string{"match.example"}, resolver.domains)
	require.Equal(t, resolvedAddresses(), metadata.MatchAddresses,
		"the answer must be published as matching evidence")
	require.Empty(t, metadata.DestinationAddresses,
		"and it must NOT become a dial candidate: that is what keeps the name on the wire")
}

// The control: without the option nothing changed.
func TestResolveDefaultStillBuildsDialCandidates(t *testing.T) {
	resolver := &countingDNS{addresses: resolvedAddresses()}
	router := newResolveTestRouter(resolver)
	metadata := &adapter.InboundContext{Destination: M.Socksaddr{Fqdn: "candidate.example", Port: 443}}

	err := router.actionResolve(context.Background(), metadata, &R.RuleActionResolve{})
	require.NoError(t, err)

	require.Equal(t, 1, resolver.calls)
	require.Equal(t, resolvedAddresses(), metadata.DestinationAddresses,
		"the historical action must keep producing the candidate list")
	require.Empty(t, metadata.MatchAddresses,
		"and it must not also publish matching evidence, or a rule would read the same answer twice")
}

// A literal destination with a sniffed name: the action runs because the name is what can be
// answered, but the literal stays the flow's target, so there is nothing to dial differently.
func TestResolveMatchOnlyOnLiteralDestinationPublishesNoCandidate(t *testing.T) {
	resolver := &countingDNS{addresses: resolvedAddresses()}
	router := newResolveTestRouter(resolver)
	metadata := &adapter.InboundContext{
		Destination: M.SocksaddrFrom(netip.MustParseAddr("10.1.2.3"), 443),
		Domain:      "sniffed.example",
	}

	err := router.actionResolve(context.Background(), metadata, &R.RuleActionResolve{MatchOnly: true})
	require.NoError(t, err)

	require.Equal(t, []string{"sniffed.example"}, resolver.domains,
		"the sniffed name is the only resolvable input, so it is what the action asks for")
	require.Equal(t, resolvedAddresses(), metadata.MatchAddresses)
	require.Empty(t, metadata.DestinationAddresses,
		"an IP connection must not acquire dial candidates from a match-only action")
	require.Equal(t, netip.MustParseAddr("10.1.2.3"), metadata.Destination.Addr,
		"and the literal destination must not be rewritten into the sniffed name")
}

// Nothing resolvable means nothing published - not an empty answer that replaces real evidence.
func TestResolveWithoutALookupNamePublishesNothing(t *testing.T) {
	resolver := &countingDNS{addresses: resolvedAddresses()}
	router := newResolveTestRouter(resolver)
	metadata := &adapter.InboundContext{
		Destination:    M.SocksaddrFrom(netip.MustParseAddr("203.0.113.5"), 443),
		MatchAddresses: []netip.Addr{netip.MustParseAddr("10.0.0.7")},
	}

	err := router.actionResolve(context.Background(), metadata, &R.RuleActionResolve{MatchOnly: true})
	require.NoError(t, err)

	require.Zero(t, resolver.calls,
		"a literal destination with no sniffed name has nothing to resolve, so no query is made")
	require.Equal(t, []netip.Addr{netip.MustParseAddr("10.0.0.7")}, metadata.MatchAddresses,
		"and evidence that is still about this target must be left alone")
}

// A dialling resolve is the newer statement about the target, so it supersedes matching evidence:
// otherwise a rule could keep matching an address the newest answer no longer returns.
func TestDialResolveSupersedesMatchOnlyAnswer(t *testing.T) {
	resolver := &countingDNS{addresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")}}
	router := newResolveTestRouter(resolver)
	metadata := &adapter.InboundContext{Destination: M.Socksaddr{Fqdn: "chain.example", Port: 443}}

	require.NoError(t, router.actionResolve(context.Background(), metadata, &R.RuleActionResolve{MatchOnly: true}))
	require.NotEmpty(t, metadata.MatchAddresses)

	resolver.addresses = []netip.Addr{netip.MustParseAddr("203.0.113.9")}
	require.NoError(t, router.actionResolve(context.Background(), metadata, &R.RuleActionResolve{}))

	require.Empty(t, metadata.MatchAddresses,
		"the superseded matching evidence must be cleared, or a rule would match the older answer")
	require.Equal(t, []netip.Addr{netip.MustParseAddr("203.0.113.9")}, metadata.DestinationAddresses)
}

// Two match-only resolutions: the second replaces the first. Keeping both would mean "match if
// either answer said so", which is not this option's meaning.
func TestSecondMatchOnlyResolveReplacesTheFirst(t *testing.T) {
	resolver := &countingDNS{addresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")}}
	router := newResolveTestRouter(resolver)
	metadata := &adapter.InboundContext{Destination: M.Socksaddr{Fqdn: "twice.example", Port: 443}}

	require.NoError(t, router.actionResolve(context.Background(), metadata, &R.RuleActionResolve{MatchOnly: true}))

	resolver.addresses = []netip.Addr{netip.MustParseAddr("203.0.113.9")}
	require.NoError(t, router.actionResolve(context.Background(), metadata, &R.RuleActionResolve{MatchOnly: true}))

	require.Equal(t, []netip.Addr{netip.MustParseAddr("203.0.113.9")}, metadata.MatchAddresses)
	require.Empty(t, metadata.DestinationAddresses)
}

// A destination rewrite creates a different target: the old answer must not judge the new one.
func TestOverrideAddressClearsMatchOnlyAnswer(t *testing.T) {
	metadata := &adapter.InboundContext{
		Destination:    M.Socksaddr{Fqdn: "before.example", Port: 443},
		MatchAddresses: []netip.Addr{netip.MustParseAddr("10.0.0.7")},
	}

	applyRouteOptionsMetadata(metadata, &R.RuleActionRouteOptions{
		OverrideAddress: M.SocksaddrFrom(netip.MustParseAddr("198.51.100.10"), 0),
	})

	require.Empty(t, metadata.MatchAddresses,
		"address evidence about the pre-rewrite destination must not survive the rewrite")
	require.Empty(t, metadata.DestinationAddresses,
		"the dial candidates were already cleared by the same rule; both must move together")
	require.Equal(t, netip.MustParseAddr("198.51.100.10"), metadata.Destination.Addr)
}

// A port-only rewrite keeps the address and therefore keeps the evidence.
func TestOverridePortKeepsMatchOnlyAnswer(t *testing.T) {
	metadata := &adapter.InboundContext{
		Destination:    M.Socksaddr{Fqdn: "same.example", Port: 443},
		MatchAddresses: []netip.Addr{netip.MustParseAddr("10.0.0.7")},
	}

	applyRouteOptionsMetadata(metadata, &R.RuleActionRouteOptions{OverridePort: 8443})

	require.Equal(t, []netip.Addr{netip.MustParseAddr("10.0.0.7")}, metadata.MatchAddresses,
		"the destination address did not change, so its address evidence still describes this flow")
}

// The configuration field must reach the runtime action, and the action must say so in its own
// string form - the log line and the route preview are both written by String().
func TestNewRuleActionCarriesAndDescribesMatchOnly(t *testing.T) {
	logger := log.NewNOPFactory().Logger()

	action, err := R.NewRuleAction(logger, option.RuleAction{
		Action:         C.RuleActionTypeResolve,
		ResolveOptions: option.RouteActionResolve{MatchOnly: true},
	})
	require.NoError(t, err)
	resolveAction, isResolve := action.(*R.RuleActionResolve)
	require.True(t, isResolve)
	require.True(t, resolveAction.MatchOnly, "the option must be carried into the runtime action")
	require.Contains(t, resolveAction.String(), "match_only",
		"the action must be identifiable in logs and previews; got %q", resolveAction.String())

	plainAction, err := R.NewRuleAction(logger, option.RuleAction{
		Action: C.RuleActionTypeResolve,
	})
	require.NoError(t, err)
	require.False(t, plainAction.(*R.RuleActionResolve).MatchOnly)
	require.NotContains(t, plainAction.String(), "match_only",
		"an action without the option must not claim it")
	require.Equal(t, "resolve", plainAction.String(),
		"and the default form must stay exactly what it was")
}

// A failed lookup publishes NOTHING. The action's error aborts the flow (the existing contract for
// every resolve action), and a half-published or retained answer would be worse than the error:
// rules would decide on evidence this target never produced.
func TestResolveMatchOnlyFailurePublishesNothing(t *testing.T) {
	resolver := &countingDNS{err: errors.New("lookup failed")}
	router := newResolveTestRouter(resolver)
	metadata := &adapter.InboundContext{
		Destination:          M.Socksaddr{Fqdn: "fails.example", Port: 443},
		DestinationAddresses: []netip.Addr{netip.MustParseAddr("203.0.113.9")},
	}

	err := router.actionResolve(context.Background(), metadata, &R.RuleActionResolve{MatchOnly: true})
	require.Error(t, err, "the lookup's failure must reach the caller")
	require.Empty(t, metadata.MatchAddresses, "a failed lookup must not publish matching evidence")
	require.Equal(t, []netip.Addr{netip.MustParseAddr("203.0.113.9")}, metadata.DestinationAddresses,
		"and must not touch candidates another action legitimately produced")
}

// An EMPTY answer replaces the old evidence instead of leaving it in place. The production DNS
// router reports "empty result" as an error, so this shape is what a different resolver
// implementation can produce - and the field must not become "keep whatever matched last time".
func TestResolveMatchOnlyEmptyAnswerClearsStaleEvidence(t *testing.T) {
	resolver := &countingDNS{addresses: []netip.Addr{}}
	router := newResolveTestRouter(resolver)
	metadata := &adapter.InboundContext{
		Destination:    M.Socksaddr{Fqdn: "empty.example", Port: 443},
		MatchAddresses: []netip.Addr{netip.MustParseAddr("10.0.0.7")},
	}

	require.NoError(t, router.actionResolve(context.Background(), metadata, &R.RuleActionResolve{MatchOnly: true}))
	require.Empty(t, metadata.MatchAddresses,
		"an empty answer must clear the previous one, not leave a stale address matching rules")
}

// Matching evidence must never be what lets a flow skip the userspace path: a bypass would hand the
// packet to the platform, which would dial the literal destination instead of the name the proxy is
// supposed to resolve.
func TestMatchOnlyEvidenceNeverAuthorisesAFastBypass(t *testing.T) {
	router := &Router{ctx: context.Background(), logger: log.NewNOPFactory().Logger()}
	packetDestination := M.SocksaddrFrom(netip.MustParseAddr("203.0.113.5"), 443)

	named := adapter.InboundContext{
		InboundType:    C.TypeTun,
		Network:        "tcp",
		Destination:    M.Socksaddr{Fqdn: "match.example", Port: 443},
		MatchAddresses: resolvedAddresses(),
	}
	require.Equal(t, BypassRefusedDomainDestination,
		router.canFastBypass(&named, packetDestination, nil, nil, false),
		"a named destination is refused before any address evidence is consulted")

	// The shape a resolve action produces for a literal destination: the literal plus a sniffed
	// name. The sniffed name is what refuses the bypass, and the matching evidence must not be able
	// to change that answer.
	sniffed := adapter.InboundContext{
		InboundType:    C.TypeTun,
		Network:        "tcp",
		Destination:    M.SocksaddrFrom(netip.MustParseAddr("203.0.113.5"), 443),
		Domain:         "sniffed.example",
		MatchAddresses: resolvedAddresses(),
	}
	require.Equal(t, BypassRefusedSniffedDomain,
		router.canFastBypass(&sniffed, packetDestination, nil, nil, false))
}
