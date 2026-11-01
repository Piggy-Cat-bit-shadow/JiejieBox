package dialer

import (
	"context"
	"net/netip"
	"sync/atomic"
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
//
// Every field is atomic, because the real manager publishes this triple from a network-event goroutine
// while dials read it from theirs. A fixture with plain fields would let a test that moves the network
// mid-dial race - and did: the round-2 attack on the hot path was caught by -race writing these fields
// from a second goroutine, which is the same defect the atomic publisher exists to prevent.
type machineManager struct {
	// NetworkManager is embedded and nil: the machine this models reports addressing, not manager
	// methods, and every method it answers is one of the optional capabilities.
	adapter.NetworkManager
	epoch  atomic.Uint64
	stable atomic.Bool
	ipv4   atomic.Uint32
	ipv6   atomic.Uint32
	egress atomic.Uint32
}

func (m *machineManager) setStates(ipv4 netcap.FamilyState, ipv6 netcap.FamilyState, egress netcap.FamilyState) {
	m.ipv4.Store(uint32(ipv4))
	m.ipv6.Store(uint32(ipv6))
	m.egress.Store(uint32(egress))
}

func (m *machineManager) NetworkFamilyCapabilitySnapshot() (uint64, bool, adapter.NetworkFamilyCapability) {
	return m.epoch.Load(), m.stable.Load(), adapter.NetworkFamilyCapability{
		Capability: netcap.FamilyCapability{
			IPv4:             netcap.FamilyState(m.ipv4.Load()),
			IPv6:             netcap.FamilyState(m.ipv6.Load()),
			GlobalIPv6Egress: netcap.FamilyState(m.egress.Load()),
		},
	}
}

func (m *machineManager) NetworkTransitionSnapshot() (uint64, bool) {
	return m.epoch.Load(), m.stable.Load()
}

// windowsLinkLocalOnly is the reported machine: IPv6 present on the link, no global IPv6 route, and
// routes that were actually read.
func windowsLinkLocalOnly() *machineManager {
	manager := &machineManager{}
	manager.stable.Store(true)
	// IPv6 EXISTS - a link-local destination on this machine really is reachable - and no global IPv6
	// destination is. Those two facts at once are the whole design.
	manager.setStates(netcap.StateProvisioned, netcap.StateProvisioned, netcap.StateUnavailable)
	return manager
}

func outageDialer(manager *machineManager) *DefaultDialer {
	return &DefaultDialer{network: newNetworkObserver(manager)}
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
	require.Equal(t, familyIPv6, refused.family, "the global IPv6 candidate is the one refused")
	require.Equal(t, address6, refused.address,
		"the refused destination is carried so the reason can name the machine's real verdict - here "+
			"the egress fact, not an absent family")
	require.Len(t, filtered.candidates, 1)
	require.Equal(t, address4, filtered.candidates[0].address)

	// The control that keeps the LAN working: a link-local IPv6 candidate is NOT refused, so the
	// scheduler still races it against the IPv4 one.
	localSet := []netip.Addr{netip.MustParseAddr("fe80::1"), address4}
	filteredLocal, refusedLocal := scheduler.filterLocalFamily(planCandidates(localSet, netip.Addr{}, 0))
	require.False(t, refusedLocal.refused(),
		"a link-local destination is reached over the link, not over the public path this verdict is about")
	require.Len(t, filteredLocal.candidates, 2)
}

// TestTheRefusalNamesTheRightFact pins the diagnostic, because the two IPv6 refusals mean different
// things to whoever reads the log - and telling a user "your machine has no IPv6" when it has a
// perfectly good link-local address is a support ticket that goes nowhere.
func TestTheRefusalNamesTheRightFact(t *testing.T) {
	// (a) no IPv6 addressing at all -> the family verdict.
	noIPv6 := &machineManager{}
	noIPv6.stable.Store(true)
	noIPv6.setStates(netcap.StateProvisioned, netcap.StateUnavailable, netcap.StateUnavailable)
	errA := outageDialer(noIPv6).captureLocalFamilyPolicy().hardUndialableError(netip.MustParseAddr("2001:db8::1"))
	require.ErrorIs(t, errA, errLocalIPv6Unavailable)
	require.NotErrorIs(t, errA, errLocalIPv6EgressUnavailable)
	require.Contains(t, errA.Error(), "no interface on this machine carries an ipv6 address")

	// (b) IPv6 addressing present, no public route -> the egress verdict.
	errB := outageDialer(windowsLinkLocalOnly()).captureLocalFamilyPolicy().hardUndialableError(netip.MustParseAddr("2001:db8::1"))
	require.ErrorIs(t, errB, errLocalIPv6EgressUnavailable)
	require.NotErrorIs(t, errB, errLocalIPv6Unavailable)
	require.Contains(t, errB.Error(), "no route to a global ipv6 destination")
}

// TestAWorkingGlobalIPv6IsNeverRefused is the fail-open control, and the one that would catch a
// catastrophic mistake: on a machine whose global IPv6 works, and on one where the routes could not be
// read at all, nothing about this feature may fire.
func TestAWorkingGlobalIPv6IsNeverRefused(t *testing.T) {
	build := func(egress netcap.FamilyState) *machineManager {
		manager := &machineManager{}
		manager.stable.Store(true)
		manager.setStates(netcap.StateProvisioned, netcap.StateProvisioned, egress)
		return manager
	}
	working := build(netcap.StateProvisioned)
	// No route reader on this platform, or the read failed.
	unobserved := build(netcap.StateUnknown)
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
	build := func(stable bool) *machineManager {
		manager := &machineManager{}
		manager.epoch.Store(4)
		manager.stable.Store(stable)
		manager.setStates(netcap.StateProvisioned, netcap.StateProvisioned, netcap.StateUnavailable)
		return manager
	}
	for name, manager := range map[string]*machineManager{
		"settled":   build(true),
		"unsettled": build(false),
	} {
		t.Run(name, func(t *testing.T) {
			policy := outageDialer(manager).captureLocalFamilyPolicy()
			target := netip.MustParseAddr("2001:db8::1")

			if !manager.stable.Load() {
				require.False(t, policy.hardUndialable(target),
					"a capture taken during a transition describes no network, so it refuses nothing")
				require.NoError(t, policy.hardUndialableError(target))
				return
			}

			require.True(t, policy.hardUndialable(target), "control: settled and current, so it refuses")

			// A reset moves the generation on, and the same capture must stop refusing.
			manager.epoch.Add(1)
			require.False(t, policy.hardUndialable(target),
				"a verdict superseded by a reset must not refuse a destination on the new network")
			require.NoError(t, policy.hardUndialableError(target))
		})
	}
}

func (m *machineManager) NetworkEnvironment() uint64 { return 0 }
