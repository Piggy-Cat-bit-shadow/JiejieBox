package libbox

// O02 — the emitter's contract: order, a begin, visible loss, bounded retention, and a Close
// that finalises exactly once. These are the properties the acceptance asks to observe with a
// slow subscriber, a lost begin and a final snapshot.

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestEmitter() *connectionTestEmitter {
	return newConnectionTestEmitter(emitterBounds{Queue: 4, Retention: 4, MaxBytes: 1024}, time.Now())
}

func TestTheStreamStartsWithABeginAndKeepsSequenceOrder(t *testing.T) {
	emitter := newTestEmitter()
	defer emitter.Close(0)
	emitter.Publish(connectionTestEvent{Kind: eventBegin, Name: "connection test"})
	emitter.Publish(connectionTestEvent{Kind: eventStage, Stage: "path", Outcome: outcomeOK})
	emitter.Publish(connectionTestEvent{Kind: eventStage, Stage: "dial", Outcome: outcomeFailed})

	var events []connectionTestEvent
	for len(events) < 3 {
		select {
		case event := <-emitter.Events():
			events = append(events, event)
		case <-time.After(time.Second):
			t.Fatal("the stream did not deliver the published events")
		}
	}
	require.Equal(t, eventBegin, events[0].Kind, "the first event is always a begin")
	for index := 1; index < len(events); index++ {
		require.Greater(t, events[index].Seq, events[index-1].Seq, "a gap must be detectable by sequence")
	}
}

func TestASlowSubscriberLosesEventsVisibly(t *testing.T) {
	emitter := newTestEmitter() // queue of two, nobody reading
	for index := 0; index < 7; index++ {
		emitter.Publish(connectionTestEvent{Kind: eventStage, Stage: "path", Outcome: outcomeOK})
	}
	emitter.Close(0)
	require.GreaterOrEqual(t, emitter.Dropped(), uint64(3), "a full queue drops events instead of blocking the test")

	snapshot := emitter.Snapshot()
	require.NotEmpty(t, snapshot)
	var sawDropFinding bool
	for _, event := range snapshot {
		if event.Outcome == outcomeDropped {
			sawDropFinding = true
		}
	}
	require.True(t, sawDropFinding, "the loss must be reported as a finding, not hidden")
}

func TestRetentionIsBoundedByCount(t *testing.T) {
	emitter := newTestEmitter() // retention of four
	for index := 0; index < 10; index++ {
		emitter.Publish(connectionTestEvent{Kind: eventStage, Stage: "path", Outcome: outcomeOK, Detail: strings.Repeat("x", 8)})
	}
	emitter.Close(0)
	snapshot := emitter.Snapshot()
	require.LessOrEqual(t, len(snapshot), 4+3, "retention is bounded; only the final snapshot/end are added")
	require.True(t, snapshot[len(snapshot)-1].Kind == eventEnd, "the last event is the end")
}

func TestALateSubscriberCanReconstructTheStream(t *testing.T) {
	emitter := newTestEmitter()
	emitter.Publish(connectionTestEvent{Kind: eventBegin, Name: "connection test"})
	emitter.Publish(connectionTestEvent{Kind: eventStage, Stage: "path", Outcome: outcomeOK})
	// A late reader did not see those two events live; the snapshot must contain the begin.
	snapshot := emitter.Snapshot()
	require.Equal(t, eventBegin, snapshot[0].Kind, "a late join never starts mid-narrative")
	emitter.Close(0)
}

func TestCloseFinalisesExactlyOnce(t *testing.T) {
	emitter := newTestEmitter()
	emitter.Close(time.Millisecond)
	emitter.Close(time.Millisecond) // idempotent
	select {
	case <-emitter.Finished():
	case <-time.After(time.Second):
		t.Fatal("Close must finish the stream")
	}
	snapshot := emitter.Snapshot()
	endCount := 0
	for _, event := range snapshot {
		if event.Kind == eventEnd {
			endCount++
		}
	}
	require.Equal(t, 1, endCount, "the end is emitted once, so 'no more events' is a fact")
}
