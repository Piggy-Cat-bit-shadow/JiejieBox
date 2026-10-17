package dialer

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The two invariants that make sniffed-domain recovery safe to have, asserted end to end.
//
// # Invariant 1: a dialable literal is not delayed by discovery
//
// Recovery exists to provide a fallback, so it may never become a precondition. The literal the
// application chose is scheduled immediately, and the lookup joins as a LATE candidate. A resolver that
// is slow, stalled or failing must therefore cost a healthy literal nothing at all.
//
// # Invariant 2: recovery reaches a family this machine can actually dial
//
// A literal whose family the machine refuses has no candidate of its own, so recovery is the ONLY thing
// that can carry the flow. It must therefore end on a family that is usable, not on the first family that
// happened to answer. The matrix below is the set of resolver orderings and answer shapes that decide
// whether that holds.
//
// Both are asserted through the production graph - `DialSerialNetwork` → the real outbound → the resolve
// wrapper → the scheduler - and on what the TRANSPORT observed (`chain.tried()`), not on what a helper
// returned. A helper-level assertion would pass on the revisions that produced the reported outage.

// recoveryCase is one row of the resolver matrix.
type recoveryCase struct {
	name string

	// delayA and delayAAAA are the per-family answer delays. A negative value blocks until cancelled,
	// which is how a hung family is modelled.
	delayA    time.Duration
	delayAAAA time.Duration

	addressesA    []netip.Addr
	addressesAAAA []netip.Addr
	errA          error
	errAAAA       error

	// wantAttempted must all appear in the transport's attempt list; wantNever must not.
	wantAttempted []netip.Addr
	wantNever     []netip.Addr

	// wantErr is true when no candidate can carry the flow.
	wantErr bool
}

// TestRecoveryMatrixIsFollowedByTheTransport drives every resolver shape the reported machine can
// produce through the production graph.
func TestRecoveryMatrixIsFollowedByTheTransport(t *testing.T) {
	originalIPv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")
	healthyIPv4 := netip.MustParseAddr("112.53.55.92")
	otherIPv4 := netip.MustParseAddr("112.53.55.93")
	otherIPv6 := netip.MustParseAddr("2409:8c54:871:2007::10d")

	nxdomain := errors.New("NXDOMAIN")

	for _, testCase := range []recoveryCase{
		{
			// The reported case, in its most likely ordering: the refused family answers first.
			name:          "AAAA first and refused, A later and usable",
			delayAAAA:     0,
			delayA:        40 * time.Millisecond,
			addressesAAAA: []netip.Addr{originalIPv6},
			addressesA:    []netip.Addr{healthyIPv4},
			wantAttempted: []netip.Addr{healthyIPv4},
			wantNever:     []netip.Addr{originalIPv6},
		},
		{
			name:          "A first and usable, AAAA later and refused",
			delayA:        0,
			delayAAAA:     40 * time.Millisecond,
			addressesA:    []netip.Addr{healthyIPv4},
			addressesAAAA: []netip.Addr{originalIPv6},
			wantAttempted: []netip.Addr{healthyIPv4},
			wantNever:     []netip.Addr{originalIPv6},
		},
		{
			name:          "AAAA only, refused",
			addressesAAAA: []netip.Addr{originalIPv6},
			wantErr:       true,
			wantNever:     []netip.Addr{originalIPv6},
		},
		{
			name:          "A only, usable",
			addressesA:    []netip.Addr{healthyIPv4},
			wantAttempted: []netip.Addr{healthyIPv4},
			wantNever:     []netip.Addr{originalIPv6},
		},
		{
			name:          "both refused",
			addressesAAAA: []netip.Addr{originalIPv6},
			addressesA:    []netip.Addr{otherIPv6},
			wantErr:       true,
			wantNever:     []netip.Addr{originalIPv6, otherIPv6},
		},
		{
			name:          "both usable",
			addressesAAAA: []netip.Addr{otherIPv6},
			addressesA:    []netip.Addr{healthyIPv4},
			// The refused AAAA must not be attempted, and the usable IPv4 must be.
			wantAttempted: []netip.Addr{healthyIPv4},
			wantNever:     []netip.Addr{otherIPv6, originalIPv6},
		},
		{
			// A resolver that answers NXDOMAIN for one family and a usable address for the other is an
			// ordinary shape, not an error.
			name:          "AAAA answers NXDOMAIN, A usable",
			errAAAA:       nxdomain,
			delayA:        30 * time.Millisecond,
			addressesA:    []netip.Addr{healthyIPv4},
			wantAttempted: []netip.Addr{healthyIPv4},
			wantNever:     []netip.Addr{originalIPv6},
		},
		{
			// An empty answer for the refused family and a usable other family: the empty family must not
			// end the lookup either.
			name:          "AAAA empty, A usable",
			addressesAAAA: nil,
			delayA:        30 * time.Millisecond,
			addressesA:    []netip.Addr{healthyIPv4},
			wantAttempted: []netip.Addr{healthyIPv4},
			wantNever:     []netip.Addr{originalIPv6},
		},
		{
			// A family that never answers must not hold the lookup: the family that did answer carries
			// the flow.
			name:          "AAAA hangs, A usable",
			delayAAAA:     -1,
			delayA:        20 * time.Millisecond,
			addressesA:    []netip.Addr{healthyIPv4},
			wantAttempted: []netip.Addr{healthyIPv4},
			wantNever:     []netip.Addr{originalIPv6},
		},
		{
			// Out of order: the refused family answers at once, the usable one is not there yet but a
			// second usable address is.
			name:          "AAAA first refused, A returns two usable",
			delayAAAA:     0,
			delayA:        50 * time.Millisecond,
			addressesAAAA: []netip.Addr{originalIPv6},
			addressesA:    []netip.Addr{healthyIPv4, otherIPv4},
			wantAttempted: []netip.Addr{healthyIPv4},
			wantNever:     []netip.Addr{originalIPv6},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// The reported machine: IPv4 usable, no IPv6 path.
			chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateUnavailable, C.DomainStrategyAsIS)
			chain.router.delayA = testCase.delayA
			chain.router.delayAAAA = testCase.delayAAAA
			chain.router.addressesA = testCase.addressesA
			chain.router.addressesAAAA = testCase.addressesAAAA
			chain.router.errA = testCase.errA
			chain.router.errAAAA = testCase.errAAAA

			ctx, cancel := context.WithTimeout(wechatFlow(originalIPv6, 80, "szminorshort.weixin.qq.com"), 5*time.Second)
			defer cancel()

			conn, err := chain.dialSerialNetworkAtTheOutbound(ctx, "tcp", M.SocksaddrFrom(originalIPv6, 80), nil)
			if conn != nil {
				conn.Close()
			}

			if testCase.wantErr {
				require.Error(t, err, "no candidate can carry this flow, so it must fail rather than hang")
			} else {
				require.NoError(t, err,
					"recovery must carry the flow: a refused family is the only candidate without it")
			}

			tried := chain.tried()
			for _, wanted := range testCase.wantAttempted {
				require.Contains(t, tried, wanted,
					"the transport must actually attempt %s; recovery that resolves it but never dials it "+
						"does not carry the flow", wanted)
			}
			for _, forbidden := range testCase.wantNever {
				require.NotContains(t, tried, forbidden,
					"the transport must never attempt %s: the machine has no path for its family, so an "+
						"attempt costs a connect timeout for an answer already known", forbidden)
			}
		})
	}
}

// TestHealthyLiteralIsNotDelayedByDiscovery is invariant 1, across every resolver behaviour.
//
// The literal is dialable, so it wins on its own. The lookup is a late candidate and may never delay it -
// which is why these cases assert on ELAPSED TIME while the resolver is slow, hung or failing. A recovery
// implementation that ran the lookup before scheduling would pass every functional assertion above and
// fail only here.
func TestHealthyLiteralIsNotDelayedByDiscovery(t *testing.T) {
	originalIPv4 := netip.MustParseAddr("112.53.55.92")
	otherIPv4 := netip.MustParseAddr("112.53.55.93")

	for _, testCase := range []struct {
		name      string
		delayA    time.Duration
		delayAAAA time.Duration
		errA      error
		errAAAA   error
		stall     bool
	}{
		{name: "resolver answers in 10ms", delayA: 10 * time.Millisecond, delayAAAA: 10 * time.Millisecond},
		{name: "resolver answers in 100ms", delayA: 100 * time.Millisecond, delayAAAA: 100 * time.Millisecond},
		{name: "resolver answers in 3s", delayA: 3 * time.Second, delayAAAA: 3 * time.Second},
		{name: "resolver hangs", stall: true},
		{name: "resolver fails", errA: errors.New("SERVFAIL"), errAAAA: errors.New("SERVFAIL")},
		{name: "one family hangs", delayAAAA: -1, delayA: 3 * time.Second},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Both families usable, so nothing about the machine is in play: this is purely about the
			// literal not waiting on the resolver.
			chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyAsIS)
			chain.router.addressesA = []netip.Addr{otherIPv4}
			chain.router.addressesAAAA = nil
			if testCase.stall {
				chain.stallLookup(t)
				defer chain.releaseLookup(t)
			} else {
				chain.router.delayA = testCase.delayA
				chain.router.delayAAAA = testCase.delayAAAA
			}
			chain.router.errA = testCase.errA
			chain.router.errAAAA = testCase.errAAAA

			ctx, cancel := context.WithTimeout(wechatFlow(originalIPv4, 80, "szextshort.weixin.qq.com"), 5*time.Second)
			defer cancel()

			start := time.Now()
			conn, err := chain.dialSerialNetworkAtTheOutbound(ctx, "tcp", M.SocksaddrFrom(originalIPv4, 80), nil)
			elapsed := time.Since(start)
			if conn != nil {
				conn.Close()
			}

			require.NoError(t, err, "the literal is dialable, so the flow must connect")
			require.Less(t, elapsed, 500*time.Millisecond,
				"a dialable literal must not wait on the resolver: recovery is a fallback, and spending "+
					"the resolver's latency here would make every connection pay for a fallback it did "+
					"not need")

			require.Contains(t, chain.tried(), originalIPv4,
				"and the application's own address is what carries it")
		})
	}
}
