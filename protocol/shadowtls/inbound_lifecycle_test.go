package shadowtls

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

// These tests pin the lifecycle boundaries the ShadowTLS inbound relies on. The
// inbound owns exactly one resource - its TCP listener - and registers its close
// with the shared adapter.Scope; it deliberately adds no close path, registry or
// goroutine of its own. What is worth pinning is that the registration actually
// runs, that a failed start is never reported as a successful one, and that a Close
// racing a Start cannot leave the port bound.

// requireTCPPortReleased waits for port to be bindable again and reports the value
// that was actually observed, so a failure says whether the port was held or
// merely slow.
func requireTCPPortReleased(t *testing.T, port uint16) {
	t.Helper()
	address := fmt.Sprintf("127.0.0.1:%d", port)
	var lastErr error
	require.Eventually(t, func() bool {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			lastErr = err
			return false
		}
		_ = listener.Close()
		return true
	}, 5*time.Second, 20*time.Millisecond, "TCP port %d was never released (last bind error: %v)", port, lastErr)
}

// TestShadowTLSInboundStartOutsideTheStartStageBindsNothing pins the stage gate:
// only StartStateStart may open the listener.
func TestShadowTLSInboundStartOutsideTheStartStageBindsNothing(t *testing.T) {
	port := freeLoopbackPort(t)
	inboundInstance := newPreAuthInbound(t, port, freeLoopbackPort(t), nil)
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())

	for _, stage := range []adapter.StartStage{
		adapter.StartStateInitialize,
		adapter.StartStatePostStart,
		adapter.StartStateStarted,
	} {
		require.NoError(t, inboundInstance.Start(stage, scope), "stage %d must be a no-op", stage)
		require.Nil(t, inboundInstance.listener.TCPListener(), "stage %d must not bind", stage)
	}
	// The port is still free, so nothing was bound and nothing needs closing.
	requireTCPPortReleased(t, port)
	require.NoError(t, scope.Close())
}

// TestShadowTLSInboundBindFailureIsReported pins that an occupied port surfaces as
// a start error instead of a silently dead inbound.
func TestShadowTLSInboundBindFailureIsReported(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer occupied.Close()
	port := uint16(occupied.Addr().(*net.TCPAddr).Port)

	inboundInstance := newPreAuthInbound(t, port, freeLoopbackPort(t), nil)
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	require.Error(t, inboundInstance.Start(adapter.StartStateStart, scope),
		"a bind failure must be reported, never returned as a successful start")
	// A failed start registers no cleanup, so closing the scope is a clean no-op
	// rather than a second close of something that was never opened.
	require.NoError(t, scope.Close())
}

// TestShadowTLSInboundScopeCloseReleasesTheListener is the reason scope.Add exists
// here: closing the shared scope must close the inbound's listener.
func TestShadowTLSInboundScopeCloseReleasesTheListener(t *testing.T) {
	port := freeLoopbackPort(t)
	inboundInstance := newPreAuthInbound(t, port, freeLoopbackPort(t), nil)
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	require.NoError(t, inboundInstance.Start(adapter.StartStateStart, scope))

	require.NoError(t, scope.Close())
	requireTCPPortReleased(t, port)

	// A second close returns the recorded result and must not re-run the cleanup:
	// re-running it would be a second Close on an already closed listener.
	require.NoError(t, scope.Close(), "a repeated scope close must be harmless")
}

// TestShadowTLSInboundCloseRacingStartLeavesNoListener pins the interleaving box.go
// allows - Close may run while a start is in flight. Whichever order the framework
// lands on, the listener must not survive.
func TestShadowTLSInboundCloseRacingStartLeavesNoListener(t *testing.T) {
	port := freeLoopbackPort(t)
	inboundInstance := newPreAuthInbound(t, port, freeLoopbackPort(t), nil)
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())

	started := make(chan error, 1)
	go func() {
		started <- inboundInstance.Start(adapter.StartStateStart, scope)
	}()

	// Race the close against the start. adapter.Scope runs a cleanup registered
	// after the queue was taken synchronously in Add, which is what makes this
	// ordering safe rather than a leak.
	require.NoError(t, scope.Close())

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("Start deadlocked against Close")
	}
	requireTCPPortReleased(t, port)
}
