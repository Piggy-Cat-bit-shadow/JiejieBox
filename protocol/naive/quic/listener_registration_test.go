package quic

import (
	"testing"

	"github.com/sagernet/sing-box/protocol/naive"
)

// TestRealHTTP3ListenerIsRegistered pins the seam the inbound warning relies on.
//
// ConfigureHTTP3ListenerFunc is non-nil even in builds without QUIC: include/quic_stub.go
// installs a stub that returns C.ErrQUICNotIncluded. HTTP3ListenerAvailable is the flag
// that distinguishes the real implementation, and the configuration warning about an
// unset quic_congestion_control must be gated on it. If the real package ever stops
// setting the flag, the warning silently disappears, so this test fails instead.
func TestRealHTTP3ListenerIsRegistered(t *testing.T) {
	if !naive.HTTP3ListenerAvailable {
		t.Fatal("the QUIC package did not mark the naive HTTP/3 listener as available")
	}
	if naive.ConfigureHTTP3ListenerFunc == nil {
		t.Fatal("the QUIC package did not install ConfigureHTTP3ListenerFunc")
	}
}
