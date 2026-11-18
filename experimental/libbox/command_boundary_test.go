package libbox

// Boundary tests for the control channel's decision logic, written from the review findings of
// 2026-11-18. Each one fails against the implementation as it was, and the property it defends is
// stated in the test so a later reader does not have to reconstruct the finding.

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// startControlServerForLease starts a stub command server and points the client at it.
func startControlServerForLease(t *testing.T, implementation *controlStubStartedService) (*controlServer, *CommandClient) {
	t.Helper()
	server := startControlServer(t, 0, implementation)
	useControlServerPort(t, server.port)
	return server, NewCommandClient(nil, nil)
}

// requireEndpointDead waits until the endpoint is definitively unreachable, which is the state a probe
// must see before any invalidation is allowed to happen.
func requireEndpointDead(t *testing.T, endpoint commandEndpoint) {
	t.Helper()
	require.Eventually(t, func() bool {
		listening, definitive := endpoint.probe()
		return definitive && !listening
	}, 5*time.Second, 10*time.Millisecond, "the endpoint never became definitively unreachable")
}

// TestAStaleFailureCannotInvalidateANewerConnection is finding 1 of the review.
//
// The bug: classifyCommandFailure closed "the client's connection" whenever the probe said nothing was
// listening. A failure delivered late - after another goroutine had re-established - therefore cleared
// the NEW connection, even though that connection was healthy and the failing call had used the old
// one. A race detector cannot see it: the locking was correct, the decision was about the wrong object.
//
// The property: an invalidation is scoped to the connection the failed call ran on. A stale lease may
// not close anything, and it may not be reported as "the core is not running" either, because the
// client is in fact connected.
func TestAStaleFailureCannotInvalidateANewerConnection(t *testing.T) {
	implementation := &controlStubStartedService{}
	oldServer, client := startControlServerForLease(t, implementation)

	// Call one establishes connection A and runs on it.
	require.NoError(t, client.SetGroupExpand("proxy", true))
	oldLease := client.currentLeaseForTest()
	require.NotZero(t, oldLease.epoch)

	// The core is replaced: a second server, and the client re-establishes on it. This is the "new
	// connection already exists and works" half of the finding.
	newServer := startControlServer(t, 0, implementation)
	useControlServerPort(t, newServer.port)
	client.closeConnection()
	require.NoError(t, client.SetGroupExpand("proxy", true))
	newLease := client.currentLeaseForTest()
	require.NotEqual(t, oldLease.epoch, newLease.epoch, "a re-established connection must have a new identity")
	require.NotNil(t, client.connectionForTest())

	// The OLD endpoint is now definitively dead, so a probe says so - which is exactly the situation that
	// used to justify the close.
	oldServer.stop()
	requireEndpointDead(t, oldLease.endpoint)

	stale := status.Error(codes.Unavailable, "the old connection is gone")

	// The stale failure must leave the new connection alone, and must not claim the core is down.
	err := client.classifyCommandFailure(stale, oldLease)
	require.False(t, errors.Is(err, ErrCommandServerNotRunning),
		"a failure of an older connection must not be reported as 'the core is not running' while a newer one is live: %v", err)
	require.ErrorIs(t, err, stale, "the original failure must be forwarded unchanged")
	require.NotNil(t, client.connectionForTest(), "the newer connection must survive a stale failure")

	// Control: the SAME failure on the CURRENT lease does invalidate, so the guard above is not simply
	// disabling the feature.
	newServer.stop()
	requireEndpointDead(t, newLease.endpoint)
	err = client.classifyCommandFailure(stale, newLease)
	require.True(t, errors.Is(err, ErrCommandServerNotRunning),
		"the current connection's failure must still be classified: %v", err)
	require.Nil(t, client.connectionForTest(), "the current connection must be dropped")
}

// TestTheLeaseEndpointIsTheOneTheCallUsed pins the second half of that scoping rule: the probe must ask
// about the address the call used, not about whatever the process globals name afterwards.
func TestTheLeaseEndpointIsTheOneTheCallUsed(t *testing.T) {
	implementation := &controlStubStartedService{}
	first, client := startControlServerForLease(t, implementation)
	require.NoError(t, client.SetGroupExpand("proxy", true))
	lease := client.currentLeaseForTest()
	require.NotEmpty(t, lease.endpoint.address)

	// Move the process globals to a different port; the lease must keep describing the first one.
	moved := startControlServer(t, 0, implementation)
	useControlServerPort(t, moved.port)
	require.NotEqual(t, lease.endpoint.address, currentCommandEndpoint().address)
	require.Equal(t, lease.endpoint.address, client.currentLeaseForTest().endpoint.address,
		"a call's lease must not be rewritten by a later endpoint change")

	first.stop()
	moved.stop()
}

// TestAConnectionReplacedBetweenCallsIsNotClosedByAnOlderFailure exercises the epoch
// compare-and-invalidate primitive directly. The check and the close happen under one lock acquisition,
// so a concurrent install cannot slip between them; this test pins both halves of that decision.
func TestAConnectionReplacedBetweenCallsIsNotClosedByAnOlderFailure(t *testing.T) {
	implementation := &controlStubStartedService{}
	server, client := startControlServerForLease(t, implementation)
	require.NoError(t, client.SetGroupExpand("proxy", true))
	staleEpoch := client.currentLeaseForTest().epoch

	server.stop()
	client.closeConnection()
	replacement := startControlServer(t, 0, implementation)
	useControlServerPort(t, replacement.port)
	require.NoError(t, client.SetGroupExpand("proxy", true))
	require.NotNil(t, client.connectionForTest())

	require.False(t, client.invalidateConnection(staleEpoch),
		"invalidating a stale epoch must report that it did nothing")
	require.NotNil(t, client.connectionForTest(), "a stale invalidation must not close the current connection")

	require.True(t, client.invalidateConnection(client.currentLeaseForTest().epoch),
		"the current epoch must be invalidatable")
	require.Nil(t, client.connectionForTest())
	replacement.stop()
}

// TestAFinishedStandaloneCallCannotCloseANewerConnection is the review's second finding about the same
// connection-generation bug, in its cleanup branch.
//
// The cleanup used to close "the client's connection" when a one-shot call returned, whatever connection
// that was by then. The failure path had been fixed with a per-call lease; the cleanup path had not, and
// the same `if c.standalone { c.closeConnection() }` shape existed in every stream and session teardown.
//
// The property: a call's teardown is scoped to the connection that call ran on.
//
// # Why the old call's teardown is invoked directly here
//
// The harmful interleaving needs the older call to be SLOW TO RETURN after its connection died: the
// transport cancellation wakes the RPC at once, but the goroutine still has to run the failure
// classification (a probe with a 150 ms cap) before its deferred cleanup executes. During that window a
// newer call can establish a newer connection. That ordering is a scheduler property, not a code path, so
// a test that waits for it is a flake; invoking the teardown with the old lease is the same defect, made
// deterministic. What the wrapper does with the lease - and that the last one-shot call still closes its
// own connection - is asserted here too.
func TestAFinishedStandaloneCallCannotCloseANewerConnection(t *testing.T) {
	implementation := &controlStubStartedService{
		expandEntered: make(chan int32, 4),
		expandGates:   []chan struct{}{make(chan struct{}), make(chan struct{})},
	}
	server := startControlServer(t, 0, implementation)
	useControlServerPort(t, server.port)
	client := NewStandaloneCommandClient()

	// Call one: it establishes the first connection and finishes, taking its own connection with it.
	firstDone := make(chan error, 1)
	go func() { firstDone <- client.SetGroupExpand("proxy", true) }()
	require.EqualValues(t, 1, <-implementation.expandEntered, "the first call must be in flight")
	firstLease := client.currentLeaseForTest()
	require.NotZero(t, firstLease.epoch)
	close(implementation.expandGates[0])
	require.NoError(t, <-firstDone)
	require.Nil(t, client.connectionForTest(), "a one-shot call closes the connection it used")

	// Call two: a later call establishes a NEW connection and is still in flight.
	secondDone := make(chan error, 1)
	go func() { secondDone <- client.SetGroupExpand("proxy", true) }()
	require.EqualValues(t, 2, <-implementation.expandEntered, "the second call must be in flight")
	require.NotNil(t, client.connectionForTest())
	secondLease := client.currentLeaseForTest()
	require.NotEqual(t, firstLease.epoch, secondLease.epoch, "a re-established connection must have a new identity")

	// The delayed cleanup of call one. It must not touch the connection call two is using.
	client.releaseStandaloneConnection(firstLease)
	require.NotNil(t, client.connectionForTest(),
		"a finished call must not close the connection a newer call is using")

	// The last one-shot call still closes its own connection.
	close(implementation.expandGates[1])
	require.NoError(t, <-secondDone)
	require.Nil(t, client.connectionForTest())
}

// TestProbeCommandServerDoesNotClaimHistory pins finding 3 at the API boundary: the probe answers about
// the present only, and its document carries no claim about earlier requests.
func TestProbeCommandServerDoesNotClaimHistory(t *testing.T) {
	port := freePort(t)
	useControlServerPort(t, port)
	client := NewCommandClient(nil, nil)

	probe := decodeCommandServerProbe(t, client.ProbeCommandServer())
	require.Equal(t, "notRunning", probe.State)
	require.NotContains(t, probe.Detail, "applied")
	require.NotContains(t, probe.Detail, "delivered")
}

// currentLeaseForTest reads the client's current lease under the same lock production uses.
func (c *CommandClient) currentLeaseForTest() commandCallLease {
	c.clientAccess.RLock()
	defer c.clientAccess.RUnlock()
	return commandCallLease{epoch: c.connectionEpoch, endpoint: c.serverEndpoint}
}

// connectionForTest reports the currently installed connection, if any.
func (c *CommandClient) connectionForTest() *grpc.ClientConn {
	c.clientAccess.RLock()
	defer c.clientAccess.RUnlock()
	return c.grpcConn
}
