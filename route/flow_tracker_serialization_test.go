package route

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-tun"

	"github.com/stretchr/testify/require"
)

// multiFlowTracker must serialize AttachFlow against CloseFlow.
//
// The two callbacks arrive from different goroutines: AttachFlow when the handover is established,
// CloseFlow when the flow dies. Before upstream 92fe54be05f the fan-out was a bare slice, so a
// tracker that assumes "attach happens before close" could observe the close first. This test holds
// AttachFlow open and proves CloseFlow waits instead of entering concurrently.
type serializationTracker struct {
	inFlight atomic.Int32
	overlap  atomic.Bool
	attached chan struct{}
	release  chan struct{}
}

func (t *serializationTracker) enter() {
	if t.inFlight.Add(1) > 1 {
		t.overlap.Store(true)
	}
}

func (t *serializationTracker) AttachFlow(tun.FlowHandle) {
	t.enter()
	close(t.attached)
	<-t.release
	t.inFlight.Add(-1)
}

func (t *serializationTracker) CountForward(int) {}
func (t *serializationTracker) CountReverse(int) {}
func (t *serializationTracker) FlowEstablished() {}
func (t *serializationTracker) CloseFlow(tun.FlowCloseReason) {
	t.enter()
	t.inFlight.Add(-1)
}

func TestMultiFlowTrackerSerializesAttachAndClose(t *testing.T) {
	tracker := &serializationTracker{
		attached: make(chan struct{}),
		release:  make(chan struct{}),
	}
	fanout := &multiFlowTracker{trackers: []tun.FlowTracker{tracker}}

	go fanout.AttachFlow(nil)
	<-tracker.attached

	closed := make(chan struct{})
	go func() {
		fanout.CloseFlow(tun.FlowCloseReset)
		close(closed)
	}()

	select {
	case <-closed:
		t.Fatal("CloseFlow entered while AttachFlow was still in progress: the fan-out no longer " +
			"serializes the two lifecycle callbacks")
	case <-time.After(50 * time.Millisecond):
	}

	close(tracker.release)
	<-closed
	require.False(t, tracker.overlap.Load(),
		"the two lifecycle callbacks overlapped inside the tracker")
}

// TestMultiFlowTrackerLeavesCountersLockFree pins the half upstream deliberately did NOT serialize:
// the per-packet counters must not take the lifecycle lock, or a slow CloseFlow would stall every
// byte counted by the flow.
func TestMultiFlowTrackerLeavesCountersLockFree(t *testing.T) {
	tracker := &serializationTracker{
		attached: make(chan struct{}),
		release:  make(chan struct{}),
	}
	fanout := &multiFlowTracker{trackers: []tun.FlowTracker{tracker}}

	go fanout.AttachFlow(nil)
	<-tracker.attached

	counted := make(chan struct{})
	go func() {
		fanout.CountForward(1)
		close(counted)
	}()
	select {
	case <-counted:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("CountForward waited on the lifecycle lock; per-packet counters must stay lock-free")
	}

	close(tracker.release)
}
