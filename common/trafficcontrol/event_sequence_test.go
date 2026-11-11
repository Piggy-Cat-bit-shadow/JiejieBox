package trafficcontrol

// The producer half of the connection-event loss signal.
//
// The per-subscription pipe is deliberately lossy: a full queue drops an event instead of blocking a
// connection. That is the right trade for a data path, but it means the consumer's view can be
// missing events with no other trace. `Sequence` is that trace: the manager stamps every emission
// under the publish lock, so a gap the consumer observes is a drop it can count.

import (
	"testing"
	"time"

	"github.com/sagernet/sing/common/observable"

	"github.com/stretchr/testify/require"
)

// TestEveryEmissionAdvancesTheSequence pins the producer's ordering.
func TestEveryEmissionAdvancesTheSequence(t *testing.T) {
	manager := NewManager()
	subscription, _, err := manager.SubscribeEvents()
	require.NoError(t, err)

	for index := 0; index < 4; index++ {
		manager.publishEvent(ConnectionEvent{})
	}
	var observed []uint64
	for index := 0; index < 4; index++ {
		select {
		case event := <-subscription:
			observed = append(observed, event.Sequence)
		case <-time.After(5 * time.Second):
			t.Fatal("the emission never arrived")
		}
	}
	require.Equal(t, []uint64{1, 2, 3, 4}, observed, "the sequence is monotonic and gap-free")
}

// TestASlowSubscriberSeesTheGapItsBufferCaused documents the queue semantics the fanout relies on
// with the library primitive it is built from: a full buffer drops inside Emit, and the next
// delivered event names the gap.
func TestASlowSubscriberSeesTheGapItsBufferCaused(t *testing.T) {
	subscriber := observable.NewSubscriber[ConnectionEvent](2)
	subscription, _ := subscriber.Subscription()
	require.NotNil(t, subscription)
	require.Equal(t, 2, cap(subscription))

	subscriber.Emit(ConnectionEvent{Sequence: 1})
	subscriber.Emit(ConnectionEvent{Sequence: 2})
	subscriber.Emit(ConnectionEvent{Sequence: 3}) // dropped: the buffer is full
	subscriber.Emit(ConnectionEvent{Sequence: 4}) // dropped

	require.EqualValues(t, 1, (<-subscription).Sequence)
	require.EqualValues(t, 2, (<-subscription).Sequence)

	// One slot is free now, so the next emission is delivered - after two that were lost.
	subscriber.Emit(ConnectionEvent{Sequence: 5})
	third := <-subscription
	require.EqualValues(t, 5, third.Sequence,
		"the consumer sees the gap; without Sequence it would only see a shorter stream")
	// The gap the daemon's tracker turns into Dropped: 5 - 2 - 1 = 2.
	require.EqualValues(t, 2, third.Sequence-2-1)
}
