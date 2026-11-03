package anytls

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

// TestDialFailuresNeverLeakPermits covers the failure shapes a real dial can take, because a
// permit that is only returned on the happy path is a gate that closes itself one failure at a
// time.
//
// Every case uses a real socket: a refused port, a server that accepts and immediately hangs up
// mid-handshake, and a handshake that outlives its deadline. The assertion is the same for all
// three - after the failures have settled, nothing is in flight and the accounting balances.
func TestDialFailuresNeverLeakPermits(t *testing.T) {
	config := resourceguard.DefaultNetworkExtensionConfig()
	config.Limit = 4
	config.Queue = 64
	config.Wait = time.Second

	// A port that nothing listens on: the dial itself is refused.
	deadListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	refusedAddress := deadListener.Addr().String()
	require.NoError(t, deadListener.Close())

	// A server that accepts and immediately closes: the TLS handshake fails part way.
	hangingUp, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { hangingUp.Close() })
	go func() {
		for {
			conn, acceptErr := hangingUp.Accept()
			if acceptErr != nil {
				return
			}
			conn.Close()
		}
	}()

	// A server that says nothing: the handshake only ends when the caller's context does.
	silent := newSilentServer(t)

	cases := []struct {
		name    string
		address string
		timeout time.Duration
	}{
		{name: "connection refused", address: refusedAddress, timeout: 2 * time.Second},
		{name: "server hangs up during handshake", address: hangingUp.Addr().String(), timeout: 2 * time.Second},
		{name: "handshake outlives deadline", address: silent.listener.Addr().String(), timeout: 40 * time.Millisecond},
	}

	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			guard := resourceguard.NewGuard(config, func() resourceguard.Level { return resourceguard.LevelNormal })
			dial := func(ctx context.Context, _ M.Socksaddr) (net.Conn, error) {
				raw, dialErr := (&net.Dialer{}).DialContext(ctx, "tcp", testCase.address)
				if dialErr != nil {
					return nil, dialErr
				}
				secured := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, ServerName: "anytls.test"})
				if handshakeErr := secured.HandshakeContext(ctx); handshakeErr != nil {
					raw.Close()
					return nil, handshakeErr
				}
				return secured, nil
			}

			const attempts = 12
			var wg sync.WaitGroup
			var failures atomic.Int64
			for index := 0; index < attempts; index++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					ctx, cancel := context.WithTimeout(context.Background(), testCase.timeout)
					defer cancel()
					conn, dialErr := dialWithAdmission(ctx, guard, dial, M.Socksaddr{})
					if dialErr != nil {
						failures.Add(1)
						return
					}
					conn.Close()
				}()
			}
			wg.Wait()

			require.Equal(t, int64(attempts), failures.Load(), "every dial in this case is expected to fail")
			stats := guard.Stats().Gate
			require.Zero(t, stats.Inflight, "a failed dial must not leak its permit")
			require.Zero(t, stats.Waiters)
			require.Equal(t, int(stats.Admitted-stats.Released), stats.Inflight,
				"admitted=%d released=%d", stats.Admitted, stats.Released)
		})
	}
}
