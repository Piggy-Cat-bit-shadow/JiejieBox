package dialer

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/common/netcap"

	"github.com/stretchr/testify/require"
)

// A dial into a named network namespace must not be refused from a host sample (§D).
//
// # The defect these pin
//
// `captureLocalFamilyPolicy` reads a machine-wide capability: the interfaces and routes THIS PROCESS
// can see. A dial with netns set leaves that environment and enters the target namespace before the
// socket exists, so the sample describes a different network. Trusting it refuses the wrong
// destinations - a host without global IPv6 egress refuses a global IPv6 target the namespace can
// reach - and the upper layers filter the candidate out before the leaf even tries.
//
// These verify the shared decision on any platform. The end-to-end mismatch needs a real namespace
// (Linux, CAP_SYS_ADMIN) and is recorded as a device acceptance rather than claimed here:
//
//	sudo ip netns add no-v6      # a namespace with no IPv6 addressing
//	sudo ip netns add with-v6    # a namespace with IPv6 and a default route
//	# host: IPv6 absent, namespace with-v6: IPv6 present -> an IPv6 target must be dialled
//	# host: IPv6 present, namespace no-v6: IPv6 absent -> nothing may be refused before setns
func TestANamespacedDialDoesNotTrustTheHostFamilyVerdict(t *testing.T) {
	globalIPv6 := netip.MustParseAddr("2001:4860:4860::8888")

	t.Run("host has no global IPv6 egress", func(t *testing.T) {
		// The reported machine: IPv6 addressing present, no public IPv6 route.
		manager := windowsLinkLocalOnly()

		host := outageDialer(manager)
		require.ErrorIs(t, host.captureLocalFamilyPolicy().hardUndialableError(globalIPv6),
			errLocalIPv6EgressUnavailable,
			"the control: a host sampled on its own must still refuse what it proved it cannot reach")

		namespaced := outageDialer(manager)
		namespaced.netns = "target"
		policy := namespaced.captureLocalFamilyPolicy()

		require.NoError(t, policy.hardUndialableError(globalIPv6),
			"the sample describes the host namespace; the socket is created in the target one, so the "+
				"verdict is evidence about a network this dial does not use")
		require.False(t, policy.hardUndialable(globalIPv6))
		require.True(t, policy.restrictsNothing())
		require.True(t, policy.ownership.owns(),
			"ownership is kept: the hand-over checks still need it, and dropping it would remove a "+
				"protection rather than a guess")
	})

	t.Run("host has no IPv6 addressing at all", func(t *testing.T) {
		manager := &machineManager{}
		manager.stable.Store(true)
		manager.setStates(netcap.StateProvisioned, netcap.StateUnavailable, netcap.StateUnavailable)

		host := outageDialer(manager)
		require.ErrorIs(t, host.captureLocalFamilyPolicy().hardUndialableError(globalIPv6),
			errLocalIPv6Unavailable)

		namespaced := outageDialer(manager)
		namespaced.netns = "target"
		require.NoError(t, namespaced.captureLocalFamilyPolicy().hardUndialableError(globalIPv6),
			"a namespace may carry the family the host does not")
	})
}

// TestNamespacedDialDoesNotDropCandidatesAtTheAdmissionLayer is the upper half of the same decision.
//
// The leaf gate is not the only one: candidate admission and the scheduler's plan filter read the
// same policy, and dropping the candidate there means the leaf is never reached at all.
func TestNamespacedDialDoesNotDropCandidatesAtTheAdmissionLayer(t *testing.T) {
	manager := windowsLinkLocalOnly()

	namespaced := outageDialer(manager)
	namespaced.netns = "target"
	policy := namespaced.captureLocalFamilyPolicy()

	scheduler := &candidateScheduler{localFamily: policy}
	addresses := []netip.Addr{
		netip.MustParseAddr("2001:4860:4860::8888"),
		netip.MustParseAddr("192.0.2.1"),
	}
	filtered, refused := scheduler.filterLocalFamily(planCandidates(addresses, netip.Addr{}, 0))
	require.False(t, refused.refused(),
		"the plan filter must not refuse a candidate from a verdict about another namespace")
	require.Len(t, filtered.candidates, len(addresses),
		"and it must leave every candidate for the real socket to decide")

	admission := admissionForTest(namespaced)
	require.True(t, admission.admits(addresses[0]),
		"candidate admission reads the same policy and must agree")
}
