# Cross-platform Fast Path unification — closeout (2026-10-25)

This round answers the question the iOS round could not: **who can actually act on an implicit Direct
Fast Path verdict, and what should every other entry do instead?** The answer turned out not to be a
platform list, so the mechanism is no longer platform-conditional.

The production commit is `8ef6231bd`. It replaces `implicitDirectFastPathIsExecutable = !C.IsIos` with
a capability the calling entry sets, and in doing so removes the mechanism from four platforms and
from every plain TUN mode, while leaving the one real consumer exactly as it was.

## 1. The consumer inventory (the only fact that decides this)

Everything below is read from the pinned `sing-tun v0.0.0-20261024234618-a0f9f68fec7d`.

| where | what it does with the verdict |
|---|---|
| `nfqueue_linux.go:310` | the only `handler.JudgeFlow` call whose caller acts on the answer |
| `nfqueue_linux.go:332-335` `applyVerdict` | `ActionBypass` → `NfRepeat` + `h.outputMark` — the flow leaves the TUN and the kernel routes it |
| `flow_dispatch.go:403` | the ForwardStage's judgement |
| `flow_dispatch.go:530` `install`, `:597` `installAccept` | `ActionFlow` builds a port flow; **every other action, accept and bypass alike, installs an accept entry** |
| `flow_dispatch.go:401-406` `judge()` | rewrites `ActionBypass && Port != nil` to `ActionFlow`; a bypass has no Port, so the branch cannot fire |
| `stack_gvisor_tcp.go:88`, `stack_gvisor_udp.go:86`, `stack_go_udp.go:109`, `stack_gvisor_icmp.go:121` | branch on `Reject`, `Drop`, `HijackDNS` only |

Build tags make the inventory platform-shaped, but not in the way the previous guard assumed:

```text
nfqueue_linux.go        //go:build linux      -> compiled for linux AND android (android satisfies linux)
redirect_linux.go       linux               -> the nfqueue-based auto-redirect
redirect_stub.go        every other GOOS    -> no nfqueue at all
```

So `windows`, `darwin` and `ios` **cannot** consume a bypass verdict (no nfqueue code exists), while
`linux` and `android` can — but only when the `auto_redirect` mode is enabled, which is a per-inbound
configuration, `option/tun.go` → `protocol/tun/inbound.go:453-500`.

## 2. Platform × entry × mode

| platform | entry that judges flows | can act on a Bypass verdict? | before this round | after |
|---|---|---|---|---|
| iOS | NE packet-tunnel TUN stack | no (no nfqueue in the build) | chain compiled out | chain not called |
| macOS | NE packet-tunnel TUN stack | no | chain computed, verdict discarded | not called |
| Windows | system TUN stack | no | chain computed, verdict discarded | not called |
| Linux, plain TUN | go / gVisor / system stack | no | chain computed, verdict discarded | not called |
| Linux, `auto_redirect` | nfqueue → `applyVerdict` | **yes** | chain computed, verdict applied | unchanged |
| Android, VpnService TUN | go / gVisor stack | no | chain computed, verdict discarded | not called |
| Android, privileged `auto_redirect` | nfqueue → `applyVerdict` | **yes** | chain computed, verdict applied | unchanged |
| any, system proxy / `ProxyService` | `ConnectionManager` | n/a — never reaches `PreMatch` | — | — |

The iOS round's conclusion was right and this round keeps its outcome; what changes is the reason and
the scope. macOS, Windows and every plain TUN were doing the same useless work, and a platform
constant could not say so, because Linux and Android are *both* in the table twice.

## 3. What changed

| file | change | why |
|---|---|---|
| `adapter/inbound.go` +27 | `InboundContext.ConsumesBypassVerdict` with the consumer inventory and the "mode, not platform" argument | the capability has to travel with the flow into `PreMatch` |
| `protocol/tun/inbound.go` +25/-2 | `JudgeFlow` split into a stack wrapper (false) and `judgeFlow(..., bool)`; `(*autoRedirectHandler).JudgeFlow` passes true | the entry that owns the nfqueue is the only one that knows its verdict is applied |
| `route/route.go` +6/-3 | `if metadata.ConsumesBypassVerdict` replaces the platform constant | the gate is now consumer-owned |
| `route/fast_path.go` -51 | the constant and its 50-line platform argument are gone, replaced by an 8-line note | the argument lives on the field now |
| `route/fast_path_platform_posture_test.go` -80 | deleted | it asserted the constant |
| `scripts/ci/verify-ios-fast-path-guard.sh` -131, `verify.yml` -15 | deleted | the assembly gate existed to catch `!C.IsDarwin`; the constant it guarded is gone |
| `route/fast_bypass_test.go` +10 | the shared helper now models the consumer | the gate is only reachable for a consumer |
| `protocol/tun/l0_authoritative_vs_reverse_mapping_test.go` 18 changed | the subtests that used "the router's fast path bypasses it" as their not-rejected observable now ask through the consumer entry | that observable no longer exists on the stack entry |
| `e2e/tun_fast_path_dataplane_test.go` +10 | the three judge harnesses declare themselves consumers | they exist to test the verdict |
| `route/fast_path_consumer_test.go` +67, `protocol/tun/judge_flow_consumer_test.go` +131 | new | pin the new boundary |

Net: **+4 production lines, -146 lines of CI, -80 lines of a test whose subject was deleted**. The
weight moved from a platform constant and its out-of-band assembly gate into one flow field and four
tests that run on any host.

Not touched: explicit `action: bypass`, L0 route sets, Flow/Port, splice/copy, `traffic_class`, the
`fast_path` configuration surface, `go.mod`/`go.sum`/submodules, and every sing-tun consumer.

## 4. Decision flow, before and after

```text
before                                     after
------                                     -----
any TUN/splice entry                        any TUN/splice entry
  Inbound.JudgeFlow                           Inbound.JudgeFlow        (stack, no consumer)
    DNS / FakeIP / L0  (unchanged)              DNS / FakeIP / L0      (unchanged)
    adapter.JudgeFlow -> Router.PreMatch         adapter.JudgeFlow -> Router.PreMatch
      preMatchFlow                                 preMatchFlow
        [platform constant?]                         [ConsumesBypassVerdict?]
          resolveFastPathRequest                       ... only for the auto-redirect entry ...
          canFastBypass (20 refusals)
        Flow/Port probe                              Flow/Port probe
        -> Bypass or Continue                        -> Bypass (consumer) or Continue

                                            auto_redirect entry
                                              (*autoRedirectHandler).JudgeFlow
                                                -> judgeFlow(..., true)
```

The `auto_redirect` entry additionally keeps the second judgement a re-injected flow gets from the
stack — but that one is now the cheap path, because the stack cannot consume a verdict anyway.

## 5. Options considered

| option | verdict |
|---|---|
| **A. a per-platform constant list** (`!C.IsIos && !C.IsDarwin && !C.IsWindows && ...`) | rejected: cannot separate plain TUN from `auto_redirect` in the same Linux/Android binary, grows exactly the `if platform` chain this round exists to remove, and every future entry needs a new audit |
| **B. the consumer entry sets a capability on the flow** | **chosen**: one rule, correct for both modes of both platforms, testable on any host without a device |
| **C. move the fast-path decision into `protocol/tun`** (call `canFastBypass` from the auto-redirect handler) | rejected: `canFastBypass` needs the router's resolved chain, outbound and metadata (process, sniff, FakeIP, tracker), so the decision would either duplicate the rule loop or need a second router entry — the state machine this round is trying to avoid |
| **D. keep the iOS constant** | rejected: leaves macOS/Windows/plain-TUN work in place and keeps the `!C.IsDarwin` hazard that the previous round's debug found |

## 6. The real subtraction, and what was not subtracted

Removed from the per-flow path of **every non-consumer entry** (iOS, macOS, Windows, Linux plain TUN,
Android VpnService, and the stack half of `auto_redirect`):

* `resolveFastPathRequest`: a walk of the outbound chain, a `trafficclass.MatchTag` per transparent
  group, and the three-state policy resolution;
* `canFastBypass`: up to twenty refusal checks including the `CanBypass` probe of the dial surface;
* the `preMatchBypass` debug log line and its arguments.

That is setup work per flow, not per packet. No throughput or latency number is claimed here: the
change is "a branch and a chain scan that could not affect the outcome no longer run", and the cost
of the decision on the consumer path is unchanged. Nothing was benchmarked, so nothing is reported as
a speed-up.

Not subtracted, and verified unchanged: explicit `action: bypass` (still produces `ActionBypass` on
every entry — it is an operator instruction, not this optimisation), L0 route sets (decided before the
router), Flow/Port, splice and copy, `traffic_class` (still resolved in the same function and still a
separate system), the configuration surface and its validation.

## 7. Lifecycle and ownership

The verdict was never an owner: it carries no `Port`, no tracker, no buffer and no close handler, so
removing it from an entry cannot change who closes what. The ledger from
`ios-fast-path-simplification-closeout.md` §6 still holds row for row; this round adds one row:

| path | creator | commit point | owner | fallback | terminal | counted |
|---|---|---|---|---|---|---|
| `auto_redirect` flow judged by nfqueue | nfqueue packet | `applyVerdict` | kernel routes it (bypass) or the TUN stack takes it (input mark) | `NfRepeat` + input mark | stack flow / kernel flow | splice diagnostics unchanged |

The two entries share `judgeFlow`'s body, so they cannot drift into two flow policies; the only
difference is the capability bit. That property is pinned by
`TestBothEntriesShareTheSameFlowPolicy` (DNS hijack) and `TestBothEntriesShareTheSameL0Policy` (route
sets), both of which assert the same verdict from both entries.

## 8. Tests and negative controls

New:

| test | pins |
|---|---|
| `route::TestTheImplicitFastPathIsNotComputedWithoutABypassConsumer` | no capability → no bypass verdict **and** the counting outbound's `CanBypass` is never called (not "called and discarded") |
| `route::TestTheImplicitFastPathIsComputedForABypassConsumer` | the control: the same flow with the capability still takes the fast path |
| `route::TestAnExplicitBypassRuleIsIndependentOfABypassConsumer` | explicit `action: bypass` survives the missing capability |
| `protocol/tun::TestTheTUNStackDoesNotAskForABypassVerdict` | the stack entry passes false |
| `protocol/tun::TestTheAutoRedirectEntryAsksForABypassVerdict` | the nfqueue entry passes true |
| `protocol/tun::TestBothEntriesShareTheSameFlowPolicy` | DNS hijack identical on both entries, without the router |
| `protocol/tun::TestBothEntriesShareTheSameL0Policy` | L0 miss identical on both entries, without the router |

Negative controls, each run against the real test and reverted (hashes checked after restore):

| ID | mutation | result |
|---|---|---|
| NC1 | `Inbound.JudgeFlow` passes `true` | **RED** `TestTheTUNStackDoesNotAskForABypassVerdict`: "the stack installs a Bypass verdict as an accept entry, so it must not ask for one" |
| NC2 | `(*autoRedirectHandler).JudgeFlow` passes `false` | **RED** `TestTheAutoRedirectEntryAsksForABypassVerdict`: "the nfqueue consumer applies a Bypass verdict, so its entry is the one that asks for it" |
| NC3 | `route` ignores the bit (`if true`) | **RED** `TestTheImplicitFastPathIsNotComputedWithoutABypassConsumer`: "a caller that cannot apply a Bypass verdict must not be given one" |
| NC4 | explicit bypass gated by the capability | **RED** `TestAnExplicitBypassRuleIsIndependentOfABypassConsumer` |

NC4 also showed that the older `TestAnExplicitBypassRuleIgnoresTheFastPathGate` stays green under the
mutation, because its metadata now models a consumer. That is correct for what it tests (the gate does
not swallow explicit bypass) and it is why the new test sets the capability explicitly to false: the
two are not redundant.

## 9. Build, test and CI evidence

Local, on the change's own tree:

```text
route 441.7s, route/rule, protocol/tun, protocol/direct, option, adapter, adapter/outbound,
common/trafficclass, common/dialer 16.8s, e2e 22.8s            all ok
go build ./...                                                   ok
cross-compile ./route ./protocol/tun ./adapter ./option ./protocol/direct
  linux/amd64, android/arm64, windows/amd64, darwin/arm64, ios/arm64   ok
gofmt -l                                                         empty
```

`go build ./...` for android fails in `experimental/boxdd` (`invalid reference to os.checkPidfdOnce`)
and for ios in the cgo-less link step; both are the daemon's linker contract, not this change — the
five packages above compile for every target.

CI on `8ef6231bd` (all success):

| workflow | run |
|---|---|
| Verify (includes the `-race` run over `./route/... ./protocol/tun/... ./adapter/...`) | `40157380698` |
| Android Core ARM64 | `40157380707` |
| Mobile contract verification | `40157380640` |
| Release contract verification | `40157380633` |
| macOS arm64 (dispatched, darwin tests on a real macOS runner) | `40158472003` |

## 10. iOS re-verification

The iOS outcome is unchanged and now has a host-independent proof:

* `ios` cannot enter `auto_redirect` (`tun.NewAutoRedirect` and the nfqueue file are not in that build),
  so `Inbound.autoRedirect` is always nil and `(*autoRedirectHandler).JudgeFlow` is never called;
* even if it were, `ConsumesBypassVerdict` would have to be set, and the only setter is that handler;
* the previous round's assembly evidence (0 `resolveFastPathRequest` / 0 `canFastBypass` calls in the
  iOS build) no longer describes the code, and the gate that produced it is deleted with the constant.
  The replacement is `protocol/tun::TestTheTUNStackDoesNotAskForABypassVerdict`, which runs in the
  `-race` step of Verify on every push and does not need an iOS binary.

## 11. Limits

* No device was used: no iOS, macOS, Android or Linux device ran this build. `GOOS=ios`/`android`
  checks are compile-only.
* The `auto_redirect` consumer path itself was not executed: it needs nfqueue and netlink privileges
  that this environment does not have. Its wiring is read from the pinned sing-tun source and from
  `protocol/tun/redirect_platform_linux.go`, and the entry split is pinned by tests — but "the nfqueue
  really applies the mark on a live kernel" is `NOT_VERIFIED` here.
* sing-tun is pinned, not modified; the consumer inventory is a source-level statement about that pin.
* An Android privileged build enters `auto_redirect` through `redirect_route_vpnservice_android.go`;
  no such build was produced or run here.

## 12. Knowledge base

`KNOWLEDGE/modules/fast-path-group-policy.md` §6 and §11 describe the eligibility gate without this
round's precondition, and §11's table of pinning tests predates the new consumer tests. Per the
knowledge base's own rule 5 (no refresh during normal development) the module was not edited; the
divergence is recorded here so a future refresh can close it with one sentence in §6 - "the gate runs
only for an entry whose transport applies the verdict" - and two rows in §11. Its current pinning
tests (`option/fast_path_test.go`, `route/fast_path_group_test.go`,
`box_fast_path_group_policy_test.go`, `route/fast_path_debug_test.go`) all still exist and pass.

## Result
```text
TASK = CROSS_PLATFORM_FAST_PATH_UNIFICATION
SCOPE = SHARED_ROUTER_BOUNDARY_AND_PLATFORM_CAPABILITY_ONLY
BRANCH = testing
BASELINE_PINNED = NO
TRUE_CONSUMER_IDENTIFIED = PASS
PLATFORM_MATRIX = PASS
NON_CONSUMER_ENTRIES_SKIP_THE_CHAIN = PASS
CONSUMER_ENTRY_UNCHANGED = PASS
EXPLICIT_BYPASS_FLOW_PORT_L0 = PASS
TRAFFIC_CLASS_PRESERVED = PASS
SPLICE_AND_LIFECYCLE = PASS
IOS_SIMPLIFICATION_PRESERVED = PASS
OTHER_PLATFORM_BUILDS = PASS
NEGATIVE_CONTROLS = PASS
DEVICE_REQUIRED = NO
CI_SAME_CODE_SHA = PASS
FINAL_SHA = 8ef6231bdb7571ef7f187ada6e7e029ae5fc9eaa
ORIGIN_TESTING_MATCH = YES
WORKTREE = CLEAN
OVERALL = DONE
```
