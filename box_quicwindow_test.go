package box

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/quicpolicy"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// platformInterfaceProbe is the smallest adapter.PlatformInterface that can answer the one
// question the window policy asks. Every other method is left to the embedded nil interface, which
// is what makes it unusable on purpose: this policy must never touch anything else.
type platformInterfaceProbe struct {
	adapter.PlatformInterface
	underNetworkExtension bool
}

func (p *platformInterfaceProbe) UnderNetworkExtension() bool { return p.underNetworkExtension }

// TestNetworkExtensionQUICReceiveWindowsGate enumerates the gate instead of sampling it, because
// the two facts it is built from are not independent in practice: a macOS Network Extension reports
// UnderNetworkExtension() == true and must still keep the desktop windows, which is exactly the pair
// a GOOS-based gate gets wrong.
func TestNetworkExtensionQUICReceiveWindowsGate(t *testing.T) {
	expected := quicpolicy.DefaultNetworkExtensionReceiveWindows()

	windows, applies := networkExtensionQUICReceiveWindows(true, true)
	require.True(t, applies, "an iOS Network Extension is the one platform this policy is for")
	require.Equal(t, expected, windows)

	for _, testCase := range []struct {
		name                  string
		isIos                 bool
		underNetworkExtension bool
	}{
		{name: "iOS outside a Network Extension", isIos: true},
		{name: "macOS Network Extension", isIos: false, underNetworkExtension: true},
		{name: "desktop", isIos: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, applies := networkExtensionQUICReceiveWindows(testCase.isIos, testCase.underNetworkExtension)
			require.False(t, applies, "only an iOS Network Extension takes the policy")
		})
	}
}

// TestWithNetworkExtensionQUICReceiveWindowsKeepsAnEmbeddersPolicy pins the ownership rule: the box
// consumes the decision, it does not overwrite one an embedder already made.
func TestWithNetworkExtensionQUICReceiveWindowsKeepsAnEmbeddersPolicy(t *testing.T) {
	own := quicpolicy.ReceiveWindows{InitialStream: 2 << 20, MaxStream: 4 << 20}
	ctx := service.ContextWithDefaultRegistry(context.Background())
	ctx = quicpolicy.NewContext(ctx, own)
	ctx = service.ContextWith[adapter.PlatformInterface](ctx, &platformInterfaceProbe{underNetworkExtension: true})

	installed, ok := quicpolicy.FromContext(withNetworkExtensionQUICReceiveWindows(ctx))
	require.True(t, ok)
	require.Equal(t, own, installed, "the embedder's policy must survive the box")
}

// TestWithNetworkExtensionQUICReceiveWindowsWithoutAPlatformInterface covers the embedder that
// builds a box with no platform interface at all: the gate must read that as "not an extension"
// rather than dereference it.
func TestWithNetworkExtensionQUICReceiveWindowsWithoutAPlatformInterface(t *testing.T) {
	ctx := service.ContextWithDefaultRegistry(context.Background())
	ctx = withNetworkExtensionQUICReceiveWindows(ctx)
	_, ok := quicpolicy.FromContext(ctx)
	require.False(t, ok)
}
