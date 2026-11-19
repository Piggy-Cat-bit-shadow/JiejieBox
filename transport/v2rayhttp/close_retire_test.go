package v2rayhttp

// A THREE-way interaction around one physical object: the pool's generation ledger.
//
//   A  Box teardown / outbound Close       -> Client.Close: pool.CloseAll, unregister the generation
//   B  a LATE dial that arrives afterwards -> dialHTTP2 sees the (now closed) registration as stale
//                                             and calls retireGeneration, which INSTALLS a generation
//   C  the ledger that must release every  -> GenerationPool.Retire / releaseGeneration
//      transport it is ever handed
//
// Any two of them are fine. A+B without a closed state in C leaves a transport inside a pool that has
// already released everything: nothing will ever release it, and the physical connections it opens
// belong to a client that was closed. That is the same shape as window A's A-3 (a QUIC carrier with no
// closed state) one layer up, and it is what these tests pin.

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"golang.org/x/net/http2"
)

// TestALateDialAfterCloseInstallsNoGeneration is the three-way case.
//
// A demand that arrives after Close still reaches dialHTTP2, and the registration is stale by then (a
// closed registration reports stale forever), so the stale path runs. What it must NOT do is put a new
// generation into a pool that has already closed: the pool's own ledger is the only thing that could
// release it, and it is closed.
func TestALateDialAfterCloseInstallsNoGeneration(t *testing.T) {
	ctx, _ := generationTestContext(t)
	server, accepted, _ := newGenerationServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	client := newGenerationClient(t, ctx, server.Listener.Addr().String())

	// Warm the pool, so the late dial is about the generation path rather than a cold creation.
	dialForResetTest(t, client)
	require.Equal(t, int32(1), accepted.Load())

	created := new(atomic.Int64)
	build := client.newTransport
	client.newTransport = func() *http2.Transport {
		created.Add(1)
		return build()
	}

	require.NoError(t, client.Close())
	require.True(t, client.generationStale(),
		"a closed registration must report stale, or the late-dial path is unreachable and this "+
			"test would pass for the wrong reason")

	// The late dial: the caller is gone, the box is down, and this arrives anyway.
	lateConn, _ := client.DialContext(ctx)
	if lateConn != nil {
		// The http/2 dial is late-bound: reading is what makes the goroutine finish, so the ledger
		// assertions below are made after the discarded request has actually settled.
		buffer := make([]byte, 8)
		_, _ = lateConn.Read(buffer)
		_ = lateConn.Close()
	}

	require.Zero(t, created.Load(),
		"a dial after Close installed a new transport into a pool that has already released "+
			"everything: no owner is left to release it, and the connections it opens belong to a "+
			"closed client")

	// The ledger must agree: nothing current, nothing left draining.
	require.Eventually(t, func() bool {
		snapshot := client.pool.Snapshot()
		return snapshot.CurrentInflight == 0 && snapshot.RetiredDraining == 0 &&
			snapshot.RetiredInflight == 0
	}, 5*time.Second, 10*time.Millisecond,
		"the pool still holds an in-flight or draining generation after the late dial settled: %+v",
		client.pool.Snapshot())
}

// TestARetireOnAClosedPoolReleasesWhatItIsGiven is the defensive half at the pool itself: even when a
// caller races a Close (the check above is a few instructions wide), a transport handed to a closed
// pool must be released rather than adopted.
func TestARetireOnAClosedPoolReleasesWhatItIsGiven(t *testing.T) {
	pool := NewGenerationPool(&http2.Transport{})
	offered := &http2.Transport{}
	pool.CloseAll()

	before := pool.Snapshot()
	pool.Retire(offered)
	after := pool.Snapshot()

	require.Equal(t, before.Sequence, after.Sequence,
		"a closed pool must not adopt a new generation")
	require.Equal(t, before.Terminated+1, after.Terminated,
		"the offered transport must be released (counted once), not leaked inside the pool")
}
