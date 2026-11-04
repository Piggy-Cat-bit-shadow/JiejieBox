package tls

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
)

func newSeamGuard(t *testing.T, config resourceguard.Config) *resourceguard.Guard {
	t.Helper()
	return resourceguard.NewGuard(config, func() resourceguard.Level {
		return resourceguard.LevelNormal
	})
}

func seamConfig(limit, queue int, wait time.Duration) resourceguard.Config {
	return resourceguard.Config{
		Limit:           limit,
		Queue:           queue,
		Wait:            wait,
		DemoteAfter:     time.Second,
		StarvationAfter: time.Second,
	}
}

// A caller that gives up while queued must never run the handshake afterwards, and must leave
// nothing behind in the queue: an abandoned waiter that is later granted a slot is how a gate
// slowly fills with permits nobody will release, and how a dial happens after its caller has
// already reported failure to the user.
func TestHandshakeWithAdmissionCancelWhileQueuedDoesNotDial(t *testing.T) {
	t.Parallel()
	guard := newSeamGuard(t, seamConfig(1, 2, 5*time.Second))
	ctx := resourceguard.NewContext(context.Background(), guard)

	hold := make(chan struct{})
	started := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		_, err := handshakeWithAdmission(ctx, func(context.Context) (Conn, error) {
			close(started)
			<-hold
			return nil, nil
		})
		holderDone <- err
	}()
	<-started

	waiterCtx, cancel := context.WithCancel(ctx)
	waiterDone := make(chan error, 1)
	var dialed atomic.Bool
	go func() {
		_, err := handshakeWithAdmission(waiterCtx, func(context.Context) (Conn, error) {
			dialed.Store(true)
			return nil, nil
		})
		waiterDone <- err
	}()

	// Let the waiter park on the gate, then abandon it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if stats := guard.Stats(); stats.Gate.Waiters == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the waiter never queued: %+v", guard.Stats().Gate)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()

	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled waiter returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled waiter kept waiting")
	}
	if dialed.Load() {
		t.Fatal("a cancelled waiter ran the handshake")
	}
	if stats := guard.Stats(); stats.Gate.Waiters != 0 {
		t.Fatalf("the cancelled waiter is still queued: %+v", stats.Gate)
	}

	close(hold)
	if err := <-holderDone; err != nil {
		t.Fatalf("the holder failed: %v", err)
	}
	// Every permit is back, and the cancelled request counts as cancelled rather than admitted.
	stats := guard.Stats()
	if stats.Gate.Inflight != 0 {
		t.Fatalf("permits leaked: %+v", stats.Gate)
	}
	if stats.Gate.Canceled == 0 {
		t.Fatalf("the cancellation was not accounted: %+v", stats.Gate)
	}
}

// The chained-dial fix must hold under contention, not just in the two-dial case: many chains
// racing one slot must each take exactly one permit, all finish, and leave the gate empty.
func TestChainedDialsUnderPressureStayBounded(t *testing.T) {
	t.Parallel()
	const chains = 16
	guard := newSeamGuard(t, seamConfig(1, chains, 5*time.Second))
	ctx := resourceguard.NewContext(context.Background(), guard)

	var wg sync.WaitGroup
	errs := make(chan error, chains)
	for index := 0; index < chains; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := handshakeWithAdmission(ctx, func(dialCtx context.Context) (Conn, error) {
				// The chain: the outer dial goes through a detour that handshakes too.
				_, innerErr := handshakeWithAdmission(dialCtx, func(context.Context) (Conn, error) {
					time.Sleep(time.Millisecond)
					return nil, nil
				})
				return nil, innerErr
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a chained dial failed under pressure: %v", err)
		}
	}

	stats := guard.Stats()
	if stats.Gate.Requested != chains {
		t.Fatalf("%d chains asked the gate %d times, want %d", chains, stats.Gate.Requested, chains)
	}
	if stats.Gate.Inflight != 0 {
		t.Fatalf("permits leaked after the storm: %+v", stats.Gate)
	}
	if stats.Gate.MaxInflight > 1 {
		t.Fatalf("the limit was exceeded: %+v", stats.Gate)
	}
}
