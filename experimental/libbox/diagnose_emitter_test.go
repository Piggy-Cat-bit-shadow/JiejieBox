package libbox

// O02 — the emitter's contract: order, a begin, visible loss, bounded retention, and a Close
// that finalises exactly once. These are the properties the acceptance asks to observe with a
// slow subscriber, a lost begin and a final snapshot.

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

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

// TestCloseAndPublishShareOneFinalisationBoundary stresses the window the old two-step Publish
// opened: a publisher that observed closed=false could append a stage after Close had already
// emitted the end event. The snapshot must always end with exactly one end event, and no event may
// be appended after it.
func TestCloseAndPublishShareOneFinalisationBoundary(t *testing.T) {
	emitter := newConnectionTestEmitter(emitterBounds{Queue: 8, Retention: 16, MaxBytes: 4096}, time.Now())
	const publishers = 16
	const eventsPerPublisher = 64
	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	waitGroup.Add(publishers)
	for index := 0; index < publishers; index++ {
		go func() {
			defer waitGroup.Done()
			<-start
			for event := 0; event < eventsPerPublisher; event++ {
				emitter.Publish(connectionTestEvent{Kind: eventStage, Stage: "path", Outcome: outcomeOK})
			}
		}()
	}
	closeDone := make(chan struct{})
	go func() {
		<-start
		emitter.Close(time.Millisecond)
		close(closeDone)
	}()
	close(start)
	waitGroup.Wait()
	<-closeDone

	snapshot := emitter.Snapshot()
	require.NotEmpty(t, snapshot)
	endCount := 0
	for index, event := range snapshot {
		if event.Kind == eventEnd {
			endCount++
			require.Equal(t, len(snapshot)-1, index, "an event was appended after the end event")
		}
	}
	require.Equal(t, 1, endCount, "the finalisation boundary must emit exactly one end event")
	require.Equal(t, eventEnd, snapshot[len(snapshot)-1].Kind)
}

// recordingSink is a platform sink that keeps what it was delivered.
type recordingSink struct {
	events []string
}

func (s *recordingSink) Emit(event string) { s.events = append(s.events, event) }

// TestTheSubscriberSeesTheSameNarrativeAsTheReport drives the streaming entry with a fake path
// so the live delivery and the returned report can be compared.
func TestTheSubscriberSeesTheSameNarrativeAsTheReport(t *testing.T) {
	path := newConnectionTestPath("proxy-a", &fakeConnectionOutbound{tag: "proxy-a", behavior: func(context.Context, string, M.Socksaddr) (net.Conn, error) { return stubConn{}, nil }})
	sink := &recordingSink{}
	emitter := newConnectionTestEmitter(emitterBounds{Queue: 64, Retention: 128, MaxBytes: 1 << 16}, time.Now())
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			select {
			case event := <-emitter.Events():
				sink.Emit(renderEvent(event))
			case <-emitter.Finished():
				// The stream is finished, but events queued before it closed may not have been
				// consumed yet. Draining them is what makes "the subscriber saw the narrative"
				// deterministic instead of dependent on which channel a select happened to pick.
				for {
					select {
					case event := <-emitter.Events():
						sink.Emit(renderEvent(event))
					default:
						return
					}
				}
			}
		}
	}()
	report := runConnectionTestWithEmitter(context.Background(), path, "proxy-a", "tcp", "https://example.com:443", emitter)
	<-drained
	require.NotEmpty(t, sink.events)
	require.Contains(t, sink.events[0], "begin", "the subscriber sees the begin first")
	require.NotEmpty(t, report.Events)
	require.Contains(t, report.Events[0], "begin", "the report narrates the same run")
}
