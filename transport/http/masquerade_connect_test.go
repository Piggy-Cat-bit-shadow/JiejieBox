package http

import (
	"net/http"
	"net/url"
	"testing"
)

// Go 1.27's httputil.ReverseProxy refuses a CONNECT before any round trip ("We cannot
// handle CONNECT requests"), so the masquerade proxy must not be handed one: an
// unauthenticated probe would get the kernel's own 502 instead of the decoy page. These
// cases pin the rewrite that keeps the decoy reachable, without needing the integration
// suite that starts a real inbound.
func TestMasqueradeRequestRewritesPlainConnect(t *testing.T) {
	original := &http.Request{
		Method:        http.MethodConnect,
		Proto:         "HTTP/2.0",
		ProtoMajor:    2,
		URL:           &url.URL{Host: "target.example:443"},
		Host:          "target.example:443",
		Header:        http.Header{},
		Body:          http.NoBody,
		ContentLength: 0,
	}

	rewritten := masqueradeRequest(original)
	if rewritten == original {
		t.Fatal("a plain CONNECT was passed through unchanged; Go 1.27's ReverseProxy will answer 502 without reaching the decoy")
	}
	if rewritten.Method != http.MethodGet {
		t.Fatalf("method = %q, want GET", rewritten.Method)
	}
	if rewritten.URL.Path != "/" {
		t.Fatalf("path = %q, want /", rewritten.URL.Path)
	}
	if rewritten.Body != http.NoBody || rewritten.ContentLength != 0 {
		t.Fatalf("body = %v/%d, want NoBody/0", rewritten.Body, rewritten.ContentLength)
	}
	if original.Method != http.MethodConnect {
		t.Fatalf("the original request was mutated: method = %q", original.Method)
	}
}

func TestMasqueradeRequestKeepsExtendedConnect(t *testing.T) {
	// An extended CONNECT is not a page request: the published contract answers it with the
	// decoy backend's bare 502, and rewriting it to a GET would invent a page that the
	// backend never served.
	original := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Host: "target.example:443"},
		Host:   "target.example:443",
		Header: http.Header{":protocol": []string{"connect-udp"}},
	}
	if got := masqueradeRequest(original); got != original {
		t.Fatal("an extended CONNECT was rewritten; only a plain CONNECT has no page representation")
	}
}

func TestMasqueradeRequestKeepsOrdinaryRequests(t *testing.T) {
	original := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Path: "/assets/app.js"},
		Header: http.Header{},
	}
	if got := masqueradeRequest(original); got != original {
		t.Fatal("an ordinary request was rewritten; the masquerade must forward it untouched")
	}
}
