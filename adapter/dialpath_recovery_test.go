package adapter

// The dial-path record's RECOVERY projection: one group handing the same dial to a second member.
//
// The runner cannot observe this after the fact - reading a group's current selection describes a
// path the request never took - so the fact has to come from the steps the decision points wrote
// while the dial ran. These cases pin the shape that makes it unambiguous: the SAME group on
// adjacent steps with a DIFFERENT member. Two nested groups, one group choosing the same member
// twice, or a step with no chooser must not read as a recovery.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestARecoveryIsTheSameGroupHandingTheDialToASecondMember(t *testing.T) {
	group, from, to, recovered := DialPathResult{Steps: []DialPathStep{
		{Group: "lb", Tag: "node-a", Revision: 3, HasRevision: true},
		{Group: "lb", Tag: "node-b", Revision: 3, HasRevision: true},
	}}.Recovery()
	require.True(t, recovered)
	require.Equal(t, "lb", group)
	require.Equal(t, "node-a", from)
	require.Equal(t, "node-b", to)
}

func TestTwoNestedGroupsAreNotARecovery(t *testing.T) {
	_, _, _, recovered := DialPathResult{Steps: []DialPathStep{
		{Group: "outer", Tag: "inner"},
		{Group: "inner", Tag: "node-a"},
	}}.Recovery()
	require.False(t, recovered,
		"a chain of two groups is one decision each, not one group changing its mind")
}

func TestOneMemberChosenTwiceIsNotARecovery(t *testing.T) {
	_, _, _, recovered := DialPathResult{Steps: []DialPathStep{
		{Group: "lb", Tag: "node-a"},
		{Group: "lb", Tag: "node-a"},
	}}.Recovery()
	require.False(t, recovered)
}

func TestAStepWithoutAChooserIsNotARecovery(t *testing.T) {
	_, _, _, recovered := DialPathResult{Steps: []DialPathStep{
		{Tag: "node-a"},
		{Tag: "node-b"},
	}}.Recovery()
	require.False(t, recovered,
		"two direct steps say nothing about who chose them, so nothing may be claimed")
}

func TestAnEmptyRecordHasNoRecovery(t *testing.T) {
	_, _, _, recovered := DialPathResult{}.Recovery()
	require.False(t, recovered)
}

// TestTheRecordedStepNamesTheChoosingGroup is the producer half: RecordDialStep must write the
// group's tag, or every recovery assertion above would be unreachable in production.
func TestTheRecordedStepNamesTheChoosingGroup(t *testing.T) {
	chooser := &dialPathTestGroup{tag: "lb", revision: 7}
	member := &dialPathTestGroup{tag: "node-b"}
	path := NewDialPath()
	path.BeginDial()
	RecordDialStep(WithDialPath(context.Background(), path), chooser, member)

	steps := path.Snapshot()
	require.Len(t, steps, 1)
	require.Equal(t, "lb", steps[0].Group)
	require.Equal(t, "node-b", steps[0].Tag)
	require.True(t, steps[0].HasRevision)
	require.EqualValues(t, 7, steps[0].Revision)

	// A caller that records without a chooser keeps the old behaviour: the tag is recorded and the
	// group is empty, which is "nobody said who chose this", not a guess.
	path.Record("direct", 0, false)
	steps = path.Snapshot()
	require.Len(t, steps, 2)
	require.Empty(t, steps[1].Group)
	require.Equal(t, "direct", steps[1].Tag)
}

// dialPathTestGroup is the minimum an object needs to be recorded as a chooser.
type dialPathTestGroup struct {
	Outbound
	tag      string
	revision uint64
}

func (g *dialPathTestGroup) Tag() string              { return g.tag }
func (g *dialPathTestGroup) DialPathRevision() uint64 { return g.revision }
