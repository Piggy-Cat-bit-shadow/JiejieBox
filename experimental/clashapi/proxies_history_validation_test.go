package clashapi

// Regression tests for the /proxies history pre-validation.
//
// A legal time.Time can be unencodable as RFC 3339 for TWO reasons, and the streamed handler must
// refuse both before it writes the 200 head:
//
//   - a year outside [0,9999];
//   - a timezone hour outside [0,23].
//
// The second case is the one that used to escape a year-only check: time.FixedZone accepts a 24h
// offset, so a history entry with a perfectly ordinary year produced 200 plus a truncated document.
//
// Every rejection case compares status AND body bytes against the package's legacy oracle, so the
// streamed handler keeps the old complete 500 body instead of inventing a new message.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
)

// storeProbeHistory puts stamp in the fixture's history storage under tag and refuses to continue
// unless the storage really returns it. A probe that silently failed to store would make the
// rejection tests pass for the wrong reason.
func storeProbeHistory(t *testing.T, fixture *proxiesFixture, tag string, stamp time.Time) *adapter.URLTestHistory {
	t.Helper()
	fixture.server.urlTestHistory.StoreDisplayHistory(tag, urltest.MeasurementScope{}, &adapter.URLTestHistory{
		Time:  stamp,
		Delay: 7,
	})
	history := fixture.server.urlTestHistory.LoadURLTestHistory(tag)
	if history == nil {
		t.Fatalf("probe history for %q was not stored", tag)
	}
	if !history.Time.Equal(stamp) {
		t.Fatalf("stored history time = %v, want %v", history.Time, stamp)
	}
	return history
}

// orderTrackingRecorder is a recorder that also records whether any body byte was written before the
// response head. A streaming handler that starts writing and only then discovers it cannot encode an
// entry would trip bodyBeforeHead; one that refuses before writing cannot.
type orderTrackingRecorder struct {
	*httptest.ResponseRecorder
	headWritten    bool
	bodyBeforeHead bool
}

func newOrderTrackingRecorder() *orderTrackingRecorder {
	return &orderTrackingRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (w *orderTrackingRecorder) WriteHeader(status int) {
	w.headWritten = true
	w.ResponseRecorder.WriteHeader(status)
}

func (w *orderTrackingRecorder) Write(p []byte) (int, error) {
	if !w.headWritten {
		w.bodyBeforeHead = true
	}
	return w.ResponseRecorder.Write(p)
}

// callStreamedProxies invokes the production handler through the order-tracking recorder.
func callStreamedProxies(fixture *proxiesFixture) *orderTrackingRecorder {
	recorder := newOrderTrackingRecorder()
	request := httptest.NewRequest(http.MethodGet, "/proxies", nil)
	getProxies(fixture.server)(recorder, request)
	return recorder
}

// requireRejectedLikeLegacy asserts the streamed handler refused the request the way the pre-streaming
// handler did: same status, byte-identical complete body, no byte written before the head, and the
// same real encoder error text (wantFragment) in the body.
func requireRejectedLikeLegacy(t *testing.T, fixture *proxiesFixture, wantFragment string) {
	t.Helper()
	legacyStatus, legacyHeader, legacyBody := callHandler(t, legacyGetProxies(fixture.server), "/proxies")
	if legacyStatus != http.StatusInternalServerError {
		t.Fatalf("legacy oracle status = %d, want 500 (the invalid history did not reach the encoder)", legacyStatus)
	}
	if !json.Valid(legacyBody) {
		t.Fatalf("legacy oracle body is not complete JSON: %q", legacyBody)
	}

	recorder := callStreamedProxies(fixture)
	if recorder.bodyBeforeHead {
		t.Fatal("streamed handler wrote body bytes before the response head; a client would see 200 and a truncated document")
	}
	if recorder.Code == http.StatusOK {
		t.Fatalf("streamed handler returned 200 for an unencodable history: body %q", recorder.Body.Bytes())
	}
	if recorder.Code != legacyStatus {
		t.Fatalf("streamed status = %d, want legacy status %d", recorder.Code, legacyStatus)
	}
	if got := recorder.Header().Get("Content-Type"); got != legacyHeader.Get("Content-Type") {
		t.Fatalf("streamed Content-Type = %q, want legacy %q", got, legacyHeader.Get("Content-Type"))
	}
	body := recorder.Body.Bytes()
	if !json.Valid(body) {
		t.Fatalf("streamed body is not complete JSON: %q", body)
	}
	if !bytes.Equal(body, legacyBody) {
		t.Fatalf("streamed error body differs from the legacy oracle:\n  legacy: %q\n  stream: %q", legacyBody, body)
	}
	if !bytes.Contains(body, []byte(wantFragment)) {
		t.Fatalf("streamed body %q does not carry the encoder error %q", body, wantFragment)
	}
	if bytes.Contains(body, []byte(`"proxies"`)) {
		t.Fatalf("streamed body contains a partial success document: %q", body)
	}
}

// TestProxiesRejectsUnencodableHistoryTime covers both legal-time.Time failure modes of
// time.Time.MarshalJSON: the +24h/-24h timezone (valid year) and years outside [0,9999] in both
// directions. Each must be refused with the legacy 500 body before the first byte.
func TestProxiesRejectsUnencodableHistoryTime(t *testing.T) {
	const timezoneFailure = "Time.MarshalJSON: timezone hour outside of range [0,23]"
	const yearFailure = "Time.MarshalJSON: year outside of range [0,9999]"

	cases := []struct {
		name         string
		stamp        time.Time
		wantFragment string
	}{
		{
			name:         "zone-plus-24h-valid-year",
			stamp:        time.Date(2027, 11, 22, 0, 0, 0, 0, time.FixedZone("invalid-24h", 24*60*60)),
			wantFragment: timezoneFailure,
		},
		{
			name:         "zone-minus-24h-valid-year",
			stamp:        time.Date(2027, 11, 22, 0, 0, 0, 0, time.FixedZone("invalid-minus-24h", -24*60*60)),
			wantFragment: timezoneFailure,
		},
		{
			name:         "year-10000",
			stamp:        time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
			wantFragment: yearFailure,
		},
		{
			name:         "year-negative",
			stamp:        time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC),
			wantFragment: yearFailure,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newProxiesFixture(t, 40)
			history := storeProbeHistory(t, fixture, "node-00004", testCase.stamp)
			if _, err := json.Marshal(history); err == nil {
				t.Fatalf("probe history unexpectedly encodes; this case does not exercise the rejection path")
			}
			requireRejectedLikeLegacy(t, fixture, testCase.wantFragment)
		})
	}
}

// TestProxiesAcceptsEncodableHistoryTimes pins that the stricter validation does not reject legal
// RFC 3339 values: UTC, local time, the extreme legal offsets ±23:59 and a half-hour offset. Each
// must keep the byte-identical 200 response of the legacy oracle, with the probe stamp actually
// present in the document.
func TestProxiesAcceptsEncodableHistoryTimes(t *testing.T) {
	cases := []struct {
		name  string
		stamp time.Time
	}{
		{name: "utc-now", stamp: time.Now().UTC()},
		{name: "local", stamp: time.Date(2026, 11, 22, 12, 0, 0, 123456789, time.Local)},
		{name: "zone-plus-2359", stamp: time.Date(2026, 11, 22, 12, 0, 0, 0, time.FixedZone("+23:59", 23*60*60+59*60))},
		{name: "zone-minus-2359", stamp: time.Date(2026, 11, 22, 12, 0, 0, 0, time.FixedZone("-23:59", -(23*60*60+59*60)))},
		{name: "zone-plus-0530", stamp: time.Date(2026, 11, 22, 12, 0, 0, 0, time.FixedZone("+05:30", 5*60*60+30*60))},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newProxiesFixture(t, 40)
			storeProbeHistory(t, fixture, "node-00004", testCase.stamp)

			legacyStatus, _, legacyBody := callHandler(t, legacyGetProxies(fixture.server), "/proxies")
			if legacyStatus != http.StatusOK {
				t.Fatalf("legacy oracle status = %d, want 200", legacyStatus)
			}
			status, _, body := callHandler(t, getProxies(fixture.server), "/proxies")
			if status != http.StatusOK {
				t.Fatalf("streamed status = %d, want 200 for an RFC 3339-encodable time", status)
			}
			if diff := firstByteDifference(legacyBody, body); diff != "" {
				t.Fatalf("streamed /proxies bytes differ from the legacy oracle: %s", diff)
			}

			// The comparison above would also pass if the probe history never reached the
			// document, so pin the exact serialized stamp inside the node card.
			quotedStamp, err := json.Marshal(testCase.stamp.Format(time.RFC3339Nano))
			if err != nil {
				t.Fatalf("marshal expected stamp: %v", err)
			}
			wantFragment := append([]byte(`"time":`), quotedStamp...)
			if !bytes.Contains(body, wantFragment) {
				t.Fatalf("streamed body does not contain the probe history %s", wantFragment)
			}
		})
	}
}

// TestProxiesRejectsUnencodableHistoryInMiddle proves the pre-validation scans every entry: an
// invalid time in the middle of the list is refused before the first (valid) card is written, not
// only when the first history happens to be bad.
func TestProxiesRejectsUnencodableHistoryInMiddle(t *testing.T) {
	fixture := newProxiesFixture(t, 40)

	// The fixture stores a valid history for node-00003 and every fourth leaf, so nodes that would
	// be streamed BEFORE the invalid one exist and are encodable.
	if fixture.server.urlTestHistory.LoadURLTestHistory("node-00003") == nil {
		t.Fatal("fixture is expected to carry a valid history for node-00003")
	}

	invalidStamp := time.Date(2027, 11, 22, 0, 0, 0, 0, time.FixedZone("invalid-24h", 24*60*60))
	history := storeProbeHistory(t, fixture, "node-00020", invalidStamp)
	if _, err := json.Marshal(history); err == nil {
		t.Fatal("probe history unexpectedly encodes; the test must reach the invalid-zone case")
	}

	requireRejectedLikeLegacy(t, fixture, "Time.MarshalJSON: timezone hour outside of range [0,23]")
}
