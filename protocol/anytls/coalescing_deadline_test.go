package anytls

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	singanytls "github.com/sagernet/sing-anytls"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/trafficclass"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

func pipeDialOut(ctx context.Context) (net.Conn, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	clientConn, serverConn := net.Pipe()
	go func() {
		_, _ = io.Copy(io.Discard, serverConn)
		_ = serverConn.Close()
	}()
	return clientConn, nil
}

func coalescingTestDestination() M.Socksaddr {
	return M.Socksaddr{Fqdn: "example.com", Port: 443}
}

// The shared creation must keep the FIRST caller's deadline. Coalescing detaches the creation from
// that caller's cancellation (another waiter may still need it), and detaching with
// context.WithoutCancel drops the deadline with it - which the TLS burst test already caught once.
// This is the direct regression: the creator's 300 ms budget must bound the dial, and a waiter must
// observe the same failure instead of hanging on a silent peer.
func TestSharedCreationKeepsTheCreatorsDeadline(t *testing.T) {
	var dials atomic.Int32
	observedDeadline := make(chan time.Duration, 1)
	started := make(chan struct{}, 1)
	client, err := singanytls.NewClient(singanytls.ClientOptions{
		Password: "test-password",
		DialOut: func(ctx context.Context) (net.Conn, error) {
			dials.Add(1)
			select {
			case started <- struct{}{}:
			default:
			}
			if deadline, ok := ctx.Deadline(); ok {
				observedDeadline <- time.Until(deadline)
			} else {
				observedDeadline <- 0
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	const creatorBudget = 300 * time.Millisecond
	creatorCtx, cancelCreator := context.WithTimeout(context.Background(), creatorBudget)
	defer cancelCreator()
	creatorErr := make(chan error, 1)
	go func() {
		_, dialErr := client.DialContext(creatorCtx, coalescingTestDestination())
		creatorErr <- dialErr
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the creator never entered DialOut")
	}

	waiterErr := make(chan error, 1)
	go func() {
		_, dialErr := client.DialContext(context.Background(), coalescingTestDestination())
		waiterErr <- dialErr
	}()
	require.Eventually(t, func() bool {
		return client.PendingCreationWaiters() == 1
	}, 2*time.Second, time.Millisecond, "the waiter did not attach to the shared creation")

	require.ErrorIs(t, <-creatorErr, context.DeadlineExceeded)
	require.ErrorIs(t, <-waiterErr, context.DeadlineExceeded,
		"the waiter must inherit the creation's deadline failure instead of hanging")
	require.EqualValues(t, 1, dials.Load(), "one creation must perform one dial")

	remaining := <-observedDeadline
	require.Greater(t, remaining, 100*time.Millisecond,
		"the dial did not receive the creator's deadline (remaining %s)", remaining)
	require.LessOrEqual(t, remaining, creatorBudget)
}

// The creation is admitted with the values of the caller that STARTED it, even when the waiters
// carry a different traffic class. A normal flow must not downgrade an interactive creator's
// admission, and an interactive waiter must not upgrade a normal creator's: the class is a property
// of the work being created, not of whoever happens to ask next.
func TestMixedPriorityWaitersShareTheCreatorsAdmission(t *testing.T) {
	guard := resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), func() resourceguard.Level {
		return resourceguard.LevelNormal
	})
	release := make(chan struct{})
	var (
		admitCalls  atomic.Int32
		seenClass   atomic.Int32
		seenClassOK atomic.Bool
	)
	client, err := singanytls.NewClient(singanytls.ClientOptions{
		Password: "test-password",
		DialOut: func(ctx context.Context) (net.Conn, error) {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return pipeDialOut(ctx)
		},
		AdmitCreation: func(ctx context.Context, create func(context.Context) error) error {
			admitCalls.Add(1)
			if class, loaded := trafficclass.ClassFromContext(ctx); loaded {
				seenClass.Store(int32(class))
				seenClassOK.Store(true)
			}
			return guard.AdmitCreation(ctx, "mixed-priority", create)
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	creatorCtx := trafficclass.WithClass(context.Background(), trafficclass.ClassInteractive)
	creatorErr := make(chan error, 1)
	creatorConn := make(chan net.Conn, 1)
	go func() {
		conn, dialErr := client.DialContext(creatorCtx, coalescingTestDestination())
		creatorErr <- dialErr
		if dialErr == nil {
			creatorConn <- conn
		}
	}()
	require.Eventually(t, func() bool { return admitCalls.Load() == 1 }, 2*time.Second, time.Millisecond,
		"the creator never reached the admission hook")

	waiterCtx := trafficclass.WithClass(context.Background(), trafficclass.ClassDefault)
	waiterErr := make(chan error, 1)
	waiterConn := make(chan net.Conn, 1)
	go func() {
		conn, dialErr := client.DialContext(waiterCtx, coalescingTestDestination())
		waiterErr <- dialErr
		if dialErr == nil {
			waiterConn <- conn
		}
	}()
	require.Eventually(t, func() bool { return client.PendingCreationWaiters() == 1 }, 2*time.Second, time.Millisecond,
		"the mixed-priority waiter did not attach")

	close(release)
	require.NoError(t, <-creatorErr)
	require.NoError(t, <-waiterErr)
	(<-creatorConn).Close()
	(<-waiterConn).Close()

	require.EqualValues(t, 1, admitCalls.Load(), "the waiters were charged as separate creations")
	require.True(t, seenClassOK.Load(), "the admission hook received no traffic class")
	require.EqualValues(t, int32(trafficclass.ClassInteractive), seenClass.Load(),
		"the creation was admitted with a waiter's class instead of the creator's")
	stats := guard.Stats().Gate
	require.EqualValues(t, 1, stats.Admitted)
	require.EqualValues(t, 1, stats.Released)
	require.Zero(t, stats.Inflight)
}
