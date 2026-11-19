package v2rayhttp

// Why this file exists: the difference between "reset this pool for a new network" and "close it".
//
// protocol/{vless,vmess,trojan}.InterfaceUpdated prefers ResetContext, then Reset, and falls back to
// Close. This transport had neither, so a network change took the fallback - and Close unregisters
// the pool's generation registration, which runtimecoord reports as stale FOREVER and refuses to
// acknowledge. Every later demand therefore took dialHTTP2's "previous generation" branch: a new
// http2.Transport (and a new TCP + TLS handshake) per stream, for the life of the process, with the
// replaced transports accumulating in the pool ledger.
//
// The observations below are made at the SERVER, not in the transport's bookkeeping, so a passing run
// means a real connection was reused.

import (
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func dialForResetTest(t *testing.T, client *Client) net.Conn {
	t.Helper()
	conn, err := client.DialContext(t.Context())
	require.NoError(t, err)
	require.Equal(t, "ok", readExactly(t, conn, 2))
	require.NoError(t, conn.Close())
	return conn
}

// TestResetKeepsThePoolReusableAcrossAGenerationChange is the property the fallback to Close broke.
//
// One reset, then two demands: the reset legitimately replaces the generation once, and the SECOND
// demand must ride on the connection the first one opened. A reset implemented as Close makes the
// second demand open its own connection, because the closed registration keeps reporting stale.
func TestResetKeepsThePoolReusableAcrossAGenerationChange(t *testing.T) {
	ctx, coordinator := generationTestContext(t)
	server, accepted, _ := newGenerationServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	client := newGenerationClient(t, ctx, server.Listener.Addr().String())

	dialForResetTest(t, client)
	require.Equal(t, int32(1), accepted.Load(), "the first demand opens one connection")

	coordinator.Advance(1)
	require.NoError(t, client.Reset())

	dialForResetTest(t, client)
	afterReset := accepted.Load()
	require.EqualValues(t, 2, afterReset,
		"the first demand after a reset must open the new generation's connection")

	dialForResetTest(t, client)
	require.Equal(t, afterReset, accepted.Load(),
		"the second demand after a reset must REUSE the connection: a reset that unregisters the "+
			"generation reads as stale forever, so every stream would pay a fresh handshake")
	require.False(t, client.generationStale(),
		"after a successful demand on the new generation the pool must be current again; a reset "+
			"that closes the registration can never leave this state")
}

// TestResetDoesNotCutAStreamThatIsAlreadyRunning is the other half, and the reason Reset is a
// retirement rather than CloseAll: a stream that started on the previous network belongs to that
// generation and finishes on it.
func TestResetDoesNotCutAStreamThatIsAlreadyRunning(t *testing.T) {
	ctx, coordinator := generationTestContext(t)
	releaseSecondChunk := make(chan struct{})
	server, accepted, _ := newGenerationServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		_, _ = writer.Write([]byte("one"))
		writer.(http.Flusher).Flush()
		<-releaseSecondChunk
		_, _ = writer.Write([]byte("two"))
		writer.(http.Flusher).Flush()
	}))
	client := newGenerationClient(t, ctx, server.Listener.Addr().String())

	busy, err := client.DialContext(ctx)
	require.NoError(t, err)
	require.Equal(t, "one", readExactly(t, busy, 3))

	coordinator.Advance(1)
	require.NoError(t, client.Reset())

	// The reset replaced the generation, so this demand is on a fresh connection while the first
	// stream is still running.
	fresh, err := client.DialContext(ctx)
	require.NoError(t, err)
	require.Equal(t, "one", readExactly(t, fresh, 3))
	require.Equal(t, int32(2), accepted.Load())

	close(releaseSecondChunk)
	require.Equal(t, "two", readExactly(t, busy, 3),
		"a reset must drain the previous generation, not cut the stream that is using it")
	require.Equal(t, "two", readExactly(t, fresh, 3))
	require.NoError(t, busy.Close())
	require.NoError(t, fresh.Close())
}
