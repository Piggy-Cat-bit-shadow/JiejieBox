package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/provider/parser"
	singjson "github.com/sagernet/sing/common/json"
)

// TestMixedFixtureIsDecodableByTheProduct is the guard the earlier fixture did not need: a mixed
// fixture names five real outbound kinds and two transports, and a typo in one of them would only
// surface as a failed child run 5 samples later. Decoding the document through the product's own
// decoder is the cheapest way to keep the fixture honest.
func TestMixedFixtureIsDecodableByTheProduct(t *testing.T) {
	spec, err := normalizeSpec(runSpec{
		Case: "ordinary", Nodes: 100, Groups: 2, NodeProto: "mixed",
		Activity: []string{"none"}, Idle: []string{"0s"}, GCMode: "forced",
		HeldFlows: 1, TrafficBytes: 1,
	})
	if err != nil {
		t.Fatalf("normalize a mixed spec: %v", err)
	}
	fixture := generateFixture(spec, 12345, "http://127.0.0.1:1/generate_204")
	if len(fixture.Document) == 0 {
		t.Fatal("the mixed fixture document is empty")
	}
	ctx := include.Context(context.Background())
	options, err := singjson.UnmarshalExtendedContext[option.Options](ctx, fixture.Document)
	if err != nil {
		t.Fatalf("decode the mixed document: %v", err)
	}
	if got := len(fixture.NodeTags); got != 100 {
		t.Fatalf("fixture records %d node tags, want 100", got)
	}
	kinds := make(map[string]int)
	var nodes int
	for _, outbound := range options.Outbounds {
		if len(outbound.Tag) < 5 || outbound.Tag[:5] != "node-" {
			continue
		}
		nodes++
		kinds[outbound.Type]++
	}
	if nodes != 100 {
		t.Fatalf("decoded %d nodes, want 100", nodes)
	}
	for _, kind := range mixedNodeKinds {
		if kinds[kind] != 20 {
			t.Errorf("kind %s has %d nodes, want 20 (the fixture's documented equal share)", kind, kinds[kind])
		}
	}
	// The WebSocket transport is the only nested transport variant, and its share is one VLESS in
	// three: 100 nodes -> 20 VLESS -> 6 with a transport (indices 7, 22, 37, 52, 67, 82).
	var websocket int
	for _, outbound := range options.Outbounds {
		if vless, ok := outbound.Options.(*option.VLESSOutboundOptions); ok && vless.Transport != nil {
			websocket++
			if vless.Transport.Type != "ws" {
				t.Errorf("VLESS transport type is %q, want ws", vless.Transport.Type)
			}
		}
	}
	if websocket != 6 {
		t.Errorf("%d VLESS nodes carry a transport, want 6", websocket)
	}
}

// TestProviderFixtureIsDecodableByTheProduct does the same for the provider shape: the configuration
// must decode with a local provider and a provider-fed group, and the node document must decode
// through the reader the local provider itself uses, with the rendered tag the group addresses.
func TestProviderFixtureIsDecodableByTheProduct(t *testing.T) {
	spec, err := normalizeSpec(runSpec{
		Case: "ordinary", Nodes: 50, Groups: 2, NodeProto: "mixed", ConfigSource: "provider",
		Activity: []string{"none"}, Idle: []string{"0s"}, GCMode: "forced",
		HeldFlows: 1, TrafficBytes: 1,
	})
	if err != nil {
		t.Fatalf("normalize a provider spec: %v", err)
	}
	fixture := generateFixtureWithProvider(spec, 12345, "http://127.0.0.1:1/generate_204", "C:/Temp/nodes.json")
	if len(fixture.ProviderDocument) == 0 {
		t.Fatal("the provider node document is empty")
	}
	ctx := include.Context(context.Background())
	options, err := singjson.UnmarshalExtendedContext[option.Options](ctx, fixture.Document)
	if err != nil {
		t.Fatalf("decode the provider configuration: %v", err)
	}
	if len(options.Providers) != 1 || options.Providers[0].Type != "local" {
		t.Fatalf("the configuration declares %d providers, want one local provider", len(options.Providers))
	}
	if len(options.Outbounds) != 3 {
		// The direct outbound plus two groups; a provider configuration that inlined its nodes would
		// carry 50 more and the difference would be invisible in the ledger's per-node slope.
		t.Fatalf("a provider configuration must not inline nodes; got %d outbounds", len(options.Outbounds))
	}

	outbounds, err := parser.ParseSubscription(ctx, string(fixture.ProviderDocument), nil, fixtureProviderTag)
	if err != nil {
		t.Fatalf("parse the provider node document: %v", err)
	}
	if len(outbounds) != 50 {
		t.Fatalf("the provider document parsed to %d nodes, want 50", len(outbounds))
	}
	// The reader returns the document's own tags. The provider renders them to "<provider>/<tag>"
	// inside UpdateNodes (Adapter.resolveOutboundTags), so the publish-time name is the one the
	// fixture records for a later stage to address.
	if outbounds[0].Tag != "node-00000" {
		t.Fatalf("parsed node tag is %q, want the document tag %q", outbounds[0].Tag, "node-00000")
	}
	if fixture.NodeTags[0] != fixtureProviderTag+"/node-00000" {
		t.Fatalf("fixture records rendered tag %q, want %q", fixture.NodeTags[0],
			fixtureProviderTag+"/node-00000")
	}
	if fixture.MemberEdges != 2+50 {
		t.Errorf("member edges = %d, want 2 provider edges + 50 node declarations", fixture.MemberEdges)
	}
}

// TestProviderFixtureGroupsNameTheProvider guards the shape that makes the provider case different
// from the static one: a group whose members come from a provider must name the provider, because an
// empty `outbounds` array with no `providers` is refused by the product at construction.
func TestProviderFixtureGroupsNameTheProvider(t *testing.T) {
	spec, err := normalizeSpec(runSpec{
		Case: "ordinary", Nodes: 10, Groups: 3, NodeProto: "mixed", ConfigSource: "provider",
		Activity: []string{"none"}, Idle: []string{"0s"}, GCMode: "forced",
		HeldFlows: 1, TrafficBytes: 1,
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	fixture := generateFixtureWithProvider(spec, 12345, "http://127.0.0.1:1/generate_204", "C:/Temp/nodes.json")
	var document struct {
		Outbounds []struct {
			Providers []string `json:"providers"`
			Outbounds []string `json:"outbounds"`
		} `json:"outbounds"`
	}
	if err = json.Unmarshal(fixture.Document, &document); err != nil {
		t.Fatalf("decode the provider configuration: %v", err)
	}
	groups := document.Outbounds[1:]
	if len(groups) != 3 {
		t.Fatalf("the configuration has %d groups, want 3", len(groups))
	}
	for index, group := range groups {
		if len(group.Providers) != 1 || group.Providers[0] != fixtureProviderTag {
			t.Errorf("group %d names providers %v, want [%s]", index, group.Providers, fixtureProviderTag)
		}
		if len(group.Outbounds) != 0 {
			t.Errorf("group %d names %d static members; a provider group must not duplicate them", index,
				len(group.Outbounds))
		}
	}
}

func TestProviderSourceNeedsNodes(t *testing.T) {
	_, err := normalizeSpec(runSpec{
		Case: "empty", Nodes: 0, Groups: 0, NodeProto: "direct", ConfigSource: "provider",
		Activity: []string{"none"}, Idle: []string{"0s"}, GCMode: "forced",
		HeldFlows: 1, TrafficBytes: 1,
	})
	if err == nil {
		t.Fatal("a provider configuration with zero nodes was accepted; the product refuses an empty node set")
	}
}
