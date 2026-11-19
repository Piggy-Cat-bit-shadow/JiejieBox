//go:build with_quic

package v2rayquic

// Window B's QUIC send-queue group: the "full -> writable" edge. A stream whose writes are blocked by
// the peer's advertised window must not take the connection down with it: when the peer starts reading,
// other work on the same connection must proceed.
//
// The test does NOT assert how many bytes fit before blocking - that is the peer's advertised window and
// not a contract of this transport, so asserting it would turn every upstream window change into a false
// failure. It asserts the two things that are contracts:
//
//	1. the write really was blocked (otherwise there is nothing to recover from), and
//	2. after the peer reads, a NEW stream on the same connection carries data.
//
// A stream whose write deadline fired is cancelled by QUIC and cannot be resumed; that is protocol
// semantics, not a transport defect, which is why the recovery assertion uses a fresh stream. The server
// holds its readers before their first read (`migrationServer.readsBlocked`), which is what fills the
// client's window; nil is the default, so no other test in the package changes behaviour.

import (
	"testing"
	"time"

	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

func TestABlockedSendQueueDoesNotPoisonTheConnection(t *testing.T) {
	server := startMigrationServer(t, nil)
	blocked := make(chan struct{})
	server.readsBlocked = blocked
	client := newGenerationClient(t, server, &migratingDialer{base: &N.DefaultDialer{}}, nil)
	t.Cleanup(func() { _ = client.Close() })

	first := openStream(t, client)
	// The first write is also what drives the handshake to completion: opening a stream does not by
	// itself give the server anything to accept.
	require.NoError(t, first.SetWriteDeadline(time.Now().Add(2*time.Second)))
	_, err := first.Write([]byte{0})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return server.streamCount.Load() >= 1 },
		2*time.Second, 5*time.Millisecond, "the server never accepted the stream")

	// Fill the peer's receive window. The byte count is deliberately not asserted.
	payload := make([]byte, 16*1024)
	var written int
	wasBlocked := false
	budget := time.Now().Add(10 * time.Second)
	for time.Now().Before(budget) {
		require.NoError(t, first.SetWriteDeadline(time.Now().Add(500*time.Millisecond)))
		count, writeErr := first.Write(payload)
		written += count
		if writeErr != nil {
			wasBlocked = true
			break
		}
	}
	if !wasBlocked {
		t.Skipf("the peer's window did not fill after %d bytes, so there is no full send queue to "+
			"recover from", written)
	}
	require.Positive(t, written, "a write deadline fired before any byte was accepted")
	t.Logf("the send queue filled after %d bytes; the peer then read and a new stream wrote", written)

	// The peer starts reading, which opens the window again.
	close(blocked)

	// A NEW stream on the same connection must carry data now.
	second := openStream(t, client)
	require.NoError(t, second.SetWriteDeadline(time.Now().Add(3*time.Second)))
	_, err = second.Write([]byte("after-the-window-opened"))
	require.NoError(t, err, "a fresh stream could not write after the peer opened the window")

	require.EqualValues(t, 1, server.handshakes.Load(),
		"the transport reconnected instead of recovering the connection it already had")
}
