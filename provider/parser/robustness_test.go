package parser

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseBoxSubscriptionRejectsHostileDocuments is the adversarial half of the parser suite: a
// subscription is remote input, and every document shape that is not a valid node list must come
// back as an error. A panic here would take down the daemon, and a silently accepted partial
// document would publish nodes the operator never wrote.
func TestParseBoxSubscriptionRejectsHostileDocuments(t *testing.T) {
	t.Parallel()
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
		{"null element", `{"outbounds": [null]}`},
		{"string element", `{"outbounds": ["vless://x"]}`},
		{"number element", `{"outbounds": [1]}`},
		{"array element", `{"outbounds": [[]]}`},
		{"object without type", `{"outbounds": [{"tag": "a"}]}`},
		{"type not a string", `{"outbounds": [{"type": 5}]}`},
		{"type null", `{"outbounds": [{"type": null}]}`},
		{"unknown type", `{"outbounds": [{"type": "does-not-exist", "tag": "a"}]}`},
		{"missing required fields", `{"outbounds": [{"type": "vless", "tag": "a"}]}`},
		{"mixed valid and invalid", `{"outbounds": [{"type": "block", "tag": "ok"}, {"type": "vless"}]}`},
		{"duplicate json keys", `{"outbounds": [], "outbounds": [{"type": "block", "tag": "a"}]}`},
		{"huge nesting", `{"outbounds": [{"type": "vless", "tag": "a", "x": ` + repeat("[", 200) + repeat("]", 200) + `}]}`},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			outbounds, endpoints, err := ParseBoxSubscription(context.Background(), testCase.content)
			require.Error(t, err)
			require.Empty(t, outbounds)
			require.Empty(t, endpoints)
		})
	}
}

func repeat(value string, count int) string {
	var builder []byte
	for i := 0; i < count; i++ {
		builder = append(builder, value...)
	}
	return string(builder)
}

// TestParseSubscriptionSurvivesHostileDocuments runs the SAME documents through the format-detection
// chain the remote provider actually uses. The native reader is only the first attempt: a document
// it refuses is offered to the YAML, SIP008 and link readers, and each of those is also handed
// untrusted bytes.
func TestParseSubscriptionSurvivesHostileDocuments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		content string
	}{
		{"empty", ``},
		{"not json", `hello`},
		{"null", `null`},
		{"array", `[]`},
		{"broken json", `{"outbounds": [`},
		{"null outbound", `{"outbounds": [null]}`},
		{"object outbounds", `{"outbounds": {}}`},
		{"clash null proxy", "proxies:\n  - null\n"},
		{"clash string proxy", "proxies:\n  - hello\n"},
		{"clash numeric proxy", "proxies:\n  - 5\n"},
		{"clash unknown type", "proxies:\n  - {name: a, type: nope}\n"},
		{"clash empty", "proxies: []\n"},
		{"sip008 empty", `{"servers": []}`},
		{"sip008 null server", `{"servers": [null]}`},
		{"sip008 invalid server", `{"servers": [{"server": "x"}]}`},
		{"link garbage", "not-a-link\n"},
		{"link partial", "vless://\n"},
		{"invalid utf8", string([]byte{0xff, 0xfe, 0xfd})},
		{"deep yaml", "proxies: " + repeat("[", 200) + repeat("]", 200) + "\n"},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			require.NotPanics(t, func() {
				_, err := ParseSubscription(context.Background(), testCase.content, nil, "p")
				require.Error(t, err)
			})
		})
	}
}

// TestParseSubscriptionKeepsValidLinksInAMixedList documents the deliberate difference between the
// JSON readers and the link reader: a link list is line-oriented and one malformed line does not
// invalidate the working ones, while a JSON document is a single value and fails as a whole.
func TestParseSubscriptionKeepsValidLinksInAMixedList(t *testing.T) {
	t.Parallel()
	outbounds, err := ParseSubscription(context.Background(),
		"vless://11111111-2222-3333-4444-555555555555@example.com:443?security=tls&sni=example.com\nnot-a-link\n", nil, "p")
	require.NoError(t, err)
	require.Len(t, outbounds, 1)
	require.Equal(t, "vless", outbounds[0].Type)
}
