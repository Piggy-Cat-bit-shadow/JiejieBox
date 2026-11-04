package anytls

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/trafficclass"
	M "github.com/sagernet/sing/common/metadata"
)

// The gate's interactive reserve is only real if the class reaches it. A flow that a person is
// waiting on must get the last slots that background traffic cannot hold, and everything else
// must stay in the normal pool.
func TestAnInteractiveFlowMayUseTheReserve(t *testing.T) {
	t.Parallel()
	guard := resourceguard.NewGuard(resourceguard.Config{
		Limit:              1,
		Queue:              0,
		Wait:               50 * time.Millisecond,
		InteractiveReserve: 1,
		DemoteAfter:        time.Second,
		StarvationAfter:    time.Second,
	}, func() resourceguard.Level { return resourceguard.LevelNormal })
	ctx := resourceguard.NewContext(context.Background(), guard)

	hold := make(chan struct{})
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := dialWithAdmission(ctx, guard, func(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
			close(started)
			<-hold
			return nil, nil
		}, M.Socksaddr{})
		done <- err
	}()
	<-started // the single normal slot is now held

	// A background flow has nowhere to go: no queue, no reserve.
	if _, err := dialWithAdmission(ctx, guard, func(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
		t.Fatal("a background dial was admitted into a full normal pool")
		return nil, nil
	}, M.Socksaddr{}); err == nil {
		t.Fatal("background traffic was admitted beyond the limit")
	}

	// An interactive flow may use the reserve.
	interactive := trafficclass.WithClass(ctx, trafficclass.ClassInteractive)
	admitted := make(chan struct{})
	var dialed bool
	var dialAccess sync.Mutex
	if _, err := dialWithAdmission(interactive, guard, func(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
		dialAccess.Lock()
		dialed = true
		dialAccess.Unlock()
		close(admitted)
		return nil, nil
	}, M.Socksaddr{}); err != nil {
		t.Fatalf("an interactive flow was refused the reserve: %v", err)
	}
	<-admitted
	dialAccess.Lock()
	ran := dialed
	dialAccess.Unlock()
	if !ran {
		t.Fatal("the interactive dial did not run")
	}
	close(hold)
	if err := <-done; err != nil {
		t.Fatalf("the holding dial failed: %v", err)
	}
}

func TestABackgroundFlowDoesNotUseTheReserve(t *testing.T) {
	t.Parallel()
	// Same shape, but the second flow is bulk: it must be refused rather than borrowing the
	// interactive slot.
	guard := resourceguard.NewGuard(resourceguard.Config{
		Limit:              1,
		Queue:              0,
		Wait:               50 * time.Millisecond,
		InteractiveReserve: 4,
		DemoteAfter:        time.Second,
		StarvationAfter:    time.Second,
	}, func() resourceguard.Level { return resourceguard.LevelNormal })
	ctx := resourceguard.NewContext(context.Background(), guard)
	hold := make(chan struct{})
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := dialWithAdmission(ctx, guard, func(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
			close(started)
			<-hold
			return nil, nil
		}, M.Socksaddr{})
		done <- err
	}()
	<-started
	bulk := trafficclass.WithClass(ctx, trafficclass.ClassBulk)
	if _, err := dialWithAdmission(bulk, guard, func(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
		t.Fatal("a bulk dial used the interactive reserve")
		return nil, nil
	}, M.Socksaddr{}); err == nil {
		t.Fatal("bulk traffic was admitted beyond the limit")
	}
	close(hold)
	<-done
}
