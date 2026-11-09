package trusttunnel

import (
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
)

// closeTestFlow builds one admitted association with a single queued datagram
// of size bytes, on a budget that also holds other budget so a double return
// would be visible. It returns the flow, its session and the budget.
func closeTestFlow(t *testing.T, associations, queuedBytes int, size int) (*udpFlow, *udpSession, *udpBudget) {
	t.Helper()
	budget := newUDPBudget(UDPLimits{
		ServiceAssociations: associations,
		ServiceQueuedBytes:  queuedBytes,
		SessionQueuedBytes:  queuedBytes,
	})
	for i := 0; i < associations-1; i++ {
		if !budget.acquireAssociation() {
			t.Fatal("setup could not reserve the other association")
		}
	}
	if !budget.acquireAssociation() {
		t.Fatal("setup could not reserve the flow association")
	}
	if !budget.acquireBytes(queuedBytes - size) {
		t.Fatal("setup could not reserve the other queued bytes")
	}
	if !budget.acquireBytes(size) {
		t.Fatal("setup could not reserve the flow bytes")
	}
	session := &udpSession{
		service:     &Service{udp: budget},
		flows:       make(map[udpFlowKey]*udpFlow),
		queuedBytes: size,
	}
	flow := &udpFlow{
		session: session,
		packets: make(chan *buf.Buffer, 1),
		done:    make(chan struct{}),
	}
	packet := buf.NewSize(size)
	_, err := packet.Write(make([]byte, size))
	if err != nil {
		t.Fatal(err)
	}
	flow.packets <- packet
	session.flows[flow.key] = flow
	return flow, session, budget
}

// TestUDPConcurrentCloseWaitsForBudget holds the session lock so the first
// Close stops between signalling done and returning the budget. A second Close
// must not return in that window: when it does return, the association slot and
// the queued bytes are back, exactly once, and the slot is admitted again.
func TestUDPConcurrentCloseWaitsForBudget(t *testing.T) {
	flow, session, budget := closeTestFlow(t, 2, 8, 4)

	session.access.Lock()
	first := make(chan struct{})
	go func() {
		flow.Close()
		close(first)
	}()
	select {
	case <-flow.done:
	case <-time.After(time.Second):
		session.access.Unlock()
		t.Fatal("the first Close did not reach its cleanup")
	}

	second := make(chan UDPStats, 1)
	go func() {
		flow.Close()
		second <- budget.stats()
	}()
	select {
	case stats := <-second:
		session.access.Unlock()
		<-first
		t.Fatalf("the second Close returned while cleanup was still blocked: %+v", stats)
	case <-time.After(100 * time.Millisecond):
	}
	session.access.Unlock()
	<-first
	if stats := <-second; stats.Associations != 1 || stats.QueuedBytes != 4 {
		t.Fatalf("the close returned the wrong budget: %+v", stats)
	}

	// The returned slot and bytes can be taken again: the flow returned its
	// association and its four queued bytes, not more and not less.
	if !budget.acquireAssociation() {
		t.Fatal("the association slot was not reusable after Close returned")
	}
	if !budget.acquireBytes(4) {
		t.Fatal("the queued bytes were not reusable after Close returned")
	}
	if budget.acquireAssociation() {
		t.Fatal("the flow returned its association slot twice")
	}
	budget.releaseAssociation()
	budget.releaseBytes(4)
	budget.releaseAssociation()
	budget.releaseBytes(4)
	if stats := budget.stats(); stats.Associations != 0 || stats.QueuedBytes != 0 {
		t.Fatalf("the budget did not return to rest: %+v", stats)
	}
}

// TestUDPConcurrentCloseAndExpiryReleaseOnce races the sweeper's closeIfIdle
// with a handler Close: the loser waits for the winner's cleanup, reports that
// it did not close, and still releases the budget exactly once.
func TestUDPConcurrentCloseAndExpiryReleaseOnce(t *testing.T) {
	flow, session, budget := closeTestFlow(t, 2, 8, 4)

	session.access.Lock()
	first := make(chan struct{})
	go func() {
		flow.Close()
		close(first)
	}()
	select {
	case <-flow.done:
	case <-time.After(time.Second):
		session.access.Unlock()
		t.Fatal("the handler Close did not reach its cleanup")
	}

	expired := make(chan bool, 1)
	go func() {
		// A deadline after the last activity would let the sweeper close the
		// flow if it were the winner; here it must lose to the handler.
		expired <- flow.closeIfIdle(time.Now().Add(time.Hour).UnixNano())
	}()
	select {
	case closed := <-expired:
		session.access.Unlock()
		<-first
		t.Fatalf("the sweeper returned closed=%v while the cleanup was blocked", closed)
	case <-time.After(100 * time.Millisecond):
	}
	session.access.Unlock()
	<-first
	if closed := <-expired; closed {
		t.Fatal("the sweeper claimed a close that the handler won")
	}
	flow.Close()
	if stats := budget.stats(); stats.Associations != 1 || stats.QueuedBytes != 4 {
		t.Fatalf("the concurrent closes did not return the budget exactly once: %+v", stats)
	}
	budget.releaseAssociation()
	budget.releaseBytes(4)
	if stats := budget.stats(); stats.Associations != 0 || stats.QueuedBytes != 0 {
		t.Fatalf("the budget did not return to rest: %+v", stats)
	}
}
