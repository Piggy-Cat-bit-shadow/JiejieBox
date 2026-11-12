package resourceguard

import (
	"context"
	"time"

	"github.com/sagernet/sing-box/common/trafficclass"
)

// AdmitCreation runs one resource creation under this process's admission policy.
//
// # Why this is the shared body of the dependency hooks
//
// The resources this policy protects are not requests: a QUIC connection, an AnyTLS session, a
// TrustTunnel session. Each is created once and then reused by many flows, so charging the policy at
// the flow would multiply one creation by its demanders, and charging it before the owner knows
// whether it will create would park healthy reuses behind a saturated creation budget (audit Q10).
// The owners therefore expose a creation-only hook, and this method is what those hooks run: the
// permit covers exactly the creation, is released when the creation returns, and is not asked for at
// all when the caller reuses an established resource.
//
// # The rules it carries
//
//   - A nested dial that already holds a permit reuses it (HeldPermit): one logical dial is charged
//     once, however many seams - detour, TLS handshake, HTTP client - it crosses.
//   - An interactive flow may use the interactive reserve; everything else is normal.
//   - The creation clock starts AFTER the permit, so queueing at the gate is not charged to the far
//     side's fast-fail window.
//   - The outcome is reported with the same key the acquisition used, so one failing server does
//     not arm another's window.
//
// A nil guard admits everything, which is what every platform without a process budget gets.
func (g *Guard) AdmitCreation(ctx context.Context, key string, create func(context.Context) error) error {
	if g == nil || HasHeldPermit(ctx) {
		return create(ctx)
	}
	class := ClassNormal
	if flowClass, loaded := trafficclass.ClassFromContext(ctx); loaded && flowClass.IsHighPriority() {
		class = ClassInteractive
	}
	permit, err := g.AcquireFor(ctx, class, key)
	if err != nil {
		return err
	}
	defer permit.Release()
	started := time.Now()
	createErr := create(WithHeldPermit(ctx))
	g.ObserveOutcomeFor(key, started, createErr)
	return createErr
}
