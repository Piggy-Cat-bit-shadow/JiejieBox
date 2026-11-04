package tls

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/trafficclass"
)

// The gate reserves capacity beyond its limit for interactive traffic. AnyTLS has mapped the
// traffic class since the reserve existed; the shared TLS seam did not, so a Trojan or VLESS flow a
// person was waiting on queued behind background work exactly like a bulk transfer. These tests
// state the property at the seam every TLS outbound shares.
//
// Same shape as the AnyTLS tests, deliberately: two consumers of one policy must be verified the
// same way, or one of them silently drifts.
func interactiveConfig(limit, reserve int) resourceguard.Config {
	return resourceguard.Config{
		Limit:              limit,
		Queue:              0,
		Wait:               50 * time.Millisecond,
		InteractiveReserve: reserve,
		DemoteAfter:        time.Second,
		StarvationAfter:    time.Second,
	}
}

func TestAnInteractiveHandshakeMayUseTheReserve(t *testing.T) {
	t.Parallel()
	guard := resourceguard.NewGuard(interactiveConfig(1, 1), func() resourceguard.Level {
		return resourceguard.LevelNormal
	})
	ctx := resourceguard.NewContext(context.Background(), guard)

	hold := make(chan struct{})
	started := make(chan struct{})
	holder := make(chan error, 1)
	go func() {
		_, err := handshakeWithAdmission(ctx, func(context.Context) (Conn, error) {
			close(started)
			<-hold
			return nil, nil
		})
		holder <- err
	}()
	<-started // the single normal slot is held

	// Background traffic has nowhere to go: the queue is empty and the reserve is not for it.
	_, backgroundErr := handshakeWithAdmission(ctx, func(context.Context) (Conn, error) {
		t.Fatal("a background handshake was admitted into a full normal pool")
		return nil, nil
	})
	if backgroundErr == nil {
		t.Fatal("background traffic was admitted beyond the limit")
	}

	// An interactive flow may use the reserve.
	interactiveCtx := trafficclass.WithClass(ctx, trafficclass.ClassInteractive)
	var ran bool
	if _, err := handshakeWithAdmission(interactiveCtx, func(context.Context) (Conn, error) {
		ran = true
		return nil, nil
	}); err != nil {
		t.Fatalf("an interactive handshake was refused the reserve: %v", err)
	}
	if !ran {
		t.Fatal("the interactive handshake did not run")
	}

	close(hold)
	if err := <-holder; err != nil {
		t.Fatalf("the holding handshake failed: %v", err)
	}
	if stats := guard.Stats(); stats.Gate.Inflight != 0 {
		t.Fatalf("permits leaked: %+v", stats.Gate)
	}
}

func TestABackgroundHandshakeDoesNotUseTheReserve(t *testing.T) {
	t.Parallel()
	guard := resourceguard.NewGuard(interactiveConfig(1, 4), func() resourceguard.Level {
		return resourceguard.LevelNormal
	})
	ctx := resourceguard.NewContext(context.Background(), guard)

	hold := make(chan struct{})
	started := make(chan struct{})
	holder := make(chan error, 1)
	go func() {
		_, err := handshakeWithAdmission(ctx, func(context.Context) (Conn, error) {
			close(started)
			<-hold
			return nil, nil
		})
		holder <- err
	}()
	<-started

	for _, class := range []trafficclass.Class{trafficclass.ClassBulk, trafficclass.ClassDefault} {
		classCtx := trafficclass.WithClass(ctx, class)
		_, err := handshakeWithAdmission(classCtx, func(context.Context) (Conn, error) {
			t.Fatalf("%v traffic used the interactive reserve", class)
			return nil, nil
		})
		if err == nil {
			t.Fatalf("%v traffic was admitted beyond the limit", class)
		}
	}

	close(hold)
	<-holder
}

// The two seams must answer the same way for the same class. AnyTLS and the shared TLS dialer are
// separate consumers of one policy; a divergence is the defect this test exists to catch.
func TestBothSeamsAgreeOnWhoMayUseTheReserve(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		class     trafficclass.Class
		wantError bool
	}{
		{"interactive", trafficclass.ClassInteractive, false},
		{"realtime", trafficclass.ClassRealtime, false},
		{"bulk", trafficclass.ClassBulk, true},
		{"default", trafficclass.ClassDefault, true},
	} {
		guard := resourceguard.NewGuard(interactiveConfig(1, 1), func() resourceguard.Level {
			return resourceguard.LevelNormal
		})
		base := resourceguard.NewContext(context.Background(), guard)
		hold := make(chan struct{})
		started := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			_, err := handshakeWithAdmission(base, func(context.Context) (Conn, error) {
				close(started)
				<-hold
				return nil, nil
			})
			done <- err
		}()
		<-started

		classCtx := trafficclass.WithClass(base, tc.class)
		var dialed bool
		_, err := handshakeWithAdmission(classCtx, func(context.Context) (Conn, error) {
			dialed = true
			return nil, nil
		})
		if tc.wantError && err == nil {
			t.Fatalf("%s: admitted beyond the limit", tc.name)
		}
		if !tc.wantError && err != nil {
			t.Fatalf("%s: refused the reserve: %v", tc.name, err)
		}
		if tc.wantError && dialed {
			t.Fatalf("%s: the refused handshake ran anyway", tc.name)
		}
		close(hold)
		<-done
	}
}
