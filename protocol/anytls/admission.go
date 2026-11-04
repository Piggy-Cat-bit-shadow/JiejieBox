package anytls

import (
	"context"
	"net"

	"github.com/sagernet/sing-box/common/resourceguard"
	M "github.com/sagernet/sing/common/metadata"
)

// streamDialFunc is the one operation this file admits: opening a stream, which may create a
// session (TCP connect + TLS handshake + key exchange) when no idle session can be reused.
type streamDialFunc func(ctx context.Context, destination M.Socksaddr) (net.Conn, error)

// dialAdmitted runs one AnyTLS dial under the process admission policy.
//
// # Why the permit is taken here and not inside the session pool
//
// Every stream this outbound opens - TCP, and UoT for every UDP flow - arrives here, and this
// is the last point BEFORE the client decides whether it reuses an idle session or creates one.
// Creating one costs a TCP connect, a TLS handshake (including the post-quantum key exchange),
// and a read buffer; the measured failure had 201 of them in flight at once. Bounding the
// decision here bounds the cost, while a bound placed inside the pool would already have paid
// for the goroutine and the stack that arrive with the flow.
//
// # What is bounded, precisely
//
// CONCURRENT creation, not steady state. The permit is released as soon as the dial returns, so
// a healthy process establishes sessions as fast as the gate's limit allows and then behaves
// exactly as before. Holding the permit for the connection's lifetime would additionally cap
// the number of live streams, but that needs a wrapper around the returned connection, and the
// AnyTLS stream implements buffer, geometry and handshake interfaces that route code probes
// for - hiding them behind a net.Conn wrapper would silently downgrade the copy path. Bounding
// the creation storm is what the evidence supports; capping live streams is a different change
// with a different test, and it is not smuggled in here.
//
// A nil guard admits everything and is the whole point of the design: this function is the only
// place that knows about admission, and on every platform without a process budget it costs one
// nil check.
func dialWithAdmission(ctx context.Context, guard *resourceguard.Guard, dial streamDialFunc, destination M.Socksaddr) (net.Conn, error) {
	permit, err := guard.Acquire(ctx, resourceguard.ClassNormal)
	if err != nil {
		return nil, err
	}
	defer permit.Release()
	// The TLS handshake this dial may perform is admitted again at the shared TLS layer. Marking
	// the context is what stops one handshake being charged twice; see resourceguard.HeldPermit.
	return dial(resourceguard.WithHeldPermit(ctx), destination)
}
