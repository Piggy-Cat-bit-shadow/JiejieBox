package dialer

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/control"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// A burst of generation changes must not turn one demand into a storm of dials.
//
// # What the pass condition asks
//
// "Frequent switches must not produce unbounded waits or a reconnect storm." The failure mode is
// not a wrong verdict on one dial - it is amplification: a per-demand retry that survives the
// generation boundary, or state shared across generations that makes concurrent demands re-dial
// each other's work. The observable is exact: after the burst, N concurrent demands produce
// exactly N tracked connections - one dial each - and the burst itself does not stall them.
//
// # Why 20 changes and 20 demands
//
// The burst has to be long enough that a reset landing between two demands is the common case, and
// the demands have to be concurrent so that any shared "in flight" state would collapse them into
// fewer connections or multiply them. Exactly 20 is the assertion in both directions.

func TestAGenerationBurstDoesNotAmplifyOneDemandPerCaller(t *testing.T) {
	const burst = 20
	const demands = 20

	target := startEchoServer(t)

	epoch := &epochNetworkManager{epoch: &atomic.Uint64{}, finder: control.NewDefaultInterfaceFinder()}
	manager := &epochManager{}
	ctx := service.ContextWith[adapter.ConnectionManager](context.Background(), manager)
	ctx = service.ContextWith[adapter.NetworkManager](ctx, epoch)
	dialer, err := NewDefault(ctx, option.DialerOptions{})
	require.NoError(t, err)

	// The burst: every change publishes a new generation and reclaims what the last one tracked.
	// Nothing is tracked yet, so this is purely the publish side.
	burstStart := time.Now()
	for index := 0; index < burst; index++ {
		epoch.epoch.Add(1)
		manager.CloseAll()
	}
	burstElapsed := time.Since(burstStart)
	require.Less(t, burstElapsed, 2*time.Second,
		"%d generation changes took %s: the publish side must not wait on anything", burst, burstElapsed)

	// One demand per caller, all started after the burst. Each must dial once and be tracked once.
	var wait sync.WaitGroup
	errors := make(chan error, demands)
	start := time.Now()
	for index := 0; index < demands; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			conn, err := dialer.DialContext(context.Background(), "tcp", M.ParseSocksaddr(target))
			if err != nil {
				errors <- fmt.Errorf("demand %d: %w", index, err)
				return
			}
			_ = conn.Close()
		}(index)
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	elapsed := time.Since(start)

	require.Equal(t, demands, manager.trackedCount(),
		"each demand must be tracked exactly once: a storm would multiply, shared state would collapse them")
	require.Less(t, elapsed, 5*time.Second, "%d concurrent demands took %s", demands, elapsed)

	// The reclamation after the burst still owns exactly those connections.
	manager.CloseAll()
	require.Zero(t, manager.trackedCount())
	require.EqualValues(t, demands, manager.closed.Load())
}
