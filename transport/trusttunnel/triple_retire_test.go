package trusttunnel

// T08 at the product entry: a body that never ends, a cancelled flow and a pool retirement, all on
// the same carrier (audit triple-wise A).
//
// The three systems are not "three tests run together": each changes what the other two are allowed
// to conclude.
//
//   - The BODY (a tunnel the server accepts and never answers on) is what makes the session's stream
//     count non-zero while nothing is moving. Without it the retirement would see an idle session and
//     closing it would be correct.
//   - The CANCEL (a second flow closed mid-transition) decrements that count. Without it the
//     retirement's drain decision would never change state after the retirement.
//   - The RETIREMENT (RetireSuspect, plus the trim that runs beside it) is the action whose verdict
//     ("nobody is using this") the first two keep invalidating. Without it there is no destructive
//     decision to get wrong.
//
// The invariant: while ANY body is open - the stalled one, the cancelled one before it closes, or the
// healthy sibling - the carrier must not be closed, and once the last one returns it must close. The
// healthy sibling is what turns "the session closed" from a bookkeeping statement into a
// user-visible one.

import (
	"net"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

func dialForTriple(t *testing.T, client *Client, destination M.Socksaddr) net.Conn {
	t.Helper()
	conn, err := client.DialContext(testContext(t), destination)
	require.NoError(t, err)
	require.NotNil(t, conn)
	return conn
}

// TestRetirementSurvivesABodyCancelAndAStalledFlow is the triple in the order the boundary walk runs:
// retire first, then the trim, then a cancel lands, then the bodies close one by one.
func TestRetirementSurvivesABodyCancelAndAStalledFlow(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			server := startTestServer(t, protocol)
			echo := startEchoServer(t)
			dialer := &testDialer{}
			client := server.newClient(t, protocol, testPassword, dialer, nil)

			// The body that never ends: hold.test is accepted and drained by the server, so the
			// CONNECT is answered and the tunnel stays silent forever.
			stalled := dialForTriple(t, client, holdDestination())
			// A second silent body; this one is the flow that will be cancelled.
			doomed := dialForTriple(t, client, holdDestination())
			// The healthy sibling on the same carrier.
			healthy := dialForTriple(t, client, echo)
			requireEcho(t, healthy, "before-the-transition")

			client.access.Lock()
			session := client.session
			client.access.Unlock()
			require.NotNil(t, session)
			require.EqualValues(t, 1, dialer.dials.Load(), "the three flows did not share one carrier")
			require.EqualValues(t, 3, session.streams.Load())

			// The boundary walk: the session carries three streams, so retirement must drain it and
			// the trim must leave it alone.
			client.RetireSuspect()
			client.CloseIdleConnections()
			require.True(t, session.conn.alive(), "the retirement closed a carrier that was carrying streams")
			require.EqualValues(t, 3, session.streams.Load())

			// The healthy flow crosses the retirement untouched.
			requireEcho(t, healthy, "after-the-retirement")

			// The cancel lands after the retirement: it releases its own slot only.
			require.NoError(t, doomed.Close())
			require.Eventually(t, func() bool { return session.streams.Load() == 2 },
				5*time.Second, time.Millisecond, "the cancelled flow did not return its slot")
			require.True(t, session.conn.alive(), "the cancelled flow took the carrier with it")
			requireEcho(t, healthy, "after-the-cancel")

			// The stalled body closes, but the healthy sibling still holds the carrier.
			require.NoError(t, stalled.Close())
			require.Eventually(t, func() bool { return session.streams.Load() == 1 },
				5*time.Second, time.Millisecond, "the stalled flow did not return its slot")
			require.True(t, session.conn.alive(),
				"the retiring carrier closed while the healthy sibling was still running")
			requireEcho(t, healthy, "after-the-stalled-close")

			// The last body returns: the drained session closes here, and nowhere earlier.
			require.NoError(t, healthy.Close())
			require.Eventually(t, func() bool { return !session.conn.alive() },
				5*time.Second, time.Millisecond,
				"the drained session did not close after its last stream returned")
			require.Eventually(t, func() bool { return server.handler.holding.Load() == 0 },
				5*time.Second, time.Millisecond,
				"the server never released the two silent CONNECTs")
			require.EqualValues(t, 1, dialer.dials.Load(),
				"the transition made the pool dial again instead of draining")
		})
	}
}

// TestACancelledFlowBeforeTheRetirementLeavesTheCarrierUsable runs the same three systems in the
// opposite order: the cancel happens first, so the retirement sees a carrier that is already one
// stream lighter - and must still refuse to close it while the others are open.
func TestACancelledFlowBeforeTheRetirementLeavesTheCarrierUsable(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			server := startTestServer(t, protocol)
			echo := startEchoServer(t)
			dialer := &testDialer{}
			client := server.newClient(t, protocol, testPassword, dialer, nil)

			stalled := dialForTriple(t, client, holdDestination())
			doomed := dialForTriple(t, client, holdDestination())
			healthy := dialForTriple(t, client, echo)
			requireEcho(t, healthy, "before")

			client.access.Lock()
			session := client.session
			client.access.Unlock()
			require.NotNil(t, session)

			// Cancel first, then the retirement, then the trim.
			require.NoError(t, doomed.Close())
			require.Eventually(t, func() bool { return session.streams.Load() == 2 }, 5*time.Second, time.Millisecond)
			client.RetireSuspect()
			client.CloseIdleConnections()
			require.True(t, session.conn.alive(), "a retirement taken after the cancel closed a busy carrier")
			require.EqualValues(t, 2, session.streams.Load())
			requireEcho(t, healthy, "after")

			// One stalled body remains: closing it must not close the carrier the sibling holds.
			require.NoError(t, stalled.Close())
			require.Eventually(t, func() bool { return session.streams.Load() == 1 }, 5*time.Second, time.Millisecond)
			require.True(t, session.conn.alive())
			requireEcho(t, healthy, "after-the-stalled-close")

			require.NoError(t, healthy.Close())
			require.Eventually(t, func() bool { return !session.conn.alive() }, 5*time.Second, time.Millisecond)
			require.EqualValues(t, 1, dialer.dials.Load())
		})
	}
}
