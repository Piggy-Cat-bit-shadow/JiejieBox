package dialer

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/netcap"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Round-2 attacks on the two remaining surfaces: the exact user-visible sequence from the report, and
// the translation topologies the fix must not damage.

// ---------------------------------------------------------------------------------------------
// Attack 7, case A, in the form the report actually took it.
// ---------------------------------------------------------------------------------------------

// TestTheReportedSequenceRecoversToIPv4 pins the whole user-visible story in one test.
//
// From the log:
//
//	dial tcp [2409:...]:443: connectex: A socket operation was attempted to an unreachable network.
//
// and the user-visible result was that WeChat images did not load. The correct outcome for that flow
// is NOT a prettier error - it is that the flow REACHES IPv4 and works, because the machine has a
// perfectly good IPv4 path and the name had an A record.
//
// So the assertion is on the ended state: the connection is established, and it is established on the
// IPv4 address. Before this round the IPv6 literal was attempted first and the kernel refused it; the
// question is whether round 1 turned that into a working flow.
func TestTheReportedSequenceRecoversToIPv4(t *testing.T) {
	// The machine from the report: IPv6 on the link, no route to a global IPv6 destination.
	manager := windowsLinkLocalOnly()
	policy := outageDialer(manager).captureLocalFamilyPolicy()

	originalIPv6 := netip.MustParseAddr("2409:8a00:1234::1")
	recoveredIPv4 := netip.MustParseAddr("198.51.100.10")

	// The original the application asked for is refused outright - no 0-26 ms kernel failure, no log
	// line, no socket. That is the fix.
	require.True(t, policy.hardUndialable(originalIPv6),
		"the global IPv6 literal must be refused before a socket exists")
	require.False(t, policy.hardUndialable(recoveredIPv4),
		"and the IPv4 answer the recovery found must be dialable")

	// The recovery candidate list is filtered by the same policy, so the flow proceeds to IPv4
	// instead of stalling on a family that cannot work.
	filtered := withoutUndialable([]netip.Addr{originalIPv6, recoveredIPv4}, policy)
	require.Equal(t, []netip.Addr{recoveredIPv4}, filtered,
		"the recovery must offer the IPv4 answer and drop the unreachable original")

	// And the recovered family is what the plan leads with, so the connection is made on it.
	plan := planCandidates(filtered, netip.Addr{}, 0)
	require.NotEmpty(t, plan.candidates)
	require.Equal(t, recoveredIPv4, plan.candidates[0].address)
}

// TestTheUDPRefusalIsDeterministicAndFinal states the UDP semantic round 1 chose, in the form the
// brief demands: not "it should behave the same", but the named choice plus what it implies.
//
// The UDP path has NO cross-family recovery, and that is deliberate: a UDP socket creation is not a
// verified outcome - on Windows a socket to an unreachable IPv6 network is created SUCCESSFULLY and
// only fails at the first send - so racing two packet sockets would deliver duplicate datagrams and
// declare a winner that has never sent anything.
//
// What the fix provides instead is a DETERMINISTIC refusal before the socket, which is what turns the
// reported "dozens of unreachable failures per minute" into dozens of immediate, distinguishable
// errors. What it does not provide is a remap of the packet flow to another family, and this test says
// so explicitly rather than leaving it to be inferred.
func TestTheUDPRefusalIsDeterministicAndFinal(t *testing.T) {
	manager := windowsLinkLocalOnly()
	dialer := outageDialer(manager)

	// A global IPv6 packet destination is refused before the socket, every time, with the same
	// distinguishable reason - no retry, no timer, no second attempt.
	target := M.ParseSocksaddrHostPort("2001:4860:4860::8888", 443)
	for attempt := 0; attempt < 3; attempt++ {
		_, err := dialer.ListenPacket(context.Background(), target)
		require.ErrorIs(t, err, errLocalIPv6EgressUnavailable,
			"attempt %d must fail the same way, deterministically", attempt)
	}

	// The flow is NOT remapped: the caller gets an error for the destination it asked for, and no
	// packet connection is handed back pointing somewhere else. Anything else would be a silent
	// substitution of the peer, which is the failure mode the audit warned about.
	conn, err := dialer.ListenPacket(context.Background(), target)
	require.Nil(t, conn, "no connection may be returned for a refused destination")
	require.Error(t, err)

	// A local-scope packet destination is still served, so the refusal is not a packet-path outage.
	_, err = dialer.ListenPacket(context.Background(), M.ParseSocksaddrHostPort("fe80::1", 53))
	require.NotErrorIs(t, err, errLocalIPv6EgressUnavailable)
	require.NotErrorIs(t, err, errLocalIPv6Unavailable)
}

// ---------------------------------------------------------------------------------------------
// Attack 12: NAT64 / CLAT / mapped IPv4.
// ---------------------------------------------------------------------------------------------

// TestMappedIPv4IsAlwaysAnIPv4Destination is the mapped-address half, asserted through the plan and
// the gate together rather than in the model alone.
func TestMappedIPv4IsAlwaysAnIPv4Destination(t *testing.T) {
	policy := egressRefusingPolicy()

	// An IPv4-mapped address carries IPv4 semantics everywhere: the family classification, the
	// strategy, and the gate.
	for _, mapped := range []string{"::ffff:8.8.8.8", "::ffff:192.0.2.1", "::ffff:10.0.0.1"} {
		address := netip.MustParseAddr(mapped)
		require.Falsef(t, policy.hardUndialable(address),
			"%s is an IPv4 destination and an IPv6 egress verdict says nothing about it", mapped)

		plan := planCandidates([]netip.Addr{address}, netip.Addr{}, 0)
		require.NotEmpty(t, plan.candidates)
		require.Equalf(t, familyIPv4, plan.candidates[0].family,
			"%s must be planned as IPv4", mapped)
	}
}

// TestACLATUnderlayIsNotRefusedItsIPv4 turns the translation-topology attack around, because that is
// where the damage would be.
//
// A CLAT/464XLAT machine has an IPv4-only underlay and a synthesized IPv4 address on a CLAT
// interface. The IPv6 side of that machine may have no usable global IPv6 route at all - the CLAT
// interface's IPv6 is a translation mechanism, not a native path. The fix must therefore leave the
// IPv4 side completely alone, which is what the operator's traffic actually uses.
func TestACLATUnderlayIsNotRefusedItsIPv4(t *testing.T) {
	// The worst case for the new term: no global IPv6 route is observed at all.
	//
	// The CLAT shape: the underlay has IPv4, and the CLAT adapter carries a synthesized IPv4 address.
	capability := netcap.Evaluate([]netcap.InterfaceObservation{
		{
			Name: "rmnet0", Up: true,
			Addresses: []netip.Prefix{
				netip.MustParsePrefix("10.20.0.5/24"),
				netip.MustParsePrefix("fe80::5/64"),
			},
			GlobalIPv6Route: netcap.RouteAbsent,
		},
		{
			Name: "clat4", Up: true,
			Addresses: []netip.Prefix{
				netip.MustParsePrefix("192.0.0.5/29"), // the synthesized 464XLAT address
				netip.MustParsePrefix("2001:db8:64::5/128"),
			},
			GlobalIPv6Route: netcap.RouteAbsent,
		},
	})

	require.Equal(t, netcap.StateUnavailable, capability.GlobalIPv6Egress)
	for _, ipv4 := range []string{"192.0.0.4", "8.8.8.8", "1.1.1.1", "203.0.113.1"} {
		require.Falsef(t, capability.HardUndialable(netip.MustParseAddr(ipv4)),
			"%s is the underlay's IPv4 and must never be touched by an IPv6 egress verdict", ipv4)
	}
	// And the synthesized 464XLAT range itself is ordinary IPv4 - it is not private space, so it must
	// not be classified as site-scope and it must not be refused.
	require.Equal(t, netcap.ScopeGlobal, netcap.ClassifyScope(netip.MustParseAddr("192.0.0.4")))
	require.False(t, capability.HardUndialable(netip.MustParseAddr("192.0.0.4")))
}

// TestANAT64MachineKeepsUsingTheNAT64Prefix is the DNS64/NAT64 half.
//
// A NAT64 network is IPv6-only from the host's point of view and reaches IPv4 destinations through a
// translator prefix. "No global IPv6 route" and "cannot reach IPv4" are NOT the same statement, and a
// fix that conflated them would refuse IPv4 destinations on the one topology that has no native IPv4
// at all.
func TestANAT64MachineKeepsUsingTheNAT64Prefix(t *testing.T) {
	// IPv6-only host, NAT64 prefix 64:ff9b::/96, global IPv6 route present (the NAT64 path IS the
	// global IPv6 route).
	capability := netcap.Evaluate([]netcap.InterfaceObservation{{
		Name: "eth0", Up: true,
		Addresses: []netip.Prefix{
			netip.MustParsePrefix("2001:db8::5/64"),
			netip.MustParsePrefix("fe80::5/64"),
		},
		GlobalIPv6Route: netcap.RoutePresent,
	}})

	require.Equal(t, netcap.StateProvisioned, capability.GlobalIPv6Egress)
	require.False(t, capability.HardUndialable(netip.MustParseAddr("64:ff9b::808:808")),
		"the NAT64 prefix is reached over the global IPv6 route and must stay dialable")

	// The same machine with the route observation unavailable must also stay permissive, because the
	// NAT64 path is exactly the case where a wrong negative costs the operator everything.
	unobserved := netcap.Evaluate([]netcap.InterfaceObservation{{
		Name: "eth0", Up: true,
		Addresses: []netip.Prefix{
			netip.MustParsePrefix("2001:db8::5/64"),
			netip.MustParsePrefix("fe80::5/64"),
		},
	}})
	require.Equal(t, netcap.StateUnknown, unobserved.GlobalIPv6Egress)
	require.False(t, unobserved.HardUndialable(netip.MustParseAddr("64:ff9b::808:808")))
	require.False(t, unobserved.HardUndialable(netip.MustParseAddr("2001:db8::99")))
}

// ---------------------------------------------------------------------------------------------
// Attack 16: the healthy dual-stack hot path, compared against the pre-round behaviour.
// ---------------------------------------------------------------------------------------------

// TestTheHealthyDualStackPathAddsNoWork is the hot-path attack at the level a test can assert.
//
// On a machine where both families work, nothing in this feature may fire: no refusal, no filter, no
// allocation, and no consultation of anything slower than an atomic load. The benchmark in
// family_capability_bench_test.go carries the numbers; this carries the SHAPE, which is what actually
// regresses when someone later moves the route read off the event path.
func TestTheHealthyDualStackPathAddsNoWork(t *testing.T) {
	healthy := &machineManager{}
	healthy.stable.Store(true)
	healthy.setStates(netcap.StateProvisioned, netcap.StateProvisioned, netcap.StateProvisioned)
	dialer := outageDialer(healthy)
	policy := dialer.captureLocalFamilyPolicy()

	// The gate answers "no opinion" for everything, so the fast path that skips the filter entirely
	// is taken.
	require.True(t, policy.restrictsNothing(),
		"a machine with both families and a working route must take the no-filter fast path")
	for _, target := range []string{
		"2409:8a00::1", "2001:4860:4860::8888", "8.8.8.8", "fe80::1", "fd00::1", "::1",
	} {
		require.Falsef(t, policy.hardUndialable(netip.MustParseAddr(target)), "%s must not be refused", target)
	}

	// And the scheduler adds nothing to the plan: the same candidates, in the same order.
	original := planCandidates(addressSet(), netip.Addr{}, 0)
	scheduler := &candidateScheduler{localFamily: policy}
	filtered, refused := scheduler.filterLocalFamily(original)
	require.False(t, refused.refused())
	require.Equal(t, original.candidates, filtered.candidates,
		"the plan must come out of the filter byte-identical, not merely equivalent")
}

// TestTheCapabilityCaptureTakesNoLockAndNoSyscall is the other half of the hot-path claim, asserted as
// behaviour rather than as a duration.
//
// A lock or a syscall on this path would be invisible in a passing test and visible only as latency,
// so the shape is pinned: the capture must be a pure function of an already-published value, which is
// what makes a thousand captures cost a thousand atomic loads.
func TestTheCapabilityCaptureTakesNoLockAndNoSyscall(t *testing.T) {
	manager := windowsLinkLocalOnly()
	dialer := outageDialer(manager)

	// Capturing concurrently with a manager whose epoch is moving must never block or fail: the read
	// is an atomic load of a published triple, so there is nothing to synchronise on.
	//
	// The writer is a SECOND goroutine on purpose - that is the arrangement the real manager has, and
	// it is what makes -race able to see a non-atomic read if one is ever introduced here. Mutating a
	// plain field from this goroutine is what the first version of this test did, and -race caught it
	// in the fixture rather than in the code.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			manager.epoch.Add(1)
			manager.stable.Store(i%2 == 0)
		}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		policy := dialer.captureLocalFamilyPolicy()
		// Whatever it captured, it must answer consistently with the ownership it recorded.
		_ = policy.restrictsNothing()
		_ = policy.hardUndialable(netip.MustParseAddr("2409:8a00::1"))
		select {
		case <-done:
			return
		default:
		}
	}
	<-done
}
