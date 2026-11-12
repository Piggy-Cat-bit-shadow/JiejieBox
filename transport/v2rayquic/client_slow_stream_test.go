//go:build with_quic

package v2rayquic

// Q16: one slow consumer must not stop the shared connection.
//
// The transport multiplexes every stream onto ONE QUIC connection, so the audit's "slow consumer /
// multi-stream saturation" question is exactly this: when one stream's peer keeps writing and the
// local consumer does not read, does another stream still make progress, and can the stalled one
// still be closed? The answer was NOT assumed - it was measured first (a 512 KiB echo left unread,
// then a second stream's record acknowledged immediately), and this test pins the observation.
//
// The measurement also showed what the queue accounting means: the ledger counts CONNECTIONS, not
// streams, so a connection carrying a stalled stream is Active until that stream ends, and returns
// to Idle when it does. That is asserted, because a ledger that said "released" while a stream was
// still stalled would be the misreading the ledger exists to prevent.

import (
	"io"
	"testing"
	"time"

	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// TestAStalledStreamDoesNotStopAnotherStreamOnTheSharedConnection is the data-plane property.
func TestAStalledStreamDoesNotStopAnotherStreamOnTheSharedConnection(t *testing.T) {
	server := startMigrationServer(t, nil)
	client := newMigratingClient(t, server, &N.DefaultDialer{})
	t.Cleanup(func() { _ = client.Close() })

	stalled := openStream(t, client)
	// A record far larger than any stream window: its ECHO is what the server writes back, and
	// this test deliberately never reads it. The write itself is accepted because the server keeps
	// reading; the pressure is on the client's receive side.
	big := make([]byte, 512<<10)
	_, err := stalled.Write(encodeMigrationRecord(0, big))
	require.NoError(t, err)

	// Give the echo time to fill the stalled stream's receive window, which is the state the audit
	// asks about: a consumer that is not consuming.
	time.Sleep(150 * time.Millisecond)

	second := openStream(t, client)
	second.SetDeadline(time.Now().Add(5 * time.Second))
	started := time.Now()
	_, err = second.Write(encodeMigrationRecord(0, []byte("second")))
	require.NoError(t, err)
	header := make([]byte, 16)
	_, err = io.ReadFull(second, header)
	require.NoError(t, err,
		"a second stream's record was not acknowledged while the first stream's consumer was stalled: "+
			"one slow consumer stopped the shared connection")
	require.Less(t, time.Since(started), 5*time.Second)

	// The connection is ACTIVE while either stream is open, and the ledger says so.
	require.Equal(t, uint64(1), client.ResidencyLedger().Active,
		"the connection carrying a stalled stream must be counted as active, not idle or released")

	// The stalled stream can still be closed, promptly.
	closeStarted := time.Now()
	require.NoError(t, stalled.Close())
	require.Less(t, time.Since(closeStarted), time.Second, "closing a stalled stream blocked")

	require.NoError(t, second.Close())
	// Once both streams are gone the connection is pooled again: idle, not terminated. The pool
	// keeps it for the next demand, which is the state the ledger has to report.
	require.Eventually(t, func() bool {
		ledger := client.ResidencyLedger()
		return ledger.Idle == 1 && ledger.Active == 0
	}, 2*time.Second, time.Millisecond,
		"the connection did not return to the idle state after its streams ended")
}
