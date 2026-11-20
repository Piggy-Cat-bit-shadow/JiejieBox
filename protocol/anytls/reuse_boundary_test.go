package anytls

// The reuse boundary at the product entry (audit P04 / pairwise-a R-2).
//
// CloseIdleConnections is the trim: it drops sessions nobody is using and deliberately skips a
// session that still carries a stream. After a sleep that skip is the wrong answer - an open stream
// can be silent across one, so the session is unverified however many streams it carries - and the
// flow written onto it next is the one that discovers the path died (three seconds later, via the
// SDK's streamOpenTimeout). adapter.ReuseSuspect is the action in between: refuse new work on the
// pre-sleep session, keep the flow that is already running.
//
// This is the end-to-end shape of the state that needs it. A burst of concurrent first flows shares
// ONE session (the coalescing path), one of them finishes, and the pool lists that session for reuse
// again while the other stream is still live. From there, a demand that arrives after the boundary
// has two possible carriers and must not pick the pre-sleep one.

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// gatedListener holds the first accepted connection until gate is closed, which keeps a session
// creation in flight long enough for a second flow to join it. That is the only way a real session
// ends up carrying two live streams: takeIdleSession removes a session from the list while a dial
// uses it, so sequential dials never share one.
type gatedListener struct {
	net.Listener
	gate chan struct{}
	once sync.Once
}

func (l *gatedListener) Accept() (net.Conn, error) {
	l.once.Do(func() { <-l.gate })
	return l.Listener.Accept()
}

// sessions reports how many distinct AnyTLS sessions the server accepted: one source port per
// session, however many streams it multiplexed.
func (h *carrierHandler) sessions() int {
	h.sourceAccess.Lock()
	defer h.sourceAccess.Unlock()
	return len(h.sources)
}

type boundaryDialResult struct {
	conn net.Conn
	err  error
}

func dialBoundaryFlow(outbound *Outbound, results chan<- boundaryDialResult) {
	ctx, cancel := context.WithTimeout(context.Background(), carrierSettle)
	defer cancel()
	conn, err := outbound.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("echo.test:443"))
	results <- boundaryDialResult{conn: conn, err: err}
}

func awaitBoundaryFlow(t *testing.T, results <-chan boundaryDialResult) net.Conn {
	t.Helper()
	select {
	case result := <-results:
		require.NoError(t, result.err)
		require.NotNil(t, result.conn)
		return result.conn
	case <-time.After(carrierSettle):
		t.Fatal("a flow never returned")
		return nil
	}
}

func echoThrough(t *testing.T, conn net.Conn, payload string) {
	t.Helper()
	require.NoError(t, conn.SetDeadline(time.Now().Add(carrierSettle)))
	_, err := conn.Write([]byte(payload))
	require.NoError(t, err)
	echo := make([]byte, len(payload))
	_, err = io.ReadFull(conn, echo)
	require.NoError(t, err)
	require.Equal(t, payload, string(echo))
}

func TestTheReuseBoundaryDrainsASessionWithALiveFlow(t *testing.T) {
	handler := &carrierHandler{target: startCarrierEcho(t)}
	gate := make(chan struct{})
	port := startCarrierServerWith(t, handler, func(listener net.Listener) net.Listener {
		return &gatedListener{Listener: listener, gate: gate}
	})
	outbound := newCarrierOutbound(t, port)

	// Two flows that share one session creation, which is what a burst looks like.
	results := make(chan boundaryDialResult, 2)
	go dialBoundaryFlow(outbound, results)
	go dialBoundaryFlow(outbound, results)
	require.Eventually(t, func() bool { return outbound.client.PendingCreationWaiters() == 1 },
		carrierSettle, time.Millisecond,
		"the two flows did not share one session creation, so the attack never reached a multiplexed session")
	close(gate)
	first := awaitBoundaryFlow(t, results)
	second := awaitBoundaryFlow(t, results)
	echoThrough(t, first, "first-flow")
	echoThrough(t, second, "second-flow")
	require.Eventually(t, func() bool { return handler.sessions() == 1 && handler.streams.Load() == 2 },
		carrierSettle, time.Millisecond, "the two flows did not land on one session")

	// One stream finishes. This is what lists a session that STILL CARRIES A STREAM for reuse again,
	// and it is the state the boundary exists for.
	require.NoError(t, first.Close())
	require.Eventually(t, func() bool { return handler.streamEnds.Load() >= 1 },
		carrierSettle, 10*time.Millisecond, "the server never saw the finished stream end")

	// The production action: the reuse boundary the reference manager applies on a sleep/wake
	// boundary (power.ReuseRetire), reached through the outbound.
	outbound.RetireSuspect()

	// The flow already running on the pre-boundary session must survive: draining is not cutting.
	echoThrough(t, second, "still-carried")

	// The demand after the boundary must not be multiplexed onto the session that predates it.
	results = make(chan boundaryDialResult, 1)
	go dialBoundaryFlow(outbound, results)
	third := awaitBoundaryFlow(t, results)
	echoThrough(t, third, "after-the-boundary")
	require.Eventually(t, func() bool { return handler.sessions() == 2 },
		carrierSettle, time.Millisecond,
		"the flow after the reuse boundary was multiplexed onto the session that predates it")

	require.NoError(t, second.Close())
	require.NoError(t, third.Close())
}

// TestTheRemoteBoundaryIsReachableItSelf pins the capability at the type level: the walk asks for
// adapter.ReuseSuspect and falls back to the idle-only action for anything that does not implement
// it. Without this method the outbound is a pool that cannot express "stop new work on what you
// hold", which is the gap this pair closes.
func TestTheRemoteBoundaryIsReachable(t *testing.T) {
	var outbound any = &Outbound{}
	_, implements := outbound.(adapter.ReuseSuspect)
	require.True(t, implements,
		"protocol/anytls.Outbound no longer implements adapter.ReuseSuspect, so the reference "+
			"manager's reuse boundary falls back to CloseIdleConnections for it and every session "+
			"with a live stream keeps taking new flows after a sleep")
}
