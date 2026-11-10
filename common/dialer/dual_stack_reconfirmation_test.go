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
}

type recoveryBatchStep struct {
	delay      time.Duration
	ipv6       bool
	addresses  []netip.Addr
	generation uint64
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
		publish(adapter.DNSFamilyResult{
			IPv6:                step.ipv6,
			Addresses:           step.addresses,
			SourceGeneration:    step.generation,
			HasSourceGeneration: true,
		})
	}
	return nil
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
