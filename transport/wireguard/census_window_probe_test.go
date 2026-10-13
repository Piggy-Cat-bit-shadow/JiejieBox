package wireguard

import (
	"fmt"
	"testing"
	"time"

	"github.com/sagernet/sing/service/pause"

	"github.com/stretchr/testify/require"
)

// How wide is the window between "the bind's socket is released" and "the receive goroutines have
// left the runtime stack"?
//
// This is the question the macOS release gate turned on. The failing assertion is
//
//	require.Zero(t, socketCensus(t, endpoint), ...)      // immediately after onPauseUpdated returns
//
// and the pinned dependency signals its receive goroutines from inside a `defer`:
//
//	// github.com/sagernet/wireguard-go device/receive.go:79
//	func (device *Device) RoutineReceiveIncoming(maxBatchSize int, recv conn.ReceiveFunc) {
//	        defer func() {
//	                device.queue.decryption.wg.Done()
//	                device.queue.handshake.wg.Done()
//	                device.net.stopping.Done()          // <- inside the defer
//	        }()
//
// while `closeBindLocked` waits on that counter:
//
//	// device/device.go:699
//	if netc.bind != nil { err = netc.bind.Close() }   // the socket fd goes FIRST
//	netc.stopping.Wait()                              // ...then the wait
//
// A WaitGroup reaches zero when `Done()` RUNS, which is during unwinding - so `Wait()` can return
// while the last goroutine is still executing its deferred calls and is therefore still visible to
// `runtime.Stack`. The socket is closed before the wait even starts; only the goroutine's
// disappearance is unbounded.
//
// This probe measures that window instead of assuming it. It is deliberately test-only and is not an
// assertion about production behaviour.

// censusOverTime samples the receiver census for `window` and reports what it saw.
func censusOverTime(t *testing.T, endpoint *Endpoint, window time.Duration) (immediate int, max int, zeroAfter time.Duration) {
	t.Helper()
	start := time.Now()
	immediate = socketCensus(t, endpoint)
	max = immediate
	if immediate == 0 {
		return 0, 0, 0
	}
	deadline := start.Add(window)
	for time.Now().Before(deadline) {
		current := socketCensus(t, endpoint)
		if current > max {
			max = current
		}
		if current == 0 {
			return immediate, max, time.Since(start)
		}
	}
	return immediate, max, -1
}

// TestCensusWindowIsBoundedButNotZeroAtThePauseBoundary measures the window the macOS gate tripped on.
//
// This is an INSTRUMENT, not a gate. It reports the width of the window on the host it runs on and
// never fails one: a host where `runtime.Stack` still prints a receiver at the pause boundary is
// exactly the host this exists to describe, so failing there would delete the measurement. What it
// does assert is the one thing that would be a real defect rather than a scheduling window - a census
// that never reaches zero at all.
func TestCensusWindowIsBoundedButNotZeroAtThePauseBoundary(t *testing.T) {
	fixture := newRebindFixture(t, 0)
	fixture.proveStandardBindBranch(t)

	endpoint := fixture.endpoint
	nonZeroAtBoundary := 0
	const cycles = 10

	for cycle := 0; cycle < cycles; cycle++ {
		held := fixture.livePort(t)
		endpoint.onPauseUpdated(pause.EventNetworkPause)

		immediate, max, zeroAfter := censusOverTime(t, endpoint, 5*time.Second)
		if immediate != 0 {
			nonZeroAtBoundary++
		}
		t.Logf("cycle %d: immediate=%d max=%d zeroAfter=%v",
			cycle, immediate, max, zeroAfter)
		if zeroAfter < 0 {
			t.Fatalf("cycle %d: the census never reached zero within 5s (max seen %d). That is a REAL "+
				"leak rather than a scheduling window, and it must not be papered over.", cycle, max)
		}

		// The socket-release witness is unaffected by any of this: it is the OS's answer, asserted at
		// the same boundary, strictly - the assertion that must hold on every host.
		requirePortReleasedByTheEndpoint(t, fixture, held,
			fmt.Sprintf("cycle %d: a network pause must RELEASE the socket", cycle))

		endpoint.onPauseUpdated(pause.EventNetworkWake)
		require.Eventually(t, func() bool {
			return !udpPortIsFree(t, held)
		}, 10*time.Second, 2*time.Millisecond,
			"cycle %d: waking the network must reopen the bind", cycle)
		require.Eventually(t, func() bool {
			return socketCensus(t, endpoint) == socketsPerStandardBind
		}, 10*time.Second, 2*time.Millisecond,
			"cycle %d: a wake must leave exactly one socket", cycle)
	}

	if nonZeroAtBoundary == 0 {
		t.Skipf("the census reached zero within the pause boundary in all %d cycles on this host, so "+
			"this machine cannot characterise the window the macOS gate tripped on. Nothing is wrong: "+
			"the window is scheduling-dependent, and the eventual assertion in "+
			"TestPauseWakeCyclesInterleavedWithRebindsKeepOneSocket covers it either way.", cycles)
	}
	t.Logf("the census was non-zero at the pause boundary in %d of %d cycles on this host, and it "+
		"reached zero within the bounded window every time - the behaviour the eventual assertion "+
		"tolerates and a bare immediate assertion would have failed.", nonZeroAtBoundary, cycles)
}
