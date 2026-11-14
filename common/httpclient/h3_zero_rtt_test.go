//go:build with_quic

package httpclient

import (
	"context"
	stdTLS "crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/sing-box/common/resourceguard"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// udpSilentDialer returns a real UDP socket connected to a listener that never answers. It stands in
// for the network between the client and a peer that is slow, congested or absent.
type udpSilentDialer struct {
	address string
}

func (d *udpSilentDialer) DialContext(ctx context.Context, network string, _ M.Socksaddr) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, d.address)
}

func (d *udpSilentDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// The admission permit covers the COLD-START QUIC handshake, not only the socket allocation.
//
// # What the API actually does on a cold start
//
// The name `DialEarlyConn` invites the assumption that it returns a connection immediately and
// completes the handshake in the background, which would mean the permit is released while the
// handshake still runs. On the pinned quic-go fork, a connection with NO resumable session behaves
// differently: with a UDP listener that never answers and a 2 s handshake timeout, the call does NOT
// return at the timeout's start - it blocks until the handshake fails. The permit is therefore held
// for the whole cold-start handshake and released when the connection object is finally returned.
//
// # What this test does NOT verify: 0-RTT resume
//
// No session ticket is installed, no early data is sent and Allow0RTT is not exercised. A RESUMED
// connection takes a different path in quic-go and may return before the handshake completes; the
// permit lifetime on that path is NOT_VERIFIED. The test name says cold start so this distinction
// cannot be lost again.
func TestH3PermitIsHeldAcrossTheColdStartHandshake(t *testing.T) {
	listener, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	guard := resourceguard.NewGuard(resourceguard.Config{Limit: 1, Queue: 0, Wait: 100 * time.Millisecond}, nil)
	admission := h3AdmissionFromContext(resourceguard.NewContext(context.Background(), guard))
	require.NotNil(t, admission)

	dial := newHTTP3DialFunc(&udpSilentDialer{address: listener.LocalAddr().String()}, nil, 0, admission)
	dialCtx, cancelDial := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelDial()

	type result struct {
		conn *quic.Conn
		err  error
	}
	done := make(chan result, 1)
	started := time.Now()
	go func() {
		conn, dialErr := dial(dialCtx, "127.0.0.1:443", &stdTLS.Config{
			InsecureSkipVerify: true,
			ServerName:         "silent.test",
		}, &quic.Config{HandshakeIdleTimeout: 2 * time.Second})
		done <- result{conn: conn, err: dialErr}
	}()

	// While the handshake is still pending, the permit must be visibly held: that is what bounds
	// concurrent handshakes, not just concurrent socket allocations.
	select {
	case dialResult := <-done:
		t.Fatalf("the dial returned before the silent handshake could be observed: err=%v", dialResult.err)
	case <-time.After(500 * time.Millisecond):
	}
	stats := guard.Stats().Gate
	require.EqualValues(t, 1, stats.Admitted)
	require.EqualValues(t, 0, stats.Released, "the permit was released while the handshake was still running")
	require.Equal(t, 1, stats.Inflight, "the handshake must hold the admission slot")

	select {
	case dialResult := <-done:
		require.Error(t, dialResult.err, "a silent peer must end in a handshake error")
		require.Nil(t, dialResult.conn)
		elapsed := time.Since(started)
		require.GreaterOrEqual(t, elapsed, time.Second,
			"DialEarlyConn returned in %s; it no longer waits for the handshake, so the permit now covers less than this test claims", elapsed)
		stats = guard.Stats().Gate
		require.EqualValues(t, 1, stats.Released, "the failed handshake must return the permit exactly once")
		require.Zero(t, stats.Inflight)
	case <-time.After(4 * time.Second):
		t.Fatal("the silent handshake never ended")
	}
}
