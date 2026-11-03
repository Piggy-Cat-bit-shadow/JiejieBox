package anytls

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

// fakeConn is the smallest net.Conn that satisfies the seam under test.
type fakeConn struct{ net.Conn }

func (fakeConn) Close() error { return nil }

// burstDial builds a dial function that records how many creations run at once.
//
// The recorded peak is the number this whole mechanism exists to bound, so the test asserts on
// the OBSERVED concurrency of the expensive work rather than on the guard's own counter - a
// guard that counted correctly but admitted everything would still fail here.
type burstDial struct {
	inFlight atomic.Int64
	peak     atomic.Int64
	calls    atomic.Int64
	hold     time.Duration
	failure  error
}

func (d *burstDial) dial(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
	current := d.inFlight.Add(1)
	defer d.inFlight.Add(-1)
	d.calls.Add(1)
	for {
		observed := d.peak.Load()
		if current <= observed || d.peak.CompareAndSwap(observed, current) {
			break
		}
	}
	if d.hold > 0 {
		time.Sleep(d.hold)
	}
	if d.failure != nil {
		return nil, d.failure
	}
	return fakeConn{}, nil
}

// TestDialWithAdmissionBoundsConcurrentCreation is the regression for the measured iOS failure:
// 200 flows arriving together, each of which would otherwise start its own AnyTLS session and
// therefore its own TLS handshake.
func TestDialWithAdmissionBoundsConcurrentCreation(t *testing.T) {
	config := resourceguard.DefaultNetworkExtensionConfig()
	config.Limit = 8
	guard := resourceguard.NewGuard(config, func() resourceguard.Level { return resourceguard.LevelNormal })

	dial := &burstDial{hold: 2 * time.Millisecond}
	const burst = 200
	var wg sync.WaitGroup
	var failures atomic.Int64
	for index := 0; index < burst; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := dialWithAdmission(context.Background(), guard, dial.dial, M.Socksaddr{})
			if err != nil {
				failures.Add(1)
				return
			}
			conn.Close()
		}()
	}
	wg.Wait()

	require.Zero(t, failures.Load(), "the queue must absorb the observed burst rather than refuse it")
	require.Equal(t, int64(burst), dial.calls.Load())
	require.LessOrEqual(t, dial.peak.Load(), int64(8), "concurrent creations must stay under the limit")
	stats := guard.Stats().Gate
	require.Equal(t, uint64(burst), stats.Admitted)
	require.Zero(t, stats.Inflight, "every permit must be returned when the dial returns")
	require.Zero(t, stats.Waiters)
}

func TestDialWithAdmissionReleasesPermitOnFailure(t *testing.T) {
	config := resourceguard.DefaultNetworkExtensionConfig()
	config.Limit = 2
	guard := resourceguard.NewGuard(config, func() resourceguard.Level { return resourceguard.LevelNormal })

	dial := &burstDial{failure: net.ErrClosed}
	for index := 0; index < 10; index++ {
		_, err := dialWithAdmission(context.Background(), guard, dial.dial, M.Socksaddr{})
		require.ErrorIs(t, err, net.ErrClosed)
	}
	require.Zero(t, guard.Stats().Gate.Inflight, "a failed dial must not leak its permit")
}

func TestDialWithAdmissionWithoutGuardIsUnbounded(t *testing.T) {
	dial := &burstDial{hold: time.Millisecond}
	const burst = 64
	var wg sync.WaitGroup
	for index := 0; index < burst; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := dialWithAdmission(context.Background(), nil, dial.dial, M.Socksaddr{})
			require.NoError(t, err)
			conn.Close()
		}()
	}
	wg.Wait()
	require.Equal(t, int64(burst), dial.calls.Load())
	require.Greater(t, dial.peak.Load(), int64(1), "with no guard the dials are not serialised")
}

func TestDialWithAdmissionRefusesAtCritical(t *testing.T) {
	guard := resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), func() resourceguard.Level {
		return resourceguard.LevelCritical
	})
	// The critical limit is a quarter of the normal one; hold every permit so the next caller
	// is the one that meets the bound.
	config := resourceguard.DefaultNetworkExtensionConfig()
	held := make([]*resourceguard.Permit, 0, config.Limit/4)
	for index := 0; index < config.Limit/4; index++ {
		permit, err := guard.Acquire(context.Background(), resourceguard.ClassNormal)
		require.NoError(t, err)
		held = append(held, permit)
	}
	started := time.Now()
	_, err := dialWithAdmission(context.Background(), guard, (&burstDial{}).dial, M.Socksaddr{})
	require.Error(t, err)
	require.Less(t, time.Since(started), 2*time.Second, "critical must fail fast, not park the caller")
	for _, permit := range held {
		permit.Release()
	}
}

func BenchmarkDialWithAdmissionNoGuard(b *testing.B) {
	dial := &burstDial{}
	destination := M.Socksaddr{}
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		conn, err := dialWithAdmission(context.Background(), nil, dial.dial, destination)
		if err != nil {
			b.Fatal(err)
		}
		conn.Close()
	}
}

func BenchmarkDialWithAdmissionNormal(b *testing.B) {
	guard := resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), func() resourceguard.Level {
		return resourceguard.LevelNormal
	})
	dial := &burstDial{}
	destination := M.Socksaddr{}
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		conn, err := dialWithAdmission(context.Background(), guard, dial.dial, destination)
		if err != nil {
			b.Fatal(err)
		}
		conn.Close()
	}
}
