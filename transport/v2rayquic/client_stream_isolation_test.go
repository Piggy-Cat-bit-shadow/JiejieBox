//go:build with_quic

package v2rayquic

// Window B's "cancel and stream termination" group, the property that keeps multiplexing usable: a
// stream whose caller gives up must fail ALONE. The transport runs every flow of a connection over one
// QUIC session, so a cancel that retired the session - or that made the pool dial a replacement - would
// turn one abandoned request into a reconnect for every other flow on it.
//
// The shape under test is the realistic one: a caller closes a stream with a PARTIAL record on it (it
// stopped mid-frame), while a sibling stream keeps sending valid records on the same connection. The
// server's per-stream reader treats the incomplete frame as the end of that stream only
// (path_migration_test.go's readStream returns without touching the connection), so what the assertion
// observes is the CLIENT's decision: does it retire the pooled generation, and does the pool hand the
// next flow the same connection?

import (
	"testing"

	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

func TestACancelledStreamDoesNotPoisonTheSharedConnection(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &migratingDialer{base: &N.DefaultDialer{}}
	client := newGenerationClient(t, server, dialer, nil)
	t.Cleanup(func() { _ = client.Close() })

	first := openStream(t, client)
	sendRecord(t, first, 0, "before-the-cancel")
	pooled := client.conn.Load()
	require.NotNil(t, pooled, "the first stream must have published a connection")

	cancelled := openStream(t, client)
	// A partial frame, then the caller gives up. The server's reader for THIS stream ends here.
	_, err := cancelled.Write([]byte{0x00, 0x00, 0x00, 0x40})
	require.NoError(t, err)
	require.NoError(t, cancelled.Close())

	// The sibling keeps carrying valid records on the same connection.
	sendRecord(t, first, 1, "after-the-cancel")

	require.Same(t, pooled, client.conn.Load(),
		"cancelling one stream retired the shared connection: every other flow on it lost its session")
	require.Equal(t, 1, dialer.count(),
		"one cancelled stream made the pool dial a second connection")

	// And the pool still lends the same connection to new work.
	third := openStream(t, client)
	sendRecord(t, third, 0, "third")
	require.Same(t, pooled, client.conn.Load())
	require.Equal(t, 1, dialer.count())
	require.EqualValues(t, 1, server.handshakes.Load(),
		"the server saw a second QUIC handshake after one stream was cancelled")
}
