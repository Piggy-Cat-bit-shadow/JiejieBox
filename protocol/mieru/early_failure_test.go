package mieru

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// The raw Winsock spellings, written out rather than imported: these are the
// numbers Windows actually delivers (see common/dialer/windows_socket_errors.go),
// and using them on every platform keeps the classifier's behaviour testable
// from Linux CI as well.
const (
	wsaRefused      = syscall.Errno(10061)
	wsaUnreachable  = syscall.Errno(10051)
	wsaAddrNotAvail = syscall.Errno(10049)
	wsaTimedOut     = syscall.Errno(10060)
	wsaReset        = syscall.Errno(10054)
)

// syscallOpError builds the shape net.Dial returns: *net.OpError wrapping
// *os.SyscallError wrapping the raw errno, exactly as net/fd_windows.go does.
func syscallOpError(errno syscall.Errno) error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connectex", errno)}
}

// localizedRefusalText is what a Chinese Windows produces for a refused port.
// The English text rules in classifyDialError cannot match it, which is the
// reason the bridge records the typed error at all.
const localizedRefusalText = "NewTCPUnderlay() failed: DialContext() failed: dial tcp 203.0.113.9:443: connectex: 由于目标计算机积极拒绝，无法连接。"

func TestClassifySocketErrorUsesTheSharedErrnoSets(t *testing.T) {
	testCases := []struct {
		name  string
		errno syscall.Errno
		want  dialFailureClass
	}{
		{"WSA refused", wsaRefused, dialSocketFailure},
		{"WSA unreachable", wsaUnreachable, dialSocketFailure},
		{"WSA address not available is local", wsaAddrNotAvail, dialCallerFailure},
		{"WSA connect timeout is not proof", wsaTimedOut, dialSocketTimeout},
		{"WSA reset is not proof", wsaReset, dialSocketTimeout},
		{"POSIX refused", syscall.ECONNREFUSED, dialSocketFailure},
		{"POSIX unreachable", syscall.ENETUNREACH, dialSocketFailure},
		{"POSIX timeout", syscall.ETIMEDOUT, dialSocketTimeout},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			class, classified := classifySocketError(syscallOpError(testCase.errno))
			require.True(t, classified)
			require.Equal(t, testCase.want, class)
		})
	}

	_, classified := classifySocketError(errors.New("some unrecognized failure"))
	require.False(t, classified, "an unrecognized socket error must not be guessed")
}

// What the bridge adds: the SDK's own English prefix ("NewTCPUnderlay() failed")
// still lets the text rules reach the transport class, so the fallback itself was
// not broken. What text cannot express is WHY it failed. Two consequences are
// real: a refusal cannot be marked definite (so two of them double-dial forever),
// and a local EADDRNOTAVAIL is indistinguishable from a path failure.
func TestTypedSocketEvidenceAddsWhatTextCannotExpress(t *testing.T) {
	sdkError := errors.New(localizedRefusalText)

	require.Equal(t, dialTransportFailure,
		classifyDialError(context.Background(), sdkError, mieruTransport{tcp: true}, nil),
		"the SDK prefix still reaches the transport class without the bridge")

	ctx, observation := withDialObservation(context.Background())
	observeSocketDial(ctx, syscallOpError(wsaRefused))
	require.Equal(t, dialSocketFailure,
		classifyDialError(context.Background(), sdkError, mieruTransport{tcp: true}, observation),
		"the typed errno recorded by the bridge is the proof the text cannot carry")
}

// A local interface failure (no IPv4/IPv6 source address, interface down) is
// not the proxy path's fault. The text rules see "NewUDPUnderlay() failed" and
// call it transport, which would switch transports and attach a Path fact the
// group may demote the member for; the typed errno keeps it local.
func TestLocalInterfaceFailureIsNotAPathFailure(t *testing.T) {
	sdkError := errors.New("NewUDPUnderlay() failed: ListenPacket() failed: dial udp: connect: A socket operation was attempted to an unreachable network.")

	require.Equal(t, dialTransportFailure,
		classifyDialError(context.Background(), sdkError, mieruTransport{udp: true}, nil),
		"text alone blames the transport")

	ctx, observation := withDialObservation(context.Background())
	observeSocketDial(ctx, syscallOpError(wsaAddrNotAvail))
	class := classifyDialError(context.Background(), sdkError, mieruTransport{udp: true}, observation)
	require.Equal(t, dialCallerFailure, class)
	facts, known := adapter.FailureFactsOf(wrapFailureFacts(sdkError, class))
	require.True(t, known)
	require.Equal(t, adapter.FailureOriginCaller, facts.Origin,
		"a local socket failure must not be published as the member's path failure")
}

// A TCP SYN blackhole surfaces as a kernel connect timeout wrapped in text.
// The text rules already reached the transport class through the SDK's
// "NewTCPUnderlay() failed" prefix, so the fallback itself worked. What they
// could not do was keep it a TIMEOUT: the flattened error is not a net.Error,
// so a Path fact would have let the group demote a member for a stall. The
// bridge supplies the typed timeout; the class is socket-timeout and carries no
// fact.
func TestSocketConnectTimeoutMakesSYNBlackholeFallBack(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	primary := &fakeClient{dialFunc: func(ctx context.Context, _ net.Addr) (net.Conn, error) {
		observeSocketDial(ctx, syscallOpError(wsaTimedOut))
		return nil, errors.New("NewTCPUnderlay() failed: DialContext() failed: dial tcp 203.0.113.9:443: i/o timeout")
	}}
	secondary := &fakeClient{dialFunc: func(context.Context, net.Addr) (net.Conn, error) {
		return clientConn, nil
	}}
	selector := newFallbackSelector(1)
	outboundInstance := &Outbound{logger: nopLogger(), generation: 1}
	spec, err := toNetAddrSpec(M.ParseSocksaddr("192.0.2.70:80"), N.NetworkTCP)
	require.NoError(t, err)

	conn, err := outboundInstance.dialWithFallback(context.Background(), spec,
		fakeRuntimeForTest("primary", primary), fakeRuntimeForTest("secondary", secondary), selector, 1)
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, 1, primary.dials)
	require.Equal(t, 1, secondary.dials)
	require.Equal(t, fallbackSecondary, selector.snapshot().Preferred)
}

// Two proven socket failures are a stable outage. The next requests must not
// re-dial the refused primary every time (2x connection churn for the whole
// outage); they stay on the secondary until the cooldown schedules one probe.
func TestDefiniteSocketFailureOnBothTransportsDoesNotDoubleDial(t *testing.T) {
	primary := &fakeClient{dialFunc: func(ctx context.Context, _ net.Addr) (net.Conn, error) {
		observeSocketDial(ctx, syscallOpError(wsaRefused))
		return nil, errors.New(localizedRefusalText)
	}}
	secondary := &fakeClient{dialFunc: func(ctx context.Context, _ net.Addr) (net.Conn, error) {
		observeSocketDial(ctx, syscallOpError(wsaRefused))
		return nil, errors.New(localizedRefusalText)
	}}
	now := time.Unix(7000, 0)
	selector := newFallbackSelector(1)
	selector.now = func() time.Time { return now }
	outboundInstance := &Outbound{logger: nopLogger(), generation: 1}
	spec, err := toNetAddrSpec(M.ParseSocksaddr("192.0.2.71:80"), N.NetworkTCP)
	require.NoError(t, err)

	for attempt := 0; attempt < 5; attempt++ {
		_, err = outboundInstance.dialWithFallback(context.Background(), spec,
			fakeRuntimeForTest("primary", primary), fakeRuntimeForTest("secondary", secondary), selector, 1)
		require.Error(t, err)
	}
	require.Equal(t, 1, primary.dials, "the refused primary must not be re-dialled on every request")
	require.Equal(t, 5, secondary.dials, "the preferred secondary is the only transport dialled")
	require.Equal(t, fallbackSecondary, selector.snapshot().Preferred)

	// The cooldown still schedules exactly one recovery probe against the primary.
	now = now.Add(fallbackCooldown + time.Second)
	choice, probe, ok := selector.choose(1)
	require.True(t, ok)
	require.Equal(t, fallbackPrimary, choice)
	require.True(t, probe)
}

// Mixed evidence must not pin the secondary: the primary failed definitively
// but the secondary merely stayed silent, so re-testing the (cheap, refused)
// primary on the next request is how its recovery is noticed.
func TestDefinitePrimaryAndSilentSecondaryResetsToPrimary(t *testing.T) {
	primary := &fakeClient{dialFunc: func(ctx context.Context, _ net.Addr) (net.Conn, error) {
		observeSocketDial(ctx, syscallOpError(wsaRefused))
		return nil, errors.New(localizedRefusalText)
	}}
	secondary := &fakeClient{dialFunc: func(context.Context, net.Addr) (net.Conn, error) {
		return nil, errors.New("failed to read socks5 connection response from the server: EOF")
	}}
	selector := newFallbackSelector(1)
	outboundInstance := &Outbound{logger: nopLogger(), generation: 1}
	spec, err := toNetAddrSpec(M.ParseSocksaddr("192.0.2.72:80"), N.NetworkTCP)
	require.NoError(t, err)

	_, err = outboundInstance.dialWithFallback(context.Background(), spec,
		fakeRuntimeForTest("primary", primary), fakeRuntimeForTest("secondary", secondary), selector, 1)
	require.Error(t, err)
	require.Equal(t, fallbackPrimary, selector.snapshot().Preferred)
	require.Zero(t, selector.snapshot().PrimaryUntil)
}

// Wrong credentials remain the fail-closed case: two silent handshakes must
// leave the primary preferred even after the definite-socket rule above.
func TestTwoSilentHandshakesStillResetToPrimary(t *testing.T) {
	silent := func() *fakeClient {
		return &fakeClient{dialFunc: func(context.Context, net.Addr) (net.Conn, error) {
			return nil, errors.New("failed to read socks5 connection response from the server: i/o timeout")
		}}
	}
	selector := newFallbackSelector(1)
	outboundInstance := &Outbound{logger: nopLogger(), generation: 1}
	spec, err := toNetAddrSpec(M.ParseSocksaddr("192.0.2.73:80"), N.NetworkTCP)
	require.NoError(t, err)

	_, err = outboundInstance.dialWithFallback(context.Background(), spec,
		fakeRuntimeForTest("primary", silent()), fakeRuntimeForTest("secondary", silent()), selector, 1)
	require.Error(t, err)
	require.Equal(t, fallbackPrimary, selector.snapshot().Preferred)
}

func TestFailureFactsSurviveWrappingAndJoin(t *testing.T) {
	typed := wrapFailureFacts(errors.New("refused"), dialSocketFailure)
	facts, known := adapter.FailureFactsOf(typed)
	require.True(t, known)
	require.Equal(t, adapter.PathFailureFacts(adapter.FailureStageDial), facts)

	// The aggregate path uses E.Errors and callers may add E.Cause around it;
	// both must keep the original chain reachable.
	caused := E.Cause(typed, "mieru: primary transport")
	facts, known = adapter.FailureFactsOf(caused)
	require.True(t, known)
	require.Equal(t, adapter.PathFailureFacts(adapter.FailureStageDial), facts)

	// errors.Join is the shape a multi-member/grouper layer may add.
	joined := errors.Join(errors.New("unrelated"), typed)
	facts, known = adapter.FailureFactsOf(joined)
	require.True(t, known)
	require.Equal(t, adapter.PathFailureFacts(adapter.FailureStageDial), facts)

	// A fact-less class must stay fact-less through the same wrappers.
	factless := wrapFailureFacts(errors.New("timeout"), dialSocketTimeout)
	require.False(t, func() bool {
		_, found := adapter.FailureFactsOf(E.Cause(errors.Join(factless, errors.New("other")), "wrap"))
		return found
	}())
}

func TestFailureFactsPerClass(t *testing.T) {
	testCases := []struct {
		name      string
		class     dialFailureClass
		wantFacts adapter.FailureFacts
		wantKnown bool
	}{
		{"target reply", dialTargetFailure, adapter.TargetFailureFacts(), true},
		{"handshake timeout", dialTransportFailure, adapter.PathFailureFacts(adapter.FailureStageHandshake), true},
		{"definite socket", dialSocketFailure, adapter.PathFailureFacts(adapter.FailureStageDial), true},
		{"caller", dialCallerFailure, adapter.FailureFacts{Stage: adapter.FailureStageDial, Origin: adapter.FailureOriginCaller}, true},
		{"socket timeout is fact-less", dialSocketTimeout, adapter.FailureFacts{}, false},
		{"ambiguous is fact-less", dialAmbiguousFailure, adapter.FailureFacts{}, false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := wrapFailureFacts(errors.New("boom"), testCase.class)
			facts, known := adapter.FailureFactsOf(err)
			require.Equal(t, testCase.wantKnown, known)
			if testCase.wantKnown {
				require.Equal(t, testCase.wantFacts, facts)
			}
		})
	}
}

// scriptedNDialer drives the bridge directly, so the recording itself is
// pinned rather than inferred through the selector.
type scriptedNDialer struct {
	N.Dialer
	result error
	conn   net.Conn
}

func (d *scriptedNDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	if d.result != nil {
		return nil, d.result
	}
	return d.conn, nil
}

func TestServerDialerRecordsTypedSocketEvidence(t *testing.T) {
	typed := syscallOpError(wsaRefused)
	bridge := &serverDialer{
		dialer: &scriptedNDialer{result: typed},
		server: M.Socksaddr{Fqdn: "example.com"},
	}
	ctx, observation := withDialObservation(context.Background())
	_, err := bridge.DialContext(ctx, "tcp", "203.0.113.9:443")
	require.Error(t, err)
	require.ErrorIs(t, observation.socketFailure(), typed,
		"the bridge must keep the typed errno the SDK is about to flatten")

	// The UDP underlay is dialled through the same bridge, so its socket
	// errors must be recorded too.
	udpBridge := &serverDialer{
		dialer: &scriptedNDialer{result: typed},
		server: M.Socksaddr{Fqdn: "example.com"},
	}
	udpCtx, udpObservation := withDialObservation(context.Background())
	_, err = udpBridge.ListenPacket(udpCtx, "udp", "", "203.0.113.9:443")
	require.Error(t, err)
	require.ErrorIs(t, udpObservation.socketFailure(), typed)
}

func TestSocketSuccessClearsAnEarlierFailure(t *testing.T) {
	ctx, observation := withDialObservation(context.Background())
	observeSocketDial(ctx, syscallOpError(wsaUnreachable))
	observeSocketDial(ctx, nil)
	require.Nil(t, observation.socketFailure(),
		"a later success means the final failure was not at the socket layer")
}

// Real SDK, real loopback: a refused TCP endpoint is the fastest failure Mieru
// can have, and the bridge must keep that proof all the way to the fact. The
// elapsed bound is deliberately generous; its purpose is to catch a regression
// back to waiting for a handshake that cannot happen.
func TestRealRefusedTCPIsADefiniteDialFailure(t *testing.T) {
	closedPort := freePortForBothTransports(t)
	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(closedPort, "tcp", "alice", "alice-password"))
	spec, err := toNetAddrSpec(M.ParseSocksaddr("192.0.2.80:443"), N.NetworkTCP)
	require.NoError(t, err)

	start := time.Now()
	conn, class, err := outboundInstance.primary.dial(context.Background(), spec)
	elapsed := time.Since(start)
	if conn != nil {
		_ = conn.Close()
	}
	require.Error(t, err)
	require.Equal(t, dialSocketFailure, class)
	require.Less(t, elapsed, 2*time.Second, "a refused connect must not wait for a handshake")
	facts, known := adapter.FailureFactsOf(wrapFailureFacts(err, class))
	require.True(t, known)
	require.Equal(t, adapter.PathFailureFacts(adapter.FailureStageDial), facts,
		"a refused proxy endpoint is a dial-stage path failure, not a handshake failure")
}

// Same-generation ordering was investigated before changing anything (the
// directive asks for a blocking/release order test, not an assumption).
//
// The observed behaviour: a slow attempt that FAILED after a newer attempt on
// the same transport already SUCCEEDED does flip the preference to the
// secondary. It is a deliberate acceptance, for two reasons recorded in
// ARCHITECTURE-DECISIONS.md §Same-generation ordering:
//
//   - the flip is a bounded hint (one cooldown, at most 5 minutes), not a
//     durable verdict, and the next success or target reply clears it;
//   - "newer" is not well defined for a long attempt: a handshake that started
//     first can observe a LATER world (the endpoint may have died mid-wait),
//     so ignoring its failure would keep new sessions on a primary that is now
//     dead. MASQUE's attempt-ordering guard solves a different, costlier
//     problem (pinning the fallback for the whole backoff window).
//
// The test pins the behaviour so a future change is a decision rather than an
// accident, and pins the self-healing direction that makes it acceptable.
func TestSameGenerationOrderOfDialOutcomes(t *testing.T) {
	selector := newFallbackSelector(1)
	now := time.Unix(7100, 0)
	selector.now = func() time.Time { return now }

	// Two attempts both chose the primary; the second one succeeds first.
	_, _, ok := selector.choose(1)
	require.True(t, ok)
	_, _, ok = selector.choose(1)
	require.True(t, ok)
	selector.report(1, fallbackPrimary, false, dialSuccess)
	require.Equal(t, fallbackPrimary, selector.snapshot().Preferred)

	// The slower attempt (started before the success) reports a transport
	// failure afterwards: it flips the bounded hint.
	selector.report(1, fallbackPrimary, false, dialTransportFailure)
	snapshot := selector.snapshot()
	require.Equal(t, fallbackSecondary, snapshot.Preferred)
	require.Equal(t, uint64(1), snapshot.PrimaryFailure)
	require.Equal(t, now.Add(fallbackCooldown), snapshot.PrimaryUntil)

	// The next successful probe clears it: the hint is not a verdict.
	now = now.Add(fallbackCooldown + time.Second)
	choice, probe, ok := selector.choose(1)
	require.True(t, ok)
	require.Equal(t, fallbackPrimary, choice)
	require.True(t, probe)
	selector.report(1, fallbackPrimary, true, dialSuccess)
	snapshot = selector.snapshot()
	require.Equal(t, fallbackPrimary, snapshot.Preferred)
	require.True(t, snapshot.PrimaryUntil.IsZero())
}

// The per-dial cost of carrying the typed evidence: one context wrapper, one
// record and one read. It is paid once per new SDK session, never per packet.
func BenchmarkDialObservationOverhead(b *testing.B) {
	parent := context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		observed, observation := withDialObservation(parent)
		observeSocketDial(observed, nil)
		if observation.socketFailure() != nil {
			b.Fatal("unexpected socket failure")
		}
	}
}

// A caller that has already given up must not start an SDK handshake: the SDK
// cannot cancel its response read, so the request would sit there for 10 s with
// nobody waiting for it. The check is at the transport boundary because that is
// the only place this package can make the decision.
func TestAlreadyCancelledCallerSkipsTheSDKHandshake(t *testing.T) {
	client := &fakeClient{dialFunc: func(context.Context, net.Addr) (net.Conn, error) {
		t.Fatal("a cancelled caller must not reach the SDK")
		return nil, nil
	}}
	runtime := fakeRuntimeForTest("primary", client)
	spec, err := toNetAddrSpec(M.ParseSocksaddr("192.0.2.81:443"), N.NetworkTCP)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, class, err := runtime.dial(ctx, spec)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, dialCallerFailure, class)
	require.Zero(t, client.dials)
}
