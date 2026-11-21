package parser

// Differential attack on the streaming native reader (PS).
//
// The reader rewrite exists to keep the ACCEPTED SET of the previous whole-document reader while
// removing its materialisation. That claim is only as good as the comparison behind it, so this
// file drives the frozen implementation and the streaming one over generated documents rather than
// over a hand-written table: duplicate keys, every value kind, endpoint forms, empty values and
// element-level duplicates are combined mechanically.
//
// One class is a DOCUMENTED divergence rather than a bug: a document that declares the same typed
// top-level field under two different spellings (`outbounds` and `Outbounds`). The old reader
// filtered only the exact-lowercase value, then re-serialised the whole object (dropping empty
// arrays on the way) and decoded it with case-insensitive last-wins. That two-stage design made the
// winner depend on emptiness, and it could return a GROUP as a provider node. The streaming reader
// applies one rule to every spelling - the last case-insensitive occurrence wins and its node types
// are filtered like any other - so it cannot publish a group and does not reproduce the accident.
// TestCaseVariantTopLevelKeysAreOneDocumentedDivergence pins both sides of that decision.

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sagernet/sing/common/json"

	"github.com/stretchr/testify/require"
)

// differentialValues is the value vocabulary both readers are driven with. Every kind the readers
// classify differently (null, array, object, scalar) and every element shape that changes the
// verdict (valid, missing type, wrong option type, group, empty object) is present.
var differentialValues = map[string]string{
	"valid-a":      `[{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p"}]`,
	"valid-b":      `[{"type":"trojan","tag":"b","server":"203.0.113.1","server_port":443,"password":"p"}]`,
	"empty":        `[]`,
	"null":         `null`,
	"number":       `5`,
	"string":       `"x"`,
	"object":       `{}`,
	"group-only":   `[{"type":"selector","tag":"g","outbounds":[]}]`,
	"null-elem":    `[null]`,
	"bad-option":   `[{"type":"trojan","tag":"a","server_port":"x"}]`,
	"missing-type": `[{"tag":"a"}]`,
	"empty-elem":   `[{}]`,
	"mixed":        `[{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p"},{"type":"trojan","tag":"b","server_port":"x"}]`,
}

var differentialEndpointValues = map[string]string{
	"empty":      `[]`,
	"null":       `null`,
	"number":     `5`,
	"object":     `{}`,
	"empty-elem": `[{}]`,
	"wireguard":  `[{"type":"wireguard","tag":"wg"}]`,
	"wg-empty":   `[{"type":"wireguard","tag":"wg","peers":[]}]`,
}

// readerComparison is one document run through both readers. It is a value rather than an
// assertion so the fuzz target can classify a disagreement as a documented divergence before it
// fails the input.
type readerComparison struct {
	frozenOutbounds, frozenEndpoints optionSlice
	streamOutbounds, streamEndpoints optionSlice
	frozenErr, streamErr             error
}

// agrees reports whether the streaming reader preserved the frozen reader's answer.
func (c readerComparison) agrees() bool {
	if (c.frozenErr == nil) != (c.streamErr == nil) {
		return false
	}
	if c.frozenErr != nil {
		return true
	}
	return marshalValue(c.frozenOutbounds) == marshalValue(c.streamOutbounds) &&
		marshalValue(c.frozenEndpoints) == marshalValue(c.streamEndpoints)
}

// compareReaders runs one document through both readers and never lets a panic escape.
func compareReaders(t *testing.T, ctx context.Context, document string) readerComparison {
	t.Helper()
	var comparison readerComparison
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("frozen reader panicked on %s: %v", document, recovered)
			}
		}()
		lo, le, err := frozenParseBoxSubscription(ctx, document)
		comparison.frozenOutbounds = toOptionSlice(lo)
		comparison.frozenEndpoints = toOptionSlice(le)
		comparison.frozenErr = err
	}()
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("streaming reader panicked on %s: %v", document, recovered)
			}
		}()
		o, e, err := ParseBoxSubscription(ctx, document)
		comparison.streamOutbounds = toOptionSlice(o)
		comparison.streamEndpoints = toOptionSlice(e)
		comparison.streamErr = err
	}()
	return comparison
}

// requireDifferentialAgreement compares one document end to end: success versus failure and the
// exact decoded outbounds and endpoints (which also covers order and values).
func requireDifferentialAgreement(t *testing.T, ctx context.Context, document string) {
	t.Helper()
	comparison := compareReaders(t, ctx, document)
	require.Equal(t, comparison.frozenErr == nil, comparison.streamErr == nil,
		"acceptance differs for %s: frozen err=%v, streaming err=%v", document, comparison.frozenErr, comparison.streamErr)
	if comparison.frozenErr != nil {
		return
	}
	require.Equal(t, marshalValue(comparison.frozenOutbounds), marshalValue(comparison.streamOutbounds),
		"outbounds differ for %s", document)
	require.Equal(t, marshalValue(comparison.frozenEndpoints), marshalValue(comparison.streamEndpoints),
		"endpoints differ for %s", document)
}

// documentedDivergenceReason names the classes where the two readers are allowed to disagree. The
// streaming rewrite removes the old reader's whole-document materialisation, and two of that
// reader's behaviours were products of it rather than of the schema:
//
//   - a typed top-level field declared under two different spellings. The old reader filtered only
//     the exact-lowercase `outbounds` value and then re-serialised the document, so the winner
//     depended on which value its marshaller considered empty, and a case-variant group could be
//     decoded as a node. The streaming reader applies one uniform last-wins rule and never publishes
//     a group (see TestCaseVariantTopLevelKeysAreOneDocumentedDivergence).
//
//   - an all-empty value for a node's `tag` (the outbound ENVELOPE field, decoded before the
//     per-protocol options). The old marshaller DROPPED such a value, so the field arrived at the
//     typed decoder as absent and the node was accepted with an empty tag. The streaming decoder
//     sees the field and rejects it ("cannot unmarshal object into ... tag of type string"). The
//     same empty value inside the protocol options (`password`, `server`, `server_port`, `tls`, ...)
//     does NOT diverge: those are decoded through the option library, which applies the same elision
//     in both readers (covered by the generated corpus above). Reproducing the envelope quirk would
//     require re-serialising every element - the materialisation this reader exists to avoid - and
//     the rejected document is malformed: no serialiser emits an empty object where a tag belongs.
//
// Everything else must agree exactly.
func documentedDivergenceReason(content string) string {
	if mixedTypedTopLevelSpelling(content) {
		return "the same typed top-level field is declared under two spellings"
	}
	if hasAllEmptyEnvelopeTag(content) {
		return "a node's envelope tag is an all-empty value the old re-serialisation elided"
	}
	return ""
}

// hasAllEmptyEnvelopeTag reports whether any element of the top-level `outbounds` array declares an
// all-empty `tag` (the badjson `IsEmpty` shape the old marshaller dropped).
func hasAllEmptyEnvelopeTag(content string) bool {
	decoder := json.NewDecoder(strings.NewReader(content))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return false
		}
		key, isKey := keyToken.(string)
		if !isKey {
			return false
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return false
		}
		if !strings.EqualFold(key, "outbounds") {
			continue
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || trimmed[0] != '[' {
			continue
		}
		elementDecoder := json.NewDecoder(bytes.NewReader(trimmed))
		if _, err := elementDecoder.Token(); err != nil { // the opening bracket
			return false
		}
		for elementDecoder.More() {
			elementToken, err := elementDecoder.Token()
			if err != nil {
				return false
			}
			if elementToken != json.Delim('{') {
				continue
			}
			for elementDecoder.More() {
				fieldToken, err := elementDecoder.Token()
				if err != nil {
					return false
				}
				field, isField := fieldToken.(string)
				if !isField {
					return false
				}
				valueEmpty, _ := scanJSONValue(elementDecoder)
				if strings.EqualFold(field, "tag") && valueEmpty {
					return true
				}
			}
			if _, err := elementDecoder.Token(); err != nil { // the closing brace
				return false
			}
		}
	}
	return false
}

// scanJSONValue consumes one value and reports whether it is empty all the way down (badjson's
// IsEmpty shape) and whether it contains such a value anywhere. Scalars are never empty.
func scanJSONValue(decoder *json.Decoder) (empty bool, containsEmpty bool) {
	token, err := decoder.Token()
	if err != nil {
		return false, false
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return false, false
	}
	switch delim {
	case '{':
		empty = true
		for decoder.More() {
			if _, err := decoder.Token(); err != nil { // the key
				return false, false
			}
			childEmpty, childContainsEmpty := scanJSONValue(decoder)
			containsEmpty = containsEmpty || childContainsEmpty
			if !childEmpty {
				empty = false
			}
		}
		if _, err := decoder.Token(); err != nil { // the closing brace
			return false, false
		}
	case '[':
		empty = true
		for decoder.More() {
			childEmpty, childContainsEmpty := scanJSONValue(decoder)
			containsEmpty = containsEmpty || childContainsEmpty
			if !childEmpty {
				empty = false
			}
		}
		if _, err := decoder.Token(); err != nil { // the closing bracket
			return false, false
		}
	default:
		return false, false
	}
	return empty, containsEmpty || empty
}

type optionSlice []any

func toOptionSlice[T any](values []T) optionSlice {
	if values == nil {
		return nil
	}
	converted := make(optionSlice, len(values))
	for index := range values {
		converted[index] = any(values[index])
	}
	return converted
}

// TestGeneratedDuplicateKeyDocumentsMatchTheFrozenReader is the corpus smoke of the differential
// attack: every ordered pair of duplicate top-level keys over the whole value vocabulary, every
// endpoint pair on top of a valid outbound list, and the nested/empty-object shapes the old
// re-serialisation treated specially.
func TestGeneratedDuplicateKeyDocumentsMatchTheFrozenReader(t *testing.T) {
	t.Parallel()
	ctx := probeContext()
	checked := 0

	outboundKeys := []string{"outbounds", "endpoints"}
	for _, firstKey := range outboundKeys {
		for _, secondKey := range outboundKeys {
			for firstName, firstValue := range differentialValues {
				if firstKey == "endpoints" && !isEndpointValue(firstName) {
					// An endpoints value must be a valid endpoint array, a scalar or null; the
					// outbound vocabulary would make the pair test nothing but the endpoints type
					// check. The endpoint vocabulary is covered by its own loop below.
					continue
				}
				for _, secondValue := range differentialValues {
					document := fmt.Sprintf(`{"%s":%s,"%s":%s}`, firstKey, firstValue, secondKey, secondValue)
					requireDifferentialAgreement(t, ctx, document)
					checked++
				}
			}
		}
	}

	outboundValid := differentialValues["valid-a"]
	// A single occurrence of the field, however it is spelled, must decode identically: the old
	// struct decode was case-insensitive and the streaming reader matches it.
	for _, key := range []string{"endpoints", "Endpoints", "ENDPOINTS"} {
		for _, firstValue := range differentialEndpointValues {
			document := fmt.Sprintf(`{"outbounds":%s,"%s":%s}`, outboundValid, key, firstValue)
			requireDifferentialAgreement(t, ctx, document)
			checked++
			// Exact duplicate of the SAME spelling is last-value-wins for both readers.
			for _, secondValue := range differentialEndpointValues {
				document = fmt.Sprintf(`{"outbounds":%s,"%s":%s,"%s":%s}`, outboundValid, key, firstValue, key, secondValue)
				requireDifferentialAgreement(t, ctx, document)
				checked++
			}
		}
	}

	// Nested empty objects: the frozen reader dropped them during re-serialisation, so the streaming
	// reader must produce the same decoded VALUES even though it saw them.
	nestedElements := []string{
		`{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p","tls":{}}`,
		`{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p","tls":{"reality":{}}}`,
		`{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p","tls":{"utls":{}}}`,
		`{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p","transport":{}}`,
		`{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p","multiplex":{}}`,
		`{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p","udp_over_tcp":{}}`,
		`{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p","headers":{}}`,
		`{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p","network":"","udp":true}`,
	}
	for _, element := range nestedElements {
		requireDifferentialAgreement(t, ctx, `{"outbounds":[`+element+`]}`)
		checked++
	}

	// Element-level duplicate keys: the old reader read `type` from its generic tree, the streaming
	// reader reads it with a typed field decode. The accepted set must not move.
	elementDuplicates := []string{
		`{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p","tag":"b"}`,
		`{"type":"selector","type":"block","tag":"a"}`,
		`{"type":"block","type":"selector","tag":"a"}`,
		`{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p","server_port":444}`,
	}
	for _, element := range elementDuplicates {
		requireDifferentialAgreement(t, ctx, `{"outbounds":[`+element+`]}`)
		checked++
	}

	// Unknown fields, extreme nesting and overlarge values are still the same documents.
	validElement := strings.TrimSuffix(strings.TrimPrefix(differentialValues["valid-a"], `[`), `]`)
	requireDifferentialAgreement(t, ctx, `{"outbounds":[`+validElement+`],"unknown":"`+strings.Repeat("x", 1<<20)+`"}`)
	requireDifferentialAgreement(t, ctx, `{"outbounds":[`+validElement+`,"x":`+strings.Repeat("[", 500)+strings.Repeat("]", 500)+`]}`)
	requireDifferentialAgreement(t, ctx, `{"outbounds":[`+validElement+`],"endpoints":`+strings.Repeat("[", 500)+strings.Repeat("]", 500)+`}`)
	checked += 3

	t.Logf("differential corpus: %d generated documents agreed", checked)
}

// isEndpointValue keeps the endpoint-key half of the pair matrix inside the endpoint vocabulary.
func isEndpointValue(name string) bool {
	switch name {
	case "empty", "null", "number", "object", "empty-elem", "wireguard", "wg-empty":
		return true
	}
	return false
}

// TestCaseVariantTopLevelKeysAreOneDocumentedDivergence pins the one class where the two readers
// deliberately disagree, in both directions, so neither side can drift silently.
func TestCaseVariantTopLevelKeysAreOneDocumentedDivergence(t *testing.T) {
	t.Parallel()
	ctx := probeContext()
	validA := `[{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p"}]`

	t.Run("the streaming rule is uniform last-wins with node filtering", func(t *testing.T) {
		// The LAST case-insensitive spelling wins, whatever its case, and a group is never a node.
		streamed, _, err := ParseBoxSubscription(ctx, `{"outbounds":`+validA+`,"Outbounds":[{"type":"selector","tag":"g","outbounds":[]}]}`)
		require.Error(t, err, "a winning group-only list is not a provider node set")
		require.Empty(t, streamed)

		streamed, _, err = ParseBoxSubscription(ctx, `{"outbounds":[{"type":"selector","tag":"g","outbounds":[]}],"OUTBOUNDS":`+validA+`}`)
		require.NoError(t, err)
		require.Len(t, streamed, 1)
		require.Equal(t, "a", streamed[0].Tag)
	})

	t.Run("the frozen reader's two asymmetries are pinned as NOT reproduced", func(t *testing.T) {
		// (1) Only the exact-lowercase value was validated, so a malformed earlier `outbounds`
		// rejected a document whose later `OUTBOUNDS` was valid. The streaming reader takes the
		// later value and accepts.
		document := `{"outbounds":[{"type":"trojan","server_port":"x"}],"OUTBOUNDS":` + validA + `}`
		_, _, frozenErr := frozenParseBoxSubscription(ctx, document)
		streamed, _, streamedErr := ParseBoxSubscription(ctx, document)
		require.Error(t, frozenErr, "the frozen reader validated the exact-lowercase occurrence")
		require.NoError(t, streamedErr)
		require.Len(t, streamed, 1)

		// (2) An all-empty case-variant value was ELIDED from the old re-serialisation, so the
		// earlier valid value won; and a non-empty group survived to be decoded as a node. The
		// streaming reader does neither.
		document = `{"outbounds":` + validA + `,"Outbounds":[]}`
		frozenOutbounds, _, frozenErr := frozenParseBoxSubscription(ctx, document)
		require.NoError(t, frozenErr)
		require.Len(t, frozenOutbounds, 1, "the frozen reader fell back to the elided-away earlier value")
		_, _, streamedErr = ParseBoxSubscription(ctx, document)
		require.Error(t, streamedErr, "the streaming reader takes the empty later value, so there are no nodes")

		document = `{"outbounds":[],"Outbounds":[{"type":"selector","tag":"g","outbounds":[]}]}`
		frozenOutbounds, _, frozenErr = frozenParseBoxSubscription(ctx, document)
		require.NoError(t, frozenErr)
		require.Len(t, frozenOutbounds, 1, "the frozen reader published the case-variant group as a node")
		require.Equal(t, "selector", frozenOutbounds[0].Type)
		_, _, streamedErr = ParseBoxSubscription(ctx, document)
		require.Error(t, streamedErr, "the streaming reader never publishes a group")
	})

	// One spelling, however cased, keeps the historical short-circuit: a document whose only
	// outbounds key is a case variant is still "no servers found" because the frozen reader looked
	// up the exact key before decoding anything.
	_, _, err := ParseBoxSubscription(ctx, `{"Outbounds":`+validA+`}`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no servers found")
}

// TestAllEmptyEnvelopeTagsAreTheSecondDocumentedDivergence pins the element-level half of the
// elision: the old marshaller dropped an all-empty `tag` before the typed decode, so the node was
// accepted with an empty tag; the streaming decoder sees the field and rejects it. The rejection is
// fail-closed (the provider keeps its previous node set) and no serialiser emits an empty object
// where a tag belongs, so this is documented rather than reproduced.
func TestAllEmptyEnvelopeTagsAreTheSecondDocumentedDivergence(t *testing.T) {
	t.Parallel()
	ctx := probeContext()

	divergent := []struct {
		name     string
		document string
	}{
		{"empty object for tag", `{"outbounds":[{"type":"trojan","tag":{}}]}`},
		{"empty array for tag", `{"outbounds":[{"type":"trojan","tag":[]}]}`},
		{"nested empty object for tag", `{"outbounds":[{"type":"trojan","tag":{"x":{}}}]}`},
	}
	for _, testCase := range divergent {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			comparison := compareReaders(t, ctx, testCase.document)
			require.False(t, comparison.agrees(), "this shape is expected to be the documented divergence")
			require.NoError(t, comparison.frozenErr, "the frozen reader elided the empty tag")
			require.Error(t, comparison.streamErr, "the streaming reader does not elide values")
			require.NotEmpty(t, documentedDivergenceReason(testCase.document))
		})
	}

	// An empty value in the PROTOCOL OPTIONS is not a divergence: both readers decode through the
	// option library, which applies the same elision. Pinning it keeps the documented class precise.
	agreeing := []struct {
		name     string
		document string
	}{
		{"empty object for password", `{"outbounds":[{"type":"trojan","tag":"a","password":{}}]}`},
		{"empty array for password", `{"outbounds":[{"type":"trojan","tag":"a","password":[]}]}`},
		{"empty object for server", `{"outbounds":[{"type":"trojan","tag":"a","server":{}}]}`},
		{"empty object for server_port", `{"outbounds":[{"type":"trojan","tag":"a","server_port":{}}]}`},
		{"empty object nested in tls", `{"outbounds":[{"type":"trojan","tag":"a","tls":{"reality":{}}}]}`},
	}
	for _, testCase := range agreeing {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			comparison := compareReaders(t, ctx, testCase.document)
			require.True(t, comparison.agrees(), "the option library elides the empty value for both readers")
			require.Empty(t, documentedDivergenceReason(testCase.document))
		})
	}

	// A NON-empty malformed value is a rejection for BOTH readers: the divergence is the elision,
	// not a general leniency.
	document := `{"outbounds":[{"type":"trojan","tag":{"x":1}}]}`
	comparison := compareReaders(t, ctx, document)
	require.True(t, comparison.agrees())
	require.Error(t, comparison.frozenErr)
	require.Error(t, comparison.streamErr)
}

// TestAnAllEmptyEndpointsValueIsIgnoredLikeTheFrozenReader is the regression for the endpoint half
// of the elision bug: the previous reader dropped an all-empty `endpoints` value during its
// re-serialisation, so the document was accepted with the endpoints ignored; the streaming reader
// used to reject the whole document instead (the outbounds were then not applied at all). The
// streaming reader now treats the all-empty value as absent, which is exactly what the frozen
// reader did, and keeps rejecting every non-empty malformed value.
func TestAnAllEmptyEndpointsValueIsIgnoredLikeTheFrozenReader(t *testing.T) {
	t.Parallel()
	ctx := probeContext()
	valid := differentialValues["valid-a"]
	for _, endpoints := range []string{`{}`, `[{}]`, `[[{}]]`, `[{},[]]`} {
		document := `{"outbounds":` + valid + `,"endpoints":` + endpoints + `}`
		requireDifferentialAgreement(t, ctx, document)
		outbounds, parsedEndpoints, err := ParseBoxSubscription(ctx, document)
		require.NoError(t, err, "endpoints %s", endpoints)
		require.Len(t, outbounds, 1)
		require.Empty(t, parsedEndpoints, "an all-empty endpoints value carries no endpoint")
	}
	// A non-empty malformed value is still a rejection for both readers.
	for _, endpoints := range []string{`5`, `"x"`, `[null]`, `[{"type":1}]`, `[{"tag":"wg"}]`} {
		document := `{"outbounds":` + valid + `,"endpoints":` + endpoints + `}`
		comparison := compareReaders(t, ctx, document)
		require.Error(t, comparison.frozenErr, "endpoints %s", endpoints)
		require.Error(t, comparison.streamErr, "endpoints %s", endpoints)
	}
}

// TestEveryTruncationOfAValidDocumentIsRejected is the PS publication half of the attack: a body
// that ends anywhere before the document does must be an ERROR, never a partial node set. The
// provider applies a successful parse as the whole new node set, so an empty success here would
// clear the previous nodes.
func TestEveryTruncationOfAValidDocumentIsRejected(t *testing.T) {
	t.Parallel()
	ctx := probeContext()
	document := `{"outbounds":[` +
		`{"type":"trojan","tag":"a","server":"203.0.113.1","server_port":443,"password":"p"},` +
		`{"type":"trojan","tag":"b","server":"203.0.113.2","server_port":443,"password":"p"},` +
		`{"type":"trojan","tag":"c","server":"203.0.113.3","server_port":443,"password":"p"}]}`
	for length := 1; length < len(document); length++ {
		prefix := document[:length]
		_, _, boxErr := ParseBoxSubscription(ctx, prefix)
		require.Error(t, boxErr, "a truncated document was accepted at length %d: %q", length, prefix)
		outbounds, err := ParseSubscription(ctx, prefix, nil, "p")
		require.Error(t, err, "the format chain accepted a truncated document at length %d: %q", length, prefix)
		require.Empty(t, outbounds)
	}
	// The complete document is the only accepted one, and it keeps order and count.
	outbounds, err := ParseSubscription(ctx, document, nil, "p")
	require.NoError(t, err)
	require.Len(t, outbounds, 3)
	require.Equal(t, []string{"a", "b", "c"}, []string{outbounds[0].Tag, outbounds[1].Tag, outbounds[2].Tag})
}
