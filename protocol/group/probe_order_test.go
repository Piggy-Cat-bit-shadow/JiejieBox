package group

// P02A — the order an automatic health round walks its members in.
//
// The round used to submit members in configuration order: identical every round, with
// no place for the member live traffic is on and no rotation for a large provider. The
// order is a separate, pure decision (orderProbeMembers), so it is pinned here without
// timing: the probe batch's ten workers make the observed dial order a scheduling
// artifact, while the submission order is the contract the round relies on.

import (
	"testing"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

// orderMember is a minimal adapter.Outbound for the ordering decision: only Tag is read.
type orderMember struct {
	adapter.Outbound
	tag string
}

func (m *orderMember) Type() string { return "order-test" }
func (m *orderMember) Tag() string  { return m.tag }

func orderMembers(tags ...string) []adapter.Outbound {
	members := make([]adapter.Outbound, 0, len(tags))
	for _, tag := range tags {
		members = append(members, &orderMember{tag: tag})
	}
	return members
}

func tagsOf(members []adapter.Outbound) []string {
	tags := make([]string, 0, len(members))
	for _, member := range members {
		tags = append(tags, member.Tag())
	}
	return tags
}

// freshEvidence builds a has-evidence predicate over the given tags.
func freshEvidence(tags ...string) func(string) bool {
	fresh := make(map[string]bool, len(tags))
	for _, tag := range tags {
		fresh[tag] = true
	}
	return func(tag string) bool { return fresh[tag] }
}

// TestTheSelectedMemberIsProbedFirst: live traffic depends on that member's evidence.
func TestTheSelectedMemberIsProbedFirst(t *testing.T) {
	members := orderMembers("a", "b", "c")
	ordered, _ := orderProbeMembers(members, "c", freshEvidence("a", "b", "c"), "")
	require.Equal(t, []string{"c", "a", "b"}, tagsOf(ordered))
}

// TestMembersWithoutEvidenceGoBeforeStaleOnes: untested and previously-failed members
// both have no health entry, and both need a measurement more than a fresh one does.
func TestMembersWithoutEvidenceGoBeforeStaleOnes(t *testing.T) {
	members := orderMembers("fresh1", "untested1", "fresh2", "untested2")
	ordered, _ := orderProbeMembers(members, "", freshEvidence("fresh1", "fresh2"), "")
	require.Equal(t, []string{"untested1", "untested2", "fresh1", "fresh2"}, tagsOf(ordered),
		"a member with no evidence must be measured before members whose evidence is merely stale")
}

// TestTheStaleTailRotatesAcrossRounds: over successive rounds the tail starts at every
// member exactly once, so a large provider is not always walked from the same head.
func TestTheStaleTailRotatesAcrossRounds(t *testing.T) {
	members := orderMembers("a", "b", "c")
	anchor := ""
	firstTags := make([]string, 0, 3)
	for round := 0; round < 3; round++ {
		ordered, next := orderProbeMembers(members, "", freshEvidence("a", "b", "c"), anchor)
		require.Len(t, ordered, 3, "every stale member is still submitted; the order only rotates")
		firstTags = append(firstTags, ordered[0].Tag())
		anchor = next
	}
	require.Equal(t, []string{"a", "b", "c"}, firstTags,
		"three rounds must start at three different members and return to the first")
}

// TestTheRotationReanchorsWhenTheAnchorDisappears pins the membership-change case: the
// anchor is a tag, so a removed member re-anchors the rotation at the top instead of
// shifting every index onto a different member.
func TestTheRotationReanchorsWhenTheAnchorDisappears(t *testing.T) {
	ordered, next := orderProbeMembers(orderMembers("a", "c"), "", freshEvidence("a", "c"), "b")
	require.Equal(t, []string{"a", "c"}, tagsOf(ordered))
	require.Equal(t, "a", next, "a missing anchor restarts the tail at the top of the remaining order")

	// And a member that appears later joins the tail without breaking the order.
	ordered, _ = orderProbeMembers(orderMembers("a", "b", "c", "d"), "", freshEvidence("a", "b", "c", "d"), "c")
	require.Equal(t, []string{"d", "a", "b", "c"}, tagsOf(ordered))
}

// TestASelectedMemberWithNoEvidenceIsNotDuplicated: the selected member is emitted once,
// even though it also has no evidence and would otherwise appear in the untested group.
func TestASelectedMemberWithNoEvidenceIsNotDuplicated(t *testing.T) {
	ordered, _ := orderProbeMembers(orderMembers("a", "b"), "a", freshEvidence(), "")
	require.Equal(t, []string{"a", "b"}, tagsOf(ordered))
}

// TestShortAndNilMemberListsAreReturnedAsIs keeps the fast path honest.
func TestShortAndNilMemberListsAreReturnedAsIs(t *testing.T) {
	single := orderMembers("only")
	ordered, next := orderProbeMembers(single, "only", nil, "")
	require.Equal(t, []string{"only"}, tagsOf(ordered))
	require.Empty(t, next)

	ordered, next = orderProbeMembers(nil, "", nil, "")
	require.Empty(t, ordered)
	require.Empty(t, next)
}
