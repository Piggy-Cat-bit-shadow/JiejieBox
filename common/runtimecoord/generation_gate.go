package runtimecoord

import "context"

// GenerationGate applies a resource's network-change action at its hand-out when the coordinator
// has published a new generation but the listener phase has not run yet.
//
// # The window it closes
//
// A physical network change is published by advancing the coordinator's epoch, and the listeners
// that reset transports run afterwards under the network manager's deadline. A demand that arrives
// between the two used to be handed a session built for the previous network: the transport still
// looked healthy, so nothing else refused it. The audit calls this the transition window (Q12/F-15)
// and requires that a new generation is not lent an invalidated session.
//
// The gate is the smallest mechanism that covers it without inventing a second generation clock:
//
//   - BeforeDial checks the resource's registration. If the generation this resource was built
//     against is no longer current, it runs the SAME action the listener would have run, at the
//     hand-out. The action is supplied by the owner (close the client, retire the pool, restart the
//     session); the gate does not decide policy.
//   - AfterDial acknowledges only a successful creation, which is the point where the resource is
//     demonstrably usable on the current network. A failed creation leaves the gate stale, so the
//     next attempt repeats the owner's action instead of trusting a generation that never produced
//     a connection.
//
// # What it deliberately does not do
//
// It does not close anything by itself, and it does not replace the listener: the listener still
// owns the transition and runs even when no dial arrives. The gate only makes the FIRST dial in the
// window pay the transition cost instead of borrowing the old generation. It also does not invent a
// coordinator: without one, NewGenerationGate returns nil and every call is a no-op, which is the
// previous behaviour for embedders and tests.
type GenerationGate struct {
	registration *Registration
	remove       func()
	reset        func(context.Context)
}

// NewGenerationGate registers a resource with the coordinator and returns its hand-out gate.
//
// reset is the owner's network-change action, normally the same method the listener calls. It is
// run only when the registration reports a stale generation, and it must be safe to run more than
// once and concurrently with a dial: the listener may run at any moment after the epoch advances.
//
// A nil coordinator or a nil reset yields a nil gate, and a nil gate is a no-op on every method.
func NewGenerationGate(coordinator *Coordinator, label string, reset func(context.Context)) *GenerationGate {
	if coordinator == nil || reset == nil {
		return nil
	}
	registration, remove := coordinator.Register(label)
	return &GenerationGate{
		registration: registration,
		remove:       remove,
		reset:        reset,
	}
}

// BeforeDial runs the owner's network-change action if the current generation has moved past the
// one this resource was built against. It is safe on a nil gate.
func (g *GenerationGate) BeforeDial(ctx context.Context) {
	if g == nil || g.registration == nil {
		return
	}
	if g.registration.Stale() {
		g.reset(ctx)
	}
}

// AfterDial acknowledges a successful creation: the resource has proved it can produce a connection
// on the current generation. A nil error is the only acknowledgement; a failure keeps the gate
// stale so the next attempt retries the transition rather than trusting the old generation.
func (g *GenerationGate) AfterDial(err error) {
	if g == nil || g.registration == nil || err != nil {
		return
	}
	g.registration.Acknowledge()
}

// Stale reports whether the resource still belongs to a previous generation. Diagnostics and tests.
func (g *GenerationGate) Stale() bool {
	if g == nil || g.registration == nil {
		return false
	}
	return g.registration.Stale()
}

// Close unregisters the gate from the coordinator. It is safe on a nil gate and idempotent.
func (g *GenerationGate) Close() {
	if g == nil {
		return
	}
	if g.remove != nil {
		g.remove()
		g.remove = nil
	}
}
