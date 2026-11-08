package box

// F03 — the box-level wiring of the build reservation.

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/buildgate"

	"github.com/stretchr/testify/require"
)

func TestTheBoxInstallsOneBuildReservation(t *testing.T) {
	// Bound the wait and the slots so the test asserts the BOUND rather than waiting out the
	// production 30-second budget (one slot, no queue: the second build defers immediately).
	originalLimit, originalQueue, originalWait := buildGateLimit, buildGateQueue, buildGateWait
	buildGateLimit, buildGateQueue, buildGateWait = 1, 0, time.Millisecond
	t.Cleanup(func() {
		buildGateLimit, buildGateQueue, buildGateWait = originalLimit, originalQueue, originalWait
	})
	ctx := withBuildGate(context.Background())
	gate := buildgate.FromContext(ctx)
	require.NotNil(t, gate, "every provider built from the box context must find the reservation")

	// The gate is bounded: a second build is deferred while the first holds a slot, which is
	// the property the wiring exists for.
	release, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	_, err = gate.Acquire(context.Background())
	require.ErrorIs(t, err, buildgate.ErrDeferred)
	release()
}

func TestAnEmbeddersReservationIsKept(t *testing.T) {
	embedder := buildgate.New(1, 0, 0)
	ctx := buildgate.ContextWithGate(context.Background(), embedder)
	installed := withBuildGate(ctx)
	require.Same(t, embedder, buildgate.FromContext(installed),
		"the box must not replace a reservation a caller installed")
}

func TestTheWiringIsIdempotent(t *testing.T) {
	once := buildgate.FromContext(withBuildGate(context.Background()))
	twice := buildgate.FromContext(withBuildGate(withBuildGate(context.Background())))
	require.NotNil(t, once)
	require.NotNil(t, twice)
	require.NotSame(t, once, twice, "two independent boxes get independent reservations")
}
