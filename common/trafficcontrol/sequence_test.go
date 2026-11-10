package trafficcontrol

// The event sequence is the source-side half of the platform's `dropped` counter.
//
// The observable bus drops silently when a subscriber's buffer is full, so a consumer cannot count
// what it never received. Stamping every emitted event makes the gap measurable: the daemon's
// subscription loop compares the incoming sequence with the last one it delivered and adds the
// difference to `dropped`.
//
// The tests read the manager's own subscriber rather than an observer: NewManager creates the
// subscriber, while the observer only exists after Start, and the sequence is assigned before the
// fan-out either way.

import (
	"testing"

	"github.com/gofrs/uuid/v5"

	"github.com/stretchr/testify/require"
)

func TestConnectionEventsCarryIncreasingSequences(t *testing.T) {
	manager := NewManager()
	subscription, _, err := manager.SubscribeEvents()
	require.NoError(t, err)

	tracker := &sequenceTestTracker{metadata: &TrackerMetadata{ID: uuid.Must(uuid.NewV4())}}
	manager.join(tracker)
	manager.leave(tracker)

	first := <-subscription
	second := <-subscription
	require.Equal(t, ConnectionEventNew, first.Type)
	require.Equal(t, ConnectionEventClosed, second.Type)
	require.NotZero(t, first.Sequence)
	require.Equal(t, first.Sequence+1, second.Sequence,
		"the sequence must advance by exactly one per emitted event, so a gap is a real loss")
}

func TestAFreshManagerStartsAtSequenceOne(t *testing.T) {
	manager := NewManager()
	subscription, _, err := manager.SubscribeEvents()
	require.NoError(t, err)
	manager.join(&sequenceTestTracker{metadata: &TrackerMetadata{ID: uuid.Must(uuid.NewV4())}})
	require.EqualValues(t, 1, (<-subscription).Sequence)
}

type sequenceTestTracker struct {
	metadata *TrackerMetadata
}

func (t *sequenceTestTracker) Metadata() *TrackerMetadata { return t.metadata }
func (t *sequenceTestTracker) Close() error               { return nil }
