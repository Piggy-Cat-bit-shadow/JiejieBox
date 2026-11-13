//go:build with_quic

package v2rayquic

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestResetDoesNotRetireAGenerationCreatedWhileWaiting pins the ResetContext window: the pending
// slot is released before migrateAccess is taken, so a demand can build and publish a new-generation
// connection while the reset waits behind a previous migration. That connection was created on the
// current network; the old reset must not retire it.
func TestResetDoesNotRetireAGenerationCreatedWhileWaiting(t *testing.T) {
	var closes atomic.Int32
	client := &Client{
		ctx: context.Background(),
		dialConn: func(context.Context) (*clientConnection, error) {
			return &clientConnection{
				ctx:     context.Background(),
				closeFn: func() { closes.Add(1) },
			}, nil
		},
	}

	// Hold the migration slot so the reset cannot proceed past the pending-clear step. The pending
	// offer signals its cancel, which is the deterministic point after the slot was released.
	cancelled := make(chan struct{})
	client.pending = &clientOffer{
		done:   make(chan struct{}),
		cancel: func(error) { close(cancelled) },
	}
	client.migrateAccess.Lock()

	resetDone := make(chan struct{})
	go func() {
		_ = client.ResetContext(context.Background())
		close(resetDone)
	}()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		client.migrateAccess.Unlock()
		t.Fatal("ResetContext did not reach its pending-clear step")
	}

	// The reset is now blocked on migrateAccess. This is the new-generation demand it must not touch.
	conn, err := client.offer(context.Background())
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Same(t, conn, client.conn.Load())

	client.migrateAccess.Unlock()
	select {
	case <-resetDone:
	case <-time.After(5 * time.Second):
		t.Fatal("ResetContext did not finish")
	}

	require.Zero(t, closes.Load(), "the old reset retired the generation created while it was waiting")
	require.Same(t, conn, client.conn.Load(), "the new generation was removed from the pool")
	conn.closeConn()
}
