# TRIPLEWISE A — Findings

One three-system defect was confirmed and fixed in this round. One triple was attacked and shown to be
coordinated (no defect). The rest of the brief's list is classified in `TRIPLEWISE_A_MATRIX.md` §4 with
the reason each could not be attacked, and nothing there is claimed as passed.

---

## T1〔CONFIRMED → FIXED〕A late dial after teardown installs a generation into a pool that has already released everything

**The three systems, and why the third is not optional.**

| | System | What it owns here |
| --- | --- | --- |
| ① | `Client.Close` — the teardown path (Box close, reload, outbound close) | releases the pool and unregisters the network-generation registration |
| ② | the **network-generation stale predicate** — `runtimecoord.Registration.Stale()` | answers "does this pool belong to an older network"; it reports a **closed** registration as stale **forever** (`coordinator.go:544-546`), and `Acknowledge` refuses to clear it (`:564-567`) |
| ③ | `GenerationPool` — the generation ledger | the only object that can release a transport it is handed |

Without ②, a dial after Close simply fails, which is the correct outcome. With ②, the same dial takes
the *retire* path in `dialHTTP2` and **installs a fresh `http2.Transport`** as the pool's current
generation — into a pool whose `CloseAll` has already run and which nothing will ever ask again. The
transport and every physical connection it opens then belong to a client that is gone.

**Trigger.** Any dial that reaches `v2rayhttp.Client` (or the gRPC-lite client, which shares the same
pool) after its `Close`: a request accepted just before an inbound listener closed, a dispatch queued
behind `ConnectionManager.CloseAll`, or a retry that wakes after teardown.

**Why the two pairwise cases were safe, and the triple was not.**

- Close alone: `CloseAll` releases current + draining; the ledger is consistent (cell A).
- Late dial alone: the pool is warm and the dial is served from the existing generation (cell B).
- Close **then** late dial: the stale predicate (②) is what routes the second one into `Retire`, and the
  ledger (③) had no closed state to refuse it (cell A+B — the defect).

**Measured before the fix.**
`TestALateDialAfterCloseInstallsNoGeneration` failed with
`a dial after Close installed a new transport into a pool that has already released everything`;
`TestARetireOnAClosedPoolReleasesWhatItIsGiven` failed with `expected 0x1, actual 0x2` — the pool
**adopted** the offered generation (its sequence advanced) instead of releasing it.

**Fix.**

1. `GenerationPool` gains a `closed` flag, set by `CloseAll`. `Retire` on a closed pool **releases** the
   transport it is handed (counted once in `terminated`) and does not advance the sequence — a closed
   pool has released everything it owned and will never be asked again, so this is the last owner that
   can release a late offer.
2. `Client` (v2rayhttp and gRPC-lite) gains a `closed` flag set at the top of `Close`, and
   `retireGeneration` returns early when it is set — so a late dial does not even build a transport to
   release. The pool's refusal remains the backstop for a caller that races `Close` by an instruction.

**The fix follows the codebase's own convention.** `transport/v2rayxhttp`'s xmux manager already has
exactly this state: `m.closed` is set by `Close`, `get()` refuses to open anything afterwards, and
`getContext` turns the empty result into `"connection pool is closed"` instead of a timeout
(`xmux.go:353-356,746-752`). The generation pool was the one pool without it.

**Regression and its power.**

| Test | What it observes |
| --- | --- |
| `TestALateDialAfterCloseInstallsNoGeneration` | a counting `newTransport` factory: a dial after Close must build **zero** transports; then the ledger must report no current and no draining generation once the late conn has settled |
| `TestARetireOnAClosedPoolReleasesWhatItIsGiven` | the pool's own counters: `sequence` unchanged and `terminated` incremented exactly once for an offered transport |

**Mutation controls.** Both pre-fix runs are the control for the fix as a whole. In addition, disabling
only the pool's `closed` branch (keeping the client guard) turns
`TestARetireOnAClosedPoolReleasesWhatItIsGiven` red with `expected 0x1, actual 0x2`, which proves the
pool-level backstop is load-bearing rather than dead code next to the client's flag.

**Severity and release position.** Bounded: one leaked transport per late dial (normally zero or one),
and the connections it opens are closed only when it is later garbage-collected. It is not a crash and
not a data leak; it becomes a *slow* resource growth across a long-lived daemon that reloads often. It
was fixed in this round because the fix is eight lines and matches an existing state in a sibling
transport.

**Commits.** `fix(v2rayhttp): release a generation offered to a closed pool` (both clients and the
shared pool, plus the two tests).

---

## T2〔NO DEFECT〕OOMKiller × admission × network generation

**The triple was attacked at the one axis where a defect could hide: a second authority over the pressure
fact.**

`grep 'pressure.Store('` over non-test code returns exactly three sites, all inside `service/oomkiller`
(`service.go:461`, `timer.go:539`, `timer.go:1171`). The only admission mapping is
`Service.guardLevel()` (`service.go:120-129`), reading that same atomic; the TUN stack reads it through
`MemoryPressure()`. The network-generation machinery never reads or writes it — `Coordinator.Advance`
publishes an epoch and calls listeners, and `GenerationGate` deals only in epochs.

So a network change cannot flip or reopen the pressure level by itself, and a Critical published during
a transition still stops a real creation before it allocates (window A's
`TestPublishedCriticalPressureStopsARealExpensiveCreation`, with its mutation control), while the
transition's destructive action runs once per generation whichever side reaches it first (window C's six
`TestInterfaceUpdatedRoutesThroughTheGenerationGate`, and the gate's own suite).

What remains unexecuted in this triple is the *cross-product*: Critical × transition × a real pool
rotation, in one test. It is recorded as a gap rather than claimed as covered, because building it needs
a transport whose pool the test can rotate deterministically and a pressure source the test can hold
mid-transition — a harness worth building next round, not a thing to approximate with a `sleep`.

---

## Not fixed, and why (cross-referenced, not repeated)

The one genuine three-layer amplification in the current tree is **TUN UDP NAT × UoT session reuse ×
admission** (window A's R-4/R-5): the admission gate is not on the UDP NAT path at all, and evicting a
NAT entry does not cancel the dial it launched, so the 4096-entry table is the only bound on the real
per-flow cost. Its fix is in the `sing-tun` fork **and** a pin bump whose release gate demands a CI run
for the new revision — which cannot be produced from here during a release freeze. Recorded, with the
deterministic test sketches, in `docs/audits/pairwise-a-resource-lifecycle-2026-11-19/PAIRWISE_A_FINDINGS.md`
(R-4…R-7).
