// The unread-payload budget of a Network Extension box.
//
// The mieru client can stop reading from the server once a session holds more unread payload than a
// budget allows, which applies backpressure instead of allocating the data first. The number is a
// property of the platform's footprint, so it is decided here, once per box, and read from the
// construction context by the outbound that can enforce it - the same ownership as the QUIC window
// policy next door, and for the same reason: a protocol that inspected the build environment would
// give every Box in the process the same answer.

package box

import (
	"context"

	"github.com/sagernet/sing-box/common/receivebudget"
)

// withNetworkExtensionReceiveBudget installs the Network Extension unread-payload budget unless the
// context already carries one.
func withNetworkExtensionReceiveBudget(ctx context.Context) context.Context {
	if _, installed := receivebudget.FromContext(ctx); installed {
		return ctx
	}
	if !networkExtensionBox(ctx) {
		return ctx
	}
	return receivebudget.NewContext(ctx, receivebudget.DefaultNetworkExtension())
}
