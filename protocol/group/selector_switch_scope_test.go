package group

// What a selector switch may interrupt, and what it may not.
//
// A selection change tears down the connections the group carries, which is the setting's job. The
// scope of that action is the part that is easy to get wrong and expensive when it is: a phone with
// a manual selector and an automatic one would drop flows of the group nobody switched, or flows
// that never went through a group at all.
//
// The three properties below are the ones the window's pass condition asks for, and they are all
// about identity rather than timing:
//
//  1. a flow carried by the group being switched is interrupted;
//  2. a flow carried by ANOTHER group instance is not - including when both instances resolve the
//     same member tag, which is exactly the "two groups, same member name" shape;
//  3. a flow that was dialed directly through the leaf outbound (not through any group) is not the
//     group's to interrupt.

import (
	"context"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/interrupt"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// newScopedSelector builds one selector instance over the shared outbound manager.
func newScopedSelector(t *testing.T, manager adapter.OutboundManager, tag string, members []string, defaultMember string, interruptExternal bool) *Selector {
	t.Helper()
	ctx := service.ContextWith[adapter.OutboundManager](context.Background(), manager)
	created, err := NewSelector(ctx, nil, log.NewNOPFactory().NewLogger(tag), tag, option.SelectorOutboundOptions{
		Outbounds:                 members,
		Default:                   defaultMember,
		InterruptExistConnections: interruptExternal,
	})
	require.NoError(t, err)
	selector, isSelector := created.(*Selector)
	require.True(t, isSelector)
	require.NoError(t, selector.Start(adapter.StartStateStart, &adapter.Scope{}))
	return selector
}

// trackedConnOf unwraps the group's interrupt wrapper down to the member's own connection, which is
// what remembers being closed.
func trackedConnOf(t *testing.T, conn net.Conn) *trackedConn {
	t.Helper()
	for {
		wrapper, isWrapper := conn.(*interrupt.Conn)
		if !isWrapper {
			break
		}
		conn = wrapper.Conn
	}
	tracked, isTracked := conn.(*trackedConn)
	require.True(t, isTracked, "the fixture hands out trackedConn, got %T", conn)
	return tracked
}

func dialOneFlow(t *testing.T, target adapter.Outbound) *trackedConn {
	t.Helper()
	conn, err := target.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return trackedConnOf(t, conn)
}

func TestASwitchInterruptsOnlyTheFlowsItsOwnGroupCarries(t *testing.T) {
	t.Parallel()

	shared := &trackingOutbound{tag: "shared"}
	manualOnly := &trackingOutbound{tag: "manual-only"}
	autoOnly := &trackingOutbound{tag: "auto-only"}
	manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{
		"shared":      shared,
		"manual-only": manualOnly,
		"auto-only":   autoOnly,
	}}

	manual := newScopedSelector(t, manager, "manual", []string{"shared", "manual-only"}, "shared", true)
	automatic := newScopedSelector(t, manager, "automatic", []string{"shared", "auto-only"}, "shared", true)

	// Two group instances resolve the same member tag; each carries its own flow through it.
	manualFlow := dialOneFlow(t, manual)
	automaticFlow := dialOneFlow(t, automatic)
	// A flow that never went through a group: dialed straight through the leaf.
	directFlow := dialOneFlow(t, shared)
	require.NotSame(t, manualFlow, automaticFlow)
	require.NotSame(t, automaticFlow, directFlow)

	// Switch ONE group. The other group's flow and the direct flow are not its connections.
	require.True(t, manual.SelectOutbound("manual-only"))
	require.True(t, manualFlow.closed.Load(), "the switched group's own flow must be interrupted")
	require.False(t, automaticFlow.closed.Load(),
		"another group's flow is not the switched group's to interrupt, even through the same member tag")
	require.False(t, directFlow.closed.Load(),
		"a flow dialed directly through the leaf must not be interrupted by a group switch")

	// The other instance switching its own member still only touches its own flow.
	require.True(t, automatic.SelectOutbound("auto-only"))
	require.True(t, automaticFlow.closed.Load())
	require.False(t, directFlow.closed.Load())
}

// TestASwitchFollowsTheSettingForExternalFlows keeps the per-setting split on the same fixture:
// with the setting off, an external flow survives the switch while an internal one does not.
func TestASwitchFollowsTheSettingForExternalFlows(t *testing.T) {
	t.Parallel()

	leaf := &trackingOutbound{tag: "shared"}
	other := &trackingOutbound{tag: "other"}
	manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{"shared": leaf, "other": other}}
	selector := newScopedSelector(t, manager, "manual", []string{"shared", "other"}, "shared", false)

	externalContext := interrupt.ContextWithIsExternalConnection(context.Background(), true)
	externalConn, err := selector.DialContext(externalContext, N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = externalConn.Close() })
	externalTracked := trackedConnOf(t, externalConn)
	internalTracked := dialOneFlow(t, selector)

	require.True(t, selector.SelectOutbound("other"))
	require.False(t, externalTracked.closed.Load(),
		"with the setting off an external flow must survive the switch")
	require.True(t, internalTracked.closed.Load(),
		"an internal flow is always interrupted by a switch, whatever the setting says")
}
