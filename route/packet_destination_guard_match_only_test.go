package route

import (
	"context"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// A UoT session is authorised ONCE, from its session destination, and every later datagram is
// judged by packetDestinationGuard. The guard copies the session metadata and clears the fields
// that belong to the old target - DestinationAddresses, Domain, Protocol - because a datagram to
// another destination is a different flow target.
//
// Address evidence published by `resolve` with `match_only: true` is exactly the same kind of
// per-target fact, and it is the most dangerous one to inherit: nothing rewrites the destination
// between datagrams, so a stale list would silently satisfy a CIDR or private-IP condition for a
// destination it was never resolved for. A session to a name that resolves into a rejected range
// could then be used to reach a destination that was never approved - or the reverse, refusing
// traffic that the rules allow.

// guardStaticDNS answers from a fixed table, so a datagram's own resolution is the only input.
type guardStaticDNS struct {
	adapter.DNSRouter
	answers map[string][]netip.Addr
}

func (r *guardStaticDNS) Lookup(ctx context.Context, domain string, options adapter.DNSQueryOptions) ([]netip.Addr, error) {
	return r.answers[domain], nil
}

func guardPrefixable(cidr string) *badoption.Prefixable {
	prefix := badoption.Prefixable(netip.MustParsePrefix(cidr))
	return &prefix
}

// TestPacketDestinationGuardDoesNotInheritMatchOnlyEvidence is the differential: the same rule, the
// same session evidence and two datagrams - one whose own address is in the rejected range, and one
// whose is not - plus the pre-fix behaviour reproduced deliberately as a negative control.
func TestPacketDestinationGuardDoesNotInheritMatchOnlyEvidence(t *testing.T) {
	rejectRule, err := R.NewDefaultRule(context.Background(), log.NewNOPFactory().Logger(), option.DefaultRule{
		RawDefaultRule: option.RawDefaultRule{
			IPCIDR: []*badoption.Prefixable{guardPrefixable("198.51.100.0/24")},
		},
		RuleAction: option.RuleAction{Action: C.RuleActionTypeReject},
	})
	require.NoError(t, err)

	router := &Router{
		ctx:    context.Background(),
		logger: log.NewNOPFactory().Logger(),
		dns: &guardStaticDNS{answers: map[string][]netip.Addr{
			"other.example":  {netip.MustParseAddr("203.0.113.5")},
			"inside.example": {netip.MustParseAddr("198.51.100.10")},
		}},
		rules: []adapter.Rule{rejectRule},
	}

	sessionMetadata := adapter.InboundContext{
		Destination:    M.Socksaddr{Fqdn: "session.example", Port: 443},
		MatchAddresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")},
	}
	guard := newPacketDestinationGuard(context.Background(), router, &guardScriptedSource{}, sessionMetadata, nil)

	// The datagram's OWN answer is outside the rejected range, so the session's evidence must not
	// decide it.
	permitted := guard.permits(M.Socksaddr{Fqdn: "other.example", Port: 443})
	require.True(t, permitted,
		"a datagram must be judged by its own destination; the session's match-only evidence is about "+
			"the session destination")

	// The control, in the same guard and against the same rule: a datagram whose own resolution IS
	// in the range is still refused, so the assertion above is not passing because the rule is inert.
	require.False(t, guard.permits(M.Socksaddr{Fqdn: "inside.example", Port: 443}),
		"the rule must still refuse a destination that resolves into its range")

	// And the negative control: the pre-fix metadata (the same copy, keeping the field) really does
	// change the verdict. Without this, the pair above could pass on a guard that never consults the
	// field at all.
	stale := sessionMetadata
	stale.Destination = M.Socksaddr{Fqdn: "other.example", Port: 443}
	stale.DestinationAddresses = nil
	stale.Domain = ""
	stale.Protocol = ""
	stale.RouteRule = ""
	stale.RouteOutbound = ""
	stale.OutboundChain = nil
	stalePeril, err := router.checkPacketDestination(context.Background(), &stale)
	require.NoError(t, err)
	require.False(t, stalePeril,
		"control: inheriting the session's match-only evidence WOULD refuse this datagram, which is "+
			"the pollution the guard's clear prevents")
}
