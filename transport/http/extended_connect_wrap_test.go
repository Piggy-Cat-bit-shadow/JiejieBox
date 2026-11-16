//go:build go1.27 && !http2legacy && badlinkname && tfogo_checklinkname0 && http2stdlibwrap

package http

import "testing"

// The opt-in pair must actually select the stdlib wrap. If a future edit tightened one condition
// without the other, this configuration would silently compile the legacy or disabled file while
// the tag set claims the wrap is in use.
func TestExtendedConnectAvailableWithStdlibWrap(t *testing.T) {
	if !extendedConnectAvailable {
		t.Fatal("extendedConnectAvailable is false although tfogo_checklinkname0 and http2stdlibwrap are both set")
	}
}
