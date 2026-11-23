package clashapi

// Clash compatibility under the new display terminal state: a failed probe is a fact for the fork's
// own clients, and MUST NOT change what a Clash client sees. The compatible document keeps the last
// SUCCESSFUL measurement (or nothing), never a zero-delay "failure" record - a Clash client renders
// `delay: 0` as a working zero-latency node.
//
// This is the invariant the layering exists to protect: the display terminal state is a separate map
// on purpose, so this test should keep passing without clashapi knowing it exists.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/urltest"
)

func TestAFailedProbeDoesNotChangeTheClashDocument(t *testing.T) {
	fixture := newProxiesFixture(t, 40)
	const tag = "node-00004"
	stamp := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	storeProbeHistory(t, fixture, tag, stamp)

	beforeStatus, _, beforeBody := callHandler(t, getProxies(fixture.server), "/proxies")
	if beforeStatus != http.StatusOK {
		t.Fatalf("status before the failure = %d, want 200", beforeStatus)
	}

	// A probe against this node really fails afterwards. This is the state the fork's clients show as
	// a timeout.
	completed := stamp.Add(time.Minute)
	if !fixture.server.urlTestHistory.StoreDisplayResult(tag, urltest.MeasurementScope{}, 0,
		completed.Add(-time.Second), completed, nil) {
		t.Fatal("the failure terminal state was not stored, so this test would prove nothing")
	}

	afterStatus, _, afterBody := callHandler(t, getProxies(fixture.server), "/proxies")
	if afterStatus != http.StatusOK {
		t.Fatalf("status after the failure = %d, want 200", afterStatus)
	}
	if diff := firstByteDifference(beforeBody, afterBody); diff != "" {
		t.Fatalf("a failed probe changed the Clash document: %s", diff)
	}

	// And pin the value itself: the last SUCCESS is what the document carries.
	var document struct {
		Proxies map[string]struct {
			History []struct {
				Time  string `json:"time"`
				Delay int    `json:"delay"`
			} `json:"history"`
		} `json:"proxies"`
	}
	if err := json.Unmarshal(afterBody, &document); err != nil {
		t.Fatalf("decode /proxies: %v", err)
	}
	entry, loaded := document.Proxies[tag]
	if !loaded {
		t.Fatalf("the document has no %s entry", tag)
	}
	if len(entry.History) == 0 {
		t.Fatalf("%s lost its history after a failed probe", tag)
	}
	last := entry.History[len(entry.History)-1]
	if last.Delay != 7 {
		t.Fatalf("Clash delay after a failure = %d, want the last success (7)", last.Delay)
	}
}
