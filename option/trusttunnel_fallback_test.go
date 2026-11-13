package option_test

// quic_fallback_to_h2 is an opt-in field on one outbound type: it decodes when present, stays absent
// when not used, and the generated schema offers it on exactly that type. The behaviour it selects is
// pinned in transport/trusttunnel/client_fallback_test.go and protocol/trusttunnel/outbound_test.go;
// this file pins the configuration surface.

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/schema"
	singjson "github.com/sagernet/sing/common/json"

	"github.com/stretchr/testify/require"
)

const trustTunnelFallbackProfile = `{
  "outbounds": [
    {
      "type": "trusttunnel",
      "tag": "fallback-on",
      "server": "vpn.example.com",
      "server_port": 443,
      "username": "user",
      "password": "secret",
      "quic": true,
      "quic_fallback_to_h2": true,
      "tls": {"enabled": true, "server_name": "vpn.example.com"}
    },
    {
      "type": "trusttunnel",
      "tag": "fallback-absent",
      "server": "vpn.example.com",
      "server_port": 443,
      "username": "user",
      "password": "secret",
      "quic": true,
      "tls": {"enabled": true, "server_name": "vpn.example.com"}
    }
  ]
}`

func TestTrustTunnelFallbackDecodesAndStaysAbsent(t *testing.T) {
	options, err := decodeOptions(t, trustTunnelFallbackProfile)
	require.NoError(t, err)
	require.Len(t, options.Outbounds, 2)

	enabled := options.Outbounds[0].Options.(*option.TrustTunnelOutboundOptions)
	require.True(t, enabled.QUICFallbackToH2, "the opt-in must reach the option the outbound reads")
	require.True(t, enabled.QUIC)

	absent := options.Outbounds[1].Options.(*option.TrustTunnelOutboundOptions)
	require.False(t, absent.QUICFallbackToH2, "an unused field must stay false, not inherit anything")

	// `omitempty`: a configuration that never enabled it is written back without the key.
	content, err := singjson.MarshalContext(context.Background(), options)
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(content, &document))
	outbounds := document["outbounds"].([]any)
	enabledRaw, _ := json.Marshal(outbounds[0])
	absentRaw, _ := json.Marshal(outbounds[1])
	require.Contains(t, string(enabledRaw), `"quic_fallback_to_h2":true`)
	require.NotContains(t, string(absentRaw), "quic_fallback_to_h2")
}

// TestTheGeneratedSchemaOffersTheFallbackOnTrustTunnelOnly pins the generated document: an editor that
// reads the schema must offer the key where the decoder accepts it, and nowhere else.
func TestTheGeneratedSchemaOffersTheFallbackOnTrustTunnelOnly(t *testing.T) {
	generated, err := schema.Generate(include.Context(context.Background()), reflect.TypeFor[option.Options]())
	require.NoError(t, err)

	var document any
	require.NoError(t, json.Unmarshal(generated, &document))

	found, declared := findVariantProperty(t, document, "trusttunnel", "quic_fallback_to_h2")
	require.True(t, found, "the generated schema has no trusttunnel variant carrying quic_fallback_to_h2")
	require.Equal(t, "boolean", declared)

	onDirect, _ := findVariantProperty(t, document, "direct", "quic_fallback_to_h2")
	require.False(t, onDirect, "the key must not be offered on an unrelated outbound type")
}

// findVariantProperty walks the generated document for a polymorphic variant whose declared type is
// declaredType, and reports whether that variant's properties carry property and its declared type.
func findVariantProperty(t *testing.T, node any, declaredType string, property string) (bool, string) {
	t.Helper()
	switch typed := node.(type) {
	case map[string]any:
		if properties, ok := typed["properties"].(map[string]any); ok {
			if typeProperty, ok := properties["type"].(map[string]any); ok {
				if declared, ok := typeProperty["const"].(string); ok {
					if declared == declaredType {
						entry, ok := properties[property].(map[string]any)
						if !ok {
							return false, ""
						}
						declaredPropertyType, _ := entry["type"].(string)
						return true, declaredPropertyType
					}
				}
			}
		}
		for _, value := range typed {
			if found, declared := findVariantProperty(t, value, declaredType, property); found {
				return found, declared
			}
		}
	case []any:
		for _, value := range typed {
			if found, declared := findVariantProperty(t, value, declaredType, property); found {
				return found, declared
			}
		}
	}
	return false, ""
}
