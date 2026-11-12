package dns

// The dial-path boundary of a remote DNS transport.
//
// A projection that only works when a test hands it a chain proves nothing about production: the
// chain has to be recorded by the dialer the transports actually use. These cases drive the REAL
// remote dialer the DNS transports are built with (dns.NewRemoteDialer -> controlPlaneDialer ->
// common/dialer.DetourDialer) over a fake outbound manager, so the facts pinned here are the ones a
// live exchange records.

import (
	"context"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// TestRemoteDialerNamesTheDetourItDialsThrough: the boundary records the detour object it enters,
// and does so without the caller having to know which wrapper the dialer ended up as.
func TestRemoteDialerNamesTheDetourItDialsThrough(t *testing.T) {
	t.Parallel()

	leaf := &dialPathOutbound{tag: "node-a"}
	manager := &dialPathOutboundManager{byTag: map[string]adapter.Outbound{"node-a": leaf}}
	ctx := service.ContextWith[adapter.OutboundManager](context.Background(), manager)

	dialer, err := NewRemoteDialer(ctx, remoteDNSOptions("node-a"))
	require.NoError(t, err)

	dialPath := adapter.NewDialPath()
	conn, err := dialer.DialContext(adapter.WithDialPath(context.Background(), dialPath), N.NetworkUDP, M.ParseSocksaddr("192.0.2.1:53"))
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	result := dialPath.Result()
	require.Equal(t, 1, result.Dials)
	require.Len(t, result.Steps, 1)
	require.Equal(t, "node-a", result.Steps[0].Tag,
		"a plain outbound detour is the leaf itself, and the boundary must name it")
	require.False(t, result.Steps[0].HasRevision, "a plain outbound has no selection revision")
}

// TestRemoteDialerRecordsAGroupDecisionUnderTheDetour: the boundary names the detour, and the group
// names the member it handed the connection to, so the event's chain is the actual one.
func TestRemoteDialerRecordsAGroupDecisionUnderTheDetour(t *testing.T) {
	t.Parallel()

	leaf := &dialPathOutbound{tag: "node-a"}
	group := &dialPathGroup{tag: "proxy", revision: 5, chosen: leaf}
	manager := &dialPathOutboundManager{byTag: map[string]adapter.Outbound{"proxy": group}}
	ctx := service.ContextWith[adapter.OutboundManager](context.Background(), manager)

	dialer, err := NewRemoteDialer(ctx, remoteDNSOptions("proxy"))
	require.NoError(t, err)

	dialPath := adapter.NewDialPath()
	conn, err := dialer.DialContext(adapter.WithDialPath(context.Background(), dialPath), N.NetworkUDP, M.ParseSocksaddr("192.0.2.1:53"))
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	result := dialPath.Result()
	require.Equal(t, []string{"proxy", "node-a"}, dialPathTagsOf(result),
		"the chain is the detour followed by the member the group chose")
	leafStep, ok := result.Leaf()
	require.True(t, ok)
	require.Equal(t, "node-a", leafStep.Tag)
	require.Equal(t, uint64(5), leafStep.Revision,
		"the member step carries the choosing group's revision")
	require.True(t, leafStep.HasRevision)
}

// TestRemoteDialerWithoutARecorderIsUntouched: the projection is inert unless somebody installed a
// record, which is what makes it affordable on every dial in the process.
func TestRemoteDialerWithoutARecorderIsUntouched(t *testing.T) {
	t.Parallel()

	leaf := &dialPathOutbound{tag: "node-a"}
	manager := &dialPathOutboundManager{byTag: map[string]adapter.Outbound{"node-a": leaf}}
	ctx := service.ContextWith[adapter.OutboundManager](context.Background(), manager)

	dialer, err := NewRemoteDialer(ctx, remoteDNSOptions("node-a"))
	require.NoError(t, err)

	conn, err := dialer.DialContext(context.Background(), N.NetworkUDP, M.ParseSocksaddr("192.0.2.1:53"))
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

func remoteDNSOptions(detour string) option.RemoteDNSServerOptions {
	return option.RemoteDNSServerOptions{
		RawLocalDNSServerOptions: option.RawLocalDNSServerOptions{
			DialerOptions: option.DialerOptions{Detour: detour},
		},
		DNSServerAddressOptions: option.DNSServerAddressOptions{Server: "192.0.2.1", ServerPort: 53},
	}
}

// dialPathOutboundManager resolves a small fixed tag map.
type dialPathOutboundManager struct {
	adapter.OutboundManager
	byTag    map[string]adapter.Outbound
	fallback adapter.Outbound
}

func (m *dialPathOutboundManager) Outbound(tag string) (adapter.Outbound, bool) {
	outbound, loaded := m.byTag[tag]
	return outbound, loaded
}

func (m *dialPathOutboundManager) Default() adapter.Outbound {
	return m.fallback
}

// dialPathOutbound is a leaf that hands back an in-memory connection.
type dialPathOutbound struct {
	adapter.Outbound
	tag string
}

func (o *dialPathOutbound) Tag() string { return o.tag }

func (o *dialPathOutbound) DialContext(_ context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

func (o *dialPathOutbound) ListenPacket(_ context.Context, _ M.Socksaddr) (net.PacketConn, error) {
	return nil, nil
}

// dialPathGroup records the member it hands the dial to, the way a real group does, and reports a
// selection revision.
type dialPathGroup struct {
	adapter.Outbound
	tag      string
	revision uint64
	chosen   adapter.Outbound
}

func (g *dialPathGroup) Tag() string { return g.tag }

func (g *dialPathGroup) DialPathRevision() uint64 { return g.revision }

func (g *dialPathGroup) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	adapter.RecordDialStep(ctx, g, g.chosen)
	return g.chosen.DialContext(ctx, network, destination)
}

func dialPathTagsOf(result adapter.DialPathResult) []string {
	tags := make([]string, 0, len(result.Steps))
	for _, step := range result.Steps {
		tags = append(tags, step.Tag)
	}
	return tags
}
