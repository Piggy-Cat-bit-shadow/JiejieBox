package dialer

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Literal-address recovery as a live stream (§B).
//
// # The defect these pin
//
// A TUN application usually has a literal address already chosen and a sniffed domain. Recovery
// re-resolved that name to find the other family, but it ended at the FIRST family that passed
// admission and cancelled the other family's query. "Passes admission" says the address may be
// dialled, not that anything answers, so:
//
//	AAAA answers first with the SAME blackholed literal the caller is dialling
//	recovery ends there, deduplication leaves nothing, and the A answer - in flight - is cancelled
//
// The flow then had no new candidate at all while a healthy address was one query away.
//
// The tests below separate the three events that were conflated: "this address is admitted",
// "this address is a NEW candidate", and "this address actually works".

var errRecoveryFastFailure = errors.New("connection refused")

// literalRecoveryDialer wires the production resolve dialer for a literal destination with recovery
// available.
func literalRecoveryDialer(inner *streamOrderDialer, router adapter.DNSRouter, fallbackDelay time.Duration, strategy C.DomainStrategy) *resolveDialer {
	return &resolveDialer{
		router:        router,
		dialer:        inner,
		parallel:      true,
		fallbackDelay: fallbackDelay,
		queryOptions:  adapter.DNSQueryOptions{Strategy: strategy},
	}
}

func countAttempts(addresses []netip.Addr, wanted netip.Addr) int {
	count := 0
	for _, address := range addresses {
		if address == wanted {
			count++
		}
	}
	return count
}

// TestLiteralRecoveryDuplicateFirstAnswerDoesNotCloseTheSource is the reported shape.
//
// The sniffed name's AAAA answer is the same address the application is already dialling, so it
// contributes nothing new. That must not be the end of recovery: the A answer, arriving a moment
// later, is the fallback the flow needs.
func TestLiteralRecoveryDuplicateFirstAnswerDoesNotCloseTheSource(t *testing.T) {
	const fallbackDelay = 40 * time.Millisecond

	original := netip.MustParseAddr("2001:db8::2c")
	healthy := netip.MustParseAddr("192.0.2.92")

	router := newScriptedFamilyRouter()
	router.addressesAAAA = []netip.Addr{original} // exactly the literal being dialled
	router.delayAAAA = 0
	router.addressesA = []netip.Addr{healthy}
	router.delayA = 30 * time.Millisecond

	inner := newStreamOrderDialer(original)
	dialer := literalRecoveryDialer(inner, router, fallbackDelay, C.DomainStrategyPreferIPv6)

	ctx, cancel := context.WithTimeout(recoveryContext(), 3*time.Second)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", M.SocksaddrFrom(original, 443))
	require.NoError(t, err,
		"the duplicate AAAA answer closed the candidate source, so the healthy A answer was never "+
			"dialled (attempts: %v)", inner.attempts())
	require.NotNil(t, conn)
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())

	attempts := inner.attempts()
	require.Equal(t, 1, countAttempts(attempts, original),
		"the application's own address must be dialled exactly once; attempts: %v", attempts)
	require.Contains(t, attempts, healthy, "the other family's answer must be dialled")
}

// TestLiteralRecoveryBlackholedFirstAnswerKeepsLooking is the second half of the same defect.
//
// The first family answers with a DIFFERENT address that shares the same silent blackhole. It is a
// genuinely new candidate, so racing it is correct - but recovery must keep reading while that race
// is alive, because the other family's answer is what actually works.
func TestLiteralRecoveryBlackholedFirstAnswerKeepsLooking(t *testing.T) {
	const fallbackDelay = 40 * time.Millisecond

	original := netip.MustParseAddr("192.0.2.1")
	blackhole6 := netip.MustParseAddr("2001:db8::30")
	healthy := netip.MustParseAddr("192.0.2.92")

	router := newScriptedFamilyRouter()
	router.addressesAAAA = []netip.Addr{blackhole6}
	router.delayAAAA = 0
	router.addressesA = []netip.Addr{healthy}
	// The healthy family answers only after the race has started, so the test proves the other
	// family can still JOIN a race in progress rather than being collected up front.
	router.delayA = 100 * time.Millisecond

	inner := newStreamOrderDialer(original, blackhole6)
	dialer := literalRecoveryDialer(inner, router, fallbackDelay, C.DomainStrategyPreferIPv6)

	ctx, cancel := context.WithTimeout(recoveryContext(), 3*time.Second)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", M.SocksaddrFrom(original, 443))
	require.NoError(t, err,
		"recovery ended at the first (blackholed) family, so the late healthy answer was never used "+
			"(attempts: %v)", inner.attempts())
	require.NotNil(t, conn)
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())

	attempts := inner.attempts()
	require.Contains(t, attempts, blackhole6,
		"the first recovered family is a genuine candidate and must be tried")
	require.Contains(t, attempts, healthy, "and the late family must join the race and win")
	require.Equal(t, 1, countAttempts(attempts, original))
}

// TestLiteralRecoveryMappedDuplicateDoesNotCloseTheSource is the canonical-identity case.
//
// ::ffff:192.0.2.1 and 192.0.2.1 are the same destination. A recovery answer in the mapped form
// must be recognised as the literal already being dialled - neither a new candidate nor a reason to
// end the lookup.
func TestLiteralRecoveryMappedDuplicateDoesNotCloseTheSource(t *testing.T) {
	const fallbackDelay = 40 * time.Millisecond

	original := netip.MustParseAddr("192.0.2.1")
	mappedDuplicate := netip.MustParseAddr("::ffff:192.0.2.1")
	healthy := netip.MustParseAddr("2001:db8::5")

	router := newScriptedFamilyRouter()
	router.addressesA = []netip.Addr{mappedDuplicate}
	router.delayA = 0
	router.addressesAAAA = []netip.Addr{healthy}
	router.delayAAAA = 60 * time.Millisecond

	inner := newStreamOrderDialer(original)
	dialer := literalRecoveryDialer(inner, router, fallbackDelay, C.DomainStrategyPreferIPv4)

	ctx, cancel := context.WithTimeout(recoveryContext(), 3*time.Second)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", M.SocksaddrFrom(original, 443))
	require.NoError(t, err,
		"a mapped form of the literal is a duplicate, not an answer; the AAAA was never dialled "+
			"(attempts: %v)", inner.attempts())
	require.NotNil(t, conn)
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())

	attempts := inner.attempts()
	require.NotContains(t, attempts, mappedDuplicate,
		"the mapped form must never be dialled: it is the same destination as the literal")
	require.Equal(t, 1, countAttempts(attempts, original))
	require.Contains(t, attempts, healthy)
}

// TestLiteralRecoveryHealthyOriginalWithSlowDNS is the preference control.
//
// A healthy original must still win outright: a slow DNS answer must not race it out of the launch
// slot it already had.
func TestLiteralRecoveryHealthyOriginalWithSlowDNS(t *testing.T) {
	const fallbackDelay = 40 * time.Millisecond

	original := netip.MustParseAddr("192.0.2.1")
	recovered := netip.MustParseAddr("192.0.2.92")

	router := newScriptedFamilyRouter()
	router.addressesA = []netip.Addr{recovered}
	router.delayA = 300 * time.Millisecond
	router.delayAAAA = -1

	inner := newStreamOrderDialer()
	dialer := literalRecoveryDialer(inner, router, fallbackDelay, C.DomainStrategyPreferIPv4)

	ctx, cancel := context.WithTimeout(recoveryContext(), 3*time.Second)
	defer cancel()

	start := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp", M.SocksaddrFrom(original, 443))
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.NotNil(t, conn)
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())

	require.Less(t, elapsed, 100*time.Millisecond,
		"a healthy original must not wait for a DNS answer: the call took %v", elapsed)
	require.Equal(t, []netip.Addr{original}, inner.attempts(),
		"and it must be the only attempt")
}

// TestLiteralRecoveryBothFamiliesFailPromptly keeps the failure path bounded and explains itself.
func TestLiteralRecoveryBothFamiliesFailPromptly(t *testing.T) {
	const fallbackDelay = 20 * time.Millisecond

	original := netip.MustParseAddr("192.0.2.1")
	recovered4 := netip.MustParseAddr("192.0.2.92")
	recovered6 := netip.MustParseAddr("2001:db8::92")

	router := newScriptedFamilyRouter()
	router.addressesA = []netip.Addr{recovered4}
	router.delayA = 0
	router.addressesAAAA = []netip.Addr{recovered6}
	router.delayAAAA = 0

	inner := newStreamOrderDialer()
	inner.fail = map[netip.Addr]error{
		original:   errRecoveryFastFailure,
		recovered4: errRecoveryFastFailure,
		recovered6: errRecoveryFastFailure,
	}
	dialer := literalRecoveryDialer(inner, router, fallbackDelay, C.DomainStrategyAsIS)

	ctx, cancel := context.WithTimeout(recoveryContext(), 3*time.Second)
	defer cancel()

	start := time.Now()
	_, err := dialer.DialContext(ctx, "tcp", M.SocksaddrFrom(original, 443))
	elapsed := time.Since(start)
	require.Error(t, err, "every candidate failed, so the dial must fail")
	require.Less(t, elapsed, time.Second,
		"both families failed in microseconds, so the call must not wait out its budget")
	require.Contains(t, inner.attempts(), recovered4)
	require.Contains(t, inner.attempts(), recovered6)
}

// TestLiteralRecoveryOriginalExcludedByStrategy covers the literal the policy forbids.
//
// The original must not be dialled at all, and recovery - which is the only way this flow can
// connect - must still produce the family the policy admits.
func TestLiteralRecoveryOriginalExcludedByStrategy(t *testing.T) {
	const fallbackDelay = 30 * time.Millisecond

	original := netip.MustParseAddr("2001:db8::1")
	healthy := netip.MustParseAddr("192.0.2.92")

	router := newScriptedFamilyRouter()
	router.addressesA = []netip.Addr{healthy}
	router.delayA = 10 * time.Millisecond
	router.addressesAAAA = []netip.Addr{netip.MustParseAddr("2001:db8::2")}
	router.delayAAAA = 0

	inner := newStreamOrderDialer()
	dialer := literalRecoveryDialer(inner, router, fallbackDelay, C.DomainStrategyIPv4Only)

	ctx, cancel := context.WithTimeout(recoveryContext(), 3*time.Second)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", M.SocksaddrFrom(original, 443))
	require.NoError(t, err)
	require.NotNil(t, conn)
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())

	attempts := inner.attempts()
	require.NotContains(t, attempts, original, "the policy excludes the original; it must not be dialled")
	require.Equal(t, []netip.Addr{healthy}, attempts,
		"only the family the policy admits may be attempted")
}

// TestLiteralRecoveryCancellationWithAnArrivedCandidate states the cancellation boundary.
//
// A recovered candidate has already arrived and is being raced when the caller cancels. The call
// must return promptly: the candidate is not a reason to outlive the caller's budget.
func TestLiteralRecoveryCancellationWithAnArrivedCandidate(t *testing.T) {
	const fallbackDelay = 20 * time.Millisecond

	original := netip.MustParseAddr("192.0.2.1")
	recovered := netip.MustParseAddr("192.0.2.92")

	router := newScriptedFamilyRouter()
	router.addressesA = []netip.Addr{recovered}
	router.delayA = 0
	router.delayAAAA = -1

	inner := newStreamOrderDialer(original, recovered)
	dialer := literalRecoveryDialer(inner, router, fallbackDelay, C.DomainStrategyAsIS)

	ctx, cancel := context.WithTimeout(recoveryContext(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := dialer.DialContext(ctx, "tcp", M.SocksaddrFrom(original, 443))
	elapsed := time.Since(start)
	require.Error(t, err, "both candidates stall, so the only ending is the caller's budget")
	require.Contains(t, inner.attempts(), recovered, "the recovered candidate must have been raced")
	require.Less(t, elapsed, time.Second,
		"cancellation must end the race promptly, not wait for the attempts' own timeouts")
}

// TestDirectChainRecoveryKeepsTheOtherFamilyAlive drives the TUN/route -> Direct ->
// DialParallelNetwork chain, which is the production path for a sniffed literal.
//
// The same defect appears there for the same reason: the feed took the first admitted family only.
func TestDirectChainRecoveryKeepsTheOtherFamilyAlive(t *testing.T) {
	original := netip.MustParseAddr("2001:db8::2c")
	firstBlackhole := netip.MustParseAddr("2001:db8::30")
	healthy := netip.MustParseAddr("192.0.2.92")

	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyAsIS)
	chain.router.addressesAAAA = []netip.Addr{firstBlackhole}
	chain.router.delayAAAA = 0
	chain.router.addressesA = []netip.Addr{healthy}
	// The healthy family answers after the blackholed one has already been raced.
	chain.router.delayA = 80 * time.Millisecond
	chain.inner.stall = map[netip.Addr]bool{original: true, firstBlackhole: true}

	ctx, cancel := context.WithTimeout(wechatFlowLongLived(original, 80, "szminorshort.weixin.qq.com"), 3*time.Second)
	defer cancel()

	conn, err := chain.dialSerialNetwork(ctx, "tcp", M.SocksaddrFrom(original, 80), []netip.Addr{original})
	require.NoError(t, err,
		"the direct chain's recovery feed ended at the blackholed family, so the healthy answer was "+
			"never dialled (attempts: %v)", chain.tried())
	require.NotNil(t, conn)
	require.NoError(t, conn.Close())

	tried := chain.tried()
	require.Contains(t, tried, healthy, "the late healthy family must join the race")
	require.Equal(t, 1, countAttempts(tried, original),
		"the literal already has its own attempt; recovery must not dial it again")
}
