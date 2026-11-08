package anytls

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// TestDialAdmissionMarkerIsScopedToOneDial is the AnyTLS half of the G03 lifetime contract. The
// marker must be visible to the dial, absent from the caller's context afterwards, and a second dial
// on that same context must take a new permit. A long-lived client context carrying a released
// permit would silently disable the gate for every later connection.
func TestDialAdmissionMarkerIsScopedToOneDial(t *testing.T) {
	guard := resourceguard.NewGuard(resourceguard.Config{
		Limit:           1,
		Queue:           4,
		Wait:            time.Second,
		DemoteAfter:     time.Second,
		StarvationAfter: time.Second,
	}, func() resourceguard.Level { return resourceguard.LevelNormal })
	base := resourceguard.NewContext(context.Background(), guard)

	var marks []bool
	dial := func(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
		marks = append(marks, resourceguard.HasHeldPermit(ctx))
		return nil, nil
	}
	for index := 0; index < 2; index++ {
		conn, err := dialWithAdmission(base, guard, dial, M.Socksaddr{})
		require.NoError(t, err)
		require.Nil(t, conn)
	}

	require.Equal(t, []bool{true, true}, marks)
	require.False(t, resourceguard.HasHeldPermit(base))
	require.Equal(t, uint64(2), guard.Stats().Gate.Requested)
	require.Equal(t, uint64(2), guard.Stats().Gate.Released)
}
