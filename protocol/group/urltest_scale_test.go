package group

// P02B at the scale the Box budget was designed for.
//
// The small mixed-load evidence (six groups, two providers, ten nodes) proves the SHAPE. This file
// proves the shape still holds when the registry, the coordinator queues and the result maps are
// asked to carry 2,000 and 10,000 distinct nodes twice over: every caller must be answered, the
// number of dials must equal the number of distinct nodes (not callers), the coordinator's limit
// must hold, and every counter must return to zero.
//
// The target server answers immediately on purpose: this measures bookkeeping, not network latency.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// scaleProbeOutbound is one node: it counts its dials and dials the local target directly.
type scaleProbeOutbound struct {
	adapter.Outbound
	tag   string
	dials atomic.Int64
}

func (o *scaleProbeOutbound) Type() string      { return "scale-probe" }
func (o *scaleProbeOutbound) Tag() string       { return o.tag }
func (o *scaleProbeOutbound) Network() []string { return []string{N.NetworkTCP} }

func (o *scaleProbeOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	o.dials.Add(1)
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, destination.String())
}

func (o *scaleProbeOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// peakCoordinator watches the coordinator's live counters while the rounds run.
type peakCoordinator struct {
	inFlight atomic.Int64
	queued   atomic.Int64
}

func watchCoordinator(t *testing.T, coordinator *urltest.Coordinator) *peakCoordinator {
	t.Helper()
	peak := &peakCoordinator{}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				recordPeak64(&peak.inFlight, int64(coordinator.InFlight()))
				recordPeak64(&peak.queued, int64(coordinator.Queued()))
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		<-done
	})
	return peak
}

func recordPeak64(peak *atomic.Int64, value int64) {
	for {
		current := peak.Load()
		if value <= current || peak.CompareAndSwap(current, value) {
			return
		}
	}
}

func TestTheBoxBudgetHoldsAtProviderScale(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	link := server.URL + "/generate_204"

	for _, nodeCount := range []int{2000, 10000} {
		nodeCount := nodeCount
		t.Run(fmt.Sprintf("%d nodes", nodeCount), func(t *testing.T) {
			if testing.Short() && nodeCount > 2000 {
				t.Skip("the 10,000-node case is skipped under -short")
			}
			if raceEnabled && nodeCount > 2000 {
				t.Skip("the 10,000-node case is skipped under -race: it measures the race runtime, not the budget")
			}
			nodes := make([]*scaleProbeOutbound, nodeCount)
			nodesByTag := make(map[string]adapter.Outbound, nodeCount)
			cast := make([]adapter.Outbound, nodeCount)
			for index := range nodes {
				node := &scaleProbeOutbound{tag: fmt.Sprintf("node-%05d", index)}
				nodes[index] = node
				nodesByTag[node.tag] = node
				cast[index] = node
			}

			history := urltest.NewHistoryStorage()
			share := urltest.NewMeasurementShare()
			coordinator := urltest.NewCoordinator(3)
			peak := watchCoordinator(t, coordinator)
			ctx := urltest.ContextWithCoordinator(
				urltest.ContextWithMeasurementShare(context.Background(), share), coordinator)
			manager := &taggedOutboundManager{byTag: nodesByTag}

			baselineGoroutines := runtime.NumGoroutine()

			// TWO health rounds over the same nodes at once: every caller is answered, and the
			// share turns two callers per node into one dial per node.
			round := func() <-chan map[string]uint16 {
				result := make(chan map[string]uint16, 1)
				go func() {
					result <- URLTestOutboundsWithTarget(ctx, manager, history,
						log.NewNOPFactory().NewLogger("scale"), cast, link,
						urltest.ExpectedStatus{}, 0, true, TestHistoryHealth)
				}()
				return result
			}
			started := time.Now()
			first := round()
			second := round()
			firstResult := <-first
			secondResult := <-second
			elapsed := time.Since(started)

			require.Len(t, firstResult, nodeCount, "every node is answered in the first round")
			require.Len(t, secondResult, nodeCount, "every node is answered in the second round")

			dials := int64(0)
			for _, node := range nodes {
				dials += node.dials.Load()
			}
			t.Logf("%d nodes: dials=%d elapsed=%s peakInFlight=%d peakQueued=%d",
				nodeCount, dials, elapsed, peak.inFlight.Load(), peak.queued.Load())

			require.EqualValues(t, nodeCount, dials,
				"two equivalent health rounds over the same nodes must dial once per node, not once per caller")
			require.LessOrEqual(t, peak.inFlight.Load(), int64(3), "the coordinator's limit holds at scale")
			require.Zero(t, share.InFlight(), "no question survives the rounds")
			require.Zero(t, share.Participants())
			require.Zero(t, coordinator.InFlight(), "no coordinator slot survives the rounds")
			require.Zero(t, coordinator.Queued())
			require.LessOrEqual(t, runtime.NumGoroutine(), baselineGoroutines+nodeCount/100+32,
				"the rounds must not leave a goroutine per node behind")
		})
	}
}
