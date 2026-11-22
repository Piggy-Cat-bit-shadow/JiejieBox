package clashapi

// Differential tests for the A/N2 list endpoints: the streaming handler must produce the same JSON
// as the pre-change implementation (kept in zz_a_legacy_test.go), both byte for byte and after
// unmarshalling. Byte equality is checked first because some dashboards compare raw strings or
// depend on field order; the semantic comparison then pins the actual contract.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// callHandler invokes handler with a fresh recorder and returns status, headers and body.
func callHandler(t *testing.T, handler http.HandlerFunc, target string) (int, http.Header, []byte) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder.Code, recorder.Header(), recorder.Body.Bytes()
}

// firstByteDifference describes where two JSON documents diverge.
func firstByteDifference(left, right []byte) string {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for index := 0; index < limit; index++ {
		if left[index] != right[index] {
			start := index - 60
			if start < 0 {
				start = 0
			}
			end := index + 90
			if end > limit {
				end = limit
			}
			return fmt.Sprintf("first difference at byte %d (left len %d, right len %d)\n  legacy: ...%s...\n  stream: ...%s...",
				index, len(left), len(right), left[start:end], right[start:end])
		}
	}
	if len(left) != len(right) {
		return fmt.Sprintf("common prefix of %d bytes, lengths differ: legacy=%d stream=%d (tail legacy=%q stream=%q)",
			limit, len(left), len(right), tail(left, limit), tail(right, limit))
	}
	return ""
}

func tail(body []byte, from int) string {
	if from >= len(body) {
		return ""
	}
	end := from + 80
	if end > len(body) {
		end = len(body)
	}
	return string(body[from:end])
}

func TestAProxiesStreamingMatchesLegacy(t *testing.T) {
	for _, nodeCount := range []int{16, 40, 100, 1000} {
		t.Run(fmt.Sprintf("nodes-%d", nodeCount), func(t *testing.T) {
			fixture := newProxiesFixture(t, nodeCount)
			_, _, legacy := callHandler(t, legacyGetProxies(fixture.server), "/proxies")
			status, header, streamed := callHandler(t, getProxies(fixture.server), "/proxies")
			require.Equal(t, http.StatusOK, status)
			if diff := firstByteDifference(legacy, streamed); diff != "" {
				t.Fatalf("streamed /proxies bytes differ from the legacy implementation: %s", diff)
			}
			require.Equal(t, "text/plain; charset=utf-8", header.Get("Content-Type"),
				"the legacy handler did not set Content-Type, so net/http sniffed the body; a streaming "+
					"writer must keep the sniffed type stable")
			var legacyValue, streamedValue any
			require.NoError(t, json.Unmarshal(legacy, &legacyValue))
			require.NoError(t, json.Unmarshal(streamed, &streamedValue))
			require.True(t, reflect.DeepEqual(legacyValue, streamedValue))
		})
	}
}

func TestARulesStreamingMatchesLegacy(t *testing.T) {
	for _, ruleCount := range []int{0, 1, 16, 100, 1000} {
		t.Run(fmt.Sprintf("rules-%d", ruleCount), func(t *testing.T) {
			rules := newRulesFixture(ruleCount)
			_, legacyHeader, legacy := callHandler(t, legacyGetRules(rules), "/rules")
			status, header, streamed := callHandler(t, getRules(rules), "/rules")
			require.Equal(t, http.StatusOK, status)
			require.Equal(t, "application/json", header.Get("Content-Type"))
			require.Equal(t, legacyHeader.Get("Content-Type"), header.Get("Content-Type"))
			if diff := firstByteDifference(legacy, streamed); diff != "" {
				t.Fatalf("streamed /rules bytes differ from the legacy implementation: %s", diff)
			}
			var legacyValue, streamedValue any
			require.NoError(t, json.Unmarshal(legacy, &legacyValue))
			require.NoError(t, json.Unmarshal(streamed, &streamedValue))
			require.True(t, reflect.DeepEqual(legacyValue, streamedValue))
		})
	}
}

// TestARulesEmptyIsNull pins the historical empty body: the accumulator stayed nil, so the old
// endpoint said {"rules":null}. Changing it to [] would be a visible semantic change for a panel
// that distinguishes "no rules" from "rules not loaded".
func TestARulesEmptyIsNull(t *testing.T) {
	_, _, legacy := callHandler(t, legacyGetRules(newRulesFixture(0)), "/rules")
	_, _, streamed := callHandler(t, getRules(newRulesFixture(0)), "/rules")
	require.Equal(t, `{"rules":null}`+"\n", string(legacy))
	require.Equal(t, string(legacy), string(streamed))
}

// TestAProxiesStoreMode pins the parts a dashboard actually reads: GLOBAL first, the default node
// first in GLOBAL.all, exact key order inside every card, duplicates resolved as before.
func TestAProxiesGLOBALAndOrdering(t *testing.T) {
	fixture := newProxiesFixture(t, 40)
	_, _, body := callHandler(t, getProxies(fixture.server), "/proxies")

	var document struct {
		Proxies map[string]json.RawMessage `json:"proxies"`
	}
	require.NoError(t, json.Unmarshal(body, &document))
	require.Contains(t, document.Proxies, "GLOBAL")
	require.Contains(t, document.Proxies, "group-selector")
	require.Contains(t, document.Proxies, "group-urltest")
	require.Contains(t, document.Proxies, "group-balance")
	require.Contains(t, document.Proxies, "group-hidden")
	require.Contains(t, document.Proxies, "endpoint-00001")

	// GLOBAL must be the first key of the object, and its all array must start with the default tag.
	globalIndex := bytes.Index(body, []byte(`"GLOBAL"`))
	require.Positive(t, globalIndex)
	firstProxyKey := bytes.Index(body, []byte(`{"proxies": {`))
	require.Equal(t, firstProxyKey+len(`{"proxies": {`), globalIndex)

	var global struct {
		All     []string `json:"all"`
		History []any    `json:"history"`
		Name    string   `json:"name"`
		Now     string   `json:"now"`
		Type    string   `json:"type"`
		UDP     bool     `json:"udp"`
	}
	require.NoError(t, json.Unmarshal(document.Proxies["GLOBAL"], &global))
	require.Equal(t, "GLOBAL", global.Name)
	require.Equal(t, "Fallback", global.Type)
	require.True(t, global.UDP)
	require.Equal(t, "node-00003", global.Now)
	require.NotEmpty(t, global.All)
	require.Equal(t, "node-00003", global.All[0], "the default outbound moved to the front of GLOBAL.all")
	require.Empty(t, global.History)
	require.NotContains(t, global.All, "DIRECT")
	require.NotContains(t, global.All, "REJECT")
	require.NotContains(t, global.All, "DNS")
	require.Contains(t, global.All, "endpoint-00001", "endpoints are listed in GLOBAL.all just like outbounds; only DIRECT/REJECT/DNS are excluded")

	// Field order inside a node card is part of the byte layout the panels have seen.
	var card bytes.Buffer
	require.NoError(t, json.Compact(&card, document.Proxies["group-selector"]))
	require.True(t, strings.HasPrefix(card.String(), `{"type":`), "card starts with type: %s", card.String())

	var selector struct {
		All     []string `json:"all"`
		History []any    `json:"history"`
		Name    string   `json:"name"`
		Now     string   `json:"now"`
		Type    string   `json:"type"`
		UDP     bool     `json:"udp"`
	}
	require.NoError(t, json.Unmarshal(document.Proxies["group-selector"], &selector))
	require.Equal(t, "Selector", selector.Type)
	require.Equal(t, "group-selector", selector.Name)
	require.Equal(t, "node-00003", selector.Now)
	require.Len(t, selector.All, 8)

	// A flow-aware group omits `now` but still publishes `all`.
	require.NotContains(t, string(document.Proxies["group-balance"]), `"now"`)
	require.Contains(t, string(document.Proxies["group-balance"]), `"all"`)

	// A hidden group is still listed: the Clash API is a node list, not the daemon's group screen.
	var hidden struct {
		All []string `json:"all"`
		Now string   `json:"now"`
	}
	require.NoError(t, json.Unmarshal(document.Proxies["group-hidden"], &hidden))
	require.Equal(t, "node-00004", hidden.Now)
}

// TestAProxiesDuplicateTagPriority pins the outbound-vs-endpoint collision behaviour of the old
// ordered map: the endpoint's card replaces the outbound's value AT THE OUTBOUND'S POSITION.
func TestAProxiesDuplicateTagPriority(t *testing.T) {
	fixture := newProxiesFixture(t, 20)
	shared := &fixtureEndpoint{&fixtureOutbound{tag: "node-00005", typ: "wireguard", networks: []string{"tcp", "udp"}}}
	fixture.endpoints.list = append(fixture.endpoints.list, shared)

	_, _, legacy := callHandler(t, legacyGetProxies(fixture.server), "/proxies")
	_, _, streamed := callHandler(t, getProxies(fixture.server), "/proxies")
	if diff := firstByteDifference(legacy, streamed); diff != "" {
		t.Fatalf("duplicate tag handling differs: %s", diff)
	}

	// The endpoint card wins for the value, and it is written once.
	require.Equal(t, 1, bytes.Count(streamed, []byte(`"node-00005":{`)))
	var document map[string]map[string]struct {
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(streamed, &document))
	require.Equal(t, "WireGuard", document["proxies"]["node-00005"].Type)

	// Position: the collision must not move the node to the end.
	positionOf := bytes.Index(streamed, []byte(`"node-00005":{`))
	positionOfNext := bytes.Index(streamed, []byte(`"node-00006":{`))
	require.Less(t, positionOf, positionOfNext)
}

// TestAProxiesDuplicateGLOBAL pins the pathological collision: a real node tagged GLOBAL replaces
// the pseudo card, and no Fallback object is emitted.
func TestAProxiesDuplicateGLOBAL(t *testing.T) {
	fixture := newProxiesFixture(t, 20)
	impostor := &fixtureOutbound{tag: "GLOBAL", typ: "shadowsocks", networks: []string{"tcp"}}
	fixture.manager.list = append(fixture.manager.list, impostor)
	fixture.manager.byTag["GLOBAL"] = impostor

	_, _, legacy := callHandler(t, legacyGetProxies(fixture.server), "/proxies")
	_, _, streamed := callHandler(t, getProxies(fixture.server), "/proxies")
	if diff := firstByteDifference(legacy, streamed); diff != "" {
		t.Fatalf("GLOBAL collision handling differs: %s", diff)
	}
	require.NotContains(t, string(streamed), `"type":"Fallback"`)
	require.Equal(t, 1, bytes.Count(streamed, []byte(`"GLOBAL":{`)))
}
