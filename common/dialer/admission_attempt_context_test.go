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

// The mechanism behind Phase 03 §E.3's open item, pinned.
//
// # The claim that was only reasoned about
//
// A late candidate arriving after the setup was cancelled IS launched - the scheduler's loop can pick the
// late-candidate arm after `<-ctx.Done()` becomes ready, because Go's select chooses at random among ready
// cases. What makes that harmless is not the launch being skipped; it is that the attempt is handed the
// SETUP's context, so the cancellation reaches the socket and the connect fails immediately.
//
// That reasoning was recorded as `VERIFIED_SOURCE`, and its load-bearing half - "the attempt is handed the
// setup's context" - is a property of one argument at one call site. A single edit to `context.Background()`
// there would make the launch real work instead of harmless work, and nothing in the suite would notice:
// the late-candidate tests assert which addresses are attempted, not which context each attempt carries.
//
// So this pins it directly, from the attempt's own side: every context an attempt receives must be DONE by
// the time the race returns, which is only true if it descends from the setup's context.
//
// # Why the failure is worth this much attention
//
// A detached attempt context on a mobile device does not look like a bug. It looks like a dial that keeps
// running for a second after the screen locked, holding the radio awake, on a connection nobody is waiting
// for - and it would be attributed to the transport, not to this call site.

// TestEveryAttemptCarriesTheSetupsContext is the pin.
func TestEveryAttemptCarriesTheSetupsContext(t *testing.T) {
	blackhole6 := netip.MustParseAddr("2001:db8::1")
	blackhole4 := netip.MustParseAddr("192.0.2.1")

	inner := newStallingDialer()
	inner.stall = map[netip.Addr]bool{blackhole6: true, blackhole4: true}

	var (
		access   sync.Mutex
		received []context.Context
	)
	scheduler := &candidateScheduler{fallbackDelay: time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, _, err := scheduler.dial(ctx,
			planCandidates([]netip.Addr{blackhole6, blackhole4}, netip.Addr{}, C.DomainStrategyAsIS),
			func(attemptCtx context.Context, address netip.Addr) (net.Conn, error) {
				access.Lock()
				received = append(received, attemptCtx)
				access.Unlock()
				return inner.DialContext(attemptCtx, "tcp", M.SocksaddrFrom(address, 443))
			})
		done <- err
	}()

	// Both attempts must be in flight before the cancel, or the test measures a race that never started.
	require.Eventually(t, func() bool {
		access.Lock()
		defer access.Unlock()
		return len(received) >= 1
	}, 2*time.Second, time.Millisecond, "no attempt was made, so there is no context to inspect")
	time.Sleep(20 * time.Millisecond) // let the second candidate start; the assertion is on what each carries
	cancel()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled race did not return")
	}

	access.Lock()
	defer access.Unlock()
	require.NotEmpty(t, received, "the race returned without making an attempt")
	for index, attemptCtx := range received {
		require.Error(t, attemptCtx.Err(),
			"attempt %d received a context that outlived the setup: the cancellation did not reach the "+
				"socket, so a launch after the setup ended is real work on a connection nobody is waiting "+
				"for", index)
	}
}

// TestAPreCancelledAttemptNeverReachesARealSocket is the other half, against a REAL listener.
//
// The claim "no socket work happens" rests on Go's `net.Dialer.DialContext`, which checks the context
// before it creates anything. That is standard-library behaviour rather than ours, so it is verified here
// once, against a listener that would otherwise accept: if the connect happened, the accept fires and this
// fails.
func TestAPreCancelledAttemptNeverReachesARealSocket(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	accepted := make(chan struct{}, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			conn.Close()
			accepted <- struct{}{}
		}
	}()

	target, parseErr := netip.ParseAddrPort(listener.Addr().String())
	require.NoError(t, parseErr)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var netDialer net.Dialer
	_, dialErr := netDialer.DialContext(ctx, "tcp", target.String())
	require.Error(t, dialErr)
	require.ErrorIs(t, dialErr, context.Canceled,
		"a cancelled context must be reported as cancelled, not as a connect failure: the two are "+
			"different facts and only one of them is about the network")

	select {
	case <-accepted:
		t.Fatal("a cancelled dial reached the listener: the standard library's pre-check did not stop it, " +
			"so the claim that a launched-after-cancel attempt does no socket work is false")
	case <-time.After(200 * time.Millisecond):
		// Nothing connected, which is the assertion. This wait is bounded measurement of an ABSENCE, not a
		// timing assumption about work that is expected to happen.
	}
}
