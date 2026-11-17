package parser_test

// P2 — negative and boundary tests for the preview entry, at the layer where the behaviour lives.
//
// These are red/green tests: each one asserts a semantic the client depends on, so a regression
// fails here instead of surfacing as a wrong list in the UI.

import (
	"context"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/provider/parser"
	singjson "github.com/sagernet/sing/common/json"

	"github.com/stretchr/testify/require"
)

func p2Context() context.Context {
	return include.Context(context.Background())
}

// TestP2PreviewHonoursCancellation pins the one bound the core can offer on an arbitrarily large
// document: a caller that cancels stops the work and is TOLD the result is partial. Without this,
// a "cancel" in the UI would keep a CPU core busy on a document nobody is waiting for.
func TestP2PreviewHonoursCancellation(t *testing.T) {
	var builder strings.Builder
	for index := 0; index < 5000; index++ {
		builder.WriteString("trojan://password@192.0.2.10:443?sni=example.com#Node-")
		builder.WriteString(p2Number(index))
		builder.WriteString("\n")
	}
	document := builder.String()

	cancelledContext, cancel := context.WithCancel(p2Context())
	cancel()

	preview := parser.ParseSubscriptionPreview(cancelledContext, document)
	require.True(t, preview.Cancelled, "a cancelled context must be reported, not silently ignored")
	require.Less(t, len(preview.Outbounds), 5000, "cancellation must stop the work early")
	require.Equal(t, parser.PreviewFormatLinks, preview.Format)

	// A live context parses the whole document: cancellation must not be a permanent switch.
	full := parser.ParseSubscriptionPreview(p2Context(), document)
	require.False(t, full.Cancelled)
	require.Len(t, full.Outbounds, 5000)
}

// TestP2PreviewDetourChainInsideOneDocument pins what a subscription-internal chain becomes after
// the provider namespaces it: a sibling detour is rewritten to "<provider>/<tag>", a detour to a
// node that is NOT in the document is cleared, and a cycle is left as written (the provider cannot
// know whether the cycle is intentional; the core's construction is what rejects it later).
func TestP2PreviewDetourChainInsideOneDocument(t *testing.T) {
	ctx := p2Context()

	chained := `{"outbounds":[
      {"type":"trojan","tag":"leaf","server":"192.0.2.10","server_port":443,"password":"p"},
      {"type":"trojan","tag":"mid","server":"192.0.2.11","server_port":443,"password":"p","detour":"leaf"},
      {"type":"selector","tag":"top","outbounds":["mid"]}
    ]}`
	outbounds, err := parser.ParseSubscription(ctx, chained, nil, "sub")
	require.NoError(t, err)
	byTag := map[string]string{}
	for index := range outbounds {
		encoded, marshalErr := singjson.MarshalContext(ctx, &outbounds[index])
		require.NoError(t, marshalErr)
		byTag[outbounds[index].Tag] = string(encoded)
	}
	// mid's detour must be namespaced to the provider, not left as the bare sibling tag.
	require.Contains(t, byTag["mid"], `"detour":"sub/leaf"`)
	// The selector is a policy object: it is filtered out of a provider document, so "top" must
	// not appear as a node.
	require.NotContains(t, byTag, "top")

	// A detour to a tag that is not in the document is cleared rather than left dangling.
	external := `{"outbounds":[
      {"type":"trojan","tag":"only","server":"192.0.2.10","server_port":443,"password":"p","detour":"not-here"}
    ]}`
	externalOutbounds, err := parser.ParseSubscription(ctx, external, nil, "sub")
	require.NoError(t, err)
	require.Len(t, externalOutbounds, 1)
	encoded, err := singjson.MarshalContext(ctx, &externalOutbounds[0])
	require.NoError(t, err)
	require.NotContains(t, string(encoded), `"detour"`)

	// A cycle is preserved as written: A->B, B->A. The preview must not invent a fix, and it must
	// not hang.
	cycle := `{"outbounds":[
      {"type":"trojan","tag":"a","server":"192.0.2.10","server_port":443,"password":"p","detour":"b"},
      {"type":"trojan","tag":"b","server":"192.0.2.11","server_port":443,"password":"p","detour":"a"}
    ]}`
	cycleOutbounds, err := parser.ParseSubscription(ctx, cycle, nil, "sub")
	require.NoError(t, err)
	require.Len(t, cycleOutbounds, 2)
}

// TestP2PreviewEndpointIsSeparatedAndNotANode pins that an endpoint-shaped document never appears
// in the outbound list, which is what makes "provider endpoints are a separate step" honest.
func TestP2PreviewEndpointIsSeparatedAndNotANode(t *testing.T) {
	document := `{"endpoints":[{"type":"wireguard","tag":"wg","address":["10.0.0.2/32"],"private_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","peers":[{"address":"192.0.2.10","port":51820,"public_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}]}]}`
	preview := parser.ParseSubscriptionPreview(p2Context(), document)
	require.Empty(t, preview.Outbounds, "an endpoint must never be reported as an outbound")
	require.False(t, preview.Recognised(), "today the reader refuses an endpoints-only document (BUGS B-01)")
}

// TestP2PreviewDuplicateTagsAreNotDeduplicated pins the fact the HOST is responsible for: the
// parser reports what the document says, including two nodes with the same tag. De-duplication
// belongs to the profile writer, because only it knows the surrounding profile's existing tags.
func TestP2PreviewDuplicateTagsAreNotDeduplicated(t *testing.T) {
	document := "trojan://p@192.0.2.10:443#Same\ntrojan://p@192.0.2.11:443#Same"
	preview := parser.ParseSubscriptionPreview(p2Context(), document)
	require.True(t, preview.Recognised())
	require.Len(t, preview.Outbounds, 2)
	require.Equal(t, "Same", preview.Outbounds[0].Tag)
	require.Equal(t, "Same", preview.Outbounds[1].Tag)
}

// p2Number renders a small non-negative integer for fixtures.
func p2Number(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [8]byte
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[position:])
}
