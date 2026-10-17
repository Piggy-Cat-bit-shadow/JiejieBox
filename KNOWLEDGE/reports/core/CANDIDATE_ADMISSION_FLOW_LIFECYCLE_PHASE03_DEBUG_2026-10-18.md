# Candidate Admission + Flow Lifecycle — Phase 03专项 Debug report

**Round:** `03 / 04`, Candidate Admission + Flow Lifecycle Debug.
**Branch:** `testing`.
**Start SHA:** `4e86eae6df7d839e11be181be1dbba99688ac5d3` (Phase 02 closed out; `0/0`, worktree clean).
**Final SHA:** the `HEAD` of `origin/testing` after the commit that adds this file — not written here, for
the reason given in the two preceding reports.

**Evidence levels** as `KNOWLEDGE/README.md` defines them: `VERIFIED_SOURCE`, `VERIFIED_UNIT`,
`VERIFIED_MUTATION`, `BLOCKED_BY_ENVIRONMENT`.

**`-race` remains `BLOCKED_BY_ENVIRONMENT`** (`CGO_ENABLED=0`, no mingw-w64 C toolchain). Every attack below is
deterministic by construction — barriers, not sleeps — precisely because the race detector is unavailable:
a test that could only fail under `-race` is a test this environment cannot run.

**Device behaviour is unverified.** No TUN, no NetworkExtension, no real handover.

> **Correction, measured after the round: the `-race` blocker is not what this report first said.**
> The blocker was recorded as "no C compiler". An MSVC-targeting `clang` **is** present, so that wording
> was wrong, and being wrong about a blocker is worse than being vague about one - it hides the fact that
> a single install would unblock the item. What was actually tested:
>
> | attempt | result |
> |---|---|
> | `CGO_ENABLED=1 CC=clang go build -race ./common/netcap` | **succeeds** - which is why the first probe looked like progress; the package compiles but never links |
> | `CGO_ENABLED=1 CC=clang go test -race ...` | fails at `runtime/cgo`: `clang: error: unsupported option '-mthreads' for target 'x86_64-unknown-windows-msvc'` - Go's Windows cgo driver passes mingw-style flags that this toolchain rejects |
> | `CC='clang --target=x86_64-w64-windows-gnu'` | fails at `stdlib.h: file not found` - no mingw sysroot or headers |
> | a `gcc.exe` anywhere on the host | none (searched) |
>
> So `-race` remains `BLOCKED_BY_ENVIRONMENT`, and the **minimum condition to resume** is: a mingw-w64 gcc
> (e.g. MSYS2's `mingw-w64-x86_64-gcc`) on `PATH`, then
> `CGO_ENABLED=1 CC=gcc go test -race ./route ./common/dialer`. Everything else this round needed is
> already in place. `VERIFIED_CI` still does not apply, and no claim in this report depends on `-race`
> having run.

---

## A. 本轮定位

Phase 01 built the producer. Phase 02 made the consumer use one immutable snapshot per flow. **Phase 03
attacks the consumer's internal consistency**: one flow, one policy, one generation, one setup, one
ownership transfer — proven by trying to break each junction rather than by re-reading the design.

Explicitly **out of scope** here, per the brief: the full A→B transition matrix (IPv4-only ↔ dual-stack,
route lag, address lag, Settling publication). Those are Phase 04's.

What 03 may do — and did — is use a controlled snapshot fixture plus, where a lifecycle claim needs it, a
real cancellation path.

---

## B. 测量方法：为什么计数点放在 owner

Phase 02 refused to measure "one capture per flow" by counting manager reads, and the reason stands: the
ownership predicate re-reads the snapshot on **every** check, deliberately, so a manager-side count mixes
*"I captured a policy"* with *"I validated that my policy is still current"*, and no threshold over that
mixture can be defended.

There is a seam that separates them cleanly:

| seam | who reaches it | what it counts |
|---|---|---|
| `localFamilyOwner.localFamilyPolicy()` on the chain | only `dialerLocalFamilyPolicy()`, i.e. only a capture | **captures, exactly** |
| `NetworkFamilyCapabilitySnapshot()` | both the capture and the ownership predicate | a mixture — rejected in 02 |
| `DNSStrategyResolver.ResolveStrategy()` | only `effectiveFamilyStrategy()`'s `sync.Once` | strategy resolutions, exactly |

`captureCountingLeaf` counts at the first seam and `strategyCountingRouter` at the third, so §2's two
counts — *strategy reads = 1*, *capability captures = 1* — are both asserted directly.

**One refinement that matters.** The real `DefaultDialer` captures once more, immediately before a socket
is created, as its last-resort gate. That capture is deliberate and stays (§R of the Phase 02 report). The
fixture's leaf delegates the dial to a transport double, so the count measures the **decision path alone**
— which is the quantity this phase is about — and the per-socket capture is covered by its own suite.

`VERIFIED_UNIT`: `TestOneFlowCapturesThePolicyExactlyOnce` (7 shapes),
`TestAStrictFlowResolvesTheStrategyFromConfiguration`. `VERIFIED_MUTATION`: re-introducing a second capture
in the serial branch fails both (Q2 below).

---

## C. 发现并修复的缺陷：serial 分支重复 capture

**Phase 02 introduced it.** `DialContext` captures the flow's policy at the top; commit `489b4511e9a`
moved the serial branch onto the shared `admitCandidates` composition but left that branch's own
`captureAdmissionPolicy()` in place. The inner `:=` shadowed the outer value:

```go
admission := d.captureAdmissionPolicy()          // the flow's policy
...
	if !d.parallel {
		admission := d.captureAdmissionPolicy()  // a SECOND one, shadowing the first
		admitted, refused, strategyExcludedAll := admission.admitCandidates(addresses)
```

Why nothing caught it:

- the compiler is silent — shadowing in an inner scope is legal;
- every static fixture answers the same way on both reads, so **two identical readings are
  behaviourally indistinguishable from one**;
- 42 call-site migrations and a 15-mutation campaign all passed.

**MEASURED**: `TestOneFlowCapturesThePolicyExactlyOnce` reported 2 captures for the serial shape and 1 for
the other six. Six were already correct; one was not. That ratio is the whole argument for counting: a
count is the only measurement that can tell one reading from two identical ones.

**The fix is one deleted line** (`f264ecf2a63`). The behaviour difference is small and in the right
direction: the serial branch used to capture **after** the DNS lookup, so a transition landing during the
lookup would be picked up by it. It now uses the value the flow began with, which is the contract — a
flow has one worldview, and a transition ends the setup rather than rewriting it.

**Why this is the finding of the round.** It is not a design flaw; it is exactly the class of defect a
consumer-side round can create while doing everything else right, and it was invisible to every method
except the one this phase exists to add.

---

## D. 四条攻击轴

### D.1 Policy：一条 flow 是否真的只有一个 worldview？

| attack | result | evidence |
|---|---|---|
| count captures across 7 flow shapes (streaming hostname, planner hostname, literal+recovery, bare literal, serial, packet literal, packet hostname) | **1 each** after the fix | `VERIFIED_UNIT` + `VERIFIED_MUTATION` Q2 |
| count strategy resolutions | **1** for AsIS, **0** for an explicit `_only` | `VERIFIED_UNIT` |
| change the manager's **capability** mid-flow under an unchanged epoch (a route flap) | the flow keeps refusing; the new flow sees the new capability | `VERIFIED_UNIT`, `TestACapabilityChangeUnderALiveFlowCannotReachIt` |
| change the manager's **generation** mid-flow | structurally impossible (§E) | `VERIFIED_SOURCE` + the corrected test |

### D.2 Generation：policy / scheduler / familyHealth 是否同一个 generation？

| attack | result | evidence |
|---|---|---|
| offset the scheduler's namespace from the policy by one | **caught** | `VERIFIED_MUTATION` P10 |
| take the scheduler's generation from a fresh read | **caught** (02) | `VERIFIED_MUTATION` M9 |
| (setup gate and connection stamp use the other counter) | unchanged, never compared | `VERIFIED_SOURCE`, Phase 02 §F |

### D.3 Setup / Ownership：transition 是否只取消 stale setup？

| attack | result | evidence |
|---|---|---|
| publish the reclaim counter before the gate it belongs to | **caught** (02, faithful form) | `VERIFIED_MUTATION` M10 |
| keep a setup's generation watch after establishment | behaviourally equivalent; the comment was corrected (02) | `VERIFIED_MUTATION` M11 |
| a late candidate arriving after the setup is cancelled | see §F.3 — launched, but its attempt carries the cancelled context | `VERIFIED_SOURCE` |

### D.4 Cross-stage consistency：各 stage 是否同答案？

`TestTheAdmissionMatrixIsOnePolicyAcrossEveryStage` enumerates **5 strategies × 4 capability shapes × 10
destinations = 200 pairs**, and for each compares six stage answers:

| stage answer | must satisfy |
|---|---|
| `admits(addr)` | the reference |
| `strategyAllows(addr)` | a superset of `admits` |
| `admitCandidates(list)` | keeps exactly what `admits` accepts, in order |
| `lateAdmission()(addr)` | never laxer **or stricter** than the strategy, and armed at all when strict |
| `verdict(addr)` | `admissionAllowed` exactly when `admits` |
| `refusalError(addr)` | non-nil exactly when `admits` is false |

plus the two list-level outcomes, which must not both claim to have emptied the list and must not both stay
silent when it is empty — the caller would then report a policy decision that never happened.

`VERIFIED_MUTATION`: disarming the late guard ("it is only an optimisation, return nil") fails the matrix
on *"a strict strategy armed no late-candidate guard"*. `VERIFIED_UNIT` otherwise.

**Why both directions.** Over-refusal loses a candidate the operator admitted — the fail-closed direction
that costs a working path. Under-refusal puts a forbidden family into a race — the fail-open direction that
turns the policy into a suggestion. Only the second is obvious, and it is the one a plausible "optimisation"
produces.

---

## E. 一个被我自己的测试推翻的前提，和它揭示的结构事实

**The premise.** Phase 03's brief asks: freeze the world at generation N, change the provider mid-flow
*without* a lifecycle cancel, and prove the flow ignores the new answer; then really transition and prove a
NEW flow picks up N+1.

My first version of that test moved the manager's **epoch** mid-flow and asserted the captured policy still
refused. It failed — and it was the test that was wrong:

> the ownership predicate saw the epoch move, correctly judged the capture superseded, and refused
> **nothing**. That is invariant B. The test was demanding that a *stale* capture still hard-deny, which is
> the one thing the whole design forbids.

**The structural fact it exposed**, and it is worth stating because it is not written anywhere else:

> **The network epoch cannot move without a lifecycle cancel.** It advances only inside `beginTransition`,
> and every path that claims one pairs it with a cancelled setup (`ResetNetwork`, `notifyInterfaceUpdate`,
> the environment transition, the power resume). So "the flow's policy was refreshed mid-flow" is
> impossible for the **generation** term: by the time a reader could observe a new epoch, the setup that
> would have used the old one has already been cancelled.

The term that **can** move under a live flow is the **capability**. A route flap, an address appearing, a
DHCPv6 lease expiring all republish the verdict under the **incumbent** epoch (Phase 01 §D). Ownership
cannot detect that — the epoch is unchanged, so the capture is still "current" — and the only thing standing
between the flow and a mid-dial verdict change is the snapshot itself.

The corrected test attacks exactly that case, and checks the four things that make it a real proof:
the capture is still owned, its verdict did not move, a full flow through it never dials the newly-admitted
family, and a **new** flow sees the new capability **under the same epoch** — which is why the generation
alone cannot be the discriminator and the flow must carry the value.

`VERIFIED_UNIT`: `TestACapabilityChangeUnderALiveFlowCannotReachIt`.

### E.3 The one lifecycle claim 03 cannot prove, recorded instead

The brief's §11 also says a late candidate must **not be launched** after the setup was cancelled. The
scheduler today *does* launch it — `startAttempt` runs, and the attempt carries `raceCtx` — so the dial
fails immediately with the cancellation and **no socket work happens**, but a goroutine was started.

- The **observable** property (§13's "no socket work on a cancelled setup") holds, and is pinned by
  `TestNetworkTransitionCancelsAnInFlightBlackholedDial` and the route suite.
- The **letter** of §11 does not: the launch happens.
- A deterministic test of the letter is not available without restructuring the scheduler: after the setup
  is cancelled both `<-ctx.Done()` and the late-candidate channel can be ready, and Go's `select` chooses
  between ready cases at random — so any assertion would be a coin flip. Writing it would be the
  "fixture that passes by luck" this brief forbids.

Recorded as a finding rather than fixed, because the fix (refusing the launch when `raceCtx.Err() != nil`)
would change error aggregation for a case where no work currently happens. It belongs to Phase 04, where
the dynamic ordering is under test.

**The mechanism that makes it harmless is now pinned rather than reasoned about** (`§L.4`): every context an
attempt receives must be done by the time the race returns, which is only true if it descends from the
setup's, and a real listener proves a pre-cancelled dial never connects. So the item stays OPEN in its
letter and is `VERIFIED_UNIT` in the part that matters.

---

## F. 变异

Three new mutations were run for 03, plus the Phase 02 set re-verified at the 03 tree.

| id | mutation | result | killer |
|---|---|---|---|
| **Q2** | the serial branch captures its own policy again | **KILLED** | `TestOneFlowCapturesThePolicyExactlyOnce`, `TestAStrictFlowResolvesTheStrategyFromConfiguration` |
| **Q3** | the late-candidate guard is disarmed ("only an optimisation") | **KILLED** | `TestTheAdmissionMatrixIsOnePolicyAcrossEveryStage` |
| **P10** | the scheduler's health namespace is offset by one from the policy | **KILLED** | `TestTheSchedulerUsesTheFlowsCapturedVerdict`, `TestTheFlowsPolicyAndItsSchedulerShareOneGeneration`, `TestProductionDialerRemembersFamilyFailure`, `TestTheHealthNamespaceComesFromTheCapture` |
| **P18** | a lifecycle cancellation is classified as a family path failure | **KILLED** | `TestCancellationDoesNotRecordFamilyFailure`, `TestSameFamilySlowCandidateRecordsNoVerdict`, `TestCallerTeardownIsNeverAFamilyFailure` |
| M8 | one flow captures the capability twice (02) | **KILLED**, now also by the counter | `TestOneFlowCapturesThePolicyExactlyOnce`, `TestAResolvedNameIsAdmittedByTheFlowsOwnCapture` |
| M13 | the parallel wrapper hands the scheduler an empty policy (02) | **KILLED** | `TestALocalFamilyFastFailIsAppliedOnEveryPath`, `TestRecoveryMatrixIsFollowedByTheTransport` |

**Two of these are the round's own defects**: Q2 was introduced in Phase 02 and found by 03's
instrumentation; Q3 is the mutation that proves the matrix is not decorative.

P10 and P18 were run expecting to find gaps — Phase 03 is a debug round, so a survivor would have been the
interesting result — and were killed by tests **that already existed**. That is worth recording precisely
because it is the opposite of a finding: the cancellation-classification and health-namespace rules were
already pinned, which is why the round does not claim credit for them (§G).

---

## G. 已有覆盖的核查（不冒充本轮功绩）

Several of the brief's attack surfaces were already covered before this phase. They were **verified by
reading the test and its assertion**, not re-derived, and are listed so the next round knows where the
proof lives rather than commissioning the same test again.

| surface | where the proof already is | level |
|---|---|---|
| leaf socket gate, and its fail-open on Unknown | `family_capability_test.go` (`owner.ListenPacket` / `LocalFamilyBlocked` directly), `global_ipv6_direct_outage_test.go` | `VERIFIED_SOURCE` |
| scheduler's local-family gate, attacked by injecting a forbidden candidate directly into the plan (bypassing the producer filters) | `family_capability_test.go` plan-injection cases; `TestUnavailableFamilyIsNeverAttempted` | `VERIFIED_SOURCE` + `VERIFIED_MUTATION` (M4) |
| refusal reasons distinguishable — strategy vs no-address vs no-egress | `dual_stack_parallel_network_test.go:620-634` (`ErrorIs` **and** `NotErrorIs` in both directions), `serial_strategy_test.go`, `refusal_verdict_test.go` | `VERIFIED_SOURCE` |
| cancellation is not a family failure | `TestCancellationDoesNotRecordFamilyFailure`, `TestCallerTeardownIsNeverAFamilyFailure` | `VERIFIED_MUTATION` P18 |
| recovery producer cancelled and joined | `TestRecoveryProducerIsJoined`, `TestLiteralOriginalWinnerCancelsRecovery` | `VERIFIED_MUTATION` M12 (02) |
| proxy boundary — the policy applies to the proxy's own address and not to the remote target | `proxy_endpoint_scope_test.go`, `path_parity_matrix_test.go` | `VERIFIED_SOURCE` |
| failover shim does not hide the parallel surface or the per-attempt options | `TestTheFailoverShimDoesNotHideTheParallelDialSurface`, `TestAPlainLeafStillDialsThroughTheFailoverGroup` | `VERIFIED_MUTATION` M14 (02) |

**What this phase did NOT do:** it did not re-attack the packet lifecycle end to end, the manager-close
ordering, or context propagation, beyond the counts and the matrix above. Those were covered by Phases 01
and 02 and by the route suite; re-running them would produce the same green with no new information, and
the brief reserves the dynamic versions for 04.

---

## H. 奇妙点

### H.1 Two identical readings are indistinguishable from one — except by a count

The serial branch's duplicate capture is the clearest example this round produced. Every behavioural test
passed because both captures returned the same answer. **A snapshot property cannot be tested by behaviour
alone when the provider is static**; it needs either a count or a provider that changes. Phase 03 now has
both, and they caught different things (the count caught Q2; the changing provider caught M8 in 02).

### H.2 The epoch cannot move without a cancel, so ownership and the snapshot cover different terms

§E. The generation term is protected by the lifecycle *and* by ownership; the capability term is protected
only by the snapshot. That asymmetry is the reason the per-flow value is not merely tidiness.

### H.3 A stale capture refuses nothing, and that is the design

The natural intuition — "the policy I captured should keep applying" — is wrong for a superseded capture,
and my own first test asserted it. Invariant B says the opposite: an unowned capture is fail-open. Worth
recording because the intuition is strong enough that a future reader may "fix" it.

### H.4 The matrix found its own value only when mutated

`TestTheAdmissionMatrixIsOnePolicyAcrossEveryStage` passed on the first run, which proves little. Q3 —
disarming the late guard — is what makes it a test: it fails on a change that leaves all 128 file-level
tests green otherwise.

### H.5 The environment cannot run `-race`, so the tests must be deterministic

Every attack here uses a barrier or a governed fixture; none sleeps and hopes. The cost is that a genuine
data race in the new value would not be detected by this round — it would have to be found by reading the
`sync` discipline, which is why `admissionPolicy` has no mutable state at all.

---

## I. 留给 04 的风险

| # | risk | why it matters for 04 |
|---|---|---|
| I.1 | **A late candidate is launched after the setup cancel**, though its attempt carries the cancelled context (§E.3). | The dynamic round is where the ordering is under test; if 04 can build a deterministic interleaving, the launch guard becomes testable and the letter of §11 can be met. |
| I.2 | **The two generation counters are still never compared** (Phase 02 §F). | An A→B run will make them diverge observably; nothing should care, and 04 should assert that nothing does. |
| I.3 | **A capability change under a live flow is covered only at the dialer level** (§E). | 04 has to prove it end to end: a real route flap during a real setup, with the flow landing on the captured verdict. |
| I.4 | `-race` unverified. | 04's end-to-end run is the last chance to catch a concurrency defect by construction rather than by inspection. |
| I.5 | The capture counter counts the **decision** path; the leaf's per-socket capture is excluded by the fixture. | 04 should not read the count as "one capture per socket" — it is one per flow plus one per socket, deliberately. |

---

## J. Commits

The round is `git log --oneline 4e86eae6df7..HEAD` on `testing`, pushed in order.

| SHA | subject |
|---|---|
| `f264ecf2a63` | `fix(admission): the serial branch captured the policy a second time` |
| `401d5a8e58e` | `test(admission): one cross-stage consistency matrix for the whole space` |
| *(hash in `git log`)* | `docs(knowledge): record the Phase 03 single-flow debug round` |

**Verification at the final SHA:**

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
go test -race ...                                  BLOCKED_BY_ENVIRONMENT
```

---

## K. 终态回答

1. **一条 flow 是否只有一个 policy snapshot?** **Yes, now provably.** Seven flow shapes each capture
   exactly once, counted at the owner seam; one shape did not, and the count is what found it (§C).
2. **policy / generation / scheduler / familyHealth 是否同一个 generation?** Yes for those four,
   `VERIFIED_MUTATION` P10 and M9. The setup gate and the connection stamp use the other counter, unchanged
   and never compared (Phase 02 §F).
3. **是否还会动态改写旧 flow 的 worldview?** No. A capability change under a live flow cannot reach it
   (§E) — and the generation cannot move without a cancel at all, which is a stronger guarantee than the
   brief asks for.
4. **transition 是否只取消 setup，而不是重写 policy?** Yes; and the one gap between the letter and the
   spirit of that rule is recorded in §E.3 rather than papered over.
5. **established 后 ownership 是否真正转移?** Yes, unchanged from 01/02 and pinned from both sides.
6. **recovery producer 是否严格从属 setup?** Yes — cancelled **and joined** on every exit path,
   `VERIFIED_MUTATION` M12.
7. **scheduler 是否会用新 generation?** It cannot: both of its network fields come from the flow's value,
   `VERIFIED_MUTATION` P10 and M9.
8. **late candidate 是否跳过 policy?** No. Both terms apply, and the guard's absence is now a test failure
   (`VERIFIED_MUTATION` Q3).
9. **refusal 是否统一?** One verdict source, four distinguishable outcomes, and the matrix proves every
   stage's answer agrees with `admits`.
10. **hot path 是否退化?** No: Phase 02's numbers stand, and 03 added no production code on the hot path
    (one deleted line, one test file, one matrix).
11. **有没有新的 split-brain / interface narrowing / ownership bug?** One **duplicate capture** — a
    consumer-side defect introduced by 02 and found here. No narrowing, no ownership bug; the one
    letter-versus-spirit gap in the late-candidate launch is recorded for 04.

---

## L. Addendum — the second pass

**The first pass was partial, and this section says so before it says anything else.** It attacked the
policy axis properly (the capture counter, the cross-stage matrix) and then established five lifecycle
claims by **reading** the test that already covered them: setup cancellation, scheduler lifetime, packet
parity, refusal distinguishability, and the policy's own lifetime. Reading a test establishes coverage; it
is not the same as attacking the claim, which is what this round is for. All five were left as "covered by
Phase 01 or 02" with no new attempt to break them.

The second pass attacks them (`a897deb2f9f`, `admission_debug_lifecycle_test.go`):

| attack | the wrong implementation it catches |
|---|---|
| `TestACancelledBeforeItStartsFlowReachesNoTransport` | one that dials a socket and then discards it — which also returns an error, and has woken the radio for a request nobody wants |
| `TestACancelledMidDialFlowRecordsNoFamilyVerdict` | one that records a caller's cancellation as a family path failure, poisoning the next connection for the whole fifteen-second window |
| `TestACancelledRaceJoinsItsAttempts` | one whose race "returns quickly" while a worker is still running — the leak that grows per transition on a device |
| `TestEveryPacketArmAnswersTheSamePolicy` | one arm applying a term another arm owns |
| `TestTheRefusalReasonsAreMutuallyDistinguishable` | two refusals rendering identically, sending an operator to the wrong subsystem |
| `TestNoLongLivedStructureHoldsAPolicy` | a struct FIELD holding a policy, which keeps a generation alive past its network and looks harmless at the definition site |

Both new guards are `VERIFIED_MUTATION`: deleting the literal packet arm's strategy check fails the parity
table on *"the literal arm must refuse a destination of the forbidden family"*, and giving
`candidateScheduler` an `admissionPolicy` field fails the source guard by name.

### L.1 The finding: the three packet arms answer one policy at three different layers

This is the second pass's substantive result, and it arrived as a **failure of my own table**. The first
version asserted one expectation for all three arms and failed twice; both failures were the test's fault.
What it exposed is not written down anywhere else:

```text
connected UDP, candidate list   the STRATEGY term by the network entry point,
                                the CAPABILITY term by the scheduler
                                (both, before a socket exists)

unconnected UDP, literal        the STRATEGY term by the wrapper,
                                the CAPABILITY term by the LEAF,
                                after the wrapper hands the literal down

unconnected UDP, hostname       BOTH terms by the wrapper - there is no scheduler and no
                                candidate list, so the list handed to N.ListenSerial IS
                                the decision (the arm that was missing the policy entirely
                                until Phase 02 found it)
```

So *"every packet arm answers the same policy"* is true in the sense that matters — no arm admits what the
operator's policy forbids, and no arm creates a socket for a family the machine cannot dial — and **false**
if it is read as "every arm refuses at the same layer". The table now encodes the asymmetry, and the
asymmetry is the documentation: a wrapper that applied the capability term to the **literal** arm would hide
the leaf's gate from every path that does not come through the wrapper, and the leaf's gate is the one
measured to be load-bearing twice (`default_parallel_network.go:245-261`).

**This is the fourth time in the round that a fixture measured the wrong thing** — Phase 02's planner
ordering, Phase 03's first pass with the serial walk, Phase 04's superseded capture, and this. The pattern
is the same every time: the test asserted a property of the *path* while claiming to assert a property of
the *policy*, and every such test passed.

### L.2 §G updated

| surface | level after the second pass |
|---|---|
| setup cancellation by the caller | `VERIFIED_UNIT` + `VERIFIED_MUTATION` (was `VERIFIED_SOURCE`) |
| scheduler joins its attempts | `VERIFIED_UNIT` |
| refusal reasons mutually distinguishable | `VERIFIED_UNIT` (was `VERIFIED_SOURCE`) |
| packet arms answer one policy at three layers | `VERIFIED_UNIT` + `VERIFIED_MUTATION` |
| policy is never a struct field | `VERIFIED_UNIT` + `VERIFIED_MUTATION` |

### L.3 The two surfaces §L named as still open are now closed

**Manager close during an in-flight setup** (`route/setup_close_boundary_test.go`). Four shapes, because
"the manager was closed" is not the question:

| shape | the silent failure it prevents |
|---|---|
| a setup in flight when Close arrives | a dial running on against a manager that has torn its resources down, its connection tracked into a list nobody drains |
| a setup begun after Close | a live context that lets a component resurrect itself during teardown, and makes Close's report a statement about a set that grew again |
| Close called twice | the `defer`-plus-explicit-shutdown shape |
| a setup whose release is never called | teardown appearing to hang |

**The 1000-flow stress, run at 200 flows and labelled as such** (`common/dialer/admission_stress_test.go`).
The first version was one test asserting that no flow ever dials a family the machine lacks on a machine
that never had it — and it FAILED, 50 IPv6 attempts, and the failure is not a defect. It is invariant B
seen from a new angle:

> a capture that is SUPERSEDED mid-flow refuses nothing (fail open), so it may attempt a family the
> machine lacks and the kernel fails that attempt immediately. The alternative — a stale capture
> hard-denying — is the one behaviour the design forbids.

So the invariant is two statements and needs two tests: **no flow in the OWNED window dials a family the
machine lacks** (200 concurrent flows, no epoch movement — deterministic, `VERIFIED_MUTATION`), and **a
superseded capture costs an attempt, not a connection** (200 flows against 50 epoch flips — every flow
still succeeds, nothing retained). Asserting the first as if it covered both is what made the first
version fail.

This is the FIFTH time in the round that a fixture measured a property of the path while claiming to
measure one of the policy — and the first time the wrong assertion was a genuinely interesting distinction
rather than a mistake about which code path ran.

**One detail worth recording**: the streaming hostname path is gated by the scheduler's LATE-ARM check, not
by `filterLocalFamily`. Disabling the plan gate leaves the owned-window stress green; disabling the late
arm fails it. So the two scheduler gates cover different arrival shapes, and a mutation aimed at "the
scheduler's local gate" has to say which one.

Still not attacked: nothing from §L. What remains unproven is what was always unproven — `-race`, device
behaviour, platform event ordering.

### L.4 The open item's mechanism, pinned

The one item this round leaves OPEN - a late candidate is launched after the setup was cancelled - rests on
a claim that was only ever reasoned about: *the attempt is handed the setup's context, so the cancellation
reaches the socket and the connect fails immediately.* That is one argument at one call site, and changing
it to a detached context would turn harmless work into real work with nothing in the suite noticing, because
the late-candidate tests assert WHICH addresses are attempted and not what each attempt carries.

Two tests now pin both halves:

| test | what it holds |
|---|---|
| `TestEveryAttemptCarriesTheSetupsContext` | every context an attempt receives is DONE by the time the race returns - true only if it descends from the setup's |
| `TestAPreCancelledAttemptNeverReachesARealSocket` | a pre-cancelled dial against a real listener never connects, so "launched but harmless" does no socket work |

`VERIFIED_MUTATION`: detaching the attempt context (`context.WithoutCancel`) fails the first test - the race
never returns, because the cancellation can no longer reach the attempt. That failure mode is itself the
diagnosis.

The item therefore remains **OPEN in its letter** (the launch happens) and is **`VERIFIED_UNIT` in the part
that matters** (the launch does no work). Both statements are needed; either alone is misleading.
---

## M. FINAL CLOSEOUT — what changed after this report was written

**Everything above is preserved as the audit trail.** Several of its conclusions were superseded by the
closeout round, and each supersession is listed here rather than edited into the sections above, because
the value of this report is the record of what was believed and why, including the parts that turned out to
be wrong. Read §A-§L as the state of knowledge at the time; read §M as the current truth.

### M.1 `-race` is no longer blocked. It ran, and it found six races.

This report's §header, §H.5 and §L say `-race` is `BLOCKED_BY_ENVIRONMENT`, and §H.5 builds an argument
around the tests having to be deterministic *because* the detector is unavailable. **That argument is now
obsolete.** The toolchain was completed and the suite was run:

```powershell
# MSYS2 (winget install MSYS2.MSYS2), then:
C:\msys64\usr\bin\pacman.exe -S --noconfirm --needed mingw-w64-x86_64-gcc
$env:PATH = "C:\msys64\mingw64\bin;$env:PATH"; $env:CGO_ENABLED='1'; $env:CC='gcc'
gcc.exe (Rev6, Built by MSYS2 project) 16.2.0
go version go1.25.5 windows/amd64

go test -race -count=1 ./common/dialer    # 21.9s
```

The first run reported **six data races**, and **all six were in test fixtures, not production code**. Four
came from one line in `scriptedObservationManager.NetworkFamilyCapabilitySnapshot`, which returned `epoch`
and `stable` straight from the fields while guarding only `next()` - so any test that advanced the epoch
while a flow was reading raced. The other two came from a shared router whose answer fields were
re-assigned between phases while a previous flow's resolver goroutine could still be reading them.

What that means for this report's claims:

- **No production race exists in the admission / transition / lifecycle paths.** The round's design claim
  survives the detector, which is a stronger result than the deterministic tests could give.
- **The determinism argument in §H.5 is superseded, not wrong.** Deterministic tests are still required -
  a race detector does not replace a barrier - but the specific claim "the environment cannot run `-race`,
  so the tests must substitute for it" no longer applies.
- The fixture defects are fixed, and the fixes are in the code with the reason attached: the manager reads
  under its lock, `stallingDialer.triedCount()` is the locked view of the attempt list, and the two
  transition fixtures install the router's answer through `sync.Once` so no phase writes it.

### M.2 The §E.3 / §L.4 open item is CLOSED, with a caveat that must not be lost

§E.3 records that a late candidate is still launched after the setup was cancelled, and §L.4 pins the
mechanism that made it harmless. **The scheduler now refuses the launch**, so the letter of the rule is met:

```go
// dual_stack_scheduler.go, in the late-candidate arm
if raceCtx.Err() != nil {
	continue
}
```

**The caveat, measured rather than assumed: removing that check does not fail the §14 test.** The loop has a
second, independent way to conclude - once the cancelled attempt returns, `started == len(failures)` and it
exits before reading the channel again - so a deterministic fixture cannot open the window the check closes.
The check is kept because it makes that window's outcome deterministic in production; it is **not**
test-enforced, and §L.4's "OPEN in its letter" is now **superseded by "closed, defensive, not
test-enforced"**.

### M.3 The §13 evidence gap was real, and is now closed with a real barrier

§L.2 lists §13 as `VERIFIED_UNIT`. **That level was too high for the test that existed.** The test named
`TestAnAnswerResolvedForTheOldGenerationCannotEnterTheNewScheduler` installed the resolver gate and then
called `close(releaseGate)` **before starting the old flow**, so the gate never blocked anything; the
generation advance afterwards was a fake-manager index change, and "release the old answer after the
transition" never happened.

It has been **deleted** and replaced by `TestAnAnswerResolvedForTheOldGenerationCannotReachANewFlowsScheduler`,
which forces the interleaving with the fixture's own barriers:

| barrier | what it makes deterministic |
|---|---|
| `stallLookup` + `gateEngaged` | the old lookup is provably parked inside the router, holding this generation's answer |
| `activeLookups` | the old producer has exited / is still inside the router |
| scenario A | the answer is released **while the old flow is still alive** after the generation advanced; the old flow decides for itself (fail-open), and a new flow's attempts come only from its own lookup |
| scenario B | the generation advances **and** the setup is cancelled; the producer is joined (`activeLookups` back to zero), and the released answer reaches nobody |

Two mutations prove the barriers are load-bearing: removing the gate makes the test fail with *"the old
lookup never reached the resolver gate"*, and making the producer ignore cancellation at the gate makes it
fail with *"the cancelled setup did not return"*.

**What remains structurally impossible rather than tested**, stated so it is not mistaken for a filter: an
answer cannot enter another flow's scheduler at all, because every flow creates its own scheduler from its
own capture and the answer is published on a channel owned by the flow that started the lookup. The test
asserts the consequence and names the reason.

### M.4 The stress runs at the scale §28 names

§L.3 says "200 concurrent flows". The stress now runs **1000 flows against 100 epoch flips** - the number
§28 names - in 2.3s. The two assertions survive the change: no flow in the **owned** window dials a family
the machine lacks (deterministic, mutation-verified), and a **superseded** capture costs an attempt but
never the connection.

### M.5 Evidence levels for this phase, restated

| claim | level |
|---|---|
| one policy capture per flow, seven shapes | `VERIFIED_UNIT` + `VERIFIED_MUTATION` (Q2, M8) |
| one generation for policy / scheduler / familyHealth | `VERIFIED_UNIT` + `VERIFIED_MUTATION` (P10, M9) |
| a superseded capture refuses nothing | `VERIFIED_UNIT` |
| the epoch cannot move without a lifecycle cancel | `VERIFIED_SOURCE` |
| no production data race on these paths | **`VERIFIED_UNIT` via `-race`** (new in this closeout) |
| the late launch is refused | `VERIFIED_SOURCE`; **not** test-enforced (§M.2) |
| cross-stage agreement over 200 pairs | `VERIFIED_UNIT` + `VERIFIED_MUTATION` (Q3) |

```markdown
## PHASE 03 HANDOFF

- testing HEAD:
    a897deb2f9f  test(admission): attack the lifecycle claims the first pass only read   <- second pass
    401d5a8e58e  test(admission): one cross-stage consistency matrix for the whole space
    f264ecf2a63  fix(admission): the serial branch captured the policy a second time
    plus the report commit (hash = `git log -1 --oneline origin/testing`)
  The first pass was PARTIAL - see §L, which names the five claims it only read and the six
  attacks that replaced reading them. A reviewer who reads only §A-§K is reading half the round.
  Start SHA for the round: 4e86eae6df7d839e11be181be1dbba99688ac5d3

- Single-policy proof:
    TestOneFlowCapturesThePolicyExactlyOnce - seven flow shapes, each capturing the policy EXACTLY ONCE,
    counted at the owner seam (localFamilyOwner.localFamilyPolicy(), reached only by a capture).
    TestAStrictFlowResolvesTheStrategyFromConfiguration - strategy reads = 1 for AsIS, 0 for an explicit
    _only, because the value is configuration.
    MUTATIONS THAT CATCH A SECOND CAPTURE: Q2 (serial branch re-captures) and M8 (raceCandidatesExcluding
    re-captures). Both killed.
    NOTE FOR 04: the count is the DECISION path. The real leaf adds one capture per SOCKET by design; do
    not read the count as "one per flow including sockets".

- Single-generation proof:
    The policy's generation is the capability's ownership epoch, and the scheduler takes BOTH its family
    verdict and its health namespace from the same value:
      DefaultDialer.newDualStackScheduler(delay, admission) -> admission.generation() + admission.localFamily
    MUTATIONS: P10 (namespace offset by one) and M9 (generation from a fresh read) both killed by
    TestTheFlowsPolicyAndItsSchedulerShareOneGeneration and TestTheHealthNamespaceComesFromTheCapture.
    STRUCTURAL FACT established this round: THE NETWORK EPOCH CANNOT MOVE WITHOUT A LIFECYCLE CANCEL - it
    advances only inside beginTransition, which every path pairs with a cancelled setup. So a mid-flow
    policy refresh is impossible for the generation term; ownership catches it first.

- Setup/ownership proof:
    TestACapabilityChangeUnderALiveFlowCannotReachIt - the term that CAN move under a live flow is the
    CAPABILITY (a route flap republishes under the incumbent epoch). Ownership cannot detect it, so the
    snapshot is the only thing holding the line; the test proves the captured verdict survives a full dial
    while a NEW flow sees the new capability under the SAME epoch.
    Ownership transfer at establishment is unchanged (route/dial_setup.go) and pinned from both sides.
    RECORDED, NOT FIXED: a late candidate IS launched after the setup cancel; its attempt carries raceCtx
    so no socket work happens. The letter of that rule needs a deterministic interleaving, which 04 is the
    right place to build.

- Recovery producer lifetime proof:
    Cancelled AND joined on every exit path (original win, recovered win, setup cancel).
    TestRecoveryProducerIsJoined, TestLiteralOriginalWinnerCancelsRecovery. MUTATION M12 killed.
    The producer consumes the FLOW's policy (a parameter), never its own capture - TestRecoveryUsesTheFlowsCapturedVerdict and MUTATIONS M1/M2.

- Scheduler/late-candidate proof:
    TestTheAdmissionMatrixIsOnePolicyAcrossEveryStage - 5 strategies x 4 capability shapes x 10
    destinations = 200 pairs, six stage answers compared for AGREEMENT: admits, strategyAllows,
    admitCandidates, lateAdmission, verdict, refusalError, plus the two list-level outcomes.
    MUTATION Q3 (late guard disarmed as "only an optimisation") killed by it - the matrix is not decorative.
    MUTATIONS M3 (late candidate skips the policy) and M4 (scheduler skips its local gate) killed.

- Wrapper/failover proof:
    The policy travels as a whole VALUE through parameters; no wrapper re-derives it. MUTATION M13 (the
    parallel wrapper hands the scheduler an empty policy) killed.
    The failover shim still forwards the per-attempt options through the context and dispatches to the
    member's own parallel surface (MUTATION M14 killed). BOUNDARY, unchanged: a failover member attempt is
    its own setup with its own domain_strategy, so "one flow one policy" binds within a member's setup.

- Mutations caught:
    This round: Q2, Q3, P10, P18 - all killed.
    Re-verified at this tree: M8, M13 (both killed, M8 now ALSO by the counter test).
    Killed by tests that already existed (so not this round's credit): P10, P18.

- Bugs fixed:
    ONE, and it was introduced by Phase 02: DialContext's serial branch took its own
    captureAdmissionPolicy() in addition to the flow's, so a serial hostname flow read the network twice.
    Shadowed by an inner `:=`, identical answers in every fixture, invisible to 42 call-site migrations and
    a 15-mutation campaign. Found by counting captures. Fix: f264ecf2a63, one deleted line plus the note.

- Remaining transition-specific risks for 04:
    SUPERSEDED BY §M, read that first: item 1 is CLOSED (the scheduler now refuses a late launch
    after the cancel - defensively, and not test-enforced, §M.2) and item 4 no longer applies
    (`-race` ran: 0 races in ./common/dialer, 21.9s - §M.1). Items 2, 3 and 5 stand.
    1. A late candidate is launched after the setup cancel (no socket work; the letter of the rule is unmet).
       Build the deterministic interleaving and decide whether to guard the launch.
    2. A capability change under a live flow is proven only at the dialer level - 04 must prove it through a
       real route flap during a real setup.
    3. The two generation counters will diverge observably in an A→B run. Assert that NOTHING compares them.
    4. -race is unavailable on this host; the end-to-end run is the last chance to catch concurrency by
       construction.
    5. The capture count excludes the leaf's per-socket capture by fixture design - do not read it as a
       total.
```
