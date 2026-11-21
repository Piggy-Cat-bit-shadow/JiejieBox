// The per-Box platform decisions that are not about one protocol.
//
// Both of the policy installers - the QUIC receive-window policy and the unread-payload budget - ask
// the SAME question: is this box an iOS Network Extension? Keeping the answer in one function is what
// stops the two from drifting into different gates, and it is why the macOS Network Extension has to
// be excluded explicitly rather than by accident: it reports UnderNetworkExtension() == true and
// C.IsIos is false there.

package box

import (
	"context"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/service"
)

// networkExtensionBox reports whether this construction context describes an iOS Network Extension.
func networkExtensionBox(ctx context.Context) bool {
	if !C.IsIos {
		return false
	}
	platformInterface := service.FromContext[adapter.PlatformInterface](ctx)
	return platformInterface != nil && platformInterface.UnderNetworkExtension()
}
