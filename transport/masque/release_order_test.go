package masque

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

// releaseOrderHandler records whether the session's address was still in its pool when the releaser
// was called. The handler matches flows by source address, so the old order (pool release first) let
// a new session receive the recycled address and register a flow that this release then closed.
type releaseOrderHandler struct {
	ServerHandler
	pool                *addressPool
	address             netip.Addr
	addressWasAllocated bool
	calls               int
}

func (h *releaseOrderHandler) ReleaseSession([]netip.Addr) {
	h.calls++
	h.pool.access.Lock()
	_, h.addressWasAllocated = h.pool.allocated[h.address]
	h.pool.access.Unlock()
}

func TestSessionReleaseRunsBeforeTheAddressReturnsToThePool(t *testing.T) {
	pool := newAddressPool(netip.MustParsePrefix("198.18.0.0/24"))
	address, ok := pool.allocate()
	require.True(t, ok)
	handler := &releaseOrderHandler{pool: pool, address: address}
	server := &Server{
		handler:   handler,
		pools:     []*addressPool{pool},
		addresses: map[netip.Addr]*serverSession{},
	}
	current := &serverSession{server: server, addresses: []netip.Addr{address}}
	server.addresses[address] = current

	server.releaseSession(current)

	require.Equal(t, 1, handler.calls, "the releaser must be called exactly once")
	require.True(t, handler.addressWasAllocated,
		"the address was returned to the pool before the old generation's flows were released")
	pool.access.Lock()
	_, stillAllocated := pool.allocated[address]
	pool.access.Unlock()
	require.False(t, stillAllocated, "the address must be returned to the pool after the release")
}
