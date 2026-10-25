# Fast Path policy — final simplification and closeout (2026-10-25)

This is the last round on the Fast Path: prove or disprove that the remaining authorisation branches
pay for something, subtract the ones that do not, fix the one test that could not be trusted, and
close the technical debt this feature accumulated.

**The AI keyword fallback is gone.** It is the only production logic this round removed, and it was
removed because it could not change a single verdict in this tree - not because it looked redundant.

`PRODUCTION_FIXES = 0` for defects; the removals below are the intended work.

## 1. Environment

| item | value |
|---|---|
| repository | `Piggy-Cat-bit-shadow/sing-box`, branch `testing` |
| HEAD at the start (a log line, not a baseline) | `72efed2c078ffcd5c69b6f118f531cc234b6eb36` |
| `BASELINE_PINNED` | NO |
| sing-tun (the one `replace` resolves to) | `github.com/Piggy-Cat-bit-shadow/sing-tun v0.0.0-20261024234618-a0f9f68fec7d` |
| build tags | `release/DEFAULT_BUILD_TAGS_OTHERS` |
| worktree before this round | clean, no user changes to protect |
| client submodules | at their recorded SHAs (`android ec61030d`, `apple cdaaf222`, `desktop eb2dbf8b`), unmodified by this round |

## 2. What the round had to achieve

Keep: the entry capability (`adapter.InboundContext.ConsumesBypassVerdict`), the nfqueue consumer's
native bypass, every hard safety refusal in `canFastBypass`, the tri-state configuration, and the
nested precedence rule.

Remove: authorisation logic that cannot change a verdict. The rule this round follows is the one the
previous rounds established: **a decision that no consumer can act on is not a decision**, and the
same test applies inside the authorisation system, not only at its entry.

## 3. Production inventory

This is the input the whole proof rests on, and it is small:

| set | members | how it is established |
|---|---|---|
| `adapter.BypassableOutbound` (can take a native bypass) | **`protocol/direct.Outbound` only** | `protocol/direct/outbound.go:35` asserts the interface; `grep "func (.*) CanBypass("` over non-test sources returns that one method |
| `adapter.FastPathAuthorizedGroup` (may carry `fast_path`) | `protocol/group.Selector`, `protocol/group.URLTest` | both declare `FastPathTransparent() bool { return true }` |
| opaque groups (own the dial/retry, never transparent) | `protocol/group.LoadBalance` and anything else with `OutboundGroup` but no `FastPathTransparent` | `canCarryFastPathPolicy` |
| everything else | proxy leaves (VLESS, MASQUE, Naive, AnyTLS, …) | no `CanBypass`, so `canFastBypass` refuses at the interface assertion before any policy question |

Two consequences drive the rest of this document:

1. A chain that ends in a bypassable leaf ends in **`direct`**. So whenever the chain is transparent,
   the intrinsic rule answers; the tag fallback could only be reached on a chain that is *not*
   transparent.
2. On a non-transparent chain, `canFastBypass` refuses with `BypassRefusedOutboundChain` unless
   `fastPathAuthorized && fastPathChainReachesLeaf(chain)`, and `fastPathChainReachesLeaf` is the same
   transparency test the intrinsic rule uses. So a `true` from the fallback was consumed by a gate
   that was going to refuse.

## 4. H1: the AI fallback was redundant — proved

The claim: step 5 of the old `resolveFastPathRequest` (the `trafficclass.MatchTag` fallback) could not
change a final verdict for any shape this build can produce.

**Proof by the shape table** (`route/fast_path_policy_truth_test.go`, 42 subtests: 7 shapes × 3
policies × {TCP, UDP}). Each row asks the gate both questions - would this flow be allowed if the bit
were true, and is it allowed when false:

| shape | auth (no policy) | allowed if true | allowed if false | action |
|---|---|---|---|---|
| bare direct | false | yes | yes | bypass (the chain gate does not apply to a one-element chain) |
| selector → direct | true (intrinsic) | yes | no | bypass |
| urltest → direct | true (intrinsic) | yes | no | bypass |
| selector → selector → direct | true (intrinsic) | yes | no | bypass |
| **loadbalance → direct** | false | **no** | **no** | continue |
| **selector → loadbalance → direct** | false | **no** | **no** | continue |
| selector → proxy leaf | false | no | no | continue |

The two bold rows are the ones the fallback used to answer `true` for. The table shows the bit is
inert there - and `TestTheAuthorisationBitIsInertOnEveryNonIntrinsicShape` asserts exactly that, by
calling `canFastBypass` with the bit forced on and off and requiring `BypassRefusedOutboundChain`
both times. That test is also the tripwire: if a future change relaxes the chain gate, the bit stops
being inert and the removed fallback would become reachable again.

Every row is additionally asserted to be **identical under an AI-looking tag and an ordinary one**,
which is the property the removal bought.

**Proof by A/B measurement** (`BenchmarkFastPathAuthorisationOpaqueChain`, the shape the fallback
used to decide, 0 allocs):

```text
after  (fallback removed)  33.09 / 34.48 / 40.81 ns/op
before (fallback restored) 33.98 / 34.64 / 40.66 ns/op
```

The difference is not measurable. That is the honest result and it is reported as such: **the value of
this removal is not speed**. It is one fewer policy source, one fewer branch, and no business
vocabulary in a kernel-handover permission. A "0 ns" change does not diminish it, and this document
does not claim a speed-up.

### 4.1 H2: the duplicated transparent-chain scan

`directFastPathLeafIsIntrinsic` and `fastPathChainReachesLeaf` both walked the chain with
`canCarryFastPathPolicy`. The duplication existed because an earlier revision had the two functions
call each other; that recursion is long gone, so the loop in the intrinsic rule was dead weight.

Change: the intrinsic rule now ends with `return fastPathChainReachesLeaf(chain)` (`route/fast_path.go`).
No behaviour change (same predicate, same order), one fewer copy of the rule, and the stale
"mutual recursion" comment that justified the duplication is replaced with the real reason.

It is *not* merged further. `canFastBypass` still calls `fastPathChainReachesLeaf` itself, so the
transparency loop runs twice per flow exactly as before; caching it in a shared value or a
`FastPathDecision` struct would add the state this round exists to remove. The two functions stay,
with the delegation making them one definition.

## 5. Production changes, with the reason

| file | change |
|---|---|
| `route/fast_path.go` | the `trafficclass` import and the whole tag fallback deleted; `resolveFastPathRequest`'s doc rewritten (rules renumbered, plus "# Why the group's TAG is not consulted" with the inventory and the pointer to the truth table); `directFastPathLeafIsIntrinsic` delegates the transparency loop |
| `route/route.go` | the debug line now names the three facts separately ("implicit Direct Fast Path authorised and eligible … this entry applies the verdict natively") instead of "bypass verdict … honoured only where the caller can carry it natively", which had become vacuous once only consumers reach it |
| `route/bypass_verdict.go` | `bypassBlockerReporter` and `bypassBlockerDetail` moved to `route/bypass_blocker_detail_test.go` (no production caller; `grep` over non-test sources is empty) |
| `option/fast_path.go` | the tri-state doc no longer advertises "automatic tag detection"; it now describes absent/true/false in terms of the intrinsic rule and the explicit switch, and states that a group's name has no bearing |

No other production file changed. `canFastBypass` is untouched, including all twenty refusals;
`preMatchFlow` keeps `if metadata.ConsumesBypassVerdict`; the entry wiring in `protocol/tun` is
untouched.

## 6. Before/after behaviour

| flow | before | after |
|---|---|---|
| bare direct, eligible (native entry) | bypass | bypass |
| selector/urltest/nested transparent → direct, unset | bypass (intrinsic) | bypass (intrinsic) |
| same, `fast_path: true` | bypass | bypass |
| same, `fast_path: false` | continue | continue |
| loadbalance → direct, unset, **AI-named group** | authorisation true → gate refused → continue | authorisation false → gate refused → continue (**same verdict**) |
| selector → loadbalance → direct, unset, AI tag | same | same |
| selector → proxy leaf | continue | continue |
| explicit `action: bypass` | bypass | bypass |
| L0 route-set hit/miss | decided before the router | unchanged |

The verdict is identical in every row. The only observable difference in production is one debug line
that is no longer produced for the opaque shapes (it was never produced for them either, because the
gate refused before logging) and one fewer `MatchTag` call on shapes that were going to be refused.

## 7. Lifecycle and ownership

The capability remains a bool on a value-copied per-flow struct, read once per flow; this round adds
no resource, no owner, no close path. The one lifecycle-adjacent change is the flake fix in
`protocol/tun/flow_release_test.go`:

* **Diagnosis.** The fixture's close path is `conns.NewConnection(..., func(err){ … })`: the
  connection manager runs that closure, and `closed` counts it. The traffic entry is removed before
  that closure runs, so reading the counter the instant `waitTraffic(0)` returns races with it. That
  is the "expected 1, actual 0" that appeared once under full-matrix contention and never in
  isolation - a test asserting too early, not a leak.
* **Fix.** `waitClosed` waits for the counter with `require.Eventually` (bounded, 5 s) and then holds
  it at exactly 1 for a 200 ms window, so a duplicate is caught as well as an absence.
* **What that window cannot see, stated rather than hidden.** The memory TUN calls
  `NewConnectionEx` with a nil `onClose`, so the forward inside the fixture never fires in this
  harness and a mutation confined to that forward is invisible here. The flow's own `onClose`
  exactly-once contract is pinned in `route` instead, and NC-08 exercises both halves.

## 8. F-01 … F-24

| ID | status | evidence |
|---|---|---|
| F-01 | PASS | truth table: `selector -> direct` with AI and ordinary tags is identical; `CanBypass` is consulted once on the native entry (the route consumer tests count it) |
| F-02 | PASS | truth table rows for `urltest -> direct` × unset/true/false |
| F-03 | PASS | `TestFastPathNestedPrecedence` (outer/inner combinations, first explicit wins) + truth table nested row |
| F-04 | PASS | truth table `loadbalance -> direct`, `selector -> loadbalance -> direct`; `TestTheAuthorisationBitIsInertOnEveryNonIntrinsicShape` |
| F-05 | PASS (source) | no proxy outbound implements `CanBypass`; the truth table's `selector -> proxy leaf` row is refused in every combination. No live MASQUE/VLESS instance is constructed, which is why this is source-level |
| F-06 | PASS | truth table `bare direct` row (bypass regardless of the bit, because the chain gate does not apply) |
| F-07 | PASS | `TestFastPathSurvivesASelectorSwitch`, the shared-inner case of `TestFastPathNestedPrecedence`, `TestTheCapabilityIsPerCallUnderConcurrency` |
| F-08 | PASS | `TestEveryBypassConditionRefusesWithItsOwnVerdict`, `TestFastPathAuthorizedButRefusedFallsBackToTheNormalPath`, the truth table over TCP and UDP |
| F-09 | PASS + NC-01 | `TestTheTUNStackDoesNotAskForABypassVerdict`, `TestTheImplicitFastPathIsNotComputedWithoutABypassConsumer` |
| F-10 | PASS + NC-02 | `TestTheAutoRedirectEntryAsksForABypassVerdict`, `TestTheImplicitFastPathIsComputedForABypassConsumer` |
| F-11 | PASS + NC-03 | 128 interleaved rounds over both networks, plus 16×8 concurrent flows, over one router and two inbounds |
| F-12 | PASS | `TestAnExplicitBypassRuleIsIndependentOfABypassConsumer`, `TestAnExplicitBypassRuleIgnoresTheFastPathGate`, `e2e::bypass_verdict_has_no_port`. The three return paths are documented in §12 |
| F-13 | PASS | `TestBothEntriesShareTheSameL0Policy`, the L0/DNS suites; untouched by this round |
| F-14 | PASS (unit) / SOURCE_ONLY (dial) | `preMatchFlow`'s `resolveOutbound(..., false)` is unchanged; the capability adds no selection |
| F-15 | PASS + NC-07 | `common/trafficclass` suite green; cutting `MatchTag` turns it red while every Fast Path test stays green, which is the independence claim |
| F-16 | PASS | `option/fast_path_test.go`, `box_fast_path_group_policy_test.go` |
| F-17 | PASS + NC-09 | `TestCachedDrainStopsAtTheFirstWriteFailure`, `TestFastPathMultiSegmentPayloadIsIntact` |
| F-18 | PASS (in-repo) | `TestBypassVerdictStillDeliversUDP`, the UDP NAT suite |
| F-19 | PASS (ownership unit) / NOT_EXECUTABLE (accepted splice) | `conn_reclaim_test.go`, `TestDirectTUNFlowTakesTheSpliceDecisionAndFallsBack`; the memory TUN refuses handover |
| F-20 | PASS + NC-08b | `TestHalfCloseVerdictAndTeardownRaceSettleOnce`, `TestHalfClosedFlowCloseIsOnceUnderRace` |
| F-21 | PASS | `android_handover_generation_test.go`, `TestTransition*`, `TestShutdownStillClosesEverything` |
| F-22 | **FIXED_TEST** | `waitClosed` (§7); NC-08a shows the exact-once half is now observable |
| F-23 | SOURCE_ONLY | a native flow is not tracked; no test claims counters for it, and the tracker/power-report refusals keep it that way |
| F-24 | PASS | truth table `selector -> direct` / `false`: unauthorised and refused; the same row on a plain TUN still produces the same bytes because the entry does not consume the verdict |

## 9. Negative controls (NC-01 … NC-10)

Each was applied to the working tree, run against the named test, and reverted with a SHA-256 check on
the restored file. Nothing of them is committed.

| ID | mutation | result |
|---|---|---|
| NC-01 | stack entry passes `true` | **RED** `TestTheTUNStackDoesNotAskForABypassVerdict` |
| NC-02 | native entry passes `false` | **RED** `TestTheAutoRedirectEntryAsksForABypassVerdict` |
| NC-03 | the capability becomes shared state (`judgeFlow` stores it in a package-global and reads it back) | **RED** `TestTwoEntriesSharingOneRouterDoNotLeakTheCapability` and `TestTheCapabilityIsPerCallUnderConcurrency` |
| NC-04 | explicit `false` ignored (`resolveFastPathPolicy` result forced true) | **RED** `TestFastPathExplicitPolicyDecides/Case_B_explicit_false` |
| NC-05 | the chain gate drops `fastPathChainReachesLeaf` | **RED** `TestFastPathNestedPrecedence` (opaque inner) and `TestTheAuthorisationBitIsInertOnEveryNonIntrinsicShape` |
| NC-06 | a non-bypassable outbound is treated as bypassable | **RED** `TestFastPathPolicyTruth` (six `allowed when authorised` assertions) |
| NC-07 | `trafficclass.MatchTag` always false | **RED** `TestMatchTagMatrix`, `TestMatchTagDoesNotDependOnPositionOrCase`, `FuzzMatchTagAgreesWithTokenisation`; every Fast Path test stays green |
| NC-08a | the close path counts twice | **RED** `TestTUNDeviceResetReleasesTrackedFlow` ("it must run exactly once") |
| NC-08b | `flowTeardown.close` calls `onClose` a second time | **RED** `TestHalfCloseVerdictAndTeardownRaceSettleOnce`, `TestHalfClosedFlowCloseIsOnceUnderRace` |
| NC-09 | cached drain continues after a failed write | **RED** `TestCachedDrainStopsAtTheFirstWriteFailure` |
| NC-10 | L0/DNS ordering | **NOT APPLICABLE** - this round does not touch that code; its existing tests were run unmodified |

## 10. Gates, builds, measurements

```text
route 439.8s, route/rule, protocol/tun, protocol/direct, option, adapter, adapter/outbound,
common/trafficclass, common/dialer 17.9s, e2e 24.2s                        all ok
go vet ./route/ ./protocol/tun/ ./adapter/ ./option/
  windows/amd64, darwin/arm64, android/arm64, linux/amd64                  ok
go build ./route/ ./protocol/tun/ ./adapter/ ./option/  (GOOS=ios, arm64)  ok
go build -tags "<DEFAULT_BUILD_TAGS_OTHERS>,tvos" ./route/ ./protocol/tun/  ok
gofmt -l route protocol/tun option adapter                                 empty
```

Per-flow decision on this host (idle, `-count=3`):

```text
BenchmarkPreMatchWithoutAConsumer   77.91 / 78.44 / 79.56 ns/op   48 B/op   2 allocs/op
BenchmarkPreMatchForAConsumer      103.40 / 103.20 / 104.00 ns/op 48 B/op   2 allocs/op
```

These are the same two benchmarks as the previous round (97.6–98.2 ns/op then). The difference is not
attributable to this round - the benchmark's flow is a bare direct leaf, which never reached the code
that changed - and it is reported rather than smoothed over. This is a per-flow setup cost on one
host, not a throughput or battery claim.

CI is recorded in §13.

## 11. Documentation and the knowledge base

* `option/fast_path.go`, `route/fast_path.go`, `route/route.go`, `adapter/experimental.go` now describe
  what the code does: no tag detection, the intrinsic rule, the explicit switch, and the entry that
  applies the verdict.
* Historical reports were left as they were. `ios-fast-path-simplification-closeout.md` and its debug
  pass already carry "superseded in part" banners from the previous round; this round does not touch
  them, because they are the record of what was measured then.
* **`KNOWLEDGE/` was not modified** (its rule 5). The refresh it needs, in one place:
  * `modules/fast-path-group-policy.md` §6 "What `fast_path: true` authorises": add one sentence that
    the gate runs only for an entry whose transport applies the verdict, and that automatic AI tag
    detection no longer exists - `fast_path` is the only authorisation a non-`direct` bypassable leaf
    can get.
  * same module §11 "Where the behaviour is pinned": add `route/fast_path_policy_truth_test.go`
    (production shapes), `route/fast_path_consumer_test.go` and
    `protocol/tun/judge_flow_consumer_test.go` (the entry boundary).
  * §2/§8 mention automatic detection implicitly through "silence is not a decision"; the phrase stays
    true, but it now refers to the intrinsic rule only.

## 12. Independent findings (recorded, not changed)

1. **The explicit-bypass branch has three return paths** (`route/route.go:590`, `:597`, and the
   `result.Action` rewrite at `:599`). A mutation that disables one of them changes nothing - verified
   in the previous round - so a negative control there must gate the whole `case`. This is a
   robustness property, and it is also a warning for whoever edits that branch next.
2. **The tun release fixture passes a nil `onClose`** (memory TUN). The fixture's own forward is
   therefore dead in that harness; the flow-level close-once contract lives in `route` instead. Both
   halves are now covered, but by different packages.
3. **`bypassBlockerDetail` was production-dead** and is now a test helper. `direct.BypassBlockers`
   itself is live and untouched.
4. **The `loadbalance` group cannot carry a policy at all** (`box.go` refuses it), so the truth table's
   explicit rows for that shape assert that even a hypothetical map entry is ignored by the carrier
   check. If that check is ever relaxed, those rows change, which is the intent.

## 13. Git record

| item | value |
|---|---|
| production commit | the commit that lands §5's four files |
| tests/docs | the same commit, plus this document |
| branch | `testing`, no new branch, `main` untouched |
| dependencies / submodules / tags / releases | unchanged |
| working tree | clean after the push |

CI on the production SHA: recorded in the task answer, because this document is part of the commit
that follows it.

## 14. Honest completion

Removed: one authorisation branch that could not change a verdict, one duplicated transparency loop,
one production-dead helper, one stale "automatic detection" claim in the configuration docs, and one
test that could fail for a reason it did not own.

Kept, deliberately: the tri-state field (its `false` is the only value that changes a verdict), the
nested-first precedence, every `canFastBypass` refusal, the entry capability, and the two small
transparency functions - now one definition, still two call sites, because caching them would
reintroduce the state this feature spent three rounds removing.

Not done, and not hidden: no measurable performance change from the removal (§4); the accepted-splice
and live-nfqueue paths remain device-unverified; the knowledge base is one sentence and two rows
behind, as listed in §11.

There is no 06.

```text
TASK = FAST_PATH_POLICY_FINAL_SIMPLIFICATION
BRANCH = testing
START_BASELINE_PINNED = NO
PRODUCTION_BYPASSABLE_OUTBOUND_INVENTORY = direct only (protocol/direct.Outbound)
AI_FALLBACK_PRODUCTION_VALUE = PROVEN_REDUNDANT
FAST_PATH_TRUE_VS_UNSET = EQUIVALENT_IN_PRODUCTION (both authorise the transparent-direct shape; true is the only grant for a future non-direct bypassable leaf)
FAST_PATH_FALSE_NATIVE_OFF_SWITCH = PASS
TRANSPARENT_CHAIN_RESCAN = REMOVED (one of two copies; the gate's own call is retained with reason)
ENTRY_CAPABILITY_PER_FLOW = PASS
NON_CONSUMER_GATE = ABSENT
NATIVE_NFQUEUE_CONTRACT = SOURCE_VERIFIED (live nfqueue NOT_EXECUTABLE here)
CONFIG_BACKCOMPAT = PASS
TRAFFIC_CLASS_UNCHANGED = PASS
EXPLICIT_BYPASS_AND_L0 = PASS
SPLICE_COPY_AND_CACHED_EXACTLY_ONCE = PASS
ONCLOSE_ONCE_AND_TEST_FLAKE = FIXED_TEST
LIFECYCLE_OWNERSHIP = PASS
PRODUCTION_DEAD_HELPER = MOVED_TO_TEST
NEGATIVE_CONTROLS = 10 EXECUTED, 10 RED AS EXPECTED, 1 NOT_APPLICABLE (NC-10)
TESTS = route 439.8s + protocol/tun + protocol/direct + option + adapter + common/trafficclass + common/dialer + e2e, all ok
CI_SAME_CODE_SHA = see task answer
DEPENDENCIES_SUBMODULES_TAG_RELEASE = UNCHANGED
KNOWLEDGE_BASELINE = UNCHANGED
PRODUCTION_SHA = see task answer (the commit carrying §5)
FINAL_HEAD = see task answer
ORIGIN_TESTING_MATCH = YES
WORKTREE = CLEAN
OVERALL = DONE_WITH_LIMITATIONS
```
