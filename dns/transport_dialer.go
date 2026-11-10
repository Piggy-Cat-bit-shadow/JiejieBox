package dns

import (
	"context"
	"net"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func NewLocalDialer(ctx context.Context, options option.LocalDNSServerOptions) (N.Dialer, error) {
	return dialer.NewWithOptions(dialer.Options{
		Context:        ctx,
		Options:        options.DialerOptions,
		DirectResolver: true,
	})
}

func NewRemoteDialer(ctx context.Context, options option.RemoteDNSServerOptions) (N.Dialer, error) {
	remoteDialer, err := dialer.NewWithOptions(dialer.Options{
		Context:        ctx,
		Options:        options.DialerOptions,
		RemoteIsDomain: options.ServerIsDomain(),
		DirectResolver: true,
	})
	if err != nil {
		return nil, err
	}
	return controlPlaneDialer{Dialer: remoteDialer}, nil
}

// controlPlaneDialer marks every dial as control-plane so the connection manager can admit it
// from the small reserved pool. A resolver that cannot establish its own cold socket while
// normal traffic is at its disposal budget is a resolver that has stopped being a resolver.
type controlPlaneDialer struct {
	N.Dialer
}

// Upstream exposes the wrapped dialer to the chain walkers. Both the detour preflight
// (common.Cast in dialer.InitializeDetour) and the capability check
// (common/dialer.resolvedDetour) find the DetourDialer by walking WithUpstream; a wrapper
// without it silently hides the detour: the detour is not initialized at start, and the
// "does not carry" warning is never reported even though the transport would fail every
// query. This is the same contract resolveDialer and DetourDialer already implement.
func (d controlPlaneDialer) Upstream() any {
	return d.Dialer
}

func (d controlPlaneDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return d.Dialer.DialContext(adapter.WithControlPlaneAdmission(ctx), network, destination)
}
