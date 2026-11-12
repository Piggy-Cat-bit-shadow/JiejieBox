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
	"github.com/sagernet/sing-box/common/resourceguard"
)

var (
	// buildGateLimit is how many builds may run at once inside one box AT LEVEL NORMAL.
	//
	// It is the base of a derivation, not a constant the reservation always admits: the shape at
	// Armed and Critical is resourceguard's own (see common/buildgate), so pressure tightens the
	// reservation instead of being handled by a second policy. LevelCritical collapses it to one
	// build, which is the strongest bound of overlapping old/new document pairs this box can prove.
	buildGateLimit = 2
	// buildGateQueue is how many callers may wait for a slot at LevelNormal. Beyond it a build is
	// deferred - retryable - rather than queued without bound.
	buildGateQueue = 8
	// buildGateWait is how long one waiter may wait at LevelNormal before it is deferred.
	buildGateWait = 30 * time.Second
)

// withBuildGate installs the box's gate unless the context already carries one.
//
// The gate is created with the process's pressure source attached, not with a snapshot of it: the
// source is looked up from THIS context at every acquisition, so a guard that the oomkiller
// registers while the box starts (after this call) is still seen, and a build that runs after a
// pressure transition is admitted under the new level. A build with no guard registered - every
// platform except an iOS Network Extension - reads LevelNormal and keeps the flat reservation.
func withBuildGate(ctx context.Context) context.Context {
	if buildgate.FromContext(ctx) != nil {
		return ctx
	}
	config := resourceguard.Config{
		Limit: buildGateLimit,
		Queue: buildGateQueue,
		Wait:  buildGateWait,
	}
	return buildgate.ContextWithGate(ctx, buildgate.NewWithPressure(config, buildPressure(ctx)))
}

// buildPressure is the bridge from the process's ONE pressure fact to the build reservation.
//
// It reads the guard the pressure policy owner registered (service/oomkiller on a Network
// Extension) rather than sampling anything itself: a second measurement here would be the drift
// this tree has paid for before. A nil guard means no pressure policy, which is LevelNormal.
func buildPressure(ctx context.Context) func() resourceguard.Level {
	return func() resourceguard.Level {
		guard := resourceguard.FromContext(ctx)
		if guard == nil {
			return resourceguard.LevelNormal
		}
		return guard.Level()
	}
}
