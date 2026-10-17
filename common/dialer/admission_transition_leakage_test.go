package dialer

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// §13 and §14 of the transition brief: the two LEAKAGE questions, which are different questions.
//
//	§13  an ANSWER resolved for generation N must not enter a generation N+1 scheduler
//	     (now proved by admission_transition_answer_interleaving_test.go, with a real barrier;
//	      the version that used to live here closed the resolver gate before the flow began and
//	      therefore never forced the interleaving its name claimed)
//	§14  a CANDIDATE delivered after the setup was cancelled must not be launched
//
// §13 is about where a value comes from. §14 is about when a value arrives. Both were previously
// established by reading nearby tests rather than by constructing the exact scenario the brief describes,
// and §14 was not satisfiable at all until the scheduler grew its cancellation check - the loop's other arm
// returns on `<-ctx.Done()`, but a candidate that is already ready wins the select at random, so the
// answer to "is it launched" was a coin flip.

// TestACandidateDeliveredAfterTheCancelAddsNoAttempt is §14 - and it is reported as a WEAKER result than
// the brief's phrasing invites, because that is what it is.
//
// # What it pins
//
// The observable the brief names: a candidate pushed into a still-open late channel after the setup was
// cancelled must not increase the attempt count. That holds, at 50 iterations, and it is worth having.
//
// # What it does NOT pin, and this was MEASURED rather than assumed
//
// It does not prove the scheduler's cancellation check is load-bearing. With the check removed, this test
// STILL PASSES. The reason is that the loop has a second, independent way to conclude: once the cancelled
// attempt returns, `started == len(failures)` and the loop exits before it reads the channel again. So in
// every scenario a deterministic fixture can build, the launch window is already closed by the time the
// candidate arrives.
//
// The check therefore closes a window only a GENUINE race can open - `cancel()` concurrent with a ready
// candidate, with the loop's select choosing between two ready cases at random. No deterministic fixture can
// open that window, and a test that tried would be the sleep-and-hope kind this round forbids. The check is
// kept because it makes that window's outcome deterministic in production; it is not claimed as
// test-enforced, and a reader of the check's comment must not expect a test to fail if it is removed.
//
// The first version of this test also had an ordinary bug worth recording: its attempt counter accumulated
// across iterations and was compared against 1, so it failed on the second iteration regardless of the code
// under test - a fixture measuring its own bookkeeping.
func TestACandidateDeliveredAfterTheCancelAddsNoAttempt(t *testing.T) {
	blackhole := netip.MustParseAddr("2001:db8::1")
	late := netip.MustParseAddr("2001:db8::2")

	inner := newStallingDialer()
	inner.stall = map[netip.Addr]bool{blackhole: true}

	var (
		access   sync.Mutex
		attempts []netip.Addr
	)
	attempt := func(attemptCtx context.Context, address netip.Addr) (net.Conn, error) {
		access.Lock()
		attempts = append(attempts, address)
		count := len(attempts)
		access.Unlock()
		_ = count
		return inner.DialContext(attemptCtx, "tcp", M.SocksaddrFrom(address, 443))
	}
	attemptCount := func() int {
		access.Lock()
		defer access.Unlock()
		return len(attempts)
	}

	for iteration := 0; iteration < 50; iteration++ {
		// The counter is PER ITERATION. Accumulating across them was the first version's bug: it compared a
		// running total against 1 and failed on the second iteration regardless of the code under test.
		access.Lock()
		attempts = nil
		access.Unlock()

		lateCandidates := make(chan dualStackCandidate, 1)
		scheduler := &candidateScheduler{fallbackDelay: time.Millisecond}
		ctx, cancel := context.WithCancel(context.Background())

		done := make(chan error, 1)
		go func() {
			_, _, err := scheduler.dialWithLateCandidatesAdmitting(ctx,
				planCandidates(nil, netip.Addr{}, C.DomainStrategyAsIS), lateCandidates, nil, attempt)
			done <- err
		}()

		// Seed the race with one candidate so an attempt is genuinely in flight, then cancel the setup.
		// Without the seed the plan is empty and nothing would ever start, so the test would be asserting
		// about a race that never began.
		lateCandidates <- dualStackCandidate{address: blackhole, family: classifyAddress(blackhole)}
		require.Eventually(t, func() bool { return attemptCount() >= 1 }, 2*time.Second, time.Millisecond,
			"iteration %d: the race never started", iteration)
		cancel()

		// Deliver a candidate into the still-open channel, exactly as the brief describes.
		select {
		case lateCandidates <- dualStackCandidate{address: late, family: classifyAddress(late)}:
		case <-time.After(time.Second):
			t.Fatalf("iteration %d: the late channel was not open", iteration)
		}

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d: the cancelled race did not return", iteration)
		}

		before := attemptCount()
		require.Equal(t, 1, before,
			"iteration %d: a candidate delivered after the setup was cancelled was launched (%d attempts, "+
				"expected 1). The launch spends a goroutine and a slot on work whose answer nobody can use, "+
				"and before the scheduler's cancellation check the outcome depended on which select case won",
			iteration, before)
	}
}
