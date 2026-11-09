package trusttunnel

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// TT07: the client owns one session and every CONNECT stream under it. Two
// attacks pin that ownership:
//
//   - Close while streams are active must release their readers; a stream that
//     keeps a dead session alive is a leak the caller cannot see.
//   - Close racing a burst of DialContext calls must let every caller return,
//     and must not poison the service for the next client.
//
// Both run for HTTP/2 and HTTP/3 because the two session types have different
// stream teardown paths (x/net/http2 body close vs QUIC stream reset).

func holdDestination() M.Socksaddr {
	return M.ParseSocksaddrHostPort(testHoldHost, 1)
}

// clientProtocols is the protocol matrix this test can actually run: HTTP/3 needs the
// with_quic build tag, and the Linux trusttunnel CI job builds only with_trusttunnel.
func clientProtocols() []string {
	protocols := []string{ProtocolHTTP2}
	if quicIncluded {
		protocols = append(protocols, ProtocolHTTP3)
	}
	return protocols
}

func TestClientCloseReleasesActiveStreams(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			server := startTestServer(t, protocol)
			client := server.newClient(t, protocol, testPassword, &testDialer{}, nil)
			ctx := testContext(t)
			destination := holdDestination()
			const streams = 8

			conns := make([]net.Conn, 0, streams)
			for index := 0; index < streams; index++ {
				conn, err := client.DialContext(ctx, destination)
				require.NoError(t, err)
				_, err = conn.Write([]byte{byte(index)})
				require.NoError(t, err)
				conns = append(conns, conn)
			}
			require.Eventually(t, func() bool {
				return server.handler.holding.Load() == int32(streams)
			}, 5*time.Second, 10*time.Millisecond,
				"the server did not observe all %d tunneled streams", streams)

			// Every stream is blocked in Read when the owner closes.
			readErrors := make(chan error, streams)
			for _, conn := range conns {
				go func(conn net.Conn) {
					_, err := conn.Read(make([]byte, 1))
					readErrors <- err
				}(conn)
			}
			time.Sleep(50 * time.Millisecond)

			require.NoError(t, client.Close())

			deadline := time.After(10 * time.Second)
			for index := 0; index < streams; index++ {
				select {
				case err := <-readErrors:
					require.Error(t, err, "a blocked read returned success after the owning client closed")
				case <-deadline:
					t.Fatalf("stream %d stayed blocked after client.Close()", index)
				}
			}

			require.NoError(t, client.Close(), "Close must stay idempotent")
			for _, conn := range conns {
				_ = conn.Close()
			}

			// The service is a separate owner: a fresh client must still work.
			fresh := server.newClient(t, protocol, testPassword, &testDialer{}, nil)
			echo := startEchoServer(t)
			conn, err := fresh.DialContext(ctx, echo)
			require.NoError(t, err)
			echoRoundTrip(t, conn, 256)
			_ = conn.Close()
		})
	}
}

func TestClientCloseRacesWithConcurrentDials(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			server := startTestServer(t, protocol)
			client := server.newClient(t, protocol, testPassword, &testDialer{}, nil)
			ctx := testContext(t)
			destination := holdDestination()
			const callers = 16

			var returned atomic.Int32
			var waitGroup sync.WaitGroup
			waitGroup.Add(callers)
			for index := 0; index < callers; index++ {
				go func() {
					defer waitGroup.Done()
					conn, err := client.DialContext(ctx, destination)
					if err == nil {
						_ = conn.Close()
					}
					returned.Add(1)
				}()
			}
			// Let part of the burst reach the session before the close lands.
			time.Sleep(5 * time.Millisecond)
			require.NoError(t, client.Close())

			finished := make(chan struct{})
			go func() {
				waitGroup.Wait()
				close(finished)
			}()
			select {
			case <-finished:
			case <-time.After(15 * time.Second):
				t.Fatalf("only %d of %d dialers returned after client.Close()", returned.Load(), callers)
			}
			require.EqualValues(t, callers, returned.Load())

			// A dial after Close must fail closed, not hang or panic.
			_, err := client.DialContext(ctx, destination)
			require.Error(t, err)

			fresh := server.newClient(t, protocol, testPassword, &testDialer{}, nil)
			echo := startEchoServer(t)
			conn, err := fresh.DialContext(ctx, echo)
			require.NoError(t, err)
			echoRoundTrip(t, conn, 256)
			_ = conn.Close()
		})
	}
}
