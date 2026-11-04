package tls

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
)

func newAdmissionGuard(t *testing.T, level resourceguard.Level) *resourceguard.Guard {
	t.Helper()
	return resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), func() resourceguard.Level {
		return level
	})
}

func TestHandshakeWithAdmissionAdmitsAnUnmarkedDial(t *testing.T) {
	t.Parallel()
	guard := newAdmissionGuard(t, resourceguard.LevelNormal)
	ctx := resourceguard.NewContext(context.Background(), guard)
	var dialed bool
	conn, err := handshakeWithAdmission(ctx, func() (Conn, error) {
		dialed = true
		return nil, errors.New("handshake refused")
	})
	if conn != nil || err == nil || !dialed {
		t.Fatalf("dialed %v, conn %v, err %v", dialed, conn, err)
	}
	stats := guard.Stats()
	if stats.Gate.Inflight != 0 {
		t.Fatalf("a failed handshake leaked a permit: %+v", stats.Gate)
	}
	if stats.Gate.Requested == 0 || stats.Gate.Admitted == 0 {
		t.Fatalf("the gate was not consulted: %+v", stats.Gate)
	}
}

func TestHandshakeWithAdmissionSkipsAMarkedDial(t *testing.T) {
	t.Parallel()
	// AnyTLS admits at its own seam and marks the context. The TLS layer must not charge the same
	// handshake again, or the effective limit halves and one dial queues twice.
	guard := newAdmissionGuard(t, resourceguard.LevelNormal)
	ctx := resourceguard.WithHeldPermit(resourceguard.NewContext(context.Background(), guard))
	var dialed bool
	_, err := handshakeWithAdmission(ctx, func() (Conn, error) {
		dialed = true
		return nil, errors.New("handshake refused")
	})
	if err == nil || !dialed {
		t.Fatalf("dialed %v, err %v", dialed, err)
	}
	if stats := guard.Stats(); stats.Gate.Requested != 0 {
		t.Fatalf("a marked dial asked the gate for a permit: %+v", stats.Gate)
	}
}

func TestHandshakeWithAdmissionWithoutAGuardIsUnbounded(t *testing.T) {
	t.Parallel()
	// Every platform without a process budget runs this path: it must never refuse.
	for index := 0; index < 64; index++ {
		dialed := false
		_, err := handshakeWithAdmission(context.Background(), func() (Conn, error) {
			dialed = true
			return nil, errors.New("handshake refused")
		})
		if !dialed || err == nil {
			t.Fatalf("iteration %d: dialed %v, err %v", index, dialed, err)
		}
	}
}

func TestHandshakeWithAdmissionRefusesWhenTheGateIsFull(t *testing.T) {
	t.Parallel()
	// One slot, no queue: the second handshake must be refused in constant time rather than
	// parking, which is the behaviour a burst needs at the edge of the budget.
	guard := resourceguard.NewGuard(resourceguard.Config{
		Limit:           1,
		Queue:           0,
		Wait:            50 * time.Millisecond,
		DemoteAfter:     time.Second,
		StarvationAfter: time.Second,
	}, func() resourceguard.Level { return resourceguard.LevelNormal })
	ctx := resourceguard.NewContext(context.Background(), guard)
	started := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := handshakeWithAdmission(ctx, func() (Conn, error) {
			close(started)
			<-release
			return nil, nil
		})
		firstDone <- err
	}()
	<-started
	dialed := false
	_, err := handshakeWithAdmission(ctx, func() (Conn, error) {
		dialed = true
		return nil, nil
	})
	if err == nil {
		t.Fatal("a full gate admitted a second handshake")
	}
	if dialed {
		t.Fatal("the refused handshake ran anyway")
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("the admitted handshake failed: %v", err)
	}
}
