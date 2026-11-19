# PAIRWISE A — Effectiveness: does each mechanism actually reach a real action?

The question this file answers is not "is the code present" but "did the mechanism load, enter the
production path, decide, act, show an observable effect, and settle". Each row is a contract in the
form `enabled by → real entry → decision → action → externally visible effect → termination/reclaim`,
followed by the mutation control that proves the test would notice if the mechanism were bypassed.

Legend: `已确认` = source + execution, `已排除` = checked with no defect, `未覆盖` = not attacked.

---

## EC-0 The load path (is the guard even installed?)

| Step | Evidence |
| --- | --- |
| Guard is created only for a Network Extension with a process budget | `service/oomkiller/service.go:96-109` (`mode == policyModeNetworkExtension`) |
| Guard is registered on the construction context | `service.MustRegister[*resourceguard.Guard](ctx, s.guard)` — `service.go:105` |
| Outbound constructors can see it | services are created **before** outbounds: `box.go:800` (`serviceManager.Create`) precedes `box.go:837` (`outboundManager.Create`); the outbound receives that same context (`box.go:830-844`) |
| Consumers read the policy owner's decision, not the platform | `resourceguard.FromContext(ctx)` in `protocol/anytls/outbound.go:55`, `protocol/naive/outbound.go:188`, `common/tls/client.go:197`, `protocol/hysteria2/outbound.go:235`, `protocol/tuic/outbound.go:179`, `protocol/mieru/outbound.go:206`, `transport/v2rayxhttp/client.go:117`, `transport/v2rayquic/client.go:372` |
| No second measurement | the guard's pressure source is `Service.guardLevel`, which reads the same atomic the TUN stack consumes (`service.go:113-129`) |

`已确认`: on a non-extension build the guard is nil and every seam is a nil check
(`common/resourceguard/guard.go:298-311`), so the mechanism is *loaded* only where a budget exists.

## EC-1 OOMKiller pressure → admission → a real creation stops

Contract: `Service.pressure` (published by the OOMKiller timer) → `guardLevel` → `Guard.AcquireFor` →
`Gate` plan → a protocol's creation seam refuses **before** the expensive work.

Execution: `service/oomkiller/guard_reaches_creation_test.go`
`TestPublishedCriticalPressureStopsARealExpensiveCreation`. The seam under test is the production TLS
dialer (`tls.defaultDialer.DialTLSContext` → `handshakeWithAdmissionFor` →
`Gate.Acquire`), and the expensive work is the TCP creation behind it, observed by a recording
dialer — an independent result, not one of the gate's own counters.

| Step | Observed |
| --- | --- |
| Normal, one permit held (limit 2) | the TLS dial is admitted; the recording dialer is called once |
| pressure → `MemoryPressureCritical` (the exact value `timer.go:1171` stores) | derived limit is 1, so the same held permit fills the gate |
| second TLS dial | `ErrQueueFull`, `resourceguard.IsLocalRejection(err) == true`, **recording dialer not called** |
| report | `Stats()` reports `level critical`, `limit 1`, `refused 1` — the same plan the refusal used |
| pressure → `MemoryPressureNone` | guard demotes after its dwell; the next TLS dial is admitted again and the dialer is called (count 2) |

`已确认` — including recovery, which is the half that is usually missing.

**Mutation control MC-1.** `Service.guardLevel`'s `MemoryPressureCritical → LevelCritical` branch was
replaced with `LevelNormal` (the "old state never reaches the gate" mutation) and the test was run:

```
--- FAIL: TestPublishedCriticalPressureStopsARealExpensiveCreation
    expected: "resource guard: admission queue is full"
    in chain: "the expensive creation was started"
```

i.e. the test detects the exact failure mode "the governor is installed but a critical process keeps
creating". Reverted immediately; the mutation is not in any commit.

## EC-2 The recovery edge reaches the queue

Contract: pressure falls → `Guard.apply` → `Gate.setPlan(wider)` → queued callers are admitted.

Execution: `common/resourceguard/gate_recovery_edge_test.go`
`TestAPlanRaiseHandsRecoveredCapacityToTheQueue` (one holder, three queued, plan raised to 4, holder
never released) and `TestAPlanShrinkDoesNotDisturbTheQueueOrTheInFlightWork` (the guard-rail).

`已确认` after F-2's fix. **Mutation control MC-2:** removing the single `g.grantAllLocked()` line from
`setPlan` makes the raise test fail with the state the bug produces —
`limit=4 inflight=1 waiters=3`.

## EC-3 The Critical transition runs reclaim hooks — and what they are allowed to do

Contract: `Guard.apply` → `runShed` → each protocol's registered action, on the transition into
`LevelCritical`, exactly once per transition (versioned by `appliedVersion`/`transitions`).

| Owner | Registered action | Is it idle-only? | Consequence |
| --- | --- | --- | --- |
| anytls | `client.CloseIdleConnections` (`outbound.go:116`) | yes — the SDK skips a session with streams (`sing-anytls client.go:412-434`) | `已确认`: safe and idempotent |
| trusttunnel | `client.CloseIdleConnections` (`outbound.go:153`) | yes — returns early when `streams > 0` (`transport/trusttunnel/client.go:868-878`) | `已确认`: safe and idempotent |
| naive | `client.CloseAllConnections` (**removed**) | **no** — terminates a live CONNECT (`cronet-go/test/integration_test.go:913-918`) | F-1: fixed |

Execution for F-1: `protocol/naive/shed_contract_test.go`
`TestNaiveShedHookNeverCutsALiveTunnel` forces the transition and requires the engine's close
primitive to be untouched. **Mutation control MC-3:** re-adding `guard.RegisterShed(...)` inside the
production helper turns it red (`expected 0, actual 1`).

Second half of the same contract: `TestNaiveDoesNotClaimAnIdleOnlyReclaimAction` pins that the naive
outbound does **not** satisfy `adapter.IdleConnectionReleaser`, so no future reader believes the trim
walk reaches that pool.

## EC-4 A shut-down carrier does not create

Contract: `Scope` cleanup → `CloseWithError(os.ErrClosed)` → the carrier is closed for good → a later
dial is refused **at the creation site**.

Execution: `protocol/hysteria2/family_teardown_test.go`, `protocol/tuic/family_teardown_test.go`
— `TestAShutdownCarrierNeverCreatesAClientAgain` asserts both the error (`os.ErrClosed`) and, more
importantly, that the client-creation callback was never invoked;
`TestANetworkChangeLeavesTheCarrierReusable` asserts the network-change half still creates.

**Mutation control MC-4:** disabling the `errors.Is(err, os.ErrClosed)` branch in
`hysteria2/family.go` makes the shutdown test fail with the creation path having been entered
(`in chain: "hysteria2: no per-family client could be created"`).

## EC-5 Admission is charged per physical creation, not per request

Contract from `common/resourceguard/admission.go:10-32`: a creation-only hook; a reuse is not asked and
not charged; a nested dial holds one permit (`HeldPermit`).

| Seam | Placement | Verified by |
| --- | --- | --- |
| AnyTLS | inside the SDK's `createSession`, after the idle-reuse attempt | `protocol/anytls/admission.go:15-48`; `admission_reuse_test.go` |
| TLS (trojan/vless/vmess/DoT/HTTP transports) | around the handshake, skipped when a permit is already held | `common/tls/client.go:185-216`; `marker_scope_test.go` |
| naive | at the Cronet socket callback (`Cronet_Engine_SetDialer`), not at `DialEarly` | `protocol/naive/native_creation.go:19-60,269-288`; `native_creation_test.go` |
| QUIC clients (hy2/tuic/masque families) | client creation | `protocol/hysteria2/admission.go`, `protocol/tuic/admission.go`, `transport/v2rayquic/client.go:372` |
| Mieru | underlay dial (`serverDialer.DialContext/ListenPacket`) | `protocol/mieru/dialer.go:89` |
| XHTTP/H3 | H3 transport establishment | `transport/v2rayxhttp/client.go:117-122`, `common/httpclient/client.go:27-32` |

`已确认` by source and by the existing suites (all re-run in this round; see
`PAIRWISE_A_VERIFICATION.md`). One negative result worth recording: **the TUN UDP path crosses none
of these seams** for a `direct` outbound or an already-established QUIC client (`PAIRWISE_A_FINDINGS.md`
R-4), so "the gate covers every expensive creation" is false for that path, on every platform.

## EC-6 The two reclaim walks are one authority — or two?

| Walk | Serialisation | Reaches |
| --- | --- | --- |
| OOMKiller idle/crisis pass | `adaptiveTimer.releaseAccess`, one executor goroutine (`timer.go:744-820`), crisis re-checks the armed line before acting (`timer.go:963-999`) | `NetworkManager.TrimMemory/ReleaseMemory` → `ReferenceManager.retireIdleResources` (`reference.go:619-625,658-677`) |
| Guard shed hooks | none; runs on the goroutine that observed the transition (`guard.go:397-416`) | the protocol clients directly |

`已确认` as a fact: the hook path is a **second executor** running concurrently with the first. The
comment in `runShed` claims this keeps "one reclaim authority"; it does not — what it keeps is one
*transition* per episode. `已排除` as harmful for anytls/trusttunnel because the action is idle-only
and takes the client's own lock, and it is the same call the walk makes; the harm was concentrated in
the one hook that was not idle-only (F-1, now removed). No change was made to the layering in this
round: routing hooks through the OOMKiller's executor is a design decision, not a debug finding.

## EC-7 Does a test that nobody runs still protect anything?

Contract in this repository's own terms (`AGENTS.md` §evidence): "a CI job declaration is not evidence
that the job ran". This round found the other half of that rule: a **test** that runs nowhere is not
evidence of anything either.

| Package | Executed by, before this round | What that proved |
| --- | --- | --- |
| `transport/v2rayxhttp` | **nothing** — `server-linux-amd64.yml` only runs `go list -deps` on it, which proves it is COMPILED into the binary | its two pool-lifecycle tests were red at `7654f1a77` and every gate stayed green (F-4) |
| `protocol/hysteria2`, `protocol/tuic` | only `go list -deps`; the SPEC 104-style tests exist but no push-time job ran them | F-3's teardown defect was untested and unseen |
| `common/resourceguard`, `common/tls`, `protocol/anytls` | `resource-guard.yml` race leg + `verify.yml` | already the best-covered part of this window |

Actions taken: `transport/v2rayxhttp/...` joined the push-gate race step in `verify.yml` (measured
~23 s under `-race` with the Darwin tag profile), and `protocol/hysteria2`, `protocol/tuic` and
`service/oomkiller` joined `resource-guard.yml`, with path filters so a change to those files runs the
job that proves the fix. `protocol/naive` is built only under `with_naive_outbound`, which the Linux
push gate does not link; its new tests run in the Darwin-tag legs.

## EC-8 What is *not* effective, and is now recorded rather than assumed

| Claim that reads as "covered" | Reality | Where recorded |
| --- | --- | --- |
| "The OOMKiller protects the process from UDP flow bursts" | The only bound on TUN UDP flows is the NAT entry cap; nothing admits before the per-flow objects exist, and eviction does not cancel the dial it launched | R-4, R-5 |
| "Idle pools are released under pressure" | naive (no idle-only primitive) and mieru (no releaser) are outside both walks | F-1 limitation, R-3 |
| "A pre-sleep session never takes new work" | anytls/hy2/tuic expose no `ReuseSuspect`, so the boundary degrades to idle-only | R-2 |
| "The xhttp pool reuses an idle connection" | It does; the two tests that said otherwise were measuring the transport's cold start, where the un-ordered downlink GET and first upload POST can take a connection each | F-4 |
