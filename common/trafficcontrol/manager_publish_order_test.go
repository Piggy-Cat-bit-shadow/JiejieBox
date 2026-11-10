package trafficcontrol

// The review's "22 -> 24" counterexample: concurrent producers can invert the order of two events
// even though both reached the queue.
//
// The manager stamped an event with the next sequence INSIDE Emit, so two goroutines could take 23
// and 24 and then race for the channel: 24 landed first. A consumer that counts gaps then reports a
// permanent loss of 23 - and a late 23 cannot retract the count. Sequence order has to BE delivery
// order, at the one publish boundary.

import (
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing/common/observable"

	"github.com/stretchr/testify/require"
)

// publishOrderManager wires a manager to a queue the test can read directly, which is the producer
// queue the observer forwards from.
func publishOrderManager(t *testing.T) (*Manager, <-chan ConnectionEvent) {
	t.Helper()
	manager := NewManager()
	subscriber := observable.NewSubscriber[ConnectionEvent](256)
	manager.eventSubscriber = subscriber
	subscription, _ := subscriber.Subscription()
	require.NotNil(t, subscription)
	return manager, subscription
}

func TestConcurrentJoinsKeepTheEmissionOrder(t *testing.T) {
	const (
		rounds   = 20
		producer = 128
	)
	for round := 0; round < rounds; round++ {
		manager, subscription := publishOrderManager(t)
		var waitGroup sync.WaitGroup
		for index := 0; index < producer; index++ {
			waitGroup.Add(1)
			go func() {
				defer waitGroup.Done()
				manager.join(newRaceTestTracker(manager))
			}()
		}
		waitGroup.Wait()

		seen := make([]uint64, producer)
		for index := range seen {
			select {
			case event := <-subscription:
				seen[index] = event.Sequence
			default:
				t.Fatalf("round %d: only %d of %d events arrived", round, index, producer)
			}
		}
		for index, sequence := range seen {
			require.EqualValues(t, index+1, sequence,
				"round %d: event %d arrived as sequence %d - the number must be the delivery order", round, index+1, sequence)
		}
	}
}

// TestADroppedEventStillConsumesItsSequence is the other half: a full queue drops, but the number is
// spent, so the next delivered event names the gap.
func TestADroppedEventStillConsumesItsSequence(t *testing.T) {
	manager := NewManager()
	subscriber := observable.NewSubscriber[ConnectionEvent](2)
	manager.eventSubscriber = subscriber
	subscription, _ := subscriber.Subscription()

	for index := 0; index < 2; index++ {
		manager.join(newRaceTestTracker(manager))
	}
	// The queue is full: these three are dropped, but each still takes a number.
	for index := 0; index < 3; index++ {
		manager.join(newRaceTestTracker(manager))
	}

	require.EqualValues(t, 1, (<-subscription).Sequence)
	require.EqualValues(t, 2, (<-subscription).Sequence)

	// One slot is free now; the next event is number 6, which is how the consumer sees 3, 4 and 5
	// were lost.
	manager.join(newRaceTestTracker(manager))
	next := <-subscription
	require.EqualValues(t, 6, next.Sequence)
	require.EqualValues(t, 3, next.Sequence-2-1)
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
		manager, subscription := publishOrderManager(t)
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

		total := producer * perProducer
		var previous uint64
		for index := 0; index < total; index++ {
			select {
			case event := <-subscription:
				require.Greater(t, event.Sequence, previous,
					"round %d: event %d arrived as sequence %d after %d", round, index+1, event.Sequence, previous)
				previous = event.Sequence
			default:
				t.Fatalf("round %d: only %d of %d events arrived", round, index, total)
			}
		}
		require.EqualValues(t, total, previous, "the numbers are exactly 1..N with nothing missing")
	}
}

// TestAFastSubscriberIsNotDampedByASlowOne is the fan-out half of "a slow consumer cannot couple
// back to the data path": two listeners of the same manager, one drained, one left full. The fast
// one must keep receiving contiguous numbers while the slow one drops - and the drop must not make
// the fast one's stream shorter or gappy.
func TestAFastSubscriberIsNotDampedByASlowOne(t *testing.T) {
	manager := NewManager()
	manager.eventObserver = observable.NewObserver(manager.eventSubscriber, 64)
	t.Cleanup(func() { manager.eventObserver.Close() })
	fast, _, err := manager.eventObserver.Subscribe()
	require.NoError(t, err)
	slow, _, err := manager.eventObserver.Subscribe()
	require.NoError(t, err)

	const firstBatch = 64 // fills both listener buffers
	for index := 0; index < firstBatch; index++ {
		manager.publish(ConnectionEvent{})
	}
	require.Eventually(t, func() bool { return len(fast) == firstBatch && len(slow) == firstBatch },
		5*time.Second, time.Millisecond, "both listeners must have their buffers full")

	// Drain the fast one; the slow one stays full, so the next publications are dropped for it.
	var fastSequences []uint64
	for index := 0; index < firstBatch; index++ {
		fastSequences = append(fastSequences, (<-fast).Sequence)
	}
	const secondBatch = 10
	for index := 0; index < secondBatch; index++ {
		manager.publish(ConnectionEvent{})
	}
	require.Eventually(t, func() bool { return len(fast) == secondBatch }, 5*time.Second, time.Millisecond)
	for index := 0; index < secondBatch; index++ {
		fastSequences = append(fastSequences, (<-fast).Sequence)
	}
	require.Len(t, fastSequences, firstBatch+secondBatch)
	for index, sequence := range fastSequences {
		require.EqualValues(t, index+1, sequence,
			"the drained listener must see a contiguous stream (position %d was %d)", index+1, sequence)
	}

	// The slow listener still holds exactly the first batch, with the dropped numbers spent.
	for index := 0; index < firstBatch; index++ {
		require.EqualValues(t, index+1, (<-slow).Sequence)
	}
	manager.publish(ConnectionEvent{})
	require.Eventually(t, func() bool { return len(slow) == 1 }, 5*time.Second, time.Millisecond)
	require.EqualValues(t, firstBatch+secondBatch+1, (<-slow).Sequence,
		"the slow listener's next event names everything it lost")
}

// TestClosingTheBusEndsTheSubscription is the stop path: closing the observer must release every
// subscriber (the daemon blocks on that done channel) and refuse new ones.
func TestClosingTheBusEndsTheSubscription(t *testing.T) {
	manager := NewManager()
	manager.eventObserver = observable.NewObserver(manager.eventSubscriber, 4)
	_, done, err := manager.eventObserver.Subscribe()
	require.NoError(t, err)

	require.NoError(t, manager.eventObserver.Close())
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("closing the bus must signal every subscriber's done channel")
	}
	_, _, err = manager.eventObserver.Subscribe()
	require.Error(t, err, "a closed bus must refuse new subscriptions")
}
