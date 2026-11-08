//go:build with_quic

package v2rayquic

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The shared offer is this transport's R05 task: one creation, many waiters, a clear owner for a
// late success.
//
// # The contract pinned here
//
//   - concurrent demand runs ONE dial; every waiter receives the same connection;
//   - a waiter's cancellation/deadline ends only that waiter - the shared dial is owned by the
//     client lifetime and is neither cancelled nor failed by a request leaving;
//   - a success that lands after the last waiter left is accepted by its owner (the client) and
//     remains reusable;
//   - Close is the discarding owner: it cancels the pending offer and a late success is closed
//     exactly once instead of being published;
//   - a failed offer publishes its error to the waiters and leaves no connection behind, so the next
//     demand retries.
//
// The fake dial replaces only the QUIC handshake; the coordination under test is the production
// one.

type offerResult struct {
	conn *clientConnection
	err  error
}

// fakeDial is a controllable connection constructor.
type fakeDial struct {
	access    sync.Mutex
	calls     int
	dialCtx   []context.Context
	started   chan struct{}
	release   chan struct{}
	result    *clientConnection
	err       error
	ignoreCtx bool
	// results, when set, gives each call its own answer by call index (the last
	// entry repeats). The single result/err pair above stays the default the
	// existing tests use.
	results []offerResult
	// probe runs inside dial, after the call is recorded and before it waits: it
	// is how a test asserts what the constructor must not be holding.
	probe func()
}

func newFakeDial() *fakeDial {
	return &fakeDial{started: make(chan struct{}), release: make(chan struct{})}
}

func (d *fakeDial) dial(ctx context.Context) (*clientConnection, error) {
	d.access.Lock()
	d.calls++
	index := d.calls - 1
	d.dialCtx = append(d.dialCtx, ctx)
	if d.calls == 1 {
		close(d.started)
	}
	d.access.Unlock()

	if d.probe != nil {
		d.probe()
	}

	if d.ignoreCtx {
		<-d.release
	} else {
		select {
		case <-d.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	d.access.Lock()
	defer d.access.Unlock()
	if index < len(d.results) {
		return d.results[index].conn, d.results[index].err
	}
	return d.result, d.err
}

func (d *fakeDial) callCount() int {
	d.access.Lock()
	defer d.access.Unlock()
	return d.calls
}

func (d *fakeDial) lastContext() context.Context {
	d.access.Lock()
	defer d.access.Unlock()
	if len(d.dialCtx) == 0 {
		return nil
	}
	return d.dialCtx[len(d.dialCtx)-1]
}

func (d *fakeDial) setResult(conn *clientConnection, err error) {
	d.access.Lock()
	defer d.access.Unlock()
	d.result = conn
	d.err = err
}

// fakeConnection is a connection whose lifetime and close action are controlled by the test.
func fakeConnection() (*clientConnection, *atomic.Int32) {
	closes := &atomic.Int32{}
	return &clientConnection{
		ctx:       context.Background(),
		closeIdle: &atomic.Bool{},
		closeFn:   func() { closes.Add(1) },
	}, closes
}

func newTestClient(t *testing.T, dial *fakeDial) *Client {
	t.Helper()
	ctx, cancel := context.WithCancelCause(context.Background())
	client := &Client{
		ctx:      ctx,
		cancel:   cancel,
		dialConn: dial.dial,
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func startOffer(client *Client, ctx context.Context) <-chan offerResult {
	result := make(chan offerResult, 1)
	go func() {
		conn, err := client.offer(ctx)
		result <- offerResult{conn: conn, err: err}
	}()
	return result
}

func waitPublished(t *testing.T, client *Client, conn *clientConnection) {
	t.Helper()
	require.Eventually(t, func() bool {
		return client.conn.Load() == conn
	}, 2*time.Second, time.Millisecond, "the late success must be accepted by the client")
}

func TestOneOfferServesConcurrentWaiters(t *testing.T) {
	dial := newFakeDial()
	conn, closes := fakeConnection()
	dial.setResult(conn, nil)
	client := newTestClient(t, dial)

	first := startOffer(client, context.Background())
	<-dial.started
	second := startOffer(client, context.Background())
	close(dial.release)

	firstResult := <-first
	secondResult := <-second
	require.NoError(t, firstResult.err)
	require.NoError(t, secondResult.err)
	require.Same(t, conn, firstResult.conn)
	require.Same(t, conn, secondResult.conn)
	require.Equal(t, 1, dial.callCount(), "concurrent demand must share one dial, not start two")
	require.Zero(t, closes.Load())
	require.Same(t, conn, client.conn.Load())
}

func TestAWaiterCancellationDoesNotTearDownTheSharedDial(t *testing.T) {
	dial := newFakeDial()
	conn, closes := fakeConnection()
	dial.setResult(conn, nil)
	client := newTestClient(t, dial)

	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	cancelled := startOffer(client, waiterCtx)
	<-dial.started
	remaining := startOffer(client, context.Background())

	cancelWaiter()
	result := <-cancelled
	require.ErrorIs(t, result.err, context.Canceled,
		"the departing waiter must report its own cancellation, not the shared result")

	require.Nil(t, dial.lastContext().Err(),
		"the shared dial must run under the client lifetime, not the departing request's context")
	require.Equal(t, 1, dial.callCount())
	require.Nil(t, client.conn.Load())

	close(dial.release)
	remainingResult := <-remaining
	require.NoError(t, remainingResult.err)
	require.Same(t, conn, remainingResult.conn, "the waiter that stayed must receive the shared connection")
	require.Zero(t, closes.Load(), "the connection must not be closed because one waiter left")
}

func TestAWaiterDeadlineIsItsOwnAndTheLateSuccessIsAccepted(t *testing.T) {
	dial := newFakeDial()
	conn, closes := fakeConnection()
	dial.setResult(conn, nil)
	client := newTestClient(t, dial)

	deadlineCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := startOffer(client, deadlineCtx)
	<-dial.started
	early := <-result
	require.ErrorIs(t, early.err, context.DeadlineExceeded)
	require.Nil(t, client.conn.Load())

	close(dial.release)
	waitPublished(t, client, conn)
	require.Zero(t, closes.Load(), "an accepted late success must stay open for reuse")
}

func TestCloseDiscardsALateOfferExactlyOnce(t *testing.T) {
	dial := newFakeDial()
	dial.ignoreCtx = true
	lateConn, closes := fakeConnection()
	dial.setResult(lateConn, nil)
	client := newTestClient(t, dial)

	waiter := startOffer(client, context.Background())
	<-dial.started

	closed := make(chan error, 1)
	go func() { closed <- client.Close() }()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Close waited for the in-flight dial: it must only take ownership of the pending offer")
	}
	require.Error(t, dial.lastContext().Err(), "Close must cancel the pending offer's dial context")
	require.Nil(t, client.conn.Load())

	close(dial.release)
	result := <-waiter
	require.ErrorIs(t, result.err, net.ErrClosed,
		"a waiter still attached when the client closed must learn the offer was discarded")

	require.Eventually(t, func() bool {
		return closes.Load() == 1
	}, 2*time.Second, time.Millisecond, "the late connection must be closed exactly once")
	require.Nil(t, client.conn.Load(), "a discarded late success must not be published")
}

func TestAFailedOfferPublishesTheErrorAndTheNextDemandRetries(t *testing.T) {
	dial := newFakeDial()
	boom := errors.New("dial boom")
	dial.setResult(nil, boom)
	client := newTestClient(t, dial)

	result := startOffer(client, context.Background())
	<-dial.started
	close(dial.release)
	failed := <-result
	require.ErrorIs(t, failed.err, boom)
	require.Nil(t, failed.conn)
	require.Nil(t, client.conn.Load(), "a failed offer must leave no connection behind")

	retryConn, _ := fakeConnection()
	dial.setResult(retryConn, nil)
	retried := startOffer(client, context.Background())
	retriedResult := <-retried
	require.NoError(t, retriedResult.err)
	require.Same(t, retryConn, retriedResult.conn)
	require.Equal(t, 2, dial.callCount())
}

func TestALateSuccessAfterEveryWaiterLeftIsAcceptedAndReusable(t *testing.T) {
	dial := newFakeDial()
	conn, _ := fakeConnection()
	dial.setResult(conn, nil)
	client := newTestClient(t, dial)

	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	onlyWaiter := startOffer(client, waiterCtx)
	<-dial.started
	cancelWaiter()
	require.ErrorIs(t, (<-onlyWaiter).err, context.Canceled)
	require.Nil(t, dial.lastContext().Err(),
		"no waiter left, but the owner still holds a dial that was never cancelled")

	close(dial.release)
	waitPublished(t, client, conn)

	reused := startOffer(client, context.Background())
	reusedResult := <-reused
	require.Same(t, conn, reusedResult.conn)
	require.Equal(t, 1, dial.callCount(), "an accepted late success must be reused, not re-dialled")
}

// TestKeepSessionIsPerFlowNotPerUnderlay is the R06 half of the shared connection: the session
// metadata a flow carries must not be inherited by the shared underlay or by another flow.
//
// The pool's idle policy is "close the shared connection when the last stream leaves". One flow
// closing with keep_session must not apply that verdict to a connection another flow still owns, and
// the ordinary last-flow close must still release it exactly once.
func TestKeepSessionIsPerFlowNotPerUnderlay(t *testing.T) {
	conn, closes := fakeConnection()
	conn.closeIdle.Store(true)

	require.True(t, conn.acquireStream(), "flow A must acquire the shared connection")
	require.True(t, conn.acquireStream(), "flow B must acquire the shared connection")

	conn.releaseStream(true) // flow A: keep_session for this close only
	require.Zero(t, closes.Load(),
		"one flow's keep_session must not close the shared underlay for the flow still on it")

	conn.releaseStream(false) // flow B: ordinary close, last stream, idle policy applies
	require.Equal(t, int32(1), closes.Load(), "the last stream's ordinary close must release the underlay once")
	require.False(t, conn.acquireStream(),
		"a closed shared connection must refuse new streams instead of accepting one")
}
