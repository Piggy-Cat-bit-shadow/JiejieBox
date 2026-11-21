package debugcombat

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// P22: graph/index optimisation x reload.
//
// The invariant under attack: the graph the runtime actually WALKS - declared dependencies UNION
// published group membership UNION Referrer references - must stay acyclic after every mutation
// that can add an edge, and a refused mutation must leave the previous published objects and the
// routing decision exactly as they were. The manager is production code and is reached through its
// exported verbs; only the objects are stand-ins.

// TestXP22ReplaceRollbackKeepsOldObjectServing is the provider-reload shape the audit measured:
// provider p publishes `p/a` with `detour: g`, while selector `g` names provider p and therefore
// selects `p/a`. `g` was constructed with no members, so the start-order sort saw no cycle; the
// cycle becomes reachable only when the membership and the node's declared detour are combined.
// The replacement must be refused BEFORE the swap, and the old object must keep serving.
func TestXP22ReplaceRollbackKeepsOldObjectServing(t *testing.T) {
	manager := newStubManager(t)
	group := newStubGroup("g")
	published := newStubOutbound("p/a")
	publish(t, manager, group)
	publish(t, manager, published)

	committed := 0
	if err := manager.UpdateGroupMembership("g", []string{"p/a"}, func() { group.publishMembers([]string{"p/a"}); committed++ }); err != nil {
		t.Fatalf("publishing membership []p/a must be accepted: %v", err)
	}
	if committed != 1 {
		t.Fatalf("commit must run exactly once, ran %d", committed)
	}

	// The provider now redefines p/a with a detour into the group that selects it.
	replacement := newStubOutbound("p/a", "g")
	err := replaceWith(t, manager, replacement)
	if err == nil {
		t.Fatalf("a replacement that closes g -> p/a -> g was accepted; the runtime would recurse on the first dial")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Fatalf("refusal must name the cycle, got %v", err)
	}
	if !referencedBy(t, manager, "p/a", published) {
		t.Fatalf("a refused replacement changed the published node: the tag no longer resolves to the old object")
	}
	if published.CloseCount() != 0 {
		t.Fatalf("a refused replacement closed the old object %d time(s); in-flight dials would see net.ErrClosed", published.CloseCount())
	}
	if replacement.CloseCount() != 1 {
		t.Fatalf("the refused replacement must be released exactly once, closed %d time(s)", replacement.CloseCount())
	}
	if err := manager.Remove("p/a"); err == nil {
		t.Fatalf("Remove must refuse a tag its group still names")
	}
	if !referencedBy(t, manager, "p/a", published) {
		t.Fatalf("a refused Remove unlinked the node")
	}
}

// TestXP22CrossGroupCycleIsRefusedWithoutRecursion publishes two groups whose union would close a
// cycle and requires the membership update to be refused promptly - a validator that recursed
// would hang here instead of returning an error.
func TestXP22CrossGroupCycleIsRefusedWithoutRecursion(t *testing.T) {
	manager := newStubManager(t)
	inner := newStubGroup("g-inner")
	outer := newStubGroup("g-outer", "g-inner")
	publish(t, manager, inner)
	publish(t, manager, outer)

	done := make(chan error, 1)
	committed := 0
	started := time.Now()
	go func() {
		done <- manager.UpdateGroupMembership("g-inner", []string{"g-outer"}, func() { committed++ })
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("g-inner -> g-outer -> g-inner was accepted")
		}
		if !strings.Contains(err.Error(), "circular") {
			t.Fatalf("refusal must name the cycle, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("membership validation did not return in 5s: the walk is unbounded")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cycle detection over 2 groups took %s", elapsed)
	}
	if committed != 0 {
		t.Fatalf("commit ran for a refused membership")
	}
	if got := inner.All(); len(got) != 0 {
		t.Fatalf("a refused membership update republished the group: %v", got)
	}
	// The refused edge must not have been committed anywhere: dropping the other direction is legal.
	if err := manager.UpdateGroupMembership("g-outer", nil, func() { outer.publishMembers(nil) }); err != nil {
		t.Fatalf("clearing a membership must stay legal: %v", err)
	}
}

// TestXP22ReferrerReferencesAreEdges covers the third half of the edge set: an object that only
// publishes References() still contributes edges, and a cycle that exists only through them must
// be refused.
func TestXP22ReferrerReferencesAreEdges(t *testing.T) {
	manager := newStubManager(t)
	group := newStubGroup("g")
	referrer := newStubOutbound("r")
	referrer.references = []string{"g"}
	publish(t, manager, group)
	publish(t, manager, referrer)

	err := manager.UpdateGroupMembership("g", []string{"r"}, func() {})
	if err == nil {
		t.Fatalf("g -> r (membership) -> g (references) was accepted")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Fatalf("refusal must name the cycle, got %v", err)
	}
	if got := group.All(); len(got) != 0 {
		t.Fatalf("a refused membership update republished the group: %v", got)
	}
}

// TestXP22DeepChainCycleIsRefusedPromptly makes the cycle long, so the walk has to visit the whole
// chain rather than find a short loop, and requires the diagnostic to stay bounded.
func TestXP22DeepChainCycleIsRefusedPromptly(t *testing.T) {
	const depth = 1500
	manager := newStubManager(t)
	chain := make([]*stubOutbound, depth)
	for index := range chain {
		tag := fmt.Sprintf("n%d", index)
		if index == depth-1 {
			chain[index] = newStubOutbound(tag)
		} else {
			chain[index] = newStubOutbound(tag, fmt.Sprintf("n%d", index+1))
		}
		publish(t, manager, chain[index])
	}
	// Close the chain: n0 -> n1 -> ... -> n(depth-1) -> n0.
	closing := newStubOutbound(fmt.Sprintf("n%d", depth-1), "n0")
	started := time.Now()
	err := replaceWith(t, manager, closing)
	elapsed := time.Since(started)
	if err == nil {
		t.Fatalf("a %d-node cycle was accepted", depth)
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Fatalf("refusal must name the cycle, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("validating a %d-node chain took %s", depth, elapsed)
	}
	if !referencedBy(t, manager, "n0", chain[0]) {
		t.Fatalf("a refused deep replacement changed the published head")
	}
	if chain[0].CloseCount() != 0 {
		t.Fatalf("a refused deep replacement closed the published head")
	}
}

// TestXP22SuccessfulReplaceRoutesToTheNewObject is the control arm: a legal replacement must be
// committed, must close the object it replaced exactly once, and every consumer that resolves the
// tag afterwards must get the new object - not the retired one.
func TestXP22SuccessfulReplaceRoutesToTheNewObject(t *testing.T) {
	manager := newStubManager(t)
	old := newStubOutbound("x")
	publish(t, manager, old)
	fresh := newStubOutbound("x")
	if err := replaceWith(t, manager, fresh); err != nil {
		t.Fatalf("a legal replacement was refused: %v", err)
	}
	if !referencedBy(t, manager, "x", fresh) {
		t.Fatalf("the tag still routes to the replaced object")
	}
	if old.CloseCount() != 1 {
		t.Fatalf("the replaced object must be closed exactly once, closed %d time(s)", old.CloseCount())
	}
	// A membership that names the tag resolves through the manager to the new object, so a group
	// cannot keep routing to the retired one.
	group := newStubGroup("g", "x")
	publish(t, manager, group)
	if got, loaded := manager.Outbound("x"); !loaded || got != fresh {
		t.Fatalf("after a reload the tag resolves to %v, want the new object", got)
	}
	if err := manager.Remove("x"); err == nil {
		t.Fatalf("Remove must refuse a tag the group still names")
	}
	if err := manager.UpdateGroupMembership("g", nil, func() { group.publishMembers(nil) }); err != nil {
		t.Fatalf("clearing the membership must stay legal: %v", err)
	}
	if err := manager.Remove("x"); err != nil {
		t.Fatalf("Remove must succeed once nothing names the tag: %v", err)
	}
	if fresh.CloseCount() != 1 {
		t.Fatalf("the removed object must be closed exactly once, closed %d time(s)", fresh.CloseCount())
	}
	if _, loaded := manager.Outbound("x"); loaded {
		t.Fatalf("a removed tag is still published")
	}
	_ = context.Background()
}
