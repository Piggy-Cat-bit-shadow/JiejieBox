package dialer

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// DNS-environment provenance on a candidate (§C).
//
// # The defect these pin
//
// The DNS client deliberately delivers an in-flight answer after the DNS environment changed; the
// answer was true when it was asked, and refusing it would silently break a documented contract. What
// must not happen is that answer taking the launch slot from one that describes the environment the
// socket is about to be created in. A split-horizon name whose address moved would otherwise send
// the new connection to the address the previous environment selected.
//
// The three boundaries are covered separately: the scheduler's choice (a superseded candidate does
// not displace a current one), the last-resort rule (it is still dialled when nothing current
// exists), and the published connection (the socket target belongs to an address the current
// environment produced).

// supersededScheduler builds a scheduler whose "now" is the given DNS generation.
func supersededScheduler(delay time.Duration, generation uint64) *candidateScheduler {
	return &candidateScheduler{
		fallbackDelay: delay,
		dnsGeneration: func() (uint64, bool) { return generation, true },
	}
}

// TestSupersededAnswerDoesNotDisplaceACurrentOne is the ordering rule.
//
// The first attempt is a current-generation address that silently blackholes. The other family then
// answers from the PREVIOUS environment and is healthy, and a second current address arrives behind
// it. The launch slot must go to the current address: the superseded one would win the race by being
// first, which is exactly the wrong outcome - a working socket to an address the environment has
// left.
func TestSupersededAnswerDoesNotDisplaceACurrentOne(t *testing.T) {
	const fallbackDelay = 40 * time.Millisecond

	first := netip.MustParseAddr("192.0.2.1")   // current, silently blackholed
	stale := netip.MustParseAddr("2001:db8::1") // superseded environment, healthy
	second := netip.MustParseAddr("192.0.2.92") // current, healthy

	inner := newStreamOrderDialer(first)
	late := make(chan dualStackCandidate, 3)
	late <- dualStackCandidate{address: first, family: familyIPv4, sourceGeneration: 2, sourceKnown: true}
	late <- dualStackCandidate{address: stale, family: familyIPv6, sourceGeneration: 1, sourceKnown: true}
	late <- dualStackCandidate{address: second, family: familyIPv4, sourceGeneration: 2, sourceKnown: true}
	close(late)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	scheduler := supersededScheduler(fallbackDelay, 2)
	conn, winner, err := scheduler.dialWithLateCandidates(ctx,
		planCandidates(nil, netip.Addr{}, C.DomainStrategyPreferIPv4), late, inner.attempt)
	require.NoError(t, err)
	require.Equal(t, second, winner,
		"the superseded answer must not take the launch slot from a current one; attempts: %v",
		inner.attempts())
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())
}

// TestSupersededAnswerIsStillDialledWhenItIsAllThereIs is the last-resort rule.
//
// A superseded answer is ordered behind current ones; it is never refused. A DNS environment flap
// must cost a launch slot, not a connection.
func TestSupersededAnswerIsStillDialledWhenItIsAllThereIs(t *testing.T) {
	stale := netip.MustParseAddr("2001:db8::1")

	inner := newStreamOrderDialer()
	late := make(chan dualStackCandidate, 1)
	late <- dualStackCandidate{address: stale, family: familyIPv6, sourceGeneration: 1, sourceKnown: true}
	close(late)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	scheduler := supersededScheduler(40*time.Millisecond, 2)
	conn, winner, err := scheduler.dialWithLateCandidates(ctx,
		planCandidates(nil, netip.Addr{}, C.DomainStrategyPreferIPv6), late, inner.attempt)
	require.NoError(t, err, "a superseded answer with nothing current beside it must still be dialled")
	require.Equal(t, stale, winner)
	require.Equal(t, []netip.Addr{stale}, inner.attempts())
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())
}

// TestUnknownProvenanceIsNeverDemoted keeps third-party implementations working.
//
// Every producer that predates the provenance fields reports nothing, and a router that cannot
// report "now" leaves the comparison unavailable. Unknown must behave exactly as before: arrival
// order decides.
func TestUnknownProvenanceIsNeverDemoted(t *testing.T) {
	first := netip.MustParseAddr("192.0.2.1")
	unknown := netip.MustParseAddr("2001:db8::1")

	inner := newStreamOrderDialer(first)
	late := make(chan dualStackCandidate, 2)
	late <- dualStackCandidate{address: first, family: familyIPv4}
	late <- dualStackCandidate{address: unknown, family: familyIPv6}
	close(late)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	scheduler := supersededScheduler(30*time.Millisecond, 9)
	conn, winner, err := scheduler.dialWithLateCandidates(ctx,
		planCandidates(nil, netip.Addr{}, C.DomainStrategyPreferIPv4), late, inner.attempt)
	require.NoError(t, err)
	require.Equal(t, unknown, winner,
		"a candidate with no provenance must keep its arrival order, not be pushed behind anything")
	_ = conn
	if conn != nil {
		require.NoError(t, conn.Close())
	}
}

// generationFamilyRouter publishes family answers stamped with a DNS environment generation, and
// reports the generation that is current now.
type generationFamilyRouter struct {
	adapter.DNSRouter
	generation atomic.Uint64
	steps      []generationFamilyStep
}

type generationFamilyStep struct {
	delay            time.Duration
	ipv6             bool
	addresses        []netip.Addr
	sourceGeneration uint64
	sourceKnown      bool
}

func (r *generationFamilyRouter) LookupFamilies(ctx context.Context, domain string, options adapter.DNSQueryOptions, publish func(adapter.DNSFamilyResult)) error {
	for _, step := range r.steps {
		if step.delay > 0 {
			select {
			case <-time.After(step.delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		publish(adapter.DNSFamilyResult{
			IPv6:                step.ipv6,
			Addresses:           step.addresses,
			SourceGeneration:    step.sourceGeneration,
			HasSourceGeneration: step.sourceKnown,
		})
	}
	return nil
}

func (r *generationFamilyRouter) DNSGeneration() uint64 {
	return r.generation.Load()
}

// TestPublishedConnectionTargetsTheCurrentDNSEnvironment is the end-to-end boundary.
//
// It drives the production entry point: the feeder copies each answer's provenance onto the
// candidate, the scheduler orders by it, and the socket that is finally created targets the address
// the current environment produced.
func TestPublishedConnectionTargetsTheCurrentDNSEnvironment(t *testing.T) {
	first := netip.MustParseAddr("192.0.2.1")   // current, silently blackholed
	stale := netip.MustParseAddr("2001:db8::1") // superseded environment, silently blackholed
	current := netip.MustParseAddr("192.0.2.92")

	router := &generationFamilyRouter{steps: []generationFamilyStep{
		{ipv6: false, addresses: []netip.Addr{first}, sourceGeneration: 2, sourceKnown: true},
		{delay: 5 * time.Millisecond, ipv6: true, addresses: []netip.Addr{stale}, sourceGeneration: 1, sourceKnown: true},
		{delay: 5 * time.Millisecond, ipv6: false, addresses: []netip.Addr{current}, sourceGeneration: 2, sourceKnown: true},
	}}
	router.generation.Store(2)

	inner := newStreamOrderDialer(first, stale)
	dialer := &resolveDialer{
		router:        router,
		dialer:        inner,
		parallel:      true,
		fallbackDelay: 40 * time.Millisecond,
		queryOptions:  adapter.DNSQueryOptions{Strategy: C.DomainStrategyPreferIPv4},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", M.ParseSocksaddr("provenance.test:443"))
	require.NoError(t, err)
	require.NotNil(t, conn)
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())

	attempts := inner.attempts()
	currentAt := -1
	staleAt := -1
	for index, address := range attempts {
		if address == current && currentAt < 0 {
			currentAt = index
		}
		if address == stale && staleAt < 0 {
			staleAt = index
		}
	}
	require.GreaterOrEqual(t, currentAt, 0, "the current-environment address must be dialled: %v", attempts)
	if staleAt >= 0 {
		require.Less(t, currentAt, staleAt,
			"the current-environment address must be attempted before the superseded one; attempts: %v", attempts)
	}
}
