package dialer

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// A DOMAIN destination on the packet arm that has no candidate list.
//
// # The gap this closes
//
// `route/conn.go` picks between three packet arms on `len(metadata.DestinationAddresses) > 0`, and a domain
// flow is the shape that arrives with an empty list - so it takes the literal-and-domain arm rather than the
// candidate walk. No test drove a domain through it, and it is the only packet arm that resolves a name
// itself before binding.
//
// It is also where a per-flow `network_strategy` / `fallback_delay` lands without a scheduler to consume it:
// the interface selection happens in the leaf instead. The candidate and connected-UDP columns of the parity
// matrix say nothing about this arm, which is why it gets its own file.
//
// # What this file can and cannot prove, stated because it cost a false negative to learn
//
// The family gate for this arm belongs to the LEAF: `N.ListenSerial` walks the resolved answers by calling
// the leaf's `ListenPacket` directly, so a fixture whose leaf is a stub bypasses the gate entirely and will
// happily bind an answer the machine cannot reach. That is a property of the harness, not of production -
// `DefaultDialer.ListenPacket` refuses before creating a socket, and the third column of
// `TestALocalFamilyFastFailIsAppliedOnEveryPath` asserts exactly that against the real leaf.
//
// So this file proves the arm resolves and binds (which nothing else did), and deliberately does not claim
// to prove the gate here.

// TestADomainDestinationOnTheLiteralArmResolvesAndBinds is the positive case.
func TestADomainDestinationOnTheLiteralArmResolvesAndBinds(t *testing.T) {
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyAsIS)
	chain.inner.packetConn = &countingPacketConn{}
	chain.inner.stall = nil
	chain.inner.connectDelay = 0

	resolved := netip.MustParseAddr("112.53.55.92")
	chain.router.addressesA = []netip.Addr{resolved}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := chain.resolver.ListenPacket(ctx, M.ParseSocksaddrHostPort("example.com", 443))
	require.NoError(t, err, "a domain destination must resolve on this arm and bind to an answer")
	require.NotNil(t, conn)
	conn.Close()

	require.Equal(t, []netip.Addr{resolved}, chain.tried(),
		"the socket is created for the RESOLVED address, exactly once - this arm has no candidates to race "+
			"and no scheduler to reorder them")
	require.GreaterOrEqual(t, chain.lookupCalls(), int32(1),
		"and the name was resolved by this arm rather than handed on unresolved")
}

// TestADomainDestinationOnTheLiteralArmDoesNotReachTheResolverForALiteral is the control that keeps the
// per-destination cost of this arm honest.
//
// The literal branch must not spend a lookup: a flow that already carries an address has nothing to resolve,
// and a resolver call there would be a DNS query per connection on the path that exists to avoid one.
func TestADomainDestinationOnTheLiteralArmDoesNotReachTheResolverForALiteral(t *testing.T) {
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyAsIS)
	chain.inner.packetConn = &countingPacketConn{}
	chain.inner.stall = nil
	chain.inner.connectDelay = 0

	literal := netip.MustParseAddr("112.53.55.92")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := chain.resolver.ListenPacket(ctx, M.SocksaddrFrom(literal, 443))
	require.NoError(t, err)
	conn.Close()

	require.Zero(t, chain.lookupCalls(), "a literal destination must not cost a lookup on any arm")
	require.Equal(t, []netip.Addr{literal}, chain.tried())
}
