//go:build with_quic

package v2rayquic

// Production-entry tests for the network-change migration.
//
// The acceptance test in path_migration_test.go proves the capability on the pinned library. This
// file drives the same thing through what InterfaceUpdated actually calls: Client.Reset with the
// production dialer (a connected UDP socket per network), the production TLS config and the real
// QUIC stream wrapper. What is observed here is what a user would feel:
//
//   - an open flow keeps carrying acknowledged bytes across the change, on the SAME connection
//     and the same stream (the endpoint counts one handshake and one stream);
//   - the socket of the old path is physically closed, exactly once, by its owner;
//   - a demand after the change reuses the moved connection instead of dialing;
//   - a candidate that cannot be validated leaves a healthy connection untouched, and once the
//     old path is really gone the next demand gets a NEW connection that carries valid bytes;
//   - repeated changes keep exactly one extra socket in flight, close each retired socket once,
//     and keep the stream continuous;
//   - Close during a migration releases the candidate and publishes nothing.

import (
	"context"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

// recordingConn records one socket the client opened, whichever shape it has: the handshake path is
// a CONNECTED net.Conn, a migration candidate is an UNCONNECTED net.PacketConn. One type for both
// keeps the index ordering and the close count observable across the two, which is what lets the
// assertions below say "socket 0 is the handshake, socket 1 is the candidate" without caring which
// API produced them.
//
// # Why the shape is asserted and not just recorded
//
// The candidate shape is load-bearing: a connected socket handed to the library's path transport
// sends with an explicit destination sockaddr, which Darwin rejects, and the write error is
// discarded inside the library - the audit's Q30 (a silent 10 s path validation timeout on macOS
// only). This type delegates ReadFrom/WriteTo, so `dialer.socket(i)` can be asked whether the
// candidate is really unconnected (see TestTheMigrationCandidateIsAnUnconnectedSocket).
type recordingConn struct {
	net.Conn
	packet net.PacketConn
	closes atomic.Int32
}

func (c *recordingConn) Close() error {
	c.closes.Add(1)
	if c.Conn != nil {
		return c.Conn.Close()
	}
	return c.packet.Close()
}

func (c *recordingConn) ReadFrom(buffer []byte) (int, net.Addr, error) {
	return c.packet.ReadFrom(buffer)
}

func (c *recordingConn) WriteTo(buffer []byte, destination net.Addr) (int, error) {
	return c.packet.WriteTo(buffer, destination)
}

func (c *recordingConn) LocalAddr() net.Addr {
	if c.Conn != nil {
		return c.Conn.LocalAddr()
	}
	return c.packet.LocalAddr()
}

func (c *recordingConn) SetDeadline(deadline time.Time) error {
	if c.Conn != nil {
		return c.Conn.SetDeadline(deadline)
	}
	return nil
}

func (c *recordingConn) SetReadDeadline(deadline time.Time) error {
	if c.Conn != nil {
		return c.Conn.SetReadDeadline(deadline)
	}
	return nil
}

func (c *recordingConn) SetWriteDeadline(deadline time.Time) error {
	if c.Conn != nil {
		return c.Conn.SetWriteDeadline(deadline)
	}
	return nil
}

// silentPacketConn accepts writes and never sends them: a candidate on such a socket can never be
// validated, which is what a dead path looks like to the probe.
type silentPacketConn struct {
	net.PacketConn
}

func (c *silentPacketConn) WriteTo(payload []byte, _ net.Addr) (int, error) { return len(payload), nil }

// migratingDialer records every socket handed to the client (index 0 is the handshake socket,
// later ones are migration candidates) and can sabotage candidates to force the fallback.
type migratingDialer struct {
	base     N.Dialer
	access   sync.Mutex
	sockets  []*recordingConn
	sabotage bool
}

func (d *migratingDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	conn, err := d.base.DialContext(ctx, network, destination)
	if err != nil {
		return nil, err
	}
	recording := &recordingConn{Conn: conn}
	d.access.Lock()
	d.sockets = append(d.sockets, recording)
	d.access.Unlock()
	return recording, nil
}

// ListenPacket is the candidate path's API after Q30: an unconnected socket.
func (d *migratingDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	packetConn, err := d.base.ListenPacket(ctx, destination)
	if err != nil {
		return nil, err
	}
	d.access.Lock()
	index := len(d.sockets)
	sabotage := d.sabotage && index > 0
	d.access.Unlock()
	var inner net.PacketConn = packetConn
	if sabotage {
		inner = &silentPacketConn{PacketConn: packetConn}
	}
	recording := &recordingConn{packet: inner}
	d.access.Lock()
	d.sockets = append(d.sockets, recording)
	d.access.Unlock()
	return recording, nil
}

func (d *migratingDialer) socket(index int) *recordingConn {
	d.access.Lock()
	defer d.access.Unlock()
	if index >= len(d.sockets) {
		return nil
	}
	return d.sockets[index]
}

func (d *migratingDialer) count() int {
	d.access.Lock()
	defer d.access.Unlock()
	return len(d.sockets)
}

func (d *migratingDialer) setSabotage(sabotage bool) {
	d.access.Lock()
	defer d.access.Unlock()
	d.sabotage = sabotage
}

// newMigratingClient builds the production client over the real dialer.
func newMigratingClient(t *testing.T, server *migrationServer, dialer N.Dialer) *Client {
	t.Helper()
	logger := log.NewNOPFactory().NewLogger("v2rayquic-migration")
	tlsConfig, err := tls.NewClient(context.Background(), logger, "localhost", option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: "localhost",
		Insecure:   true,
	})
	require.NoError(t, err)
	transport, err := NewClient(context.Background(), dialer, M.SocksaddrFromNet(server.addr), option.V2RayQUICOptions{}, tlsConfig)
	require.NoError(t, err)
	client, isClient := transport.(*Client)
	require.True(t, isClient, "NewClient must build the package client, got %T", transport)
	return client
}

// openStream performs one production dial and returns the stream wrapper.
func openStream(t *testing.T, client *Client) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), migrationSettle)
	defer cancel()
	stream, err := client.DialContext(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = stream.Close() })
	return stream
}

// TestANetworkChangeMovesTheActiveStreamInsteadOfReconnecting is the production acceptance: one
// open flow, one Reset (what InterfaceUpdated calls), and the flow continues on the same
// connection while the old socket is physically closed.
func TestANetworkChangeMovesTheActiveStreamInsteadOfReconnecting(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &migratingDialer{base: &N.DefaultDialer{}}
	client := newMigratingClient(t, server, dialer)
	t.Cleanup(func() { _ = client.Close() })

	stream := openStream(t, client)
	sendRecord(t, stream, 0, "before-the-network-change")
	require.Len(t, server.waitRecords(t, 0, 1), 1)

	started := time.Now()
	require.NoError(t, client.Reset())
	elapsed := time.Since(started)
	require.Less(t, elapsed, migrationProbeBudget+migrationRetireBudget+time.Second,
		"a migration must stay inside its budgets")

	require.Equal(t, 2, dialer.count(), "a network change creates exactly one candidate socket")
	require.Equal(t, int32(1), server.handshakes.Load(),
		"the change must move the connection, not perform a second handshake")
	require.Equal(t, int32(1), server.streamCount.Load(),
		"the change must not open a second stream")

	// The old socket is physically closed by the client, once, and only after the retirement.
	require.Eventually(t, func() bool { return dialer.socket(0).closes.Load() == 1 }, migrationSettle, time.Millisecond,
		"the handshake socket must be closed exactly once")
	// The candidate socket is the live one and is not closed.
	require.Zero(t, dialer.socket(1).closes.Load(), "the socket the connection moved to must stay open")

	// The same stream keeps carrying bytes the endpoint acknowledges.
	sendRecord(t, stream, 1, "after-the-network-change")

	// A demand after the change reuses the moved connection: no third socket, one handshake.
	nextStream := openStream(t, client)
	sendRecord(t, nextStream, 0, "new-flow-on-the-moved-connection")
	require.Equal(t, 2, dialer.count(), "a demand after a successful migration must not dial")
	require.Equal(t, int32(1), server.handshakes.Load())
	require.Equal(t, int32(2), server.streamCount.Load())
}

// TestAFailedCandidateLeavesAHealthyConnectionAndThenFallsBackToADial covers the two failure
// branches the task asks for: the old path stays usable while it is healthy, and once it is gone
// the next demand gets a new connection that carries valid business bytes.
func TestAFailedCandidateLeavesAHealthyConnectionAndThenFallsBackToADial(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &migratingDialer{base: &N.DefaultDialer{}, sabotage: true}
	client := newMigratingClient(t, server, dialer)
	t.Cleanup(func() { _ = client.Close() })

	stream := openStream(t, client)
	sendRecord(t, stream, 0, "before-the-failed-candidate")

	started := time.Now()
	require.NoError(t, client.Reset())
	elapsed := time.Since(started)
	require.Less(t, elapsed, migrationProbeBudget+migrationRetireBudget+time.Second,
		"a failed candidate must stay inside its budget")
	require.GreaterOrEqual(t, elapsed, migrationProbeBudget/2,
		"the probe must have been given its time instead of failing on a closed socket")

	require.Equal(t, 2, dialer.count(), "the failed candidate was still dialed once")
	require.Eventually(t, func() bool { return dialer.socket(1).closes.Load() == 1 }, migrationSettle, time.Millisecond,
		"the failed candidate's socket must be released exactly once")
	require.Zero(t, dialer.socket(0).closes.Load(), "a failed candidate must not retire the healthy connection")

	// Branch one: the old path is still healthy, so the same stream keeps carrying bytes.
	sendRecord(t, stream, 1, "the-old-path-is-still-here")
	require.Equal(t, int32(1), server.handshakes.Load())

	// Branch two: the old path really fails now. The generation ends, and the next demand dials a
	// NEW connection that carries valid bytes - and the sabotaged candidate must not be reused.
	dialer.setSabotage(false)
	require.NoError(t, dialer.socket(0).Close())
	require.Eventually(t, func() bool {
		conn := client.conn.Load()
		return conn == nil || conn.Context().Err() != nil
	}, migrationSettle, time.Millisecond, "closing the old path must end the generation")

	freshStream := openStream(t, client)
	require.Equal(t, 3, dialer.count(), "the fallback performs one new dial")
	sendRecord(t, freshStream, 0, "fresh-connection-after-the-old-path-failed")
	require.Equal(t, int32(2), server.handshakes.Load(), "the fallback establishes a new QUIC connection")
	require.Equal(t, int32(2), server.streamCount.Load())
}

// TestRepeatedNetworkChangesKeepOneSocketInFlight drives two changes with an open flow (path
// A -> B -> C): the second migration is what proves the retirement is a state machine and not a
// one-shot. Every retired socket is closed exactly once and the stream stays continuous.
//
// Two moves are the measured bound of this pin's path-validation connection-ID budget (the
// dependency's TestRepeatedMigrationStopsAtTheConnectionIDBudget records the same boundary); a
// third candidate cannot be validated, so production stops trying after one retry and falls back
// to retiring the generation, which TestASecondFailedCandidateRetiresTheGeneration pins.
func TestRepeatedNetworkChangesKeepOneSocketInFlight(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &migratingDialer{base: &N.DefaultDialer{}}
	client := newMigratingClient(t, server, dialer)
	t.Cleanup(func() { _ = client.Close() })

	stream := openStream(t, client)
	const changes = 2
	sequence := uint64(0)
	sendRecord(t, stream, sequence, "before-any-change")
	sequence++
	for change := 0; change < changes; change++ {
		require.NoError(t, client.Reset())
		require.Equal(t, change+2, dialer.count(), "each change adds exactly one candidate")
		sendRecord(t, stream, sequence, "after-change")
		sequence++
	}
	require.Len(t, server.waitRecords(t, 0, int(sequence)), int(sequence))
	require.Equal(t, int32(1), server.handshakes.Load(), "no change may reconnect")
	require.Equal(t, int32(1), server.streamCount.Load())

	// Every retired socket is closed once; the socket of the last applied path is still open.
	for index := 0; index < changes; index++ {
		socket := dialer.socket(index)
		require.NotNil(t, socket)
		require.Eventually(t, func() bool { return socket.closes.Load() == 1 }, migrationSettle, time.Millisecond,
			"socket %d must be closed exactly once", index)
	}
	live := dialer.socket(changes)
	require.NotNil(t, live)
	require.Zero(t, live.closes.Load(), "the socket of the last applied path must stay open")

	// The final close retires the live socket too, still exactly once.
	require.NoError(t, client.Close())
	require.Eventually(t, func() bool { return live.closes.Load() == 1 }, migrationSettle, time.Millisecond)
}

// TestAFailedCandidateBudgetRetiresTheGeneration pins the stop rule for a connection whose
// candidates cannot be validated: the first failure and one retry leave the connection alone,
// and the next network change retires the generation immediately instead of stalling every
// further change by the probe budget.
func TestAFailedCandidateBudgetRetiresTheGeneration(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &migratingDialer{base: &N.DefaultDialer{}, sabotage: true}
	client := newMigratingClient(t, server, dialer)
	t.Cleanup(func() { _ = client.Close() })

	stream := openStream(t, client)
	sendRecord(t, stream, 0, "before-the-failed-candidates")

	// First failure: the connection is untouched and still usable.
	require.NoError(t, client.Reset())
	require.Equal(t, 2, dialer.count())
	sendRecord(t, stream, 1, "the-old-path-after-the-first-failure")
	require.NotNil(t, client.conn.Load(), "one failed candidate must not retire the generation")

	// The retry fails too; the connection is still untouched after it.
	require.NoError(t, client.Reset())
	require.Equal(t, 3, dialer.count())
	require.NotNil(t, client.conn.Load(), "the retry must not retire the generation either")

	// The budget is spent now: the next change retires the generation without dialing a candidate.
	require.NoError(t, client.Reset())
	require.Equal(t, 3, dialer.count(), "a spent migration budget must not dial another candidate")
	require.Nil(t, client.conn.Load(), "the change after a spent budget retires the generation")
	require.Eventually(t, func() bool { return dialer.socket(0).closes.Load() == 1 }, migrationSettle, time.Millisecond)

	// The next demand is a bounded fresh dial that carries valid bytes.
	dialer.setSabotage(false)
	freshStream := openStream(t, client)
	sendRecord(t, freshStream, 0, "fresh-connection-after-the-spent-budget")
	require.Equal(t, 4, dialer.count())
	require.Equal(t, int32(2), server.handshakes.Load())
}

// TestCloseDuringAMigrationReleasesTheCandidateAndPublishesNothing pins the terminal state: Close
// while a candidate is being validated cancels the attempt, the candidate socket is released, and
// no connection is published afterwards.
func TestCloseDuringAMigrationReleasesTheCandidateAndPublishesNothing(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &migratingDialer{base: &N.DefaultDialer{}, sabotage: true}
	client := newMigratingClient(t, server, dialer)

	stream := openStream(t, client)
	sendRecord(t, stream, 0, "before-the-close")
	// The baseline is taken once the server, the connection and its stream goroutines exist, so
	// only the migration's own goroutines are counted.
	baseline := runtime.NumGoroutine()

	resetDone := make(chan error, 1)
	go func() { resetDone <- client.Reset() }()

	// Wait until the candidate exists, then close the client under the migration.
	require.Eventually(t, func() bool { return dialer.count() == 2 }, migrationSettle, time.Millisecond)
	require.NoError(t, client.Close())

	select {
	case err := <-resetDone:
		require.NoError(t, err, "Reset must not report the close as an error")
	case <-time.After(5 * time.Second):
		t.Fatal("the migration was not released by Close: the probe outlived the client")
	}

	require.Eventually(t, func() bool { return dialer.socket(1).closes.Load() == 1 }, migrationSettle, time.Millisecond,
		"the candidate socket must be released exactly once")
	require.Nil(t, client.conn.Load(), "a migration interrupted by Close must not publish a connection")
	require.Eventually(t, func() bool { return dialer.socket(0).closes.Load() == 1 }, migrationSettle, time.Millisecond,
		"the handshake socket is closed with the connection")
	requireNoExtraGoroutines(t, baseline)
}
