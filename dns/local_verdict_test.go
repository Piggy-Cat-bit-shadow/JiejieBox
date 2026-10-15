package dns

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"

	mDNS "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// A rule action's verdict and an upstream answer are the SAME rcode and must stay
// interchangeable for every caller that asks "did this name resolve". They differ in whether the
// answer is a FAULT, which is what the router's logging classification reads: the router reported
// a blocked dialer lookup as `lookup failed for <domain>` at ERROR level, for exactly the traffic
// the adblock feature exists to make cheap. See ErrLocalVerdict.
//
// TestLocalVerdictKeepsTheRcodeAndAddsTheMarker is the contract half.
//
// The two facts have to hold TOGETHER, and each is asserted separately because either could be
// broken alone: widening the identity without preserving the rcode would break every existing
// errors.Is(err, RcodeError(...)) caller, and preserving the rcode without the marker is the
// defect.
func TestLocalVerdictKeepsTheRcodeAndAddsTheMarker(t *testing.T) {
	t.Parallel()

	verdict := LocalVerdictRcode(RcodeNameError)

	// The new fact.
	require.True(t, IsLocalVerdict(verdict), "a rule action's verdict must be recognisable as one")
	require.True(t, errors.Is(verdict, ErrLocalVerdict))

	// The PRESERVED fact, which every existing caller depends on.
	require.True(t, errors.Is(verdict, RcodeError(RcodeNameError)),
		"an existing errors.Is(err, RcodeError(...)) check must keep working")
	require.False(t, errors.Is(verdict, RcodeError(RcodeServerFailure)),
		"and must not match a different rcode")
	require.False(t, errors.Is(verdict, RcodeError(RcodeSuccess)))

	// The message is the rcode's own string. Error text reaches logs and, on some paths, a
	// user-visible refusal, so marking the error must not rewrite what a reader sees: a caller
	// that printed `NXDOMAIN` before must still read exactly that.
	require.Equal(t, RcodeError(RcodeNameError).Error(), verdict.Error())
	require.Equal(t, "NXDOMAIN", verdict.Error())

	// It survives wrapping, because the router wraps its errors with E.Cause on the way out and
	// the classification runs on the wrapped value.
	wrapped := E.Cause(verdict, "lookup ", "ads.example.")
	require.True(t, IsLocalVerdict(wrapped), "the marker must survive the router's own wrapping")
	require.True(t, errors.Is(wrapped, RcodeError(RcodeNameError)))

	// The NEGATIVE control, and it is the half that makes the marker worth having: an ordinary
	// rcode is NOT a verdict, so an upstream NXDOMAIN keeps logging as a failure.
	require.False(t, IsLocalVerdict(RcodeError(RcodeNameError)),
		"an upstream NXDOMAIN is not a local verdict and must not be classified as one")
	require.False(t, IsLocalVerdict(RcodeError(RcodeServerFailure)))
	require.False(t, IsLocalVerdict(nil))
	require.False(t, IsLocalVerdict(errors.New("something else")))
}

// TestBlockedDialerLookupIsAVerdictAndNotAFailure is the production-path half.
//
// # Why this goes through Router.Lookup and not the rule walk
//
// Every other adblock test drives the walk or the inbound Exchange path. The DIALER path is a
// different consumer with its own conversion - `lookupWithRulesType` turns a non-success rcode
// into the error a dialer sees - and it had no adblock coverage at all. That conversion is where
// the marker is attached, so a test that never calls Lookup cannot see whether it was.
//
// The transport is hostile on purpose: `panicWhenExchanged` turns any accidental upstream call
// into a loud failure rather than a plausible answer, so "the name never left this process" is
// observed rather than assumed.
func TestBlockedDialerLookupIsAVerdictAndNotAFailure(t *testing.T) {
	t.Parallel()

	transport := &fakeDNSTransport{tag: "final", address: netip.MustParseAddr("192.0.2.1")}
	router := raceTestRouter(t, transport)
	router.rules = raceTestRules(t, []option.DNSRule{adblockTestRule(t, "ads.example")})

	addresses, err := router.Lookup(context.Background(), adblockTestName, adapter.DNSQueryOptions{})

	require.Error(t, err, "a blocked name must not resolve")
	require.Empty(t, addresses)
	require.Zero(t, transport.queryCount.Load(), "the blocked name must never reach a transport")

	// The caller's existing view is unchanged: it still reads NXDOMAIN.
	require.True(t, errors.Is(err, RcodeError(mDNS.RcodeNameError)),
		"the dialer must still see the rcode it has always seen, got %v", err)
	require.Contains(t, err.Error(), "NXDOMAIN",
		"the message must still name the rcode, got %v", err)

	// And the new fact, which is what the logging classification uses.
	require.True(t, IsLocalVerdict(err),
		"a blocked dialer lookup is a configured verdict, not a lookup failure; got %v", err)
}

// TestLegacyBlockedDialerLookupIsAVerdictToo pins the second conversion site.
//
// The legacy dispatch (legacyDNSMode) builds its own error for a local answer rather than going
// through lookupWithRulesType, and the audit that found this defect confirmed the branch is
// reachable in production and had no test coverage at all - removing it panics rather than
// degrading. Both sites must mark the verdict, or the log noise returns for exactly the
// configurations that use the legacy path.
func TestLegacyBlockedDialerLookupIsAVerdictToo(t *testing.T) {
	t.Parallel()

	transport := &fakeDNSTransport{tag: "final", address: netip.MustParseAddr("192.0.2.1")}
	router := raceTestRouter(t, transport)
	router.rules = raceTestRules(t, []option.DNSRule{adblockTestRule(t, "ads.example")})
	router.legacyDNSMode = true

	addresses, err := router.Lookup(context.Background(), adblockTestName, adapter.DNSQueryOptions{})

	require.Error(t, err)
	require.Empty(t, addresses)
	require.Zero(t, transport.queryCount.Load())

	require.True(t, errors.Is(err, RcodeError(mDNS.RcodeNameError)),
		"the legacy path must return the same rcode, got %v", err)
	require.True(t, IsLocalVerdict(err),
		"the legacy conversion site must mark the verdict too, got %v", err)
}

// TestUpstreamFailureIsNotAVerdict is the end-to-end negative control.
//
// Without it, marking every non-success rcode as a verdict would satisfy the two tests above while
// silently demoting a genuine upstream failure to Debug - losing the one signal an operator has
// that their resolver is returning errors. This drives a real transport answer that is not
// success and asserts the distinction survives the whole path.
func TestUpstreamFailureIsNotAVerdict(t *testing.T) {
	t.Parallel()

	transport := &fakeDNSTransport{
		tag:     "final",
		address: netip.MustParseAddr("192.0.2.1"),
		rcode:   mDNS.RcodeServerFailure,
	}
	router := raceTestRouter(t, transport)

	_, err := router.Lookup(context.Background(), "ordinary.example.", adapter.DNSQueryOptions{})

	require.Error(t, err)
	require.Positive(t, transport.queryCount.Load(), "an unblocked name must reach the transport")
	require.True(t, errors.Is(err, RcodeError(mDNS.RcodeServerFailure)),
		"the upstream rcode must be preserved, got %v", err)
	require.False(t, IsLocalVerdict(err),
		"an upstream failure is not a configured verdict and must keep logging as a failure, got %v", err)
}
