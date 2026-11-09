package daemon

// The two ways a group switch could leak across a boundary the daemon owns:
//
//	another GROUP in the same Box   a concurrent switch of group-b must not move group-a
//	another SERVICE in the process  two instances running the same profile (the same group tags)
//	                                must not answer each other's switches or share their selection
//
// Both are identity questions, and both are cheap to get wrong in a manager that keys anything by
// tag alone. They use the same fixture as the reload test: a test-only member type whose dial
// reaches a per-member loopback listener, so "which member the flow left through" is observed.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// twoGroupProfile has two selectors in one Box, each with its own two members.
func twoGroupProfile(members map[string]*memberListener) string {
	return fmt.Sprintf(`{
  "log": {"level": "error"},
  "outbounds": [
    {"type": "probe-member", "tag": "a1", "destination": "%s"},
    {"type": "probe-member", "tag": "a2", "destination": "%s"},
    {"type": "probe-member", "tag": "b1", "destination": "%s"},
    {"type": "probe-member", "tag": "b2", "destination": "%s"},
    {"type": "selector", "tag": "group-a", "outbounds": ["a1", "a2"], "default": "a1"},
    {"type": "selector", "tag": "group-b", "outbounds": ["b1", "b2"], "default": "b1"}
  ]
}`,
		members["a1"].target(), members["a2"].target(),
		members["b1"].target(), members["b2"].target())
}

func (m *memberListener) target() string { return fmt.Sprintf("127.0.0.1:%d", m.port()) }

// reportedGroupSelection reads the selection of one named group.
func reportedGroupSelection(t *testing.T, started *StartedService, groupTag string) string {
	t.Helper()
	groups := started.readGroups()
	for _, entry := range groups.Group {
		if entry.Tag == groupTag {
			require.True(t, entry.Selectable, "the group %s must be reported as a selectable selector", groupTag)
			return entry.Selected
		}
	}
	t.Fatalf("the running instance reports no group named %s", groupTag)
	return ""
}

// dialThroughNamedGroup dials through one group and returns the member tag whose listener received
// the connection.
func dialThroughNamedGroup(t *testing.T, started *StartedService, groupTag string, known map[string]*memberListener) string {
	t.Helper()
	instance := started.Instance()
	require.NotNil(t, instance)
	groupOutbound, isLoaded := instance.outboundManager.Outbound(groupTag)
	require.True(t, isLoaded, "the running instance has no group %s", groupTag)
	dialer, isDialer := groupOutbound.(N.Dialer)
	require.True(t, isDialer, "the group %s must be a dialer", groupTag)

	before := make(map[string]int32, len(known))
	for tag, member := range known {
		before[tag] = member.accepted.Load()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("origin.test:443"))
	require.NoError(t, err, "the group %s must be able to dial through its selected member", groupTag)
	_ = conn.Close()

	reached := ""
	require.Eventually(t, func() bool {
		for tag, member := range known {
			if member.accepted.Load() > before[tag] {
				reached = tag
				return true
			}
		}
		return false
	}, 5*time.Second, 2*time.Millisecond, "the flow of %s reached none of its members", groupTag)
	return reached
}

// newTwoGroupService starts one service on the two-group profile.
func newTwoGroupService(t *testing.T, profile string) *StartedService {
	t.Helper()
	started := NewStartedService(ServiceOptions{Context: selectorSwitchContext(t)})
	require.NoError(t, started.StartOrReloadService(context.Background(), profile, nil))
	require.NotNil(t, started.Instance())
	return started
}

func TestConcurrentSwitchesOfTwoGroupsStayIndependent(t *testing.T) {
	members := map[string]*memberListener{
		"a1": startMemberListener(t),
		"a2": startMemberListener(t),
		"b1": startMemberListener(t),
		"b2": startMemberListener(t),
	}
	profile := twoGroupProfile(members)
	started := newTwoGroupService(t, profile)
	t.Cleanup(func() { _ = started.CloseService() })

	require.Equal(t, "a1", reportedGroupSelection(t, started, "group-a"))
	require.Equal(t, "b1", reportedGroupSelection(t, started, "group-b"))

	// Both groups switch at the same time, in opposite directions.
	var waitGroup sync.WaitGroup
	switchOne := func(groupTag, memberTag string) {
		defer waitGroup.Done()
		_, err := started.SelectOutbound(context.Background(), &SelectOutboundRequest{
			GroupTag:    groupTag,
			OutboundTag: memberTag,
		})
		require.NoError(t, err)
	}
	waitGroup.Add(2)
	go switchOne("group-a", "a2")
	go switchOne("group-b", "b2")
	waitGroup.Wait()

	require.Equal(t, "a2", reportedGroupSelection(t, started, "group-a"),
		"a concurrent switch of group-b moved group-a")
	require.Equal(t, "b2", reportedGroupSelection(t, started, "group-b"),
		"a concurrent switch of group-a moved group-b")

	groupAMembers := map[string]*memberListener{"a1": members["a1"], "a2": members["a2"]}
	groupBMembers := map[string]*memberListener{"b1": members["b1"], "b2": members["b2"]}
	require.Equal(t, "a2", dialThroughNamedGroup(t, started, "group-a", groupAMembers),
		"group-a reports a2 but its flow left through another member")
	require.Equal(t, "b2", dialThroughNamedGroup(t, started, "group-b", groupBMembers),
		"group-b reports b2 but its flow left through another member")
}

func TestTwoServiceInstancesWithTheSameGroupDoNotCrossTalk(t *testing.T) {
	members := map[string]*memberListener{
		"a1": startMemberListener(t),
		"a2": startMemberListener(t),
		"b1": startMemberListener(t),
		"b2": startMemberListener(t),
	}
	profile := twoGroupProfile(members)

	// Two independent services running the SAME profile: same group tags, same member tags.
	first := newTwoGroupService(t, profile)
	t.Cleanup(func() { _ = first.CloseService() })
	second := newTwoGroupService(t, profile)
	t.Cleanup(func() { _ = second.CloseService() })

	// Switching one instance must leave the other where it was.
	_, err := first.SelectOutbound(context.Background(), &SelectOutboundRequest{
		GroupTag:    "group-a",
		OutboundTag: "a2",
	})
	require.NoError(t, err)
	require.Equal(t, "a2", reportedGroupSelection(t, first, "group-a"))
	require.Equal(t, "a1", reportedGroupSelection(t, second, "group-a"),
		"a switch addressed to one service moved the same-named group in another service")

	groupAMembers := map[string]*memberListener{"a1": members["a1"], "a2": members["a2"]}
	require.Equal(t, "a2", dialThroughNamedGroup(t, first, "group-a", groupAMembers))
	require.Equal(t, "a1", dialThroughNamedGroup(t, second, "group-a", groupAMembers))

	// Closing one instance leaves the other running with its own selection.
	require.NoError(t, first.CloseService())
	require.Equal(t, "a1", reportedGroupSelection(t, second, "group-a"),
		"closing one service changed the selection of another")
	require.Equal(t, "a1", dialThroughNamedGroup(t, second, "group-a", groupAMembers))
}
