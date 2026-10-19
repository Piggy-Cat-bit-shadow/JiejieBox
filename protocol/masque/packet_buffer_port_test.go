package masque

import (
	"testing"

	tun "github.com/sagernet/sing-tun"
)

// The forward dispatcher hands owned, headroomed buffers only to a port that advertises the
// capability. Compiling this assertion is what keeps the endpoint and the dispatcher in step: if
// either side changes the method set, the build fails here rather than silently falling back to
// the copy path (or, worse, passing borrowed slices to a port that retains them).
func TestClientEndpointImplementsPacketBufferPort(t *testing.T) {
	var _ tun.PacketBufferPort = (*ClientEndpoint)(nil)
}
