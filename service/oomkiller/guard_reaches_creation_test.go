package oomkiller

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

// errCreationReached is returned by the recording dialer when the expensive creation actually
// starts. It doubles as the proof of reachability: the test asserts on the CALL, not on the error.
var errCreationReached = errors.New("the expensive creation was started")

// recordingDialer stands in for the TCP connect a TLS outbound performs before its handshake. This
// is the last cheap point before the expensive work (key exchange, per-connection goroutines, the
// handshake buffers), which is exactly where the admission seam sits.
type recordingDialer struct {
	dials int
}

func (d *recordingDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	d.dials++
	return nil, errCreationReached
}

func (d *recordingDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errCreationReached
}

// TestPublishedCriticalPressureStopsARealExpensiveCreation is the A01 pairing test: OOMKiller's
// published pressure, the guard's level, and a REAL creation seam, in one deterministic chain.
//
// # What was missing before it
//
// service/oomkiller's own tests pin the pressure→level mapping (guard_wiring_test.go) and the
// protocol packages pin "a guard at Critical refuses" with a guard they built by hand. Neither
// proves the two meet: that the level the OOMKILLER publishes is the same knob that stops a real
// outbound from creating, and that the seam refuses BEFORE the expensive work rather than after it.
// That is the difference between "the governor is installed" and "the governor acted", which is the
// distinction this pairing exists to make.
//
// # Why the dialer counter is the assertion
//
// Every refusal bucket is a counter the code sets about itself. A test that only read those would
// pass even if the seam were called after the dial. The recording dialer is the independent result:
// under Critical it must not be reached at all.
//
// # The numbers are chosen so no timer decides the outcome
//
// Limit 2, queue 0. Under Normal one held permit leaves room and the TLS dial reaches the dialer.
// Under Critical the derived limit is 1, so the same held permit fills the gate and the next
// acquisition is refused in constant time (ErrQueueFull) instead of queueing. Nothing sleeps, and
// nothing depends on scheduling.
func TestPublishedCriticalPressureStopsARealExpensiveCreation(t *testing.T) {
	service := &Service{}
	guard := resourceguard.NewGuard(resourceguard.Config{
		Limit:       2,
		Queue:       0,
		Wait:        time.Second,
		DemoteAfter: 10 * time.Millisecond,
	}, service.guardLevel)
	ctx := resourceguard.NewContext(t.Context(), guard)

	config, err := tls.NewSTDClient(t.Context(), logger.NOP(), "creation.example", option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: "creation.example",
		// The dialer never completes a connection, so verification is never reached.
		Insecure: true,
	})
	require.NoError(t, err)
	dialer := &recordingDialer{}
	tlsDialer := tls.NewDialer(dialer, config)
	destination := M.ParseSocksaddrHostPort("creation.example", 443)

	// Normal: the seam admits, and the creation really starts.
	_, err = tlsDialer.DialTLSContext(ctx, destination)
	require.ErrorIs(t, err, errCreationReached,
		"at LevelNormal the admission seam must let the creation through")
	require.Equal(t, 1, dialer.dials)

	// One permit in flight is all the tightened plan has room for.
	holder, err := guard.Acquire(ctx, resourceguard.ClassNormal)
	require.NoError(t, err)
	require.NotNil(t, holder)

	// The status the OOMKiller publishes on its critical path (timer.go releaseMemory stores this
	// exact value) is now the input to the guard.
	service.pressure.Store(uint32(tun.MemoryPressureCritical))

	_, err = tlsDialer.DialTLSContext(ctx, destination)
	require.Error(t, err)
	require.ErrorIs(t, err, resourceguard.ErrQueueFull,
		"a critical process must refuse the creation at the gate, not start it and then notice")
	require.True(t, resourceguard.IsLocalRejection(err),
		"the refusal is this process's own decision and must stay classifiable as local: a group "+
			"policy that reads it as a node failure would eject a healthy node during a memory crisis")
	require.Equal(t, 1, dialer.dials,
		"the expensive creation started anyway: the gate is not between the decision and the work")

	// Observability must describe the same state as the decision.
	stats := guard.Stats()
	require.Equal(t, resourceguard.LevelCritical, stats.Level)
	require.Equal(t, 1, stats.Gate.Limit, "the reported limit must be the one the refusal used")
	require.EqualValues(t, 1, stats.Gate.Refused)

	// Recovery: pressure falls, and the same seam lets the creation through again without any
	// intervention beyond the level returning to normal. The dwell is the guard's own hysteresis.
	service.pressure.Store(uint32(tun.MemoryPressureNone))
	require.Eventually(t, func() bool {
		return guard.Level() == resourceguard.LevelNormal
	}, 2*time.Second, time.Millisecond, "the guard never demoted after the pressure was cleared")
	holder.Release()
	_, err = tlsDialer.DialTLSContext(ctx, destination)
	require.ErrorIs(t, err, errCreationReached,
		"the seam stayed closed after recovery: pressure removal must restore admission by itself")
	require.Equal(t, 2, dialer.dials)
}
