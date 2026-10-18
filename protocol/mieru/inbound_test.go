package mieru

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTrackedConnRemovesItselfFromRegistry pins the bookkeeping that keeps the
// inbound's live-connection registry bounded. Without it, every finished
// session would stay in the map until the inbound closed.
func TestTrackedConnRemovesItselfFromRegistry(t *testing.T) {
	registry := connRegistry{conns: make(map[net.Conn]struct{})}
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	tracked := &trackedConn{Conn: serverConn, registry: &registry}
	require.True(t, registry.add(tracked))
	require.Len(t, registry.conns, 1)
	require.NoError(t, tracked.Close())
	require.Empty(t, registry.conns)

	// CloseAll closes every remaining connection and refuses new ones.
	secondClient, secondServer := net.Pipe()
	defer secondClient.Close()
	second := &trackedConn{Conn: secondServer, registry: &registry}
	require.True(t, registry.add(second))
	registry.closeAll()
	require.Empty(t, registry.conns)
	require.False(t, registry.add(&trackedConn{Conn: secondClient, registry: &registry}))
}

// TestConnRegistryClosedBeforeAdd proves a connection accepted while Close is
// running is closed rather than tracked into a registry nobody will walk.
func TestConnRegistryClosedBeforeAdd(t *testing.T) {
	registry := connRegistry{conns: make(map[net.Conn]struct{})}
	registry.closeAll()
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	require.False(t, registry.add(&trackedConn{Conn: serverConn, registry: &registry}))
}
