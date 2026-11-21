package urltest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProbeSmallBuffersPreserveLongRequestsAndResponseHeaders(t *testing.T) {
	path := "/" + strings.Repeat("long-path/", 512)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != path || request.Method != http.MethodHead {
			t.Errorf("request changed: %s %s", request.Method, request.URL.Path)
		}
		requests.Add(1)
		writer.Header().Set("X-Long-Response", strings.Repeat("v", 64<<10))
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	dialer := &countingDialer{}
	measurement, err := Measure(context.Background(), MeasureOptions{Link: server.URL + path}, dialer)
	if err != nil {
		t.Fatal(err)
	}
	if measurement.StatusCode != http.StatusNoContent || requests.Load() != 2 || dialer.dials.Load() != 1 {
		t.Fatalf("want two HEADs on one connection; status=%d requests=%d dials=%d", measurement.StatusCode, requests.Load(), dialer.dials.Load())
	}
}
