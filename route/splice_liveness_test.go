package route

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	singtun "github.com/sagernet/sing-tun"

	"github.com/stretchr/testify/require"
)

// Spliced-stream liveness.
//
// # What is being pinned, and what cannot be pinned here
//
// A handover that sing-tun accepts takes the stream out of userspace: `spliceConnection` returns true
// and `NewConnection` returns before any copy loop starts, so the half-close observer never arms. For
// a DIRECT TCP flow with tracking on - the only shape whose target is a `syscall.Conn` - the remaining
// liveness signal used to be the userspace stack's keepalive, which ships with a 2h04m idle and nine
// 75s probes.
//
// The fix arms the stack's keepalive with splice timings right after the handover, so the stack itself
// notices a silent peer and detaches the stream, which runs the existing release chain
// (`detachConn` -> `spliceDetach` -> `spliceRelease` -> `OnClose` -> the tracker's close).
//
// The handover itself CANNOT be executed on this host: sing-tun refuses `Splice` unless the platform IO
// supports sockets, and the only test TUN that supports sockets is a real one (`*NativeTun`). The tests
// below therefore prove the two halves that are reachable here - the configuration that is armed, and
// that arming it on the real userspace stack makes a silent peer release the flow - and the handover's
// own release path is pinned by `TestTUNDeviceFinReleasesTrackedFlow`-style tests plus the source
// citations in `docs/fork/half-closed-flow-release.md`. What remains unproven on this host is stated
// there as `BLOCKED_BY_ENVIRONMENT`, not claimed.

type recordingKeepAlive struct {
	calls  atomic.Int64
	config atomic.Pointer[net.KeepAliveConfig]
	err    error
}

func (r *recordingKeepAlive) SetKeepAliveConfig(config net.KeepAliveConfig) error {
	r.calls.Add(1)
	r.config.Store(&config)
	return r.err
}

// TestSplicedLivenessArmsTheDocumentedTimings pins the configuration to the constants, so the bound
// the record states cannot drift away from what is actually armed.
func TestSplicedLivenessArmsTheDocumentedTimings(t *testing.T) {
	manager := NewConnectionManager(nil)

	recorder := &recordingKeepAlive{}
	require.True(t, manager.armSplicedLiveness(context.Background(), recorder))
	config := recorder.config.Load()
	require.NotNil(t, config)
	require.True(t, config.Enable)
	require.Equal(t, spliceKeepAliveIdle, config.Idle)
	require.Equal(t, spliceKeepAliveInterval, config.Interval)
	require.Equal(t, spliceKeepAliveCount, config.Count)
	require.Equal(t, uint64(1), manager.TCPSpliceDiagnostics().LivenessArmed)

	// The production bound is the idle plus the whole probe countdown; a device report quotes one
	// number, and it has to be this one.
	require.Equal(t, 3*time.Minute+30*time.Second, spliceKeepAliveIdle+time.Duration(spliceKeepAliveCount)*spliceKeepAliveInterval,
		"the documented bound is 2m + 3 x 30s = 3.5m after the peer stops answering")
}

// TestSplicedLivenessRefusalsAreNotFatal covers the two ways arming can fail, both of which must leave
// the flow to the stack's default keepalive rather than close it.
func TestSplicedLivenessRefusalsAreNotFatal(t *testing.T) {
	manager := NewConnectionManager(nil)
	require.False(t, manager.armSplicedLiveness(context.Background(), nil),
		"no target means nothing to configure")

	refusing := &recordingKeepAlive{err: net.ErrClosed}
	require.False(t, manager.armSplicedLiveness(context.Background(), refusing))
	require.Equal(t, uint64(0), manager.TCPSpliceDiagnostics().LivenessArmed,
		"a refused configuration must not be counted as an armed stream")
}

// TestSplicedLivenessSummaryCarriesTheCount is the field lever: a device log can prove the arm ran on
// the handover path instead of inferring it.
func TestSplicedLivenessSummaryCarriesTheCount(t *testing.T) {
	manager := NewConnectionManager(nil)
	require.NotContains(t, manager.TCPSpliceDiagnostics().TCPSpliceSummary(), "liveness_armed",
		"a summary without an armed stream keeps its previous exact form")

	recorder := &recordingKeepAlive{}
	require.True(t, manager.armSplicedLiveness(context.Background(), recorder))

	summary := manager.TCPSpliceDiagnostics().TCPSpliceSummary()
	require.Contains(t, summary, "TCP handover diagnostics:")
	require.Contains(t, summary, "liveness_armed=1")

	// The packet path has no half-closed stream to arm, and its summary must not grow the field.
	require.NotContains(t, manager.SpliceDiagnostics().SpliceSummary(), "liveness_armed")
}

// TestSplicedLivenessDetachesASilentUserspaceStream is the half that matters most: the connection the
// handover leaves behind is the userspace stack's socket, and arming its keepalive must make a peer
// that stopped answering release the flow - through onClose and both tables, not by deleting a row.
//
// No FIN is sent, so the half-close observer is not involved: this release can only come from the
// keepalive the handover armed.
func TestSplicedLivenessDetachesASilentUserspaceStream(t *testing.T) {
	target := startAdversarialTarget(t, adversarialSilent, 0)
	fixture := newTUNHalfCloseFixture(t, tunProbeAddr(target.port()))
	fixture.manager.spliceKeepAlive = spliceKeepAliveOptions{
		idle:     150 * time.Millisecond,
		interval: 50 * time.Millisecond,
	}

	accepted := make(chan net.Conn, 1)
	fixture.handler.onAccept = func(conn net.Conn) { accepted <- conn }

	deviceNext, stackNext := fixture.establish(t, 40300, target.port())
	fixture.waitTraffic(t, 1, 3*time.Second)
	fixture.waitRoute(t, 1, 3*time.Second)
	// A live device acknowledges the data it was sent; the flow is only "silent" once that stops.
	// Leaving the echo unacknowledged would leave data outstanding, and the stack correctly refuses
	// to probe (or detach) while it still has something to deliver.
	stopAcks := fixture.ackDevice(t, 40300, target.port(), deviceNext, stackNext)
	time.Sleep(300 * time.Millisecond)
	stopAcks()

	var goConn net.Conn
	select {
	case goConn = <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("the stack never accepted the connection")
	}
	require.IsType(t, (*singtun.GoConn)(nil), goConn)

	// The device is silent from here on: nothing acknowledges the stack's probes.
	require.True(t, fixture.manager.armSplicedLiveness(context.Background(), goConn.(keepAliveConfigurable)))

	fixture.waitTraffic(t, 0, 10*time.Second)
	fixture.waitRoute(t, 0, 5*time.Second)
	require.Equal(t, int64(1), fixture.handler.closed.Load(), "onClose must run exactly once")
	require.Equal(t, uint64(0), fixture.manager.HalfCloseDiagnostics().Observed,
		"this release must come from the splice keepalive, not from the copy-path observer")
	require.Equal(t, uint64(1), fixture.manager.TCPSpliceDiagnostics().LivenessArmed)
}

// TestSplicedLivenessKeepsALiveStream is the false-positive guard: a device that still answers keeps
// the flow, however long the peers are quiet, and only stopping the answers releases it.
func TestSplicedLivenessKeepsALiveStream(t *testing.T) {
	target := startAdversarialTarget(t, adversarialSilent, 0)
	fixture := newTUNHalfCloseFixture(t, tunProbeAddr(target.port()))
	fixture.manager.spliceKeepAlive = spliceKeepAliveOptions{
		idle:     150 * time.Millisecond,
		interval: 50 * time.Millisecond,
	}

	accepted := make(chan net.Conn, 1)
	fixture.handler.onAccept = func(conn net.Conn) { accepted <- conn }

	deviceNext, stackNext := fixture.establish(t, 40301, target.port())
	fixture.waitTraffic(t, 1, 3*time.Second)

	var goConn net.Conn
	select {
	case goConn = <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("the stack never accepted the connection")
	}
	require.True(t, fixture.manager.armSplicedLiveness(context.Background(), goConn.(keepAliveConfigurable)))

	// The device is alive and acknowledges the probes, so many idle periods pass without a release.
	stopAcks := fixture.ackDevice(t, 40301, target.port(), deviceNext, stackNext)
	time.Sleep(time.Second)
	require.Equal(t, int64(0), fixture.handler.closed.Load(),
		"a live peer must never be released by the splice keepalive")
	require.Equal(t, 1, fixture.traffic.ConnectionsLen())

	// And when it stops answering, the same armed keepalive releases it.
	stopAcks()
	fixture.waitTraffic(t, 0, 10*time.Second)
	fixture.waitRoute(t, 0, 5*time.Second)
	require.Equal(t, int64(1), fixture.handler.closed.Load())
}

// TestTUNCopyFixtureRecordsTheRefusedHandover is the guard against a test that claims to exercise the
// handover while silently falling back to copies.
//
// The memory TUN reports `supportsSockets() == false`, so `GoConn.Splice` refuses even when the target
// is a tracked, splice-eligible socket. The tests in this file therefore exercise the COPY path's
// consequence of the arm, not the handover - and this test makes that statement measurable instead of
// implied: an attempt happened, it was refused, and the reason is the one recorded.
func TestTUNCopyFixtureRecordsTheRefusedHandover(t *testing.T) {
	target := startAdversarialTarget(t, adversarialSilent, 0)
	fixture := newTUNHalfCloseFixture(t, tunProbeAddr(target.port()))
	fixture.establish(t, 40302, target.port())
	fixture.waitTraffic(t, 1, 3*time.Second)

	snapshot := fixture.manager.TCPSpliceDiagnostics()
	require.GreaterOrEqual(t, snapshot.Attempts, uint64(1), "the flow reached the handover decision")
	require.Equal(t, uint64(0), snapshot.Accepted,
		"a memory TUN cannot hand over, so these tests must not be read as splice coverage")
	require.NotZero(t, snapshot.Reasons["splice_rejected"],
		"the refusal has to be the recorded outcome, not an unrecorded path")
	require.Equal(t, uint64(0), snapshot.LivenessArmed,
		"nothing is armed on a refused handover; the copy path arms the half-close observer instead")
}

// TestSplicedLivenessAppearsInTheShutdownLog is the device path itself, not the formatter.
//
// The one line a phone run is read from is written by ConnectionManager.Close, and it is gated on the
// TCP handover counter being non-zero. This test records a handover, arms the liveness on it, closes
// the manager the way a tunnel stop does, and reads the captured log - so the field the device
// instructions tell a reader to look for is proven to be emitted there.
func TestSplicedLivenessAppearsInTheShutdownLog(t *testing.T) {
	captured := &stallLog{}
	manager := NewConnectionManager(captured.logger())

	// One accepted handover, recorded the way the stream path records it.
	manager.tcpSpliceDiagnostics.recordOutcome(spliceReasonHandoverAccepted)
	require.True(t, manager.armSplicedLiveness(context.Background(), &recordingKeepAlive{}))

	require.NoError(t, manager.Close())

	log := captured.dump()
	require.Contains(t, log, "TCP handover diagnostics:")
	require.Contains(t, log, "accepted=1")
	require.Contains(t, log, "liveness_armed=1",
		"the device line must carry the arm count, or a phone run cannot tell whether the fix ran")
}
