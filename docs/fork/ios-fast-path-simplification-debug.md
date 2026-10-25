# iOS Fast Path simplification — adversarial debug pass (2026-10-25)

This is the adversarial debug pass on `cc21e7548` (`refactor(route): compile the implicit Direct
Fast Path out on iOS`). It exists to answer one question: **did removing the iOS implicit-bypass
authorisation and eligibility chain remove anything that the data path, the ownership model or
another platform was relying on?**

Answer: no production regression was found, and the fault-injection evidence below is what says so.
The pass did find one **evidence gap** in the previous round and closed it: the guard's platform
check (`!C.IsIos`, not `!C.IsDarwin`) was only observable on a macOS host, so a `!C.IsDarwin`
mistake would have passed every Linux/Windows test and the iOS compile gate. It is now caught on
any host by a darwin compile control in the CI gate.

No production code changed in this round. What changed is one CI gate and this document.

## 1. Change under review, and what it can and cannot reach

The production commit is one constant and one `if`:

| location | change |
|---|---|
| `route/fast_path.go:59` | `const implicitDirectFastPathIsExecutable = !C.IsIos` |
| `route/route.go` `(*Router).preMatchFlow` | `resolveFastPathRequest` + `canFastBypass` wrapped in that constant |

Reachability, re-derived from the tree rather than from the previous report:

```text
sing-tun stack (TCP/UDP/IP) -> ForwardStage / gvisor forwarder
  -> Inbound.JudgeFlow               protocol/tun/inbound.go:1057
       DNS hijack (2,3), FakeIP guard (4), L0 route sets (5) - BEFORE the router
  -> adapter.JudgeFlow               adapter/router.go:86
  -> Router.PreMatch                 route/route.go:490
       prepareMatchMetadata          process search, neighbour, FakeIP - BEFORE the rules
       rule loop                     sniff / route / bypass / reject / hijack / resolve
  -> preMatchFlow                    route/route.go:951
       resolveOutbound(preview)      unchanged by this round
       [GUARD] fast-path decision    resolveFastPathRequest -> canFastBypass
       FlowOutbound / Port probe     unchanged by this round
  -> RouteConnectionEx / RoutePacketConnectionEx
  -> spliceConnection / splicePacketConnection -> Copy
```

Two properties make the blast radius smaller than it looks:

1. **`Router.PreMatch` has exactly two callers**: the TUN path through `adapter.JudgeFlow`, and the
   cloudflared ICMP handler (`protocol/cloudflare/inbound.go:234`, `InboundType: C.TypeCloudflared`,
   `Network: N.NetworkICMP`). `canFastBypass` refuses that one at its first predicate, so the guard
   changes nothing there. A system-proxy inbound never reaches `PreMatch` at all: it routes through
   `ConnectionManager`.
2. **`PreMatchResult.Outbound` is read only for the `PreMatchFlow` action** (`adapter/router.go:114`,
   `protocol/cloudflare/inbound.go:243`). The bypass verdict and the continue verdict both carry no
   Port, no Tracker and no buffer, so nothing downstream changes ownership when the verdict's kind
   changes.

Side-effect audit of everything the skipped block calls — all pure, so skipping cannot lose state:

| call | side effects | evidence |
|---|---|---|
| `resolveFastPathRequest` / `resolveFastPathPolicy` / `directFastPathLeafIsIntrinsic` / `fastPathChainReachesLeaf` | none: slice scans, map reads, interface assertions | `route/fast_path.go` |
| `trafficclass.MatchTag` | none: string tokenisation, no cache | `common/trafficclass/match.go` |
| `processMetadataIsProven` | none: reads `processSearcher`, `metadata.ProcessInfo`, `metadata.Source`; the actual search happens in `prepareMatchMetadata` before the rules | `route/process_cache.go:52`, `route/route.go:495` |
| `(*direct.Outbound).CanBypass` -> `BypassBlockers` | none: reads the resolved dial surface, the network manager and the local-family verdict | `protocol/direct/outbound.go:451-500` |
| `powerReport.Recorder()` | getter | `route/route.go:910` |

No out-of-bounds change: `git diff --check` is clean, and the diff contains no `go.mod`, `go.sum`,
submodule, system-proxy, `traffic_class`, MASQUE/VLESS/Naive or release change.

## 2. The decisive experiment: run the data plane as iOS would

The previous round proved the iOS branch by compilation. This round drives it: the constant is
forced to `false` in a scratch tree (the iOS posture on a Windows host), the verdict expectations in
the e2e file are flipped to `ActionAccept` so the assertions describe the posture being tested, and
the real TUN data plane is run. Every change was reverted with `git checkout --`; nothing of it is
committed.

| suite | result under the iOS posture |
|---|---|
| `TestFastPathVerdictDoesNotChangeTheTUNDataPath` (TCP, 4 verdict cases, real frames, real router, real direct outbound, real peer) | **PASS** — identical bytes and the same outbound in all four cases |
| `TestBypassVerdictStillDeliversUDP` | **PASS** — the datagram comes back through the stack; the socks sink stays empty |
| `TestFastPathMultiSegmentPayloadIsIntact` | **PASS** — 8 KiB arrives byte-identical |
| `TestSpliceAttemptIsIndependentOfFastPath` (unset/true/false) | **PASS** — one attempt each, one recorded refusal |
| `TestDirectTUNFlowTakesTheSpliceDecisionAndFallsBack` | **PASS** |
| `TestProxiedTUNFlowStillHasAGoConnSource` | **PASS** |
| `TestProxiedSpliceRejectionDeliversAMultiSegmentPayload` | **PASS** |
| `TestSystemProxyTCPSpliceIsRefusedAtTheSource` | **PASS** |

This is the strongest available substitute for a device: with the decision removed, the same
topology delivers the same bytes through the same outbound, and the splice decision is untouched.

The same posture run against the **non-iOS contract tests** turns 23 of them red, which is the
expected and desired direction — they encode the mechanism that iOS does not have:

```text
TestFastBypassPlainDirectTCPEligible, TestFastBypassPlainDirectIPv6Eligible,
TestFastBypassPlainUDPEligible, TestFastPathHitSimulation, TestDirectFastBypassVerdictHasNoPort,
TestDirectFastBypassVerdictHasNoPortUDP, TestFastPathFalseSurvivesASelectorSwitch,
TestFastPathTruthTable, TestDirectFastPathSurvivesTransparentSelectorWithNoPolicy,
TestDirectFastPathSelectorExplicitFalseSuppressesTheAttempt,
TestDirectFastPathSelectorExplicitTrueTakesExactlyOneDecision,
TestDirectFastPathSurvivesNestedTransparentGroups,
TestDirectFastPathNestedExplicitPolicyWinsOverIntrinsic,
TestDirectFastPathSelectorsSwitchDoesNotLeakTheIntrinsicGrant,
TestFastBypassAuthorizedGroupChainIsEligible, TestFastBypassUrltestGroupIsEligibleToo,
TestFastPathUnsetAndTrueAreIndistinguishable, TestFastPathFalseOnlySuppressesTheIntrinsicShape,
TestExplicitFastPathFalseSuppressesTheDirectChain, TestExplicitFastPathFalseNeedsAGroup,
TestRouteOptionsDisableTheFastPath, TestRouteOptionsStillAllowBypassWhenIrrelevant,
TestTheImplicitFastPathGuardIsExactlyTheIOSPlatformCheck
```

The last one is the new posture test: it is the test that notices when the guard is switched off on
a host where it must be on.

## 3. Negative controls (deliberate breakage, then reverted)

| ID | mutation | expected | observed |
|---|---|---|---|
| NC1 | `implicitDirectFastPathIsExecutable = true` (old gate restored everywhere) | the gate names the iOS calls again | **FAIL**: "the iOS build still calls the implicit Direct Fast Path decision (resolveFastPathRequest: 1 call(s), canFastBypass: 1 call(s))". The Go posture test stayed green on this host, correctly: `true == !C.IsIos` here |
| NC2 | `= !C.IsDarwin` (the wrong platform check) | macOS must not lose the mechanism | **FAIL** (after the fix in §4): "the macOS build no longer calls the implicit Direct Fast Path decision (0 call(s), 0 call(s))". Before the fix this mutation passed every Linux/Windows test **and** the iOS compile gate |
| NC3 | explicit `action: bypass` made to return `continueResult` | the explicit-bypass contract tests go red | **FAIL**: `TestAnExplicitBypassRuleIgnoresTheFastPathGate` ("an explicit bypass action must keep its meaning") and `TestTheExplicitBypassRuleSurvivesThePlatformGuard` |
| NC4 | splice source check skipped (`if false && !isGoConn`) | the system-proxy negative test goes red | **FAIL**: `TestSystemProxyTCPSpliceIsRefusedAtTheSource` — the diagnostic moved to `source_reader_writer_mismatch:1`, i.e. a **second, independent** guard still refused the handover (`Accepted: 0`) |
| NC5 | cached drain `continue`s after a failed write | the terminal-write test goes red | **FAIL**: `TestCachedDrainStopsAtTheFirstWriteFailure` ("an error is expected but got nil"); the success-path test stayed green |
| NC6 | `flowTeardown.close` calls `onClose` a second time | the close-once tests go red | **FAIL**: `TestHalfCloseVerdictAndTeardownRaceSettleOnce` ("onClose exactly once"), `TestHalfClosedFlowCloseIsOnceUnderRace` ("onClose ran more than once") |

Two observations worth keeping:

* **NC4 shows the splice refusal is layered on purpose.** Removing the source check does not produce
  an unsafe handover; the reader/writer mismatch check catches it. The system-proxy test asserts the
  *reason*, which is what makes the first guard's removal visible at all rather than silently
  absorbed by the second.
* **NC6 shows the close-once contract has two owners.** Only two of the four targeted tests went red;
  `TestHalfClosedPeerGoneIsReleased` stayed green because in that scenario the single `onClose`
  comes from the observer's release path, not from `flowTeardown.close`. Both paths are covered, by
  different tests — which is why the mutation has to be aimed at the path a test owns.

## 4. The one fix this round: the darwin control in the CI gate

NC2 exposed a real hole in the previous round's evidence. `C.IsDarwin` is true on macOS and false on
Linux/Windows, so a guard written from it is indistinguishable from the correct one on every host
that is not a Mac — and on iOS both spellings are `false`, so the iOS compile gate cannot see the
difference either. Only the macOS runner would have caught it, at macOS cost, if it ran at the right
time.

`scripts/ci/verify-ios-fast-path-guard.sh` now compiles `./route` for **darwin/arm64** as well and
requires the two calls to be present there, exactly like the Linux control. That makes the platform
triangle checkable on any runner: iOS 0/0, Linux 1/1, macOS 1/1. Measured on this host:

```text
ios/arm64    resolveFastPathRequest 0, canFastBypass 0, resolveTrafficClass 3
linux/amd64  resolveFastPathRequest 1, canFastBypass 1
darwin/arm64 resolveFastPathRequest 1, canFastBypass 1, resolveTrafficClass 3
```

A note on how this gate fails safe: an absence check is worthless if the compiler never ran, so the
script also requires a live `resolveTrafficClass` call in the same iOS output and the fast-path
calls in both non-iOS outputs. During this pass a hand-run `go build -gcflags=-S` was captured
through a PowerShell redirection that wrapped long lines and produced a convincing "0 calls" for
darwin; the presence controls in the script are exactly what turns that class of measurement error
into a failure instead of a false pass.

## 5. D01–D26 matrix

Every row was run on this tree with `-tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)"`. "PASS"
means the named tests passed on the tree under review; where a row concerns iOS-only behaviour, the
iOS-posture experiment in §2 is the evidence.

| ID | verdict | evidence |
|---|---|---|
| D01 bare `direct` TCP | PASS | `TestFastBypassPlainDirectTCPEligible`, `TestFastPathVerdictDoesNotChangeTheTUNDataPath/absent` |
| D02 `selector → direct` unset/true/false | PASS | `TestFastPathUnsetAndTrueAreIndistinguishable`, `TestDirectFastPathSelectorExplicitFalseSuppressesTheAttempt`, the four e2e verdict cases; config load: `option/fast_path_test.go` |
| D03 nested `urltest → selector → direct` | PASS | `TestDirectFastPathSurvivesNestedTransparentGroups`, `TestDirectFastPathNestedExplicitPolicyWinsOverIntrinsic` |
| D04 `selector → MASQUE/VLESS/SOCKS` | PASS | `TestFastBypassAuthorizedGroupChainIsEligible`, `TestFastPathVerdictDoesNotChangeTheTUNDataPath` (socks sink must stay empty) |
| D05 `loadbalance` | PASS | `TestFastPathTransparentGroupsAreTheOnlyPolicyCarriers`, `TestDirectFastPathSelectorsSwitchDoesNotLeakTheIntrinsicGrant` |
| D06 two groups sharing one leaf | PASS | `TestDirectFastPathSelectorsSwitchDoesNotLeakTheIntrinsicGrant`, `TestFastPathFalseSurvivesASelectorSwitch` |
| D07 AI keywords, false positives | PASS | `common/trafficclass` suite (`NaiveProxy`/`mail`/`Thailand` stay unmatched) |
| D08 system proxy | PASS | `TestSystemProxyTCPSpliceIsRefusedAtTheSource`; no `PreMatch` call site on that path |
| D09 UDP NAT / connected UDP / timeout | PASS | `protocol/tun` suite, `TestBypassVerdictStillDeliversUDP`, `udp_connect_detour_test.go` |
| D10 FakeIP v4/v6 | PASS | `route/fakeip_bypass_boundary_test.go`, `protocol/tun` FakeIP tests |
| D11 DNS hijack / 53 | PASS | `protocol/tun` hijack contract tests |
| D12 sniff / override destination / candidates | PASS | `TestRouteOptionsDisableTheFastPath`, `protocol/tun` sniff tests, e2e chains |
| D13 process metadata unavailable | PASS | `route/process_transient_miss_test.go`; `processMetadataIsProven` is pure and the search runs before the rules |
| D14 explicit `action: bypass` | PASS | `TestAnExplicitBypassRuleIgnoresTheFastPathGate`, `TestTheExplicitBypassRuleSurvivesThePlatformGuard`, NC3 |
| D15 Flow/Port (ICMP) | PASS | `TestDirectFastBypassVerdictHasNoPort`, `protocol/direct` Port tests; the guard does not wrap the FlowOutbound branch |
| D16 L0 route sets | PASS | `protocol/tun/l0_authoritative_vs_reverse_mapping_test.go`; L0 is decided before the router and the diff does not touch it |
| D17 splice attempt independent of `fast_path` | PASS | `TestSpliceAttemptIsIndependentOfFastPath` (also under the iOS posture) |
| D18 proxied conn cannot raw-splice | PASS | `TestProxiedTUNFlowStillHasAGoConnSource`, `TestProxiedSpliceRejectionDeliversAMultiSegmentPayload` |
| D19 system proxy source is not a GoConn | PASS | `TestSystemProxyTCPSpliceIsRefusedAtTheSource`, NC4 |
| D20 splice refusal, multi-segment payload | PASS | `TestProxiedSpliceRejectionDeliversAMultiSegmentPayload`, `TestFastPathMultiSegmentPayloadIsIntact` |
| D21 cached drain terminal on first write failure | PASS | `TestCachedDrainStopsAtTheFirstWriteFailure`, NC5 |
| D22 UDP cached packets | PASS (delivery) / NOT_APPLICABLE (ordering) | `TestBypassVerdictStillDeliversUDP`, `splice_eligibility_test.go` (`takeCached` empty case). The ordering/exactly-once contract of `SplicePacketOptions.Cached` lives in sing-tun and has no unit test in this tree; it is untouched by this round's diff |
| D23 tracker/dashboard | PASS | `TestFastBypassTrackerIsIneligible`, `connection_lifecycle_test.go` (FIN/reset/close-all/shutdown), `flow_tracker_serialization_test.go` |
| D24 TLS fragment / record fragment / spoof | PASS | `TestRouteOptionsDisableTheFastPath` and the TLS-option branch of `canFastBypass` (unchanged; the guard removes the call on iOS only) |
| D25 iOS vs non-iOS | PASS | iOS: assembly 0/0 + iOS-posture e2e; macOS: darwin control 1/1 + the posture test's `== !C.IsIos` assertion on any darwin runner; Linux/Windows: 1/1 and full suites |
| D26 `traffic_class` | PASS | `common/trafficclass` suite, `route` traffic-class tests, and `resolveTrafficClass` still called 3× in the iOS assembly |

Suites run for the matrix (all green on this tree):

```text
route (441.7s), route/rule, protocol/tun, protocol/direct, option, adapter, adapter/outbound,
common/trafficclass, common/dialer (16.8s), e2e (23.3s)
```

## 6. Lifecycle and ownership ledger

The guard removes a verdict, not a resource. The table names the owner and the test that pins it;
every row is unchanged by this round, and the removal cannot create a second owner because the
removed verdict carried nothing.

| path | creator | commit point | owner of fd / GoConn / buffer / tracker | fallback | terminal | counted | pinning test |
|---|---|---|---|---|---|---|---|
| TUN TCP → Direct → splice refused | TUN flow; `direct` dials | `routeConnection` | `*tun.GoConn` + outbound conn; copy loops own buffers | refusal before acceptance → copy | `flowTeardown.close` once | splice diagnostics | `TestDirectTUNFlowTakesTheSpliceDecisionAndFallsBack` |
| TUN TCP → proxy (packaged) | TUN flow; proxy owns the tunnel | `routeConnection` | `trackedConn`; no `syscall.Conn` | refusal at the source → copy | same | same | `TestProxiedTUNFlowStillHasAGoConnSource` |
| TUN UDP → NAT | UDP NAT session | `routePacketConnection` | `UDPNatConn` + packet buffers | refusal → copy, cached delivered once | NAT cleanup / idle timeout | UDP diagnostics | `TestBypassVerdictStillDeliversUDP` |
| cached TCP drain fails | reader / cache | after `ReadCached` | remote socket owns the bytes already written | **terminal**, no replay | caller closes | `CountFunc` once | `TestCachedDrainStopsAtTheFirstWriteFailure` |
| cached buffer handover | reader / cache | `deliverCachedBuffer` | exactly one releaser | fall back when geometry does not fit | buffer released once | counters | `TestCachedBufferHandoverPutsTheArrayInThePoolExactlyOnce`, `TestCachedBufferCallerReleaseAfterFailureWouldDoubleRelease` |
| tracker attach/close | router / connection manager | `NewTracker` at flow commit | tracker owns the flow entry | n/a | close-all / shutdown | `CountFunc` | `TestTrafficTrackerReleasesOnCleanFIN/Reset/CloseAll/Shutdown` |
| half-close observer | copy direction that saw EOF | `observeHalfClosed` | observer settles once; teardown settles it if it wins | n/a | `settled` once | diagnostics | `TestHalfCloseVerdictAndTeardownRaceSettleOnce`, `TestHalfClosedFlowCloseIsOnceUnderRace` |
| network transition / reclaim | connection manager | `Transition` epoch | idle owners drained; kernel-owned spared | n/a | `ShutdownStillClosesEverything` | reclaim diagnostics | `TestTransitionDrainsAnActiveConnection`, `TestTransitionProtectsAKernelOwnedConnection`, `TestShutdownStillClosesEverything` |
| system proxy TCP | proxy inbound | `ConnectionManager` | no GoConn, no TUN buffer | n/a | ordinary close | unchanged | `TestSystemProxyTCPSpliceIsRefusedAtTheSource` |

The removed verdict appears in none of these rows: it had no Port (so it could never become a
`Flow`), no tracker, no buffer and no close handler. The only thing that changed is which of two
accept-equivalent `tun.FlowAction` values reaches the dispatcher.

## 7. What was subtracted (and what was not)

Subtracted on iOS, measured in the emitted code rather than in prose: one chain policy scan, one AI
tag match per transparent group, and the twenty `canFastBypass` refusal reasons. On iOS those calls
are no longer in the binary (0 `CALL`s), so the gain is "less setup work per flow", not throughput.
No benchmark is reported, and no device number is claimed.

Not subtracted, verified unchanged: explicit `action: bypass`, L0, Flow/Port, splice and copy,
`traffic_class` (still resolved in the same function, still called 3× in the iOS build), the
`fast_path` configuration surface and its group-only validation, and every other platform.

## 8. Failure injection, races, builds, CI

| check | result |
|---|---|
| fault injection (six controls, above) | 6/6 red where they must be, green after revert |
| iOS build | `GOOS=ios GOARCH=arm64 CGO_ENABLED=0 go build ./...`: PASS on this host (compile-only; no cgo, no link) |
| darwin build | compile-only: PASS; assembly control 1/1 |
| `-race` | `go test -race` needs cgo on this host; CGO is available through mingw, and the race-enabled subset was run in the previous round. The repository's own race suite runs in `Verify` on Linux: green on the code SHA |
| `git diff --check` | clean |
| full matrix (section 5) | green |

CI on the production SHA `cc21e7548` (from the closeout, re-checked): Verify `40150190306`, Android
`40150190303`, Mobile contract `40150190317`, Release contract `40150190301`, macOS `40150235723` —
all success. This round's commit carries the gate change and this document; its own runs are listed
in the task answer, not here, because a document cannot contain the run of the commit that adds it.

## 9. Git record

| item | value |
|---|---|
| observed HEAD at the start of this pass (evidence, not a baseline) | `639efa4c11f80b793f5cd656fe713d8feb196e0a` |
| production commit under review | `cc21e75485465b7b81c217ef997e56f66c91735e` |
| this round's commit | the commit that adds the darwin control, the closeout pointer and this document |
| branch | `testing`, no new branch, no `main` change |
| tags / releases | none |
| dependency pins / submodules | untouched (`go.mod`, `go.sum`, `test/go.mod`, `test/go.sum`, `clients/` have no diff) |
| scratch trees | all fault injections reverted with `git checkout --`; no production mutation is committed |
| working tree | clean after the push |

## 10. Independent findings

1. **The previous round's platform assertion was weaker than it looked.** `require.Equal(!C.IsIos,
   constant)` cannot distinguish `!C.IsIos` from `!C.IsDarwin` on a host where neither Go constants
   is set, which is every runner except macOS. Fixed by the darwin compile control (§4). This is the
   one real defect this pass found, and it was in the evidence, not in the product.
2. **The splice refusal is deliberately layered.** NC4 shows the reader/writer mismatch check catches
   what the source check would have caught, and `Accepted` stays 0 either way. The system-proxy test
   is only a real guard because it asserts the *reason*; a test that only asserted `Accepted == 0`
   would let the first check be deleted silently.
3. **The close-once contract is owned by two paths.** `flowTeardown.close` and the half-close
   observer's release each call `onClose`; the split is covered by different tests, so a mutation has
   to be aimed at the right one to be visible (NC6).
4. **`bypassBlockerDetail` is production-dead.** `route/bypass_verdict.go:97` and the
   `bypassBlockerReporter` assertion are called only from `route/direct_offload_hits_test.go`; the
   production gate uses `BypassableOutbound.CanBypass` and `direct.BypassBlockers` directly. It is a
   diagnostic seam that was never wired to a log. Out of scope here (it predates this change and is
   not iOS-specific); recorded as a future cleanup with its own evidence.
5. **The `fast_path` policy map is still built on iOS** (`box.go:845-866`) and is now read by
   nothing. Startup-only, deliberately left alone: moving the platform condition into configuration
   loading would spread it across one more file for no runtime gain, and the field's validation is a
   cross-platform configuration contract that should not become iOS-specific.
6. **A wrapped-tty measurement can fake a zero.** During this pass a darwin `-gcflags=-S` capture
   through PowerShell line wrapping produced "0 calls" for every symbol; the same build re-captured
   unwrapped produced 1/1/3. The CI gate's presence controls exist for exactly this failure mode, and
   the pass confirmed they fire when the matcher is broken.

## Result

```text
TASK = IOS_FAST_PATH_SIMPLIFICATION_ADVERSARIAL_DEBUG
SCOPE = CHANGED_IOS_IMPLICIT_BYPASS_AND_ADJACENT_LIFECYCLE_ONLY
BRANCH = testing
BASELINE_PINNED = NO
INVALID_IOS_GATE_ELIMINATED = PASS
TUN_ROUTING_EQUIVALENCE = PASS
TCP_UDP_BYTE_INTEGRITY = PASS
SPLICE_REFUSAL_FALLBACK = PASS
CACHE_EXACTLY_ONCE = PASS
CONNECTION_LIFECYCLE_UNIFIED = PASS
CLOSE_ONCE_AND_NO_LEAK = PASS
NETWORK_TRANSITION = PASS
SYSTEM_PROXY_PRESERVED = PASS
TRAFFIC_CLASS_PRESERVED = PASS
EXPLICIT_BYPASS_FLOW_PORT_L0 = PASS
IOS_CONFIG_COMPATIBILITY = PASS
OTHER_PLATFORM_CONTRACTS = PASS
NEGATIVE_CONTROLS = PASS
IOS_BUILD = PASS
IOS_DEVICE_REQUIRED = NO
CI_SAME_CODE_SHA = PASS
FINAL_SHA = cc21e75485465b7b81c217ef997e56f66c91735e   (reviewed production commit; the debug
            pass's own commit adds only the darwin gate control and this document, and is the
            branch HEAD after it - a document cannot contain the run of the commit that adds it)
ORIGIN_TESTING_MATCH = YES
WORKTREE = CLEAN
OVERALL = DONE
```

## Appendix: what this pass changed in the repository

| file | change |
|---|---|
| `scripts/ci/verify-ios-fast-path-guard.sh` | darwin/arm64 compile control (the §4 fix), and its header now explains the three presence controls |
| `docs/fork/ios-fast-path-simplification-debug.md` | this document |
| `docs/fork/ios-fast-path-simplification-closeout.md` | pointer to this pass, and the script description corrected to name the Linux **and** macOS controls |

No production Go file, test expectation, dependency pin or submodule was touched. Every fault
injection in §3 was reverted with `git checkout --` before the commit.
