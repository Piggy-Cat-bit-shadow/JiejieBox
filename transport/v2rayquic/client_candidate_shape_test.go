//go:build with_quic

package v2rayquic

// The migration candidate's socket SHAPE, pinned as a regression (audit Q30).
//
// # Why this test exists on every platform
//
// The macos job failed the two path-migration tests with a bare 10 s `context deadline exceeded`
// from `path.Probe`, while Windows passed. The cause is not the probe budget and not the library's
// path manager: a candidate used to be opened with `DialContext("udp", ...)` - a CONNECTED socket -
// and adapted to `net.PacketConn` by a wrapper that ignored the destination argument. On a platform
// where quic-go can reach the socket's file descriptor it sends every path packet with an EXPLICIT
// destination sockaddr, which a connected socket rejects; the library discards that write error, so
// the only symptom is a silent validation timeout. Windows never reaches that branch, which is
// exactly why the failure looked macOS-only.
//
// The fix is the shape: a candidate is an UNCONNECTED `ListenPacket` socket, which is what the
// library's destination-sockaddr write is designed for (and it keeps GSO/ECN/download tuning
// available on Darwin instead of falling back to the unoptimised transport).
//
// The assertion is written against the DIALER, not against the library: a candidate that arrives
// through `DialContext` is refused with an explicit error, so any future edit that goes back to
// "just dial a second socket" fails this test on Windows and Linux too - the two platforms where
// the original defect was invisible.

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// listenOnlyCandidateDialer allows exactly one connected dial (the handshake) and requires every
// later socket to come from ListenPacket.
type listenOnlyCandidateDialer struct {
	dialer N.Dialer

	access  sync.Mutex
	dials   int
	listens int
}

func (d *listenOnlyCandidateDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	d.access.Lock()
	d.dials++
	attempt := d.dials
	d.access.Unlock()
	if attempt > 1 {
		return nil, errors.New("Q30: a migration candidate must be an UNCONNECTED socket, not a connected dial")
	}
	return d.dialer.DialContext(ctx, network, destination)
}

func (d *listenOnlyCandidateDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	d.access.Lock()
	d.listens++
	d.access.Unlock()
	return d.dialer.ListenPacket(ctx, destination)
}

func (d *listenOnlyCandidateDialer) counts() (dials int, listens int) {
	d.access.Lock()
	defer d.access.Unlock()
	return d.dials, d.listens
}

// TestTheMigrationCandidateIsAnUnconnectedSocket drives one real network change through the
// production Reset and requires the candidate to arrive as a packet socket.
func TestTheMigrationCandidateIsAnUnconnectedSocket(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &listenOnlyCandidateDialer{dialer: &N.DefaultDialer{}}
	client := newMigratingClient(t, server, dialer)
	t.Cleanup(func() { _ = client.Close() })

	stream := openStream(t, client)
	sendRecord(t, stream, 0, "before-the-change")

	require.NoError(t, client.Reset())

	// The migration happened, the flow survived it, and it happened on a socket that was NOT a
	// connected dial: a connected candidate would have been refused above and the attempt would
	// have ended as migrationUnchanged (or the connection would still be on the old path).
	sendRecord(t, stream, 1, "after-the-change")
	require.Equal(t, int32(1), server.handshakes.Load(),
		"the change must move the connection, not establish a second one")

	dials, listens := dialer.counts()
	require.Equal(t, 1, dials, "only the handshake may be a connected dial")
	require.Equal(t, 1, listens, "the candidate must come from ListenPacket")
}
