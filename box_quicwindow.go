// The QUIC receive-window policy of a Network Extension box.
//
// # Why the box decides this and not the protocol
//
// The protocol constructors are the only place a quic.Config is built, but they do not know what
// platform they are running on, and they must not guess: a protocol that inspected the build
// environment would give every Box in the process the same answer. The box already owns the other
// per-box platform decisions (the build reservation, the packet-up budget), and it receives the
// platform interface the client registered, so the answer is made once, here, and read from the
// construction context by the protocols that need it.
//
// # Why only an iOS Network Extension
//
// That is the platform with an enforced footprint: the extension is terminated by the system at
// roughly 50 MiB, and a receive window is credit a peer may fill while the application is not
// reading. A desktop box, a CLI box, a server inbound and a macOS extension have no such ceiling,
// and narrowing their windows would trade throughput for a budget nobody enforces. The macOS
// extension in particular is deliberately excluded: C.IsIos is false there, and using GOOS=darwin
// as the gate would silently include it.
//
// An embedder that already installed a policy keeps it; the box is a consumer of the decision, not
// its owner.

package box

import (
	"context"

	"github.com/sagernet/sing-box/common/quicpolicy"
	C "github.com/sagernet/sing-box/constant"
)

// withNetworkExtensionQUICReceiveWindows installs the Network Extension receive-window policy
// unless the context already carries one.
func withNetworkExtensionQUICReceiveWindows(ctx context.Context) context.Context {
	if _, installed := quicpolicy.FromContext(ctx); installed {
		return ctx
	}
	windows, applies := networkExtensionQUICReceiveWindows(C.IsIos, networkExtensionBox(ctx))
	if !applies {
		return ctx
	}
	return quicpolicy.NewContext(ctx, windows)
}

// networkExtensionQUICReceiveWindows is the whole gate, as a function of its two facts.
//
// It is separated from the context plumbing so the decision can be enumerated on a host that is not
// iOS: C.IsIos is a compile-time constant, so a test running on Windows can never reach the true
// branch through withNetworkExtensionQUICReceiveWindows, and a gate that is only asserted on the
// device is a gate whose macOS leak would be discovered by a user. The macOS extension is the case
// this excludes: it reports UnderNetworkExtension() == true and IsIos == false, and it must keep the
// desktop windows.
func networkExtensionQUICReceiveWindows(isIos bool, underNetworkExtension bool) (quicpolicy.ReceiveWindows, bool) {
	if !isIos || !underNetworkExtension {
		return quicpolicy.ReceiveWindows{}, false
	}
	return quicpolicy.DefaultNetworkExtensionReceiveWindows(), true
}
