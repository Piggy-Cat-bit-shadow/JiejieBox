package anytls

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	M "github.com/sagernet/sing/common/metadata"
)

// The independent audit's finding, pinned: a chain whose INNER hop is AnyTLS re-entered this
// seam with the outer permit already held and asked the gate again. With the normal capacity
// taken by the outer dial, that is a queue, a refusal, or a wait-budget expiry with nothing
// wrong on the network - and it happens even though the TLS seam was fixed, because a chain can
// cross AnyTLS rather than TLS.
//
// The rule is the same on every seam: one logical dial holds one permit.
func TestANestedAnyTLSDialReusesTheHeldPermit(t *testing.T) {
	t.Parallel()
	guard := resourceguard.NewGuard(resourceguard.Config{
		Limit:           1,
		Queue:           0,
		Wait:            50 * time.Millisecond,
		DemoteAfter:     time.Second,
		StarvationAfter: time.Second,
	}, func() resourceguard.Level { return resourceguard.LevelNormal })
	ctx := resourceguard.NewContext(context.Background(), guard)

	// The outer hop (a TLS outbound, or another AnyTLS) holds the only slot and then dials
	// through this outbound, exactly as a detour or a chain does.
	hold := make(chan struct{})
	started := make(chan struct{})
	outerDone := make(chan error, 1)
	go func() {
		_, err := handshakeLikeOuter(ctx, guard, func(outerCtx context.Context) error {
			close(started)
			<-hold
			// The inner AnyTLS hop runs under the outer dial's context.
			_, innerErr := dialWithAdmission(outerCtx, guard, func(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
				return nil, nil
			}, M.Socksaddr{})
			return innerErr
		})
		outerDone <- err
	}()
	<-started

	// Give the inner hop a chance to run and fail while the outer slot is still held.
	close(hold)
	select {
	case err := <-outerDone:
		if err != nil {
			t.Fatalf("a nested AnyTLS dial was refused by its own outer permit: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the nested dial never returned")
	}
	if stats := guard.Stats(); stats.Gate.Inflight != 0 {
		t.Fatalf("permits leaked: %+v", stats.Gate)
	}
	if stats := guard.Stats(); stats.Gate.Requested != 1 {
		t.Fatalf("the chain asked the gate %d times, want 1: %+v", stats.Gate.Requested, stats.Gate)
	}
}

// handshakeLikeOuter stands in for the outer seam: it admits once, marks the context, and runs
// the outer dial with that context - the shape of common/tls' handshakeWithAdmission.
func handshakeLikeOuter(ctx context.Context, guard *resourceguard.Guard, dial func(context.Context) error) (net.Conn, error) {
	permit, err := guard.Acquire(ctx, resourceguard.ClassNormal)
	if err != nil {
		return nil, err
	}
	defer permit.Release()
	err = dial(resourceguard.WithHeldPermit(ctx))
	return nil, err
}
