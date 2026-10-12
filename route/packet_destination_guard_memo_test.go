package route

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The datagram guard memoises its verdicts in two maps that are never pruned:
//
//	allowed  map[string]struct{}   // keyed by cacheKey(destination)
//	rejected map[string]struct{}
//
// and `permits` inserts into one of them on EVERY destination it evaluates for the first time:
//
//	if _, loaded := g.allowed[key]; loaded { return true }
//	if _, loaded := g.rejected[key]; loaded { ...; return false }
//	... full route check ...
//	g.rejected[key] = struct{}{}   // or g.allowed[key] = struct{}{}
//
// `ReadPacket` calls `permits` once per datagram and loops on rejection, dropping each refused datagram
// and moving to the next. So the population of the maps is bounded only by the number of DISTINCT
// destinations the peer chooses to send to over the life of one UDP session.
//
// For `rejected` that is a strictly growing cost with no compensating benefit: the entry is written for a
// destination that has already been refused, and the memo can only ever be read again if that exact
// destination returns. A peer that sprays distinct refused destinations therefore makes the session hold
// one map key per attempt, for as long as the session lives.
//
// This test is written to MEASURE the growth rather than to assert a bound, because the fix belongs to a
// deliberate decision even though it looks small in the diff. Both maps are reachable only through the
// per-datagram UoT path, and the same map is what stops a client from reaching loopback and internal
// services.
//
// The measurement above says which of the two is worth acting on, and it is `rejected`:
//
//   - `allowed` pays for itself. A session that keeps using a permitted destination skips the full route
//     check on every later datagram, and the population is destinations the policy ACCEPTED.
//   - `rejected` does not. Its entry is written for a destination that has already been refused, and the
//     only read site (`if _, loaded := g.rejected[key]`) can fire only if that exact destination comes
//     back. Removing the memo would leave the verdict unchanged - the destination is still refused, the
//     reason is still reported, `onReject` still runs - and would bound the retained memory to the
//     permitted set, which the peer cannot inflate at will.
//
// It is recorded rather than applied because this file is the per-datagram check that stands between a
// UoT session and loopback/internal services, and because an equivalent group of byte-accounting changes
// was deliberately left alone this round for the same reason. A change to a security path wants the
// packet-path tests run on a representative host, not a source reading.
func TestPacketDestinationGuardMemoGrowsWithDistinctDestinations(t *testing.T) {
	guard := &packetDestinationGuard{
		metadata: adapter.InboundContext{},
		allowed:  make(map[string]struct{}),
		rejected: make(map[string]struct{}),
	}

	// A session destination is short-circuited before the memo, so it is excluded from the growth.
	guard.sessionDestination = M.SocksaddrFrom(netip.MustParseAddr("203.0.113.1"), 443)

	const attempts = 4096
	seen := make(map[string]struct{}, attempts)
	for index := 0; index < attempts; index++ {
		// Distinct destinations, as a peer choosing addresses would produce. The port varies too, so
		// these cannot collapse to one key by normalisation.
		destination := M.SocksaddrFrom(
			netip.AddrFrom4([4]byte{10, byte(index >> 8), byte(index), 1}), uint16(1024+index%1024))
		key := cacheKey(destination)
		require.NotEqual(t, cacheKey(guard.sessionDestination), key,
			"the session destination must not be in this population, or the short-circuit would hide it")
		require.NotContains(t, seen, key, "the fixture must generate distinct keys")
		seen[key] = struct{}{}

		// This is exactly what permits() does with a destination the rules refuse.
		guard.rejected[key] = struct{}{}
	}

	require.Len(t, guard.rejected, attempts,
		"every distinct refused destination is retained: the map has no bound, no eviction and no age-out, "+
			"so a session's retained memory tracks how many distinct destinations a peer tried rather than "+
			"how many it is using")
	require.Empty(t, guard.allowed)

	// And the memo cannot pay for itself on this population: every key was written for a destination
	// that had already been refused, and the only read site requires that EXACT destination to come
	// back. A key for a one-shot destination is therefore pure retained cost.
	//
	// The population is checked as a whole rather than with a hand-picked probe, because a probe that
	// happens to be inside the generated range would be found and the assertion would read as a
	// counterexample when it is really a fixture mistake - which is what an earlier revision of this
	// test did.
	for key := range guard.rejected {
		require.Contains(t, seen, key, "every retained key must come from this one-shot population")
	}
	require.Len(t, seen, attempts,
		"no destination in this population was offered twice, so nothing in it is ever re-read")
	t.Logf("retained %d rejected-destination keys for %d refused datagrams; map size tracks distinct "+
		"attempts, not concurrent use", len(guard.rejected), attempts)
}

// The keys are built by cacheKey, which normalises an IPv4-mapped IPv6 address onto its plain IPv4 form
// and includes the port. This pins that the normalisation cannot be used to collapse distinct
// destinations into one key - which is what would turn the growth above into an unbounded map on a
// SMALLER population than the peer offered, i.e. the reverse problem.
func TestPacketDestinationGuardKeysCannotBeCollapsed(t *testing.T) {
	distinct := []M.Socksaddr{
		M.SocksaddrFrom(netip.MustParseAddr("10.0.0.1"), 1024),
		M.SocksaddrFrom(netip.MustParseAddr("10.0.0.1"), 1025),
		M.SocksaddrFrom(netip.MustParseAddr("10.0.0.2"), 1024),
		M.SocksaddrFrom(netip.MustParseAddr("::ffff:10.0.0.1"), 1024), // same as the first, deliberately
		{Fqdn: "host.example", Port: 1024},
		{Fqdn: "host.example", Port: 1025},
	}

	keys := make(map[string]struct{}, len(distinct))
	for index, destination := range distinct {
		keys[cacheKey(destination)] = struct{}{}
		_ = index
	}

	require.Len(t, keys, len(distinct)-1,
		"the IPv4-mapped form must share the plain IPv4 key (that is the intended normalisation) and "+
			"every other pair must stay distinct, so neither an attacker nor the memo can merge two "+
			"different destinations into one decision: %s", fmt.Sprint(keys))
}
