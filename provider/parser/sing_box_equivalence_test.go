package parser

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	singjson "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badjson"
	"github.com/sagernet/sing/service"
)

// TestParseBoxSubscriptionMatchesTheWholeDocumentReader is the equivalence proof for the streaming
// rewrite of the native subscription reader.
//
// It keeps a frozen copy of the previous implementation (the generic JSON tree, the filtered
// re-serialisation and the whole-document typed decode) and runs the same corpus through both. The
// corpus is deliberately hostile: a subscription is remote input, so "accepts the same documents,
// produces the same values, rejects the same documents" is the contract, and the previous reader is
// the only reference that can establish it.
//
// The compared facts are: success versus failure, the exact decoded outbounds and endpoints, and
// the exact error text for every case that is a structural or option-level rejection. The remaining
// cases are listed in errorTextMayDiffer with the reason each one is a diagnostic difference only;
// the test still requires both readers to reject them.
func TestParseBoxSubscriptionMatchesTheWholeDocumentReader(t *testing.T) {
	t.Parallel()
	ctx := probeContext()
	cases := []struct {
		name    string
		content string
	}{
		{"empty", ``},
		{"whitespace", " \n\t "},
		{"not json", `hello`},
		{"top level array", `[]`},
		{"top level string", `"outbounds"`},
		{"top level number", `42`},
		{"top level null", `null`},
		{"broken json", `{"outbounds": [`},
		{"outbounds null", `{"outbounds": null}`},
		{"outbounds string", `{"outbounds": "vless://x"}`},
		{"outbounds object", `{"outbounds": {}}`},
		{"outbounds number", `{"outbounds": 5}`},
		{"outbounds bool", `{"outbounds": true}`},
		{"null element", `{"outbounds": [null]}`},
		{"string element", `{"outbounds": ["vless://x"]}`},
		{"number element", `{"outbounds": [1]}`},
		{"array element", `{"outbounds": [[]]}`},
		{"object element", `{"outbounds": [{}]}`},
		{"object without type", `{"outbounds": [{"tag": "a"}]}`},
		{"type not a string", `{"outbounds": [{"type": 5}]}`},
		{"type null", `{"outbounds": [{"type": null}]}`},
		{"type array", `{"outbounds": [{"type": []}]}`},
		{"type object", `{"outbounds": [{"type": {}}]}`},
		{"unknown type", `{"outbounds": [{"type": "does-not-exist", "tag": "a"}]}`},
		{"missing required fields", `{"outbounds": [{"type": "vless", "tag": "a"}]}`},
		{"mixed valid and invalid", `{"outbounds": [{"type": "block", "tag": "ok"}, {"type": "vless"}]}`},
		{"duplicate json keys", `{"outbounds": [], "outbounds": [{"type": "block", "tag": "a"}]}`},
		{"duplicate outbounds with node", `{"outbounds": [{"type": "trojan", "tag": "a"}], "outbounds": [{"type": "trojan", "tag": "b"}]}`},
		{"valid trojan", `{"outbounds": [{"type": "trojan", "tag": "a", "server": "203.0.113.1", "server_port": 443, "password": "p"}]}`},
		{"valid trojan tls", `{"outbounds": [{"type": "trojan", "tag": "a", "server": "203.0.113.1", "server_port": 443, "password": "p", "tls": {"enabled": true, "insecure": true}}]}`},
		{"group then node", `{"outbounds": [{"type": "selector", "tag": "g", "outbounds": ["a"]}, {"type": "trojan", "tag": "a", "server": "203.0.113.1", "server_port": 443, "password": "p"}]}`},
		{"selector with hostile payload", `{"outbounds": [{"type": "selector", "tag": "g", "outbounds": 5}, {"type": "trojan", "tag": "a", "server": "203.0.113.1", "server_port": 443, "password": "p"}]}`},
		{"dropped types only", `{"outbounds": [{"type": "direct", "tag": "d"}, {"type": "block", "tag": "b"}, {"type": "urltest", "tag": "u", "outbounds": ["d"]}]}`},
		{"unknown top level key", `{"log": {"level": "debug"}, "outbounds": [{"type": "trojan", "tag": "a", "server": "203.0.113.1", "server_port": 443, "password": "p"}]}`},
		{"empty outbounds", `{"outbounds": []}`},
		{"empty outbounds with empty endpoints", `{"outbounds": [], "endpoints": []}`},
		{"endpoints empty only", `{"endpoints": []}`},
		{"endpoints null", `{"outbounds": [{"type": "trojan", "tag": "a", "server": "203.0.113.1", "server_port": 443, "password": "p"}], "endpoints": null}`},
		{"endpoints non empty", `{"outbounds": [], "endpoints": [{"type": "wireguard", "tag": "wg"}]}`},
		{"endpoints not array", `{"outbounds": [], "endpoints": 5}`},
		{"comments", "// leading\n{\"outbounds\": [{\"type\": \"trojan\", \"tag\": \"a\", \"server\": \"203.0.113.1\", \"server_port\": 443, \"password\": \"p\"}] // trailing\n}"},
		{"port float", `{"outbounds": [{"type": "trojan", "tag": "a", "server": "203.0.113.1", "server_port": 443.5, "password": "p"}]}`},
		{"duplicate type key", `{"outbounds": [{"type": "selector", "type": "block", "tag": "a"}]}`},
		{"duplicate type key reversed", `{"outbounds": [{"type": "block", "type": "trojan", "tag": "a"}]}`},
		{"unknown option field", `{"outbounds": [{"type": "trojan", "tag": "a", "server": "203.0.113.1", "server_port": 443, "password": "p", "unknown_field": 1}]}`},
		{"nested port type", `{"outbounds": [{"type": "trojan", "server_port": "x"}]}`},
		{"nested tls type", `{"outbounds": [{"type": "trojan", "tls": {"enabled": "x"}}]}`},
		{"nested tls unknown", `{"outbounds": [{"type": "trojan", "tls": {"nope": true}}]}`},
		{"port out of range", `{"outbounds": [{"type": "trojan", "server_port": 70000}]}`},
		{"second element bad", `{"outbounds": [{"type": "block", "tag": "a"}, {"type": "trojan", "server_port": "x"}]}`},
		{"trailing garbage", `{"outbounds": [{"type": "block", "tag": "a"}]} trailing`},
		{"two documents", `{"outbounds": [{"type": "block", "tag": "a"}]}{"outbounds": []}`},
		{"invalid utf8", string([]byte{'{', '"', 'o', 'u', 't', 'b', 'o', 'u', 'n', 'd', 's', '"', ':', '[', '"', 0xff, 0xfe, '"', ']', '}'})},
		{"truncated inside element", `{"outbounds": [{"type": "trojan", "tag":`},
		{"unterminated array", `{"outbounds": [{"type": "block", "tag": "a"}`},
	}
	// The streaming decoder rejects every case below exactly like the frozen reader, but the message
	// is allowed to differ for the documented reasons: a malformed `endpoints` value is reported by
	// the array reader instead of the document field, an element-local syntax error has no reliable
	// document-relative row/column, and trailing content after a complete document is reported by the
	// token reader instead of the whole-document validator.
	errorTextMayDiffer := map[string]string{
		"empty":                    "truncated document: the whole-document validator reported the end of input",
		"whitespace":               "truncated document: the whole-document validator reported the end of input",
		"broken json":              "truncated document: the whole-document validator reported the end of input",
		"unterminated array":       "truncated document: the whole-document validator reported the end of input",
		"truncated inside element": "truncated document inside an element: the streaming decoder reports io.ErrUnexpectedEOF",
		"endpoints not array":      "a typed array decode reports the value, not the document field",
		"invalid utf8":             "the element reader reports the byte through a different path",
		"trailing garbage":         "the token reader reports the trailing byte, the validator reported position",
		"two documents":            "the token reader reports the second value, the validator reported position",
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			legacyOutbounds, legacyEndpoints, legacyErr := frozenParseBoxSubscription(ctx, testCase.content)
			outbounds, endpoints, err := ParseBoxSubscription(ctx, testCase.content)
			if (legacyErr == nil) != (err == nil) {
				t.Fatalf("acceptance differs: frozen err=%v, streaming err=%v", legacyErr, err)
			}
			if legacyErr != nil {
				if reason, allowed := errorTextMayDiffer[testCase.name]; allowed {
					t.Logf("error text may differ (%s): frozen=%q streaming=%q", reason, legacyErr.Error(), err.Error())
					return
				}
				if legacyErr.Error() != err.Error() {
					t.Fatalf("error text changed:\nfrozen:    %s\nstreaming: %s", legacyErr, err)
				}
				return
			}
			if marshalValue(legacyOutbounds) != marshalValue(outbounds) {
				t.Fatalf("outbounds differ:\nfrozen:    %s\nstreaming: %s", marshalValue(legacyOutbounds), marshalValue(outbounds))
			}
			if marshalValue(legacyEndpoints) != marshalValue(endpoints) {
				t.Fatalf("endpoints differ:\nfrozen:    %s\nstreaming: %s", marshalValue(legacyEndpoints), marshalValue(endpoints))
			}
		})
	}
}

func marshalValue(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "marshal error: " + err.Error()
	}
	return string(encoded)
}

// The frozen reader below is the exact pre-rewrite implementation, kept as the reference the
// streaming reader is compared against. Do not "fix" it: its quirks are the contract under test.
type frozenSingBoxDocument _frozenSingBoxDocument

type _frozenSingBoxDocument struct {
	Outbounds []option.Outbound `json:"outbounds"`
	Endpoints []option.Endpoint `json:"endpoints"`
}

func (o *frozenSingBoxDocument) UnmarshalJSONContext(ctx context.Context, inputContent []byte) error {
	var content badjson.JSONObject
	err := content.UnmarshalJSONContext(ctx, inputContent)
	if err != nil {
		return err
	}
	outbounds, hasOutbounds := content.Get("outbounds")
	if !hasOutbounds || outbounds == nil {
		o.Outbounds = nil
		return nil
	}
	array, isArray := outbounds.(badjson.JSONArray)
	if !isArray {
		return E.New("outbounds must be an array, got ", describeJSONKind(outbounds))
	}
	var outs badjson.JSONArray
	for i, element := range array {
		object, isObject := element.(*badjson.JSONObject)
		if !isObject {
			return E.New("outbound[", i, "] must be an object, got ", describeJSONKind(element))
		}
		typeValue, loaded := object.Get("type")
		if !loaded {
			return E.New("missing type in outbound[", i, "]")
		}
		typeName, isString := typeValue.(string)
		if !isString {
			return E.New("outbound[", i, "] type must be a string, got ", describeJSONKind(typeValue))
		}
		switch typeName {
		case "direct", "block", "dns", "selector", "urltest":
			continue
		default:
			outs = append(outs, object)
		}
	}
	content.Put("outbounds", outs)
	inputContent, err = content.MarshalJSONContext(ctx)
	if err != nil {
		return err
	}
	return singjson.UnmarshalContext(ctx, inputContent, (*_frozenSingBoxDocument)(o))
}

func frozenParseBoxSubscription(ctx context.Context, content string) ([]option.Outbound, []option.Endpoint, error) {
	document, err := singjson.UnmarshalExtendedContext[frozenSingBoxDocument](ctx, []byte(content))
	if err != nil {
		return nil, nil, err
	}
	if len(document.Outbounds) == 0 && len(document.Endpoints) == 0 {
		return nil, nil, E.New("no servers found")
	}
	return document.Outbounds, document.Endpoints, nil
}

// probeContext registers only the option types the corpus needs. The constructors are never called
// by a decode.
func probeContext() context.Context {
	registry := probeRegistry()
	return service.ContextWith[option.OutboundOptionsRegistry](context.Background(), registry)
}

func probeRegistry() *probeOptionsRegistry {
	registry := &probeOptionsRegistry{options: make(map[string]func() any)}
	registry.add("trojan", func() any { return new(option.TrojanOutboundOptions) })
	registry.add("vless", func() any { return new(option.VLESSOutboundOptions) })
	registry.add("selector", func() any { return new(option.SelectorOutboundOptions) })
	registry.add("urltest", func() any { return new(option.URLTestOutboundOptions) })
	registry.add("direct", func() any { return new(option.DirectOutboundOptions) })
	registry.add("block", func() any { return new(option.StubOptions) })
	return registry
}

type probeOptionsRegistry struct {
	options map[string]func() any
}

func (r *probeOptionsRegistry) add(name string, constructor func() any) {
	r.options[name] = constructor
}

func (r *probeOptionsRegistry) OptionTypes() []string {
	return nil
}

func (r *probeOptionsRegistry) CreateOptions(outboundType string) (any, bool) {
	constructor, loaded := r.options[outboundType]
	if !loaded {
		return nil, false
	}
	return constructor(), true
}

// describeJSONKind names the JSON kind an assertion met. It is the frozen reader's helper: the
// streaming reader classifies raw values and tokens instead, and the two vocabulary lists must stay
// identical for the equivalence test to be meaningful.
func describeJSONKind(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case *badjson.JSONObject:
		return "object"
	case badjson.JSONArray:
		return "array"
	case string:
		return "string"
	case float64, int64:
		return "number"
	case bool:
		return "boolean"
	default:
		return "unknown"
	}
}
