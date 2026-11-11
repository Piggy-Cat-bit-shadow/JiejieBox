package tls

// Stage-2 coupling test: the fast-fail policy must measure CREATION time, not the
// time a dial spent queued at the admission gate.
//
// The rate limiter exists to stop a storm of creations against a far side that is
// refusing instantly. A dial that waited 300 ms for a permit and then failed in a
// microsecond is exactly that signal; if the wait is counted as creation time the
// failure looks slow, the streak resets, and the limiter stays disarmed while the
// gate is saturated - the state in which the storm is most likely.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
)

func TestAQueuedFastFailureStillArmsTheRateLimit(t *testing.T) {
	t.Parallel()
	guard := resourceguard.NewGuard(resourceguard.Config{
		Limit: 1,
		Queue: 1,
		Wait:  2 * time.Second,
	}, func() resourceguard.Level { return resourceguard.LevelNormal })
	ctx := resourceguard.NewContext(context.Background(), guard)

	const attempts = 4
	for attempt := 0; attempt < attempts; attempt++ {
		blocker, err := guard.Acquire(ctx, resourceguard.ClassNormal)
		if err != nil {
			t.Fatalf("attempt %d: blocker: %v", attempt, err)
		}
		done := make(chan error, 1)
		go func() {
			_, dialErr := handshakeWithAdmission(ctx, func(context.Context) (Conn, error) {
				return nil, errors.New("far side refused immediately")
			})
			done <- dialErr
		}()
		// Wait until the dial is actually queued at the gate, so the wait below is
		// queue time rather than scheduler luck.
		deadline := time.Now().Add(2 * time.Second)
		for guard.Stats().Gate.Waiters == 0 {
			if time.Now().After(deadline) {
				t.Fatal("the dial never queued at the gate")
			}
			time.Sleep(time.Millisecond)
		}
		// Longer than the fast-fail window (250 ms): if the wait is counted as
		// creation time, the failure is classified as slow.
		time.Sleep(300 * time.Millisecond)
		blocker.Release()
		if dialErr := <-done; dialErr == nil {
			t.Fatalf("attempt %d: the dial was expected to fail", attempt)
		}
	}
	stats := guard.Stats()
	if stats.Rate.FastFailures < attempts {
		t.Fatalf("queued fast failures were not counted: FastFailures=%d, want >=%d (a wait at the gate must not make a refusal look slow)",
			stats.Rate.FastFailures, attempts)
	}
	if stats.Rate.Windows == 0 {
		t.Fatalf("the rate limit never armed after %d queued fast failures", attempts)
	}
}
