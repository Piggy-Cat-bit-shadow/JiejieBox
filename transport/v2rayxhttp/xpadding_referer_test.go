package v2rayxhttp

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// lx: XHTTP_OUTPERFORM — the padding header is now assembled by concatenation.
//
// The old shape built a url.URL, took its Query(), Set() the padding, Encode()d
// it back into RawQuery and String()ified the result: ~9 allocations and three
// passes over the padding bytes on EVERY request, on the packet-up path once per
// post. The replacement is one concatenation, and the only thing that can go
// wrong with it is escaping — so these tests compare the produced bytes against
// the exact chain that was replaced, including the paths where a hand-rolled
// concatenation is most likely to diverge (a space, a percent sign, a
// pre-encoded path, a non-ASCII byte, an empty path).

// legacyRefererChain is the implementation the concatenation replaced, kept here
// as the reference the new form must match byte for byte.
func legacyRefererChain(scheme string, host string, requestURL *url.URL, pad string) string {
	referer := &url.URL{Scheme: scheme, Host: host, Path: requestURL.Path}
	rq := referer.Query()
	rq.Set("x_padding", pad)
	referer.RawQuery = rq.Encode()
	return referer.String()
}

func TestLegacyPaddingRefererIsByteIdentical(t *testing.T) {
	t.Parallel()
	const padLength = 37
	pad := strings.Repeat("0", padLength)

	testCases := []struct {
		name    string
		path    string
		rawPath string
	}{
		{name: "plain", path: "/xhttp/sid/3"},
		{name: "trailing slash", path: "/upload/"},
		{name: "space", path: "/a b"},
		{name: "encoded query in path", path: "/%3Fproxyip=1.2.3.4"},
		{name: "percent", path: "/100%25"},
		{name: "non-ascii", path: "/café"},
		{name: "root", path: "/"},
		{name: "empty", path: ""},
		{name: "raw path honoured", path: "/a b", rawPath: "/a%20b"},
		{name: "raw path with plus", path: "/a+b", rawPath: "/a+b"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			client := clientWith(t, modePacketUp, intRange{padLength, padLength}, metaOptions{})
			request := &http.Request{
				Header: make(http.Header),
				URL: &url.URL{
					Scheme:  client.scheme,
					Host:    client.host,
					Path:    testCase.path,
					RawPath: testCase.rawPath,
				},
			}
			client.applyXPadding(request)

			want := legacyRefererChain(client.scheme, client.host, request.URL, pad)
			if got := request.Header.Get("Referer"); got != want {
				t.Fatalf("Referer = %q, want %q (the replaced url.URL chain)", got, want)
			}
		})
	}
}

// TestObfsQueryInHeaderRefererIsByteIdentical does the same for the obfs
// queryInHeader placement, which used to build a url.URL and String()ify it.
func TestObfsQueryInHeaderRefererIsByteIdentical(t *testing.T) {
	t.Parallel()
	const padLength = 64

	testCases := []struct {
		name string
		path string
	}{
		{name: "plain", path: "/xhttp/sid/3"},
		{name: "trailing slash", path: "/upload/"},
		{name: "space", path: "/a b"},
		{name: "encoded", path: "/%3Fproxyip=1.2.3.4"},
		{name: "non-ascii", path: "/café"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			client := clientWith(t, modePacketUp, intRange{padLength, padLength}, metaOptions{
				XPaddingObfsMode:  true,
				XPaddingPlacement: "queryInHeader",
			})
			request := &http.Request{
				Header: make(http.Header),
				URL:    &url.URL{Scheme: client.scheme, Host: client.host, Path: testCase.path},
			}
			client.applyXPadding(request)

			// The obfs pad is generated per request, so the expectation is built
			// from the value that was actually produced.
			produced := request.Header.Get(client.meta.xPaddingHeader)
			separator := "?" + client.meta.xPaddingKey + "="
			if !strings.Contains(produced, separator) {
				t.Fatalf("header %q does not carry %q", produced, separator)
			}
			pad := produced[strings.Index(produced, separator)+len(separator):]

			reference := &url.URL{Scheme: client.scheme, Host: client.host, Path: testCase.path}
			reference.RawQuery = client.meta.xPaddingKey + "=" + pad
			if want := reference.String(); produced != want {
				t.Fatalf("queryInHeader = %q, want %q (the replaced url.URL form)", produced, want)
			}
		})
	}
}
