package parser

import (
	"strings"
	"testing"

	"github.com/sagernet/sing/common/json"
)

// Differential fuzz target for the streaming native reader (PS).
//
// The corpus test (sing_box_differential_test.go) pins the generated shapes; this target lets the
// Go fuzzer mutate arbitrary documents and compares the streaming reader against the frozen
// whole-document implementation on the facts the rewrite must preserve: accepted versus rejected,
// and the exact decoded outbounds and endpoints. Error TEXT is deliberately not compared - a few
// diagnostic shapes are documented as allowed to differ - and one class is skipped: a document that
// declares the same typed top-level field under two different spellings is the documented
// divergence, not an equivalence claim (see TestCaseVariantTopLevelKeysAreOneDocumentedDivergence).
//
// Run it with:
//
//	go test ./provider/parser -run '^$' -fuzz '^FuzzParseBoxSubscriptionMatchesFrozen$' -fuzztime=30s
func FuzzParseBoxSubscriptionMatchesFrozen(f *testing.F) {
	seeds := []string{
		``,
		`{}`,
		`null`,
		`[]`,
		`{"outbounds":[]}`,
		`{"outbounds":null}`,
		`{"outbounds":null,"endpoints":5}`,
		`{"outbounds":[{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p"}]}`,
		`{"outbounds":[{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p"}],"endpoints":[{"type":"wireguard","tag":"wg"}]}`,
		`{"outbounds":[{"type":"block","tag":"a"},{"type":"direct","tag":"b"}],"endpoints":[]}`,
		`{"outbounds":5,"outbounds":[{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p"}]}`,
		`{"outbounds":[{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p"}],"outbounds":5}`,
		`{"outbounds":[{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p"}],"endpoints":{}}`,
		`{"outbounds":[{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p"}],"endpoints":[{}]}`,
		`{"endpoints":[]}`,
		`{"OUTBOUNDS":[{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p"}]}`,
		`{"outbounds":[{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p"}]} trailing`,
		`{"outbounds":[{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p"}]}{}`,
		`{"outbounds":[{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p"}],"x":[[[[[[[[[[{}]]]]]]]]]]}`,
		`{"outbounds":[{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p","tls":{}}]}`,
		`{"outbounds":[{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p","tls":{"reality":{}}}]}`,
		`{"outbounds":[{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p","transport":{}}]}`,
		`{"outbounds":[{"type":"selector","tag":"g","outbounds":[]}]}`,
		`{"outbounds":[{"type":"trojan","server_port":"x"}]}`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, content string) {
		if len(content) > 1<<20 {
			t.Skip("bound one fuzz case to a provider-document-sized input")
		}
		comparison := compareReaders(t, probeContext(), content)
		if comparison.agrees() {
			return
		}
		if reason := documentedDivergenceReason(content); reason != "" {
			t.Skip("documented divergence: ", reason)
		}
		t.Fatalf("readers disagree for %q:\n  frozen: outbounds=%s endpoints=%s err=%v\n  streaming: outbounds=%s endpoints=%s err=%v",
			content,
			marshalValue(comparison.frozenOutbounds), marshalValue(comparison.frozenEndpoints), comparison.frozenErr,
			marshalValue(comparison.streamOutbounds), marshalValue(comparison.streamEndpoints), comparison.streamErr)
	})
}

// mixedTypedTopLevelSpelling reports whether the document declares `outbounds` or `endpoints` under
// more than one spelling at the top level. That is the only shape the streaming reader deliberately
// treats differently from the frozen implementation, so the fuzzer must not count it as a
// disagreement. A document that is not a JSON object is never mixed.
func mixedTypedTopLevelSpelling(content string) bool {
	decoder := json.NewDecoder(strings.NewReader(content))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	spellings := make(map[string]string)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return false
		}
		key, isKey := keyToken.(string)
		if !isKey {
			return false
		}
		lower := strings.ToLower(key)
		if lower == "outbounds" || lower == "endpoints" {
			if previous, loaded := spellings[lower]; loaded && previous != key {
				return true
			}
			spellings[lower] = key
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return false
		}
	}
	return false
}
