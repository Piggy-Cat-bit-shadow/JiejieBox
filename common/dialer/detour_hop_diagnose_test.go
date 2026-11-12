package dialer

// O03 鈥?the hop an outbound's OWN dialer makes.
//
// A DNS transport records the detour it hands the query to. When that detour is itself an outbound
// whose dialer declares `detour: warp`, the second hop happens inside that outbound, past the
// boundary the transport opened: the projection named `proxy` and stopped, although the connection
// was established through `warp`. The outbound's dialer is the only place that knows, so it records
// the hop there.

import (
	"context"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// hopOutbound is a leaf that answers the dial itself.
type hopOutbound struct {
	tag string
}

func (o *hopOutbound) Type() string                                   { return C.TypeDirect }
func (o *hopOutbound) Tag() string                                    { return o.tag }
func (o *hopOutbound) Network() []string                              { return []string{N.NetworkTCP, N.NetworkUDP} }
func (o *hopOutbound) Dependencies() []string                         { return nil }
func (o *hopOutbound) Start(adapter.StartStage, *adapter.Scope) error { return nil }
func (o *hopOutbound) Close() error                                   { return nil }
func (o *hopOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}
func (o *hopOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// hopManager resolves one tag.
type hopManager struct {
	outbound *hopOutbound
}

func (m *hopManager) Outbounds() []adapter.Outbound { return []adapter.Outbound{m.outbound} }
func (m *hopManager) Outbound(tag string) (adapter.Outbound, bool) {
	if tag == m.outbound.tag {
		return m.outbound, true
	}
	return nil, false
}
func (m *hopManager) Default() adapter.Outbound                      { return m.outbound }
func (m *hopManager) Start(adapter.StartStage, *adapter.Scope) error { return nil }
func (m *hopManager) Close() error                                   { return nil }
func (m *hopManager) Create(context.Context, adapter.Router, log.ContextLogger, string, string, any) error {
	return nil
}

// TestTheOutboundsOwnDetourHopIsRecorded drives the production DetourDialer under a recorder.
func TestTheOutboundsOwnDetourHopIsRecorded(t *testing.T) {
	hop := &hopOutbound{tag: "warp"}
	detourDialer := NewDetour(&hopManager{outbound: hop}, "warp", false)

	recorder := adapter.NewDialPath()
	recorder.BeginDial()
	ctx := adapter.WithDialPath(context.Background(), recorder)

	conn, err := detourDialer.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("127.0.0.1:1"))
	require.NoError(t, err)
	_ = conn.Close()

	result := recorder.Result()
	require.Len(t, result.Steps, 1, "the hop the outbound's own dialer made is part of the path")
	require.Equal(t, "warp", result.Steps[0].Tag)
	require.Empty(t, result.Steps[0].Group, "no group chose this hop; it is the outbound's own dialer")
	require.Equal(t, adapter.DialPathReasonNone, result.Reason())
}

// TestADialWithoutARecorderIsUnchanged: the hop recording costs one context lookup and changes
// nothing about the dial.
func TestADialWithoutARecorderIsUnchanged(t *testing.T) {
	hop := &hopOutbound{tag: "warp"}
	detourDialer := NewDetour(&hopManager{outbound: hop}, "warp", false)
	conn, err := detourDialer.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("127.0.0.1:1"))
	require.NoError(t, err)
	_ = conn.Close()
	require.Nil(t, adapter.DialPathFromContext(context.Background()))
}
