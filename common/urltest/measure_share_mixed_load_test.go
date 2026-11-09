package urltest

// The Box-wide picture: many groups, many providers, one coordinator, one share.
//
// The per-caller tests prove the contract of one question. This file proves the property the
// per-Box bound exists for: six groups measuring two providers' members at once must dial once per
// distinct node, stay inside the coordinator's limit, queue the rest, answer every caller, and
// leave nothing behind - no slot, no in-flight entry, no participant, no goroutine.

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// boxSnapshot records the coordinator and share counters while a load is running.
type boxSnapshot struct {
	peakInFlight     atomic.Int32
	peakQueued       atomic.Int32
	peakParticipants atomic.Int32
}

func watchBox(t *testing.T, ctx context.Context, coordinator *Coordinator, share *MeasurementShare) *boxSnapshot {
	t.Helper()
	snapshot := &boxSnapshot{}
	watchCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
				recordPeak(&snapshot.peakInFlight, coordinator.InFlight())
				recordPeak(&snapshot.peakQueued, coordinator.Queued())
				recordPeak(&snapshot.peakParticipants, share.Participants())
			}
		}
	}()
	t.Cleanup(func() {
		stop()
		<-done
	})
	return snapshot
}

func recordPeak(peak *atomic.Int32, value int) {
	for {
		current := peak.Load()
		if int32(value) <= current || peak.CompareAndSwap(current, int32(value)) {
			return
		}
	}
}

func TestSixGroupsAndTwoProvidersShareOneBoxBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(10 * time.Millisecond)
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	link := server.URL + "/generate_204"

	const (
		groupCount     = 6
		perProvider    = 5
		providerCount  = 2
		coordinatorCap = 3
	)
	nodeTags := make([]string, 0, providerCount*perProvider)
	for provider := 0; provider < providerCount; provider++ {
		for member := 0; member < perProvider; member++ {
			nodeTags = append(nodeTags, providerTag(provider, member))
		}
	}

	coordinator := NewCoordinator(coordinatorCap)
	share := NewMeasurementShare()
	boxCtx := ContextWithMeasurementShare(ContextWithCoordinator(context.Background(), coordinator), share)
	snapshot := watchBox(t, context.Background(), coordinator, share)

	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	goroutinesBefore := runtime.NumGoroutine()

	dialer := &probeCountingDialer{target: server.Listener.Addr().String()}

	// Every group submits every member: equivalent questions arrive from different groups, which is
	// exactly the case the share and the Box budget have to resolve together.
	var waitGroup sync.WaitGroup
	start := make(chan struct{})
	var successes atomic.Int32
	var failures atomic.Int32
	for group := 0; group < groupCount; group++ {
		for _, tag := range nodeTags {
			waitGroup.Add(1)
			go func(tag string) {
				defer waitGroup.Done()
				<-start
				key := MeasurementKey{Node: tag, Target: link, Purpose: PurposeHealth}
				_, err := Measure(boxCtx, MeasureOptions{Link: link, Key: &key}, dialer)
				if err != nil {
					failures.Add(1)
					return
				}
				successes.Add(1)
			}(tag)
		}
	}
	close(start)
	waitGroup.Wait()

	require.Zero(t, failures.Load(), "no caller may be failed by the shared budget")
	require.EqualValues(t, groupCount*len(nodeTags), successes.Load(), "every caller receives an answer")
	require.EqualValues(t, len(nodeTags), dialer.dials.Load(),
		"one dial per distinct node, however many groups asked for it")
	require.LessOrEqual(t, int(snapshot.peakInFlight.Load()), coordinatorCap,
		"the Box-wide probe limit must hold across groups and providers")
	require.Greater(t, snapshot.peakQueued.Load(), int32(0),
		"the surplus is queued behind the limit instead of dialling past it")
	require.GreaterOrEqual(t, snapshot.peakParticipants.Load(), int32(2),
		"equivalent callers from different groups really did wait on one probe")

	// Nothing is left behind: no slot, no question, no participant.
	require.Zero(t, coordinator.InFlight())
	require.Zero(t, coordinator.Queued())
	require.Zero(t, share.InFlight())
	require.Zero(t, share.Participants())

	// The shared probes ran on goroutines this package started; they must all be gone.
	deadline := time.Now().Add(2 * time.Second)
	for {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		if runtime.NumGoroutine() <= goroutinesBefore+4 || time.Now().After(deadline) {
			break
		}
	}
	require.LessOrEqual(t, runtime.NumGoroutine(), goroutinesBefore+4,
		"the shared measurements must not leave goroutines behind; before=%d after=%d",
		goroutinesBefore, runtime.NumGoroutine())
}

func providerTag(provider, member int) string {
	return string(rune('a'+provider)) + "-node-" + string(rune('0'+member))
}

// probeCountingDialer counts dials and forwards to one local listener.
type probeCountingDialer struct {
	target string
	dials  atomic.Int32
}

func (d *probeCountingDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	d.dials.Add(1)
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, d.target)
}

func (d *probeCountingDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// keep the import set honest if the fixture above is edited later.
var _ N.Dialer = (*probeCountingDialer)(nil)
