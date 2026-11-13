package masque

import (
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFlowRegistryRejectsRegistrationAfterCloseAll pins the teardown boundary: a flow the userspace
// stack dispatches after the endpoint started closing must be closed immediately, not stored in a
// map no later closeAll will walk again.
func TestFlowRegistryRejectsRegistrationAfterCloseAll(t *testing.T) {
	var registry flowRegistry
	first, firstPeer := net.Pipe()
	t.Cleanup(func() {
		_ = first.Close()
		_ = firstPeer.Close()
	})
	require.True(t, registry.add(first, netip.MustParseAddr("198.18.0.1")))
	require.Equal(t, 1, registry.closeAll())

	second, secondPeer := net.Pipe()
	t.Cleanup(func() {
		_ = second.Close()
		_ = secondPeer.Close()
	})
	require.False(t, registry.add(second, netip.MustParseAddr("198.18.0.2")),
		"a flow registered after closeAll must be refused")
	_, err := second.Write([]byte("after-close"))
	require.Error(t, err, "the refused flow must already be closed")

	registry.access.Lock()
	require.Empty(t, registry.flows)
	registry.access.Unlock()
}
