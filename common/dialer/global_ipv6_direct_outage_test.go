package dialer

import (
	"context"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The reported 0.1.10 Windows outage, at the gate.
//
// A user could not send WeChat images and saw Douyin media stall, with dozens of unrelated targets in
// the log:
//
//	dial tcp [2409:...]:443: connectex: A socket operation was attempted to an unreachable network.
//	open UDP connection to [2001:4860:4860::8888]:443: connect: ... unreachable network.
//
// failing in 0-26ms - the kernel saying "no route", not a server refusing. The machine behind it is
// ordinary: an IPv4 address, a fe80:: link-local address, and no global IPv6 route.
//
// common/netcap's tests prove the MODEL. These prove the WIRING: that the real localFamilyPolicy a
// DefaultDialer builds refuses a global IPv6 destination before any socket exists, and leaves every
// other destination alone.
//
// # Why the assertions here stop at the gate
//
// They have to. A gate that does NOT fire continues into a real socket syscall, which a unit test
// cannot complete against a public address and must not attempt - using a cancelled context to abort
// early would only prove that the refusal happened to be checked first. So each case asserts the
// DECISION, which is the whole of the new behaviour, and the destination that must still be attempted
// is asserted as "not refused". test/jiejie and the Windows builds cover what happens after.

// machineManager is a network manager that reports exactly the three facts this feature consumes.
type machineManager struct {
	epoch  uint64
	stable bool
	ipv4   netcap.FamilyState
	ipv6   netcap.FamilyState
	egress netcap.FamilyState
}

func (m *machineManager) NetworkFamilyCapabilitySnapshot() (uint64, bool, adapter.NetworkFamilyCapability) {
	return m.epoch, m.stable, adapter.NetworkFamilyCapability{
		Capability: netcap.FamilyCapability{
			IPv4:             m.ipv4,
			IPv6:             m.ipv6,
			GlobalIPv6Egress: m.egress,
		},
	}
}

func (m *machineManager) NetworkTransitionSnapshot() (uint64, bool) { return m.epoch, m.stable }

// windowsLinkLocalOnly is the reported machine: IPv6 present on the link, no global IPv6 route, and
// routes that were actually read.
func windowsLinkLocalOnly() *machineManager {
	return &machineManager{
		stable: true,
		// IPv6 EXISTS - a link-local destination on this machine really is reachable - and no global
		// IPv6 destination is. Those two facts at once are the whole design.
		ipv4:   netcap.StateProvisioned,
		ipv6:   netcap.StateProvisioned,
		egress: netcap.StateUnavailable,
	}
}

func outageDialer(manager *machineManager) *DefaultDialer {
	return &DefaultDialer{familySnapshot: manager.NetworkFamilyCapabilitySnapshot}
}

func globalIPv6Targets() []netip.Addr {
	return []netip.Addr{
		netip.MustParseAddr("2409:8a00:1234::1"),    // the prefix family from the log
		netip.MustParseAddr("2001:4860:4860::8888"), // the exact UDP destination from the log
		netip.MustParseAddr("2606:4700:4700::1111"),
		netip.MustParseAddr("2a00:1450:4001:800::200e"),
	}
}

// TestGlobalIPv6IsRefusedBeforeASocketExists is the reported failure at the real gate.
func TestGlobalIPv6IsRefusedBeforeASocketExists(t *testing.T) {
	dialer := outageDialer(windowsLinkLocalOnly())

	for _, target := range globalIPv6Targets() {
		err := dialer.captureLocalFamilyPolicy().hardUndialableError(target)
		require.ErrorIsf(t, err, errLocalIPv6EgressUnavailable,
			"%s must be refused with the EGRESS reason: this machine HAS ipv6, and reporting the "+
				"family reason instead would send the user hunting for a missing address that is "+
				"right there", target)
		require.NotErrorIsf(t, err, errLocalIPv6Unavailable,
			"%s: the family verdict is a different fact and must not be reused", target)
	}
}

// TestTheRefusalIsPerDestinationNotPerMachine is the anti-regression that matters most.
//
// A fix that refused IPv6 wholesale would pass the test above and break the campus, the LAN peer and
// the local resolver - the exact failure mode the design notes call "enlarging the public failure into
// the LAN". Every one of these must survive.
func TestTheRefusalIsPerDestinationNotPerMachine(t *testing.T) {
	policy := outageDialer(windowsLinkLocalOnly()).captureLocalFamilyPolicy()

	for _, allowed := range []string{
		"fe80::1",        // link-local: the fe80:: address is exactly what makes it work
		"fd00:1234::5",   // unique-local: the campus-internal case
		"fc00::1",        // unique-local, the other half of fc00::/7
		"::1",            // loopback
		"127.0.0.1",      // loopback, v4
		"192.168.1.1",    // LAN peer
		"10.0.0.1",       // LAN peer
		"172.16.0.9",     // LAN peer
		"8.8.8.8",        // public IPv4: untouched by an IPv6 egress verdict
		"1.1.1.1",        //
		"::ffff:8.8.8.8", // an IPv4-mapped address IS an IPv4 destination
	} {
		target := netip.MustParseAddr(allowed)
		require.Falsef(t, policy.hardUndialable(target), "%s must not be refused", allowed)
		require.NoErrorf(t, policy.hardUndialableError(target), "%s must not be refused", allowed)
	}
}

// TestTheSameGateAnswersBothDialAndPacketPaths pins that the stream gate and the packet gate cannot
// disagree, because both now ask this one function rather than composing the terms themselves.
//
// The packet gate is the one that mattered in the log: `open UDP connection to [2001:4860:4860::8888]`
// is a connected packet socket, and on Windows a UDP socket to an unreachable IPv6 network is created
// SUCCESSFULLY and only fails at the first send - so without a shared gate the client is handed a dead
// connection instead of an error it could fall back from.
func TestTheSameGateAnswersBothDialAndPacketPaths(t *testing.T) {
	manager := windowsLinkLocalOnly()
	dialer := outageDialer(manager)

	for _, target := range globalIPv6Targets() {
		refusal := dialer.captureLocalFamilyPolicy().hardUndialableError(target)
		require.Error(t, refusal)

		// The dial path refuses it...
		_, dialErr := dialer.DialContext(context.Background(), "tcp", M.SocksaddrFrom(target, 443))
		require.ErrorIs(t, dialErr, errLocalIPv6EgressUnavailable)

		// ...and so does the packet path, for the same destination and the same reason.
		_, packetErr := dialer.ListenPacket(context.Background(), M.SocksaddrFrom(target, 443))
		require.ErrorIs(t, packetErr, errLocalIPv6EgressUnavailable)

		// A wildcard packet destination is not a destination and must not be refused - that is how a
		// listener asks for a socket with no family at all, and the packet transports rely on it.
		_, wildcardErr := dialer.ListenPacket(context.Background(), M.Socksaddr{Addr: netip.IPv6Unspecified()})
		require.NotErrorIs(t, wildcardErr, errLocalIPv6EgressUnavailable)
		require.NotErrorIs(t, wildcardErr, errLocalIPv6Unavailable)
	}
}

// TestTheSchedulerDropsTheGlobalIPv6Candidate is what a CONNECTED packet socket and every stream go
// through: the candidate is removed before a socket exists, which is the only way a packet path can
// move to the other family - the serial packet loop has no recovery to fall back on, so a refusal
// before socket creation IS its fallback.
func TestTheSchedulerDropsTheGlobalIPv6Candidate(t *testing.T) {
	dialer := outageDialer(windowsLinkLocalOnly())
	policy := dialer.captureLocalFamilyPolicy()
	scheduler := &candidateScheduler{localFamily: policy}

	filtered, refused := scheduler.filterLocalFamily(planCandidates(addressSet(), netip.Addr{}, 0))
	require.Equal(t, familyIPv6, refused, "the global IPv6 candidate is the one refused")
	require.Len(t, filtered.candidates, 1)
	require.Equal(t, address4, filtered.candidates[0].address)

	// The control that keeps the LAN working: a link-local IPv6 candidate is NOT refused, so the
	// scheduler still races it against the IPv4 one.
	localSet := []netip.Addr{netip.MustParseAddr("fe80::1"), address4}
	filteredLocal, refusedLocal := scheduler.filterLocalFamily(planCandidates(localSet, netip.Addr{}, 0))
	require.Equal(t, familyInvalid, refusedLocal,
		"a link-local destination is reached over the link, not over the public path this verdict is about")
	require.Len(t, filteredLocal.candidates, 2)
}

// TestTheRefusalNamesTheRightFact pins the diagnostic, because the two IPv6 refusals mean different
// things to whoever reads the log - and telling a user "your machine has no IPv6" when it has a
// perfectly good link-local address is a support ticket that goes nowhere.
func TestTheRefusalNamesTheRightFact(t *testing.T) {
	// (a) no IPv6 addressing at all -> the family verdict.
	noIPv6 := &machineManager{
		stable: true,
		ipv4:   netcap.StateProvisioned,
		ipv6:   netcap.StateUnavailable,
		egress: netcap.StateUnavailable,
	}
	errA := outageDialer(noIPv6).captureLocalFamilyPolicy().hardUndialableError(netip.MustParseAddr("2409:8a00::1"))
	require.ErrorIs(t, errA, errLocalIPv6Unavailable)
	require.NotErrorIs(t, errA, errLocalIPv6EgressUnavailable)
	require.Contains(t, errA.Error(), "no interface on this machine carries an ipv6 address")

	// (b) IPv6 addressing present, no public route -> the egress verdict.
	errB := outageDialer(windowsLinkLocalOnly()).captureLocalFamilyPolicy().hardUndialableError(netip.MustParseAddr("2409:8a00::1"))
	require.ErrorIs(t, errB, errLocalIPv6EgressUnavailable)
	require.NotErrorIs(t, errB, errLocalIPv6Unavailable)
	require.Contains(t, errB.Error(), "no route to a global ipv6 destination")
}

// TestAWorkingGlobalIPv6IsNeverRefused is the fail-open control, and the one that would catch a
// catastrophic mistake: on a machine whose global IPv6 works, and on one where the routes could not be
// read at all, nothing about this feature may fire.
func TestAWorkingGlobalIPv6IsNeverRefused(t *testing.T) {
	working := &machineManager{
		stable: true,
		ipv4:   netcap.StateProvisioned,
		ipv6:   netcap.StateProvisioned,
		egress: netcap.StateProvisioned,
	}
	unobserved := &machineManager{
		stable: true,
		ipv4:   netcap.StateProvisioned,
		ipv6:   netcap.StateProvisioned,
		egress: netcap.StateUnknown, // no route reader on this platform, or the read failed
	}
	for name, manager := range map[string]*machineManager{"working route": working, "routes unread": unobserved} {
		policy := outageDialer(manager).captureLocalFamilyPolicy()
		for _, target := range globalIPv6Targets() {
			require.Falsef(t, policy.hardUndialable(target), "%s: %s must still be dialled", name, target)
			require.NoErrorf(t, policy.hardUndialableError(target), "%s: %s must still be dialled", name, target)
		}
	}
}

// TestOwnershipStillGatesTheEgressVerdict pins that the new term did NOT become a way around
// generation ownership. A stale or mid-transition capture must refuse nothing, egress term included -
// and this is the assertion that keeps the two rounds' work composable rather than additive.
func TestOwnershipStillGatesTheEgressVerdict(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		manager *machineManager
	}{
		{"settled", &machineManager{epoch: 4, stable: true,
			ipv4: netcap.StateProvisioned, ipv6: netcap.StateProvisioned, egress: netcap.StateUnavailable}},
		{"unsettled", &machineManager{epoch: 4, stable: false,
			ipv4: netcap.StateProvisioned, ipv6: netcap.StateProvisioned, egress: netcap.StateUnavailable}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			policy := outageDialer(testCase.manager).captureLocalFamilyPolicy()
			target := netip.MustParseAddr("2409:8a00::1")

			if !testCase.manager.stable {
				require.False(t, policy.hardUndialable(target),
					"a capture taken during a transition describes no network, so it refuses nothing")
				require.NoError(t, policy.hardUndialableError(target))
				return
			}

			require.True(t, policy.hardUndialable(target), "control: settled and current, so it refuses")

			// A reset moves the generation on, and the same capture must stop refusing.
			testCase.manager.epoch++
			require.False(t, policy.hardUndialable(target),
				"a verdict superseded by a reset must not refuse a destination on the new network")
			require.NoError(t, policy.hardUndialableError(target))
		})
	}
}
