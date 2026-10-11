package transport

import (
	"testing"

	"github.com/sagernet/sing-box/constant"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

// TestRequirementFor pins each DNS transport against the implementation that decides it, under the
// H-PROVABLE contract.
//
// The entry a name-based guess gets wrong is `udp`: it needs UDP for ANY query and TCP only for the
// truncation RETRY, because an ordinary response completes over UDP alone. Listing TCP as
// hard-required refused a UDP-only detour that answers every normal query.
func TestRequirementFor(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		transportType string
		allOf         []string
		conditional   []string
	}{
		"udp needs UDP for any query, TCP only for the retry": {
			transportType: constant.DNSTypeUDP,
			allOf:         []string{N.NetworkUDP},
			conditional:   []string{N.NetworkTCP},
		},
		"tcp":   {transportType: constant.DNSTypeTCP, allOf: []string{N.NetworkTCP}},
		"tls":   {transportType: constant.DNSTypeTLS, allOf: []string{N.NetworkTCP}},
		"https": {transportType: constant.DNSTypeHTTPS, allOf: []string{N.NetworkTCP}},
		"quic (DoQ)": {
			transportType: constant.DNSTypeQUIC,
			allOf:         []string{N.NetworkUDP},
		},
		"h3 (DoH3)": {
			transportType: constant.DNSTypeHTTP3,
			allOf:         []string{N.NetworkUDP},
		},
	} {
		name, testCase := name, testCase
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requirement := RequirementFor(testCase.transportType, nil)
			require.ElementsMatch(t, testCase.allOf, requirement.AllOf)
			require.ElementsMatch(t, testCase.conditional, requirement.Conditional)
			require.Empty(t, requirement.AnyOf, "no DNS transport has a disjunctive requirement")
		})
	}

	// The load-bearing assertion: TCP must NOT be in the hard set for `udp`. Promoting it is the
	// M-A2 mutation, and it refuses a UDP-only detour that answers every ordinary query.
	udp := RequirementFor(constant.DNSTypeUDP, nil)
	require.NotContains(t, udp.AllOf, N.NetworkTCP,
		"a truncated response is retried over TCP, but an ordinary response never touches it, so "+
			"TCP is a retry capability rather than a startup requirement")
	require.Contains(t, udp.Conditional, N.NetworkTCP,
		"and it is recorded so a diagnostic can explain what is lost")

	// The unmodelled types must report NO requirement rather than a guessed one: they either dial
	// nothing, delegate to another transport, or reach the network through an endpoint rather than
	// through the namespace a detour names.
	for _, unmodelled := range []string{
		constant.DNSTypeLocal, constant.DNSTypeHosts, constant.DNSTypeFakeIP,
		constant.DNSTypeGroup, constant.DNSTypeDHCP, constant.DNSTypeMDNS,
		constant.DNSTypeTailscale, constant.DNSTypeOpenConnect, constant.DNSTypeOpenVPN,
		"something-out-of-tree",
	} {
		requirement := RequirementFor(unmodelled, nil)
		require.Empty(t, requirement.AllOf, "%s must stay unmodelled", unmodelled)
		require.Empty(t, requirement.AnyOf, "%s must stay unmodelled", unmodelled)
		require.Empty(t, requirement.Conditional, "%s must stay unmodelled", unmodelled)
	}
}
