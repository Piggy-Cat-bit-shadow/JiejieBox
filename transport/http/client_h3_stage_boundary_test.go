//go:build with_quic

package http

import (
	"context"
	"net"
	"testing"

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
