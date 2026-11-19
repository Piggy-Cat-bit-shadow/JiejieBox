//go:build with_quic

package v2rayquic

// R-B2: publishing a completed creation and acknowledging its generation used to be two steps, with a
// lock in between and the acknowledgement after it. The dial's own re-confirmation closed the large
// window (a change during the handshake), but not the instruction-sized one: an Advance landing
// between the comparison and the acknowledgement made Acknowledge() stamp the NEW epoch onto a
// connection built on the OLD network. After that nothing retires it - Stale() is false - so every
// later demand is handed a session from the network that just ended. That is audit F-15 reached
// through a gap a comparison cannot close, and it is why the sibling pools' publish decisions became
// one call; the QUIC pool was the last one left.
//
// The test moves the epoch inside exactly that window with the package's test hook, which is the only
// deterministic way to stand in it.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/runtimecoord"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

func TestAPublishIsNotAcknowledgedForAGenerationItWasNotBuiltFor(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &migratingDialer{base: &N.DefaultDialer{}}
	coordinator := runtimecoord.New()
	t.Cleanup(func() { _ = coordinator.Close() })
	client := newGenerationClient(t, server, dialer, coordinator)
	t.Cleanup(func() { _ = client.Close() })

	// The epoch moves after the dial's own re-confirmation and before the publish decision.
	testHookBeforePublish = func() {
		testHookBeforePublish = nil
		coordinator.Advance(1)
	}
	t.Cleanup(func() { testHookBeforePublish = nil })

	conn, err := client.DialContext(context.Background())
	require.Nil(t, conn, "a connection built for the previous generation must not be handed out")
	require.True(t, errors.As(err, new(staleGenerationError)),
		"a result published across a network change is a retryable stale generation, got %v", err)
	require.Nil(t, client.conn.Load(),
		"the connection was published into the pool even though the network had moved on")
	require.True(t, client.generationStale(),
		"the pool recorded itself as current for a generation it was not built for; nothing will "+
			"retire the stale connection and every later demand would be handed it")

	// The next demand starts on the network that is current now, and the pool is current again.
	next, err := client.DialContext(context.Background())
	require.NoError(t, err)
	defer next.Close()
	require.NotNil(t, client.conn.Load())
	require.False(t, client.generationStale())
	require.Equal(t, 2, dialer.count(),
		"the retry must dial the new generation rather than reuse the discarded one")
	_, err = next.Write([]byte("after-the-change"))
	require.NoError(t, err)
	require.Eventually(t, func() bool { return server.streamCount.Load() == 1 },
		migrationSettle, time.Millisecond,
		"exactly one stream must have opened: the discarded connection carried none, and the "+
			"recovered one carries this record")
}
