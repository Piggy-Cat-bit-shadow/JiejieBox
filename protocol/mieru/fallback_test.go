package mieru

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/sagernet/sing-box/adapter"

	mieruclient "github.com/enfein/mieru/v3/apis/client"
	"github.com/enfein/mieru/v3/pkg/stderror"
	"github.com/stretchr/testify/require"
)

type timeoutTestError struct{}

func (timeoutTestError) Error() string   { return "i/o timeout" }
func (timeoutTestError) Timeout() bool   { return true }
func (timeoutTestError) Temporary() bool { return true }

func TestFallbackSelectorPrimaryAndTargetFailure(t *testing.T) {
	now := time.Unix(1000, 0)
	selector := newFallbackSelector(1)
	selector.now = func() time.Time { return now }

	choice, probe, ok := selector.choose(1)
	require.True(t, ok)
	require.Equal(t, fallbackPrimary, choice)
	require.False(t, probe)

	selector.report(1, fallbackPrimary, false, dialSuccess)
	snapshot := selector.snapshot()
	require.Equal(t, fallbackPrimary, snapshot.Preferred)
	require.False(t, snapshot.Probing)
	require.Zero(t, snapshot.PrimaryFailure)

	// A target refusal proves the proxy transport is healthy and must not
	// start a cooldown or change the preference.
	selector.report(1, fallbackPrimary, false, dialTargetFailure)
	snapshot = selector.snapshot()
	require.Equal(t, fallbackPrimary, snapshot.Preferred)
	require.True(t, snapshot.PrimaryUntil.IsZero())
}

func TestFallbackSelectorCooldownAndSingleFlightProbe(t *testing.T) {
	now := time.Unix(2000, 0)
	selector := newFallbackSelector(1)
	selector.now = func() time.Time { return now }
	selector.cooldown = 30 * time.Second

	selector.report(1, fallbackPrimary, false, dialTransportFailure)
	snapshot := selector.snapshot()
	require.Equal(t, fallbackSecondary, snapshot.Preferred)
	require.Equal(t, uint64(1), snapshot.PrimaryFailure)
	require.Equal(t, now.Add(30*time.Second), snapshot.PrimaryUntil)

	now = now.Add(10 * time.Second)
	choice, probe, ok := selector.choose(1)
	require.True(t, ok)
	require.Equal(t, fallbackSecondary, choice)
	require.False(t, probe)

	now = now.Add(20 * time.Second)
	choice, probe, ok = selector.choose(1)
	require.True(t, ok)
	require.Equal(t, fallbackPrimary, choice)
	require.True(t, probe, "the first business request after cooldown becomes the single recovery probe")

	choice, probe, ok = selector.choose(1)
	require.True(t, ok)
	require.Equal(t, fallbackSecondary, choice)
	require.False(t, probe, "other concurrent requests must stay on the secondary while the probe runs")

	// The probe failed at the transport layer: cooldown is extended, not reset
	// to the initial instant.
	now = now.Add(5 * time.Second)
	selector.report(1, fallbackPrimary, true, dialTransportFailure)
	snapshot = selector.snapshot()
	require.Equal(t, fallbackSecondary, snapshot.Preferred)
	require.Equal(t, uint64(2), snapshot.PrimaryFailure)
	require.Equal(t, now.Add(60*time.Second), snapshot.PrimaryUntil)
	require.False(t, snapshot.Probing)
}

func TestFallbackSelectorProbeRecoveryAndTargetReply(t *testing.T) {
	now := time.Unix(3000, 0)
	selector := newFallbackSelector(1)
	selector.now = func() time.Time { return now }
	selector.baseCooldown = 10 * time.Second
	selector.cooldown = 10 * time.Second

	selector.report(1, fallbackPrimary, false, dialTransportFailure)
	now = now.Add(10 * time.Second)
	choice, probe, ok := selector.choose(1)
	require.True(t, ok)
	require.Equal(t, fallbackPrimary, choice)
	require.True(t, probe)

	// A successful handshake on the probe restores primary preference.
	selector.report(1, fallbackPrimary, true, dialSuccess)
	choice, probe, ok = selector.choose(1)
	require.True(t, ok)
	require.Equal(t, fallbackPrimary, choice)
	require.False(t, probe)
	require.True(t, selector.snapshot().PrimaryUntil.IsZero())

	// A target reply also proves the transport is healthy.
	selector.report(1, fallbackPrimary, false, dialTransportFailure)
	now = now.Add(10 * time.Second)
	choice, probe, ok = selector.choose(1)
	require.True(t, ok)
	require.True(t, probe)
	selector.report(1, fallbackPrimary, true, dialTargetFailure)
	require.Equal(t, fallbackPrimary, selector.snapshot().Preferred)
}

func TestFallbackSelectorProbeCancellationAndGeneration(t *testing.T) {
	now := time.Unix(4000, 0)
	selector := newFallbackSelector(1)
	selector.now = func() time.Time { return now }
	selector.baseCooldown = 10 * time.Second
	selector.cooldown = 10 * time.Second

	selector.report(1, fallbackPrimary, false, dialTransportFailure)
	now = now.Add(10 * time.Second)
	_, probe, ok := selector.choose(1)
	require.True(t, ok)
	require.True(t, probe)

	// A caller cancellation does not publish a transport result, but it must
	// release the probe latch so a later request can try again.
	selector.report(1, fallbackPrimary, true, dialCallerFailure)
	require.False(t, selector.snapshot().Probing)
	choice, probe, ok := selector.choose(1)
	require.True(t, ok)
	require.Equal(t, fallbackPrimary, choice)
	require.True(t, probe)

	// A completion from generation 1 must not change generation 2.
	selector.reset(2)
	require.Equal(t, uint64(2), selector.snapshot().Generation)
	selector.report(1, fallbackPrimary, false, dialTransportFailure)
	require.Equal(t, fallbackPrimary, selector.snapshot().Preferred)
	require.Equal(t, uint64(2), selector.snapshot().Generation)
	_, _, ok = selector.choose(1)
	require.False(t, ok, "a stale generation may not choose a transport")

	selector.close()
	_, _, ok = selector.choose(2)
	require.False(t, ok)
}

func TestClassifyDialError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	testCases := []struct {
		name string
		ctx  context.Context
		err  error
		want dialFailureClass
	}{
		{"success", context.Background(), nil, dialSuccess},
		{"canceled", ctx, errors.New("anything"), dialCallerFailure},
		{"client stopped", context.Background(), mieruclient.ErrClientIsNotRunning, dialCallerFailure},
		{"dns", context.Background(), &net.DNSError{Err: "no such host", Name: "example.com"}, dialCallerFailure},
		{"target reply", context.Background(), errors.New("server returned socks5 error code 1"), dialTargetFailure},
		{"dial refused", context.Background(), errors.New("NewTCPUnderlay() failed: DialContext() failed: dial tcp 127.0.0.1:1: connect: connection refused"), dialTransportFailure},
		{"write failure", context.Background(), errors.New("failed to write socks5 connection request to the server: broken pipe"), dialTransportFailure},
		{"handshake timeout", context.Background(), fmt.Errorf("failed to read socks5 connection response from the server: %w", timeoutTestError{}), dialTransportFailure},
		{"reset", context.Background(), errors.New("failed to read socks5 connection response from the server: read tcp: connection reset by peer"), dialTransportFailure},
		{"bare eof is ambiguous", context.Background(), fmt.Errorf("failed to read socks5 connection response from the server: %w", io.EOF), dialAmbiguousFailure},
		{"unknown is ambiguous", context.Background(), errors.New("something else"), dialAmbiguousFailure},
		{"typed network", context.Background(), stderror.WrapErrorWithType(errors.New("wire failure"), stderror.NETWORK_ERROR), dialTransportFailure},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.want, classifyDialError(testCase.ctx, testCase.err))
		})
	}
}

type fakeClient struct {
	dialFunc func(context.Context, net.Addr) (net.Conn, error)
	dials    int
	running  bool
	startErr error
}

func (c *fakeClient) Load() (*mieruclient.ClientConfig, error) {
	return nil, mieruclient.ErrNoClientConfig
}
func (c *fakeClient) Store(*mieruclient.ClientConfig) error { return nil }
func (c *fakeClient) Start() error {
	if c.startErr != nil {
		return c.startErr
	}
	c.running = true
	return nil
}
func (c *fakeClient) Stop() error     { c.running = false; return nil }
func (c *fakeClient) IsRunning() bool { return c.running }
func (c *fakeClient) DialContext(ctx context.Context, address net.Addr) (net.Conn, error) {
	c.dials++
	if c.dialFunc == nil {
		return nil, errors.New("no fake dial result")
	}
	return c.dialFunc(ctx, address)
}

func fakeRuntimeForTest(label string, client *fakeClient) *transportRuntime {
	return &transportRuntime{label: label, transport: mieruTransport{tcp: true}, client: client}
}

func TestFallbackTargetRefusalDoesNotTrySecondary(t *testing.T) {
	primary := &fakeClient{dialFunc: func(context.Context, net.Addr) (net.Conn, error) {
		return nil, errors.New("server returned socks5 error code 1")
	}}
	secondary := &fakeClient{dialFunc: func(context.Context, net.Addr) (net.Conn, error) {
		t.Fatal("target refusal must not trigger the secondary transport")
		return nil, nil
	}}
	selector := newFallbackSelector(1)
	outboundInstance := &Outbound{logger: nopLogger()}
	spec, err := toNetAddrSpec(M.ParseSocksaddr("192.0.2.60:80"), N.NetworkTCP)
	require.NoError(t, err)

	_, err = outboundInstance.dialWithFallback(context.Background(), spec,
		fakeRuntimeForTest("primary", primary), fakeRuntimeForTest("secondary", secondary), selector, 1)
	require.Error(t, err)
	require.Equal(t, 1, primary.dials)
	require.Zero(t, secondary.dials)
	require.Equal(t, fallbackPrimary, selector.snapshot().Preferred)
}

func TestFallbackBothSilentFailuresDoNotLeaveSecondaryPreferred(t *testing.T) {
	primary := &fakeClient{dialFunc: func(context.Context, net.Addr) (net.Conn, error) {
		return nil, fmt.Errorf("failed to read socks5 connection response from the server: %w", timeoutTestError{})
	}}
	secondary := &fakeClient{dialFunc: func(context.Context, net.Addr) (net.Conn, error) {
		return nil, fmt.Errorf("failed to read socks5 connection response from the server: %w", timeoutTestError{})
	}}
	selector := newFallbackSelector(1)
	outboundInstance := &Outbound{logger: nopLogger()}
	spec, err := toNetAddrSpec(M.ParseSocksaddr("192.0.2.61:80"), N.NetworkTCP)
	require.NoError(t, err)

	_, err = outboundInstance.dialWithFallback(context.Background(), spec,
		fakeRuntimeForTest("primary", primary), fakeRuntimeForTest("secondary", secondary), selector, 1)
	require.Error(t, err)
	require.Equal(t, 1, primary.dials)
	require.Equal(t, 1, secondary.dials)
	require.Equal(t, fallbackPrimary, selector.snapshot().Preferred,
		"two silent handshakes must not be published as a durable transport preference")
}

func TestFallbackSelectorAmbiguousProbeBacksOff(t *testing.T) {
	now := time.Unix(4500, 0)
	selector := newFallbackSelector(1)
	selector.now = func() time.Time { return now }
	selector.cooldown = 10 * time.Second
	selector.baseCooldown = 10 * time.Second

	selector.report(1, fallbackPrimary, false, dialTransportFailure)
	now = now.Add(10 * time.Second)
	_, probe, ok := selector.choose(1)
	require.True(t, ok)
	require.True(t, probe)
	selector.report(1, fallbackPrimary, true, dialAmbiguousFailure)

	snapshot := selector.snapshot()
	require.Equal(t, fallbackSecondary, snapshot.Preferred)
	require.False(t, snapshot.Probing)
	require.True(t, snapshot.PrimaryUntil.After(now),
		"a silent probe must extend the cooldown instead of allowing a probe on every request")

	choice, probe, ok := selector.choose(1)
	require.True(t, ok)
	require.Equal(t, fallbackSecondary, choice)
	require.False(t, probe)
}

func TestTransportRuntimeClosesConnectionReturnedWithHandshakeError(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	var closed atomic.Int32
	client := &fakeClient{dialFunc: func(context.Context, net.Addr) (net.Conn, error) {
		return &closeTrackingConn{Conn: clientConn, closed: &closed}, fmt.Errorf(
			"failed to read socks5 connection response from the server: %w", io.EOF)
	}}
	runtime := fakeRuntimeForTest("primary", client)
	spec, err := toNetAddrSpec(M.ParseSocksaddr("192.0.2.62:80"), N.NetworkTCP)
	require.NoError(t, err)

	conn, class, err := runtime.dial(context.Background(), spec)
	require.Nil(t, conn)
	require.Error(t, err)
	require.Equal(t, dialAmbiguousFailure, class)
	require.Equal(t, int32(1), closed.Load(),
		"the SDK returns a live connection together with a handshake error; dropping it leaks a session")
}

func BenchmarkFallbackSelectorChooseReport(b *testing.B) {
	selector := newFallbackSelector(1)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		choice, probe, ok := selector.choose(1)
		if !ok || choice != fallbackPrimary || probe {
			b.Fatal("unexpected selector state")
		}
		selector.report(1, choice, probe, dialSuccess)
	}
}

func TestOutboundStartRollsBackBothRuntimesOnSecondaryFailure(t *testing.T) {
	primaryClient := &fakeClient{}
	secondaryClient := &fakeClient{startErr: errors.New("secondary start failed")}
	outboundInstance := &Outbound{
		logger:    nopLogger(),
		primary:   fakeRuntimeForTest("primary", primaryClient),
		secondary: fakeRuntimeForTest("secondary", secondaryClient),
		inFlight:  newInflightGroup(),
	}
	scope := adapter.NewScope(context.Background(), nopLogger())
	err := outboundInstance.Start(adapter.StartStateStart, scope)
	require.Error(t, err)
	require.Contains(t, err.Error(), "start fallback client")
	require.Nil(t, outboundInstance.primary)
	require.Nil(t, outboundInstance.secondary)
	require.False(t, outboundInstance.started)
	require.False(t, primaryClient.running, "the already-started primary must be rolled back")
	require.NoError(t, outboundInstance.Close())
	require.NoError(t, scope.Close())
}
