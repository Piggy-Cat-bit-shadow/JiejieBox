//go:build with_quic

package v2rayquic

// Who owns the cleanup of a path socket.
//
// # The defect
//
// every candidate used to start a cleanup goroutine that waited for the whole CONNECTION to end
// before it closed the candidate's socket. A candidate that failed had its socket closed by the
// attempt itself, and a path that a LATER migration replaced had its socket closed by adoptPath -
// but in both cases the goroutine started for that socket had nothing left to wait for except the
// connection's end, which is minutes or hours away. Every migration therefore added one permanent
// goroutine: after A -> B -> C the review observed two `(*Client).migrate.func1` frames in
// runtime.Stack while the connection was still carrying the stream.
//
// # What is asserted here
//
// The cleanup task belongs to the PATH, not to the attempt and not to the connection: it exits when
// the path is retired (abandoned by a failed candidate, replaced by a successful migration, or
// closed with the connection), so one live connection keeps exactly one task however many
// migrations it went through. The count is kept by the production code (pathCleanupTasks), so the
// assertion does not depend on sampling stacks.
//
// Socket identity, exactly-once close and the acknowledged bytes of the moved stream are pinned by
// client_migration_test.go and path_migration_test.go; this file is about the task's lifetime.

import (
	"testing"
	"time"

	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// pathCleanupBaseline is the number of tasks that belong to something other than the client under
// test. Every assertion below is a delta from it, so a test running in parallel elsewhere in the
// package cannot make this one flaky.
func pathCleanupBaseline() int64 { return pathCleanupTasks.Load() }

// requirePathCleanupTasks waits until the live task count has the expected delta and then checks it
// stays there: a task that exits late is as wrong as one that never exits, because both leave the
// connection's bookkeeping describing a path that is gone.
func requirePathCleanupTasks(t *testing.T, baseline int64, delta int64, message string) {
	t.Helper()
	require.Eventually(t, func() bool {
		return pathCleanupTasks.Load()-baseline == delta
	}, migrationSettle, time.Millisecond, "%s: have %d tasks above baseline, want %d",
		message, pathCleanupTasks.Load()-baseline, delta)
}

// TestAFailedCandidateReleasesItsCleanupTaskWhileTheConnectionLives is the reproduction.
//
// A candidate that cannot be validated is abandoned before any switch: the connection is never
// touched and keeps serving the stream on its old path. The failed attempt's socket is closed, so
// its cleanup task has nothing left to own - it must exit immediately, not at the connection's end.
func TestAFailedCandidateReleasesItsCleanupTaskWhileTheConnectionLives(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &migratingDialer{base: &N.DefaultDialer{}, sabotage: true}
	client := newMigratingClient(t, server, dialer)
	t.Cleanup(func() { _ = client.Close() })

	baseline := pathCleanupBaseline()
	stream := openStream(t, client)
	requirePathCleanupTasks(t, baseline, 1, "one connection with one path owns one cleanup task")

	require.NoError(t, client.Reset())
	require.Equal(t, 2, dialer.count(), "the failed attempt dialed one candidate")
	require.Eventually(t, func() bool { return dialer.socket(1).closes.Load() == 1 }, migrationSettle, time.Millisecond,
		"the abandoned candidate's socket must be released exactly once")
	require.NotNil(t, client.conn.Load(), "a failed candidate must leave the generation alive")

	// The only task left is the live path's. Before the fix the abandoned candidate's task kept
	// waiting for the connection, so this stayed at 2 for the connection's whole lifetime.
	requirePathCleanupTasks(t, baseline, 1,
		"the abandoned candidate's cleanup task outlived the path it was created for")

	// The connection is still the one carrying the flow: the old path was never retired.
	sendRecord(t, stream, 0, "the-old-path-is-still-carrying-bytes")
	require.Len(t, server.waitRecords(t, 0, 1), 1)
}

// TestEachMigrationLeavesOneCleanupTaskForOneConnection drives A -> B -> C with an open flow.
//
// A successful migration hands the candidate to the connection and closes the previous path: the
// previous path's task must exit with it. The count therefore stays at one while the connection
// lives, and returns to the baseline when the connection is closed.
func TestEachMigrationLeavesOneCleanupTaskForOneConnection(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &migratingDialer{base: &N.DefaultDialer{}}
	client := newMigratingClient(t, server, dialer)
	t.Cleanup(func() { _ = client.Close() })

	baseline := pathCleanupBaseline()
	stream := openStream(t, client)
	requirePathCleanupTasks(t, baseline, 1, "one connection with one path owns one cleanup task")

	sequence := uint64(0)
	sendRecord(t, stream, sequence, "before-any-change")
	sequence++
	for change := 0; change < 2; change++ {
		require.NoError(t, client.Reset())
		sendRecord(t, stream, sequence, "after-change")
		sequence++
		// Every retired path is closed exactly once...
		require.Eventually(t, func() bool { return dialer.socket(change).closes.Load() == 1 },
			migrationSettle, time.Millisecond, "path %d must be closed exactly once", change)
		// ...and its cleanup task exits with it, so the connection never accumulates one per
		// migration. Two migrations are the measured bound of this pin's path-validation budget.
		requirePathCleanupTasks(t, baseline, 1,
			"each migration must leave exactly one cleanup task for the one live path")
	}
	require.Len(t, server.waitRecords(t, 0, int(sequence)), int(sequence))
	require.Equal(t, int32(1), server.handshakes.Load(), "no change may reconnect")
	require.Zero(t, dialer.socket(2).closes.Load(), "the live path stays open")

	require.NoError(t, client.Close())
	requirePathCleanupTasks(t, baseline, 0, "the final close retires the last path and its task")
}

// TestCloseDuringACandidateReleasesBothCleanupTasks is the terminal state: a candidate in flight
// when the client closes must release its socket AND its task, and the connection's own task must
// go with the connection. Nothing may be left waiting for an end that already happened.
func TestCloseDuringACandidateReleasesBothCleanupTasks(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &migratingDialer{base: &N.DefaultDialer{}, sabotage: true}
	client := newMigratingClient(t, server, dialer)

	baseline := pathCleanupBaseline()
	stream := openStream(t, client)
	requirePathCleanupTasks(t, baseline, 1, "one connection with one path owns one cleanup task")

	resetDone := make(chan error, 1)
	go func() { resetDone <- client.Reset() }()
	require.Eventually(t, func() bool { return dialer.count() == 2 }, migrationSettle, time.Millisecond,
		"the candidate socket never appeared")

	// Both tasks exist while the candidate is being validated: the live path's and the candidate's.
	requirePathCleanupTasks(t, baseline, 2, "the candidate must own a cleanup task while it is in flight")

	require.NoError(t, client.Close())
	select {
	case err := <-resetDone:
		require.NoError(t, err, "Reset must not report the close as an error")
	case <-time.After(5 * time.Second):
		t.Fatal("the migration was not released by Close")
	}
	_ = stream
	requirePathCleanupTasks(t, baseline, 0, "a closed client must leave no cleanup task behind")
	require.Eventually(t, func() bool { return dialer.socket(0).closes.Load() == 1 }, migrationSettle, time.Millisecond)
	require.Eventually(t, func() bool { return dialer.socket(1).closes.Load() == 1 }, migrationSettle, time.Millisecond)
}
