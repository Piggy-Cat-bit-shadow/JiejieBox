package dialer

import (
	"context"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/stretchr/testify/require"
)

// DS-01: a late candidate refused by the machine must still explain why nothing was attempted.
//
// # The exact shape the audit's probe found
//
// A literal IPv6 destination with a sniffed domain: the streaming hostname path starts with an empty
// plan, the AAAA answer arrives late, and the local egress gate refuses it. The scheduler drops that
// candidate - correctly, the machine cannot dial it - but before this fix the drop was silent: the
// refusal was not recorded, so when the stream closed with nothing started the conclusion was
// "no dial candidates". The reader was told "no candidate was offered" when the truth was "the
// candidate was offered and this machine has no public IPv6 path".
//
// The plan-filtered case was already fixed (filterLocalFamily records its refusals); this is the
// late-arrival arm, which never passes through the planner.
func TestLateRefusalExplainsWhyNothingWasAttempted(t *testing.T) {
	// The reported machine: IPv6 addressing present, no global IPv6 egress.
	policy := outageDialer(windowsLinkLocalOnly()).captureLocalFamilyPolicy()
	refusedAddress := netip.MustParseAddr("2001:4860:4860::8888")
	require.ErrorIs(t, policy.hardUndialableError(refusedAddress), errLocalIPv6EgressUnavailable,
		"the fixture must reproduce the reported machine, otherwise this test proves nothing")

	var attempts atomic.Int32
	lateCandidates := make(chan dualStackCandidate, 1)
	go func() {
		lateCandidates <- dualStackCandidate{address: refusedAddress, family: familyIPv6}
		close(lateCandidates)
	}()

	scheduler := &candidateScheduler{
		fallbackDelay: 20 * time.Millisecond,
		localFamily:   policy,
	}
	_, _, err := scheduler.dialWithLateCandidates(context.Background(),
		planCandidates(nil, netip.Addr{}, C.DomainStrategyAsIS),
		lateCandidates,
		func(ctx context.Context, candidate netip.Addr) (net.Conn, error) {
			attempts.Add(1)
			return &countingConn{}, nil
		})

	require.Error(t, err)
	require.Zero(t, attempts.Load(), "a refused candidate must not be dialed")
	require.ErrorIs(t, err, errLocalIPv6EgressUnavailable,
		"the refusal is the reason nothing was attempted, so it must be the error")
	require.NotContains(t, err.Error(), "no dial candidates",
		"that message describes a symptom and hides the cause the machine already knew")
}
