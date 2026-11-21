package box_test

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
)

// trojanNodeConfig is one Trojan node the deferred path admits: TLS on with the std engine, no
// transport, no multiplexer, no bind or resolver field. Anything else falls back to the eager path
// by design, so a test that expects an expansion must use a node this shape.
func trojanNodeConfig(tag string) string {
	return `{"type":"trojan","tag":"` + tag + `","server":"203.0.113.1","server_port":443,` +
		`"password":"p","tls":{"enabled":true,"insecure":true}}`
}

// TestBoxInstallsDeferredOutboundsForEligibleTrojanNodes is the product-level half of GM-4: the seam
// is installed by box.New, metadata queries do not expand anything, and a first demand expands
// exactly one node. The manager-level measurement (TestMeasureBoundedProbe) proves the budget; this
// proves the box actually uses it, which is what "not just an unused switch" has to mean.
func TestBoxInstallsDeferredOutboundsForEligibleTrojanNodes(t *testing.T) {
	const nodeCount = 8
	outbounds := make([]string, 0, nodeCount+1)
	for index := 0; index < nodeCount; index++ {
		outbounds = append(outbounds, trojanNodeConfig(nodeTag(index)))
	}
	outbounds = append(outbounds, `{"type":"selector","tag":"group","outbounds":["`+nodeTag(0)+`","`+nodeTag(1)+`"]}`)
	instance := newBoxWithOutbounds(t, outbounds...)
	manager := instance.Outbound()

	if err := instance.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Starting a Box constructs every outbound. With the seam installed, an eligible node is a recipe
	// and nothing more, so not one expansion exists yet.
	if expanded := instance.DeferredExpansionCount(); expanded != 0 {
		t.Fatalf("Start expanded %d deferred node(s); construction must stay lazy", expanded)
	}

	// A management query is the path a UI takes: it must not build. Asking for the object and its
	// metadata is exactly what a provider reload, a group member listing and a graph validation do.
	for index := 0; index < nodeCount; index++ {
		loaded, ok := manager.Outbound(nodeTag(index))
		if !ok {
			t.Fatalf("outbound %s is missing", nodeTag(index))
		}
		_ = loaded.Type()
		_ = loaded.Network()
	}
	if expanded := instance.DeferredExpansionCount(); expanded != 0 {
		t.Fatalf("a metadata query expanded %d deferred node(s)", expanded)
	}

	// First demand: the build happens, the dial itself has nowhere to go and is expected to fail.
	loaded, ok := manager.Outbound(nodeTag(0))
	if !ok {
		t.Fatalf("outbound %s is missing", nodeTag(0))
	}
	dialContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, dialErr := loaded.DialContext(dialContext, "tcp", M.ParseSocksaddrHostPort("example.com", 443))
	if dialErr == nil {
		_ = conn.Close()
	}
	if expanded := instance.DeferredExpansionCount(); expanded != 1 {
		t.Fatalf("one demand expanded %d nodes, want 1", expanded)
	}
	if _, isDeferred := loaded.(adapter.Lifecycle); !isDeferred {
		t.Fatal("the eligible node was eagerly constructed: the seam did not reach this box")
	}
}

func nodeTag(index int) string {
	return "lazy-" + string(rune('a'+index))
}
