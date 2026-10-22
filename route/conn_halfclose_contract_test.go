package route

import (
	"context"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The diagnostics state contract, exercised as state transitions rather than as log fragments.
//
// # What the contract is
//
// An observation is counted in `observed` only after its probe is armed, and then reaches exactly ONE
// final state, decided by a compare-and-swap: `released` (a terminal error was read from the finished
// side and reclamation was invoked) or `stopped` (no verdict). Anything counted but not settled is
// `pending`, derived as observed - released - stopped. See conn_halfclose_diagnostics.go.
//
// # Why these tests look like this
//
// Asserting substrings of the summary would pass for an implementation that counted the same flow
// twice or forgot the normal-teardown path. Every test here asserts the TRANSITION and the invariant
// `observed == released + stopped + pending`, and each of the five ways an observation can end has
// its own subtest.

func requireConserved(t *testing.T, snapshot HalfCloseSnapshot) {
	t.Helper()
	require.Equal(t, snapshot.Observed, snapshot.Released+snapshot.Stopped+snapshot.Pending,
		"the state contract must hold: observed == released + stopped + pending (observed %d, released %d, stopped %d, pending %d)",
		snapshot.Observed, snapshot.Released, snapshot.Stopped, snapshot.Pending)
	require.Equal(t, snapshot.Released, snapshot.PeerReset+snapshot.KeepaliveTimeout,
		"released must be partitioned into peer reset and keepalive timeout")
}

// TestHalfCloseDiagnosticsReleasedByReset is the ordinary peer-gone verdict.
func TestHalfCloseDiagnosticsReleasedByReset(t *testing.T) {
	fixture := newHalfCloseFixture(t)
	require.Eventually(t, func() bool {
		return fixture.client.keepAliveSet.Load() == 1
	}, 3*time.Second, 2*time.Millisecond)

	fixture.client.setNextReadErr(syscall.ECONNRESET)
	select {
	case <-fixture.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("the flow was never released")
	}

	snapshot := fixture.manager.HalfCloseDiagnostics()
	requireConserved(t, snapshot)
	require.Equal(t, uint64(1), snapshot.Observed)
	require.Equal(t, uint64(1), snapshot.Released)
	require.Equal(t, uint64(1), snapshot.PeerReset)
	require.Equal(t, uint64(0), snapshot.KeepaliveTimeout)
	require.Equal(t, uint64(0), snapshot.Stopped)
	require.Equal(t, uint64(0), snapshot.Pending)
}

// TestHalfCloseDiagnosticsReleasedByKeepaliveTimeout is the other half of the released split.
func TestHalfCloseDiagnosticsReleasedByKeepaliveTimeout(t *testing.T) {
	fixture := newHalfCloseFixture(t)
	require.Eventually(t, func() bool {
		return fixture.client.keepAliveSet.Load() == 1
	}, 3*time.Second, 2*time.Millisecond)

	fixture.client.setNextReadErr(syscall.ETIMEDOUT)
	select {
	case <-fixture.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("the flow was never released")
	}

	snapshot := fixture.manager.HalfCloseDiagnostics()
	requireConserved(t, snapshot)
	require.Equal(t, uint64(1), snapshot.Released)
	require.Equal(t, uint64(0), snapshot.PeerReset)
	require.Equal(t, uint64(1), snapshot.KeepaliveTimeout,
		"an unanswered probe is the timeout half, not a reset")
}

// TestHalfCloseDiagnosticsStoppedByNormalTeardown is the common healthy path: the surviving
// direction ends the flow, and the observation must NOT be counted as a release.
func TestHalfCloseDiagnosticsStoppedByNormalTeardown(t *testing.T) {
	fixture, sink := newHalfCloseFixtureSink(t, halfCloseTestTimings)
	require.Eventually(t, func() bool {
		return fixture.client.keepAliveSet.Load() == 1
	}, 3*time.Second, 2*time.Millisecond)
	require.Eventually(t, func() bool { return sink.connCount() == 1 }, 3*time.Second, 2*time.Millisecond)

	// The peer closes: the download direction finishes, which owns the teardown.
	sink.closeAll()
	select {
	case <-fixture.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("the flow was never torn down")
	}

	snapshot := fixture.manager.HalfCloseDiagnostics()
	requireConserved(t, snapshot)
	require.Equal(t, uint64(1), snapshot.Observed)
	require.Equal(t, uint64(0), snapshot.Released, "a normal teardown is not a peer-gone verdict")
	require.Equal(t, uint64(1), snapshot.Stopped)
	require.Equal(t, uint64(0), snapshot.Pending)
}

// TestHalfCloseDiagnosticsStoppedByLocalClose is the observation losing its side of the race.
func TestHalfCloseDiagnosticsStoppedByLocalClose(t *testing.T) {
	fixture := newHalfCloseFixture(t)
	require.Eventually(t, func() bool {
		return fixture.client.keepAliveSet.Load() == 1
	}, 3*time.Second, 2*time.Millisecond)

	// A locally closed connection reports a closed error, not a peer verdict.
	fixture.client.setNextReadErr(net.ErrClosed)
	require.NoError(t, fixture.client.Close())

	require.Eventually(t, func() bool {
		return fixture.manager.HalfCloseDiagnostics().Stopped == 1
	}, 3*time.Second, 2*time.Millisecond,
		"a local close must settle the observation as stopped")

	snapshot := fixture.manager.HalfCloseDiagnostics()
	requireConserved(t, snapshot)
	require.Equal(t, uint64(0), snapshot.Released)
	require.Equal(t, uint64(1), snapshot.Stopped)
}

// TestHalfCloseDiagnosticsPendingIsADerivedState checks the state that has no verdict yet, and that
// it is derived rather than stored: nothing increments it, it is the difference.
func TestHalfCloseDiagnosticsPendingIsADerivedState(t *testing.T) {
	// A probe interval far longer than the test keeps the observation from reaching its first read.
	fixture, _ := newHalfCloseFixtureSink(t, halfCloseOptions{
		keepAliveIdle:     5 * time.Millisecond,
		keepAliveInterval: 5 * time.Millisecond,
		probeInterval:     30 * time.Second,
	})
	t.Cleanup(func() { _ = fixture.manager.Close() })
	require.Eventually(t, func() bool {
		return fixture.client.keepAliveSet.Load() == 1
	}, 3*time.Second, 2*time.Millisecond)

	snapshot := fixture.manager.HalfCloseDiagnostics()
	requireConserved(t, snapshot)
	require.Equal(t, uint64(1), snapshot.Observed)
	require.Equal(t, uint64(1), snapshot.Pending, "an observation with no read yet is pending")
	require.Equal(t, uint64(0), snapshot.Released)
	require.Equal(t, uint64(0), snapshot.Stopped)
}

// TestHalfCloseShutdownSettlesPendingObservations is the shutdown contract: the tunnel closes the
// flows, so the observations those flows still had pending are settled as stopped BEFORE the summary,
// and the summary never suggests that a pending observation was about to release something.
func TestHalfCloseShutdownSettlesPendingObservations(t *testing.T) {
	captured := &stallLog{}
	fixture, _ := newHalfCloseFixtureSinkLogged(t, halfCloseOptions{
		keepAliveIdle:     5 * time.Millisecond,
		keepAliveInterval: 5 * time.Millisecond,
		probeInterval:     30 * time.Second,
	}, captured)
	require.Eventually(t, func() bool {
		return fixture.client.keepAliveSet.Load() == 1
	}, 3*time.Second, 2*time.Millisecond)
	require.Equal(t, uint64(1), fixture.manager.HalfCloseDiagnostics().Pending)

	require.NoError(t, fixture.manager.Close())

	snapshot := fixture.manager.HalfCloseDiagnostics()
	requireConserved(t, snapshot)
	require.Equal(t, uint64(0), snapshot.Pending, "shutdown must not leave an observation pending")
	require.Equal(t, uint64(1), snapshot.Stopped)
	require.Equal(t, uint64(0), snapshot.Released)
	require.Contains(t, captured.dump(), "half-closed flows: 1 observed",
		"the summary must be emitted by the shutdown that settled it")
	require.Contains(t, captured.dump(), "pending 0")
}

// TestHalfCloseKernelSocketIsSkipped is the capability boundary, as a count rather than a comment.
//
// A kernel socket accepts the keepalive configuration but its read side cannot report the verdict
// after EOF (measured: it keeps returning EOF, and only a write sees the reset). It must therefore
// never become an observation - a pending entry that can never settle is worse than no entry.
func TestHalfCloseKernelSocketIsSkipped(t *testing.T) {
	sink := newPeerSink(t)
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	manager.halfClose = halfCloseTestTimings
	t.Cleanup(func() { _ = manager.Close() })

	var dialer net.Dialer
	clientConn, err := dialer.DialContext(context.Background(), "tcp", sink.addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { clientConn.Close() })

	closed := make(chan struct{})
	manager.NewConnection(context.Background(), halfCloseDialer{target: sink.addr()}, clientConn,
		adapter.InboundContext{Destination: M.ParseSocksaddrHostPort("peer.example", 443)},
		func(error) { close(closed) })

	// The router holds clientConn as its INBOUND socket, so the FIN has to come from that socket's
	// peer - the sink. A CloseWrite on the test's own end only tells the peer the test is done
	// sending, which is not what the router reads.
	// The sink accepts the inbound socket first and the dialer's own connection after it.
	require.Eventually(t, func() bool { return sink.connCount() >= 1 }, 3*time.Second, 2*time.Millisecond)
	require.NoError(t, sink.closeWriteFirst())

	require.Eventually(t, func() bool {
		return manager.HalfCloseDiagnostics().SkippedUnobservable == 1
	}, 3*time.Second, 2*time.Millisecond,
		"a kernel socket must be skipped as unobservable rather than observed forever")

	snapshot := manager.HalfCloseDiagnostics()
	requireConserved(t, snapshot)
	require.Equal(t, uint64(0), snapshot.Observed, "it must not be counted as an observation")
	require.Equal(t, uint64(0), snapshot.Pending)

	select {
	case <-closed:
		t.Fatal("a flow that is not observable must not be released by the mechanism")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestHalfCloseUnarmableConnectionIsSkipped is the other skip: nothing in the chain can probe.
func TestHalfCloseUnarmableConnectionIsSkipped(t *testing.T) {
	sink := newPeerSink(t)
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	manager.halfClose = halfCloseTestTimings
	t.Cleanup(func() { _ = manager.Close() })

	scripted := newScriptedConn()
	// bareConn exposes only net.Conn, so the keepalive capability is not reachable - the shape of a
	// pure userspace wrapper with no socket behind it.
	bare := &bareConn{Conn: scripted}
	manager.NewConnection(context.Background(), halfCloseDialer{target: sink.addr()}, bare,
		adapter.InboundContext{Destination: M.ParseSocksaddrHostPort("peer.example", 443)},
		func(error) {})

	require.Eventually(t, func() bool {
		return manager.HalfCloseDiagnostics().SkippedUnarmable == 1
	}, 3*time.Second, 2*time.Millisecond)
	snapshot := manager.HalfCloseDiagnostics()
	requireConserved(t, snapshot)
	require.Equal(t, uint64(0), snapshot.Observed)
}

// TestHalfCloseRefusedConfigurationIsSkipped: the connection can be asked, and says no.
func TestHalfCloseRefusedConfigurationIsSkipped(t *testing.T) {
	fixture := newHalfCloseFixture(t)
	fixture.client.setConfigErr(syscall.EINVAL)
	require.Eventually(t, func() bool {
		return fixture.manager.HalfCloseDiagnostics().SkippedUnarmable == 1
	}, 3*time.Second, 2*time.Millisecond)

	snapshot := fixture.manager.HalfCloseDiagnostics()
	requireConserved(t, snapshot)
	require.Equal(t, uint64(0), snapshot.Observed)
	select {
	case <-fixture.closed:
		t.Fatal("a connection that refused the probe must not be closed")
	case <-time.After(150 * time.Millisecond):
	}
}

// bareConn hides every optional capability.
type bareConn struct {
	net.Conn
}

// TestHalfCloseDetectionBoundMatchesTheConstants pins the latency promise to the timers it is derived
// from, so a future tuning cannot silently invalidate the record.
//
//	verdict by the userspace stack   idle + count*interval
//	verdict to the observer's read   at most the poll cap
func TestHalfCloseDetectionBoundMatchesTheConstants(t *testing.T) {
	verdict := halfClosedKeepAliveIdle + time.Duration(halfClosedKeepAliveCount)*halfClosedKeepAliveInterval
	require.LessOrEqual(t, verdict, time.Minute, "the stack's own verdict must arrive within a minute")
	require.LessOrEqual(t, verdict+halfClosedProbeMaxInterval, halfClosedObservationBound,
		"the documented bound must cover the worst case: verdict plus one poll interval")
	require.Equal(t, 2*time.Minute, halfClosedObservationBound)
	require.Less(t, halfClosedProbeInterval, halfClosedProbeMaxInterval,
		"the poll starts below its cap, or the backoff has nothing to do")
}

// TestHalfClosePollBackoffIsBounded checks the schedule itself rather than a timing observation.
func TestHalfClosePollBackoffIsBounded(t *testing.T) {
	observer := &halfClosedObserver{interval: halfClosedProbeInterval, maxInterv: halfClosedProbeMaxInterval}
	previous := time.Duration(0)
	for step := 0; step < 12; step++ {
		next := observer.nextInterval()
		require.GreaterOrEqual(t, next, previous, "the backoff must never shrink")
		require.LessOrEqual(t, next, halfClosedProbeMaxInterval)
		if previous < halfClosedProbeMaxInterval {
			require.Greater(t, next, previous, "it must grow until it reaches the cap")
		} else {
			require.Equal(t, halfClosedProbeMaxInterval, next, "and then stay at the cap")
		}
		observer.interval = next
		previous = next
	}
	require.Equal(t, halfClosedProbeMaxInterval, observer.interval, "and then stay at the cap")
}

// TestHalfCloseWalkTerminatesOnCycles covers the walk's depth and cycle guards.
func TestHalfCloseWalkTerminatesOnCycles(t *testing.T) {
	self := &selfUpstreamConn{Conn: newScriptedConn()}
	require.Nil(t, keepAliveConnOf(self), "a wrapper that points at itself must end the walk")

	var chain any = newScriptedConn()
	for depth := 0; depth < 100; depth++ {
		chain = &upstreamLinkConn{Conn: chain.(net.Conn), upstream: chain}
	}
	require.Nil(t, keepAliveConnOf(chain), "a chain deeper than the guard must end the walk")
}

// selfUpstreamConn points at itself.
type selfUpstreamConn struct {
	net.Conn
}

func (c *selfUpstreamConn) Upstream() any { return c }

// upstreamLinkConn is one link of a long chain.
type upstreamLinkConn struct {
	net.Conn
	upstream any
}

func (c *upstreamLinkConn) Upstream() any { return c.upstream }

// TestHalfCloseVerdictAndTeardownRaceSettleOnce drives the two owners at the same instant, many
// times, and requires exactly one settlement and one onClose.
func TestHalfCloseVerdictAndTeardownRaceSettleOnce(t *testing.T) {
	for attempt := 0; attempt < 30; attempt++ {
		fixture, sink := newHalfCloseFixtureSink(t, halfCloseOptions{
			keepAliveIdle:     5 * time.Millisecond,
			keepAliveInterval: 5 * time.Millisecond,
			probeInterval:     2 * time.Millisecond,
		})
		require.Eventually(t, func() bool {
			return fixture.client.keepAliveSet.Load() == 1
		}, 3*time.Second, time.Millisecond)

		// The peer's socket "disappears" at the same time as the peer closes the flow.
		fixture.client.setNextReadErr(syscall.ECONNRESET)
		go sink.closeAll()

		select {
		case <-fixture.closed:
		case <-time.After(3 * time.Second):
			t.Fatalf("attempt %d: the flow was never released or torn down", attempt)
		}

		snapshot := fixture.manager.HalfCloseDiagnostics()
		requireConserved(t, snapshot)
		require.Equal(t, uint64(1), snapshot.Observed, "attempt %d", attempt)
		require.Equal(t, uint64(1), snapshot.Released+snapshot.Stopped,
			"attempt %d: exactly one of released/stopped", attempt)
		require.Equal(t, uint64(0), snapshot.Pending, "attempt %d", attempt)
		require.Equal(t, int32(1), fixture.closeCall.Load(), "attempt %d: onClose exactly once", attempt)
	}
}
