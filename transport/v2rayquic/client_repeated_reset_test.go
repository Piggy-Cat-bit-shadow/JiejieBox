//go:build with_quic

package v2rayquic

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRepeatedNetworkChangesRetireEachGenerationExactlyOnce covers the fallback this window
// actually ships on: while client-initiated migration cannot validate on the pinned QUIC
// library (see path_migration_test.go), a physical network change is still handled by
// InterfaceUpdated -> Reset -> new dial, and a phone changes networks more than once.
//
// The properties are the ones a repeated change must not break:
//
//   - every retired generation is closed exactly once, and only that generation;
//   - a Reset that arrives twice for the same change is idempotent (the second one closes
//     nothing and does not condemn the next generation);
//   - the client stays dialable across every generation, with one real dial per demand;
//   - the final Close is still final: the next demand is handed an already-cancelled dial
//     context, so no real dialer can resurrect the client.
//
// The fake replaces only the QUIC handshake; the retired-connection bookkeeping under test is
// the production one.
func TestRepeatedNetworkChangesRetireEachGenerationExactlyOnce(t *testing.T) {
	first, firstCloses := fakeConnection()
	second, secondCloses := fakeConnection()
	third, thirdCloses := fakeConnection()
	fourth, fourthCloses := fakeConnection()

	dial := newFakeDial()
	dial.results = []offerResult{
		{conn: first},
		{conn: second},
		{conn: third},
		{conn: fourth},
	}
	client := newTestClient(t, dial)

	dialGeneration := func(want *clientConnection, wantCalls int) {
		t.Helper()
		result := <-startOffer(client, context.Background())
		require.NoError(t, result.err)
		require.Same(t, want, result.conn)
		require.Equal(t, wantCalls, dial.callCount(), "each demand performs exactly one dial")
	}

	// Generation 1: the first physical network change.
	offer := startOffer(client, context.Background())
	<-dial.started
	close(dial.release)
	require.NoError(t, (<-offer).err)
	require.Equal(t, 1, dial.callCount())

	require.NoError(t, client.Reset())
	require.Equal(t, int32(1), firstCloses.Load(), "the retired generation is closed once")
	require.NoError(t, client.ctx.Err(), "Reset is not Close")

	// Generation 2: the change repeats.
	dialGeneration(second, 2)
	require.NoError(t, client.Reset())
	require.Equal(t, int32(1), secondCloses.Load())
	require.Equal(t, int32(1), firstCloses.Load(), "an older generation is not closed again")

	// Generation 3: two Resets for one observed change must be one retirement.
	dialGeneration(third, 3)
	require.NoError(t, client.Reset())
	require.NoError(t, client.Reset())
	require.Equal(t, int32(1), thirdCloses.Load(), "a repeated Reset must not close the generation twice")
	require.Equal(t, int32(1), secondCloses.Load())

	// Generation 4: still dialable after three changes.
	dialGeneration(fourth, 4)

	require.NoError(t, client.Close())
	require.Equal(t, int32(1), fourthCloses.Load(), "the final Close retires the live generation once")
	require.Error(t, client.ctx.Err())

	// The final Close stays final for the next demand too: the constructor is handed the
	// client's already-cancelled lifetime context, which is what stops a real dialer from
	// producing a fifth generation (the in-flight case is pinned separately by
	// TestClosedClientCannotBeRevivedByALateSuccess).
	startOffer(client, context.Background())
	require.Eventually(t, func() bool {
		return dial.callCount() == 5
	}, 2*time.Second, time.Millisecond, "the demand after the final close must reach the constructor")
	require.Error(t, dial.lastContext().Err(), "a demand after the final close must be handed a cancelled context")
}
