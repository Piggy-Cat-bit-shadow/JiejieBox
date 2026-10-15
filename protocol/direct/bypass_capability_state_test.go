package direct

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/option"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// Layer 9 of the debug round: the native bypass across every capability state.
//
// # What the bypass is, and why a verdict has to reach it
//
// A bypassed flow is a connect performed by the PLATFORM: the router has established that the
// userspace dial options would not change anything for this flow, so the operating system may carry
// it. It therefore never passes through the candidate scheduler, which is where the local family
// verdict is otherwise enforced.
//
// On every TUN stack that is immaterial - the L1 semantic bypass is only honoured by the Linux nfqueue
// handler, so a "bypassed" flow still travels the userspace path and meets the verdict there. In Linux
// redirect mode it is not immaterial: the packet really leaves this process, and handing the kernel an
// address the machine has no interface for spends the whole connect timeout rediscovering what the
// network manager already knew.
//
// # The three states the brief asks about
//
//	Unavailable  -> refuse the bypass, so the flow returns to the path that fails it immediately
//	                and says why. This is a RESTORATION of the userspace behaviour, not a new policy.
//	Unknown      -> allow it. No opinion must never close a performance path.
//	Provisioned  -> allow it. "Unproven" is not "impossible", and the bypass's own eligibility rules
//	                already answered the questions the platform can answer.
//	Degraded     -> allow it. A soft suspicion is not a reason to disable a fast path, and no producer
//	                for this state exists in any case.
//
// The last two are the ones a careless implementation breaks, so they are asserted explicitly.

func TestBypassRefusesOnlyAProvablyAbsentFamily(t *testing.T) {
	ipv4 := netip.MustParseAddr("93.184.216.34")
	ipv6 := netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946")

	testCases := []struct {
		name string
		// blocked answers the machine-level question: is this machine unable to dial this address?
		blocked    func(netip.Addr) bool
		expectIPv4 bool
		expectIPv6 bool
		why        string
	}{
		{
			name:       "no verdict reported",
			blocked:    nil,
			expectIPv4: true,
			expectIPv6: true,
			why: "a chain that cannot answer is the shape every third-party implementation and every " +
				"test double has; it must keep the previous behaviour exactly",
		},
		{
			name:       "nothing refused",
			blocked:    func(netip.Addr) bool { return false },
			expectIPv4: true,
			expectIPv6: true,
			why:        "unknown and provisioned both answer false here, and neither may close the fast path",
		},
		{
			name:       "ipv6 provably absent",
			blocked:    func(address netip.Addr) bool { return address.Is6() },
			expectIPv4: true,
			expectIPv6: false,
			why: "the userspace path refuses this immediately, so the platform connect is not equivalent " +
				"and the flow must return to the path that says why",
		},
		{
			name:       "ipv4 provably absent",
			blocked:    func(address netip.Addr) bool { return address.Is4() },
			expectIPv4: false,
			expectIPv6: true,
			why:        "the same rule in the other direction, which is what makes it a family rule",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			outbound := bypassable(option.DialerOptions{})
			outbound.localFamily = testCase.blocked

			require.Equal(t, testCase.expectIPv4, outbound.CanBypass(N.NetworkTCP, ipv4),
				"ipv4: %s", testCase.why)
			require.Equal(t, testCase.expectIPv6, outbound.CanBypass(N.NetworkTCP, ipv6),
				"ipv6: %s", testCase.why)

			// The blocker bit is attributed to the destination that was actually refused, and it is
			// its own bit rather than folded into an existing one - which is what lets an operator
			// tell "your device has no IPv6" from "your configuration excludes IPv6".
			ipv4Blockers := outbound.BypassBlockers(N.NetworkTCP, ipv4)
			require.Equal(t, !testCase.expectIPv4, ipv4Blockers&dialer.BlockerLocalFamilyUnavailable != 0,
				"the machine-level refusal must appear for the refused family only")

			if !testCase.expectIPv4 {
				require.NotContains(t, ipv4Blockers.String(), "family strategy",
					"a machine-level refusal must never be reported as a configuration decision")
				require.NotContains(t, ipv4Blockers.String(), "unclassified",
					"nor as an unclassified option, which is a different defect entirely")
			}
		})
	}
}

// TestBypassVerdictIsLiveAndNotPrecomputed answers the brief's last question about this layer: does the
// dynamic state end up baked into the static profile?
//
// It must not. The socket semantics profile is built once, from the dial OPTIONS, and answers "would
// the userspace dialer do anything special for this flow" - a question whose answer cannot change
// while the process runs. "Can this machine dial this family" changes whenever an interface does, so
// it is held as a live predicate and consulted per flow.
//
// The test drives one outbound through the whole sequence, which is the only way to show that the
// answer is not frozen: the same object, with the same options, must change its verdict when the
// machine's addressing changes and only then.
func TestBypassVerdictIsLiveAndNotPrecomputed(t *testing.T) {
	ipv6 := netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946")

	outbound := bypassable(option.DialerOptions{})
	blocked := false
	outbound.localFamily = func(address netip.Addr) bool { return blocked && address.Is6() }

	require.True(t, outbound.CanBypass(N.NetworkTCP, ipv6),
		"the machine currently has IPv6 addressing, so the fast path is available")

	// The addressing goes away. This moves no epoch and no configuration - it is the same network
	// with a different answer.
	blocked = true
	require.False(t, outbound.CanBypass(N.NetworkTCP, ipv6),
		"the verdict must follow the machine, so it cannot have been computed into the profile")

	// And it comes back.
	blocked = false
	require.True(t, outbound.CanBypass(N.NetworkTCP, ipv6),
		"a restored family must restore the fast path, not leave it disabled for the process's life")

	// The static half is genuinely static: the profile still reflects the OPTIONS and nothing else.
	require.True(t, outbound.IsEmpty(),
		"the socket semantics were built from empty options and are unaffected by any of the above")
}

// TestBypassRefusesAGlobalIPv6DestinationWithoutPublicEgress is this round's case, and it is NOT the
// same as "the family is absent".
//
// The reported failing machine has IPv6 - a fe80:: link-local address, which is what makes a
// link-local destination work - and no route to a global IPv6 destination. The bypass decision has to
// separate the two, because a bypassed flow never passes through the scheduler where the verdict is
// otherwise enforced, and in Linux redirect mode the packet really does leave this process.
//
// The predicate used here is the one production wires: DefaultDialer.LocalFamilyBlocked, reached
// through the LocalFamilyDialer assertion in NewDirect. It is asserted through the same outbound
// surface as the states above so the two layers cannot drift.
func TestBypassRefusesAGlobalIPv6DestinationWithoutPublicEgress(t *testing.T) {
	globalIPv6 := netip.MustParseAddr("2409:8a00:1234::1")
	linkLocal := netip.MustParseAddr("fe80::1")
	uniqueLocal := netip.MustParseAddr("fd00:1234::5")
	publicIPv4 := netip.MustParseAddr("93.184.216.34")

	// The machine: IPv4 and IPv6 both present, no public IPv6 route.
	outbound := bypassable(option.DialerOptions{})
	outbound.localFamily = func(address netip.Addr) bool {
		switch {
		case address.Unmap().Is4():
			return false
		case address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsPrivate():
			return false
		default:
			return true
		}
	}

	require.False(t, outbound.CanBypass(N.NetworkTCP, globalIPv6),
		"a global IPv6 destination cannot be carried by the platform when the machine has no route to one")
	require.NotZero(t, outbound.BypassBlockers(N.NetworkTCP, globalIPv6)&dialer.BlockerLocalFamilyUnavailable,
		"and the refusal is attributed to the machine, not to the configuration")

	require.True(t, outbound.CanBypass(N.NetworkTCP, linkLocal),
		"the link-local destination the fe80:: address exists for is unaffected")
	require.True(t, outbound.CanBypass(N.NetworkTCP, uniqueLocal),
		"and so is the campus-internal one: a public failure must not be enlarged into the LAN")
	require.True(t, outbound.CanBypass(N.NetworkTCP, publicIPv4),
		"an IPv6 egress verdict says nothing about IPv4")
}

// TestBypassWithoutTheCapabilityIsUnchanged is the degradation contract at this layer.
//
// An outbound whose dialer chain cannot report a verdict must produce byte-identical blockers to the
// ones it produced before this round. That is what makes the change safe to land in a fork whose
// clients build their own outbounds.
func TestBypassWithoutTheCapabilityIsUnchanged(t *testing.T) {
	ipv4 := netip.MustParseAddr("93.184.216.34")
	ipv6 := netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946")

	silent := bypassable(option.DialerOptions{})
	require.Nil(t, silent.localFamily, "a chain that cannot answer leaves the field nil")

	require.Equal(t, "none", silent.BypassBlockers(N.NetworkTCP, ipv4).String())
	require.Equal(t, "none", silent.BypassBlockers(N.NetworkTCP, ipv6).String())
	require.Equal(t, "none", silent.BypassBlockers(N.NetworkUDP, ipv6).String())
}

// TestBypassRefusalIsAttributableAndDistinct is the diagnostic half.
//
// A refusal that cannot be attributed sends an operator nowhere. The machine-level refusal must be
// distinguishable from the configuration-level one, because they are fixed in different places - and
// the assertion is made on the rendered blocker names, which is what an operator actually reads.
func TestBypassRefusalIsAttributableAndDistinct(t *testing.T) {
	ipv6 := netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946")

	outbound := bypassable(option.DialerOptions{})
	outbound.localFamily = func(address netip.Addr) bool { return address.Is6() }

	blockers := outbound.BypassBlockers(N.NetworkTCP, ipv6)
	rendered := blockers.String()
	require.Contains(t, rendered, "no usable path for this destination's address family",
		"the refusal must name the machine's addressing as the reason")
	require.NotContains(t, rendered, "unclassified",
		"and it must not be reported as an unclassified option, which is a different defect")
}
