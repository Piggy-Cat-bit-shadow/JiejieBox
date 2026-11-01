package mieru

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

// startIdleClosingTCPProxy forwards connections to backendPort and closes each
// relay once neither direction has moved a byte for idle.
//
// The SDK's own server-side idle timeout has a one-minute minimum, which is too
// slow for a unit test; this proxy reproduces the same event from the client's
// point of view: the TCP underlay carrying an established Mieru session goes
// away while the session is idle.
func startIdleClosingTCPProxy(t *testing.T, backendPort uint16, idle time.Duration) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			clientConn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			backendConn, dialErr := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", backendPort))
			if dialErr != nil {
				_ = clientConn.Close()
				continue
			}
			var lastActivity atomic.Int64
			lastActivity.Store(time.Now().UnixNano())
			client := &activityConn{Conn: clientConn, last: &lastActivity}
			backend := &activityConn{Conn: backendConn, last: &lastActivity}
			done := make(chan struct{})
			var closeOnce sync.Once
			closeBoth := func() {
				closeOnce.Do(func() {
					_ = clientConn.Close()
					_ = backendConn.Close()
					close(done)
				})
			}
			go func() {
				_, _ = io.Copy(backend, client)
				closeBoth()
			}()
			go func() {
				_, _ = io.Copy(client, backend)
				closeBoth()
			}()
			go func() {
				ticker := time.NewTicker(idle / 4)
				defer ticker.Stop()
				for {
					select {
					case <-done:
						return
					case <-ticker.C:
						if time.Since(time.Unix(0, lastActivity.Load())) >= idle {
							closeBoth()
							return
						}
					}
				}
			}()
		}
	}()
	return uint16(listener.Addr().(*net.TCPAddr).Port)
}

type activityConn struct {
	net.Conn
	last *atomic.Int64
}

func (c *activityConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.last.Store(time.Now().UnixNano())
	}
	return n, err
}

func (c *activityConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.last.Store(time.Now().UnixNano())
	}
	return n, err
}

// TestMieruClientObservesServerClosingAnIdleUnderlay covers the event the
// v3.39.0 server idle timeout introduces: the TCP underlay is closed while the
// session carries no traffic. The client must surface an error to the blocked
// read promptly (not wait for the caller's deadline, which is what a stale
// deadline or a lost close would look like), and a new dial must still work.
func TestMieruClientObservesServerClosingAnIdleUnderlay(t *testing.T) {
	serverPort := freePortForBothTransports(t)
	startTestInbound(t, inboundOptionsForPort(serverPort, "tcp"))
	const idle = 900 * time.Millisecond
	proxyPort := startIdleClosingTCPProxy(t, serverPort, idle)
	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(proxyPort, "tcp", "alice", "alice-password"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("192.0.2.61:443")
	conn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.Write([]byte("warm"))
	require.NoError(t, err)
	require.Equal(t, []byte("warm"), readExactWithTimeout(t, conn, len("warm")))

	// The proxy closes the underlay after ~idle of silence; the blocked read
	// must return an error well before its own 8 s deadline.
	start := time.Now()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(8*time.Second)))
	_, err = conn.Read(make([]byte, 1))
	require.Error(t, err, "the client must observe the underlay closure")
	var netErr net.Error
	require.False(t, errors.As(err, &netErr) && netErr.Timeout(),
		"the read only saw its own deadline; the underlay closure was lost: %v", err)
	require.Less(t, time.Since(start), 6*time.Second)

	// The proxy keeps listening; a new session must build a new underlay.
	next, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	defer next.Close()
	_, err = next.Write([]byte("after"))
	require.NoError(t, err)
	require.Equal(t, []byte("after"), readExactWithTimeout(t, next, len("after")))
}
