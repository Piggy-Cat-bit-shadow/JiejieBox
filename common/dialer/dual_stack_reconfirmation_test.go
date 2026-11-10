package dialer

import (
	"context"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// A repeat of an address is not always redundant (§C same-address re-confirmation).
//
// # The defect these pin
//
// Deduplication was keyed by canonical address alone, so a repeat was dropped before anything could
// tell what it carried. A resolver can name the same destination twice for different reasons: an old
// batch names it, and the current environment confirms it before anything is dequeued. Dropping that
// confirmation left the queued entry marked superseded, and the scheduler then launched an address
// that only the PREVIOUS environment had named - ahead of the one the current environment had
// confirmed. The raced winner was the unconfirmed address.
//
// The upgrade is deliberately narrow: only the not-yet-started segment can absorb a better stamp, a
// stale or unknown repeat never overwrites a known current one, and an address that has already been
// launched is never re-ordered or re-dialled.

// TestCurrentReconfirmationUpgradesAQueuedAddress is the reported shape.
func TestCurrentReconfirmationUpgradesAQueuedAddress(t *testing.T) {
	const fallbackDelay = 200 * time.Millisecond

	blackhole6 := netip.MustParseAddr("2001:db8::1")
	oldFirst := netip.MustParseAddr("192.0.2.1")
	oldSecond := netip.MustParseAddr("192.0.2.2")

	inner := newStreamOrderDialer(blackhole6)
	late := make(chan dualStackCandidate, 4)
	late <- dualStackCandidate{address: blackhole6, family: familyIPv6, sourceGeneration: 2, sourceKnown: true}
	late <- dualStackCandidate{address: oldFirst, family: familyIPv4, sourceGeneration: 1, sourceKnown: true}
	late <- dualStackCandidate{address: oldSecond, family: familyIPv4, sourceGeneration: 1, sourceKnown: true}
	late <- dualStackCandidate{address: oldSecond, family: familyIPv4, sourceGeneration: 2, sourceKnown: true}
	close(late)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	scheduler := supersededScheduler(fallbackDelay, 2)
	conn, winner, err := scheduler.dialWithLateCandidates(ctx,
		planCandidates(nil, netip.Addr{}, C.DomainStrategyPreferIPv6), late, inner.attempt)
	require.NoError(t, err)
	require.Equal(t, oldSecond, winner,
		"the address the current environment confirmed must take the slot; attempts: %v", inner.attempts())
	require.Equal(t, 1, countAttempts(inner.attempts(), oldSecond),
		"the confirmation must upgrade the queued entry, not add a second attempt")
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())
}

// TestLateSupersededReconfirmationDoesNotDowngrade is the reverse arrival order: the current answer
// is queued first and a late old answer repeats it. The old stamp must not overwrite the current
// confirmation.
func TestLateSupersededReconfirmationDoesNotDowngrade(t *testing.T) {
	const fallbackDelay = 200 * time.Millisecond

	blackhole6 := netip.MustParseAddr("2001:db8::1")
	currentFirst := netip.MustParseAddr("192.0.2.2")
	oldSecond := netip.MustParseAddr("192.0.2.1")

	inner := newStreamOrderDialer(blackhole6)
	late := make(chan dualStackCandidate, 3)
	late <- dualStackCandidate{address: blackhole6, family: familyIPv6, sourceGeneration: 2, sourceKnown: true}
	late <- dualStackCandidate{address: currentFirst, family: familyIPv4, sourceGeneration: 2, sourceKnown: true}
	late <- dualStackCandidate{address: oldSecond, family: familyIPv4, sourceGeneration: 1, sourceKnown: true}
	close(late)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	scheduler := supersededScheduler(fallbackDelay, 2)
	conn, winner, err := scheduler.dialWithLateCandidates(ctx,
		planCandidates(nil, netip.Addr{}, C.DomainStrategyPreferIPv6), late, inner.attempt)
	require.NoError(t, err)
	require.Equal(t, currentFirst, winner)
	require.Equal(t, 1, countAttempts(inner.attempts(), currentFirst))
	if conn != nil {
		_ = conn.Close()
	}
}

// TestMappedReconfirmationUpgradesTheQueuedNativeAddress checks the canonical identity: the
// confirmation can arrive in the mapped form.
func TestMappedReconfirmationUpgradesTheQueuedNativeAddress(t *testing.T) {
	const fallbackDelay = 200 * time.Millisecond

	blackhole6 := netip.MustParseAddr("2001:db8::1")
	oldNative := netip.MustParseAddr("192.0.2.1")
	nativeConfirm := netip.MustParseAddr("192.0.2.2")
	mappedConfirm := netip.MustParseAddr("::ffff:192.0.2.2")

	inner := newStreamOrderDialer(blackhole6)
	late := make(chan dualStackCandidate, 4)
	late <- dualStackCandidate{address: blackhole6, family: familyIPv6, sourceGeneration: 2, sourceKnown: true}
	late <- dualStackCandidate{address: oldNative, family: familyIPv4, sourceGeneration: 1, sourceKnown: true}
	late <- dualStackCandidate{address: nativeConfirm, family: familyIPv4, sourceGeneration: 1, sourceKnown: true}
	late <- dualStackCandidate{address: unmapAddress(mappedConfirm), family: familyIPv4, sourceGeneration: 2, sourceKnown: true}
	close(late)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	scheduler := supersededScheduler(fallbackDelay, 2)
	conn, winner, err := scheduler.dialWithLateCandidates(ctx,
		planCandidates(nil, netip.Addr{}, C.DomainStrategyPreferIPv6), late, inner.attempt)
	require.NoError(t, err)
	require.Equal(t, nativeConfirm, winner,
		"a mapped confirmation names the same canonical address; attempts: %v", inner.attempts())
	require.Equal(t, 1, countAttempts(inner.attempts(), nativeConfirm))
	if conn != nil {
		_ = conn.Close()
	}
}

// TestReconfirmationAfterTheAddressStartedDoesNotRedial keeps the single-attempt rule: once launched,
// the address is out of the queue and a repeat cannot re-order or re-dial it.
func TestReconfirmationAfterTheAddressStartedDoesNotRedial(t *testing.T) {
	const fallbackDelay = 60 * time.Millisecond

	first := netip.MustParseAddr("192.0.2.1")
	other := netip.MustParseAddr("192.0.2.92")

	inner := newStreamOrderDialer(first)
	stream := make(chan dualStackCandidate, 3)
	stream <- dualStackCandidate{address: first, family: familyIPv4, sourceGeneration: 1, sourceKnown: true}
	// The current environment confirms the address after it has already been launched.
	stream <- dualStackCandidate{address: first, family: familyIPv4, sourceGeneration: 2, sourceKnown: true}
	stream <- dualStackCandidate{address: other, family: familyIPv4, sourceGeneration: 2, sourceKnown: true}
	close(stream)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	scheduler := supersededScheduler(fallbackDelay, 2)
	conn, winner, err := scheduler.dialWithLateCandidates(ctx,
		planCandidates(nil, netip.Addr{}, C.DomainStrategyPreferIPv4), stream, inner.attempt)
	require.NoError(t, err)
	require.Equal(t, other, winner, "the already-started address must not be dialled again")
	require.Equal(t, 1, countAttempts(inner.attempts(), first))
	if conn != nil {
		_ = conn.Close()
	}
}

// recoveryBatchRouter publishes several answers from the same family, stamping each with its source
// generation, which is what a re-confirmation looks like on the literal-recovery path.
type recoveryBatchRouter struct {
	adapter.DNSRouter
	generation atomic.Uint64
	steps      []recoveryBatchStep
	// publishedGeneration and publishedAt record the environment and instant each batch became
	// visible, so a test can prove "the old batch was current when it was accepted" and "the
	// confirmation was already queued before anything was launched from that family".
	//
	// Guarded because the lookup goroutine writes them and the test reads them after the dial, and
	// the mutex is what gives that read a happens-before edge rather than a wall-clock assumption.
	access              sync.Mutex
	publishedGeneration []uint64
	publishedAt         []time.Time
}

type recoveryBatchStep struct {
	delay      time.Duration
	ipv6       bool
	addresses  []netip.Addr
	generation uint64
	// unknown publishes the batch WITHOUT a source stamp, which is the legacy producer's shape: the
	// addresses are offered but nothing can be said about the environment they came from.
	unknown bool
	// advanceTo, when non-zero, moves the router's generation BEFORE this batch is published. It is
	// how a test builds the exact sequence the defect needs: an old batch accepted while its
	// environment is still current, then the environment moves, then the confirmation arrives.
	advanceTo uint64
}

func (r *recoveryBatchRouter) LookupFamilies(ctx context.Context, domain string, options adapter.DNSQueryOptions, publish func(adapter.DNSFamilyResult)) error {
	for _, step := range r.steps {
		if step.delay > 0 {
			select {
			case <-time.After(step.delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if step.advanceTo != 0 {
			r.generation.Store(step.advanceTo)
		}
		r.access.Lock()
		r.publishedGeneration = append(r.publishedGeneration, r.generation.Load())
		r.publishedAt = append(r.publishedAt, time.Now())
		r.access.Unlock()
		publish(adapter.DNSFamilyResult{
			IPv6:                step.ipv6,
			Addresses:           step.addresses,
			SourceGeneration:    step.generation,
			HasSourceGeneration: !step.unknown,
		})
	}
	return nil
}

// publication returns the environment each batch was published under, and when.
func (r *recoveryBatchRouter) publication() ([]uint64, []time.Time) {
	r.access.Lock()
	defer r.access.Unlock()
	return append([]uint64(nil), r.publishedGeneration...), append([]time.Time(nil), r.publishedAt...)
}

func (r *recoveryBatchRouter) DNSGeneration() uint64 {
	return r.generation.Load()
}

// TestLiteralRecoveryCurrentReconfirmationUpgradesTheQueuedAddress is the same rule through the
// production literal-recovery entry point, including the recovery producer's own deduplication.
func TestLiteralRecoveryCurrentReconfirmationUpgradesTheQueuedAddress(t *testing.T) {
	const fallbackDelay = 200 * time.Millisecond

	original := netip.MustParseAddr("2001:db8::2c")
	blackhole6 := netip.MustParseAddr("2001:db8::30")
	oldFirst := netip.MustParseAddr("192.0.2.1")
	oldSecond := netip.MustParseAddr("192.0.2.2")

	router := &recoveryBatchRouter{steps: []recoveryBatchStep{
		// The recovered race starts on IPv6, so the IPv4 queue is not the first launch.
		{ipv6: true, addresses: []netip.Addr{blackhole6}, generation: 2},
		{delay: 5 * time.Millisecond, ipv6: false, addresses: []netip.Addr{oldFirst, oldSecond}, generation: 1},
		{delay: 10 * time.Millisecond, ipv6: false, addresses: []netip.Addr{oldSecond}, generation: 2},
	}}
	router.generation.Store(2)

	inner := newStreamOrderDialer(original, blackhole6)
	dialer := &resolveDialer{
		router:        router,
		dialer:        inner,
		parallel:      true,
		fallbackDelay: fallbackDelay,
		queryOptions:  adapter.DNSQueryOptions{Strategy: C.DomainStrategyPreferIPv4},
	}

	ctx, cancel := context.WithTimeout(recoveryContext(), 3*time.Second)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", M.SocksaddrFrom(original, 443))
	require.NoError(t, err, "attempts: %v", inner.attempts())
	require.NotNil(t, conn)

	attempts := inner.attempts()
	require.Equal(t, 1, countAttempts(attempts, oldSecond))
	require.LessOrEqual(t, countAttempts(attempts, oldFirst), 1)
	// The current confirmation must be the first IPv4 address dialled, ahead of the address only the
	// old batch named.
	firstIPv4 := netip.Addr{}
	for _, address := range attempts {
		if classifyAddress(address) == familyIPv4 {
			firstIPv4 = address
			break
		}
	}
	require.Equal(t, oldSecond, firstIPv4,
		"the confirmed address must lead the recovered race; attempts: %v", attempts)
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())
}

// TestLiteralRecoveryCrossGenerationConfirmationKeepsTheCurrentAddress is C1: the old batch was
// CURRENT when the producer accepted it, and only later did the environment move and re-confirm one
// of its addresses.
//
// # The defect this pins
//
// The producer remembered "was this superseded when it arrived". For this sequence the answer was
// no, and the stored `false` was never re-evaluated, so the later confirmation looked like a
// redundant repeat and was dropped. The queued entry kept generation 1, the scheduler - which does
// evaluate provenance at dequeue - saw it as superseded and launched the address only the old
// environment had named. Both IPv4 addresses were healthy, so the stale one won.
//
// The producer now keeps the SOURCE and forwards a repeat whose source is newer. The scheduler
// upgrades the queued entry and the current confirmation leads its family.
func TestLiteralRecoveryCrossGenerationConfirmationKeepsTheCurrentAddress(t *testing.T) {
	const fallbackDelay = 200 * time.Millisecond

	original := netip.MustParseAddr("2001:db8::2c")
	blackhole6 := netip.MustParseAddr("2001:db8::30")
	oldFirst := netip.MustParseAddr("192.0.2.1")
	oldSecond := netip.MustParseAddr("192.0.2.2")

	router := &recoveryBatchRouter{steps: []recoveryBatchStep{
		// The recovered race starts on IPv6, so the IPv4 family is not the first launch.
		{ipv6: true, addresses: []netip.Addr{blackhole6}, generation: 1},
		// The IPv4 batch is accepted while generation 1 is STILL current. This is the event the old
		// producer recorded as "not superseded" and then never re-evaluated.
		{delay: 5 * time.Millisecond, ipv6: false, addresses: []netip.Addr{oldFirst, oldSecond}, generation: 1},
		// The environment moves and re-confirms .2 before the IPv4 family's launch slot.
		{delay: 10 * time.Millisecond, ipv6: false, addresses: []netip.Addr{oldSecond}, generation: 2, advanceTo: 2},
	}}
	router.generation.Store(1)

	inner := newStreamOrderDialer(original, blackhole6)
	dialer := &resolveDialer{
		router:        router,
		dialer:        inner,
		parallel:      true,
		fallbackDelay: fallbackDelay,
		queryOptions:  adapter.DNSQueryOptions{Strategy: C.DomainStrategyPreferIPv4},
	}

	ctx, cancel := context.WithTimeout(recoveryContext(), 3*time.Second)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", M.SocksaddrFrom(original, 443))
	require.NoError(t, err, "attempts: %v", inner.attempts())
	require.NotNil(t, conn)

	attempts := inner.attempts()
	publishedGeneration, publishedAt := router.publication()
	require.Equal(t, []uint64{1, 1, 2}, publishedGeneration,
		"the old batch must have been published while generation 1 was current")
	startedAt, started := inner.startedAt(oldSecond)
	require.True(t, started, "the confirmed address must be dialled; attempts: %v", attempts)
	require.False(t, startedAt.Before(publishedAt[len(publishedAt)-1]),
		"the confirmation must be queued before the address it confirms is launched")

	firstIPv4 := netip.Addr{}
	for _, address := range attempts {
		if classifyAddress(address) == familyIPv4 {
			firstIPv4 = address
			break
		}
	}
	require.Equal(t, oldSecond, firstIPv4,
		"the address the current environment confirmed must lead its family; attempts: %v", attempts)
	require.Equal(t, 1, countAttempts(attempts, oldSecond), "one attempt per address")
	require.LessOrEqual(t, countAttempts(attempts, oldFirst), 1)
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())
}

// TestLiteralRecoveryLateOldConfirmationDoesNotDowngrade is the reverse source order through the
// same producer: the newer source is accepted first and an older repeat must not replace it.
func TestLiteralRecoveryLateOldConfirmationDoesNotDowngrade(t *testing.T) {
	const fallbackDelay = 120 * time.Millisecond

	original := netip.MustParseAddr("2001:db8::2c")
	blackhole6 := netip.MustParseAddr("2001:db8::30")
	current := netip.MustParseAddr("192.0.2.2")
	older := netip.MustParseAddr("192.0.2.1")

	router := &recoveryBatchRouter{steps: []recoveryBatchStep{
		{ipv6: true, addresses: []netip.Addr{blackhole6}, generation: 2},
		{delay: 5 * time.Millisecond, ipv6: false, addresses: []netip.Addr{current}, generation: 2},
		{delay: 5 * time.Millisecond, ipv6: false, addresses: []netip.Addr{current}, generation: 1},
		{delay: 5 * time.Millisecond, ipv6: false, addresses: []netip.Addr{older}, generation: 1},
	}}
	router.generation.Store(2)

	inner := newStreamOrderDialer(original, blackhole6)
	dialer := &resolveDialer{
		router:        router,
		dialer:        inner,
		parallel:      true,
		fallbackDelay: fallbackDelay,
		queryOptions:  adapter.DNSQueryOptions{Strategy: C.DomainStrategyPreferIPv4},
	}

	ctx, cancel := context.WithTimeout(recoveryContext(), 3*time.Second)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", M.SocksaddrFrom(original, 443))
	require.NoError(t, err, "attempts: %v", inner.attempts())
	require.NotNil(t, conn)
	attempts := inner.attempts()
	firstIPv4 := netip.Addr{}
	for _, address := range attempts {
		if classifyAddress(address) == familyIPv4 {
			firstIPv4 = address
			break
		}
	}
	require.Equal(t, current, firstIPv4,
		"an older repeat must not displace the current confirmation; attempts: %v", attempts)
	require.Equal(t, 1, countAttempts(attempts, current))
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())
}

// TestRecoveredSourceStampDecidesTheWinner is the dial-observation segment for C3.
//
// The stamp VALUES are exactly what the real Router publishes (verified in
// dns.TestRealRouterDisableCachePublishesItsSource): a DisableCache exchange names the generation it
// was asked on. This test feeds the same shape through the real recovery producer and the real
// scheduler, and the control shows what happens when the source is unknown - the behaviour C3 fixed.
//
// The two segments exist because Go's import graph does not allow one: dns imports common/dialer, so
// a dialer test cannot import dns to drive the real Router in the same process. The Router segment
// proves the value is published; this segment proves the value decides the launch.
func TestRecoveredSourceStampDecidesTheWinner(t *testing.T) {
	const fallbackDelay = 120 * time.Millisecond

	original := netip.MustParseAddr("2001:db8::2c")
	blackhole6 := netip.MustParseAddr("2001:db8::30")
	stale4 := netip.MustParseAddr("192.0.2.1")
	current4 := netip.MustParseAddr("192.0.2.2")

	run := func(t *testing.T, unknown bool) []netip.Addr {
		t.Helper()
		router := &recoveryBatchRouter{steps: []recoveryBatchStep{
			{ipv6: true, addresses: []netip.Addr{blackhole6}, generation: 2, unknown: unknown},
			{delay: 5 * time.Millisecond, ipv6: false, addresses: []netip.Addr{stale4}, generation: 1, unknown: unknown},
			{delay: 5 * time.Millisecond, ipv6: false, addresses: []netip.Addr{current4}, generation: 2, unknown: unknown},
		}}
		router.generation.Store(2)
		inner := newStreamOrderDialer(original, blackhole6, stale4)
		dialer := &resolveDialer{
			router:        router,
			dialer:        inner,
			parallel:      true,
			fallbackDelay: fallbackDelay,
			queryOptions:  adapter.DNSQueryOptions{Strategy: C.DomainStrategyPreferIPv4},
		}
		ctx, cancel := context.WithTimeout(recoveryContext(), 3*time.Second)
		defer cancel()
		conn, err := dialer.DialContext(ctx, "tcp", M.SocksaddrFrom(original, 443))
		require.NoError(t, err, "attempts: %v", inner.attempts())
		require.NotNil(t, conn)
		requireBusinessData(t, conn)
		require.NoError(t, conn.Close())
		return inner.attempts()
	}

	t.Run("known source", func(t *testing.T) {
		attempts := run(t, false)
		firstIPv4 := netip.Addr{}
		for _, address := range attempts {
			if classifyAddress(address) == familyIPv4 {
				firstIPv4 = address
				break
			}
		}
		require.Equal(t, current4, firstIPv4,
			"the current confirmation must lead its family; attempts: %v", attempts)
	})

	t.Run("unknown source (control)", func(t *testing.T) {
		attempts := run(t, true)
		firstIPv4 := netip.Addr{}
		for _, address := range attempts {
			if classifyAddress(address) == familyIPv4 {
				firstIPv4 = address
				break
			}
		}
		require.Equal(t, stale4, firstIPv4,
			"without provenance the queue keeps its arrival order, which is the behaviour C3 fixed")
	})
}
