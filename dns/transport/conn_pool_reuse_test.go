package transport

// O03 — reuse attribution for the DNS transports whose pool is NOT the query multiplexer.
//
// The multiplexer publishes a reused connection's chain because it owns the connection. The serial
// and shared pools (TCP, TLS, QUIC) own theirs too, but they live in ConnPool: net-based transports
// hand it a dial function and it decides whether to dial or to reuse. That decision is exactly the
// fact the event needs, so the pool records the chain at establishment and publishes it when it hands
// the connection over a second time.
//
// These tests drive the REAL pool: they do not fake the reuse decision, they observe it.

import (
	"context"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// pooledTestConn is a connexion the pool can track: comparable and alive.
type pooledTestConn struct {
	net.Conn
	identifier int
}

func (c *pooledTestConn) Close() error { return nil }

// newPoolForReuse builds an ordered pool that closes its connections through the same interface.
func newPoolForReuse(t *testing.T) (*ConnPool[*pooledTestConn], *int) {
	t.Helper()
	closed := new(int)
	pool := NewConnPool(ConnPoolOptions[*pooledTestConn]{
		Mode:    ConnPoolOrdered,
		MaxIdle: 1,
		IsAlive: func(*pooledTestConn) bool { return true },
		Close:   func(*pooledTestConn, error) { *closed++ },
	})
	t.Cleanup(func() { _ = pool.Close() })
	return pool, closed
}

// TestAReusedPooledConnectionPublishesItsDialPath is the fact itself.
func TestAReusedPooledConnectionPublishesItsDialPath(t *testing.T) {
	pool, _ := newPoolForReuse(t)
	dialCount := 0
	dial := func(dialCtx context.Context) (*pooledTestConn, error) {
		dialCount++
		adapter.DialPathFromContext(dialCtx).RecordStep("", "node-a", 7, true)
		return &pooledTestConn{identifier: 1}, nil
	}

	first := adapter.NewDialPath()
	first.BeginDial()
	firstConn, created, err := pool.Acquire(adapter.WithDialPath(context.Background(), first), dial)
	require.NoError(t, err)
	require.True(t, created)
	pool.Release(firstConn, true)

	second := adapter.NewDialPath()
	second.BeginDial()
	_, created, err = pool.Acquire(adapter.WithDialPath(context.Background(), second), dial)
	require.NoError(t, err)
	require.False(t, created, "the pool must hand back the socket it kept, or this test proves nothing")
	require.Equal(t, 1, dialCount)

	result := second.Result()
	require.True(t, result.Replayed, "a reuse is not a dial")
	require.Len(t, result.Steps, 1, "the reused connection's chain is published to this exchange")
	require.Equal(t, "node-a", result.Steps[0].Tag)
	require.Equal(t, adapter.DialPathReasonNone, result.Reason())
}

// TestAConnectionWithoutAChainStaysUntracked is the negative control: a socket established while
// nothing was recording must not be replayed as an empty path - the event says it is untracked.
func TestAConnectionWithoutAChainStaysUntracked(t *testing.T) {
	pool, _ := newPoolForReuse(t)
	dial := func(context.Context) (*pooledTestConn, error) {
		return &pooledTestConn{identifier: 2}, nil
	}
	unobserved, _, err := pool.Acquire(context.Background(), dial)
	require.NoError(t, err)
	pool.Release(unobserved, true)

	observed := adapter.NewDialPath()
	observed.BeginDial()
	_, created, err := pool.Acquire(adapter.WithDialPath(context.Background(), observed), dial)
	require.NoError(t, err)
	require.False(t, created)

	result := observed.Result()
	require.True(t, result.Replayed, "the exchange WAS served by a reuse, even though its chain is unknown")
	require.Empty(t, result.Steps)
	require.Equal(t, adapter.DialPathReasonReplayUnknown, result.Reason(),
		"the socket's chain was never recorded, and the reason vocabulary says so")
}

// TestAnEvictedConnectionForgetsItsChain keeps the association bounded: a connection that leaves the
// pool must not leave its entry behind.
func TestAnEvictedConnectionForgetsItsChain(t *testing.T) {
	pool, closed := newPoolForReuse(t)
	dialCount := 0
	dial := func(dialCtx context.Context) (*pooledTestConn, error) {
		dialCount++
		adapter.DialPathFromContext(dialCtx).RecordStep("", "node-a", 1, true)
		return &pooledTestConn{identifier: dialCount}, nil
	}

	first := adapter.NewDialPath()
	first.BeginDial()
	firstConn, _, err := pool.Acquire(adapter.WithDialPath(context.Background(), first), dial)
	require.NoError(t, err)
	pool.Release(firstConn, true)

	// Drop the idle socket: the attribution must go with it.
	pool.CloseIdle()
	require.Equal(t, 1, *closed)

	pool.access.Lock()
	retained := len(pool.dialPaths)
	pool.access.Unlock()
	require.Zero(t, retained, "an attribution must not outlive the connection it describes")

	second := adapter.NewDialPath()
	second.BeginDial()
	_, created, err := pool.Acquire(adapter.WithDialPath(context.Background(), second), dial)
	require.NoError(t, err)
	require.True(t, created, "nothing was left to reuse")
	require.Equal(t, 2, dialCount)
	require.False(t, second.Result().Replayed, "a fresh dial is not a replay")
}

// TestAFreshConnectionLeavesNoAttribution pins the AcquireFresh boundary: a caller-owned connection
// never enters the pool, so none of the pool's removal paths (Release, idle eviction, Reset, Close)
// will ever run for it. Recording its chain in dialPaths would therefore retain the dead connection
// and its steps for the life of the transport, one entry per stale-connection retry.
func TestAFreshConnectionLeavesNoAttribution(t *testing.T) {
	pool, closed := newPoolForReuse(t)
	dial := func(dialCtx context.Context) (*pooledTestConn, error) {
		adapter.DialPathFromContext(dialCtx).RecordStep("", "node-fresh", 1, true)
		return &pooledTestConn{identifier: 9}, nil
	}

	for index := 0; index < 32; index++ {
		path := adapter.NewDialPath()
		path.BeginDial()
		conn, err := pool.AcquireFresh(adapter.WithDialPath(context.Background(), path), dial)
		require.NoError(t, err)
		require.NotNil(t, conn)
		require.Len(t, path.Result().Steps, 1, "the caller's own recorder still receives the dial's steps")
		require.NoError(t, conn.Close())
	}

	pool.access.Lock()
	retained := len(pool.dialPaths)
	pool.access.Unlock()
	require.Zero(t, retained, "a caller-owned fresh connection must not leave a pool attribution")
	require.Zero(t, *closed, "the pool must not close a connection AcquireFresh handed to its caller")
}

// keepM imports used if the file grows; the dialer type below is the one these tests need.
var _ = M.Socksaddr{}
