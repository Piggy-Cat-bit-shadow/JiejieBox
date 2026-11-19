package v2rayhttp

// The publish decision: compare the epoch and record the pool as current in ONE step.
//
// # The window this closes
//
// A dial captures the generation it is being built for (`startGeneration`) and must decide, once the
// creation returned, whether the result may be published. The obvious shape - compare the epoch, then
// acknowledge - leaves the few instructions between the two open: an Advance landing inside them makes
// the acknowledgement stamp the NEW epoch on a connection built on the OLD network, after which
// nothing retires it and every later demand is handed it.
//
// The test below forces exactly that interleaving through testHookAfterGenerationCompare, which is the
// only way to hit a window that is a handful of instructions wide. A code path that compares first and
// acknowledges second fails it; the atomic AcknowledgeIfCurrent passes.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAnEpochChangeDuringThePublishDecisionDiscardsTheConnection is the window.
func TestAnEpochChangeDuringThePublishDecisionDiscardsTheConnection(t *testing.T) {
	ctx, coordinator := generationTestContext(t)
	server, accepted, _ := newGenerationServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	client := newGenerationClient(t, ctx, server.Listener.Addr().String())

	// Warm the pool, so the next dial is a reuse and its response arrives immediately: what is being
	// tested is the publish decision, not the creation.
	dialForResetTest(t, client)
	require.Equal(t, int32(1), accepted.Load())
	require.False(t, client.generationStale())

	// One Advance, delivered inside the decision.
	var advanced bool
	testHookAfterGenerationCompare = func() {
		if advanced {
			return
		}
		advanced = true
		coordinator.Advance(1)
	}
	t.Cleanup(func() { testHookAfterGenerationCompare = nil })

	conn, err := client.DialContext(ctx)
	require.NoError(t, err, "the failure is delivered by the late-bound conn, not by the dial")
	require.NotNil(t, conn)
	// The conn is "late": the publish decision has not been made when DialContext returns, so the
	// discard surfaces as the first read's error.
	buffer := make([]byte, 8)
	_, readErr := conn.Read(buffer)
	require.Error(t, readErr,
		"a connection whose generation moved during the publish decision must be discarded, not "+
			"published with the new epoch stamped on it")
	require.Contains(t, readErr.Error(), "the network changed while the connection was being established",
		"the caller must see the retryable generation error rather than the pooled connection")
	_ = conn.Close()

	// The proof that it was not stamped: the pool is still stale for the new generation, so the next
	// hand-out retires it instead of lending the connection built on the old network.
	require.True(t, client.generationStale(),
		"the discarded connection was acknowledged anyway, so the pool now claims to be current on "+
			"evidence from the previous network")
}

// TestThePublishDecisionIsAPlainAcknowledgementWithoutANetworkChange is the guard-rail: the atomic
// form must not turn a healthy publish into a discard.
func TestThePublishDecisionIsAPlainAcknowledgementWithoutANetworkChange(t *testing.T) {
	ctx, _ := generationTestContext(t)
	server, accepted, _ := newGenerationServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	client := newGenerationClient(t, ctx, server.Listener.Addr().String())
	dialForResetTest(t, client)
	require.Equal(t, int32(1), accepted.Load())
	require.False(t, client.generationStale())

	dialForResetTest(t, client)
	require.Equal(t, int32(1), accepted.Load(), "a healthy publish keeps reusing the pool")
	require.False(t, client.generationStale())
}

var _ = httptest.NewServer // keep the import available for fixtures added later
