package trafficcontrol

// The source sequence must describe DELIVERY order, not allocation order.
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
// The fix is one critical section around "assign the sequence, publish the event". The queue stays
// non-blocking (Emit drops when full); the sequence simply stops being able to overtake the event it
// belongs to.

import (
	"sync"
	"testing"

	"github.com/gofrs/uuid/v5"

	"github.com/stretchr/testify/require"
)

// concurrentTracker is a tracker whose identity is only used for the event stream.
func concurrentTracker() *sequenceTestTracker {
	return &sequenceTestTracker{metadata: &TrackerMetadata{ID: uuid.Must(uuid.NewV4())}}
}

// TestConcurrentEventsKeepSourceOrder is the reviewed failure: 128 goroutines each produce a NEW and
// a CLOSED event, the queue holds exactly 256, and every event is delivered.
func TestConcurrentEventsKeepSourceOrder(t *testing.T) {
	manager := NewManager()
	subscription, _ := manager.eventSubscriber.Subscription()

	const pairs = 128
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

// TestARealOverflowCountsAsAGapOnTheNextEvent is the honest drop contract. The queue holds 256; 300
// events are produced without a reader, so the tail is dropped. The 256 received events are the
// PREFIX and their sequences are contiguous - no false gap. The loss becomes visible only when a
// later event arrives, and then the gap must be exactly the number lost.
func TestARealOverflowCountsAsAGapOnTheNextEvent(t *testing.T) {
	manager := NewManager()
	subscription, _ := manager.eventSubscriber.Subscription()

	const produced = 300
	trackers := make([]*sequenceTestTracker, produced)
	for index := 0; index < produced; index++ {
		trackers[index] = concurrentTracker()
		manager.join(trackers[index])
	}

	received := make([]uint64, 0, 256)
	for len(received) < 256 {
		select {
		case event := <-subscription:
			received = append(received, event.Sequence)
		default:
			t.Fatalf("only %d of the buffered 256 events were delivered", len(received))
		}
	}
	for index, sequence := range received {
		require.EqualValues(t, index+1, sequence,
			"the buffered prefix must be contiguous: the drop happens at the tail, not in the middle")
	}

	// No further event: the tail loss is NOT observable from the delivered prefix. That is the
	// contract - the counter never invents a loss, and a silent tail stays silent until the next
	// source event (or a reconnect's snapshot) surfaces it.
	require.EqualValues(t, produced, manager.eventSequence.Load())

	// The next source event reveals the gap: sequences 257..300 were dropped.
	last := concurrentTracker()
	manager.join(last)
	var gapEvent ConnectionEvent
	select {
	case gapEvent = <-subscription:
	default:
		t.Fatalf("the revealing event was not delivered")
	}
	require.EqualValues(t, produced+1, gapEvent.Sequence)
	require.EqualValues(t, produced-256, gapEvent.Sequence-received[len(received)-1]-1,
		"the gap must be exactly the number of events the queue dropped")
}

// TestTheSequenceIsNeverZero keeps the "no sequence yet" sentinel unambiguous: zero means "an event
// from a producer that does not stamp sequences", so a stamped event must start at 1.
func TestTheSequenceIsNeverZero(t *testing.T) {
	manager := NewManager()
	subscription, _ := manager.eventSubscriber.Subscription()
	manager.join(concurrentTracker())
	require.EqualValues(t, 1, (<-subscription).Sequence)
}
