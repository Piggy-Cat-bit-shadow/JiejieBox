# TUN Fast Path — decision/lifecycle adversarial debug (2026-10-24)

**CURRENT_HEAD:** `86b1cc9ee5073fd0d37eaa58fa9a1e69a3f4ffa2` (= `origin/testing` at audit start)
**BASELINE_PINNED:** NO — this round started from the live `testing` tip, not from a report SHA.
**Resolved `sing-tun`:** `replace … => Piggy-Cat-bit-shadow/sing-tun v0.0.0-20261020085711-109186a15f3b`; `go list -m -json` confirms the `Replace.Dir`. The upstream `require` line (`v0.9.7-0.20261013140216-9ffa5391a2ad`) is not the build input.
**Environment:** Windows 10 amd64, Go 1.25.5, `GOTOOLCHAIN=local`, tags from `release/DEFAULT_BUILD_TAGS_OTHERS`, `CGO_ENABLED=0`.
**Scope:** `TUN_FAST_PATH_DECISION_AND_LIFECYCLE_ONLY`. No protocol cryptography, DNS rewrite, UI, subscription or release work.

## 1. What was attacked, and what could not be

This round tried to break the construction report's claims rather than restate them. The evidence
layers are kept separate:

| layer | what it can prove | this host |
|---|---|---|
| `INTENT` | tri-state precedence, outer-first, shared leaf, selector switch | real router tests |
| `ELIGIBILITY` | FakeIP/sniff/rewrite/UoT/socket/tracker/process gates return their own verdict | real metadata tests |
| `EXECUTION` | which handler took the flow, whether bytes moved | in-memory TUN e2e (ordinary TUN only) |
| `OWNERSHIP` | close once, kernel-owned drain protection, socket handover, half-close | unit/race tests |
| `VALUE` | throughput/CPU on a real device | **not measurable here** |

The Linux nfqueue/L0 and accepted-handover rows are `SOURCE_CONFIRMED` at best on this host. They
are not written as device PASS anywhere below.

## 2. Historical claims, re-tested

| claim | result |
|---|---|
| ordinary TUN `ActionBypass` is data-plane equivalent to `ActionAccept` | **CONFIRMED** by the e2e negative control: the same bytes, the same single handler, the same echo with `ActionBypass` and `ActionAccept`; the dispatcher is the layer that consumes the verdict and installs accept. |
| nfqueue `NfRepeat` + mark is the only real per-flow bypass | **CONFIRMED (source)** in the resolved dispatcher; **NOT_VERIFIED on device** (no Linux CAP_NET_ADMIN). |
| L0 happens before TUN and is a different owner | **CONFIRMED (source)** in `JudgeFlow`/route-set installation; device route-table diff not run. |
| `fast_path` does not gate splice | **CONFIRMED** by the new P05 test: unset/true/false all reach the splice decision exactly once; only the bypass verdict differs. |
| `false` suppresses the implicit attempt but not ordinary direct | **CONFIRMED** by `TestDirectFastPathSelectorExplicitFalseSuppressesTheAttempt` and `TestFastPathFalseOnlySuppressesTheIntrinsicShape`. |
| `PreMatchBypass` is not a kernel-bypass claim | **CONFIRMED (source)**: the e2e records `ActionBypass` and still observes the userspace handler on ordinary TUN; the diagnostics report the decision, not kernel bytes. |
| accepted handover proves kernel byte movement | **REFUTED as a claim**: the diagnostics record `accepted` only; the memory platform refuses, so no accepted row exists here. |

The old `D0_VALUE = NO_DISTINCT_FAST_PATH` is still the correct product conclusion: there is no
second data plane to switch on in ordinary TUN. This round adds no production path.

## 3. Intent matrix (I01–I15)

All rows are real router tests against the production group doubles; the file mapping is below.

| ID | condition | expected | test |
|---|---|---|---|
| I01 | single-leaf direct | no group field applies; direct behaves as before | `TestDirectIsTheOnlyProductionBypassableOutbound` |
| I02 | selector→direct, unset | intrinsic attempt allowed | `TestDirectFastPathSurvivesTransparentSelectorWithNoPolicy` |
| I03 | selector→direct, true | attempt allowed, eligibility unchanged | `TestDirectFastPathSelectorExplicitTrueTakesExactlyOneDecision` |
| I04 | selector→direct, false | attempt withheld, direct still connects | `TestDirectFastPathSelectorExplicitFalseSuppressesTheAttempt` |
| I05 | urltest→direct, tri-state | same as selector | `TestFastPathUnsetAndTrueAreIndistinguishable`, `TestDirectFastPathSurvivesNestedTransparentGroups` |
| I06 | outer true, inner false | outermost wins | `TestDirectFastPathNestedExplicitPolicyWinsOverIntrinsic` |
| I07 | outer false, inner true | outermost wins | same test, reversed case |
| I08 | shared leaf, one true one false | per-flow, no leaf-tag cache | `TestSharedDirectLeafKeepsPerGroupPoliciesIncludingFalse` |
| I09 | concurrent selector switch | no cross-group grant, no double dial | `TestSharedDirectLeafConcurrentPoliciesDoNotCross`, `TestFastPathFalseSurvivesASelectorSwitch` |
| I10 | selector→loadbalance→direct | opaque chain refuses | `TestDirectFastPathOpaqueGroupStillRefusesUnsetPolicy` |
| I11 | proxy leaf (VLESS/MASQUE/Mieru) | never native direct bypass | `TestDirectFastPathStillRefusesWhenDirectItselfRefuses`, production equivalence |
| I12 | false + AI-like tag | false wins | `TestFastPathFalseOnlySuppressesTheIntrinsicShape` |
| I13 | AI tag + non-bypassable leaf | refused | `TestDirectFastPathIsNotGrantedToATagNamedDirect` |
| I14 | field on leaf/loadbalance | config rejected by the existing parser | `option/fast_path_test.go` |
| I15 | future bypassable non-direct double | not granted by the direct intrinsic | `TestDirectFastPathAIMatcherStillDecidesForBypassableNonDirectLeaves` |

### Mutation evidence

Two disposable-worktree mutations (removed afterwards; no permanent test switch):

| mutation | expected red test | actual |
|---|---|---|
| M1: move `directFastPathLeafIsIntrinsic` before `resolveFastPathPolicy` | `TestDirectFastPathSelectorExplicitFalseSuppressesTheAttempt`, `TestExplicitFastPathFalseSuppressesTheDirectChain` | **FAIL**, both with "the explicit decision must win over the intrinsic grant" |
| M2: delete the `canCarryFastPathPolicy` loop from the intrinsic shape | `TestDirectFastPathOpaqueGroupStillRefusesUnsetPolicy` | **FAIL**, both opaque cases |

This is the falsification the first report's claims needed: the tests are not green because the code
is unobservable.

## 4. Eligibility matrix (E01–E10)

| ID | condition | evidence | status |
|---|---|---|---|
| E01 | FakeIP | `fast_bypass_verdict_matrix_test.go` (`BypassRefusedFakeIP`) | PASS |
| E02 | sniffed domain / resolved candidates | matrix rows | PASS |
| E03 | destination rewrite / original destination | matrix rows | PASS |
| E04 | UoT / UDP connect / domain unmapping / custom timeout | matrix rows | PASS |
| E05 | bind/interface/mark/strategy/family | matrix rows (`NetworkOptions`, `OutboundSemantics`) | PASS |
| E06 | network generation | `conn_reclaim_test.go` generation/drain tests | PASS (lifecycle, not a bypass gate) |
| E07 | TLS fragmentation/spoof | matrix row `BypassRefusedTLSOptions` | PASS |
| E08 | tracker / power report | matrix rows | PASS |
| E09 | process metadata missing | `process_transient_miss_test.go`, matrix row | PASS |
| E10 | platform cannot execute | `e2e` shows `ActionBypass` consumed as accept; `BypassAllowed` is not "accelerated" | PASS (ordinary TUN); nfqueue NOT_VERIFIED |

No gate can be satisfied by `fast_path: true`: the matrix is exercised through `canFastBypass`, and
the authorisation is a separate input (`fastPathAuthorized`), never an override.

## 5. Path truth (P01–P08)

| ID | what was tested | result |
|---|---|---|
| P01 | same TCP/UDP bytes under `ActionAccept` and `ActionBypass` | **PASS**: one handler, identical echo, identical byte count (`e2e/tun_fast_path_dataplane_test.go`) |
| P02 | Linux nfqueue verdict/mark | **SOURCE_CONFIRMED / DEVICE_NOT_VERIFIED** (no CAP_NET_ADMIN) |
| P03 | L0 route-set “never enters TUN” | **SOURCE_CONFIRMED / DEVICE_NOT_VERIFIED** (no route-table diff) |
| P04 | Flow/Port ownership and preview/commit | **PASS** for the production direct ICMP path; the commit re-resolves the chain, and `splice_socket_owner_test.go` covers attach/detach once |
| P05 | splice is independent of `fast_path` | **PASS** — new `TestSpliceAttemptIsIndependentOfFastPath` (unset/true/false, 1 attempt each) |
| P06 | accepted handover vs kernel bytes | **NOT_VERIFIED**: memory IO refuses; the refusal and fallback bytes are proven |
| P07 | copyDirect/sendfile owner | **SOURCE_CONFIRMED**; the copy path is the fallback owner, not a bypass candidate |
| P08 | explicit `action: bypass` | **PASS** (`TestAnExplicitBypassRuleIgnoresTheFastPathGate`), including its deliberate gate bypass |

## 6. Lifecycle attacks (L01–L30)

The full 30-case matrix was not run — most require accepted handover, nfqueue or a real device. What
was executed is listed; the rest is `NOT_VERIFIED` with the missing condition.

| cases | status |
|---|---|
| L01 (bypass verdict → accept), L08 (system-proxy source), L10 (opaque/mismatched target), L13 (cached bytes then splice false) | **PASS** — e2e + `splice_eligibility_test.go` + `TestProxiedSpliceRejectionDeliversAMultiSegmentPayload` |
| L02/L15/L24/L25 (Port/socket close once, UDP NAT close) | **PASS** in unit tests (`splice_socket_owner_test.go`, `tun_splice_dataplane_test.go`); L24/L25 device timing NOT_VERIFIED |
| L04/L05/L06/L07 (setup gate vs generation, Close race) | **PASS** in `dial_setup*_test.go` / `conn_reclaim_test.go`; exact device timing NOT_VERIFIED |
| L16/L17 (kernel-owned drain/shutdown) | **PASS** in `conn_reclaim_test.go` (`TestTransitionProtectsAKernelOwnedConnection`, `TestShutdownStillClosesEverything`); accepted handover itself NOT_VERIFIED |
| L18/L19 (half-close, RST/close/network race) | **PASS** in `conn_halfclose*_test.go`; device timing NOT_VERIFIED |
| L20 (keepalive unsupported) | **PASS** — `armSplicedLiveness` logs and leaves the stack default; no claim of observation |
| L09/L11/L12/L14 (multi-level wrappers, cache close/failure, handover+copy race) | **PASS** for L12 (`TestCachedDrainStopsAtTheFirstWriteFailure`); L09/L11/L14 source-confirmed by the wrapper and handover tests |
| L03/L21/L22/L23 (commit switch, UDP FakeIP/NAT, family mismatch, batch order) | **SOURCE_CONFIRMED**; UDP batch refusal is covered by the packet path tests, not by a deterministic multi-datagram fixture |
| L26–L30 (TUN restart, L0 callback, nfqueue toggle, tracker at handover, pause/OOM) | **NOT_VERIFIED** — require device/nfqueue or platform pause; recorded as environment limits |

The strongest production-level statement this host supports is: **the refused/fallback half has one
owner and intact bytes; the accepted half has one owner by code and unit tests but no device proof.**

## 7. Fallback truth table

| failure point | safe fallback? | evidence |
|---|---|---|
| eligibility `false` | yes | ordinary route/connection unchanged; `fast_path:false` e2e |
| platform cannot splice | yes | `splice_rejected`, copy loops deliver 16 KiB byte-identical |
| source/target not socket-capable | yes | `source_not_go_conn` / `splice_target_*` reasons; no owner transfer |
| cached write fails mid-drain | **no** — terminal | code returns the error and does not replay; `TestCachedDrainStopsAtTheFirstWriteFailure` (writer sees exactly two writes, the failed one is not retried) |
| splice accepted | **no further userspace fallback** | success returns `markKernelOwned`; only close/keepalive paths remain |
| nfqueue already released the packet | not applicable to userspace fallback | kernel owner; not verified here |
| Port already owns the flow | no preview fallback | Port contract; attach/detach unit tests |

## 8. Diagnostic truth table

| field | source | updated | owner | granularity | can prove | cannot prove |
|---|---|---|---|---|---|---|
| `BypassVerdict` | `canFastBypass` | per flow, pre-match | router | per flow | eligibility/refusal reason | bytes were bypassed |
| `PreMatchResult.Action` | router/dispatcher | per flow | TUN dispatcher | per flow | which action the platform was asked to take | the platform honoured it |
| `SpliceSnapshot.Attempts` | manager | once per stream | ConnectionManager | per flow | a decision was reached | bytes moved |
| `Accepted` | `GoConn.Splice` true | once on success | ConnectionManager | per flow | handover accepted | kernel actually moved bytes |
| `Reasons[...]` | refusal classifier | once per attempt | ConnectionManager | per flow | why it declined | performance |
| `managedConnState.activityObservable` | `markKernelOwned` | once on confirmed success | connection state | per connection | userspace can no longer judge idleness | kernel liveness |
| tracker counters | copy/splice counters | per write/read | tracker | per connection | bytes userspace saw | bytes the kernel moved after handover |

`Attempts == Accepted + sum(Reasons)` is the invariant the diagnostics tests assert; `accepted` is
never reported as "accelerated".

## 9. Performance

`BenchmarkCanFastBypassEligible` and `BenchmarkBypassDecision`, 3×200 ms, same host:

```text
BenchmarkBypassDecision-16        81.66–84.67 ns/op   0 B/op   0 allocs/op
BenchmarkCanFastBypassEligible-16 16.09–16.87 ns/op   0 B/op   0 allocs/op
```

This is control-plane cost, not throughput. No production path changed this round, so there is no
A/B to claim; the splice accepted/refused CPU comparison remains a device task.

## 10. New test added

`e2e/tun_splice_dataplane_test.go`:

- `TestSpliceAttemptIsIndependentOfFastPath` — P05: selector→direct with `fast_path` unset/true/false;
  each flow must reach the splice decision once, record `splice_rejected` once, and deliver its bytes.
- `TestProxiedSpliceRejectionDeliversAMultiSegmentPayload` (previous round) — 16 KiB, 1200-byte
  segments, proxied path; the refused handover must not lose or duplicate the cached stream.

`route/splice_cached_failure_test.go`:

- `TestCachedDrainStopsAtTheFirstWriteFailure` — L12: the failing buffer is not retried and the next
  buffer is not written; the error is returned immediately.
- `TestCachedDrainWritesEveryBufferInOrder` — the success path drains both buffers in order.

## 11. Repository closeout

- New: this report, the P05 test and the L12 failure test. The only production change is extracting
  `drainCachedSource` out of `spliceConnection` so the failure is unit-testable; its behaviour is
  unchanged (same loop, same counters, same terminal error).
- Commit: the commit adding these files; pushed to `origin/testing`; CI on that SHA below.
- Worktree clean; the mutation worktree was removed; submodule pointers untouched.

```text
OVERALL = PASS_WITH_LIMITATIONS
SCOPE = TUN_FAST_PATH_DECISION_AND_LIFECYCLE_ONLY
BRANCH = testing
BASELINE_PINNED = NO
PATH_VERDICT_VS_DATAPLANE = VERIFIED
POLICY_INTENT = PASS
CAPABILITY_GATES = PASS
FALLBACK_DATA_INTEGRITY = PASS
OWNER_CLOSE_ONCE = PASS
NETWORK_GENERATION = PASS
LIFECYCLE_TRANSITIONS = PASS
REAL_DEVICE_BENCHMARK = NOT_VERIFIED_ON_DEVICE
REGRESSIONS = PASS
```

Why `PASS_WITH_LIMITATIONS` and not `PASS`: the accepted handover, Linux nfqueue/L0 and every device
timing are unverified; the 30-case lifecycle matrix was executed only where the fixture can reach the state. Nothing above upgrades those rows.
