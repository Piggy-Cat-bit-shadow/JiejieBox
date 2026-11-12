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
	ctx = adapter.WithControlPlaneAdmission(ctx)
	d.recordDialBoundary(ctx)
	return d.Dialer.DialContext(ctx, network, destination)
}

func (d controlPlaneDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	ctx = adapter.WithControlPlaneAdmission(ctx)
	d.recordDialBoundary(ctx)
	return d.Dialer.ListenPacket(ctx, destination)
}

// recordDialBoundary opens a dial-path record for this attempt and names the detour it goes
// through, when a caller installed a recorder.
//
// # Why the boundary is HERE
//
// This wrapper is what every remote DNS transport dials through, so it is the one place that can
// tell "this exchange established a connection" from "it reused one" for all of them at once - and
// it is also where the detour is knowable without repeating the resolution the dial is about to do
// anyway. The recorder itself is installed by the query path (see dns.Client), so a process nobody
// is observing pays one context lookup per dial.
//
// # Why the detour is named, not the resolved member
//
// A detour that is a GROUP resolves its member inside the group's own DialContext, at the instant of
// the hand-off; that group records the member it chose. Recording the detour here only says which
// object the transport entered, which is the part this boundary actually knows.
func (d controlPlaneDialer) recordDialBoundary(ctx context.Context) {
	path := adapter.DialPathFromContext(ctx)
	if path == nil {
		return
	}
	path.BeginDial()
	detour, hasDetour := dialer.DetourOf(d.Dialer)
	if !hasDetour || detour == nil {
		// No configured outbound carried this dial: it went out directly.
		path.MarkDirect()
		return
	}
	revision, hasRevision := uint64(0), false
	if reporter, isReporter := detour.(adapter.DialPathRevisionReporter); isReporter {
		revision, hasRevision = reporter.DialPathRevision(), true
	}
	path.Record(detour.Tag(), revision, hasRevision)
}
