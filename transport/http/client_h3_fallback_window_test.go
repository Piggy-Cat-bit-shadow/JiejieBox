package http

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The H3 -> H2 version fallback needs its own establishment window.
//
// # The failure this pins
//
// The fallback branch has always been in DialContext, and it was unreachable in exactly the case
// it was written for. A peer whose UDP path is silently blackholed - packets dropped, nothing
// refused - makes the QUIC handshake wait until the CALLER's deadline. Only then does the code try
// HTTP/2, by which point the caller has given up, so the node looks like "does not work over UDP"
// rather than "fell back to TCP". Bounding the H3 attempt is what makes the fallback real.

// hangingHTTP3Client never succeeds until its context ends, which is what a blackholed UDP path
// looks like from here: no refusal, just silence.
type hangingHTTP3Client struct {
	calls   int
	started chan struct{}
}

func (c *hangingHTTP3Client) DialContext(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
	c.calls++
	if c.started != nil {
		select {
		case c.started <- struct{}{}:
		default:
		}
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *hangingHTTP3Client) OpenTunnel(ctx context.Context, request tunnelRequest) (DatagramStream, error) {
	return nil, ErrHTTP3Unavailable
}

func (c *hangingHTTP3Client) ResetConnection() {}
func (c *hangingHTTP3Client) Close() error     { return nil }

// failingHTTP3Client refuses immediately, like a peer with no UDP listener at all.
type failingHTTP3Client struct {
	calls int
	err   error
}

func (c *failingHTTP3Client) DialContext(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return nil, ErrHTTP3Unavailable
}

func (c *failingHTTP3Client) OpenTunnel(ctx context.Context, request tunnelRequest) (DatagramStream, error) {
	return nil, ErrHTTP3Unavailable
}

func (c *failingHTTP3Client) ResetConnection() {}
func (c *failingHTTP3Client) Close() error     { return nil }

// newFallbackTestClient builds a client whose H3 is the injected double and whose "H2" is a dialer
// that fails, so the observable outcome is WHICH branch was reached rather than a live connection.
func newFallbackTestClient(http3 http3Client, h2Err error) *Client {
	client := &Client{
		http3:                  http3,
		disableVersionFallback: false,
	}
	client.tlsDialer = nil
	client.http1Dialer = &recordingDialer{err: h2Err}
	return client
}

type recordingDialer struct {
	calls int
	err   error
}

func (d *recordingDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	return nil, errTestNoConn
}

func (d *recordingDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, errTestNoConn
}

var errTestNoConn = ErrHTTP3Unavailable

// A blackholed H3 attempt must not consume the caller's whole budget: the fallback must be reached
// while the caller still has time.
func TestHTTP3AttemptIsBoundedSoTheFallbackIsReachable(t *testing.T) {
	previous := http3EstablishTimeout
	http3EstablishTimeout = 50 * time.Millisecond
	t.Cleanup(func() { http3EstablishTimeout = previous })

	h3 := &hangingHTTP3Client{}
	h1 := &recordingDialer{err: errTestNoConn}
	client := &Client{http3: h3, http1Dialer: h1}

	// A caller budget far larger than the window. Before the window existed this dial would block
	// for the entire budget and never reach HTTP/1.1.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	_, _ = client.DialContext(ctx, "tcp", M.ParseSocksaddr("127.0.0.1:443"))
	elapsed := time.Since(start)

	require.Equal(t, 1, h3.calls, "the H3 attempt must have been made")
	require.Equal(t, 1, h1.calls,
		"the fallback must have been reached: without a window it is unreachable in exactly this case")
	require.Less(t, elapsed, 5*time.Second,
		"the dial must not have waited for the caller's whole budget")
	require.ErrorIs(t, ctx.Err(), nil, "the caller's context must still be alive when the fallback runs")
}

// An expired window is still a failed H3 attempt: it must be remembered, or the window is paid on
// every single connection.
func TestExpiredHTTP3WindowIsRemembered(t *testing.T) {
	previous := http3EstablishTimeout
	http3EstablishTimeout = 20 * time.Millisecond
	t.Cleanup(func() { http3EstablishTimeout = previous })

	h3 := &hangingHTTP3Client{}
	client := &Client{http3: h3, http1Dialer: &recordingDialer{err: errTestNoConn}}

	_, _ = client.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:443"))
	require.Equal(t, 1, h3.calls)
	require.True(t, client.http3Broken.Load() != 0,
		"an expired window must mark H3 broken, or every connection pays the window again")
	require.False(t, client.http3Available())
}

// A caller that goes away is a LOCAL lifecycle event. It is not evidence about H3, and it is not a
// reason to start an H2 dial for a user who has already left.
func TestCallerCancelIsNeutralForHTTP3(t *testing.T) {
	previous := http3EstablishTimeout
	http3EstablishTimeout = 10 * time.Second
	t.Cleanup(func() { http3EstablishTimeout = previous })

	h3 := &hangingHTTP3Client{started: make(chan struct{}, 1)}
	h1 := &recordingDialer{err: errTestNoConn}
	client := &Client{http3: h3, http1Dialer: h1}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.DialContext(ctx, "tcp", M.ParseSocksaddr("127.0.0.1:443"))
		done <- err
	}()
	<-h3.started
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("the dial did not return after the caller cancelled")
	}
	require.Zero(t, h1.calls,
		"a cancelled caller must not trigger an H2 fallback: nobody is waiting for it")
	require.Zero(t, client.http3Broken.Load(),
		"a caller cancel is not evidence about H3")
}

// An immediate refusal is still an H3 failure and must behave exactly as it did before the window
// was added - the window must not change the refusal path.
func TestImmediateHTTP3RefusalStillFallsBackAndIsRemembered(t *testing.T) {
	h3 := &failingHTTP3Client{}
	h1 := &recordingDialer{err: errTestNoConn}
	client := &Client{http3: h3, http1Dialer: h1}

	_, _ = client.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:443"))
	require.Equal(t, 1, h3.calls)
	require.Equal(t, 1, h1.calls, "a refusal falls back")
	require.True(t, client.http3Broken.Load() != 0, "and is remembered")
}

// A real error that is not "HTTP/3 unavailable" must still be returned rather than swallowed into a
// fallback, unless the fallback is explicitly enabled for it.
func TestNonAvailabilityHTTP3ErrorIsReturned(t *testing.T) {
	h3 := &failingHTTP3Client{err: context.DeadlineExceeded}
	h1 := &recordingDialer{err: errTestNoConn}
	client := &Client{http3: h3, http1Dialer: h1}

	_, err := client.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:443"))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Zero(t, h1.calls, "a non-availability error is not a reason to fall back")
}

// answeredHTTP3Client delivers the proxy's ANSWER at the instant the establishment window expires,
// which is the interleaving a timer cannot be trusted to distinguish: the request came back, and
// our own clock also ran out.
type answeredHTTP3Client struct {
	response *http.Response
	calls    int
}

func (c *answeredHTTP3Client) DialContext(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
	c.calls++
	<-ctx.Done()
	return nil, statusError(c.response)
}

func (c *answeredHTTP3Client) OpenTunnel(ctx context.Context, request tunnelRequest) (DatagramStream, error) {
	<-ctx.Done()
	return nil, statusError(c.response)
}

func (c *answeredHTTP3Client) ResetConnection() {}
func (c *answeredHTTP3Client) Close() error     { return nil }

// TestProxyAnswerOutranksTheHTTP3WindowExpiry is the plain-CONNECT half of the interleaving.
//
// The proxy answered 407, and the local window expired in the same instant. The answer is the
// stronger fact: it is returned as-is, no second CONNECT is sent over HTTP/2, and the transport is
// not remembered as broken - an authentication rejection says nothing about QUIC.
func TestProxyAnswerOutranksTheHTTP3WindowExpiry(t *testing.T) {
	previous := http3EstablishTimeout
	http3EstablishTimeout = 20 * time.Millisecond
	t.Cleanup(func() { http3EstablishTimeout = previous })

	h3 := &answeredHTTP3Client{response: &http.Response{StatusCode: 407, Status: "407 Proxy Authentication Required"}}
	h1 := &recordingDialer{err: errTestNoConn}
	client := &Client{http3: h3, http1Dialer: h1}

	_, err := client.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:443"))
	require.ErrorContains(t, err, "authentication required")
	require.Zero(t, h1.calls,
		"a CONNECT the proxy already answered must not be duplicated over HTTP/2")
	require.Zero(t, client.http3Broken.Load(),
		"an application answer is not a transport verdict")
}

// TestTunnelProxyAnswerOutranksTheHTTP3WindowExpiry is the tunnel half.
//
// A CONNECT-IP setup may create server-side state; answering it with a second setup because our
// own timer fired is exactly the duplicate the "was it answered?" distinction exists to prevent.
func TestTunnelProxyAnswerOutranksTheHTTP3WindowExpiry(t *testing.T) {
	previous := http3TunnelEstablishTimeout
	http3TunnelEstablishTimeout = 20 * time.Millisecond
	t.Cleanup(func() { http3TunnelEstablishTimeout = previous })

	h3 := &answeredHTTP3Client{response: &http.Response{StatusCode: 403, Status: "403 Forbidden"}}
	h1 := &recordingDialer{err: errTestNoConn}
	client := &Client{http3: h3, http1Dialer: h1, server: M.ParseSocksaddr("127.0.0.1:443")}

	_, err := client.OpenTunnel(context.Background(), "connect-ip", "/")
	require.ErrorContains(t, err, "unexpected status: 403")
	require.Zero(t, h1.calls,
		"a tunnel the server already answered must not be set up again over HTTP/1.1")
	require.Zero(t, client.http3Broken.Load())
}
