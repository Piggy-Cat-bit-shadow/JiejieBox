//go:build with_quic

// The CONNECT-IP tunnel setup is a separate branch from Client.DialContext, and it did not
// inherit the HTTP/3 establishment window. These tests pin the window on the branch that
// actually carries MASQUE traffic, plus the four rules around it: strict mode, caller
// cancellation, application refusals and the memory that makes the fallback useful.
package http

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

func freezeTunnelWindow(t *testing.T, window time.Duration) {
	t.Helper()
	previous := http3TunnelEstablishTimeout
	http3TunnelEstablishTimeout = window
	t.Cleanup(func() { http3TunnelEstablishTimeout = previous })
}

func tunnelRequestForTest() tunnelRequest {
	return tunnelRequest{
		protocol: "connect-ip",
		url:      &url.URL{Path: "/.well-known/masque/ip/*/*/"},
	}
}

// scriptedTunnelHTTP3Client scripts one OpenTunnel result, so the tunnel branch can be driven
// without a QUIC stack.
type scriptedTunnelHTTP3Client struct {
	open        func(ctx context.Context, request tunnelRequest) (DatagramStream, error)
	dialCalls   int
	tunnelCalls int
}

func (c *scriptedTunnelHTTP3Client) DialContext(context.Context, M.Socksaddr) (net.Conn, error) {
	c.dialCalls++
	return nil, ErrHTTP3Unavailable
}

func (c *scriptedTunnelHTTP3Client) OpenTunnel(ctx context.Context, request tunnelRequest) (DatagramStream, error) {
	c.tunnelCalls++
	return c.open(ctx, request)
}

func (c *scriptedTunnelHTTP3Client) ResetConnection() {}
func (c *scriptedTunnelHTTP3Client) Close() error     { return nil }

// The defect: with no window of its own, an H3 tunnel attempt against a silent UDP path waited
// for the CALLER's deadline (C.TCPTimeout, 15 s in MASQUE), returned the caller's deadline as an
// ordinary error, and never reached the HTTP/2 branch. The test uses a background context, so the
// OLD code would never return at all; a bounded return proves the attempt has its own budget.
func TestH3TunnelAttemptHasItsOwnWindow(t *testing.T) {
	freezeTunnelWindow(t, 40*time.Millisecond)

	h3 := &hangingTunnelHTTP3Client{}
	h1 := &recordingDialer{err: errTestNoConn}
	client := &Client{http3: h3, http1Dialer: h1}

	start := time.Now()
	_, _, transport, err := client.openTunnel(context.Background(), tunnelRequestForTest())
	elapsed := time.Since(start)

	require.Error(t, err)
	require.Equal(t, TunnelTransportUnknown, transport)
	require.Less(t, elapsed, 2*time.Second,
		"the tunnel attempt must be bounded by its own window, not by a caller that never cancels")
	require.Equal(t, 1, h1.calls,
		"once the window expires the HTTP/2/H1 branch must be reachable within the same call")
	require.NotZero(t, client.http3Broken.Load(),
		"an expired window is a failed attempt, so the next tunnel setup must not pay it again")
	require.False(t, client.http3Available())
}

// Strict mode must cover the tunnel branch as well: no HTTP/2 attempt and no armed memory.
func TestH3TunnelStrictWindowExpiryIsStillStrict(t *testing.T) {
	freezeTunnelWindow(t, 40*time.Millisecond)

	h3 := &hangingTunnelHTTP3Client{}
	h1 := &recordingDialer{err: errTestNoConn}
	client := &Client{http3: h3, http1Dialer: h1, disableVersionFallback: true}

	_, _, _, err := client.openTunnel(context.Background(), tunnelRequestForTest())
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "exceeded"),
		"a window expiry must be named, not returned as an unexplained deadline: %v", err)
	require.Zero(t, h1.calls, "strict means no HTTP/2 tunnel setup at all")
	require.Zero(t, client.http3Broken.Load(),
		"and arming the memory would be a fallback too: the next setup would skip H3")
}

// A caller whose budget is shorter than the window must not be delayed by it.
func TestH3TunnelCallerDeadlineIsNotExtendedByTheWindow(t *testing.T) {
	freezeTunnelWindow(t, 30*time.Second)

	h3 := &hangingTunnelHTTP3Client{}
	h1 := &recordingDialer{err: errTestNoConn}
	client := &Client{http3: h3, http1Dialer: h1}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, _, err := client.openTunnel(ctx, tunnelRequestForTest())
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, elapsed, 2*time.Second, "the caller's earlier deadline must win")
	require.Zero(t, h1.calls, "nobody is waiting for an HTTP/2 tunnel after the caller left")
	require.Zero(t, client.http3Broken.Load(), "a caller deadline is not evidence about H3")
}

// The window must leave the caller with budget: the whole point is that the
// HTTP/2 attempt runs BEFORE the caller's own deadline. This is the shape the
// MASQUE client uses (context.WithTimeout(c.ctx, C.TCPTimeout)).
func TestH3TunnelFallbackLeavesTheCallerBudget(t *testing.T) {
	freezeTunnelWindow(t, 50*time.Millisecond)

	h3 := &hangingTunnelHTTP3Client{}
	h1 := &recordingDialer{err: errTestNoConn}
	client := &Client{http3: h3, http1Dialer: h1}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := time.Now()
	_, _, _, err := client.openTunnel(ctx, tunnelRequestForTest())
	elapsed := time.Since(start)

	require.Error(t, err)
	require.NoError(t, ctx.Err(),
		"the caller's own budget must still be alive after the bounded H3 attempt")
	require.Less(t, elapsed, 2*time.Second, "H3 must not consume the caller's 15s budget")
	require.Equal(t, 1, h1.calls, "the fallback must have had the remaining budget to run")
}

// An application refusal from a WORKING H3 connection is not a transport verdict: the version
// came up, the server refused this CONNECT. Falling back would put the tunnel on HTTP/2 for a
// failure HTTP/2 would reproduce, and would arm a memory that is evidence about nothing.
func TestH3TunnelApplicationRefusalIsNotAVersionVerdict(t *testing.T) {
	freezeTunnelWindow(t, time.Second)

	refusal := statusError(&http.Response{StatusCode: http.StatusForbidden, Status: "403 Forbidden"})
	h3 := &scriptedTunnelHTTP3Client{open: func(context.Context, tunnelRequest) (DatagramStream, error) {
		return nil, refusal
	}}
	h1 := &recordingDialer{err: errTestNoConn}
	client := &Client{http3: h3, http1Dialer: h1}

	_, _, _, err := client.openTunnel(context.Background(), tunnelRequestForTest())
	require.Error(t, err)
	require.Zero(t, h1.calls, "an HTTP status refusal is not a reason to change HTTP version")
	require.Zero(t, client.http3Broken.Load())
}

// An immediate ErrHTTP3Unavailable (the transport could not come up) does fall back, and the
// memory is armed so the next setup skips the version that just failed.
func TestH3TunnelImmediateUnavailableFallsBack(t *testing.T) {
	h3 := &scriptedTunnelHTTP3Client{open: func(context.Context, tunnelRequest) (DatagramStream, error) {
		return nil, ErrHTTP3Unavailable
	}}
	h1 := &recordingDialer{err: errTestNoConn}
	client := &Client{http3: h3, http1Dialer: h1}

	_, _, _, err := client.openTunnel(context.Background(), tunnelRequestForTest())
	require.Error(t, err)
	require.Equal(t, 1, h1.calls)
	require.NotZero(t, client.http3Broken.Load())
}

// The success path must report HTTP/3, touch no other version and leave the memory clear.
func TestH3TunnelSuccessReportsHTTP3(t *testing.T) {
	h3 := &scriptedTunnelHTTP3Client{open: func(context.Context, tunnelRequest) (DatagramStream, error) {
		return newGatedDatagramStream(nil), nil
	}}
	h1 := &recordingDialer{err: errTestNoConn}
	client := &Client{http3: h3, http1Dialer: h1}

	conn, stream, transport, err := client.openTunnel(context.Background(), tunnelRequestForTest())
	require.NoError(t, err)
	require.Nil(t, conn, "an HTTP/3 tunnel is a datagram stream, not a stream connection")
	require.NotNil(t, stream)
	require.Equal(t, TunnelTransportHTTP3, transport)
	require.Zero(t, h1.calls)
	require.Zero(t, client.http3Broken.Load())
	require.NoError(t, stream.Close())
}

// A window expiry must not be reported as the caller's deadline when the caller is still there;
// the retry loop logs this error, and "context deadline exceeded" would send an operator looking
// at the wrong budget.
func TestH3TunnelWindowExpiryErrorNamesTheWindow(t *testing.T) {
	freezeTunnelWindow(t, 20*time.Millisecond)

	h3 := &hangingTunnelHTTP3Client{}
	h1 := &recordingDialer{err: errTestNoConn}
	client := &Client{http3: h3, http1Dialer: h1, disableVersionFallback: true}

	_, _, _, err := client.openTunnel(context.Background(), tunnelRequestForTest())
	require.Error(t, err)
	require.Contains(t, err.Error(), http3TunnelEstablishTimeout.String())
}

// The window must not cut into a healthy tunnel: a real loopback HTTP/3 CONNECT-IP setup
// (UDP socket + QUIC handshake + one CONNECT round trip) has to finish far inside it. This is
// also the measurement the window value is justified by: it is four round trips of ~750 ms in
// the budget, not a guess copied from the stream-dial window.
func TestH3TunnelSetupOverRealLoopbackIsFarInsideTheWindow(t *testing.T) {
	pair := newStandPair(t)
	stand := newStandClient(t, pair.source)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := time.Now()
	stream, transport, err := stand.client.OpenTunnelWithInfo(ctx, "connect-ip", "/.well-known/masque/ip/*/*/")
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.Equal(t, TunnelTransportHTTP3, transport)
	require.NotNil(t, stream)
	require.NoError(t, stream.Close())
	require.Less(t, elapsed, http3TunnelEstablishTimeout,
		"a real loopback setup must not depend on the window; measured %s", elapsed)
	t.Logf("CONNECT-IP setup over loopback HTTP/3: %s (window %s)", elapsed, http3TunnelEstablishTimeout)
}

// The tunnel returned by the setup is USABLE, not merely a 200: payload written
// after the window has been cancelled out of the setup context still reaches the
// server and comes back. This is the "200 is the beginning, not the end" rule on
// the branch the window touches.
func TestH3TunnelOverRealLoopbackCarriesPayload(t *testing.T) {
	pair := newStandPair(t)
	stand := newStandClient(t, pair.source)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stream, _, err := stand.client.OpenTunnelWithInfo(ctx, "connect-ip", "/.well-known/masque/ip/*/*/")
	require.NoError(t, err)
	defer stream.Close()

	payload := []byte("connect-ip-payload")
	_, err = stream.Write(payload)
	require.NoError(t, err)

	echoed := make([]byte, len(payload))
	echoedCh := make(chan error, 1)
	go func() {
		_, readErr := io.ReadFull(stream, echoed)
		echoedCh <- readErr
	}()
	select {
	case readErr := <-echoedCh:
		require.NoError(t, readErr)
	case <-time.After(5 * time.Second):
		t.Fatal("the tunnel did not carry the payload back")
	}
	require.Equal(t, payload, echoed)
	_, _, _, tunnels, _, _, _ := pair.egressA.snapshot()
	require.NotZero(t, tunnels)
}
