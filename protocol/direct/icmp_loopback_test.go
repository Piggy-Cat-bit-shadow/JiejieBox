package direct

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-tun/ping"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// An ICMP flow to this host's own TUN range must be rejected before it becomes a Port flow.
//
// The direct outbound is the only production FlowOutbound, and ICMP is the only network it serves
// through a Port. The ping Port would send the packet straight back into the TUN's address range,
// so the loop is refused rather than forwarded. Upstream b722a038e61b added exactly this guard; the
// regression it fixes is a routing loop that the router's other blockers cannot see because an ICMP
// flow never reaches canFastBypass's socket-semantics check.
func TestICMPLoopbackToTheTunRangeIsRejected(t *testing.T) {
	h := &Outbound{
		logger:   log.NewNOPFactory().Logger(),
		icmpPort: &ping.Port{},
	}
	h.myAddresses.Store([]netip.Prefix{netip.MustParsePrefix("198.18.0.0/15")})

	require.Equal(t, adapter.PreMatchReject,
		h.PreMatchFlow(N.NetworkICMP, netip.MustParseAddr("198.18.0.1")),
		"an ICMP flow to the TUN's own range must be rejected")
	require.Equal(t, adapter.PreMatchFlow,
		h.PreMatchFlow(N.NetworkICMP, netip.MustParseAddr("93.184.216.34")),
		"an ICMP flow to a real destination still takes the Port")
	require.Equal(t, adapter.PreMatchContinue,
		h.PreMatchFlow(N.NetworkTCP, netip.MustParseAddr("198.18.0.1")),
		"only ICMP takes this branch; TCP uses the normal dial path")
}
