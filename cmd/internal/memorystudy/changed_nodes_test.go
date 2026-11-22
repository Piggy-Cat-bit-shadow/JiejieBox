package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestChangedNodesAxisKeepsTheHistoricalDocument pins the three properties the K axis needs to be
// usable as evidence, because the entire point of the field is that the OLD ledger's "every node
// changed" reading must not be re-labelled as "one node changed":
//
//   - nil keeps the historical document byte-for-byte (every node's port moves);
//   - K=0 produces exactly the published document (a refresh that changes nothing);
//   - K=1 differs in exactly the first node and nothing else;
//   - K=N is identical to the historical document.
//
// It compares decoded documents rather than raw bytes so a difference in the node COUNT cannot hide
// inside an otherwise plausible string comparison: the ports of every node are read back by tag.
func TestChangedNodesAxisKeepsTheHistoricalDocument(t *testing.T) {
	base := runSpec{Case: "ordinary", Nodes: 8, Groups: 1, NodeProto: "trojan", ConfigSource: "provider", GCMode: "forced", HeldFlows: 1, TrafficBytes: 1}
	spec, err := normalizeSpec(base)
	if err != nil {
		t.Fatalf("normalize the base spec: %v", err)
	}
	published, err := normalizeSpec(base)
	if err != nil {
		t.Fatalf("normalize the published spec: %v", err)
	}
	publishedDocument := generateProviderDocument(published, 0)
	if moved := movedPorts(t, publishedDocument, generateProviderDocument(published, 1)); len(moved) != published.Nodes {
		t.Fatalf("the historical document moved %d of %d nodes; the default is supposed to move every node", len(moved), published.Nodes)
	}

	tests := []struct {
		name    string
		changed *int
		offset  int
		// moved is the set of tags whose server_port differs from the published document.
		moved []string
	}{
		{name: "zero changes is the published document", changed: intPointer(0), offset: 1},
		{name: "one change moves the first node only", changed: intPointer(1), offset: 1, moved: []string{"node-00000"}},
		{name: "ten changes move the first ten nodes only", changed: intPointer(10), offset: 1,
			moved: []string{"node-00000", "node-00001", "node-00002", "node-00003", "node-00004",
				"node-00005", "node-00006", "node-00007"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec.ChangedNodes = test.changed
			document := generateProviderDocument(spec, test.offset)
			moved := movedPorts(t, publishedDocument, document)
			if len(moved) != len(test.moved) {
				t.Fatalf("moved %v, want %v", moved, test.moved)
			}
			for index, tag := range test.moved {
				if moved[index] != tag {
					t.Fatalf("moved %v, want %v", moved, test.moved)
				}
			}
		})
	}

	// K=N is the historical trigger: identical bytes, so an old and a new ledger can be compared
	// directly at K=N without attributing a fixture difference to the product.
	spec.ChangedNodes = intPointer(spec.Nodes)
	if got, want := generateProviderDocument(spec, 1), generateProviderDocument(published, 1); !bytes.Equal(got, want) {
		t.Fatalf("K=N is not the historical all-nodes-changed document")
	}
}

// movedPorts reports, in document order, the tags whose server_port differs between two documents.
func movedPorts(t *testing.T, before, after []byte) []string {
	t.Helper()
	type outboundDocument struct {
		Outbounds []struct {
			Tag        string  `json:"tag"`
			ServerPort float64 `json:"server_port"`
		} `json:"outbounds"`
	}
	decode := func(document []byte) outboundDocument {
		var parsed outboundDocument
		if err := json.Unmarshal(document, &parsed); err != nil {
			t.Fatalf("decode the provider document: %v", err)
		}
		return parsed
	}
	beforeDocument, afterDocument := decode(before), decode(after)
	if len(beforeDocument.Outbounds) != len(afterDocument.Outbounds) {
		t.Fatalf("the two documents carry %d and %d nodes", len(beforeDocument.Outbounds), len(afterDocument.Outbounds))
	}
	moved := make([]string, 0, len(beforeDocument.Outbounds))
	for index, outbound := range beforeDocument.Outbounds {
		if outbound.ServerPort != afterDocument.Outbounds[index].ServerPort {
			moved = append(moved, outbound.Tag)
		}
	}
	return moved
}

func intPointer(value int) *int { return &value }

// TestChangedNodesRejectsUselessCombinations guards the two ways the knob could silently mean
// something other than what the ledger says: a K on a static configuration (there is no changed
// document) and a K larger than the document (the fixture would clamp it).
func TestChangedNodesRejectsUselessCombinations(t *testing.T) {
	static := runSpec{Case: "ordinary", Nodes: 4, NodeProto: "trojan", ConfigSource: "static", GCMode: "forced", ChangedNodes: intPointer(1)}
	if _, err := normalizeSpec(static); err == nil {
		t.Fatal("a K axis on a static configuration was accepted")
	}
	tooLarge := runSpec{Case: "ordinary", Nodes: 4, NodeProto: "trojan", ConfigSource: "provider", GCMode: "forced", ChangedNodes: intPointer(5)}
	if _, err := normalizeSpec(tooLarge); err == nil {
		t.Fatal("a K larger than the node count was accepted")
	}
	negative := runSpec{Case: "ordinary", Nodes: 4, NodeProto: "trojan", ConfigSource: "provider", GCMode: "forced", ChangedNodes: intPointer(-1)}
	if _, err := normalizeSpec(negative); err == nil {
		t.Fatal("a negative K was accepted")
	}
}

// TestChangedNodesSurvivesTheChildCommandLine pins the plumbing the measurement actually uses: the
// parent reproduces the whole spec on the child's command line, so a K that is dropped there would
// make a run measure K=N while its ledger says K=1.
func TestChangedNodesSurvivesTheChildCommandLine(t *testing.T) {
	spec := runSpec{Case: "ordinary", Nodes: 4, Groups: 1, NodeProto: "trojan", ConfigSource: "provider", Activity: []string{"none"}, Idle: []string{"0s"}, PostIdle: "0s", GCMode: "forced", HeldFlows: 1, TrafficBytes: 1, ChangedNodes: intPointer(1)}
	arguments := childArguments(spec, "run-id")
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == "-changed-nodes" {
			if arguments[index+1] != "1" {
				t.Fatalf("-changed-nodes %s, want 1", arguments[index+1])
			}
			return
		}
	}
	t.Fatal("the child command line does not carry -changed-nodes")
}
