package group

// The dial-path projection is only worth anything if the DECISION POINTS fill it in. A selector
// chooses its member at the instant of the hand-off, so it is the only place that can say which
// member carried the connection and at which selection revision. These cases drive the real
// Selector.DialContext and read what it recorded; the projection onto the DNS event document is
// pinned in dns/events_dialpath_test.go.

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

func TestSelectorRecordsTheMemberItHandedTheDialTo(t *testing.T) {
	t.Parallel()

	selector, _ := newSelectorFixture(t, false)
	dialPath := adapter.NewDialPath()
	ctx := adapter.WithDialPath(context.Background(), dialPath)

	dialPath.BeginDial()
	conn, err := selector.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	result := dialPath.Result()
	require.Len(t, result.Steps, 1, "one group decision, one step")
	require.Equal(t, "node-a", result.Steps[0].Tag)
	require.True(t, result.Steps[0].HasRevision,
		"a selector reports its selection revision, so the step is not an unknown-revision zero")
	require.Equal(t, uint64(0), result.Steps[0].Revision,
		"the initial selection is revision zero, and zero here means revision zero because the group "+
			"reports one")

	// A selection change advances the revision, and the NEXT dial records the new pair. The pair
	// recorded by the first dial must not move with it.
	require.True(t, selector.SelectOutbound("node-b"))
	dialPath.BeginDial()
	conn, err = selector.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	result = dialPath.Result()
	require.Len(t, result.Steps, 1, "a new attempt starts a new chain")
	require.Equal(t, "node-b", result.Steps[0].Tag)
	require.Equal(t, uint64(1), result.Steps[0].Revision,
		"the step carries the revision the decision was taken at, not the group's current value")
}

// A dial that was not asked for an attribution records nothing and must not pay for one: the
// decision point is reached on every connection in the process.
func TestSelectorRecordsNothingWithoutARecorder(t *testing.T) {
	t.Parallel()

	selector, _ := newSelectorFixture(t, false)
	conn, err := selector.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}
