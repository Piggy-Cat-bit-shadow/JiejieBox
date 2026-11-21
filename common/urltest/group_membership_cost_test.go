package urltest_test

// GM-5: what the GROUP membership and the provider membership cache retain per node.
//
// These are part of the same "management data" attribution as the history storage, but they belong
// to protocol/group and adapter/provider, which this task may not modify. The test therefore only
// MEASURES them, using the real `NewURLTestGroup` constructor: the number it produces is the cost of
// the production object, not of a hand-built approximation.
//
// The shape is the real provider-backed one: N members, two providers, two groups. For each group the
// constructor keeps the member slice it was given, and the provider publication path keeps one slice
// per provider in `outboundsCache`, so the per-node reference count is what this test pins.

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/protocol/group"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

const gm5GroupMeasureEnv = "HAKO_GM5_GROUP_MEASURE"

// gm5Node is a member that is never started and never dialled: the constructor must not need more
// than its tag, which is what "a member is a reference" means.
type gm5Node struct {
	adapter.Outbound
	tag string
}

func (n *gm5Node) Type() string      { return "gm5-node" }
func (n *gm5Node) Tag() string       { return n.tag }
func (n *gm5Node) Network() []string { return nil }

// gm5Manager is the manager surface the group constructor touches. Every method is a no-op read.
type gm5Manager struct {
	byTag map[string]adapter.Outbound
}

func (m *gm5Manager) Start(adapter.StartStage, *adapter.Scope) error { return nil }
func (m *gm5Manager) Close() error                                   { return nil }
func (m *gm5Manager) Outbounds() []adapter.Outbound                  { return nil }
func (m *gm5Manager) Outbound(tag string) (adapter.Outbound, bool) {
	outbound, loaded := m.byTag[tag]
	return outbound, loaded
}
func (m *gm5Manager) Default() adapter.Outbound { return nil }
func (m *gm5Manager) Create(context.Context, adapter.Router, log.ContextLogger, string, string, any) error {
	return nil
}

func gm5HeapLive() (int64, int64) {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return int64(stats.HeapAlloc), int64(stats.HeapObjects)
}

// TestGM5GroupMembershipCost is opt-in: it measures the host and prints a ledger, it is not a gate.
func TestGM5GroupMembershipCost(t *testing.T) {
	if os.Getenv(gm5GroupMeasureEnv) == "" {
		t.Skipf("set %s=1 to measure the group membership cost", gm5GroupMeasureEnv)
	}
	for _, nodes := range []int{1000, 10000} {
		nodes := nodes
		t.Run(fmt.Sprintf("%d-nodes", nodes), func(t *testing.T) {
			// Fixture first: the node objects and the tag map are not what is measured.
			memberObjects := make([]*gm5Node, nodes)
			tags := make([]string, nodes)
			byTag := make(map[string]adapter.Outbound, nodes)
			for index := 0; index < nodes; index++ {
				memberObjects[index] = &gm5Node{tag: fmt.Sprintf("node-%05d", index)}
				tags[index] = memberObjects[index].tag
				byTag[tags[index]] = memberObjects[index]
			}
			manager := &gm5Manager{byTag: byTag}
			history := urltest.NewHistoryStorage()
			ctx := service.ContextWithPtr(context.Background(), history)
			logger := log.NewNOPFactory().NewLogger("gm5")

			baseHeap, baseObjects := gm5HeapLive()

			// 1. The member slice the group is constructed with. Interface value = two words.
			members := make([]adapter.Outbound, nodes)
			for index, node := range memberObjects {
				members[index] = node
			}
			afterMembersHeap, afterMembersObjects := gm5HeapLive()

			// 2. The provider membership cache: one slice per provider, covering the same nodes.
			// The production map is `map[string][]adapter.Outbound` (provider_members.go).
			firstHalf := make([]adapter.Outbound, nodes/2)
			secondHalf := make([]adapter.Outbound, nodes-nodes/2)
			copy(firstHalf, members[:nodes/2])
			copy(secondHalf, members[nodes/2:])
			cache := map[string][]adapter.Outbound{"provider-1": firstHalf, "provider-2": secondHalf}
			afterCacheHeap, afterCacheObjects := gm5HeapLive()

			// 3. Two real groups over the same members. A never-probed group must hold NO history:
			// history entries exist for measured (node, target) pairs, not for configured nodes.
			first, err := group.NewURLTestGroup(ctx, manager, logger, members, urltest.DefaultURLTestURL,
				30*time.Minute, 50, time.Hour, false)
			require.NoError(t, err)
			second, err := group.NewURLTestGroup(ctx, manager, logger, members, urltest.DefaultURLTestURL,
				30*time.Minute, 50, time.Hour, false)
			require.NoError(t, err)
			afterGroupHeap, afterGroupObjects := gm5HeapLive()
			require.Zero(t, history.DisplayEntryCount(),
				"a never-measured group must not create display entries for its members")
			require.Zero(t, history.HealthEntryCount(),
				"a never-measured group must not create health entries for its members")
			require.Equal(t, nodes, first.HealthSnapshot().Members)
			require.NoError(t, first.Close())
			require.NoError(t, second.Close())
			require.NoError(t, history.Close())

			// Every fixture and every measured object is kept alive until AFTER the last reading.
			// MEASURED the hard way in this task: a KeepAlive placed before the baseline let the
			// after-reading GC free the fixture's own pointer slice, which halved the member-slice
			// cost (160 000 -> 81 920 bytes for 10 000 nodes) and produced a negative "two groups"
			// delta. The census must see the fixture survive every reading it is the baseline for.
			runtime.KeepAlive(memberObjects)
			runtime.KeepAlive(tags)
			runtime.KeepAlive(byTag)
			runtime.KeepAlive(manager)
			runtime.KeepAlive(history)
			runtime.KeepAlive(ctx)
			runtime.KeepAlive(logger)
			runtime.KeepAlive(members)
			runtime.KeepAlive(firstHalf)
			runtime.KeepAlive(secondHalf)
			runtime.KeepAlive(cache)
			runtime.KeepAlive(first)
			runtime.KeepAlive(second)

			perNode := func(delta int64) int64 {
				if nodes == 0 {
					return 0
				}
				return delta / int64(nodes)
			}
			t.Logf("MEASURED group-membership nodes=%d member_slice=%+d (%.1f B/node) "+
				"provider_cache_slices=%+d (%.1f B/node) two_groups=%+d (%.1f B/node) "+
				"history_display=%d history_health=%d objects: members=%+d cache=%+d groups=%+d",
				nodes,
				afterMembersHeap-baseHeap, float64(afterMembersHeap-baseHeap)/float64(nodes),
				afterCacheHeap-afterMembersHeap, float64(afterCacheHeap-afterMembersHeap)/float64(nodes),
				afterGroupHeap-afterCacheHeap, float64(afterGroupHeap-afterCacheHeap)/float64(nodes),
				history.DisplayEntryCount(), history.HealthEntryCount(),
				afterMembersObjects-baseObjects, afterCacheObjects-afterMembersObjects,
				afterGroupObjects-afterCacheObjects)
			require.Greater(t, perNode(afterMembersHeap-baseHeap), int64(0),
				"the member slice must cost something; a zero would mean the census cannot see it")
		})
	}
}
