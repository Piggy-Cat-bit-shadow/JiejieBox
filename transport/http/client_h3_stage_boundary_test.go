//go:build with_quic

package http

import (
	"context"
	"net"
	"testing"
	"time"

	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The safety boundary of an HTTP/3 CONNECT attempt: before the request header is written the peer
// cannot have processed anything and another version may be tried; after it, "did the peer see
// it?" is unknown and a second tunnel would be a guess. These tests drive both sides of that
// boundary through the production entry points.

type boundaryHTTP3Client struct {
	dialErr       error
	openTunnelErr error
	dialCalls     int
	tunnelCalls   int
}

func (c *boundaryHTTP3Client) DialContext(context.Context, M.Socksaddr) (net.Conn, error) {
	c.dialCalls++
	return nil, c.dialErr
}

func (c *boundaryHTTP3Client) OpenTunnel(context.Context, tunnelRequest) (DatagramStream, error) {
	c.tunnelCalls++
	return nil, c.openTunnelErr
}

func (c *boundaryHTTP3Client) ResetConnection() {}
func (c *boundaryHTTP3Client) Close() error     { return nil }

func newStageBoundaryClient(h3 http3Client, tcp *recordingDialer) *Client {
	return &Client{
		http3:       h3,
		http1Dialer: tcp,
		server:      M.ParseSocksaddr("127.0.0.1:443"),
	}
}

// TestPostSendResponsePendingDoesNotOpenASecondTunnel is the review's B2/B3 correction: the header
// went out, the 200 did not arrive, and the server may have accepted the tunnel already. Opening a
// second one over HTTP/2 is what the old code did (with the H3 address still allocated), so this
// attempt must report the stage instead.
func TestPostSendResponsePendingDoesNotOpenASecondTunnel(t *testing.T) {
	h3 := &boundaryHTTP3Client{
		openTunnelErr: newHTTP3ResponsePendingError(context.DeadlineExceeded),
	}
	tcp := &recordingDialer{err: E.New("tcp fallback reached")}
	client := newStageBoundaryClient(h3, tcp)

	_, err := client.OpenTunnel(context.Background(), "connect-ip", "/")
	require.Error(t, err)
	require.ErrorContains(t, err, "refusing to open a second tunnel over HTTP/2")
	require.Zero(t, tcp.calls, "no TCP fallback may be started for a possibly-processed CONNECT")
	require.Zero(t, client.http3Broken.Load(), "and it is not a transport verdict")
}

// TestPreSendTimeoutStillFallsBackToTCP is the availability half: nothing was written, so the
// fallback is safe and must still happen.
func TestPreSendTimeoutStillFallsBackToTCP(t *testing.T) {
	h3 := &boundaryHTTP3Client{
		openTunnelErr: E.Cause(errHTTP3PreSend, context.DeadlineExceeded),
	}
	tcp := &recordingDialer{err: E.New("tcp fallback reached")}
	client := newStageBoundaryClient(h3, tcp)

	_, err := client.OpenTunnel(context.Background(), "connect-ip", "/")
	require.ErrorContains(t, err, "tcp fallback reached",
		"a CONNECT that never wrote its header must still reach the fallback")
	require.Zero(t, client.http3Broken.Load(), "our own window is not a transport verdict")
}

// TestRetryAfterAPossiblySentSetupTakesTheTCPPath is the availability half of the same rule: the
// first setup still fails (no duplicate tunnel), but the caller's RETRY must not pay the same H3
// timeout again - it takes the TCP path directly.
func TestRetryAfterAPossiblySentSetupTakesTheTCPPath(t *testing.T) {
	h3 := &boundaryHTTP3Client{
		openTunnelErr: newHTTP3ResponsePendingError(context.DeadlineExceeded),
	}
	tcp := &recordingDialer{err: E.New("tcp fallback reached")}
	client := newStageBoundaryClient(h3, tcp)

	_, err := client.OpenTunnel(context.Background(), "connect-ip", "/")
	require.ErrorContains(t, err, "refusing to open a second tunnel")

	_, err = client.OpenTunnel(context.Background(), "connect-ip", "/")
	require.ErrorContains(t, err, "tcp fallback reached",
		"the retry must take the TCP path instead of paying the HTTP/3 timeout again")
	require.Equal(t, 1, h3.tunnelCalls, "the retry must not attempt HTTP/3 again")
	require.Equal(t, 1, tcp.calls)

	client.ResetConnections()
	require.Zero(t, client.http3TunnelAmbiguousUntil.Load(),
		"a network change must forget the ambiguity")
}

// TestPlainConnectPostSendPendingIsNotRetried is the same boundary on the ordinary CONNECT path.
func TestPlainConnectPostSendPendingIsNotRetried(t *testing.T) {
	h3 := &boundaryHTTP3Client{
		dialErr: newHTTP3ResponsePendingError(context.DeadlineExceeded),
	}
	tcp := &recordingDialer{err: E.New("tcp fallback reached")}
	client := newStageBoundaryClient(h3, tcp)

	_, err := client.DialContext(context.Background(), "tcp", M.ParseSocksaddr("example.org:443"))
	require.Error(t, err)
	require.ErrorIs(t, err, errHTTP3ResponsePending)
	require.Zero(t, tcp.calls, "the CONNECT may have reached the proxy; it must not be repeated")
	require.Zero(t, client.http3Broken.Load())
}

// TestPlainConnectPreSendTimeoutFallsBack is its availability half.
func TestPlainConnectPreSendTimeoutFallsBack(t *testing.T) {
	h3 := &boundaryHTTP3Client{
		dialErr: E.Cause(errHTTP3PreSend, context.DeadlineExceeded),
	}
	tcp := &recordingDialer{err: E.New("tcp fallback reached")}
	client := newStageBoundaryClient(h3, tcp)

	_, err := client.DialContext(context.Background(), "tcp", M.ParseSocksaddr("example.org:443"))
	require.ErrorContains(t, err, "tcp fallback reached")
	require.Zero(t, client.http3Broken.Load())
}

// TestOldNetworkPendingCannotArmTheAmbiguityWindow pins the epoch and ordering rules of the window.
func TestOldNetworkPendingCannotArmTheAmbiguityWindow(t *testing.T) {
	client := &Client{}
	oldEpoch := client.networkEpoch.Load()
	client.ResetConnections()
	// A pending result that STARTED on the network being left cannot reopen the window.
	client.noteHTTP3TunnelAmbiguous(oldEpoch, 7)
	require.Zero(t, client.http3TunnelAmbiguousUntil.Load(),
		"an old-network result must not arm the new network's window")

	epoch := client.networkEpoch.Load()
	client.noteHTTP3TunnelAmbiguous(epoch, 10)
	require.True(t, client.http3TunnelAmbiguous(time.Now()), "a current pending result arms the window")
	// A newer success disproves it, and an OLDER pending reported afterwards cannot re-arm it.
	client.clearHTTP3TunnelAmbiguous(epoch, 12)
	client.noteHTTP3TunnelAmbiguous(epoch, 11)
	require.False(t, client.http3TunnelAmbiguous(time.Now()),
		"a pending result older than the success must not re-open the window")
	// A newer pending after the success is new evidence and arms it again.
	client.noteHTTP3TunnelAmbiguous(epoch, 13)
	require.True(t, client.http3TunnelAmbiguous(time.Now()))
}

// TestAmbiguityWindowExpiryRestoresH3 is the recovery half: the window is a delay, not a verdict.
func TestAmbiguityWindowExpiryRestoresH3(t *testing.T) {
	client := &Client{}
	client.noteHTTP3TunnelAmbiguous(client.networkEpoch.Load(), 1)
	require.True(t, client.http3TunnelAmbiguous(time.Now()))
	client.http3TunnelAmbiguousUntil.Store(time.Now().Add(-time.Second).UnixNano())
	require.False(t, client.http3TunnelAmbiguous(time.Now()),
		"an expired window must let the next setup try HTTP/3 again")
}

// TestStrictModeNeverArmsTheAmbiguityWindow: strict mode does not fall back at all, so remembering
// an ambiguity would only change which error the NEXT attempt reports.
func TestStrictModeNeverArmsTheAmbiguityWindow(t *testing.T) {
	h3 := &boundaryHTTP3Client{openTunnelErr: newHTTP3ResponsePendingError(context.DeadlineExceeded)}
	tcp := &recordingDialer{err: E.New("tcp fallback reached")}
	client := newStageBoundaryClient(h3, tcp)
	client.disableVersionFallback = true

	_, err := client.OpenTunnel(context.Background(), "connect-ip", "/")
	require.Error(t, err)
	require.Zero(t, tcp.calls)
	require.Zero(t, client.http3TunnelAmbiguousUntil.Load(),
		"strict mode must not record a fallback trigger")
}

// TestPlainConnectDoesNotArmTheTunnelWindow: the window belongs to CONNECT-IP setup, and the plain
// CONNECT path has its own, stricter rule (no retry at all after the header may have been sent).
func TestPlainConnectDoesNotArmTheTunnelWindow(t *testing.T) {
	h3 := &boundaryHTTP3Client{dialErr: newHTTP3ResponsePendingError(context.DeadlineExceeded)}
	tcp := &recordingDialer{err: E.New("tcp fallback reached")}
	client := newStageBoundaryClient(h3, tcp)

	_, err := client.DialContext(context.Background(), "tcp", M.ParseSocksaddr("example.org:443"))
	require.Error(t, err)
	require.Zero(t, client.http3TunnelAmbiguousUntil.Load(),
		"the ordinary CONNECT path must not touch the tunnel window")
}
