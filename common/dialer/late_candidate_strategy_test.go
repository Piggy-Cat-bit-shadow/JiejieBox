package dialer

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
)

// A late candidate must satisfy the operator's policy, not just the machine's capability.
//
// # The gap this closes
//
// A late candidate never passes through `planCandidates`: it is appended to the launch queue straight from
// the resolver's stream. The scheduler filtered it for the local family capability and for nothing else, so
// the strict family strategy was enforced on a late candidate only by whichever producer happened to filter
// it first.
//
// Every current producer does filter (`mergeRecovered` plans the recovered list with the effective strategy
// before feeding it), which means the invariant was CONVENTIONAL: correct today because all three call sites
// remembered, and silently broken by a fourth that did not. These tests pin the structural version - the
// scheduler itself refuses a late candidate the policy excludes - so a future producer cannot reintroduce
// the forbidden family by forgetting a filter.
//
// The ordering matters and is why this is not merely theoretical: the family that arrives LATE is the one
// that answered slowly, and a strict policy is exactly the configuration where the other family must never
// be dialled at all.

// recordingAttemptFunc records every address the scheduler hands to a dial and fails the ones asked to.
//
// The mutex is not decoration: the scheduler runs attempts on their own goroutines, so a slice appended
// without it is a data race the moment two candidates race - which is the normal case here, not an edge.
// The first version of this fixture omitted it and `-race` reported it.
type recordingAttemptFunc struct {
	access sync.Mutex
	tried  []netip.Addr
	failOn map[netip.Addr]bool
}

func (f *recordingAttemptFunc) attempt(ctx context.Context, address netip.Addr) (net.Conn, error) {
	f.access.Lock()
	f.tried = append(f.tried, address)
	f.access.Unlock()
	if f.failOn[address] {
		return nil, errors.New("refused for the test")
	}
	return &lateStubConn{}, nil
}

// lateStubConn is a connection that does nothing, so an attempt can succeed without a transport.
type lateStubConn struct{}

func (c *lateStubConn) Read(p []byte) (int, error)         { return 0, errors.New("closed") }
func (c *lateStubConn) Write(p []byte) (int, error)        { return len(p), nil }
func (c *lateStubConn) Close() error                       { return nil }
func (c *lateStubConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (c *lateStubConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (c *lateStubConn) SetDeadline(t time.Time) error      { return nil }
func (c *lateStubConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *lateStubConn) SetWriteDeadline(t time.Time) error { return nil }

// newCapabilityScheduler builds a scheduler whose local-family view is the machine's real capability shape,
// with both families usable so nothing about the machine can mask a policy decision.
func newCapabilityScheduler(t *testing.T) *candidateScheduler {
	t.Helper()
	return newCapabilityLeaf(netcap.StateProvisioned, netcap.StateProvisioned).newDualStackScheduler(5 * time.Millisecond)
}

// TestALateCandidateTheStrategyExcludesIsNotAttempted is the regression.
func TestALateCandidateTheStrategyExcludesIsNotAttempted(t *testing.T) {
	admittedIPv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")
	forbiddenIPv4 := netip.MustParseAddr("112.53.55.92")

	scheduler := newCapabilityScheduler(t)
	late := make(chan dualStackCandidate, 1)
	late <- dualStackCandidate{address: forbiddenIPv4, family: familyIPv4}
	close(late)

	recorder := &recordingAttemptFunc{failOn: map[netip.Addr]bool{}}
	plan := planCandidates([]netip.Addr{admittedIPv6}, netip.Addr{}, C.DomainStrategyPreferIPv6)

	// The ipv6_only admission rule, stated the way the production wrapper states it.
	admit := func(address netip.Addr) bool {
		return AddressAllowedByStrategy(address, C.DomainStrategyIPv6Only)
	}

	conn, winner, err := scheduler.dialWithLateCandidatesAdmitting(context.Background(), plan, late, admit, recorder.attempt)
	if conn != nil {
		conn.Close()
	}
	require.NoError(t, err, "the admitted candidate is dialable, so the race must win on it")
	require.Equal(t, admittedIPv6, winner)
	require.Equal(t, []netip.Addr{admittedIPv6}, recorder.tried,
		"the late candidate belongs to the family ipv6_only forbids, so it must never be handed to a dial: "+
			"the capability filter cannot see a policy, and every current producer filtering it first makes "+
			"the invariant conventional rather than structural")
}

// TestALateCandidateTheStrategyAdmitsIsAttempted is the control.
//
// Without it, a guard that dropped EVERY late candidate would pass the regression - and late candidates are
// the entire reason the scheduler accepts mid-race arrivals.
func TestALateCandidateTheStrategyAdmitsIsAttempted(t *testing.T) {
	plannedIPv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")
	lateIPv6 := netip.MustParseAddr("2409:8c54:871:2007::10d")

	scheduler := newCapabilityScheduler(t)
	late := make(chan dualStackCandidate, 1)
	late <- dualStackCandidate{address: lateIPv6, family: familyIPv6}
	close(late)

	// The planned candidate fails, so the admitted late one is what must win.
	recorder := &recordingAttemptFunc{failOn: map[netip.Addr]bool{plannedIPv6: true}}
	plan := planCandidates([]netip.Addr{plannedIPv6}, netip.Addr{}, C.DomainStrategyPreferIPv6)

	admit := func(address netip.Addr) bool {
		return AddressAllowedByStrategy(address, C.DomainStrategyIPv6Only)
	}

	conn, winner, err := scheduler.dialWithLateCandidatesAdmitting(context.Background(), plan, late, admit, recorder.attempt)
	if conn != nil {
		conn.Close()
	}
	require.NoError(t, err, "the only admitted candidate is dialable")
	require.Equal(t, lateIPv6, winner)
	require.Contains(t, recorder.tried, lateIPv6,
		"an admitted late candidate must still be attempted: the guard exists to enforce a policy, not to "+
			"stop the scheduler accepting late arrivals")
}

// TestAPreferenceAdmitsEveryLateCandidate keeps the guard from becoming a restriction.
//
// `lateCandidateAdmission` returns nil for every strategy that is not one of the two `_only` values, and this
// pins the polarity through the predicate itself: a preference admits both families.
func TestAPreferenceAdmitsEveryLateCandidate(t *testing.T) {
	for _, strategy := range []C.DomainStrategy{
		C.DomainStrategyAsIS,
		C.DomainStrategyPreferIPv4,
		C.DomainStrategyPreferIPv6,
	} {
		require.True(t, AddressAllowedByStrategy(netip.MustParseAddr("112.53.55.92"), strategy),
			"strategy %v is a preference and must admit IPv4", strategy)
		require.True(t, AddressAllowedByStrategy(netip.MustParseAddr("2409:8c54:871:1007::2c"), strategy),
			"strategy %v is a preference and must admit IPv6", strategy)
	}
}
