//go:build with_quic

package v2rayquic

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Idle trim and a network change are different actions on the same object, and the window's
// accounting distinguishes them:
//
//	idle trim     the generation has nothing left on it, so closing it costs no flow
//	network change with no flow   nothing to preserve either, but it must not need a candidate:
//	                              moving a socket nobody uses is pure cost
//	network change with a flow    migration, never a silent close (covered by
//	                              client_migration_test.go on real sockets)
//
// The failure this file guards against is a "reset everything on any cleanup trigger" shape: an
// idle trim that closes a generation carrying a flow would drop a live stream with no network
// event behind it at all.

// streamCountOf sets up the streams a fake connection reports, without a real QUIC stream.
func setStreams(conn *clientConnection, streams int) {
	conn.access.Lock()
	conn.streams = streams
	conn.access.Unlock()
}

func TestIdleTrimClosesOnlyAGenerationWithNothingOnIt(t *testing.T) {
	dial := newFakeDial()
	first, firstCloses := fakeConnection()
	dial.setResult(first, nil)
	client := newTestClient(t, dial)
	// The fake constructor returns as soon as the gate is open, so every demand below resolves
	// without a real handshake.
	close(dial.release)

	result := <-startOffer(client, context.Background())
	require.NoError(t, result.err)
	require.Equal(t, 1, dial.callCount())

	// The generation carries a flow: an idle trim must leave it alone.
	setStreams(first, 1)
	client.SetKeepIdleConnections(false)
	client.CloseIdleConnections()
	require.Zero(t, firstCloses.Load(), "an idle trim must not close a generation carrying a flow")
	require.Same(t, first, client.conn.Load(), "the trimmed generation must stay published while it carries a flow")

	// The flow finishes: the same trim now retires it, exactly once.
	setStreams(first, 0)
	client.CloseIdleConnections()
	require.Equal(t, int32(1), firstCloses.Load(), "an idle generation is closed once by the trim")

	// And the next demand dials again: the trim did not end the client.
	second, secondCloses := fakeConnection()
	dial.setResult(second, nil)
	result = <-startOffer(client, context.Background())
	require.NoError(t, result.err)
	require.Same(t, second, result.conn)
	require.Equal(t, 2, dial.callCount())
	require.Zero(t, secondCloses.Load())
}

func TestANetworkChangeRetiresAnIdleGenerationWithoutACandidate(t *testing.T) {
	dial := newFakeDial()
	first, firstCloses := fakeConnection()
	dial.setResult(first, nil)
	client := newTestClient(t, dial)
	// The fake constructor returns as soon as the gate is open, so every demand below resolves
	// without a real handshake.
	close(dial.release)

	result := <-startOffer(client, context.Background())
	require.NoError(t, result.err)
	require.Equal(t, 1, dial.callCount())

	// No flow on the generation: the network change retires it without opening a candidate socket
	// (there is nothing to move, and a candidate would be a wasted dial on the new network).
	require.NoError(t, client.Reset())
	require.Equal(t, int32(1), firstCloses.Load(), "the idle generation is retired once by the change")
	require.Equal(t, 1, dial.callCount(), "no candidate may be dialed for a generation with nothing on it")
	require.Nil(t, client.conn.Load(), "the retired generation must not stay published")

	// The next demand is a fresh generation.
	second, _ := fakeConnection()
	dial.setResult(second, nil)
	result = <-startOffer(client, context.Background())
	require.NoError(t, result.err)
	require.Same(t, second, result.conn)
	require.Equal(t, 2, dial.callCount())
}
