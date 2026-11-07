package physicalpath

import (
	"testing"

	"github.com/sagernet/sing-box/adapter"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// A group whose membership can be populated by an outbound provider AFTER Start. The capability is
// what the walk reads to tell "waiting" from "permanently empty"; see adapter.ProviderBackedGroup.
type providerBackedTestGroup struct {
	*testGroup
	providers   []string
	usesAllFlag bool
}

func (g *providerBackedTestGroup) ProviderMembership() ([]string, bool) {
	return g.providers, g.usesAllFlag
}

func newProviderBackedFixture(t *testing.T, members []string) (*testRegistry, *providerBackedTestGroup, *testLeaf) {
	t.Helper()
	group := &providerBackedTestGroup{
		testGroup: &testGroup{
			tag:       "g",
			groupType: "selector",
			members:   members,
			networks:  []string{N.NetworkTCP, N.NetworkUDP},
			lookup:    make(map[string]adapter.Outbound),
		},
		providers: []string{"p"},
	}
	exit := dualLeaf("exit", "g")
	registry := newRegistry(exit, group)
	if len(members) > 0 {
		// The group declares its first member as selected, like a real selector after Start.
		group.selected = members[0]
	}
	return registry, group, exit
}

// TestAProviderFedEmptyGroupIsPendingAndNotRefused is the start-time half of the reported defect.
//
// A static outbound dials through a selector whose only member source is an outbound provider. The
// provider publishes after the outbound manager's dry run, so the group is legitimately empty during
// the walk. The route must be reported as PENDING - no failure, no claimed exit - and `Err()`, which
// is the start verdict, must be nil. Before the fix this walk produced FailureStructure
// "this route never reached its far end" and refused a legal configuration.
func TestAProviderFedEmptyGroupIsPendingAndNotRefused(t *testing.T) {
	registry, group, exit := newProviderBackedFixture(t, nil)

	report, err := ValidateRoots(registry.resolver(), []adapter.Outbound{exit}, nil,
		[]string{N.NetworkTCP, N.NetworkUDP}, Declarations{})
	require.NoError(t, err)

	require.NoError(t, report.Err(), "a route through a provider-fed group that has not published yet must not be a start failure")
	require.Empty(t, report.Failures)
	require.Len(t, report.Pending, 1, "the route is pending, not proven: the members arrive later")
	require.Equal(t, "g", report.Pending[0].Group)
	require.Equal(t, "exit", report.Pending[0].Root)

	require.False(t, report.Reachable(),
		"a pending route has no validated leaf, so the stronger 'every leaf was proven usable' question is false")
	for _, node := range report.Nodes {
		require.False(t, node.Exit,
			"a deferred route must not claim an exit; nodes=%v", describeChecks(report.Nodes))
	}
	_ = group
}

// TestAnEmptyGroupWithoutProviderFeedIsStillRefused is the discriminating control.
//
// The same shape with a group that NO provider feeds is a genuine declaration of an empty group, so
// the previous fatal verdict must stay. Without this test, suppressing every empty-group failure
// would satisfy the test above.
func TestAnEmptyGroupWithoutProviderFeedIsStillRefused(t *testing.T) {
	empty := &testGroup{
		tag:       "g",
		groupType: "selector",
		networks:  []string{N.NetworkTCP, N.NetworkUDP},
		lookup:    make(map[string]adapter.Outbound),
	}
	exit := dualLeaf("exit", "g")
	registry := newRegistry(exit, empty)

	report, err := ValidateRoots(registry.resolver(), []adapter.Outbound{exit}, nil,
		[]string{N.NetworkTCP}, Declarations{})
	require.NoError(t, err)

	require.Error(t, report.Err(),
		"a group with no members and no provider feed will never reach its far end; accepting it "+
			"starts a route that cannot carry a byte")
	require.Empty(t, report.Pending)
	require.Contains(t, report.Err().Error(), "never reached its far end")
}

// TestAProviderFedGroupWithACompleteMemberIsNotPending proves the deferral is about the ROUTE, not
// about the group: a group that already has a static member reaching a far end is validated
// normally, and the unvalidated provider part does not turn the root into a pending route.
func TestAProviderFedGroupWithACompleteMemberIsNotPending(t *testing.T) {
	member := dualLeaf("static-member")
	registry, group, exit := newProviderBackedFixture(t, []string{"static-member"})
	registry.objects["static-member"] = member
	group.selected = "static-member"

	report, err := ValidateRoots(registry.resolver(), []adapter.Outbound{exit}, nil,
		[]string{N.NetworkTCP, N.NetworkUDP}, Declarations{})
	require.NoError(t, err)
	require.NoError(t, report.Err())
	require.Empty(t, report.Pending, "this route reaches a far end through its static member")
	require.True(t, report.Reachable())
}

// TestAProviderFedGroupDoesNotMaskAMissingMember keeps the deferral narrow.
//
// The group names a provider, so the route is eligible for the pending verdict - but it also has a
// declared member that does not exist. A missing member is a structural defect and must still be
// reported; the pending path exists for "no member YET", not for "a member that can never resolve".
func TestAProviderFedGroupDoesNotMaskAMissingMember(t *testing.T) {
	registry, _, exit := newProviderBackedFixture(t, []string{"ghost"})

	report, err := ValidateRoots(registry.resolver(), []adapter.Outbound{exit}, nil,
		[]string{N.NetworkTCP}, Declarations{})
	require.NoError(t, err)
	require.Error(t, report.Err(), "an unresolvable declared member is fatal even in a provider-fed group")
	require.Empty(t, report.Pending)
}

// TestANestedProviderFedGroupIsStillPending covers the shape the first fix could miss: the empty
// provider-fed group is not the detour target itself but a member of a static group on the chain.
func TestANestedProviderFedGroupIsStillPending(t *testing.T) {
	inner := &providerBackedTestGroup{
		testGroup: &testGroup{
			tag:       "inner",
			groupType: "selector",
			networks:  []string{N.NetworkTCP, N.NetworkUDP},
			lookup:    make(map[string]adapter.Outbound),
		},
		providers: []string{"p"},
	}
	outer := &testGroup{
		tag:       "outer",
		groupType: "selector",
		members:   []string{"inner"},
		networks:  []string{N.NetworkTCP, N.NetworkUDP},
		lookup:    map[string]adapter.Outbound{"inner": inner},
		selected:  "inner",
	}
	exit := dualLeaf("exit", "outer")
	registry := newRegistry(exit, outer, inner)

	report, err := ValidateRoots(registry.resolver(), []adapter.Outbound{exit}, nil,
		[]string{N.NetworkTCP}, Declarations{})
	require.NoError(t, err)
	require.NoError(t, report.Err())
	require.Len(t, report.Pending, 1)
	require.Equal(t, "inner", report.Pending[0].Group)
	require.False(t, report.Reachable())
}
