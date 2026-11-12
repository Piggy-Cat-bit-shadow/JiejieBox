package group

// Q08: the Box bounds how many measurement rounds PRODUCE at once, not how many members a round may
// submit.
//
// # The failure these pin
//
// The measurement budget bounded the probes in flight, but nothing bounded the producers. Each round
// starts its own worker set and its own submitted-task queue, so N rounds firing together created N
// worker sets - measured on the pre-budget tree: 4 rounds produced 40 coordinator waiters and 44
// extra goroutines, 32 rounds produced 320 and 352. The queue grew with the number of concurrent
// rounds, which is the audit's "全 Box 测速任务生产/等待队列没有独立硬上界".
//
// # Why the bound cannot be a queue cap
//
// A cap on the measurement queue refuses waiters, and a refused waiter is a member that was never
// measured. Round 08 measured exactly that: a cap of max(limit*4, 8) made the 10,000-node gate answer
// 9,976 of 10,000 members. The budget is therefore taken BEFORE a round creates anything
// (protocol/group/urltest.go, runMeasurementRound), and a round that cannot take a slot waits.
//
// What these tests assert, in the audit's terms:
//   - 全局常数上界: with 4 rounds and with 32 rounds the created goroutines and the coordinator queue
//     stay at the same constant, because a fixed number of rounds may produce at once;
//   - 全测不漏项: every round still measures every member (dials == rounds × members);
//   - 交互公平: an interactive round is admitted ahead of background rounds that queued earlier;
//   - 取消排空: a round waiting for its budget returns on cancellation and leaves no entry behind.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"runtime"
	"sync"
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

// roundBudgetOutbound is one member of a gated round: it counts its dials and fails fast, so a round
// measures without touching the network and without creating a goroutine per dial (which is what the
// goroutine assertion must not confuse with round production).
type roundBudgetOutbound struct {
	adapter.Outbound
	tag   string
	sleep time.Duration
	dials atomic.Int64
}

func (o *roundBudgetOutbound) Type() string      { return "round-budget" }
func (o *roundBudgetOutbound) Tag() string       { return o.tag }
func (o *roundBudgetOutbound) Network() []string { return []string{N.NetworkTCP} }

func (o *roundBudgetOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	o.dials.Add(1)
	if o.sleep > 0 {
		select {
		case <-time.After(o.sleep):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, errors.New("no route")
}

func (o *roundBudgetOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("no route")
}

// heldRoundOutbound parks its dial until a token arrives, so a test can hold a round slot.
type heldRoundOutbound struct {
	adapter.Outbound
	tag     string
	entered atomic.Int64
	release chan struct{}
}

func (o *heldRoundOutbound) Type() string      { return "round-held" }
func (o *heldRoundOutbound) Tag() string       { return o.tag }
func (o *heldRoundOutbound) Network() []string { return []string{N.NetworkTCP} }

func (o *heldRoundOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	o.entered.Add(1)
	select {
	case <-o.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return nil, errors.New("no route")
}

func (o *heldRoundOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("no route")
}

// roundBudgetSample is a live sample of the counters a round budget has to bound.
type roundBudgetSample struct {
	goroutines     atomic.Int64
	roundsInFlight atomic.Int64
	roundsQueued   atomic.Int64
	measureQueued  atomic.Int64
}

func sampleRoundBudget(t *testing.T, coordinator *urltest.Coordinator) *roundBudgetSample {
	t.Helper()
	sample := &roundBudgetSample{}
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
				recordPeak64(&sample.goroutines, int64(runtime.NumGoroutine()))
				recordPeak64(&sample.roundsInFlight, int64(coordinator.RoundsInFlight()))
				recordPeak64(&sample.roundsQueued, int64(coordinator.RoundsQueued()))
				recordPeak64(&sample.measureQueued, int64(coordinator.Queued()))
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		<-done
	})
	return sample
}

// runOneRoundFromTest is the round entry point under test, called the way a group health check, a
// Clash delay probe and a native group test all call it.
func runOneRoundFromTest(ctx context.Context, manager adapter.OutboundManager, members []adapter.Outbound, link string) {
	URLTestOutboundsWithTarget(ctx, manager, urltest.NewHistoryStorage(),
		log.NewNOPFactory().NewLogger("round-budget"), members, link,
		urltest.ExpectedStatus{}, 0, true, TestHistoryHealth)
}

// TestTheBoxBoundsProducingRoundsNotMeasuredMembers is the Q08 acceptance gate.
func TestTheBoxBoundsProducingRoundsNotMeasuredMembers(t *testing.T) {
	const membersPerRound = 200
	// roundWorkers mirrors the per-round worker count newProbeBatch is given.
	const roundWorkers = 10

	for _, roundCount := range []int{4, 32} {
		roundCount := roundCount
		t.Run(fmt.Sprintf("%d rounds", roundCount), func(t *testing.T) {
			coordinator := urltest.NewCoordinator(10)
			ctx := urltest.ContextWithCoordinator(context.Background(), coordinator)

			server := httptest.NewServer(nil)
			t.Cleanup(server.Close)
			link := server.URL + "/generate_204"

			members := make([]adapter.Outbound, membersPerRound)
			manager := &taggedOutboundManager{byTag: make(map[string]adapter.Outbound, membersPerRound)}
			for index := range members {
				member := &roundBudgetOutbound{
					tag:   fmt.Sprintf("node-%03d", index),
					sleep: 200 * time.Microsecond,
				}
				members[index] = member
				manager.byTag[member.tag] = member
			}

			sample := sampleRoundBudget(t, coordinator)
			baseline := runtime.NumGoroutine()

			// One goroutine per round is the CALLER's own demand: holding a caller back is the
			// budget working, so those goroutines are not what the bound is about. What must not grow
			// is what the rounds CREATE.
			var waitGroup sync.WaitGroup
			waitGroup.Add(roundCount)
			for range roundCount {
				go func() {
					defer waitGroup.Done()
					runOneRoundFromTest(ctx, manager, members, link)
				}()
			}
			waitGroup.Wait()

			var total int64
			for _, member := range members {
				dials := member.(*roundBudgetOutbound).dials.Load()
				require.EqualValues(t, roundCount, dials,
					"member %s was measured %d time(s) in %d rounds: the round budget must hold a "+
						"round back, never drop one of its members", member.Tag(), dials, roundCount)
				total += dials
			}
			require.EqualValues(t, int64(roundCount*membersPerRound), total)

			// 全局常数上界. The created goroutines are the round workers of at most
			// maxConcurrentRounds rounds; the callers waiting for a slot are counted separately, and
			// the submitted-but-not-started tasks are channel entries, not goroutines.
			require.LessOrEqual(t, int(sample.goroutines.Load()),
				baseline+roundCount+2*roundWorkers+32,
				"the rounds created a goroutine per round, not a worker set per round: peak %d "+
					"goroutines for %d rounds (baseline %d)",
				sample.goroutines.Load(), roundCount, baseline)
			require.LessOrEqual(t, int(sample.roundsInFlight.Load()), 2,
				"more rounds were producing at once than the round budget allows")
			require.LessOrEqual(t, int(sample.measureQueued.Load()), 10,
				"the coordinator's measuring queue exceeded its own bound; the round budget must "+
					"keep the waiters at worker scale")
			require.LessOrEqual(t, int(sample.roundsQueued.Load()), roundCount)
			t.Logf("%d rounds × %d members: peak goroutines=%d (baseline %d), roundsInFlight=%d, "+
				"roundsQueued=%d, measureQueued=%d, dials=%d",
				roundCount, membersPerRound, sample.goroutines.Load(), baseline,
				sample.roundsInFlight.Load(), sample.roundsQueued.Load(),
				sample.measureQueued.Load(), total)

			// 取消/排空: every counter returns to zero, and no round worker outlives its round.
			require.Zero(t, coordinator.InFlight())
			require.Zero(t, coordinator.Queued())
			require.Zero(t, coordinator.RoundsInFlight())
			require.Zero(t, coordinator.RoundsQueued())
			require.Eventually(t, func() bool { return runtime.NumGoroutine() <= baseline+8 },
				5*time.Second, time.Millisecond, "the rounds left goroutines behind")
		})
	}
}

// TestTheRoundBudgetPutsDemandAheadOfQueuedBackgroundRounds pins 交互公平.
//
// Both round slots are held by rounds parked in a dial. A background round queues first, then an
// interactive one. When a slot frees, the interactive round must be admitted first: a person waiting
// for a test must not wait behind periodic health work that asked a moment earlier.
func TestTheRoundBudgetPutsDemandAheadOfQueuedBackgroundRounds(t *testing.T) {
	coordinator := urltest.NewCoordinator(10)
	base := urltest.ContextWithCoordinator(context.Background(), coordinator)
	server := httptest.NewServer(nil)
	t.Cleanup(server.Close)
	link := server.URL + "/generate_204"

	held := &heldRoundOutbound{tag: "held", release: make(chan struct{}, 4)}
	manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{"held": held}}
	heldMembers := []adapter.Outbound{held}

	backgroundCtx := adapter.ContextWithBackgroundProbe(base)

	// Two background rounds take both round slots and park inside their dial.
	heldRounds := make(chan struct{}, 2)
	var heldWaitGroup sync.WaitGroup
	for range 2 {
		heldWaitGroup.Add(1)
		go func() {
			defer heldWaitGroup.Done()
			runOneRoundFromTest(backgroundCtx, manager, heldMembers, link)
			heldRounds <- struct{}{}
		}()
	}
	require.Eventually(t, func() bool { return held.entered.Load() == 2 },
		5*time.Second, time.Millisecond, "both round slots must be held before the queue is built")

	// The queued rounds: a background one first, then an interactive one. The interactive round is
	// slow on purpose, so it demonstrably HOLDS its slot while the background round is still queued.
	background := &roundBudgetOutbound{tag: "background-node"}
	interactive := &roundBudgetOutbound{tag: "interactive-node", sleep: 300 * time.Millisecond}
	queuedManager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{
		background.tag:  background,
		interactive.tag: interactive,
	}}

	backgroundDone := make(chan struct{})
	go func() {
		defer close(backgroundDone)
		runOneRoundFromTest(backgroundCtx, queuedManager, []adapter.Outbound{background}, link)
	}()
	interactiveDone := make(chan struct{})
	go func() {
		defer close(interactiveDone)
		// No background marker: this is what a user test or the Clash API supplies.
		runOneRoundFromTest(base, queuedManager, []adapter.Outbound{interactive}, link)
	}()

	require.Eventually(t, func() bool { return coordinator.RoundsQueued() == 2 },
		5*time.Second, time.Millisecond,
		"both rounds must be waiting for a round slot (queued=%d)", coordinator.RoundsQueued())

	// Release exactly one held dial: one slot frees.
	held.release <- struct{}{}

	require.Eventually(t, func() bool { return interactive.dials.Load() == 1 },
		5*time.Second, time.Millisecond,
		"the interactive round was not admitted when a slot freed")
	// The second held round still occupies the other slot, so the background round cannot be running
	// yet - and by the time it could be, this assertion has already been made.
	require.Zero(t, background.dials.Load(),
		"the background round that queued FIRST was admitted before the interactive one: the queue "+
			"order must prefer declared demand, or a user's test waits behind periodic health work")

	// Let the rest finish: the second held round, then the interactive round, then the background one.
	held.release <- struct{}{}
	for range 2 {
		select {
		case <-heldRounds:
		case <-time.After(5 * time.Second):
			t.Fatal("a held round never returned")
		}
	}
	select {
	case <-interactiveDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the interactive round never returned")
	}
	select {
	case <-backgroundDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the background round never returned")
	}
	heldWaitGroup.Wait()
	require.EqualValues(t, 1, background.dials.Load(), "the background round still measures its member")

	require.Zero(t, coordinator.RoundsInFlight())
	require.Zero(t, coordinator.RoundsQueued())
	require.Zero(t, coordinator.InFlight())
	require.Zero(t, coordinator.Queued())
}

// TestAQueuedRoundReturnsWhenItsContextEnds pins 取消排空 for the round budget.
func TestAQueuedRoundReturnsWhenItsContextEnds(t *testing.T) {
	coordinator := urltest.NewCoordinator(10)
	base := urltest.ContextWithCoordinator(context.Background(), coordinator)
	server := httptest.NewServer(nil)
	t.Cleanup(server.Close)
	link := server.URL + "/generate_204"

	held := &heldRoundOutbound{tag: "held", release: make(chan struct{}, 4)}
	manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{"held": held}}
	backgroundCtx := adapter.ContextWithBackgroundProbe(base)

	var heldWaitGroup sync.WaitGroup
	for range 2 {
		heldWaitGroup.Add(1)
		go func() {
			defer heldWaitGroup.Done()
			runOneRoundFromTest(backgroundCtx, manager, []adapter.Outbound{held}, link)
		}()
	}
	require.Eventually(t, func() bool { return held.entered.Load() == 2 },
		5*time.Second, time.Millisecond, "both round slots must be held")

	waitingCtx, cancelWaiting := context.WithCancel(backgroundCtx)
	waitingDone := make(chan error, 1)
	go func() {
		_, _, _, err := runMeasurementRound(waitingCtx, manager, urltest.NewHistoryStorage(),
			log.NewNOPFactory().NewLogger("round-budget"), []adapter.Outbound{held}, link,
			urltest.ExpectedStatus{}, 0, true, TestHistoryHealth)
		waitingDone <- err
	}()
	require.Eventually(t, func() bool { return coordinator.RoundsQueued() == 1 },
		5*time.Second, time.Millisecond, "the third round must be waiting for a round slot")

	cancelWaiting()
	select {
	case err := <-waitingDone:
		require.ErrorIs(t, err, context.Canceled,
			"a round that never got a slot must report WHY it measured nothing, not an empty success")
	case <-time.After(5 * time.Second):
		t.Fatal("a round waiting for its budget must return when its context ends")
	}
	require.Zero(t, coordinator.RoundsQueued(), "the cancelled round left a queue entry behind")

	held.release <- struct{}{}
	held.release <- struct{}{}
	heldWaitGroup.Wait()
	require.Zero(t, coordinator.RoundsInFlight())
	require.Zero(t, coordinator.RoundsQueued())
}
