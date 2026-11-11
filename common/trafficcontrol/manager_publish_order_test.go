package trafficcontrol

// The producer's ordering and loss contract, adapted from the observable-based tests to the
// fanout implementation that replaced it.
//
// The reviewed failure: the manager stamped an event with the next sequence INSIDE Emit, so two
// goroutines could take 23 and 24 and then race for the channel: 24 landed first and a consumer
// that counts gaps reported a permanent loss of 23. `publishEvent` now assigns the number and hands
// the event to the fanout under one lock, so number order IS delivery order.
//
// The second half is the loss that is real: a full per-subscription queue drops, but the number is
// already spent, so the next delivered event names the gap. The fanout counts that drop per
// subscription (`SubscribeEventsWithLoss`), which is what makes a silent tail recoverable.

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func orderSubscriptionWithLoss(t *testing.T, manager *Manager) (<-chan ConnectionEvent, func() uint64) {
	t.Helper()
	subscription, _, dropped, err := manager.SubscribeEventsWithLoss()
	require.NoError(t, err)
	return subscription, dropped
}

// drainUntilIdle reads everything already delivered, stopping after a short quiet window. The
// producers have finished by the time this is called, so the quiet window means "the queue is
// empty", not "a producer is slow".
func drainUntilIdle(subscription <-chan ConnectionEvent) []uint64 {
	sequences := make([]uint64, 0, eventListenerBuffer)
	quiet := time.NewTimer(100 * time.Millisecond)
	defer quiet.Stop()
	for {
		select {
		case event := <-subscription:
			sequences = append(sequences, event.Sequence)
			quiet.Reset(100 * time.Millisecond)
		case <-quiet.C:
			return sequences
		}
	}
}

func requireStrictlyIncreasing(t *testing.T, sequences []uint64, message string) {
	t.Helper()
	for index := 1; index < len(sequences); index++ {
		require.Greater(t, sequences[index], sequences[index-1],
			"%s: position %d carried %d after %d", message, index, sequences[index], sequences[index-1])
	}
}

func TestConcurrentJoinsKeepTheEmissionOrder(t *testing.T) {
	const (
		rounds   = 20
		producer = 128
	)
	for round := 0; round < rounds; round++ {
		manager := NewManager()
		subscription, dropped := orderSubscriptionWithLoss(t, manager)

		var waitGroup sync.WaitGroup
		for index := 0; index < producer; index++ {
			waitGroup.Add(1)
			go func() {
				defer waitGroup.Done()
				manager.join(newRaceTestTracker(manager))
			}()
		}
		waitGroup.Wait()

		seen := drainUntilIdle(subscription)
		requireStrictlyIncreasing(t, seen, "delivery order must be sequence order")
		require.Equal(t, producer, len(seen)+int(dropped()),
			"round %d: every produced event is either delivered or counted as dropped", round)
		require.EqualValues(t, 1, seen[0], "the first delivered event carries the first number")
	}
}

// TestADroppedEventStillConsumesItsSequence is the other half: a full queue drops, but the number
// is spent, so the next delivered event names the gap.
func TestADroppedEventStillConsumesItsSequence(t *testing.T) {
	manager := NewManager()
	// Subscribe and do not read: the queue holds eventListenerBuffer events, the rest drop.
	subscription, dropped := orderSubscriptionWithLoss(t, manager)

	// Fill the queue and overflow it by three events.
	total := eventListenerBuffer + 3
	for index := 0; index < total; index++ {
		manager.join(newRaceTestTracker(manager))
	}
	require.EqualValues(t, 3, dropped(), "the overflow is counted immediately, before any read")

	seen := drainUntilIdle(subscription)
	require.Len(t, seen, eventListenerBuffer, "the queued prefix is bounded by the listener buffer")
	requireStrictlyIncreasing(t, seen, "the surviving prefix keeps its order")
	for index, sequence := range seen {
		require.EqualValues(t, index+1, sequence, "queued events keep their assigned numbers")
	}

	// One slot is free now; the next event is number total+1, which is how the consumer sees that
	// three numbers are missing.
	manager.join(newRaceTestTracker(manager))
	select {
	case next := <-subscription:
		require.EqualValues(t, total+1, next.Sequence)
		require.EqualValues(t, 3, next.Sequence-uint64(eventListenerBuffer)-1)
	case <-time.After(5 * time.Second):
		t.Fatal("the post-overflow event never arrived")
	}
}

// TestConcurrentJoinsAndLeavesKeepTheEmissionOrder includes the OTHER producer: `leave` publishes a
// closed event through the same path. If ordering held only for join, a mixed workload would still
// invert - and closed events are exactly the ones a client cannot re-derive.
func TestConcurrentJoinsAndLeavesKeepTheEmissionOrder(t *testing.T) {
	const (
		rounds      = 10
		producer    = 64
		perProducer = 2 // one join + one leave
	)
	for round := 0; round < rounds; round++ {
		manager := NewManager()
		subscription, dropped := orderSubscriptionWithLoss(t, manager)
		total := producer * perProducer

		var waitGroup sync.WaitGroup
		for index := 0; index < producer; index++ {
			waitGroup.Add(1)
			go func() {
				defer waitGroup.Done()
				tracker := newRaceTestTracker(manager)
				manager.join(tracker)
				manager.leave(tracker)
			}()
		}
		waitGroup.Wait()

		seen := drainUntilIdle(subscription)
		requireStrictlyIncreasing(t, seen, "mixed join/leave delivery order")
		require.Equal(t, total, len(seen)+int(dropped()),
			"round %d: the two producers are fully accounted for", round)
		require.EqualValues(t, 1, seen[0])
	}
}

// TestAFastSubscriberIsNotDampedByASlowOne is the fan-out half of "a slow consumer cannot couple
// back to the data path": two subscriptions of the same manager, one drained, one left full. The
// fast one must keep receiving contiguous numbers while the slow one drops - and the drop must not
// make the fast one's stream shorter or gappy.
func TestAFastSubscriberIsNotDampedByASlowOne(t *testing.T) {
	manager := NewManager()
	fast, _ := orderSubscriptionWithLoss(t, manager)
	slow, slowDropped := orderSubscriptionWithLoss(t, manager)

	collected := make(chan []uint64, 1)
	go func() {
		collected <- drainUntilIdle(fast)
	}()

	// Exactly one buffer of events: the fast subscriber drains them, the stalled one fills its
	// queue. The number is small enough that the fast reader cannot fall behind by more than the
	// buffer, so this test is about DAMPING, not about scheduling.
	for index := 0; index < eventListenerBuffer; index++ {
		manager.join(newRaceTestTracker(manager))
	}

	seen := <-collected
	require.Len(t, seen, eventListenerBuffer, "the drained subscriber receives every event")
	for index, sequence := range seen {
		require.EqualValues(t, index+1, sequence,
			"the drained subscriber keeps every number; the stalled one must not damp it")
	}
	require.Equal(t, eventListenerBuffer, len(slow),
		"the stalled subscriber's queue is bounded, and it stopped at that bound")
	require.Zero(t, slowDropped(), "the stalled queue is full but nothing overflowed yet")
}
