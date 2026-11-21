package outbound

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/sagernet/sing-box/adapter"
)

func TestGraphEdgesDoNotMutateTheOwnersDependencies(t *testing.T) {
	node := &graphNode{tag: "n", dependencies: []string{"a", "a", "b"}}
	edges := outboundGraphEdges(node, nil)
	if !reflect.DeepEqual(edges, []string{"a", "b"}) {
		t.Fatalf("edges changed: %v", edges)
	}
	edges[0] = "changed"
	if !reflect.DeepEqual(node.dependencies, []string{"a", "a", "b"}) {
		t.Fatalf("borrowed dependencies were modified: %v", node.dependencies)
	}
	group := &graphGroup{tag: "g", members: []string{"old"}, selected: "old"}
	if edges := outboundGraphEdges(group, []string{}); len(edges) != 0 {
		t.Fatalf("empty prospective membership retained old references: %v", edges)
	}
}

func independentGraphFixture() map[string]adapter.Outbound {
	nodes := make(map[string]adapter.Outbound, 1000)
	for i := 0; i < 1000; i++ {
		tag := fmt.Sprint("node-", i)
		nodes[tag] = &graphNode{tag: tag}
	}
	return nodes
}

func TestIndependentGraphHasNoPerNodeScratchAllocation(t *testing.T) {
	nodes := independentGraphFixture()
	allocations := testing.AllocsPerRun(10, func() {
		if err := validateAcyclicOutboundGraph(nodes, nil); err != nil {
			t.Fatal(err)
		}
	})
	// Publishing N independent nodes used N temporary edge slices per
	// validation, and repeated validation made a provider update quadratic in
	// allocations. Allow traversal bookkeeping, not a scratch object per node.
	if allocations > 16 {
		t.Fatalf("1000 independent nodes need %.0f scratch allocations, want <=16", allocations)
	}
}

func BenchmarkIndependentOutboundGraph(b *testing.B) {
	nodes := independentGraphFixture()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := validateAcyclicOutboundGraph(nodes, nil); err != nil {
			b.Fatal(err)
		}
	}
}
