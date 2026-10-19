package mieru

// Round-2 adversarial audit: the decision tables that the first round's new
// failure classes feed. Every case here is a contract the directive names, not
// an implementation detail.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// aggregateAttempt builds one failed SDK attempt with the given class-
// producing evidence and the SDK's flattened error text.
func aggregateAttempt(t *testing.T, class dialFailureClass) *fakeClient {
	t.Helper()
	var setupErr error
	var text string
	switch class {
	case dialSocketFailure:
		setupErr = syscallOpError(wsaRefused)
		text = localizedRefusalText
	case dialSocketTimeout:
		setupErr = syscallOpError(wsaTimedOut)
		text = "NewTCPUnderlay() failed: DialContext() failed: dial tcp 203.0.113.9:443: i/o timeout"
	case dialTransportFailure:
		text = "failed to read socks5 connection response from the server: TIMEOUT"
	case dialAmbiguousFailure:
		text = "failed to read socks5 connection response from the server: EOF"
	case dialTargetFailure:
		text = "server returned socks5 error code 2"
	case dialCallerFailure:
		text = "client dial stopped: context canceled"
	case dialLocalFailure:
		text = "client password is not set"
	case dialEndpointFailure:
		setupErr = &net.DNSError{Err: "no such host", Name: "nonexistent.invalid"}
		text = "NewTCPUnderlay() failed: DialContext() failed: lookup nonexistent.invalid: no such host"
	default:
		t.Fatalf("unhandled class %v", class)
	}
	return &fakeClient{dialFunc: func(ctx context.Context, _ net.Addr) (net.Conn, error) {
		if setupErr != nil {
			observeSocketDial(ctx, setupErr)
		}
		if class == dialCallerFailure {
			// The classification checks errors.Is, not the text.
			return nil, fmt.Errorf("client dial stopped: %w", context.Canceled)
		}
		return nil, errors.New(text)
	}}
}

func aggregateFactsFor(t *testing.T, primaryClass, secondaryClass dialFailureClass) (adapter.FailureFacts, bool) {
	t.Helper()
	outboundInstance := &Outbound{logger: nopLogger(), generation: 1}
	spec, err := toNetAddrSpec(M.ParseSocksaddr("192.0.2.99:80"), N.NetworkTCP)
	require.NoError(t, err)
	selector := newFallbackSelector(1)

	_, dialErr := outboundInstance.dialWithFallback(context.Background(), spec,
		fakeRuntimeForTest("primary", aggregateAttempt(t, primaryClass)),
		fakeRuntimeForTest("secondary", aggregateAttempt(t, secondaryClass)),
		selector, 1)
	require.Error(t, dialErr)
	return adapter.FailureFactsOf(dialErr)
}

// §9.1 of the debug directive: the aggregate must express the responsibility of
// the FINAL behaviour, not the first wrapper's or the last attempt's class.
//
// Precedence pinned here:
//
//	target reply anywhere      -> Target/Target   (the proxy path worked)
//	definite socket anywhere   -> Dial/Path       (a first hop was proven dead)
//	handshake transport        -> Handshake/Path  (the pre-round-1 secondary rule)
//	only soft evidence         -> no fact         (timeout/ambiguous are not proof)
func TestAggregateFailureFactsPrecedence(t *testing.T) {
	testCases := []struct {
		name      string
		primary   dialFailureClass
		secondary dialFailureClass
		want      adapter.FailureFacts
		wantKnown bool
	}{
		{"target after socket", dialSocketFailure, dialTargetFailure, adapter.TargetFailureFacts(), true},
		{"handshake after socket", dialSocketFailure, dialTransportFailure, adapter.PathFailureFacts(adapter.FailureStageDial), true},
		{"timeout after socket keeps the proof", dialSocketFailure, dialSocketTimeout, adapter.PathFailureFacts(adapter.FailureStageDial), true},
		{"ambiguous after socket keeps the proof", dialSocketFailure, dialAmbiguousFailure, adapter.PathFailureFacts(adapter.FailureStageDial), true},
		{"caller after socket keeps the proof", dialSocketFailure, dialCallerFailure, adapter.PathFailureFacts(adapter.FailureStageDial), true},
		{"local after socket keeps the proof", dialSocketFailure, dialLocalFailure, adapter.PathFailureFacts(adapter.FailureStageDial), true},
		{"socket after handshake", dialTransportFailure, dialSocketFailure, adapter.PathFailureFacts(adapter.FailureStageDial), true},
		{"handshake after handshake", dialTransportFailure, dialTransportFailure, adapter.PathFailureFacts(adapter.FailureStageHandshake), true},
		{"timeout after handshake keeps the handshake evidence", dialTransportFailure, dialSocketTimeout, adapter.PathFailureFacts(adapter.FailureStageHandshake), true},
		{"socket timeout alone is fact-less", dialSocketTimeout, dialSocketTimeout, adapter.FailureFacts{}, false},
		{"handshake after socket timeout", dialSocketTimeout, dialTransportFailure, adapter.PathFailureFacts(adapter.FailureStageHandshake), true},
		{"socket proof outranks endpoint failure", dialSocketFailure, dialEndpointFailure, adapter.PathFailureFacts(adapter.FailureStageDial), true},
		{"endpoint failure after socket timeout", dialSocketTimeout, dialEndpointFailure, adapter.FailureFacts{}, false},
		{"handshake after endpoint failure", dialEndpointFailure, dialTransportFailure, adapter.PathFailureFacts(adapter.FailureStageHandshake), true},
		{"endpoint failure alone is fact-less in the aggregate", dialEndpointFailure, dialEndpointFailure, adapter.FailureFacts{}, false},
		{"local failure alone is fact-less in the aggregate", dialTransportFailure, dialLocalFailure, adapter.FailureFacts{}, false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			facts, known := aggregateFactsFor(t, testCase.primary, testCase.secondary)
			require.Equal(t, testCase.wantKnown, known)
			if testCase.wantKnown {
				require.Equal(t, testCase.want, facts)
			}
		})
	}
}

// A closed/replaced outbound must not be reported as a transport failure just
// because the SDK wraps our own lifecycle error with "NewUDPUnderlay() failed".
func TestLifecycleClosureIsLocalNotTransport(t *testing.T) {
	bridge := &serverDialer{
		dialer: &scriptedNDialer{conn: pipeConn(t)},
		server: M.Socksaddr{Fqdn: "example.com"},
	}
	// Final shutdown: the packet dialer refuses new sockets.
	bridge.shutdownPacketConns()
	ctx, observation := withDialObservation(context.Background())
	_, err := bridge.ListenPacket(ctx, "udp", "", "203.0.113.9:443")
	require.Error(t, err)
	require.Contains(t, err.Error(), "outbound was closed")

	// The SDK flattens it into the underlay message the text rules see.
	sdkErr := fmt.Errorf("NewUDPUnderlay() failed: %v", err)
	class := classifyDialError(context.Background(), sdkErr, mieruTransport{udp: true}, observation)
	require.Equal(t, dialLocalFailure, class,
		"a teardown race is a local lifecycle event, not evidence about the path")
	facts, known := adapter.FailureFactsOf(wrapFailureFacts(sdkErr, class))
	require.True(t, known)
	require.Equal(t, adapter.FailureFacts{Stage: adapter.FailureStageDial, Origin: adapter.FailureOriginLocal}, facts)
}

func pipeConn(t *testing.T) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	return client
}

// A proxy-endpoint DNS failure is local to that endpoint (Dial/Local fact) but
// the fallback endpoint may carry a different server, so it must still try the
// other transport once. The resolver runs inside the SDK for the UDP path, so
// the bridge records it explicitly; otherwise only the flattened text exists.
type failingDNSRouter struct {
	adapter.DNSRouter
	err error
}

func (r *failingDNSRouter) Lookup(context.Context, string, adapter.DNSQueryOptions) ([]netip.Addr, error) {
	return nil, r.err
}

func TestEndpointResolutionFailureIsLocalButTriesTheOtherEndpoint(t *testing.T) {
	bridge := &resolverBridge{router: &failingDNSRouter{
		err: &net.DNSError{Err: "no such host", Name: "nonexistent.invalid"},
	}}
	ctx, observation := withDialObservation(context.Background())
	_, lookupErr := bridge.LookupIP(ctx, "ip4", "nonexistent.invalid")
	require.Error(t, lookupErr)

	// The SDK wraps resolution failures with %v before Mieru sees them.
	sdkErr := fmt.Errorf("NewUDPUnderlay() failed: ResolveUDPAddr() failed: %v", lookupErr)
	class := classifyDialError(context.Background(), sdkErr, mieruTransport{udp: true}, observation)
	require.Equal(t, dialEndpointFailure, class,
		"a resolver failure recorded by the bridge must not be guessed from text")
	require.True(t, isTransportClass(class),
		"the fallback endpoint may use a different server, so it is worth one attempt")
	facts, known := adapter.FailureFactsOf(wrapFailureFacts(sdkErr, class))
	require.True(t, known)
	require.Equal(t, adapter.FailureFacts{Stage: adapter.FailureStageDial, Origin: adapter.FailureOriginLocal}, facts,
		"but the member's path must not be blamed for a name that did not resolve")

	// The fallback really does use the secondary when only the primary name fails.
	primary := aggregateAttempt(t, dialEndpointFailure)
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	secondary := &fakeClient{dialFunc: func(context.Context, net.Addr) (net.Conn, error) {
		return clientConn, nil
	}}
	outboundInstance := &Outbound{logger: nopLogger(), generation: 1}
	selector := newFallbackSelector(1)
	spec, err := toNetAddrSpec(M.ParseSocksaddr("192.0.2.100:80"), N.NetworkTCP)
	require.NoError(t, err)
	conn, err := outboundInstance.dialWithFallback(context.Background(), spec,
		fakeRuntimeForTest("primary", primary), fakeRuntimeForTest("secondary", secondary), selector, 1)
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, 1, primary.dials)
	require.Equal(t, 1, secondary.dials)
	require.Equal(t, fallbackSecondary, selector.snapshot().Preferred)
}

// The both-failed decision table, pinned as data rather than as prose. Only two
// definite setup failures (socket proof or an unresolvable endpoint) may keep
// the secondary preference; everything else resets, because only that pair is
// proof that re-testing the primary cannot help within this cooldown.
func TestReportBothFailedDecisionTable(t *testing.T) {
	classes := []dialFailureClass{
		dialTransportFailure, dialSocketFailure, dialSocketTimeout, dialAmbiguousFailure, dialCallerFailure,
		dialLocalFailure, dialEndpointFailure,
	}
	for _, primaryClass := range classes {
		for _, secondaryClass := range classes {
			t.Run(primaryClass.String()+"+"+secondaryClass.String(), func(t *testing.T) {
				now := time.Unix(8000, 0)
				selector := newFallbackSelector(1)
				selector.now = func() time.Time { return now }
				// Model the state after the primary's report: the only way
				// reportBothFailed is reached.
				selector.preferred = fallbackSecondary
				selector.primaryUntil = now.Add(fallbackCooldown)

				selector.reportBothFailed(1, primaryClass, secondaryClass)

				snapshot := selector.snapshot()
				keepSecondary := isDefiniteSetupClass(primaryClass) && isDefiniteSetupClass(secondaryClass)
				if keepSecondary {
					require.Equal(t, fallbackSecondary, snapshot.Preferred)
					require.Equal(t, now.Add(fallbackCooldown), snapshot.PrimaryUntil)
					return
				}
				if isSilentHandshakeClass(primaryClass) || isDefiniteSetupClass(primaryClass) {
					require.Equal(t, fallbackPrimary, snapshot.Preferred)
					require.True(t, snapshot.PrimaryUntil.IsZero())
					return
				}
				// A caller-class primary cannot reach reportBothFailed in the
				// dial path; the method itself leaves the state untouched.
				require.Equal(t, fallbackSecondary, snapshot.Preferred)
			})
		}
	}
}

// Same-generation ordering, the part the directive asks for explicitly: what
// does the NEXT new connection prefer after an old attempt reports late?
func TestLateFailureAffectsTheNextNewSessionPreference(t *testing.T) {
	selector := newFallbackSelector(1)
	now := time.Unix(8100, 0)
	selector.now = func() time.Time { return now }

	// Two attempts start on the primary; the second one succeeds first.
	_, _, ok := selector.choose(1)
	require.True(t, ok)
	_, _, ok = selector.choose(1)
	require.True(t, ok)
	selector.report(1, fallbackPrimary, false, dialSuccess)

	// The older attempt reports a transport failure after the success.
	selector.report(1, fallbackPrimary, false, dialTransportFailure)

	// The next NEW session follows the flipped, bounded hint.
	choice, probe, ok := selector.choose(1)
	require.True(t, ok)
	require.Equal(t, fallbackSecondary, choice,
		"a late failure currently flips the next new session to the secondary for one cooldown")
	require.False(t, probe)

	// And the probe restores the primary once the cooldown passes, so the hint
	// cannot become a durable verdict.
	now = now.Add(fallbackCooldown + time.Second)
	choice, probe, ok = selector.choose(1)
	require.True(t, ok)
	require.Equal(t, fallbackPrimary, choice)
	require.True(t, probe)
	selector.report(1, fallbackPrimary, true, dialSuccess)
	choice, _, _ = selector.choose(1)
	require.Equal(t, fallbackPrimary, choice)
}

// A definite socket failure on the preferred secondary must not publish a new
// preference by itself: the decision table says only the primary's evidence
// moves the preference. This pins that the secondary path is inert.
func TestSecondaryDefiniteFailureDoesNotMoveThePreference(t *testing.T) {
	selector := newFallbackSelector(1)
	selector.preferred = fallbackSecondary
	selector.report(1, fallbackSecondary, false, dialSocketFailure)
	snapshot := selector.snapshot()
	require.Equal(t, fallbackSecondary, snapshot.Preferred)
	require.True(t, snapshot.PrimaryUntil.IsZero())
	require.Zero(t, snapshot.PrimaryFailure)
}

// The cooldown boundaries the directive names: 29/30/31 s around the first
// window, then the doubling schedule up to the 5-minute ceiling. A probe
// failure advances the backoff once; it must never overshoot the ceiling.
func TestFallbackCooldownDoublesToTheCeiling(t *testing.T) {
	now := time.Unix(8200, 0)
	selector := newFallbackSelector(1)
	selector.now = func() time.Time { return now }

	selector.report(1, fallbackPrimary, false, dialTransportFailure)
	require.Equal(t, now.Add(fallbackCooldown), selector.snapshot().PrimaryUntil)

	// 29 s: still in the first window, no probe.
	now = now.Add(fallbackCooldown - time.Second)
	choice, probe, ok := selector.choose(1)
	require.True(t, ok)
	require.Equal(t, fallbackSecondary, choice)
	require.False(t, probe)

	// 30 s: exactly at the boundary, the single probe is allowed.
	now = now.Add(time.Second)
	choice, probe, ok = selector.choose(1)
	require.True(t, ok)
	require.Equal(t, fallbackPrimary, choice)
	require.True(t, probe)

	// The probe fails: 60 s. Then 120, 240, 300 (capped), 300.
	expected := []time.Duration{60 * time.Second, 120 * time.Second, 240 * time.Second, fallbackCooldownMax, fallbackCooldownMax}
	for index, want := range expected {
		selector.report(1, fallbackPrimary, true, dialTransportFailure)
		snapshot := selector.snapshot()
		require.Equal(t, fallbackSecondary, snapshot.Preferred)
		require.Equal(t, now.Add(want), snapshot.PrimaryUntil,
			"step %d: the cooldown must be %s", index, want)
		now = now.Add(want)
		choice, probe, ok = selector.choose(1)
		require.True(t, ok)
		require.Equal(t, fallbackPrimary, choice)
		require.True(t, probe)
	}

	// A caller cancellation during a probe releases the latch and leaves the
	// backoff untouched (it is not evidence about the primary).
	before := selector.snapshot()
	selector.report(1, fallbackPrimary, true, dialCallerFailure)
	after := selector.snapshot()
	require.False(t, after.Probing, "a cancelled probe must not hold the latch")
	require.Equal(t, before.PrimaryUntil, after.PrimaryUntil)
	require.Equal(t, before.PrimaryFailure, after.PrimaryFailure)
}
