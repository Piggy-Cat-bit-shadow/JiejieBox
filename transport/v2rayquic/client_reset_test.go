//go:build with_quic

package v2rayquic

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResetKeepsTheClientAliveButCloseEndsIt pins the difference the network hook relies on.
//
// InterfaceUpdated used to call Close, which cancels the client's lifetime context: after one
// physical network change a VLESS/VMess/Trojan transport.type=quic instance could never dial again.
// Reset must abort the current connection while leaving a later offer able to create a new one.
func TestResetKeepsTheClientAliveButCloseEndsIt(t *testing.T) {
	dial := newFakeDial()
	conn, closes := fakeConnection()
	dial.setResult(conn, nil)

	client := newTestClient(t, dial)

	first := startOffer(client, context.Background())
	<-dial.started
	close(dial.release)
	firstResult := <-first
	require.NoError(t, firstResult.err)
	require.Same(t, conn, firstResult.conn)

	require.NoError(t, client.Reset())
	require.Equal(t, int32(1), closes.Load(), "the current connection must be closed exactly once")
	require.NoError(t, client.ctx.Err(), "Reset must NOT end the client's lifetime")

	second := startOffer(client, context.Background())
	secondResult := <-second
	require.NoError(t, secondResult.err, "a later dial must still be possible after Reset")
	require.Equal(t, 2, dial.callCount(), "the second offer must actually dial again")

	require.NoError(t, client.Close())
	require.Error(t, client.ctx.Err(), "Close remains the final action")
}
