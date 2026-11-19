# TRIPLEWISE C — Patterns and the next-round order

The eight cross-window failure shapes are in
`docs/audits/pairwise-c-autonomous-redteam-2026-11-19/PAIRWISE_C_PATTERNS.md` (P1–P8). This round adds two
that are specific to *three*-system interactions, both derived from the defects this effort actually
fixed, not from imagination.

## P9 — A marker that outlives its owner

**Shape.** System A tears a resource down. System B holds a *marker* about that resource (a registration,
a generation number, a "seen" bit) and answers a question about it afterwards. The marker's answer is
still *technically true* ("this is not current") and therefore passes every check, but its meaning has
changed: the resource is gone, not old.

**Instances.** `runtimecoord.Registration.Stale()` returns true for a **closed** registration forever and
`Acknowledge` refuses to clear it, so a pool closed by a fallback was read as "a pool from a previous
network" and paid one handshake per stream (pairwise B, B-2); the same marker, surviving `Close`, made a
late dial install a generation into a released pool (triple A, C-T2); a QUIC carrier without a shut-down
state installed a client nobody owned (pairwise A, A-3).

**The question for a diff.** *Which markers survive their owner, and what does the code's question mean
once the owner is gone? If the answer to a stale check can be "true because destroyed", the caller needs
a second question — "closed?" — before it acts.*

**Where to look next.** Every `atomic.Bool` / `sync.Once` / registration that a `Close` does not clear;
`transport/v2rayquic`'s publish site is the recorded outstanding member of the same family (it is a
narrow race rather than a closed marker, but the reasoning crosses).

## P10 — Three clocks, one constraint

**Shape.** The process runs several monotone clocks (config revision, network generation, pressure
episode), each correct on its own, and a resource's state is keyed to one of them while a decision that
depends on another is made from the first. The bug appears only when two clocks move in an order the code
never enforced.

**Instances.** The fake-IP cursor (config clock) restarting while the issuance ledger (process clock) still
remembers (pairwise C, C-T3); the pressure plan publishing against the generation in the same transition
(triple A's T2, where the two are provably independent and therefore coordinated); the DNS environment
generation vs an in-flight answer (whose test, `TestRemoteTransportInFlightAnswerIsNotStoredOnTheNewNetwork`,
is the model for how this pattern should be pinned).

**The question for a diff.** *For every state a decision reads, which clock does it belong to, and is
there a constraint between that clock and the one the decision is executing under? If not, the ordering
must be enforced somewhere — in a lock, or in a marker that carries both.*

## The next round's order (unchanged from pairwise C, still the right order)

1. **Health probe × selector × recovery state** (`protocol/group`, `common/urltest`). Largest untouched
   family; real queue; a user-visible failure mode (a dead node selected for a long time).
2. **Reload atomicity** (`daemon/started_service.go`, rule-set artifacts). R-B9 documents the ordering; a
   build-then-swap with a capture window is the design question.
3. **P9 sweep**: every marker that survives its owner (start with `transport/v2rayquic`'s publish site).
4. **P1 sweep on non-outbound listeners**: inbounds and endpoints that are not gated, looking for the
   double-action shape fixed for outbounds in C-T1.
5. **P6 census as a CI gate**: fail when a package with tests is mentioned by no workflow. This effort
   found three defects behind that gap; a script would have caught them before a release.

## What this round would do differently

The two rounds that produced fixes (pairwise B, pairwise C) both started by *running a suite nobody runs*
or by *grep-ing for a marker's lifetime*. The rounds that produced only classification started by *reading
the brief's candidate list*. That is the meta-lesson, and it is worth writing in the knowledge base
instead of another catalogue: **start from a query over the tree, not from a topic list.**
