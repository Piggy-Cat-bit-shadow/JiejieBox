package naive

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sagernet/sing-box/log"
)

// TestAuditHTTP2ConnectPseudoHeadersAreRejected pins the wire answer for an
// HTTP/2 or HTTP/3 CONNECT carrying :scheme and/or :path. The reference
// (klzgrad/forwardproxy) answers 400 before authentication; a handler that
// only logs and returns lets net/http send its implicit 200, which tells the
// client the tunnel was accepted when no tunnel exists.
func TestAuditHTTP2ConnectPseudoHeadersAreRejected(t *testing.T) {
	inbound := &Inbound{logger: log.NewNOPFactory().Logger()}

	cases := []struct {
		name   string
		scheme string
		path   string
	}{
		{"scheme", "https", ""},
		{"path", "", "/"},
		{"both", "https", "/x"},
	}
	for _, testCase := range cases {
		testCase := testCase
		for _, protoMajor := range []int{2, 3} {
			protoMajor := protoMajor
			t.Run(testCase.name+"/h"+string(rune('0'+protoMajor)), func(t *testing.T) {
				request := httptest.NewRequest(http.MethodConnect, "https://example.com:443", nil)
				request.ProtoMajor = protoMajor
				request.ProtoMinor = 0
				request.URL.Scheme = testCase.scheme
				request.URL.Path = testCase.path

				recorder := httptest.NewRecorder()
				inbound.ServeHTTP(recorder, request)
				if recorder.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400 for an H%d CONNECT with :scheme/:path", recorder.Code, protoMajor)
				}
			})
		}
	}
}

// TestAuditHTTP2ConnectWithoutPseudoHeadersStillChallenges is the control:
// a well-formed H2 CONNECT falls through to authentication and gets 407, not
// 400, so the fix above cannot reject every H2 request.
func TestAuditHTTP2ConnectWithoutPseudoHeadersStillChallenges(t *testing.T) {
	inbound := &Inbound{logger: log.NewNOPFactory().Logger()}
	request := httptest.NewRequest(http.MethodConnect, "example.com:443", nil)
	request.ProtoMajor = 2
	request.ProtoMinor = 0

	recorder := httptest.NewRecorder()
	inbound.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusProxyAuthRequired {
		t.Fatalf("status = %d, want 407 for an unauthenticated H2 CONNECT", recorder.Code)
	}
}
