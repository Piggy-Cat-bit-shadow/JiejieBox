package anytls

import (
	"context"
	"net"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/trafficclass"
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
	// One logical dial holds one permit, however many seams it crosses. A detour, a chain or a
	// fallback re-enters the dial path under the caller's context: an outbound dialling through
	// another outbound, or the TLS handshake of a session this dial is creating. The outer seam
	// already admitted this work, so asking the gate again would queue a dial behind a permit its
	// own cohort is holding - the defect the TLS seam had, and the reason this check exists on
	// both seams rather than only one.
	if resourceguard.HasHeldPermit(ctx) {
		return dial(ctx, destination)
	}
	// A flow whose class says a person is waiting on it may use the interactive reserve; that is
	// what makes the reserve a priority rather than dead capacity. Everything else is normal.
	class := resourceguard.ClassNormal
	if flowClass, loaded := trafficclass.ClassFromContext(ctx); loaded && flowClass.IsHighPriority() {
		class = resourceguard.ClassInteractive
	}
	permit, err := guard.Acquire(ctx, class)
	if err != nil {
		return nil, err
	}
	defer permit.Release()
	// Queueing at the gate is our own backpressure, not creation time. Starting the clock before
	// Acquire would make a queued refusal look slow and keep the fast-fail rate limit disarmed
	// exactly when the gate is saturated.
	started := time.Now()
	// The TLS handshake this dial may perform is admitted again at the shared TLS layer. Marking
	// the context is what stops one handshake being charged twice; see resourceguard.HeldPermit.
	conn, dialErr := dial(resourceguard.WithHeldPermit(ctx), destination)
	// Report the outcome to the fast-fail policy. A queue refusal never reaches here (Acquire
	// returned it), so what is reported is a real creation result - which is exactly what the
	// policy needs to distinguish a failing far side from this process's own backpressure.
	guard.ObserveOutcome(started, dialErr)
	return conn, dialErr
}
