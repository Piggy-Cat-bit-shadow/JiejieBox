# Candidate Admission + Flow Lifecycle — Phase 02 construction report

**Round:** `02 / 04`, Candidate Admission + Flow Lifecycle Construction.
**Branch:** `testing` (worked in place; no feature, debug, temp branch or worktree).
**Start SHA:** `c449256be39583bd86dc6e2070ed808021ad9e69` — the Phase 01 HEAD, verified clean and `0/0`
against `origin/testing` before any edit.
**Final SHA:** the `HEAD` of `origin/testing` immediately after the commit that adds this file. It is not
written here for the reason given in Phase 01 §A: a commit cannot carry its own hash without being
rewritten, and a rewritten hash is worse than a pointer to `git log -1`.

**Evidence levels** follow `KNOWLEDGE/README.md`:

| label | meaning |
|---|---|
| `VERIFIED_SOURCE` | read in the tree at the final SHA, with the file/function named |
| `VERIFIED_UNIT` | a `go test` run on this host passed and the named test is what asserts it |
| `VERIFIED_MUTATION` | a deliberate source defect was introduced, the suite was run, and the named test failed |
| `BLOCKED_BY_ENVIRONMENT` | could not run here; what it would have proved is stated |

**Not `VERIFIED_CI`.** No workflow-run record exists in this tree. **Device behaviour unverified:** no TUN,
no NetworkExtension, no real handover. **`-race` is `BLOCKED_BY_ENVIRONMENT`** (`CGO_ENABLED=0`, no C
compiler); the functional equivalents ran without it and passed, listed in §S.

---

## A. Branch / start SHA / final SHA

| | |
|---|---|
| branch | `testing` |
| start SHA | `c449256be39583bd86dc6e2070ed808021ad9e69` |
| commits | see §W |
| worktree | **clean** at the start and at every commit; `clients/desktop` was synced during the Phase 01 close-out and stays on the pin `HEAD` declares |

---

## B. 原判定 + 生命周期架构图

Read from the tree, not from Phase 01's prose. Two shapes reach the same scheduler; the numbers are the
capture points **before** this round.

```text
route.ConnectionManager
  NewConnection / NewPacketConnection
    ├─ dialSetups.begin(ctx)        setup context for the CURRENT transition; ownership transfers at release
    ├─ (connected packet / stream)  dialer.DialSerialNetwork / DialSerialNetwork(...)
    │     └─ ParallelNetworkDialer? dispatch by capability, never by a typed assertion on a narrow interface
    │           │
    │           ├─ failover shim  → context-carried dial options → member's own parallel surface
    │           │
    │           └─ resolveParallelNetworkDialer.DialParallelNetwork      [CAPTURE ①]
    │                 ├─ withoutFamilyExcluded(list, strategy)             read strategy
    │                 ├─ recoverCandidates(ctx, dest)  ──goroutine──┐      [CAPTURE ②]
    │                 │                                            │      read capability
    │                 ├─ mergeRecovered(dest, recovered) ───────────┘      [CAPTURE ③]
    │                 ├─ schedule(...)                                     read capability + strategy
    │                 │     ├─ plannableOriginal = ... strategyAllows(original)
    │                 │     ├─ planCandidates(...)
    │                 │     ├─ scheduler = owner.newDualStackScheduler(delay)  [CAPTURE ④]
    │                 │     │        └─ networkGeneration + localFamily from that capture
    │                 │     └─ dialWithLateCandidatesAdmitting(..., d.lateCandidateAdmission())  strategy read
    │                 └─ feedRecoveryAsLateCandidates → recoverCandidates      [CAPTURE ⑤]
    │
    └─ (plain resolve shape) resolveDialer.DialContext                    [CAPTURE ①]
          ├─ literal → dialLiteralWithRecovery                             [CAPTURE ②]
          │     ├─ original admission: strategy + capability
          │     ├─ recoverCandidates  ──goroutine──► admittedRecoveredCandidates   [CAPTURE ③]
          │     ├─ dialRecoveredOrReport → MergeOriginalDestination + raceCandidatesExcluding
          │     │        └─ newScheduler → owner.newDualStackScheduler     [CAPTURE ④]
          │     └─ raceWithPendingOriginal → raceCandidatesExcluding        [CAPTURE ⑤]
          ├─ hostname → raceResolvedName → plan + scheduler                 [CAPTURE ② ③]
          └─ serial   → withoutFamilyExcluded + withoutUndialable           [CAPTURE ②]
      → DefaultDialer.DialContext                                          [CAPTURE per socket]
          gate: localFamily.hardUndialableError   +   hand-over: the SAME capture's ownership predicate
      → established → release()  → track: state.generation = ConnectionManager.generation
```

**The finding, stated as the graph shows it:** one literal flow could take **five** readings of the
network, and each was internally consistent — a `localFamilyPolicy` always carried its own capability and
its own ownership predicate. Nothing failed. What could differ was **which** reading a given stage used, so
the family recovery had just refused could be re-admitted by a later capture, and the scheduler could
compute its generation from a different instant than the planner.

| stage | what it reads | owns? | cancels? | re-reads the world? |
|---|---|---|---|---|
| `ConnectionManager.NewConnection` | metadata, dialer | the flow | caller cancel, transition | no |
| `dialSetups.begin` | the current generation's context | — | — | no |
| recovery producer | the flow's context | the lookup | setup cancel | **yes, before this round** |
| planner | addresses + strategy | — | — | **yes** |
| scheduler | capability + generation | the race | winner / cancel | **its own capture, before** |
| leaf `DefaultDialer` | capability + ownership | the socket | the socket's owner | yes — **one per socket, deliberately** |
| `TrackConn` | `ConnectionManager.generation` | the connection | — | no (stamped once) |

---

## C. 新的 Candidate Admission Policy

`common/dialer/admission.go`. Two terms and nothing else:

```go
type admissionPolicy struct {
	strategy    C.DomainStrategy   // Layer 0: configuration, survives every generation, memoised per dialer
	localFamily localFamilyPolicy  // Layer 1: Phase 01's published snapshot WITH its ownership
}
```

| surface | meaning |
|---|---|
| `generation() uint64` | the capability's own ownership epoch, so a policy and the scheduler and the health namespace that come out of it cannot disagree |
| `strategyAllows(addr)` | the operator's policy alone (a compatibility term, kept for the call sites that must NOT apply the capability) |
| `admits(addr)` | **both** terms — the single predicate the stage-specific gates ask |
| `admitCandidates([]addr)` | the whole policy applied to a list, reporting the three outcomes a caller has to distinguish |
| `lateAdmission() func(addr) bool` | the strategy guard for late candidates; `nil` when nothing can be excluded, so the common path pays nothing |
| `verdict(addr)` / `refusalError(addr)` | the decision and the explanation from ONE place |
| `emptiedListRefusal(...)` | the list-level rendering: was it the operator's policy, the machine, or nothing at all |
| `restrictsNothing()` | the fast-path answer, which must never disagree with `admits` |

**What it deliberately does not do** (§8): no DNS, no sniff, no Fake-IP, no Happy Eyeballs, no timer, no
goroutine, no mutable state, no socket, no logging, no reclaim, no connection tracking. It is a value.

**The two terms stay separate on purpose**, and the four verdicts say why:

| verdict | remedy |
|---|---|
| `admissionStrategyExcluded` | a configuration decision the operator can change |
| `admissionNoLocalFamily` | the machine has no addressing of that family |
| `admissionNoGlobalEgress` | the machine HAS IPv6 addressing and nothing routes it out — the **route** is the remedy, not the address |
| `admissionAllowed` | — |

`VERIFIED_UNIT`: `TestZeroAdmissionPolicyAdmitsEverything`, `TestAdmissionSeparatesTheFourVerdicts`,
`TestAdmitCandidatesReportsTheThreeOutcomes`, `TestAdmissionPolicyGenerationIsTheCapabilityOwnership`,
`TestAdmissionFastPathNeverDisagreesWithTheVerdict` (the whole space: 251 capability/ownership
combinations × 5 strategies × 14 destination scope classes — 17 570 pairs, because a table of examples
cannot catch a composition that is wrong for a state it does not list).

---

## D. Policy capture 点

**One capture per flow entry, at the earliest point that has BOTH terms.** §5 asks which layer that is;
the answer came out of the object graph rather than from taste:

| entry point | captures at | why there |
|---|---|---|
| `resolveDialer.DialContext` | top, after `initialize()` | strategy (dialer configuration) and capability (chain walk to the owner) are both available; everything below is a function of them |
| `resolveDialer.ListenPacket` | top, before either arm | both arms need it, and the literal arm used to read the strategy separately |
| `resolveParallelNetworkDialer.DialParallelNetwork` | top | the network entry point; one policy for the pre-filter, recovery, the merge, the plan and the scheduler |
| `…ListenSerialNetworkPacket` | top | same |
| `…DialParallelInterface` | top | same; it is also the only place `preferIPv6` can come from the effective strategy |
| `…ListenSerialInterfacePacket` | top | same |
| `DefaultDialer.DialContext` / `ListenPacket` / `ListenSerialInterfacePacket` | top | the leaf's own socket gate; **one per socket, deliberately** (§R) |

**Why not earlier** — `dialer.DialSerialNetwork` and the free `DialParallelNetwork` sit BELOW the layer
that knows the strategy: they receive `*C.NetworkStrategy` (interface selection) and a `preferIPv6 bool`,
neither of which is the operator's family policy. Putting the capture there would require inventing a
strategy, which is exactly what §5 forbids. The free `DialParallelNetwork` therefore **takes** the policy
as a parameter rather than computing one.

**Why not later** — recovery runs in a goroutine on its own schedule, and the scheduler is built per
attempt; a capture at either would be a second reading.

`VERIFIED_SOURCE` for the table. `VERIFIED_MUTATION` that the parameter is actually used: handing the
scheduler an empty policy fails `TestALocalFamilyFastFailIsAppliedOnEveryPath` and
`TestRecoveryMatrixIsFollowedByTheTransport`; re-capturing inside `raceCandidatesExcluding` fails
`TestAResolvedNameIsAdmittedByTheFlowsOwnCapture`.

---

## E. Policy lifetime

```text
setup alive      → policy snapshot valid, read-only
network transition → the lifecycle owner cancels the stale SETUP
                  → the flow ends or retries
                  → the next flow captures a new policy
```

- No goroutine, no ticker, no listener, no self-refresh, no `Admit()` that consults the manager to
  re-derive the verdict (§6). The one manager read a *check* performs is the ownership predicate, which
  **validates** the capture rather than replacing it — see §F and the note in §U.5.
- The policy is passed **by value** down every call; nothing stores it in a long-lived struct except the
  scheduler, whose whole lifetime is one race.
- `VERIFIED_UNIT`: `TestTheSchedulerUsesTheFlowsCapturedVerdict`,
  `TestRecoveryUsesTheFlowsCapturedVerdict`, `TestAPacketFlowUsesItsCapturedVerdict` — each uses a manager
  whose answer CHANGES after the capture, so a stage that refreshed would be visible. This is the
  measurement Phase 01 could not make (its own §U.3 residual risk was exactly "the producer's freshness is
  unobservable from the consumer side"); here it is observable, because the consumer is what is being
  pinned.

---

## F. Generation ownership

**Two counters exist. They are not the same number, they are never compared, and each is internally
atomic.** `VERIFIED_SOURCE`, and the most important finding of the phase that is NOT a defect to fix.

| counter | advanced where | used by |
|---|---|---|
| `NetworkManager.networkResetGeneration` — the **network epoch** | `beginTransition()`, once per claim (`network.go:997`) | the capability snapshot's ownership; `familyHealth`'s namespace; the dialer's hand-over predicate; `runtimeCoordinator.Advance` |
| `ConnectionManager.generation` — the **transition counter** (route) | `dialSetups.advanceAndBump(bump)`, and only inside a **draining** `Reclaim(ReclaimNetworkTransition)` (`conn_reclaim.go:281`) | the managed connection's `state.generation` stamp; the drain scan; the sweep; the `dialSetupGate` context swap |

Four consequences, all `VERIFIED_SOURCE`:

1. **They are not equal and do not have a fixed difference.** The route counter does not advance on
   `ReclaimDeadPath` or `CloseAll`; the network epoch advances on every claim.
2. **Nothing compares them.** A repo-wide search finds no expression that mixes them. So there is no
   split-brain today — the split is *latent*, not live.
3. **§11 is therefore satisfied per subsystem, not globally.** `policy` / `scheduler` / `familyHealth` all
   come from the network epoch and provably from ONE capture (`TestTheFlowsPolicyAndItsSchedulerShareOne
   Generation`). `setup gate` / `connection stamp` come from the route counter, and their atomicity is
   `advanceAndBump`'s (`TestATransitionNeverPublishesItsCounterBeforeItsGate`,
   `TestATransitionCancelsSetupsItBeganAfter`).
4. **The risk is for the next round**: any future check of the form "the setup's generation equals the
   policy's generation" must pick one counter and derive the other, not compare them.

**The ownership predicate re-reads the snapshot on every check, and that is not a second capture.** It
must: detecting a superseded generation requires a fresh look, and answering from the captured value would
make invariant B unenforceable. What the predicate does with the fresh reading is *validate ownership*
only — the capability it also happens to return is discarded. `VERIFIED_SOURCE`,
`network_observation.go:190-217`.

---

## G. Setup lifecycle

Unchanged, and the boundary is preserved as required (§3): `dialSetupGate` still owns **setup only**.

```text
caller cancel            → cancels setup
network transition       → cancels every setup that derives from the stale generation
manager close            → cancels all in-flight setup, and a later begin gets a dead context
successfully established → release() detaches the setup from the generation, then releases it
```

`advanceAndBump(func(){ generation.Add(1) })` keeps the route counter and the context swap in ONE critical
section. `VERIFIED_MUTATION`: publishing the counter **before** the gate fails
`TestATransitionNeverPublishesItsCounterBeforeItsGate`; cancelling setups without the swap fails
`TestATransitionCancelsSetupsItBeganAfter`.

**One correction to a claim in this area** — the release function's comment said stopping the generation
watch prevents a transition from "cancelling a connection that already exists". It does not: the returned
connection is not derived from the setup context, and `cancelSetup` runs on the next line either way.
`VERIFIED_MUTATION`: `_ = stopGenerationWatch` leaves the entire suite green, including
`TestNetworkTransitionDoesNotKillAnEstablishedFlow`. The line is still right — it stops the watch from
outliving its owner, which bounds `AfterFunc` registrations to the dials in flight — and the comment now
says that. See §U.6 and commit `c15196cd7ba`.

---

## H. Ownership transfer

**Setup ownership transfers at establishment and the policy does not travel with it.** §12's boundary is
intact:

```text
Admission Policy       decides how a NEW connection is dialled
Setup Lifecycle        decides when an UNESTABLISHED attempt is cancelled
Established Connection exists independently once established
Reclaim                decides when an OLD connection drains or closes
```

`VERIFIED_SOURCE`: the connection returned by a dial is not derived from the setup context, so nothing in
the setup path can reach it; `Reclaim` is the only thing that can end it, and it drains rather than closes
on a transition (Phase 01 §Q, `TestConfirmedOfflineIsReclaimedAsADeadPath` /
`TestAGapThatHasNotHeldIsStillADrain`).

**A capability change never calls Reclaim at all** — it publishes a value and does nothing else. Only a
transition reaches the connection manager.

---

## I. Recovery producer lifecycle

The producer is subordinate to the setup, and the two halves of that are both pinned:

| shape | mechanism | `VERIFIED_UNIT` |
|---|---|---|
| original wins | the caller's deferred `cancelRecovery` runs, then the producer is **joined** | `TestLiteralOriginalWinnerCancelsRecovery` |
| recovered wins | the race cancels the resolution and waits for both producers before returning | `TestRecoveryProducerStopsOnRequest` / `TestRecoveryProducerIsJoined` |
| setup cancelled | the setup context is a parent of the recovery context, so a transition cancels it | `TestNetworkTransitionCancelsAnInFlightBlackholedDial` |
| a late candidate arrives after the winner | the scheduler's late stream is closed and the producer has already exited | `TestOldVerdictCannotDenyNewGenerationSchedulerLateStream` |

The producer now also receives the flow's **policy** instead of capturing its own, so the family that ends
a lookup is the family the flow decided on. `VERIFIED_MUTATION`: cancelling without joining fails
`TestRecoveryProducerIsJoined`; dropping the strategy term from the recovery filter fails
`TestRecoveryRescuesALiteralTheStrategyExcludes` and
`TestPacketPathIsRescuedByRecoveryWhenTheStrategyExcludesTheLiteral`; dropping the capability term fails
`TestRecoverySkipsAnUndialableAnswerToReachAUsableOne` and
`TestRecoveryReportsNothingWhenEveryFamilyIsUndialable`.

---

## J. Scheduler lifecycle

The scheduler's worldview is fixed at birth, and every field that makes it up now comes from the flow's
one capture:

```go
func (d *DefaultDialer) newDualStackScheduler(delay time.Duration, admission admissionPolicy) *candidateScheduler {
	return &candidateScheduler{
		fallbackDelay:     delay,
		health:            d.familyHealth,
		networkGeneration: admission.generation(),   // health namespace
		localFamily:       admission.localFamily,     // the family gate
	}
}
```

- **It owns** the race: launch order, spacing, the late stream, the winner, the losers.
- **It does not own** the network: it never reads the manager, never re-derives the strategy, never
  refreshes its verdict. §10 and §22 hold.
- **It is business-ignorant**: no sniff, no Fake-IP, no DNS, no metadata. The only inputs are candidates,
  the policy, the generation, the health table and a dial function.
- **It still removes candidates itself.** That is the authoritative gate for a family the machine provably
  cannot dial, because it runs before a socket exists and nothing downstream can rescue a family it kept.

`VERIFIED_MUTATION`: taking the generation from a fresh read fails
`TestTheFlowsPolicyAndItsSchedulerShareOneGeneration`, `TestOnePlanCostsOneOwnershipRead` and
`TestTheHealthNamespaceComesFromTheCapture`; building the scheduler from an empty policy fails
`TestALocalFamilyFastFailIsAppliedOnEveryPath`; skipping the local gate fails
`TestUnavailableFamilyIsNeverAttempted` and `TestTheSchedulerUsesTheFlowsCapturedVerdict`.

---

## K. FamilyHealth generation

Keyed by the network reset epoch, and the key comes from the **same capture** as the verdict and the
ownership it accompanies. Phase 01 pinned the keying; this phase pinned that the key is the *flow's*
capture rather than a second read:

| property | mechanism | `VERIFIED_UNIT` / `MUTATION` |
|---|---|---|
| one source for policy, scheduler and health | `admission.generation()` feeds all three | `TestTheFlowsPolicyAndItsSchedulerShareOneGeneration` (unit + mutation M9) |
| no inheritance across a transition | epoch-keyed | `TestFamilyHealthIsScopedToTheNetworkGeneration` (Phase 01) |
| soft, never a refusal | the health table only decides whether both families start together | `TestUnavailableFamilyDoesNotDelayTheHealthyOne` |
| bounded | clear-at-cap at 256 generations | `TestTheFamilyHealthTableStaysBounded` |

Phase 02 did not change familyHealth's design. It changed *where its namespace comes from*, which is what
made it possible for §11 to be a property of the wiring rather than of three call sites staying in step.

---

## L. Established connection / Reclaim boundary

Untouched, and deliberately so (§12, §26):

```text
ReclaimNetworkTransition   drain: reclaim only what is provably idle (20 s grace), mark the rest
                           as belonging to the path the device has left, sweep as they fall silent
ReclaimDeadPath            close everything (a confirmed 2 s outage)
ReclaimIdle                proven-idle only
```

`VERIFIED_UNIT`: Phase 01's suite, plus `TestConfirmedOfflineIsReclaimedAsADeadPath` and
`TestAGapThatHasNotHeldIsStillADrain`. Nothing this phase changed reaches the connection manager: the
policy is a value the dial path reads, and a capability change does not call `Reclaim` at all.

---

## M. Wrapper / failover propagation

This is the area §15 calls the repeated bug family — *rich capability → narrow interface → silently lost*.
Three things were done and one boundary was recorded.

**1. The policy travels as a whole value.** It is a function parameter from the capture point down to the
scheduler and the leaf; no stage unpacks it into `strategy` + `localFamily` + `generation` and re-assembles.
The only place fields are taken individually is the scheduler's struct, because that file names them that
way and it is the last consumer.

**2. The rich surface is reached by walking, not by asserting.** `findFamilyHealthOwner` and
`dialerLocalFamilyPolicy` walk the chain through `Upstream()` / `DialChain()` to the ONE owner
(`*DefaultDialer`), and the unexported method rule means no other package can accidentally satisfy either
interface. Phase 01's fix for the two `daemon/started_service.go` chains is preserved.

**3. `DialParallelNetwork` no longer drops the verdict when there is no owner.** It used to fall back to a
bare scheduler, so a chain without a `familyHealthOwner` lost the machine's family capability on the one
racing entry point that had none. It now builds its fallback scheduler from the flow's policy and still
carries no health. `VERIFIED_MUTATION` M13.

**4. The failover boundary, recorded rather than changed.** `failoverDialer` passes the per-attempt dial
options through the **context** and dispatches to the member's own parallel surface, so a member is a
separate setup with its own `domain_strategy` and therefore its own capture. "One flow, one policy"
therefore binds **within one member's setup**, not across a group. That is correct — a member's
`domain_strategy` is part of its configuration, and carrying one member's policy to another would be the
narrowing this section is about — but it is a boundary, so it is stated. `VERIFIED_MUTATION` M14: making
the shim bypass the group and the options fails `TestTheFailoverShimDoesNotHideTheParallelDialSurface` and
`TestAPlainLeafStillDialsThroughTheFailoverGroup`.

---

## N. TCP / UDP / packet

Four packet arms, and this phase found that one of them applied **no policy at all**:

| arm | policy before | policy now |
|---|---|---|
| connected UDP (candidate list) | both terms, via `ListenSerialNetworkPacket` | unchanged, via `admitCandidates` |
| unconnected UDP, literal destination | the strategy term at the wrapper; the capability term at the leaf | unchanged (same two gates), one capture |
| **unconnected UDP, hostname destination** | **nothing** | both terms, `admitCandidates` + `emptiedListRefusal` |
| parallel interface packet path | both terms | unchanged, threaded |

**The hole, measured.** With `ipv4_only` and a name whose A answer was empty while AAAA answered, the
flow returned `err = nil` and dialled `2409:8c54:871:1007::2c` — a UDP socket for the family the operator
had forbidden, reported as a success. `route/conn.go:606` is the call site
(`this.ListenPacket(ctx, metadata.Destination)` for a domain with no `DestinationAddresses`), and
`resolveDialer.ListenPacket`'s domain branch went from `router.Lookup` straight to `N.ListenSerial`. No
test could see it because every strategy test was written for the stream path or for a candidate list.

**Why it is worse than the stream equivalent.** A stream dial can be rescued later — the plan is filtered,
the scheduler filters, the leaf re-checks. Here there is no scheduler and no race: `N.ListenSerial` walks
what it is given and the first address that opens a socket wins, so **the list handed to it IS the
decision**. `VERIFIED_MUTATION`: reverting only that arm makes
`TestPacketDomainPathAppliesTheStrategy` and `TestPacketDomainPathAppliesTheMachineVerdict` fail and the
control still pass.

**`connected` and `unconnected` share the policy but not the lifecycle**, and §19 says not to turn UDP into
the TCP scheduler race: connected UDP keeps its candidate-list dispatch, unconnected UDP keeps the serial
walk. The fix was the POLICY, not the lifecycle.

---

## O. Proxy boundary

Preserved, and it is structural rather than a rule someone remembers (§17):

```text
local machine IPv4-only
proxy endpoint         IPv4        ← this machine dials it, so the policy applies
final target           IPv6-only   ← the REMOTE peer reaches it, so the policy must not
```

- A detour never reaches `DefaultDialer.DialContext` — the detour chain hands the request to another
  outbound instead of dialling it — so a capability read cannot reach a remote target even in principle
  (`family_capability.go:59-68`).
- The proxy's OWN server address is dialled by this machine, so the policy is exactly the right question for
  it. That is the case that looks like an exception and is not.
- `VERIFIED_UNIT`: `proxy_endpoint_scope_test.go` (the wrapper answers `localFamilyPolicy` for the proxy
  endpoint and for the final target independently), `path_parity_matrix_test.go`
  (`TestALocalFamilyFastFailIsAppliedOnEveryPath`).

This phase did not widen the policy's scope; the per-flow value makes the existing scope harder to lose,
because there is exactly one set of terms to consult.

---

## P. Refusal model

Unified to the extent §18 requires, and no further:

| requirement | mechanism |
|---|---|
| strategy exclusion and local inability are separate | two distinct `admissionVerdict` values, and the strategy term is asked FIRST |
| "no IPv6 address" and "no global IPv6 route" are separate | `admissionNoLocalFamily` vs `admissionNoGlobalEgress` |
| `Unknown` produces no hard refusal | the zero capability refuses nothing, and `verdict` returns `admissionAllowed` |
| messages rendered at the boundary, no logger in the policy | `refusalError(address)` returns an error; the caller wraps it with the destination, the outbound and the flow shape |
| no heap object on the hot path for logging | `refusalError` allocates, so it is only called on the refusal path; the hot path asks `admits` or `verdict`, both allocation-free |

**What was joined:** the three-way list-level question ("was the list emptied by the operator's policy,
by the machine, or was there nothing to offer") had been written by hand at four arms of the package.
Two copies were correct; the arms added later were not, and the packet arm was missing the whole
decision. It is `admissionPolicy.admitCandidates` + `emptiedListRefusal` now, and the two original arms
were migrated onto it in the same commit as the fix, so there is one implementation rather than three.

**What was deliberately NOT unified:** the list-level *wording*. Three arms say "every candidate belongs to
the family this policy forbids" and the serializer says "every resolved address…". Those messages name
different flows, and a caller that reads them to decide where to look is being helped, not confused. The
per-address verdict — the part that must not diverge — IS unified.

---

## Q. 哪些判断被统一

| judgement | before | now |
|---|---|---|
| "may this flow dial this address" | composed at 5 call sites from 2 terms read at 2 instants | `admissionPolicy.admits` |
| "which family may this flow use" | `withoutFamilyExcluded` here, `hardUndialable` there, order depending on the arm | `admitCandidates`, one order, one place |
| "was the list emptied by the policy or by the machine" | hand-written 4 times, 2 of them wrong, 1 missing | `admitCandidates` + `emptiedListRefusal` |
| "why was this address refused" | assembled per call site from the strategy and the family | `verdict` + `refusalError` |
| "which generation does this verdict belong to" | the owner's capture for the scheduler, a separate read for the namespace | `admission.generation()` |
| "is the original address admissible" | `strategyAllows` at one site, `strategyAllows && !hardUndialable` at another | `admits` at both |
| the late-candidate strategy guard | `lateCandidateAdmission()`, a 4th copy of the strategy rule | `admission.lateAdmission()` (the method was deleted) |

**One thing was NOT unified and should not be:** `localFamilyPolicy` and `admissionPolicy` both exist.
The former is a *capability* fact with its ownership; the latter is a *flow's decision* built from it plus
the operator's configuration. Collapsing them would put a configuration term inside a value that Phase 01
publishes as a fact about the machine.

---

## R. 哪些 gate 故意保留

All five, for the reasons §13 gives. None is duplicate code to delete; each covers a way a candidate can
arrive that the others cannot see.

| gate | what only it covers | `VERIFIED_MUTATION` |
|---|---|---|
| initial filter (`withoutFamilyExcluded`, in `DialParallelNetwork` / `ListenSerialNetworkPacket`) | a strict strategy must remove an address BEFORE the planner, which expresses preference and has no strict mode | M5 |
| recovery admission (`admittedRecoveredCandidates`) | recovery's RESULT decides whether the caller races recovery at all; a list that looks non-empty but holds only undialable addresses sends the caller into a race that cannot connect | M1, M2 |
| late-candidate admission (`admission.lateAdmission()` + the scheduler's `hardUndialable`) | a late arrival never passes the planner, so it needs a structural guard | M3, M13 |
| scheduler local-family guard (`filterLocalFamily`) | runs before a socket exists; nothing downstream can rescue a family it kept | M4 |
| leaf socket gate (`DefaultDialer`) | a caller that arrives with one explicit address and no candidate list — every UDP packet connection and every direct entry | Phase 01: `TestUnavailableFamilyIsNeverAttempted`; and the packet-domain finding in §N shows what happens when an arm has none |

Two further gates are worth naming because they look redundant and are not:

- `plannableOriginal`'s `admission.admits` is a **third** gate over the original address, and
  `VERIFIED_MUTATION` M6 shows it cannot fire differently today (the planner is family-strict and the
  scheduler filters the capability term). It stays: §13's rule is that depth is kept even when the
  redundancy is provable, because one of the other two can be moved by a future refactor and this one is
  where the intent is written down.
- the wrapper's strategy check on the literal packet arm and the leaf's capability gate are the same rule
  split across two objects. That split is documented at `default_parallel_network.go:245-261` and was
  MEASURED when the leaf's half was missing: `ListenSerialInterfacePacket` returned `err = nil` and created
  an IPv6 socket on a machine with no IPv6. They must stay separate because the leaf is reachable on paths
  the wrapper never sees.

---

## S. Performance

Measured on this host, `go test -bench . -benchtime 300000x ./common/dialer/`:

| benchmark | ns/op | B/op | allocs/op |
|---|---|---|---|
| `AdmissionPolicyAdmits/dual-stack/allowed` | 19.3 | 0 | **0** |
| `AdmissionPolicyAdmits/dual-stack/ipv4-allowed` | 17.8 | 0 | **0** |
| `AdmissionPolicyAdmits/ipv4-only/refused` | 11.1 | 0 | **0** |
| `AdmissionPolicyAdmits/zero/allowed` | 21.8 | 0 | **0** |
| `AdmissionPolicyVerdict` | 9.4 | 0 | **0** |
| `AdmissionPolicyAdmitCandidates/nothing removed` | 78.5 | 0 | **0** |
| `AdmissionPolicyAdmitCandidates/strategy removes one` | 97.7 | 48 | 1 |
| `AdmissionPolicyAdmitCandidates/capability empties` | 75.8 | 24 | 1 |

Reading them:

- **The ordinary dual-stack flow pays nothing.** `admits` is 11–22 ns with zero allocations, and
  `admitCandidates` returns the resolver's OWN slice — no copy, no allocation — when it removes nothing.
  That is the contract Phase 01's `withoutUndialable`/`withoutFamilyExcluded` already had for the
  resolver-cache ownership reason, and it survives being composed.
- **The refusing case allocates one 24–48 byte slice**, which is the case where an attempt is being saved.
- **The zero policy is not slower than a real one**, so a dialer with no capability source is
  indistinguishable from having no policy at all.
- `admitCandidates` at 78 ns versus `admits` at 19 ns is two filter passes over a two-element list, once
  per flow; it is not a per-candidate cost. The per-candidate cost inside the scheduler is unchanged
  (`BenchmarkLocalFamilyFilter/single/*` from Phase 01: 11 ns, 0 allocs, and zero when nothing is refused).

**The prohibitions of §23 hold, `VERIFIED_SOURCE`:** no new goroutine, no new timer, no new DNS query, no
route scan, no new lock. The policy adds fields to a struct that was already on the stack and a
`niceness`-free function call per stage.

`VERIFIED_UNIT` for the properties the numbers imply: `TestTheCapabilityCaptureTakesNoLockAndNoSyscall`,
`TestOnePlanCostsOneOwnershipRead`, `TestCapabilityPublicationRegistersNoCallback`,
`TestInterfaceUpdatesDoNotAccumulateGoroutines`.

**Not measured:** a real transition's wall-clock latency on a device, and the cost on a host with a very
large routing table. Both `BLOCKED_BY_ENVIRONMENT`.

---

## T. Mutation

Fifteen mutations, run against the tree at `c449256be3` plus this phase's commits. The dialer subset ran
`-run 'Strategy|Strict|Plan|Late|Serial|Packet|Recovery|Family|Capability|Admission|Capture|Verdict|Promotion|Original|Health|Transition|Ownership|Generation'`,
and the route subset `-run 'Generation|Reclaim|Setup|Transition|Dial|Failover|Shim|Packet|Family'`.

| id | §28 mutation | result | test that caught it |
|---|---|---|---|
| M1 | recovery drops the strict strategy term | **KILLED** | `TestNetworkEntryPointPreservesTheOriginalWithinItsFamily`, `TestPacketPathIsRescuedByRecoveryWhenTheStrategyExcludesTheLiteral`, `TestRecoveryRescuesALiteralTheStrategyExcludes` |
| M2 | recovery drops the local capability term | **KILLED** | `TestRecoveryUsesTheFlowsCapturedVerdict`, `TestRecoverySkipsAnUndialableAnswerToReachAUsableOne`, `TestRecoveryReportsNothingWhenEveryFamilyIsUndialable`, `TestRecoveryMatrixIsFollowedByTheTransport` |
| M3 | a late candidate skips the policy | **KILLED** | `TestUnavailableFamilyIsRefusedFromTheLateStream`, `TestLateCandidateCanJoinOnceItsFamilyBecomesAvailable`, `TestRemovingTheLastDialableFamilyFailsTheRaceImmediately` |
| M4 | the scheduler skips its local-family gate | **KILLED** | `TestTheSchedulerUsesTheFlowsCapturedVerdict`, `TestOwnedVerdictStillHardDeniesOnItsOwnNetwork`, `TestCurrentFamilySnapshotStillHardDenies`, `TestOnePlanCostsOneOwnershipRead`, `TestUnavailableFamilyIsNeverAttempted` |
| M5 | an emptied packet candidate list is re-synthesised from the literal | **KILLED** | `TestPacketPathNeverDialsAFamilyTheStrategyExcluded`, `TestAStrictFamilyPolicyIsAppliedOnEveryPath` |
| M6 | original promotion ignores the policy | **EQUIVALENT** | — see below |
| M7 | one flow reads the strategy twice | **EQUIVALENT** | — see below |
| M8 | one flow captures the capability twice | **KILLED**, after a new test | `TestAResolvedNameIsAdmittedByTheFlowsOwnCapture` |
| M9 | the scheduler takes its generation from a fresh read | **KILLED** | `TestTheFlowsPolicyAndItsSchedulerShareOneGeneration`, `TestOnePlanCostsOneOwnershipRead`, `TestTheHealthNamespaceComesFromTheCapture` |
| M10 | the reclaim counter is published before the gate it belongs to | **KILLED** | `TestATransitionNeverPublishesItsCounterBeforeItsGate` |
| M11 | a setup keeps its generation watch after establishment | **EQUIVALENT** | — see below |
| M12 | the recovery producer is cancelled but not joined | **KILLED** | `TestRecoveryProducerIsJoined` |
| M13 | the parallel wrapper hands the scheduler an empty policy | **KILLED** | `TestALocalFamilyFastFailIsAppliedOnEveryPath`, `TestRecoveryMatrixIsFollowedByTheTransport` |
| M14 | the failover shim bypasses the group and the per-attempt options | **KILLED** | `TestTheFailoverShimDoesNotHideTheParallelDialSurface`, `TestAPlainLeafStillDialsThroughTheFailoverGroup` |

**11 killed, 3 equivalent, 1 of them found a real hole.**

### The three equivalences, each with its proof

**M6 — original promotion ignores the policy.** Replacing `if admission.admits(original)` with `if true`
cannot change which address is dialled, for two independent reasons. First, `planCandidates` is
family-strict: under a `_only` strategy it collects one family and `promoteOriginal` returns a slice
belonging to another family untouched (`dual_stack.go:263-272`), so a strategy-excluded original is never
inserted. Second, the scheduler filters the capability term afterwards, so a capability-refused original
that IS promoted is dropped before a socket. The guard stays as the third gate; the mutation is recorded as
equivalent so nobody mistakes it for the only thing holding the rule.

**M7 — one flow reads the strategy twice.** Cannot change any decision: the effective strategy is
configuration resolved once per dialer and memoised behind `sync.Once`, so a re-read returns the identical
value. `TestTheEffectiveStrategyIsResolvedOncePerDialer` pins that — the router is asked exactly once, and
a strategy changed afterwards does not move the value. **This equivalence is the answer to the obvious
question about this phase's design**: why a per-flow value at all, if the strategy cannot drift? Because
the *capability* does. That asymmetry — one term memoised per dialer, one term republished per generation —
is the whole reason the policy has to exist, and it is now measured from both sides.

**M11 — a setup keeps its generation watch.** Cannot change behaviour: the connection a dial returns is
not derived from the setup context, and `cancelSetup` runs regardless, so a later transition firing the
watch cancels an already-cancelled context. The suite agrees —
`TestNetworkTransitionDoesNotKillAnEstablishedFlow` and its siblings stay green. What the stop really buys
is bounded `AfterFunc` registrations, and the comment that claimed otherwise was corrected in
`c15196cd7ba`.

### One methodological correction, recorded because it nearly became a false finding

The first form of M10 was `advanceAndBump(nil)` **followed by** `generation.Add(1)`. It SURVIVED, and the
reason is that it does not reintroduce the defect: the bump is still after the gate's critical section, so
no dial can capture the old context and be stamped with the new counter. The faithful mutation is the
bump **before** the gate, which is what the code's own comment describes, and it is killed. A surviving
mutation means "the tests did not catch this edit", not "the tests are wrong" — and the difference has to
be established before a test is written for it.

### And the one that found a real hole

M8 survived the first pass. A second capture is invisible to every fixture in the tree, because they all
answer the same way on every read. The first test written for it **also** failed to catch it — it offered
both families and, under AsIS, the planner puts the IPv4 family first, so the IPv6 candidate was never
reached and the assertion measured the order rather than the policy. The test that bites offers only the
refused family, so the verdict is the only thing that can stop it. Both the wrong version and the reason are
recorded in the test's own comment, because a test that passes against the regression it was written for is
worse than no test.

---

## U. 新发现的奇妙点

### U.1 The packet domain arm had no policy at all — a live, user-visible defect

Measured before the fix, `ipv4_only`, name with no A answer and an AAAA answer:

```text
err   = <nil>
tried = [2409:8c54:871:1007::2c]
```

A UDP socket for the forbidden family, reported as a success. §N has the details; `489b4511e9a` is the fix
and the four tests. This is the round's one real code defect, and it was found by asking §19's question
("do connected and unconnected UDP use the same policy?") rather than by reading the code for defects.

### U.2 One literal flow took five readings of the network

§B's graph. Each reading was consistent; the composition was not. This is the finding the phase exists for,
and it is invisible to any per-function test because every function was right.

### U.3 The two terms have different lifetimes, and that is the whole design

The strategy is memoised per dialer; the capability is republished per generation. §T's M7 equivalence is
the proof, and it answers the "why not just keep reading the dialer" question: because half of the answer
does not change and the other half does, and only a value captured once can promise that a flow sees one of
each.

### U.4 `DialParallelNetwork` dropped the verdict when no owner was in the chain

`VERIFIED_SOURCE`, `default_parallel_network.go`. The fallback scheduler carried `fallbackDelay` only, so a
chain without a `familyHealthOwner` lost the capability term as well as the health — the narrowing defect
in its purest form, one function away from the code that fixed it three times. Fixed, and the fallback now
carries the policy without inventing a health table.

### U.5 The ownership predicate re-reads, and that is not a violation of §6

§6 says a policy must not "re-read the network manager on every `Admit()`". The predicate does read the
snapshot on every check — and must, because that is how a superseded generation is detected. The
distinction that makes both statements true: the predicate **validates** the capture and discards the
capability it also happens to receive; it never substitutes a fresh verdict. Stated here because the two
readings of §6 are one sentence apart and the wrong one is the safer-sounding one.

### U.6 The `dial_setup.go` comment claimed a correctness property the code does not have

`VERIFIED_MUTATION` M11. The connection is not derived from the setup context, so no ordering of "stop the
watch" versus "cancel the setup" can protect it. The line is still right — for bookkeeping — and the
comment now says that.

### U.7 Two generation counters, no fixed relationship, never compared

§F. Latent, not live. The important consequence is for Phase 03: **do not compare them**. Pick one and
derive the other.

### U.8 `DefaultDialer.networkGeneration()` has only test callers and captures on every call

`VERIFIED_SOURCE`: two test files call it, no production path does. The live namespace comes from
`newDualStackScheduler(…, admission)`. It is a test-facing accessor whose per-call capture is exactly the
shape this round removed elsewhere, so it is named here so the next reader does not mistake it for the
mechanism.

### U.9 The failover boundary is per member, not per group

§M.4. Recorded because "one flow one policy" is now a documented invariant, and a documented invariant
needs its exceptions written down next to it.

---

## V. Residual risks

| # | risk | severity | why it is accepted |
|---|---|---|---|
| V.1 | **A failover group's member attempts each capture their own policy** (§M.4, §U.9). | low | A member is a separate setup with its own `domain_strategy`; carrying one member's policy to another would be the interface narrowing this phase removes. Stated as the boundary of the invariant. |
| V.2 | **`-race` unverified.** | medium | `BLOCKED_BY_ENVIRONMENT`. The functional equivalents ran without it: `TestCapabilitySnapshotIsRaceFree`, `TestDNSEnvironmentChangeIsRaceFree`, `TestConcurrentEnvironmentTransitionsAreSerialised`, `TestCapabilityReadIsConsistentUnderConcurrentTransitions`, `TestRepeatedTransitionsWithDialsInFlightLeakNothing`. |
| V.3 | **Two generation counters with no fixed relationship** (§F, §U.7). | low now, medium for Phase 03 | Nothing compares them. The risk materialises only if a future check assumes they are equal. |
| V.4 | **The literal packet arm's two-gate split** — the wrapper applies the strategy, the leaf applies the capability. | low | Measured and documented as load-bearing twice (`default_parallel_network.go:245-261`); merging them would remove a fast-fail that only the leaf provides on paths the wrapper never sees. |
| V.5 | **`plannableOriginal`'s `admits` guard cannot fire differently today** (§T M6). | low | Kept as depth per §13; recorded so it is not read as the only gate. |
| V.6 | **Device behaviour unverified**; Darwin's missing route-change callback and Windows' IPv4-only default-interface picker carry over from Phase 01 (§V.4, §V.7 there). | medium | No device in this environment. Phase 04 is where the dynamic A→B run belongs. |
| V.7 | **The recovery goroutine's policy is captured before the goroutine starts**, so a transition during recovery does not change its filter — by design, but it means a recovery result can be filtered by a generation whose setup is already cancelled. | low | The setup cancel ends the flow; the late candidate is judged again by a FRESH policy at admission (§13), which is the gate that decides. |

---

## W. Commits

The round is `git log --oneline c449256be3..HEAD` on `testing`, pushed in order. The commits whose effect
is code or tests are named with their hashes; the report and any correction to it are listed by subject,
because a commit cannot contain its own hash.

| SHA | subject | files |
|---|---|---|
| `01be4b9d42d` | `admission: one immutable per-flow Candidate Admission Policy` | `common/dialer/admission.go` (+242), `admission_policy_test.go` (+276) |
| `489b4511e9a` | `fix(admission): the unconnected packet domain arm applied no policy at all` | `admission.go` (+33), `resolve.go` (+32/−23), `admission_packet_domain_test.go` (+145) |
| `14cf18f826f` | `admission: one capture per flow, threaded through every stream and packet path` | 22 files: `resolve.go`, `dual_stack_parallel_network.go`, `dual_stack_recovery.go`, `default.go`, `default_parallel_network.go`, `admission_single_capture_test.go` (new), and 16 mechanical test migrations |
| `1db62223cbc` | `test(admission): cover the three gaps the §28 mutation campaign found` | `admission_flow_identity_test.go` (+153), `admission_bench_test.go` (+123) |
| `c15196cd7ba` | `refactor(route): state the real reason the generation watch is stopped` | `route/dial_setup.go` (+20/−7, comment only) |
| *(hash in `git log`)* | `docs(knowledge): record the Phase 02 candidate admission construction` | `KNOWLEDGE/reports/core/CANDIDATE_ADMISSION_FLOW_LIFECYCLE_PHASE02_2026-10-18.md`, `KNOWLEDGE/reports/README.md` |

**Verification run on this host at the final SHA:**

```text
go build -tags "$TAGS" ./...                       PASS
go vet  -tags "$TAGS" ./common/dialer ./route ./protocol/direct   PASS
go test -tags "$TAGS" ./common/netcap              ok
go test -tags "$TAGS" ./common/dialer              ok   14.3s
go test -tags "$TAGS" ./dns                        ok    7.9s
go test -tags "$TAGS" ./protocol/direct            ok    0.02s
go test -tags "$TAGS" ./route                      ok  421.1s
gofmt -l .                                         clean
git diff --check                                   clean
go test -race ...                                  BLOCKED_BY_ENVIRONMENT (no cgo, no C compiler)
```

`TAGS` is `release/DEFAULT_BUILD_TAGS_OTHERS`; `DEFAULT_BUILD_TAGS` is the Darwin profile despite its name
and cannot build on Windows (`AGENTS.md`).

---

## X. Final verdict

1. **一条 flow 是否只有一个 Admission Policy snapshot?** **Yes**, for every entry point, and per socket
   attempt at the leaf by design. The capture points are listed in §D and each is at the top of an entry
   method. `VERIFIED_MUTATION` M8/M13 that the value is the one used, and `VERIFIED_UNIT` by the three
   changing-manager tests.

2. **policy / setup / scheduler / familyHealth 是否使用同一个 generation?** **Policy, scheduler and
   familyHealth do — provably from one capture.** The **setup gate and the managed connection stamp use a
   different counter** (the route transition counter), which is never compared with the network epoch and
   has no fixed relationship to it. §F states this precisely; it is a latent split, not a live one, and it
   is Phase 03's to keep that way.

3. **同一 flow 是否还会二次读取 strategy / capability?** The **capability**: no, and it is now measurable
   (M8 killed by a test that changes the answer mid-flow). The **strategy**: reads still occur, and they
   cannot drift because the value is memoised per dialer (M7 equivalent, pinned by
   `TestTheEffectiveStrategyIsResolvedOncePerDialer`). Both answers are properties the suite can see, which
   they were not before this phase.

4. **network transition 是否取消旧 setup，而不是动态修改旧 policy?** **Yes.** The policy is a value; nothing
   rewrites it. A transition cancels the stale setup through `dialSetupGate`, and the next flow captures a
   new policy. `VERIFIED_SOURCE` (§E) and unchanged from Phase 01.

5. **established 后 ownership 是否真正转移?** **Yes** — and the reason is now stated correctly: the returned
   connection is not derived from the setup context, so the setup path cannot reach it in either ordering of
   the release function. Reclaim is the only thing that can end it (§H, §L).

6. **recovery producer 是否严格从属于 setup?** **Yes.** It is cancelled and **joined** on every exit path —
   original win, recovered win, setup cancel — and it now consumes the flow's policy rather than capturing
   its own. `VERIFIED_MUTATION` M12 and the four survivors in §I.

7. **scheduler 是否不会自己刷新网络世界观?** **Yes.** Every field that makes up its worldview comes from the
   flow's capture; it never touches the manager. `VERIFIED_MUTATION` M9 (a fresh generation) and M4 (no
   local gate) and M13 (an empty policy).

8. **reclaim 是否仍只管理 established connection 的退场?** **Yes.** Nothing in this phase calls `Reclaim`,
   and Phase 01's drain/dead-path semantics are unchanged and pinned from both sides.

9. **initial / recovery / late / packet 是否使用同一 Admit 语义?** **Yes for the semantics, no for the code
   path — deliberately.** All five gates ask one `admissionPolicy`, and each keeps its own position because
   each covers a way a candidate arrives that the others cannot see (§R). The late-candidate guard is the
   one that also carries the scheduler's structural check, because a late arrival never passes the planner.

10. **哪些纵深 gate 被保留?** All five: initial filter, recovery admission, late-candidate admission,
    scheduler local-family guard, leaf socket gate — plus `plannableOriginal`'s guard, kept as a third gate
    whose redundancy is now proved rather than assumed (§R, §T M6).

11. **是否发现新的 interface narrowing / lifecycle split-brain?** **One narrowing, fixed**:
    `DialParallelNetwork`'s fallback scheduler dropped the verdict when no owner was in the chain (§U.4).
    **One split-brain that is documentation, not code**: the `dial_setup.go` comment claimed a correctness
    reason the code does not have (§U.6, fixed). **One latent split-brain, recorded**: the two generation
    counters (§F, §U.7). **One live defect, fixed**: the unconnected-packet domain arm applied no policy at
    all and created a socket for a forbidden family, reported as success (§N, §U.1).

12. **hot path 是否退化?** **No, and it is measured.** `admits` 11–22 ns with **zero allocations** in every
    case including the zero policy; `verdict` 9.4 ns, zero allocations; `admitCandidates` returns the
    resolver's own slice with no copy and no allocation when it removes nothing, and costs one 24–48 byte
    slice only when it drops a candidate. No new goroutine, timer, DNS query, route scan or lock (§S).

---

## PHASE 02 HANDOFF

```markdown
## PHASE 02 HANDOFF

- testing HEAD:
    <report commit>  docs(knowledge): record the Phase 02 candidate admission construction
    c15196cd7ba      refactor(route): state the real reason the generation watch is stopped
    1db62223cbc      test(admission): cover the three gaps the §28 mutation campaign found
    14cf18f826f      admission: one capture per flow, threaded through every stream and packet path
    489b4511e9a      fix(admission): the unconnected packet domain arm applied no policy at all
    01be4b9d42d      admission: one immutable per-flow Candidate Admission Policy
  Start SHA for the round: c449256be39583bd86dc6e2070ed808021ad9e69

- Flow Decision Context capture point:
    The TOP of each dial/packet entry method, and nowhere else:
      resolveDialer.DialContext                 (resolve.go)
      resolveDialer.ListenPacket                (resolve.go)
      resolveParallelNetworkDialer.DialParallelNetwork
      resolveParallelNetworkDialer.ListenSerialNetworkPacket
      resolveParallelNetworkDialer.DialParallelInterface
      resolveParallelNetworkDialer.ListenSerialInterfacePacket
      DefaultDialer.DialContext / ListenPacket / ListenSerialInterfacePacket  -> ONE PER SOCKET, deliberate
    The free DialParallelNetwork TAKES the policy as a parameter: it sits below the layer that knows the
    strategy, and a capture there would have to invent one.

- Capability snapshot source:
    Phase 01's adapter.NetworkFamilyCapabilitySnapshotter, reached through
    resolveDialer.dialerLocalFamilyPolicy() (a chain walk to the ONE localFamilyOwner, *DefaultDialer).
    Consumed as localFamilyPolicy{ownership, capability} inside admissionPolicy.
    02 must NOT add a second source. admission.go:57-58 is the only place a policy's two terms are read.

- Strategy capture point:
    resolveDialer.effectiveFamilyStrategy(), MEMOISED per dialer behind sync.Once.
    It is configuration (Layer 0) and does not drift within a flow - proved by
    TestTheEffectiveStrategyIsResolvedOncePerDialer, and it is why mutation M7 is EQUIVALENT.
    The capability term is the one that changes; that asymmetry is the reason the policy exists.

- Generation ownership:
    THE FLOW'S POLICY, THE SCHEDULER AND familyHealth ALL USE THE NETWORK RESET EPOCH, taken from
    admissionPolicy.generation() (= the capability's own ownership epoch).
    The SETUP GATE and the MANAGED CONNECTION STAMP use a DIFFERENT counter: route.ConnectionManager.generation,
    advanced only inside a draining Reclaim's advanceAndBump.
    The two are NEVER COMPARED and have NO FIXED RELATIONSHIP (the route counter does not advance on
    ReclaimDeadPath). 03 MUST NOT compare them; pick one and derive the other.
    The ownership predicate DOES re-read the snapshot on every check - that is invariant B, not a second
    capture. It validates; it never substitutes a verdict.

- Recovery policy propagation:
    resolveDialer.recoverCandidates(ctx, destination, admission) and
    resolveDialer.admittedRecoveredCandidates(addresses, admission) - the policy is a PARAMETER, so the
    family that ends a lookup is the family the flow decided on.
    The producer is cancelled AND JOINED on every exit path (recoveryFeed.stop waits).
    It is started from a goroutine but the capture happens BEFORE the goroutine starts.
    03 must keep both: the join, and the parameter.

- Scheduler policy propagation:
    DefaultDialer.newDualStackScheduler(fallbackDelay, admission) is the ONLY production construction.
    Both of its network fields come from the one value: networkGeneration = admission.generation(),
    localFamily = admission.localFamily. It never reads the manager.
    familyHealthOwner's method signature carries the policy - one method, not two.

- Packet path propagation:
    All four arms ask ONE policy:
      connected UDP / candidate list   -> ListenSerialNetworkPacket -> admitCandidates
      unconnected UDP, literal         -> wrapper applies the strategy, LEAF applies the capability (two
                                          gates, both load-bearing - see default_parallel_network.go:245)
      unconnected UDP, HOSTNAME        -> admitCandidates + emptiedListRefusal  (this arm applied NOTHING
                                          before 489b4511e9a; a UDP socket for a forbidden family was
                                          created and reported as success)
      parallel interface packet path   -> threaded
    UDP lifecycle was NOT changed into the TCP scheduler race (connected stays a candidate dispatch,
    unconnected stays a serial walk). Only the policy was added.

- Wrapper/failover propagation:
    The policy travels as a whole VALUE through function parameters; no wrapper re-derives it and none
    unpacks it. Rich surfaces are reached by WALKING (findFamilyHealthOwner / dialerLocalFamilyPolicy through
    Upstream()/DialChain()), never by asserting on a narrow interface.
    DialParallelNetwork's fallback scheduler now carries the policy instead of dropping the verdict.
    BOUNDARY: a failover group's member attempts each capture their own policy, because a member is a
    separate setup with its own domain_strategy. "One flow one policy" binds per member setup, not across a
    group. Do not "fix" that.

- Setup ownership transfer boundary:
    route/dial_setup.go. release() stops the generation watch and then cancels the setup. The ORDER is right
    but NOT for the reason the old comment gave: the returned connection is not derived from the setup
    context, so neither ordering can protect it (mutation M11). The stop bounds AfterFunc registrations.
    Ownership transfer is unchanged and must stay: a capability change never reaches the connection manager.

- Reclaim boundary:
    Untouched by 02. ReclaimNetworkTransition drains (20 s proven-idle grace, sweep after), ReclaimDeadPath
    closes everything, ReclaimIdle reclaims proven-idle only. Nothing in the admission path calls Reclaim.

- Stage-specific gates intentionally retained:
    1. initial filter            withoutFamilyExcluded(list, admission.strategy) at the network entry
    2. recovery admission        admittedRecoveredCandidates(addresses, admission)  (both terms)
    3. late candidate admission  admission.lateAdmission() + the scheduler's hardUndialable per arrival
    4. scheduler local gate      filterLocalFamily -> s.localFamily.gate()  (ownership-gated)
    5. leaf socket gate          DefaultDialer's own capture, one per socket
    plus 6. plannableOriginal's admission.admits - a third gate whose redundancy is PROVED (planner is
    family-strict; scheduler filters capability) and which is kept as depth per §13.

- Known risks reserved for 03:
    * the two generation counters (§F/U.7): never compare them;
    * the failover per-member boundary (§M.4/U.9);
    * the literal packet arm's two-gate split (V.4);
    * `DefaultDialer.networkGeneration()` is test-only and captures per call (U.8);
    * -race is BLOCKED_BY_ENVIRONMENT on this host; the functional equivalents are named in §S.

- Dynamic transition questions reserved for 04:
    * A→B during setup: does the flow end with the machine's verdict or the old network's? (The policy is
      captured at entry and the setup is cancelled by the transition, so the answer should be "neither - the
      setup ends and the retry captures B". Needs the dynamic harness.)
    * A→B during recovery: a recovered candidate filtered by generation N's policy arriving after B claims.
      The setup cancel ends it; the late-admission gate re-judges with a FRESH policy. Prove the ordering.
    * A→B between the plan and the first attempt.
    * A→B after establishment: the capability must NOT close the connection (Phase 01 §Q) while a new flow
      uses B's policy. Prove both halves in one run.
    * Whether the two generation counters can be observed to disagree in one A→B run - they should, and
      nothing should care.
```
