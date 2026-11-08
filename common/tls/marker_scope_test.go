package tls

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"

	"github.com/stretchr/testify/require"
)

// TestHeldPermitMarkerDoesNotOutliveTheDial is the G03 lifetime contract.
//
// The marker exists so one logical dial crosses the AnyTLS seam and this seam without paying twice.
// The failure it must not cause: a long-lived client context that keeps the marker after the permit
// was released, so every later connection believes it is already admitted and the gate silently
// stops bounding creation. The assertion is therefore on the CALLER's context: it must be unchanged
// after the dial, and a second dial on the same base context must acquire again.
func TestHeldPermitMarkerDoesNotOutliveTheDial(t *testing.T) {
	guard := newSeamGuard(t, seamConfig(1, 4, time.Second))
	base := resourceguard.NewContext(context.Background(), guard)

	var marks []bool
	dial := func(ctx context.Context) (Conn, error) {
		marks = append(marks, resourceguard.HasHeldPermit(ctx))
		return nil, errors.New("handshake refused")
	}

	_, firstErr := handshakeWithAdmission(base, dial)
	require.Error(t, firstErr)
	_, secondErr := handshakeWithAdmission(base, dial)
	require.Error(t, secondErr)

	require.Equal(t, []bool{true, true}, marks, "each admitted dial must carry the marker")
	require.False(t, resourceguard.HasHeldPermit(base), "the caller's context must not keep the marker")
	require.Equal(t, uint64(2), guard.Stats().Gate.Requested,
		"a second dial on the same base context must acquire a second permit")
}

// TestNestedHandshakeChargesOnePermit is the other half: a dial that is already admitted must not
// take a second permit when it re-enters this seam (detour, chain, ECH retry).
func TestNestedHandshakeChargesOnePermit(t *testing.T) {
	guard := newSeamGuard(t, seamConfig(1, 4, time.Second))
	base := resourceguard.NewContext(context.Background(), guard)

	var inner bool
	outer := func(ctx context.Context) (Conn, error) {
		_, _ = handshakeWithAdmission(ctx, func(innerCtx context.Context) (Conn, error) {
			inner = resourceguard.HasHeldPermit(innerCtx)
			return nil, errors.New("inner refused")
		})
		return nil, errors.New("outer refused")
	}
	_, _ = handshakeWithAdmission(base, outer)
	require.True(t, inner, "the inner seam must see the held marker")
	require.Equal(t, uint64(1), guard.Stats().Gate.Requested,
		"one logical dial must hold exactly one handshake permit")
}
