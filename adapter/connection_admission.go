package adapter

import (
	"context"
	"net"
)

// ConnectionTicket is one authorized slot in a connection manager's admission budget.
//
// It is acquired BEFORE the caller hands a socket to the manager and consumed by exactly one of
// TrackConn / TrackPacketConn (success) or Release (the dial failed or was cancelled). The manager
// can therefore account for authorized-but-not-yet-tracked sockets, which is what makes the
// admission check atomic with shutdown: a ticket acquired before Close is either consumed before the
// manager closes or rejected by it, and either way the socket ends up in the manager's ledger.
type ConnectionTicket interface {
	TrackConn(conn net.Conn) (net.Conn, error)
	TrackPacketConn(conn net.PacketConn) (net.PacketConn, error)
	Release()
}

// ConnectionAdmission is the optional capability the dialer asks for before tracking a connection.
//
// Managers that do not implement it keep the previous behaviour: the dialer tracks directly.
type ConnectionAdmission interface {
	AcquireConnection(ctx context.Context) (ConnectionTicket, error)
}

type controlPlaneAdmissionKey struct{}

// WithControlPlaneAdmission marks a dial that belongs to a control plane (DNS in this fork). Such a
// dial draws from a small reserved admission pool, so a normal-traffic backlog cannot stop the
// resolver from establishing its own cold connection. The reservation is bounded separately; it is
// not a bypass of the shutdown contract.
func WithControlPlaneAdmission(ctx context.Context) context.Context {
	return context.WithValue(ctx, controlPlaneAdmissionKey{}, true)
}

// IsControlPlaneAdmission reports whether the dial carries the control-plane marker.
func IsControlPlaneAdmission(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	controlPlane, _ := ctx.Value(controlPlaneAdmissionKey{}).(bool)
	return controlPlane
}
