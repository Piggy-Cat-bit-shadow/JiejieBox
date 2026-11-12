package anytls

import (
	"context"
	"net"

	"github.com/sagernet/sing-box/common/resourceguard"
	M "github.com/sagernet/sing/common/metadata"
)

// streamDialFunc is one AnyTLS stream dial. It may create a session (TCP connect + TLS handshake +
// key exchange) when no idle session can be reused, and that is the expensive path this file admits.
type streamDialFunc func(ctx context.Context, destination M.Socksaddr) (net.Conn, error)

// admit runs create under one process admission permit and reports its outcome.
//
// # Where this is applied, and why that changed
//
// The permit used to wrap the whole AnyTLS dial, which is BEFORE the client decides whether to
// reuse an idle session or create one. Every healthy reuse therefore queued behind creations it was
// not part of: the gate is per process, and a saturated creation budget parked reuse too (audit Q10).
//
// The client now calls its AdmitCreation hook only from `createSession`, after the reuse attempt
// failed. This function is that hook's body, so the permit covers exactly the work it was sized for
// - connect, TLS handshake, first write - and a reuse of an already established session does not
// touch it at all.
//
// # What is bounded, precisely
//
// CONCURRENT creation, not steady state. The permit is released as soon as the creation returns, so
// a healthy process establishes sessions as fast as the gate's limit allows and then behaves
// exactly as before. Holding the permit for a session's lifetime would additionally cap the number
// of live sessions, but that needs a wrapper around the session, and the AnyTLS stream implements
// buffer, geometry and handshake interfaces that route code probes for - hiding them behind a
// net.Conn wrapper would silently downgrade the copy path. Bounding the creation storm is what the
// evidence supports; capping live sessions is a different change with a different test.
//
// A nil guard admits everything and is the whole point of the design: this function is the only
// place that knows about admission, and on every platform without a process budget it costs one
// nil check.
func admit(ctx context.Context, guard *resourceguard.Guard, key string, create func(context.Context) error) error {
	// The body is common/resourceguard.(*Guard).AdmitCreation, shared with the QUIC clients' hooks:
	// one logical dial holds one permit however many seams it crosses (HeldPermit), the interactive
	// reserve is reachable, the creation clock starts after the permit, and the outcome is reported
	// with the key the acquisition used. This wrapper exists so the AnyTLS placement - creation
	// only, after the idle-reuse attempt - stays visible here.
	return guard.AdmitCreation(ctx, key, create)
}

// admitSessionCreation is the AnyTLS client's AdmitCreation hook. It is called exactly once per
// new session, and never for a reuse.
func admitSessionCreation(guard *resourceguard.Guard, key string) func(context.Context, func(context.Context) error) error {
	if guard == nil {
		return nil
	}
	return func(ctx context.Context, create func(context.Context) error) error {
		return admit(ctx, guard, key, create)
	}
}

// dialWithAdmission runs one AnyTLS stream dial under the process admission policy.
//
// It is the dial-shaped wrapper around admit, kept for callers and tests that want to bracket a
// dial rather than a creation. Production does NOT use it around the client: the client owns the
// reuse/creation split and asks admitSessionCreation only on the creation path.
func dialWithAdmission(ctx context.Context, guard *resourceguard.Guard, dial streamDialFunc, destination M.Socksaddr) (net.Conn, error) {
	return dialWithAdmissionFor(ctx, guard, "", dial, destination)
}

// dialWithAdmissionFor is dialWithAdmission with the AnyTLS server as the rate-window key.
//
// The destination parameter is the TARGET of the stream, which may differ on every flow, while the
// session whose creation can fail is shared per server. Keying the fast-fail window by the target
// would let one unreachable target budget a storm that delays every other target sharing the same
// server, and would grow one bucket per target. The production outbound therefore passes its
// server address; the empty key keeps the old process-global behaviour for tests and callers that
// do not name one.
func dialWithAdmissionFor(ctx context.Context, guard *resourceguard.Guard, key string, dial streamDialFunc, destination M.Socksaddr) (net.Conn, error) {
	var conn net.Conn
	err := admit(ctx, guard, key, func(ctx context.Context) error {
		var dialErr error
		conn, dialErr = dial(ctx, destination)
		return dialErr
	})
	return conn, err
}
