package dns

import (
	"testing"

	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing/common"
)

// The control-plane wrapper must stay transparent to the wrapper-chain walkers.
//
// Both the detour preflight (dialer.InitializeDetour) and the capability check
// (common/dialer.DetourOf) locate the DetourDialer through WithUpstream. When the
// control-plane marker was introduced, the wrapper did not implement it: the detour was
// never initialized at start and the "does not carry" warning was skipped, so a DNS
// transport that could not possibly serve its queries started silently.
//
// This asserts the contract directly: a DetourDialer nested under the wrapper is still
// reachable by the same cast the production preflight uses.
func TestControlPlaneWrapperKeepsTheDetourReachable(t *testing.T) {
	inner := &dialer.DetourDialer{}
	wrapped := controlPlaneDialer{Dialer: inner}

	found, ok := C.Cast[*dialer.DetourDialer](wrapped)
	if !ok {
		t.Fatal("the control-plane wrapper hides the DetourDialer from common.Cast; the detour preflight and the capability warning would be silently skipped")
	}
	if found != inner {
		t.Fatalf("Cast returned a different DetourDialer: %p != %p", found, inner)
	}
}
