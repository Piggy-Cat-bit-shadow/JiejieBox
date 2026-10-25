# Cross-platform Fast Path unification — adversarial debug (2026-10-25)

This pass attacks `8ef6231bd` (the round that replaced `implicitDirectFastPathIsExecutable` with an
entry-owned capability). It answers one question: **when the gate stopped running on non-consumer
entries, did anything that mattered stop running too?**

Answer: no production regression was found. The capability is a per-call field with exactly one
setter and one reader, and the three ways it could have gone wrong - Router-level state, a field of
the inbound, a platform constant - are each driven by a test that goes red under the corresponding
mutation. The pass did add two tests and two benchmarks, and it found one pre-existing flaky test and
one property of the explicit-bypass branch that is worth recording (both below).

`PRODUCTION_FIXES = 0`: the production code under review was not changed by this pass.

## 0. The tree this was run against

| item | value |
|---|---|
| observed HEAD at the start | `6503ba2c15f572f32493572aa0816207581cb136` |
| production commit under review | `8ef6231bd` (`refactor(route): let the bypass consumer own the fast-path decision`) |
| sing-tun pin | `github.com/Piggy-Cat-bit-shadow/sing-tun v0.0.0-20261024234618-a0f9f68fec7d` (replace) |
| build tags | `release/DEFAULT_BUILD_TAGS_OTHERS` |
| client submodules | initialized read-only at the recorded SHAs (`clients/android ec61030d`, `clients/apple cdaaf222`, `clients/desktop eb2dbf8b`) for the source audit |
| what this pass changed | two test files (below); no production code |

Added by this pass:

* `protocol/tun/judge_flow_consumer_test.go`: `TestTwoEntriesSharingOneRouterDoNotLeakTheCapability`
  (CPD-12);
* `route/fast_path_consumer_bench_test.go`: the two per-flow decision benchmarks.

## 1. The diff, function by function, with its consumers

| function / symbol | before | after | who calls it | does the verdict have a consumer | evidence |
|---|---|---|---|---|---|
| `adapter.InboundContext.ConsumesBypassVerdict` | — | new field, default false | read by `route/preMatchFlow` | n/a | `adapter/inbound.go` |
| `protocol/tun.(*Inbound).JudgeFlow` | the body | wrapper: `judgeFlow(..., false)` | sing-tun `tun.NewStack(Handler: t)` (go, gVisor and system stacks) | **no** | `TestTheTUNStackDoesNotAskForABypassVerdict` |
| `protocol/tun.(*Inbound).judgeFlow` | (the body) | body + capability parameter | the two entries | n/a | DNS/FakeIP/L0 order unchanged |
| `protocol/tun.(*autoRedirectHandler).JudgeFlow` | delegated to `JudgeFlow` | `judgeFlow(..., true)` | sing-tun `nfqueueHandler` (`nfqueue_linux.go:310`) and the platform auto-redirect service | **yes** (`applyVerdict:334`, `NfRepeat` + output mark) | `TestTheAutoRedirectEntryAsksForABypassVerdict` |
| `route.(*Router).preMatchFlow` | `if implicitDirectFastPathIsExecutable` | `if metadata.ConsumesBypassVerdict` | `Router.PreMatch`, from `adapter.JudgeFlow` (TUN) and `protocol/cloudflare` (ICMP) | only the TUN auto-redirect entry | `TestTheImplicitFastPathIsNotComputedWithoutABypassConsumer` |
| `route.implicitDirectFastPathIsExecutable` | `!C.IsIos` | deleted | — | — | `grep` over `*.go` returns nothing |
| `scripts/ci/verify-ios-fast-path-guard.sh` | assembly gate | deleted | — | — | `verify.yml` has no `fast-path` step |

The one caller that is easy to forget is `protocol/cloudflare/inbound.go:234`. It calls
`Router.PreMatch` for ICMP flows with `InboundType: C.TypeCloudflared`, and the gate refused that
inbound type before this round (`BypassRefusedInboundType`, `route/bypass_verdict.go:25`), so a
cloudflared flow returned `PreMatchContinue` before and returns it now - the difference is that it
no longer walks the chain to discover that. No behaviour change; `route` has no test that pins it, so
I state it as SOURCE_ONLY rather than PASS.

## 2. Where the capability lives, and the three ways it could have been wrong

The capability rides on the per-flow `adapter.InboundContext`. There is exactly one producer
(`judgeFlow`), exactly one reader (`preMatchFlow`), no cache, no map, and nothing on the Router,
the inbound, the group or the leaf.

| wrong design | what it would break | what catches it |
|---|---|---|
| a **Router-level** flag | a plain TUN flow borrowing the native answer of a concurrent native flow on the same Router (and vice versa) | `TestTwoEntriesSharingOneRouterDoNotLeakTheCapability` - one real router, two inbounds, four interleaved judgements per round |
| a **field of the Inbound** | in `auto_redirect` mode the same Inbound serves BOTH entries (`tun.NewStack(Handler: t)` and `Handler: (*autoRedirectHandler)(t)`), so the stack would inherit the native claim | the same test drives one inbound through both entries in the same loop |
| a **platform constant** (`C.IsLinux`, `C.IsAndroid`, ...) | the two modes of one Linux/Android binary cannot be told apart; `C.IsDarwin` silently kills macOS | negative control NC3: `consumesBypassVerdict = C.IsLinux` turns the native test red on this Windows host |

That middle row is the reason the field is per call and not per object, and it is not hypothetical:
`protocol/tun/inbound.go` builds the stack with `Handler: t` and the auto-redirect service with
`Handler: (*autoRedirectHandler)(inbound)`, both from the same object.

## 3. CPD-01 … CPD-40

Legend: PASS means executed on this host or in CI with the named test; SOURCE_ONLY means the code was
read and the in-repo path is pinned by another test, but the platform wrapper was not executed;
NOT_EXECUTABLE means the environment cannot run it (no device, no root); NOT_APPLICABLE means the
entry does not exist in this tree. Where a row has two halves, both labels are given.

| ID | status | evidence |
|---|---|---|
| CPD-01 | PASS (stack entry) / SOURCE_ONLY (iOS NE wrapper) | the stack entry is `Inbound.JudgeFlow`, executed on this host by `TestFastPathVerdictDoesNotChangeTheTUNDataPath`, `TestBypassVerdictStillDeliversUDP`, `TestFastPathMultiSegmentPayloadIsIntact`; iOS cannot enter `auto_redirect` (see CPD-03), so the gate cannot run there |
| CPD-02 | PASS (path exists) / SOURCE_ONLY (iOS HTTP proxy) | a system-proxy flow goes `ConnectionManager` -> `Router.RouteConnection`, never `PreMatch`; `TestSystemProxyTCPSpliceIsRefusedAtTheSource` pins that it is not a `GoConn` |
| CPD-03 | PASS | macOS is the same stack entry (tested here); the Apple wrapper is doubly disabled: `ExtensionPlatformInterface.usePlatformAutoRedirect() -> false` and `createAutoRedirect` throws (`clients/apple/Library/Network/ExtensionPlatformInterface.swift:857-864`), and the Go side is `redirect_platform_other.go` (`!linux`) returning "only supported on Linux" |
| CPD-04 | NOT_APPLICABLE | this tree has exactly two flow-judging entries (stack, auto-redirect); there is no second macOS userspace TUN mode to audit |
| CPD-05 | PASS | Windows is this host: the e2e data-plane suite runs the real sing-tun stack here; `CanBypass` is not called for the stack entry (`TestTheImplicitFastPathIsNotComputedWithoutABypassConsumer`) |
| CPD-06 | PASS (source) / NOT_EXECUTABLE (Wintun driver) | the gVisor and system stacks are pure Go in this tree and are dispatched by the same `Handler` interface; no Wintun driver is installed here, so the driver path is not executed |
| CPD-07 | PASS | `e2e/route_metadata_chain_test.go` runs the Mixed/system-proxy shaped entries against the real router; no `PreMatch` call site exists on that path |
| CPD-08 | PASS (CI) / PASS (host) | Verify runs `go test -race ./route/... ./protocol/tun/... ./adapter/...` on ubuntu (`40157380698`); the same packages run here on Windows |
| CPD-09 | NOT_EXECUTABLE (live nfqueue) / SOURCE_ONLY (consumer contract) | nfqueue needs CAP_NET_ADMIN; the entry contract is pinned by `TestTheAutoRedirectEntryAsksForABypassVerdict`, and the consumption by `TestDispatcherTreatsBypassAndAcceptIdentically` (real `ForwardStage`) plus `nfqueue_linux.go:334` read |
| CPD-10 | SOURCE_ONLY | group precedence on the native entry uses the same `fastBypassMetadata` (bit set) that 105 route assertions do; `TestFastPathUnsetAndTrueAreIndistinguishable`, `TestDirectFastPathSelectorExplicitFalseSuppressesTheAttempt` |
| CPD-11 | SOURCE_ONLY | `TestBypassChainAndLeafConditionsRefuseWithTheirOwnVerdict`, `TestEveryBypassConditionRefusesWithItsOwnVerdict`; a MASQUE/VLESS leaf has no `CanBypass` |
| CPD-12 | PASS | `TestTwoEntriesSharingOneRouterDoNotLeakTheCapability` (new): one real router, two inbounds, both entries, four rounds, verdicts alternate `Accept/Bypass` with no carry-over |
| CPD-13 | PASS | `TestDirectFastPathSelectorsSwitchDoesNotLeakTheIntrinsicGrant`, `TestFastPathFalseSurvivesASelectorSwitch`; the policy map is read-only per flow and the leaf is never written |
| CPD-14 | PASS | `TestFastPathTransparentGroupsAreTheOnlyPolicyCarriers` (loadbalance refuses), loadbalance retry tests in `route` |
| CPD-15 | PASS | `TestTheImplicitFastPathIsComputedForABypassConsumer` / `...NotComputedWithout...`: a bare direct leaf is eligible for the consumer and not computed for the stack |
| CPD-16 | PASS + NC4 | `TestAnExplicitBypassRuleIsIndependentOfABypassConsumer`, `TestAnExplicitBypassRuleIgnoresTheFastPathGate`, `e2e/route_metadata_chain_test.go::bypass_verdict_has_no_port` |
| CPD-17 | PASS | `TestBypassVerdictCarriesNoPort`, `TestDirectFastBypassVerdictHasNoPort{,UDP}`, `TestDispatcherConsumesOnlyAFlow` |
| CPD-18 | PASS | `TestBothEntriesShareTheSameFlowPolicy` (hijack identical on both entries, router not reached), `TestDNSHijackAddressBeatsDirectFastPath`, `TestDNSByPortDisabledUDP53FollowsRouteAddressSetPolicy` |
| CPD-19 | PASS | `TestFakeIPIsNotBypassedByRouteAddressSet` and the FakeIP guard tests in `route` and `protocol/tun`; the guard is inside `judgeFlow` before the router, unchanged |
| CPD-20 | PASS (in-TUN decision) / NOT_EXECUTABLE (real OS routes) | `TestBothEntriesShareTheSameL0Policy`, the L0 suite; the e2e fixtures use a memory TUN, so no OS route is installed |
| CPD-21 | PASS | `TestRouteOptionsDisableTheFastPath`, `route/process_transient_miss_test.go`; the gate is the only thing removed from `preMatchFlow`, the rule loop runs unchanged |
| CPD-22 | PASS | `TestFastBypassTrackerIsIneligible`, `connection_lifecycle_test.go` (FIN/reset/close-all/shutdown), `flow_tracker_serialization_test.go` |
| CPD-23 | PASS + NC7 | `common/trafficclass` suite; cutting `MatchTag` turns `TestMatchTagMatrix`, `TestMatchTagDoesNotDependOnPositionOrCase` and the fuzz target red, while the fast-path code is untouched |
| CPD-24 | PASS | `TestSpliceAttemptIsIndependentOfFastPath`, `TestDirectTUNFlowTakesTheSpliceDecisionAndFallsBack` |
| CPD-25 | PASS | `TestProxiedTUNFlowStillHasAGoConnSource`, `TestProxiedSpliceRejectionDeliversAMultiSegmentPayload` |
| CPD-26 | PASS (delivery) / NOT_APPLICABLE (ordering unit) | `TestBypassVerdictStillDeliversUDP`; `SplicePacketOptions.Cached` ordering is owned by sing-tun and has no in-repo unit test |
| CPD-27 | PASS | `TestFastPathMultiSegmentPayloadIsIntact` (8 KiB), `TestProxiedSpliceRejectionDeliversAMultiSegmentPayload` |
| CPD-28 | PASS + NC5 | `TestCachedDrainStopsAtTheFirstWriteFailure`; NC5 (`continue` instead of `return`) turns it red |
| CPD-29 | PASS (ownership unit) / NOT_EXECUTABLE (real accepted splice) | the memory TUN refuses handover, so the accepted branch is not executed here; `conn_halfclose*`, `splice_*` and `conn_reclaim_test.go` cover the ownership rules, and `TestDirectTUNFlowTakesTheSpliceDecisionAndFallsBack` covers the refusal |
| CPD-30 | PASS + NC6 | `TestHalfClosedPeerGoneIsReleased`, `TestHalfClosedFlowCloseIsOnceUnderRace`, `TestHalfCloseVerdictAndTeardownRaceSettleOnce` |
| CPD-31 | PASS (in-repo) / NOT_EXECUTABLE (device) | `TestTransitionDrainsAnActiveConnection`, `TestTransitionProtectsAKernelOwnedConnection`, `TestShutdownStillClosesEverything`, `TestHandoverRepeatedGenuineChangesEachClaimOnce` |
| CPD-32 | SOURCE_ONLY | `clients/android` `BoxService.kt` (start/stop/revoke handling) read; no device, so the fd/Scope close ordering is not executed |
| CPD-33 | SOURCE_ONLY | `VpnService.protect` is called from the Android `PlatformInterface` implementation in the client; no device |
| CPD-34 | SOURCE_ONLY (chain complete) | `Settings.autoRedirect` -> `BoxService.kt:156` -> `OverrideOptions.autoRedirect` -> `experimental/libbox/command_server.go:206` -> `daemon/instance.go:99` (`&& AutoRoute`) -> `option.TunInboundOptions.AutoRedirect` -> `protocol/tun/inbound.go:453` -> `tun.NewAutoRedirect(Handler: (*autoRedirectHandler)(inbound))`; the libbox platform stub returns `UsePlatformAutoRedirect() == false`, so the consumer is the in-Go nfqueue. The capability is set exactly on this entry |
| CPD-35 | SOURCE_ONLY | `clients/android` `ProxyService`/per-app read; no `fast_path` or `ActionBypass` reference exists anywhere under `clients/` (`grep` empty), so this round cannot reach them |
| CPD-36 | PASS | `GOOS=windows/darwin/android/linux` `go vet ./route/ ./protocol/tun/ ./adapter/ ./option/` clean (vet compiles the test files too); `GOOS=ios` and `GOOS=ios -tags <tags>,tvos` `go build` clean - iOS `go vet` is NOT_EXECUTABLE because vet links a test binary and needs an Apple toolchain |
| CPD-37 | SOURCE_ONLY | no dedicated 1000-flow test was written; the capability is one bool read once per flow from a value-copied struct, and `TestMultiFlowTrackerSerializesAttachAndClose` plus the `-race` step over `./route/... ./protocol/tun/...` are the concurrency evidence |
| CPD-38 | PASS (in-repo) / NOT_EXECUTABLE (device) | `TestTheUpdaterDoesNotOutliveItsClose`, `route/rule_set_updater_lifecycle_test.go`, `TestTUNDeviceResetReleasesTrackedFlow`, the Scope tests; repeated real Start/Stop of a NE/VpnService needs a device |
| CPD-39 | PASS | `option/fast_path_test.go`, `box_fast_path_group_policy_test.go`, `TestDirectFastPathSurvivesNestedTransparentGroups`, `TestExplicitFastPathFalseNeedsAGroup` |
| CPD-40 | PASS (compile) | `GOOS=ios GOARCH=arm64 go build -tags "<tags>,tvos" ./route/ ./protocol/tun/` clean |

### 3.1 Android sub-matrix (AD01 … AD36)

`clients/android` is now present at `ec61030d`, so the client-side claims are source-level. No Android
device or emulator is available, so every runtime row is capped at SOURCE_ONLY. The rows collapse into
five families; each keeps its own ID so nothing is silently dropped.

| IDs | status | evidence |
|---|---|---|
| AD01, AD02, AD05 | SOURCE_ONLY | the Android VpnService path is the same `Inbound.JudgeFlow` -> `adapter.JudgeFlow`; that code is executed on this host by the e2e dataplane tests, and the Android wrapper only supplies the fd. IPv4-mapped canonicalisation is inside `judgeFlow` before the router (`TestMappedDestinationReachesTheRouterCanonical`) |
| AD03, AD04, AD06, AD08 | SOURCE_ONLY | group policy lives in `route/fast_path.go` and is entry-independent; the same tests that pin it for the consumer run here; the Android wrapper does not touch it. AD06: a proxy leaf has no `CanBypass`, so the gate refuses before any dial |
| AD07, AD14, AD15 | SOURCE_ONLY (client) + PASS (in-repo) | loadbalance retry and explicit bypass are in-repo and tested; the client forwards the config verbatim |
| AD09, AD10, AD11, AD12, AD13 | SOURCE_ONLY | UDP NAT, DNS hijack, FakeIP, sniff/process order are all inside `judgeFlow`/`route`; the Android wrapper adds nothing to them. The in-repo tests for each were run here |
| AD17, AD18, AD34 | SOURCE_ONLY | AD17 is the chain in CPD-34. AD18: `C.IsLinux` is not used anywhere in this round's diff (`grep`), and Android's distinguishing behaviour is the `auto_redirect` option, not a constant. AD34: `option/fast_path_test.go` + `box_fast_path_group_policy_test.go` |
| AD19, AD20, AD21, AD22, AD23, AD35 | NOT_EXECUTABLE | fd lifetime, permission revoke, `protect` timing and Service stop are device or emulator behaviours. The client code was read (`BoxService.kt`, `PlatformInterfaceWrapper`) and shows no dependency on the fast-path field |
| AD24–AD31 | PASS (in-repo) | splice/refusal/cached/half-close/reclaim families listed in CPD-24 … CPD-31; the Android wrapper is not in these paths (`route/splice.go`, `route/conn_halfclose.go`) |
| AD16, AD32, AD33, AD36 | SOURCE_ONLY / PASS | AD16: L0 in-TUN decision is PASS, OS-route install is NOT_EXECUTABLE. AD32: `common/trafficclass` PASS. AD33: the compile matrix. AD36: no bounded stress run was added (see CPD-37) |

## 4. Lifecycle ledger

The capability adds no resource: it is one bool on a value-copied struct, read once. The rows below
are unchanged from `cross-platform-fast-path-simplification-closeout.md` and the iOS debug pass, and
are re-checked here because the entry split touches the same flow setup.

| resource | allocate | commit boundary | final owner | normal close | failure close | race winner | evidence |
|---|---|---|---|---|---|---|---|
| pre-match metadata | `judgeFlow` builds it | none (preview) | the call | stack copy only | stack copy only | n/a | `route/fast_path_consumer_test.go`, CPD-12 |
| dispatcher entry (Accept/Bypass) | `ForwardStage.install` | `installAccept` | sing-tun stack | reset | reset | n/a | `TestDispatcherTreatsBypassAndAcceptIdentically`, `TestDispatcherRemovesAnAcceptEntryOnReset` |
| GoConn / user copy | `NewConnectionEx` | `routeConnection` | `flowTeardown` | `close` once | `close` once | `sync.Once` + `ran` | `TestHalfClosedFlowCloseIsOnceUnderRace` (+NC6) |
| remote socket | dialer | established | `flowTeardown` | `close` | `close` | same | same |
| TCP cached buffer | `ReadCached` | drain | the drain loop | released after each write | released then terminal | n/a | `TestCachedDrainStopsAtTheFirstWriteFailure` (+NC5) |
| UDP NAT / packet buffer | `splicePacketConnection` | `Splice` accepted | TUN engine or copy | `PutPacketBuffer` | `PutPacketBuffer` | n/a | `splice_eligibility_test.go`, CPD-26 |
| tracker | `NewTracker` at commit | flow commit | connection manager | `CloseFlow` | `CloseFlow` | n/a | `connection_lifecycle_test.go` |
| nfqueue mark | `autoRedirect.Start` | `RegisterAutoRedirectOutputMark` | the inbound | `ReleaseAutoRedirectOutputMark` | same | claim CAS | `auto_redirect_mark_combination_test.go` |

### 4.1 L01 … L16

| window | status | evidence |
|---|---|---|
| L01 TUN fd before Scope registration, concurrent Close | NOT_EXECUTABLE | needs a real NE/VpnService fd |
| L02 rule-set callback installed, stack start fails | PASS | `rule_set_updater_lifecycle_test.go`, `TestRuleSetItemCloseReleasesRefs` |
| L03 Proxy and TUN redirect simultaneously | NOT_EXECUTABLE | device-level redirect |
| L04 JudgeFlow DNS hijack vs route-set update | PASS (unit) / NOT_EXECUTABLE (live update) | `judgeFlow` reads the route sets under one RLock into locals; the FakeIP/L0 ordering tests |
| L05 preview completed, group changes before commit | PASS | `fast_path` is resolved per call and never written back (`TestDirectFastPathSelectorsSwitchDoesNotLeakTheIntrinsicGrant`) |
| L06 loadbalance preview/commit, retry after dial failure | PASS | loadbalance route tests |
| L07 Android socket created, protect not yet done, Stop | NOT_EXECUTABLE | device |
| L08 network reset before/after setup dial | PASS (in-repo) | `TestHandoverRepeatedGenuineChangesEachClaimOnce`, `TestStall_*` |
| L09 handshake success vs Stop | PASS (in-repo) / NOT_EXECUTABLE (device) | half-close and teardown tests |
| L10 cached drain second write fails, third pending | PASS + NC5 | `TestCachedDrainStopsAtTheFirstWriteFailure` |
| L11 Splice accepted, then CloseAll | PASS (ownership) / NOT_EXECUTABLE (accepted on a real socket) | `conn_reclaim_test.go`, `TestTransitionProtectsAKernelOwnedConnection` |
| L12 Splice refused, Stop or EOF | PASS | `TestDirectTUNFlowTakesTheSpliceDecisionAndFallsBack`, `TestSpliceAttemptIsIndependentOfFastPath` |
| L13 UDP NAT timeout vs cached flush | NOT_EXECUTABLE | needs a live NAT session |
| L14 FIN half-close + peer gone + CloseAll | PASS | `TestHalfClosedPeerGoneIsReleased`, `TestHalfClosedLivePeerIsNotReleased`, `TestStall_OnlyTransitionsEndStalledFlows` |
| L15 nfqueue bypass vs rule update | NOT_EXECUTABLE | root |
| L16 stale callback after Close | PASS | `TestHandoverRepeatedGenuineChangesEachClaimOnce`, `android_handover_generation_test.go` |

## 5. Negative controls

Seven, each in the working tree, reverted, restored hash verified. All but NC4 are one-liners on the
new code; NC4 needed two attempts because the explicit-bypass branch has three return paths (§6.1).

| ID | mutation | expected red | observed |
|---|---|---|---|
| NC1 | `route`: `if metadata.ConsumesBypassVerdict` -> `if true` (the old per-flow gate restored on the plain TUN) | `TestTheImplicitFastPathIsNotComputedWithoutABypassConsumer` | **RED**: "a caller that cannot apply a Bypass verdict must not be given one" |
| NC2 | `autoRedirectHandler.JudgeFlow` passes `false` (the native consumer short-circuited) | `TestTheAutoRedirectEntryAsksForABypassVerdict` | **RED**: "the nfqueue consumer applies a Bypass verdict, so its entry is the one that asks for it" |
| NC3 | `judgeFlow` derives the capability from the platform: `consumesBypassVerdict = C.IsLinux` | the native test and CPD-12 | **RED**: both; this is the platform-confusion control, and it fails on a Windows host exactly because the platform is the wrong source of truth |
| NC4 | the whole `case *R.RuleActionBypass:` gated by `!metadata.ConsumesBypassVerdict` | `TestAnExplicitBypassRuleIsIndependentOfABypassConsumer` | **RED**; the older `TestAnExplicitBypassRuleIgnoresTheFastPathGate` stays green because its metadata models a consumer - the two tests are not redundant |
| NC5 | `drainCachedSource`: `return` -> `continue` after a failed write | `TestCachedDrainStopsAtTheFirstWriteFailure` | **RED** |
| NC6 | `flowTeardown.close`: a second `onClose(err)` after `once.Do` | `TestHalfCloseVerdictAndTeardownRaceSettleOnce`, `TestHalfClosedFlowCloseIsOnceUnderRace` | **RED**: "attempt 0: onClose exactly once", "onClose ran more than once" |
| NC7 | `trafficclass.MatchTag` returns false (the shared AI matcher cut) | the `common/trafficclass` suite | **RED**: `TestMatchTagMatrix`, `TestMatchTagDoesNotDependOnPositionOrCase`, `FuzzMatchTagAgreesWithTokenisation` |

Not executable here: a negative control against the **live nfqueue consumer** (needs root), and one
against a **real accepted splice** (the memory TUN refuses handover). Their contracts are covered by
NC2 and NC5/CPD-29 respectively; the live halves are `NOT_EXECUTABLE`, not PASS.

## 6. Independent findings

### 6.1 The explicit-bypass branch has three return paths, and that is why NC4 needed two attempts

`route/route.go` can answer `PreMatchBypass` for an explicit rule in three places: the
no-outbound branch (`:590`), the post-rewrite branch (`:597`) and the `result.Action` rewrite
(`:598-601`, the assignment at `:599`). My first NC4 gated only the outer `if action.Outbound == ""` and **nothing turned red** -
the flow fell through to the second branch and still returned `PreMatchBypass`. That is a good
property (a single-lane mutation cannot disable an operator instruction) but it also means a naive
mutation test here proves nothing. The control has to gate the whole case, which is what the record
above does.

### 6.2 One observed flake, in a pre-existing test, under matrix contention

`protocol/tun::TestTUNDeviceResetReleasesTrackedFlow/splice-eligible` failed once in a full-matrix run
with `expected: 1, actual: 0` for `onClose`. It is not a double close and not a leak: the subtest
waits for the traffic entry to disappear (`waitTraffic(t, 0, 5s)`) and then asserts the close counter,
and under ten-package CPU contention the assertion can run in the window between the two.

Reproduction: 30/30 alone, 3/3 whole-package shuffled, and the immediate full-matrix re-run is green.
The test predates this round and does not touch the verdict path. Recorded, not fixed - the fix would
be a `require.Eventually` on the counter, which is a separate change to a test this round did not
cause to be flaky.

### 6.3 The knowledge base is one precondition behind

`KNOWLEDGE/modules/fast-path-group-policy.md` §6 describes the eligibility gate without the consumer
precondition. Per its own rule 5 I did not edit it; the divergence is recorded in the closeout (§12)
and here so a future refresh can add one sentence and two rows.

## 7. Build, test, benchmark, CI

```text
route 442.0s, route/rule, protocol/tun, protocol/direct, option, adapter, adapter/outbound,
common/trafficclass, common/dialer 16.9s, e2e 23.6s                      all ok (clean matrix re-run)
go vet ./route/ ./protocol/tun/ ./adapter/ ./option/
  windows/amd64, darwin/arm64, android/arm64, linux/amd64                ok (compiles the test files)
go build ./route/ ./protocol/tun/ ./adapter/ ./option/ ./protocol/direct/
  ios/arm64, ios/arm64 -tags <tags>, ios/arm64 -tags <tags>,tvos        ok
ios go vet                                                              NOT_EXECUTABLE (needs an Apple toolchain to link a test binary)
```

Per-flow decision on this host (AMD Ryzen 7 4800H, Windows, Go 1.25, `-count=3`):

```text
BenchmarkPreMatchWithoutAConsumer   77.50 / 77.67 / 78.80 ns/op   48 B/op   2 allocs/op
BenchmarkPreMatchForAConsumer       98.15 / 97.62 / 98.02 ns/op   48 B/op   2 allocs/op
```

The delta (~20 ns/flow) is the whole authorisation-and-eligibility chain on a minimal eligible flow,
and it is the cost the non-consumer entries no longer pay. It is **not** a throughput claim: flow
setup is dominated by dial and the rule loop, and the 944 B/3-allocs figure of
`BenchmarkPreMatchDirectFlowEndToEnd` shows where the allocations actually are. The structural claim
is the call count: `CanBypass` is invoked 0 times per flow on the stack entry and once on the native
entry, pinned by the two `TestTheImplicitFastPath…` tests.

CI on the reviewed code `8ef6231bd`: Verify `40157380698`, Android Core ARM64 `40157380707`, Mobile
contract `40157380640`, Release contract `40157380633`, macOS arm64 `40158472003` - all success, and
Verify's `-race` step covers `./route/... ./protocol/tun/... ./adapter/...`. This pass' own commit is
documentation plus two test files; its runs are in the task answer.

## 8. Limits

* No device: iOS, macOS, Android, Windows Wintun and Linux nfqueue were not executed. Windows TUN
  flows are executed on this host through the memory-TUN fixtures, which is not a Wintun driver.
* `auto_redirect` was not run end to end (needs nfqueue + netlink privileges). Its wiring is
  source-confirmed through the client chain in CPD-34, and the entry contract is unit-tested; "the
  nfqueue really applies the mark on a live kernel" is NOT_VERIFIED.
* No accepted splice was executed: the memory TUN refuses handover, so the accepted branch is
  ownership-tested only.
* `go test -race` on iOS/Android is NOT_EXECUTABLE here; the Linux `-race` run is the CI step above.
* The client submodules were initialized read-only for the audit and left at their recorded SHAs.

## Result

```text
TASK = CROSS_PLATFORM_FAST_PATH_SIMPLIFICATION_ADVERSARIAL_DEBUG
TARGET_BRANCH = testing
START_BASELINE_PINNED = NO
IOS_STAGE_RESULTS_PRESERVED = YES
ANDROID_VPNSERVICE = SOURCE_ONLY
ANDROID_NATIVE_SPECIAL = SOURCE_ONLY
WINDOWS_TUN = PASS
MACOS_TUN = SOURCE_ONLY
IOS_TUN = SOURCE_ONLY
LINUX_NATIVE_BYPASS = SOURCE_ONLY
PLAIN_TUN_REDUNDANT_GATE = ELIMINATED
EXPLICIT_BYPASS_PORT = PASS
CONFIG_COMPAT = PASS
SPLICE_COPY_BYTE_EXACT = PASS
OWNER_CLOSE_ONCE = PASS
HALFCLOSE_NETWORK_RECLAIM = PASS
NEGATIVE_CONTROLS = 7/7 RED AS EXPECTED (NC1 NC2 NC3 NC4 NC5 NC6 NC7)
PRODUCTION_FIXES = 0
REAL_DEVICE_LIMITATIONS = no iOS/macOS/Android device; no Wintun; no live nfqueue; no accepted splice
CODE_SHA = 6503ba2c15f572f32493572aa0816207581cb136
FINAL_SHA = <the commit that adds this document>
PUSHED = YES
OVERALL = DONE_WITH_LIMITATIONS
```
