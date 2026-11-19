# TRIPLEWISE A — Matrix

Window A of the three-system round: which **third** mechanism really participates in a causal chain,
what was executed, and what the evidence shows. The rule this file is written against: a third system
counts only if it changes the decision, the ownership, the ordering or the recovery — not if it merely
runs alongside.

Base: `testing` @ `413e115b3`, plus this round's commits.

## 1. The two triples that were actually attacked

| Row | The three systems | Shared object / state | Why the third is not optional | Status |
| --- | --- | --- | --- | --- |
| **T1** | ① `v2rayhttp.Client.Close` (Box teardown / reload) ② the **network-generation stale predicate** (a closed `Registration` reports stale forever) ③ `GenerationPool`'s generation ledger | one `http2.Transport` and every physical connection it owns | without ②, a post-Close dial simply fails; with ② it takes the *retire* path and **installs** a generation into a pool that released everything | **CONFIRMED bug → fixed** |
| **T2** | ① `service/oomkiller` (pressure fact) ② `common/resourceguard` (admission) ③ network generation + `GenerationGate` (transition) | the process pressure atomic, the admission plan, and the epoch a dial belongs to | admission's plan and the transition run on different clocks; a generation change during Critical could in principle reopen admission or double the destructive action | **NO DEFECT — coordination proven** (§3) |

The other rows of the brief's list are classified in §4 with the reason each could not be attacked
here; none of them is claimed as passed.

## 2. T1 — the 2³ control matrix

The three binary factors: **A** = a Close happened; **B** = a late dial arrives afterwards; **C** = the
pool has a closed state (the fix). "Same workload" means the same local TLS+HTTP/2 server and the same
one-request dial.

| Cell | A Close | B late dial | C pool closed-state | Observed | Where |
| --- | --- | --- | --- | --- | --- |
| ∅ | no | no | — | a warm pool reuses one connection (the baseline every other cell is compared with) | `generation_test.go` (existing), `publish_decision_test.go` |
| A | yes | no | — | `CloseAll` releases the current generation; the ledger reports no live generation | `reset_test.go` (existing close path), asserted inside `TestALateDialAfterCloseInstallsNoGeneration` before the dial |
| B | no | yes | — | the dial is served from the warm pool: no new transport, no new connection | `TestThePublishDecisionIsAPlainAcknowledgementWithoutANetworkChange` |
| C | no | no | yes | `Retire` on a live pool rotates as designed | `TestResetKeepsThePoolReusableAcrossAGenerationChange` |
| **A+B** | yes | yes | **no** | **DEFECT: a new transport is installed into a closed pool and nothing will ever release it** (`created = 1`) | the pre-fix run of `TestALateDialAfterCloseInstallsNoGeneration`, and the pool-level cell below |
| A+C | yes | no | yes | `Retire` offered to a closed pool is **released**, not adopted (`sequence` unchanged, `terminated + 1`) | `TestARetireOnAClosedPoolReleasesWhatItIsGiven` |
| B+C | no | yes | yes | the backstop alone releases the offered transport, but a transport is still built only to be released — the client-side guard is what removes that | reasoned from the same two tests; the client guard is the only thing that changes this cell |
| **A+B+C** | yes | yes | **yes** | no transport is built (`created = 0`), nothing is current, nothing is draining | `TestALateDialAfterCloseInstallsNoGeneration` |

Note the honest shape of this table: the cells that *changed* with the fix are A+B (the defect), B+C
(the wasteful middle) and A+B+C (the fixed behaviour). A alone, B alone and C alone are exactly the
paths the pairwise windows had already proven, which is why this is a genuine triple and not a pairwise
case renamed: the failing sequence needs the stale predicate (②) to *survive* the Close (①), and only
the pool's ledger (③) can turn it into a leak.

## 3. T2 — the coordination evidence (no defect)

The claim "OOMKiller × admission × generation are coordinated" needs three facts, and each has a test
or a source location rather than an argument:

| Fact | Evidence |
| --- | --- |
| The pressure state has ONE writer | `grep 'pressure.Store('` over non-test code returns three sites, all in `service/oomkiller` (`service.go:461` on stop, `timer.go:539` on a poll's state, `timer.go:1171` on the critical pass). No other package writes it |
| Admission reads that same writer, and nothing else | `Service.guardLevel()` (`service.go:120-129`) is the only mapping to `resourceguard.Level`, and it reads `s.pressure.Load()`; the TUN stack reads the same atomic through `MemoryPressure()` (`:436-438`) |
| The generation path never touches the pressure state | the transition machinery (`route/network.go`) publishes an epoch through `runtimecoord.Coordinator.Advance` and calls the listeners; neither it nor `GenerationGate` reads or writes `Service.pressure` |
| A published Critical really stops a real creation, and recovers | `service/oomkiller/guard_reaches_creation_test.go` (window A): the production TLS dialer is refused **before** the TCP creation, and admitted again after the pressure clears; mutation control MC-1 (break the mapping) turns it red |
| A generation transition runs the destructive action once, whichever side gets there first | `generation_gate_test.go` plus, after window C, the six owners' `TestInterfaceUpdatedRoutesThroughTheGenerationGate` |

So the third system's participation in T2 is **temporal, not stateful**: a network change cannot change
what the pressure says, so it cannot reopen admission; what it can do is reset the pool *at the same
time* as admission tightens, and the two orderings that matter (transition-then-admit, admit-then-
transition) were each executed. That is why the row's verdict is "no defect" rather than "not tested" —
but it is also *not* a claim that every 2³ cell was executed; the missing cells are listed in §4.

## 4. The rest of the brief's list, classified

| Row | Verdict | Reason (no invented work) |
| --- | --- | --- |
| A01 | `NO DEFECT` (T2) | see §3; the axis where a defect could hide (a second pressure writer) does not exist |
| A02 iOS NE budget × H3 speed test × TrustTunnel | `NOT_OBSERVABLE` | no device, no NE process; Go/Linux numbers are not `phys_footprint` and no device claim is made |
| A03 TUN UDP NAT × UoT reuse × gate | `CONFIRMED (recorded, cross-repo)` | A's R-4/R-5: the admission gate is not on the UDP NAT path at all (so the third system is outside the decision), and eviction does not cancel the dial it launched. The fix belongs to the `sing-tun` fork **plus a pin bump whose evidence is a CI run** — a release-freeze constraint, not a code one |
| A04 pool borrow × reload × cancel | `NOT_COVERED` | a genuine reload is *close-then-build* (`daemon/started_service.go:642-662`), so there is no window in which a new owner borrows from an old pool; the interesting version of this triple needs the build-then-swap redesign recorded as R-B9 |
| A05 admission × reuse × retry/fallback | `NO DEFECT (partial)` | the naive native seam charges one permit per physical socket creation and reuses the pooled H2 session without one; the H3→H2 fallback's extra probe is B's R-B4 (recorded) |
| A06 reclaim × trim/retire × shutdown | `PARTIAL` | `reclaim_overlap_test.go` serialises trim against the crisis pass; T1 is the part of this triple that was *not* covered and is now fixed |
| A07 queue × fairness × deadline | `NO DEFECT` | window A's A06 pair + the recovery-edge fix; no third mechanism was found that changes it |
| A08 XHTTP body × buffer × pool close | `NOT_COVERED` | needs a half-closed/bodiless-EOF server fixture; A's F-4 already covered the pool-side lifecycle |
| A09 Cronet native × Go owner × NE pressure | `NOT_OBSERVABLE` | the native side needs the Darwin/iOS build and a device |
| A10 NAT timeout × network change × OOM | `NOT_COVERED` | depends on A03's dependency fix first |
| A11 process budget × per-protocol limits × burst | `PARTIAL` | A's contract tests + B's per-protocol evidence; the multi-protocol burst needs a harness this window did not build |
| A12 diagnostics × pressure × shutdown | `PARTIAL` | A's R-8 records the one interaction found (the report path can complete a transition); profiling cost needs a device |
| A13 Mieru SDK × reset × reclaim | `NOT_COVERED` | no runnable SDK harness at the current pin |
| A14 GC/native heap × cache × platform budget | `NOT_OBSERVABLE` | measurement-class limit; no device |

## 5. What this matrix does not say

- It does not claim the 2³ matrix for T2 was executed in all eight cells; the two orderings that matter
  were, and the state axis was proven to have a single writer.
- It does not claim any of the `NOT_COVERED` rows is safe. They are simply not attacked.
- It does not treat the recorded cross-repo item (A03) as fixed. It is the one row where a real
  amplification exists and no in-scope fix was possible.
