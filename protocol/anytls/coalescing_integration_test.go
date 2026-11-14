package anytls

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	singanytls "github.com/sagernet/sing-anytls"
	"github.com/sagernet/sing-box/common/resourceguard"

	"github.com/stretchr/testify/require"
)

// A burst of first streams must open ONE physical AnyTLS session.
//
// # What this measures
//
// The SDK shares one in-flight session creation between concurrent first flows (the pool's
// coalescing change). This test proves it on the production admission hook and a real local server:
// 200 concurrent streams attach to one creation, one TCP accept happens, and the gate is charged
// once.
//
// The wait is made deterministic by the client's PendingCreationWaiters gauge rather than by a
// sleep: the creation is parked in DialOut until every flow has attached, so a goroutine that is
// merely slow cannot be mistaken for a second creation (which is what happened under -race before
// the gauge was used).
func TestConcurrentFirstStreamsShareOnePhysicalSession(t *testing.T) {
	address, accepted := startPlainAnyTLSServer(t)
	guard := resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), nil)
	release := make(chan struct{})
	client, err := singanytls.NewClient(singanytls.ClientOptions{
		Password: carrierPassword,
		DialOut: func(ctx context.Context) (net.Conn, error) {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return (&net.Dialer{}).DialContext(ctx, "tcp", address)
		},
		AdmitCreation: admitSessionCreation(guard, address),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	const flows = 200
	start := make(chan struct{})
	errs := make(chan error, flows)
	conns := make(chan net.Conn, flows)
	var wg sync.WaitGroup
	for index := 0; index < flows; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			dialCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			conn, dialErr := client.DialContext(dialCtx, admissionDestination())
			if dialErr != nil {
				errs <- dialErr
				return
			}
			conns <- conn
		}()
	}
	close(start)
	require.Eventually(t, func() bool {
		return client.PendingCreationWaiters() == flows-1
	}, 5*time.Second, time.Millisecond, "the burst did not attach to one creation")
	close(release)
	wg.Wait()
	close(errs)
	close(conns)

	for streamErr := range errs {
		require.NoError(t, streamErr)
	}
	opened := 0
	for conn := range conns {
		opened++
		_ = conn.Close()
	}
	require.Equal(t, flows, opened)
	require.Eventually(t, func() bool { return accepted.Load() == 1 }, 2*time.Second, time.Millisecond,
		"the burst opened %d physical sessions instead of sharing one", accepted.Load())

	stats := guard.Stats().Gate
	require.EqualValues(t, 1, stats.Admitted, "the gate charged more than one creation")
	require.EqualValues(t, 1, stats.Released)
	require.Zero(t, stats.Inflight)
}
