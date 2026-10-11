package dns

import (
	"testing"

	"github.com/sagernet/sing-box/constant"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

// TestDNSTransportRequirement pins each type against the implementation that decides it.
//
// The two entries that a name-based guess would get wrong are `udp` and `https`:
//
//   - `udp` needs BOTH networks, because a truncated response is retried over TCP. "UDP DNS needs
//     UDP" is the intuitive rule and it would bless a TCP-less detour that breaks on any response
//     too large for a datagram.
//   - `https` needs TCP ONLY. `h3` is a separate type (constant.DNSTypeHTTP3), so there is no
//     "DoH might use QUIC" branch in this tree to model as optional.
func TestDNSTransportRequirement(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		transportType string
		want          []string
	}{
		"udp needs UDP and TCP for the truncation retry": {
			transportType: constant.DNSTypeUDP,
			want:          []string{N.NetworkUDP, N.NetworkTCP},
		},
		"tcp":        {transportType: constant.DNSTypeTCP, want: []string{N.NetworkTCP}},
		"tls":        {transportType: constant.DNSTypeTLS, want: []string{N.NetworkTCP}},
		"https":      {transportType: constant.DNSTypeHTTPS, want: []string{N.NetworkTCP}},
		"quic (DoQ)": {transportType: constant.DNSTypeQUIC, want: []string{N.NetworkUDP}},
		"h3 (DoH3)":  {transportType: constant.DNSTypeHTTP3, want: []string{N.NetworkUDP}},
	} {
		name, testCase := name, testCase
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requirement := DNSTransportRequirement(testCase.transportType, nil)
			require.ElementsMatch(t, testCase.want, requirement.Required)
		})
	}

	// The unmodelled types must report NO requirement rather than a guessed one. These either dial
	// nothing, delegate to another transport, or reach the network through an endpoint rather than
	// through the namespace a detour names - so constraining their detour would invent a rule.
	for _, unmodelled := range []string{
		constant.DNSTypeLocal, constant.DNSTypeHosts, constant.DNSTypeFakeIP,
		constant.DNSTypeGroup, constant.DNSTypeDHCP, constant.DNSTypeMDNS,
		constant.DNSTypeTailscale, constant.DNSTypeOpenConnect, constant.DNSTypeOpenVPN,
		"something-out-of-tree",
	} {
		requirement := DNSTransportRequirement(unmodelled, nil)
		require.Empty(t, requirement.Required,
			"%s must stay unmodelled rather than guessed", unmodelled)
		require.Empty(t, requirement.Optional,
			"%s must not claim an optional network either", unmodelled)
	}
}
