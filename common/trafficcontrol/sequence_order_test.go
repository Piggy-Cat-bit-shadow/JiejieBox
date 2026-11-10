package trafficcontrol

// The source sequence must describe DELIVERY order, and a drop must be counted exactly once, for the
// subscription that lost it.
//
// # The defect these tests pin
//
// `join` and `leave` used to do `Sequence: m.eventSequence.Add(1)` INSIDE the struct literal passed to
// `Emit`. The increment and the channel send are two separate steps, so two goroutines can allocate
// 1 and 2 and then publish 2 before 1: the subscriber receives sequences out of order, and the
// daemon's gap accounting reads that as "events were lost" when none were. A false drop is worse than
// no counter - it makes the client throw away a complete snapshot and resynchronise on every busy
// second.
//
// # And the drop that is real
//
// The fanout never blocks the data plane: a full listener queue drops. Unlike the previous
// observable.Observer path, THE DROP IS COUNTED PER SUBSCRIPTION, which is what allows the daemon to
// publish a loss signal while the source is silent. These tests separate the two situations the
// recovery contract depends on: queued-but-not-yet-read (no drop) and actually dropped (counted).

import (
	"sync"
	"testing"

	"github.com/gofrs/uuid/v5"

	"github.com/stretchr/testify/require"
)

func concurrentTracker() *sequenceTestTracker {
	return &sequenceTestTracker{metadata: &TrackerMetadata{ID: uuid.Must(uuid.NewV4())}}
}

// TestConcurrentEventsKeepSourceOrder is the reviewed ordering failure: concurrent producers must
// deliver sequences in the order they were assigned.
func TestConcurrentEventsKeepSourceOrder(t *testing.T) {
	manager := NewManager()
	subscription, _, err := manager.SubscribeEvents()
	require.NoError(t, err)

	// The listener queue holds eventListenerBuffer; produce exactly that many events so every one is
	// delivered and the ordering assertion covers the whole queue.
	pairs := eventListenerBuffer / 2
	var workers sync.WaitGroup
	for index := 0; index < pairs; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			tracker := concurrentTracker()
			manager.join(tracker)
			manager.leave(tracker)
		}()
	}
	workers.Wait()

	require.EqualValues(t, 2*pairs, manager.eventSequence.Load())
	sequences := make([]uint64, 0, 2*pairs)
	for len(sequences) < 2*pairs {
		select {
		case event := <-subscription:
			sequences = append(sequences, event.Sequence)
		default:
			t.Fatalf("only %d of %d events were delivered; the queue must hold them all", len(sequences), 2*pairs)
		}
	}
	for index, sequence := range sequences {
		require.EqualValues(t, index+1, sequence,
			"delivery order must be sequence order; position %d carried %d", index, sequence)
	}
}

// TestQueuedEventsAreNotCountedAsDropped separates "slow" from "lost": a listener that has not read
// yet has its events in its queue, and the counter must stay zero.
func TestQueuedEventsAreNotCountedAsDropped(t *testing.T) {
	manager := NewManager()
	subscription, _, dropped, err := manager.SubscribeEventsWithLoss()
	require.NoError(t, err)

	for index := 0; index < eventListenerBuffer; index++ {
		manager.join(concurrentTracker())
	}
	require.Zero(t, dropped(), "queued events are not lost events")
	for index := 0; index < eventListenerBuffer; index++ {
		<-subscription
	}
	require.Zero(t, dropped())
}

// TestARealOverflowIsCountedImmediately is the silent-tail contract at the source: a full queue drops,
// the counter knows at once (no later event is needed to reveal it), and the buffered prefix is
// contiguous.
func TestARealOverflowIsCountedImmediately(t *testing.T) {
	manager := NewManager()
	subscription, _, dropped, err := manager.SubscribeEventsWithLoss()
	require.NoError(t, err)

	const produced = 300
	for index := 0; index < produced; index++ {
		manager.join(concurrentTracker())
	}
	require.EqualValues(t, produced-eventListenerBuffer, dropped(),
		"the counter must report the drop immediately; silence must not hide it")

	received := make([]uint64, 0, eventListenerBuffer)
	for len(received) < eventListenerBuffer {
		select {
		case event := <-subscription:
			received = append(received, event.Sequence)
		default:
			t.Fatalf("only %d of the buffered events were delivered", len(received))
		}
	}
	for index, sequence := range received {
		require.EqualValues(t, index+1, sequence,
			"the buffered prefix must be contiguous: the drop happens at the tail, not in the middle")
	}
}

// TestOneSlowSubscriberDoesNotAffectAnother makes the attribution explicit and deterministic: the
// fast subscriber is kept drained while the slow one is never read, so the slow queue overflows and
// the fast queue never does. The fast reader is not racing the producer for a limited budget - each
// publish is preceded by exactly one drain - which is what makes the assertion stable under load.
func TestOneSlowSubscriberDoesNotAffectAnother(t *testing.T) {
	manager := NewManager()
	fast, _, fastDropped, err := manager.SubscribeEventsWithLoss()
	require.NoError(t, err)
	_, _, slowDropped, err := manager.SubscribeEventsWithLoss()
	require.NoError(t, err)

	// Fill BOTH queues without reading: exactly the buffer size, so nothing is dropped yet.
	for index := 0; index < eventListenerBuffer; index++ {
		manager.join(concurrentTracker())
	}
	require.Zero(t, fastDropped())
	require.Zero(t, slowDropped())

	// Now make room in the fast queue before every publish; the slow queue stays full and drops.
	const extra = 36
	for index := 0; index < extra; index++ {
		<-fast
		manager.join(concurrentTracker())
	}
	for index := 0; index < extra; index++ {
		<-fast
	}

	require.Zero(t, fastDropped(), "a drained subscriber reports no loss")
	require.EqualValues(t, extra, slowDropped(), "the slow subscriber's loss is its own")
}

// TestTheSequenceIsNeverZero keeps the "no sequence yet" sentinel unambiguous: zero means "an event
// from a producer that does not stamp sequences", so a stamped event must start at 1.
func TestTheSequenceIsNeverZero(t *testing.T) {
	manager := NewManager()
	subscription, _, err := manager.SubscribeEvents()
	require.NoError(t, err)
	manager.join(concurrentTracker())
	require.EqualValues(t, 1, (<-subscription).Sequence)
}
