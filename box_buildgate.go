package box

// F03 — the box's build reservation, wired into the one place every provider sees.
//
// common/buildgate owns the reservation; this file owns the box-level decision: ONE gate per
// box, created before any provider is constructed, so every refresh in the box shares its
// slots and no provider can hold more than its share of build memory.
//
// An embedder that already installed a gate keeps it: the box is a consumer of the policy, not
// the owner of it, and overwriting a caller's reservation would silently raise its bound.

import (
	"context"
	"time"

	"github.com/sagernet/sing-box/common/buildgate"
)

var (
	// buildGateLimit is how many builds may run at once inside one box.
	buildGateLimit = 2
	// buildGateQueue is how many callers may wait for a slot. Beyond it a build is deferred -
	// retryable - rather than queued without bound.
	buildGateQueue = 8
	// buildGateWait is how long one waiter may wait before it is deferred.
	buildGateWait = 30 * time.Second
)

// withBuildGate installs the box's gate unless the context already carries one.
func withBuildGate(ctx context.Context) context.Context {
	if buildgate.FromContext(ctx) != nil {
		return ctx
	}
	return buildgate.ContextWithGate(ctx, buildgate.New(buildGateLimit, buildGateQueue, buildGateWait))
}
