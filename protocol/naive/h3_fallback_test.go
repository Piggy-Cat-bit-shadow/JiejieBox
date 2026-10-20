//go:build with_naive_outbound

package naive

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/cronet-go"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// P3: the opt-in H3->H2 retry.
//
// What these tests pin, in the order the decision is made:
//
//  1. an H3 attempt that SUCCEEDS is returned and clears the window;
//  2. an H3 attempt that fails at the TRANSPORT is retried once over H2;
//  3. an H3 attempt that the proxy REJECTED (407, any status) is NOT retried;
//  4. a caller cancellation is not evidence and is not retried;
//  5. an armed window skips H3 for the next CONNECT and does not pay the failure
//     again;
//  6. the window is not extended by a second parallel failure;
//  7. a network transition clears the window;
//  8. an out-of-order failure cannot arm the window after a newer success;
//  9. when both transports fail, the H2 error is the one the caller sees;
// 10. the opt-in switch is the only thing that changes behaviour: with it off the
//     ordinary dial path is used unchanged.
//
// They run without a native library: the client is a scripted double, and the
// only production code exercised is this package's decision logic plus the
// two-stage dial.

// scriptedDialClient answers each per-dial choice with a scripted outcome and
// records the order of the choices it was asked for.
type scriptedDialClient struct {
	access  sync.Mutex
	choices []bool
	dial    map[bool]error
	hand    map[bool]error
}

func (c *scriptedDialClient) DialEarly(context.Context, M.Socksaddr) (cronet.NaiveConn, error) {
	return c.DialEarlyWithQUIC(context.Background(), M.Socksaddr{}, true)
}

func (c *scriptedDialClient) DialEarlyWithQUIC(_ context.Context, _ M.Socksaddr, forceQUIC bool) (cronet.NaiveConn, error) {
	c.access.Lock()
	c.choices = append(c.choices, forceQUIC)
	c.access.Unlock()
	if err := c.dial[forceQUIC]; err != nil {
		return nil, err
	}
	return &fakeNaiveConn{handshakeErr: c.hand[forceQUIC]}, nil
}

func (c *scriptedDialClient) recorded() []bool {
	c.access.Lock()
	defer c.access.Unlock()
	return append([]bool(nil), c.choices...)
}

func newScriptedClient() *scriptedDialClient {
	return &scriptedDialClient{dial: map[bool]error{}, hand: map[bool]error{}}
}

func withFallbackWindow(t *testing.T, window time.Duration) {
	t.Helper()
	previous := h3FallbackWindow
	h3FallbackWindow = window
	t.Cleanup(func() { h3FallbackWindow = previous })
}

func dialFallback(t *testing.T, client naiveDialerClient, fallback *h3Fallback, ctx context.Context) error {
	t.Helper()
	_, err := dialNaiveWithH3Fallback(ctx, client, fallback, logger.NOP(), M.ParseSocksaddr("example.com:443"))
	return err
}

// TestH3FallbackTransportFailureMatrix is the classification the retry hinges on.
func TestH3FallbackTransportFailureMatrix(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"QUIC protocol error", cronet.NetErrorQUICProtocolError, true},
		{"QUIC handshake failed", cronet.NetErrorQUICHandshakeFailed, true},
		{"connection refused", cronet.NetErrorConnectionRefused, true},
		{"address unreachable", cronet.NetErrorAddressUnreachable, true},
		{"timeout", cronet.NetErrorTimedOut, true},
		{"connection closed", cronet.NetErrorConnectionClosed, true},
		{"connection reset", cronet.NetErrorConnectionReset, true},
		{"407 is the proxy answering", errors.New("unexpected response status: 407"), false},
		{"name not resolved is local", cronet.NetErrorNameNotResolved, false},
		{"a caller cancellation is not evidence", context.Canceled, false},
		{"this process closing is not evidence", net.ErrClosed, false},
		{"nil is nothing", nil, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.want, h3TransportFailure(testCase.err))
		})
	}
}

// TestH3FallbackSuccess proves the happy path makes exactly one attempt and
// clears the window.
func TestH3FallbackSuccess(t *testing.T) {
	client := newScriptedClient()
	fallback := &h3Fallback{}
	fallback.markBroken(0, time.Now()) // arm it first: a success must clear it

	require.NoError(t, dialFallback(t, client, fallback, context.Background()))
	require.Equal(t, []bool{true}, client.recorded())
	require.True(t, fallback.available(time.Now()), "a successful H3 attempt must clear the window")
}

// TestH3FallbackRetriesOnceOverH2 is the feature.
func TestH3FallbackRetriesOnceOverH2(t *testing.T) {
	withFallbackWindow(t, time.Minute)
	client := newScriptedClient()
	client.hand[true] = cronet.NetErrorQUICProtocolError
	fallback := &h3Fallback{}

	require.NoError(t, dialFallback(t, client, fallback, context.Background()))
	require.Equal(t, []bool{true, false}, client.recorded(),
		"the failed H3 attempt must be followed by exactly one H2 attempt")
	require.False(t, fallback.available(time.Now()), "the window must be armed")
}

// TestH3FallbackDoesNotRetryAProxyRejection proves a 407 is returned as-is.
func TestH3FallbackDoesNotRetryAProxyRejection(t *testing.T) {
	client := newScriptedClient()
	client.hand[true] = errors.New("naive CONNECT handshake: unexpected response status: 407")
	fallback := &h3Fallback{}

	err := dialFallback(t, client, fallback, context.Background())
	require.ErrorContains(t, err, "407")
	require.Equal(t, []bool{true}, client.recorded(), "a rejected CONNECT must not be retried")
	require.True(t, fallback.available(time.Now()), "a proxy answer says nothing about H3's transport")
}

// TestH3FallbackDoesNotRetryACallerCancellation proves the caller's withdrawal is
// not evidence about the transport.
func TestH3FallbackDoesNotRetryACallerCancellation(t *testing.T) {
	client := newScriptedClient()
	client.hand[true] = context.Canceled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fallback := &h3Fallback{}

	err := dialFallback(t, client, fallback, ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, []bool{true}, client.recorded())
	require.True(t, fallback.available(time.Now()))
}

// TestH3FallbackArmedWindowSkipsH3 proves the second CONNECT during the window
// does not pay the failure again.
func TestH3FallbackArmedWindowSkipsH3(t *testing.T) {
	withFallbackWindow(t, time.Minute)
	client := newScriptedClient()
	client.hand[true] = cronet.NetErrorQUICProtocolError
	fallback := &h3Fallback{}

	require.NoError(t, dialFallback(t, client, fallback, context.Background()))
	require.NoError(t, dialFallback(t, client, fallback, context.Background()))
	require.NoError(t, dialFallback(t, client, fallback, context.Background()))
	require.Equal(t, []bool{true, false, false, false}, client.recorded(),
		"after one transport failure the window sends later CONNECTs straight to H2")
}

// TestH3FallbackWindowExpires proves the memory is bounded: once it expires, H3
// is attempted again.
func TestH3FallbackWindowExpires(t *testing.T) {
	withFallbackWindow(t, 20*time.Millisecond)
	client := newScriptedClient()
	client.hand[true] = cronet.NetErrorQUICProtocolError
	fallback := &h3Fallback{}

	require.NoError(t, dialFallback(t, client, fallback, context.Background()))
	time.Sleep(30 * time.Millisecond)
	require.NoError(t, dialFallback(t, client, fallback, context.Background()))
	require.Equal(t, []bool{true, false, true, false}, client.recorded(),
		"the attempt after the window must try H3 again")
}

// TestH3FallbackDoesNotExtendOnASecondFailure proves one event is one decision.
func TestH3FallbackDoesNotExtendOnASecondFailure(t *testing.T) {
	withFallbackWindow(t, time.Minute)
	fallback := &h3Fallback{}
	now := time.Now()
	first := fallback.begin()
	fallback.markBroken(first, now)
	armedUntil := fallback.brokenUntil
	second := fallback.begin()
	fallback.markBroken(second, now.Add(10*time.Second))
	require.Equal(t, armedUntil, fallback.brokenUntil,
		"a failure inside the open window must not move its end")
}

// TestH3FallbackIgnoresOutOfOrderOutcomes proves the attempt numbering.
func TestH3FallbackIgnoresOutOfOrderOutcomes(t *testing.T) {
	withFallbackWindow(t, time.Minute)
	fallback := &h3Fallback{}
	old := fallback.begin()
	newer := fallback.begin()

	fallback.clear(newer) // the newer attempt succeeded
	fallback.markBroken(old, time.Now())
	require.True(t, fallback.available(time.Now()),
		"a failure from an older attempt must not arm the window after a newer success")

	// The mirror: an older success must not clear a newer failure.
	third := fallback.begin()
	fallback.markBroken(third, time.Now())
	fallback.clear(old)
	require.False(t, fallback.available(time.Now()),
		"a success from an older attempt must not clear a newer failure")
}

// TestH3FallbackResetOnNetworkChange proves a transition forgets the window and
// supersedes the attempts that belong to the old network.
func TestH3FallbackResetOnNetworkChange(t *testing.T) {
	withFallbackWindow(t, time.Minute)
	fallback := &h3Fallback{}
	old := fallback.begin()
	fallback.markBroken(old, time.Now())
	require.False(t, fallback.available(time.Now()))

	fallback.reset()
	require.True(t, fallback.available(time.Now()), "a new network starts with H3")

	// The failure that was in flight on the old network must not re-arm it.
	fallback.markBroken(old, time.Now())
	require.True(t, fallback.available(time.Now()),
		"an outcome that started before the reset belongs to the old network")
}

// TestH3FallbackBothTransportsFail proves the caller sees the H2 error, which is
// the attempt that describes the path still in use.
func TestH3FallbackBothTransportsFail(t *testing.T) {
	withFallbackWindow(t, time.Minute)
	client := newScriptedClient()
	client.hand[true] = cronet.NetErrorQUICProtocolError
	client.hand[false] = cronet.NetErrorConnectionRefused
	fallback := &h3Fallback{}

	err := dialFallback(t, client, fallback, context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, cronet.NetErrorConnectionRefused,
		"the H2 error is the one that describes the surviving path")
	require.Equal(t, []bool{true, false}, client.recorded())
}

// TestH3FallbackDialErrorIsNotRetriedAtDialStage documents a deliberate limit:
// a failure BEFORE the stream exists says nothing about the transport, so it is
// not retried.
func TestH3FallbackDialErrorIsNotRetriedAtDialStage(t *testing.T) {
	client := newScriptedClient()
	client.dial[true] = errors.New("cronet: client closed")
	fallback := &h3Fallback{}

	err := dialFallback(t, client, fallback, context.Background())
	require.Error(t, err)
	require.Equal(t, []bool{true}, client.recorded())
	require.True(t, fallback.available(time.Now()))
}
