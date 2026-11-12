package tls

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
)

// TestHandshakeRateWindowIsScopedToTheDestination proves the shared TLS seam reports the server it
// is dialling as the fast-fail bucket key: a storm against one server refuses a handshake to that
// server inside a short budget, while a handshake to another server with the same budget proceeds.
// The refusal is deterministic (the armed window outlasts the budget), so this does not measure
// scheduler timing.
func TestHandshakeRateWindowIsScopedToTheDestination(t *testing.T) {
	t.Parallel()
	guard := resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), func() resourceguard.Level {
		return resourceguard.LevelNormal
	})
	ctx := resourceguard.NewContext(context.Background(), guard)
	const bad = "bad.example:443"
	const good = "good.example:443"
	for index := 0; index < 8; index++ {
		_, _ = handshakeWithAdmissionFor(ctx, bad, func(context.Context) (Conn, error) {
			return nil, errors.New("handshake refused")
		})
	}
	if stats := guard.Stats().Rate; stats.ArmedBuckets != 1 {
		t.Fatalf("the failures armed %d buckets, want exactly the bad destination: %+v", stats.ArmedBuckets, stats)
	}

	shortCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()

	dialedBad := false
	_, err := handshakeWithAdmissionFor(shortCtx, bad, func(context.Context) (Conn, error) {
		dialedBad = true
		return nil, errors.New("must not reach the dial")
	})
	if !errors.Is(err, resourceguard.ErrRateLimited) || dialedBad {
		t.Fatalf("dial to the armed destination: dialed=%v err=%v, want a local rate refusal", dialedBad, err)
	}

	dialedGood := false
	_, err = handshakeWithAdmissionFor(shortCtx, good, func(context.Context) (Conn, error) {
		dialedGood = true
		return nil, errors.New("handshake refused")
	})
	if !dialedGood || errors.Is(err, resourceguard.ErrRateLimited) {
		t.Fatalf("dial to the healthy destination: dialed=%v err=%v, want it to run", dialedGood, err)
	}
}
