package trusttunnel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/trafficclass"

	"github.com/stretchr/testify/require"
)

func normalGuard(limit int, queue int) *resourceguard.Guard {
	return resourceguard.NewGuard(resourceguard.Config{
		Limit: limit,
		Queue: queue,
		Wait:  100 * time.Millisecond,
	}, func() resourceguard.Level { return resourceguard.LevelNormal })
}

// TestAdmitSessionNilGuard pins the platform boundary: a process with no
// creation budget keeps the plain client path.
func TestAdmitSessionNilGuard(t *testing.T) {
	t.Parallel()
	require.Nil(t, admitSession(nil))
}

// TestAdmitSessionHoldsOnePermitForOneCreation pins the acquisition and the
// release: the admitted context carries HeldPermit, the slot is busy while the
// creation runs, and it is free again once finish is called.
func TestAdmitSessionHoldsOnePermitForOneCreation(t *testing.T) {
	t.Parallel()
	guard := normalGuard(1, 0)
	admit := admitSession(guard)
	require.NotNil(t, admit)
	admitted, finish, err := admit(context.Background())
	require.NoError(t, err)
	require.True(t, resourceguard.HasHeldPermit(admitted))
	require.NotNil(t, finish)

	// A second creation is refused while the first holds the only slot.
	_, _, err = admit(context.Background())
	require.ErrorIs(t, err, resourceguard.ErrQueueFull)

	finish(nil)
	_, finishAgain, err := admit(context.Background())
	require.NoError(t, err, "the slot must be free after finish")
	finishAgain(nil)
}

// TestAdmitSessionForwardsTheDialResult pins that the finish hook reports the
// real creation outcome to the fast-fail policy instead of a fabricated one.
func TestAdmitSessionForwardsTheDialResult(t *testing.T) {
	t.Parallel()
	guard := normalGuard(1, 0)
	admit := admitSession(guard)
	_, finish, err := admit(context.Background())
	require.NoError(t, err)
	dialErr := errors.New("dial failed")
	finish(dialErr)
	// The permit was released even though the creation failed.
	_, finishAgain, err := admit(context.Background())
	require.NoError(t, err)
	finishAgain(nil)
}

// TestAdmitSessionSkipsAHeldPermit pins the nested seam: a creation that is
// already covered by an outer admission marker is not queued behind our own
// permit.
func TestAdmitSessionSkipsAHeldPermit(t *testing.T) {
	t.Parallel()
	guard := normalGuard(1, 0)
	permit, err := guard.Acquire(context.Background(), resourceguard.ClassNormal)
	require.NoError(t, err)
	defer permit.Release()
	admit := admitSession(guard)
	ctx := resourceguard.WithHeldPermit(context.Background())
	admitted, finish, err := admit(ctx)
	require.NoError(t, err)
	require.Nil(t, finish, "a skipped admission owns no permit to release")
	require.Equal(t, ctx, admitted)
}

// TestAdmitSessionUsesTheInteractiveClass pins that a high-priority flow may
// use the interactive reserve.
func TestAdmitSessionUsesTheInteractiveClass(t *testing.T) {
	t.Parallel()
	guard := resourceguard.NewGuard(resourceguard.Config{
		Limit:              1,
		Queue:              0,
		Wait:               100 * time.Millisecond,
		InteractiveReserve: 1,
	}, func() resourceguard.Level { return resourceguard.LevelNormal })
	admit := admitSession(guard)
	// Hold the normal slot so only the interactive reserve is left.
	normalPermit, err := guard.Acquire(context.Background(), resourceguard.ClassNormal)
	require.NoError(t, err)
	defer normalPermit.Release()
	interactiveCtx := trafficclass.WithClass(context.Background(), trafficclass.ClassInteractive)
	_, finish, err := admit(interactiveCtx)
	require.NoError(t, err, "the interactive reserve must be reachable from the flow class")
	finish(nil)
}
