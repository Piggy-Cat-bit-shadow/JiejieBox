package shadowtls

import (
	"context"
	"net"
	"sync"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// handshakeTargets releases the handshake-target connection sing-shadowtls
// abandons on an error return.
//
// # The upstream defect this exists for
//
// After the ClientHello has been authenticated, sing-shadowtls dials the configured
// handshake target and then, on three separate error returns, forgets it:
//
//	service.go:212-216  write client handshake  -> return without Close
//	service.go:218-222  read server handshake   -> return without Close
//	service.go:224-228  write server handshake  -> return without Close
//
// (v0.2.2-0.20260928201441; the v2 path has the same shape at its `return err` after
// copyUntilHandshakeFinishedV2.) Only the paths that FINISH the handshake close it:
// the task group's Cleanup and the fallback's CopyConn. So every handshake that
// fails after the dial leaks one file descriptor, and the socket survives until the
// process exits. A handshake target that accepts and then stalls, or a client that
// disappears between the two writes, is enough - the target connection is still
// live and ours, and nothing in the fork can reach it.
//
// # Why this is not the ShadowTLS-specific lifecycle framework the module forbids
//
// The prohibition is on a SECOND lifecycle system. There is no second system here:
// the connection is created inside a third-party function and is never registered
// anywhere the fork can observe, so adapter.Scope cannot close it and
// adapter.ConnectionManager never tracks it - the existing framework cannot solve
// this by construction, which is the condition the module reserves this for. What
// is added is the smallest thing that can: the dialer we already own records what
// it opened, keyed by the ONE context that handshake call belongs to, and the
// caller closes the leftovers the instant that call returns. No global registry, no
// goroutine, no state that outlives a single connection.
type handshakeTargets struct {
	access sync.Mutex
	conns  []net.Conn
}

func (t *handshakeTargets) add(conn net.Conn) {
	t.access.Lock()
	t.conns = append(t.conns, conn)
	t.access.Unlock()
}

// closeLeftovers closes every target connection this handshake dialled that the
// service did not close itself.
//
// It is safe on the paths that DID close it - the authenticated handoff and the
// fallback relay - because a second Close on a net.Conn reports ErrClosed and
// changes nothing, and this is called after the service call has already returned,
// so no handshake is still using the connection.
func (t *handshakeTargets) closeLeftovers() {
	t.access.Lock()
	conns := t.conns
	t.conns = nil
	t.access.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

// handshakeTargetsKey is the context key the tracking dialer reads. It is a pointer
// key used as (*handshakeTargetsKey)(nil), matching how adapter.InboundContext is
// carried, so an identical empty struct from another package cannot collide with it.
type handshakeTargetsKey struct{}

func contextWithHandshakeTargets(ctx context.Context, targets *handshakeTargets) context.Context {
	return context.WithValue(ctx, (*handshakeTargetsKey)(nil), targets)
}

func handshakeTargetsFromContext(ctx context.Context) *handshakeTargets {
	targets, _ := ctx.Value((*handshakeTargetsKey)(nil)).(*handshakeTargets)
	return targets
}

// trackingDialer is the handshake dialer handed to the SDK: it dials exactly as the
// configured one does and records a successful dial so the caller can release it.
//
// Only the connection is recorded, never the error: a failed dial has nothing to
// close, and the error must reach the service unchanged so its own classification
// (and ours) still sees the real cause.
type trackingDialer struct {
	dialer N.Dialer
}

func newTrackingDialer(dialer N.Dialer) N.Dialer {
	if dialer == nil {
		return nil
	}
	return &trackingDialer{dialer: dialer}
}

func (d *trackingDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	conn, err := d.dialer.DialContext(ctx, network, destination)
	if err != nil {
		return nil, err
	}
	if targets := handshakeTargetsFromContext(ctx); targets != nil {
		targets.add(conn)
	}
	return conn, nil
}

// ListenPacket forwards to the configured dialer without recording: the ShadowTLS
// handshake is a TCP dial, and a packet connection has no lifetime this wrapper
// could make claims about.
func (d *trackingDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return d.dialer.ListenPacket(ctx, destination)
}

// Upstream keeps the wrapped dialer's capabilities reachable through common.Cast,
// so wrapping cannot silently hide one from a caller that type-asserts it.
func (d *trackingDialer) Upstream() any {
	return d.dialer
}

// Compile-time proof that the wrapper still satisfies the interface the SDK
// consumes for both the default and the per-server-name handshake configurations.
var _ N.Dialer = (*trackingDialer)(nil)
