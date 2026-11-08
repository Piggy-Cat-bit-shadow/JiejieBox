package group

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// The large-round regression: production is bounded BEFORE tasks are accepted.
//
// # The failure this pins
//
// The round used to submit each leaf through the generic batch helper, which starts ONE GOROUTINE
// PER TASK and only then waits for a concurrency token. Measured on the previous tree, a 10,000-node
// round peaked at ~9,650 goroutines, all parked before a single probe ran. This test drives a real
// round through the same group entry point and asserts that the worker count - not the member count -
// bounds the goroutines, while every member is still measured (no silent sampling).
//
// The probes fail fast on a closed port; the value under test is the production shape, not the
// network outcome.

const largeRoundWorkers = 10

type saturationOutbound struct {
	adapter.Outbound
	tag   string
	dials atomic.Int64
	block bool
}

func (o *saturationOutbound) Type() string           { return "saturation" }
func (o *saturationOutbound) Tag() string            { return o.tag }
func (o *saturationOutbound) Network() []string      { return []string{N.NetworkTCP} }
func (o *saturationOutbound) Dependencies() []string { return nil }
func (o *saturationOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	o.dials.Add(1)
	if o.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	time.Sleep(200 * time.Microsecond)
	return nil, errors.New("no route")
}
func (o *saturationOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("no route")
}

func saturationGroup(t *testing.T, members []adapter.Outbound, coordinator *urltest.Coordinator) *URLTestGroup {
	t.Helper()
	ctx := service.ContextWithPtr(context.Background(), urltest.NewHistoryStorage())
	ctx = urltest.ContextWithCoordinator(ctx, coordinator)
	group, err := NewURLTestGroup(ctx, &stubOutboundManager{}, log.NewNOPFactory().NewLogger("group"),
		members, "http://127.0.0.1:1/generate_204", 0, 0, 0, false)
	require.NoError(t, err)
	return group
}

func measureRoundPeak(t *testing.T, group *URLTestGroup, coordinator *urltest.Coordinator, baseline int) (peakGoroutines, peakQueued, peakInflight int32) {
	t.Helper()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
				if current := runtime.NumGoroutine(); current > int(atomic.LoadInt32(&peakGoroutines)) {
					atomic.StoreInt32(&peakGoroutines, int32(current))
				}
				if queued := coordinator.Queued(); queued > int(atomic.LoadInt32(&peakQueued)) {
					atomic.StoreInt32(&peakQueued, int32(queued))
				}
				if inflight := coordinator.InFlight(); inflight > int(atomic.LoadInt32(&peakInflight)) {
					atomic.StoreInt32(&peakInflight, int32(inflight))
				}
			}
		}
	}()
	group.CheckOutbounds(context.Background(), true)
	close(stop)
	<-done
	return
}

func TestLargeProbeRoundKeepsGoroutinesBounded(t *testing.T) {
	for _, count := range []int{2000, 10000} {
		t.Run(fmt.Sprintf("%d", count), func(t *testing.T) {
			members := make([]adapter.Outbound, count)
			for index := range members {
				members[index] = &saturationOutbound{tag: fmt.Sprintf("member-%d", index)}
			}
			coordinator := urltest.NewCoordinator(8)
			group := saturationGroup(t, members, coordinator)

			baseline := runtime.NumGoroutine()
			peakGoroutines, peakQueued, peakInflight := measureRoundPeak(t, group, coordinator, baseline)

			var dials int64
			for _, member := range members {
				dials += member.(*saturationOutbound).dials.Load()
			}
			require.EqualValues(t, count, dials,
				"every member must be measured exactly once; the bounded producer must not sample silently")
			require.LessOrEqual(t, int(peakGoroutines), baseline+largeRoundWorkers*2+8,
				"goroutines must be bounded by the worker count, not the member count (peak %d for %d members)",
				peakGoroutines, count)
			require.LessOrEqual(t, int(peakQueued), largeRoundWorkers,
				"the submitted-but-not-started queue must stay at the worker scale")
			require.LessOrEqual(t, int(peakInflight), 8, "the coordinator bound must hold")
			t.Logf("count=%d peakGoroutines=%d peakQueued=%d peakInflight=%d", count, peakGoroutines, peakQueued, peakInflight)
		})
	}
}

func TestProbeRoundCancellationDrains(t *testing.T) {
	const count = 2000
	members := make([]adapter.Outbound, count)
	for index := range members {
		members[index] = &saturationOutbound{tag: fmt.Sprintf("blocked-%d", index), block: true}
	}
	coordinator := urltest.NewCoordinator(8)
	group := saturationGroup(t, members, coordinator)
	baseline := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	started := time.Now()
	group.CheckOutbounds(ctx, true)
	elapsed := time.Since(started)

	require.Less(t, elapsed, 2*time.Second, "a canceled round must drain promptly")
	require.Zero(t, coordinator.InFlight())
	require.Zero(t, coordinator.Queued())
	require.Eventually(t, func() bool { return runtime.NumGoroutine() <= baseline+5 },
		2*time.Second, time.Millisecond, "no probe worker may outlive the round")
}
