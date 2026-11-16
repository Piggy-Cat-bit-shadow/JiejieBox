//go:build go1.27 && !http2legacy && !badlinkname

package http

import "testing"

// A build without badlinkname carries neither -checklinkname=0 nor the private-layout cast, so
// extended CONNECT must stay unavailable and the tunnel falls back to HTTP/1. The migration
// deleted the old stub that said this and briefly let this configuration inherit the legacy
// file's `true`; this test is the regression that keeps the two apart.
func TestExtendedConnectUnavailableWithoutBadlinkname(t *testing.T) {
	if extendedConnectAvailable {
		t.Fatal("extendedConnectAvailable is true in a build without badlinkname: the HTTP/2 path would be " +
			"attempted by a build that cannot link or serve it")
	}
}
