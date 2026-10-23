package route

import (
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"

	"github.com/stretchr/testify/require"
)

type historyStubOutbound struct {
	adapter.Outbound
	tag string
}

func (s *historyStubOutbound) Tag() string { return s.tag }

type historyStubGroup struct {
	adapter.URLTestGroup
	tag string
}

func (g *historyStubGroup) Tag() string { return g.tag }

// A failed dial through a URLTest group must clear that node's measurement, or the group's next
// selection prefers the node that just failed to dial until the periodic test overwrites it.
// Upstream 5372a195b3e.
func TestFailedURLTestDialDeletesTheLeafHistory(t *testing.T) {
	storage := urltest.NewHistoryStorage()
	storage.StoreURLTestHistory("leaf-a", &adapter.URLTestHistory{Delay: 120})
	storage.StoreURLTestHistory("leaf-b", &adapter.URLTestHistory{Delay: 80})
	require.Equal(t, 2, storage.HealthEntryCount())

	manager := NewConnectionManager(nil)
	manager.SetURLTestHistory(storage)

	// The leaf is the last chain element; the group that selected it is in front.
	manager.deleteURLTestHistory([]adapter.Outbound{
		&historyStubGroup{tag: "auto"},
		&historyStubOutbound{tag: "leaf-a"},
	})

	require.Zero(t, storage.LoadURLTestHistory("leaf-a"), "the failed leaf's measurement must be gone")
	require.NotNil(t, storage.LoadURLTestHistory("leaf-b"), "other members keep their measurements")
	require.Equal(t, 1, storage.HealthEntryCount())
}

// The deletion is keyed on the presence of a URLTest group in the chain: a failure through a plain
// selector must not clear a same-named measurement that belongs to another group's member.
func TestFailedPlainChainKeepsTheHistory(t *testing.T) {
	storage := urltest.NewHistoryStorage()
	storage.StoreURLTestHistory("leaf-a", &adapter.URLTestHistory{Delay: 120})

	manager := NewConnectionManager(nil)
	manager.SetURLTestHistory(storage)

	manager.deleteURLTestHistory([]adapter.Outbound{
		&historyStubOutbound{tag: "selector"},
		&historyStubOutbound{tag: "leaf-a"},
	})
	require.NotNil(t, storage.LoadURLTestHistory("leaf-a"))
}
