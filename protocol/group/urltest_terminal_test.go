package group

// The URLTestGroup's terminal state.
//
// Close used to stop the ticker and cancel the background context, but Select and the selection
// writers still had paths that could read or publish a member afterwards. A retired group is not a
// paused group: it has no services, no background context and no authority to choose where new
// traffic goes. These cases pin the terminal boundary on every entry point that can still act.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

func TestURLTestGroupIsTerminalAfterClose(t *testing.T) {
	nodeA := &trackingOutbound{tag: "node-a"}
	nodeB := &trackingOutbound{tag: "node-b"}
	group, storage := newGroupFixture(t, "https://probe.example/generate_204", nodeA, nodeB)
	group.selected.Store(&selectedState{tcp: nodeA, udp: nodeA})
	storage.StoreURLTestHistoryFor("node-a", group.scope, &adapter.URLTestHistory{Time: time.Now(), Delay: 20})
	storage.StoreURLTestHistoryFor("node-b", group.scope, &adapter.URLTestHistory{Time: time.Now(), Delay: 5})
	wrapper := newTestURLTestWrapper(group, log.NewNOPFactory().NewLogger("urltest"))

	selected, exists := group.Select(N.NetworkTCP)
	require.True(t, exists)
	require.Equal(t, "node-a", selected.Tag())
	require.Equal(t, "node-a", wrapper.Selected(N.NetworkTCP).Tag())

	require.NoError(t, group.Close())

	// Select must not choose a member from a group that no longer exists.
	selected, exists = group.Select(N.NetworkTCP)
	require.False(t, exists)
	require.Nil(t, selected)
	require.Nil(t, wrapper.Selected(N.NetworkTCP), "a closed group still reported a selection")

	// A dial that arrives after the close must fail closed, not lease a retired member.
	_, err := wrapper.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
	require.ErrorIs(t, err, os.ErrClosed)

	// A health round is terminal too: no measurement is started and a synchronous caller gets the
	// closed error rather than an empty successful result.
	_, err = group.URLTest(context.Background())
	require.ErrorIs(t, err, os.ErrClosed)

	// A queued selection update cannot publish after Close.
	group.performUpdateCheck()
	state := group.selected.Load()
	require.NotNil(t, state)
	require.Same(t, nodeA, state.tcp, "Close was followed by a new selection")

	// Neither Touch nor PostStart can resurrect background work.
	group.Touch()
	group.PostStart()
	group.access.Lock()
	require.False(t, group.started)
	require.Nil(t, group.ticker)
	group.access.Unlock()
}
