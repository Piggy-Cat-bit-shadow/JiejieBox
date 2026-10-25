# iOS Fast Path — architecture simplification and closeout (2026-10-25)

This is the subtraction round promised by `composite-fast-path-final-closeout.md` and
`tun-fast-path-value-review.md`: the implicit Direct Fast Path is removed from the iOS flow path,
because on iOS it could not change where a byte went.

Nothing else about `fast_path` moved. The explicit `action: bypass` rule, L0 route sets, Flow/Port,
splice/copy, `traffic_class`, the configuration surface and every other platform are unchanged, and
the sections below say where that was checked rather than asserted.

> Adversarial debug pass: `ios-fast-path-simplification-debug.md`. It re-derived the blast radius,
> ran the TUN data plane under the iOS posture, and closes the one gap it found — the `!C.IsIos`
> platform check is now also proven against a darwin compile, which the unit tests could not do on a
> non-Mac host. No production code changed in that pass.

## 1. Judgment: what was real, and what was not

`fast_path` in this fork never was a speed setting. It is a **group authorisation**: a boolean that
lets a transparent `selector`/`urltest` chain in front of `direct` produce `PreMatchBypass` instead
of `PreMatchContinue`. It does not select a node, does not reorder a group, does not touch splice,
does not affect the system-proxy inbound (a proxy inbound routes through `ConnectionManager`, and
`Router.PreMatch` has exactly two callers: the TUN path in `adapter.JudgeFlow`, and the cloudflared
ICMP handler, which `canFastBypass` refuses at its first predicate), and cannot bypass the TUN: the
verdict only tells the TUN engine "the platform may carry this".

On iOS "the platform may carry this" is indistinguishable from an accept, and that is a property of
the pinned sing-tun revision, not an opinion:

| site in `sing-tun` (pinned) | what it does with the verdict |
|---|---|
| `flow_dispatch.go:530` `ForwardStage.install` | `ActionFlow` builds a port flow; **every other action** falls through to `installAccept` |
| `flow_dispatch.go:597` `installAccept` | records the entry as `ActionAccept` and stores the verdict only so the gvisor forwarders need not judge the flow twice |
| `flow_dispatch.go:401-406` `ForwardStage.judge` | rewrites `ActionBypass && Port != nil` to `ActionFlow`; `adapter.JudgeFlow` deliberately returns **no** Port for a bypass, so this cannot fire |
| `stack_gvisor_tcp.go:88` | switches on `ActionReject`, `ActionDrop` only |
| `stack_gvisor_udp.go:86` | switches on `ActionReject`, `ActionDrop`, `ActionHijackDNS` only |
| `stack_go_udp.go:109` | the same three cases |
| `stack_gvisor_icmp.go:123,185` | groups `ActionFlow` and `ActionBypass` together, and ICMP is never eligible here anyway (`canFastBypass` requires TCP or UDP) |

So the per-flow cost — the `fast_path` tri-state scan, the intrinsic grant to a transparent chain
over `direct`, the AI tag matcher, and the twenty refusal reasons in `canFastBypass` — produced an
`ActionBypass` that the dispatcher immediately treated as `ActionAccept`.

**What this round did:** it made the compiler delete that work on iOS, with a compile-time
constant, instead of calling it and discarding the answer. There is no new state machine, no new
configuration key, and no behaviour change on any platform that can honour the verdict.

**What the owner will notice on iOS:** a configuration that writes `fast_path` still loads, a group
that writes it still validates, and the debug line `pre-match: bypass verdict ...` no longer appears —
because there is no verdict. That is the intended reading: the router no longer reports an
optimisation it does not perform.

## 2. Dependencies and source evidence

The dependency this reasoning depends on, resolved from the tree rather than from a report:

```text
$ go list -m -f '{{.Path}} {{.Version}} => {{.Replace.Path}} {{.Replace.Version}}' github.com/sagernet/sing-tun
github.com/sagernet/sing-tun v0.9.7-0.20261013140216-9ffa5391a2ad => github.com/Piggy-Cat-bit-shadow/sing-tun v0.0.0-20261024234618-a0f9f68fec7d
```

The module is read from
`$GOMODCACHE/github.com/!piggy-!cat-bit-shadow/sing-tun@v0.0.0-20261024234618-a0f9f68fec7d`; the
file and line references in section 1 are from that directory. No pin was changed in this round.

Kernel-side locations:

| location | role |
|---|---|
| `route/fast_path.go` | `implicitDirectFastPathIsExecutable` — the compile-time guard, and the record of why |
| `route/route.go` `(*Router).preMatchFlow` | the single consumer of the authorisation; the guard now wraps the two calls |
| `adapter/router.go:131-149` | `PreMatchBypass` → `tun.FlowVerdict{Action: tun.ActionBypass}`, with the comment explaining why no Port is attached |
| `protocol/tun/inbound.go:974-1042` | `JudgeFlow` order: DNS hijack, FakeIP guard, **L0 before the router**, then `Router.PreMatch` |
| `option/fast_path.go` | the tri-state `FastPathPolicy` and its (now stated) iOS no-op semantics |
| `box.go:795-818`, `845-866` | `fast_path` is group-only (a leaf is a configuration error), and the per-group map the router reads |
| `constant/os.go:9,19` | `IsDarwin` includes iOS; `IsIos` does not include macOS. The guard uses `IsIos` |

## 3. Data path, before and after

Identical rows are marked as such deliberately: this round changed exactly one of them.

| path | before (iOS) | after (iOS) | other platforms |
|---|---|---|---|
| System proxy (HTTP/Mixed) | proxy inbound → `ConnectionManager` → router rule → outbound | **unchanged** | unchanged |
| TUN → Direct, TCP/UDP | `PreMatch` computed authorisation + eligibility; on success returned `PreMatchBypass`, which the dispatcher installed as an accept entry | `PreMatch` skips both calls; the flow continues to the `FlowOutbound` probe and returns the same accept-equivalent verdict | unchanged (the verdict can still be honoured) |
| TUN → proxy (VLESS/MASQUE/AnyTLS/...) | eligibility refused (`BypassRefusedOutboundNotBypassable`/`Semantics`) and the flow continued | the refusal is not computed; the flow continues | unchanged |
| TUN → Direct, ICMP | never eligible (`BypassRefusedNetwork`); `FlowOutbound` Port path | **unchanged** (the guard does not wrap the Port path) | unchanged |
| TCP/UDP splice after the socket exists | automatic attempt, independent of `fast_path` | **unchanged** | unchanged |
| Splice refused → copy | cached bytes delivered exactly once | **unchanged** | unchanged |
| L0 (`route_address_set` / `route_exclude_address_set`) | decided in the TUN inbound before the router | **unchanged** | unchanged |
| explicit `action: bypass` | rule loop returns `PreMatchBypass` without consulting `canFastBypass` | **unchanged** | unchanged |
| `traffic_class` / AI keywords | resolved after the fast-path decision in the same function | **unchanged** | unchanged |
| config load with `fast_path` | accepted on selector/urltest, refused on a leaf | **unchanged** | unchanged |

The last row matters for compatibility: the key is not removed, so a shared configuration keeps one
meaning. On iOS the field is inert, and `option/fast_path.go` now says so in the field's own
documentation.

## 4. Removed and retained

**Removed on iOS only, by compilation:**

| what | why it is safe |
|---|---|
| the `resolveFastPathRequest` call in `preMatchFlow` | its result only fed `canFastBypass`, whose verdict is accept-equivalent on iOS |
| the `canFastBypass` call in `preMatchFlow` | twenty refusal reasons that could not change the outcome; the verdict had no Port, no Tracker, no buffer and no flow |
| the `pre-match: bypass verdict ...` debug line on iOS | it described a decision that no longer exists; keeping it would report an optimisation that does not happen |

**Retained everywhere:**

| what | why |
|---|---|
| `resolveFastPathRequest`, `canFastBypass`, `BypassVerdict`, `FastPathPolicies`, `FastPathAuthorizedGroup`, `FastPathTransparent` | live on macOS, Linux, Windows and Android; deleted only from the iOS build |
| the `fast_path` config field, its group-only validation and its schema entry | config compatibility; removing it would break shared configurations for no gain |
| explicit `action: bypass`, L0, Flow/Port, splice, copy, `traffic_class`, trackers, power report | untouched contracts, each with its own tests |
| the AI matcher `trafficclass.MatchTag` | shared with `traffic_class`; only its fast-path consumer is compiled out on iOS |

**Added:** the constant + its rationale, three tests, one CI gate
(`scripts/ci/verify-ios-fast-path-guard.sh`, wired into `Verify`), and documentation notes in
`option/fast_path.go` and `adapter/experimental.go`.

## 5. Lifecycle and ownership

The table answers the question the subtraction has to answer: did removing the verdict remove any
ownership? It did not, because the verdict owned nothing — but every row was re-checked rather than
assumed.

| path | who creates the resource | where the choice commits | owner of fd / GoConn / buffer / tracker | fallback | close / onClose | exactly once | counted |
|---|---|---|---|---|---|---|---|
| System proxy TCP/UDP | proxy inbound conn | `RouteConnection` rule match | connection manager; no GoConn, no TUN buffer | ordinary dial errors | existing onClose chain | unchanged | yes, unchanged |
| iOS TUN TCP Direct → splice refused | TUN engine creates the flow; `direct` dials | `routeConnection` after `PreMatch` | `*tun.GoConn` + outbound conn; buffers owned by the copy loops | splice refusal falls back to copy **before** acceptance | `common.Close` + GoConn close, once | unchanged | splice diagnostics unchanged |
| iOS TUN TCP proxy → splice refused (packaged conn) | TUN flow; proxy outbound owns the tunnel | `routeConnection` | `trackedConn` wrapper; the packaged conn is not a `syscall.Conn` | refusal at the source, copy | unchanged | unchanged | unchanged |
| iOS TUN UDP Direct/proxy | UDP NAT session | `routePacketConnection` | `UDPNatConn` + per-packet buffers | splice refusal → copy, cache delivered once | NAT cleanup / idle timeout | unchanged | unchanged |
| FakeIP / sniff / override destination | router metadata | rule action before outbound selection | n/a | n/a | n/a | n/a | unchanged |
| explicit `action: bypass` | rule loop | `preMatchFlow`'s rule action branch | none: no Port, no tracker, no bytes | n/a | n/a | unchanged | not counted as an implicit bypass (already the case) |
| L0 | TUN inbound | before `Router.PreMatch` | OS routing table | n/a | n/a | unchanged | unchanged |
| Flow/Port (ICMP) | `direct` Port | `PreMatchFlow` + committed chain | Port owns the flow | port close | unchanged | unchanged | unchanged |
| network switch / setup cancel / close-all / repeated Close / half-close / OOM reclaim | existing owners | existing epochs and close handlers | unchanged | unchanged | unchanged | unchanged | unchanged |

**The changed row is the second one, and only on iOS:** the flow now reaches the outbound through
the same rule it used when the fast-path gate refused — which was already the majority case —
instead of through a verdict the dispatcher turned into the identical accept entry. No resource
that the removed verdict could have owned exists to be double-closed, and no second selection is
introduced: `resolveOutbound(..., commit=false)` in `preMatchFlow` remains a preview, exactly as
before.

## 6. Evidence: negative controls, tests, CI

### 6.1 The subtraction is in the emitted code, not in a comment

`C.IsIos` is a compile-time constant, so the guarded block is removed by the compiler. Measured on
this tree (`GOOS=ios GOARCH=arm64 CGO_ENABLED=0 go build -gcflags='github.com/sagernet/sing-box/route=-S' ./route/`),
counting `CALL ...(SB)` lines:

| symbol | before | after |
|---|---|---|
| `route.resolveFastPathRequest` | 1 | **0** |
| `route.(*Router).canFastBypass` | 1 | **0** |
| `route.resolveTrafficClass` (control: must stay) | 3 | **3** |
| same two fast-path symbols on `GOOS=linux` (control: must stay) | 1 / 1 | **1 / 1** |

`(*Router).preMatchFlow` is still present in the iOS output, so the absence above is a removed call
inside a compiled function, not a dropped function.

### 6.2 The gate that keeps it that way

`scripts/ci/verify-ios-fast-path-guard.sh` reproduces the measurement and fails if either call comes
back. It is falsifiable by construction: it also requires the `traffic_class` call in the same iOS
output (proving the package really was compiled), both fast-path calls in the **Linux** output
(proving the guard is not a blanket removal), and both fast-path calls in the **macOS** output
(proving the guard is not `!C.IsDarwin`, which would silence the mechanism on a platform that still
honours it). The debug pass added the darwin control; see `ios-fast-path-simplification-debug.md` §4.

### 6.3 Negative controls (run, then reverted; nothing of them is committed)

| control | mutation | expected | observed |
|---|---|---|---|
| A | `implicitDirectFastPathIsExecutable = false` (simulate the iOS posture on the host) | the posture test keeps passing on its iOS branch, and the eligibility suite notices | `TestTheImplicitFastPathFiresExactlyWhereTheGuardAllowsIt` PASS; `TestFastBypassPlainDirectTCPEligible` **FAIL** ("must take the fast path") |
| B | `implicitDirectFastPathIsExecutable = true` (guard removed) | the guard gate fails and names the calls | gate **FAIL**: "the iOS build still calls the implicit Direct Fast Path decision (resolveFastPathRequest: 1 call(s), canFastBypass: 1 call(s))" |
| B' | restored to `!C.IsIos` | the gate passes | PASS (0 / 0 on iOS, 1 / 1 off iOS) |

Control A is the one that matters for the test suite: it proves the iOS branch of the new test is
reachable and that turning the mechanism off everywhere would be caught by tests that already exist.

### 6.4 Tests run on this tree

```text
go test -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" \
  ./route/... ./protocol/tun/... ./protocol/direct/... ./option/... ./adapter/... \
  ./common/trafficclass/... ./common/dialer/...
ok  route (441.7s) - route/rule - protocol/tun - protocol/direct - option - adapter -
    adapter/outbound - common/trafficclass - common/dialer          (all ok)

go test -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./e2e/...
ok  e2e (23.1s)
```

Named data-plane cases, run explicitly:

```text
PASS TestSpliceAttemptIsIndependentOfFastPath (unset / true / false)
PASS TestDirectTUNFlowTakesTheSpliceDecisionAndFallsBack
PASS TestProxiedTUNFlowStillHasAGoConnSource
PASS TestSystemProxyTCPSpliceIsRefusedAtTheSource
PASS TestProxiedSpliceRejectionDeliversAMultiSegmentPayload
PASS TestCachedDrainStopsAtTheFirstWriteFailure
PASS TestAnExplicitBypassRuleIgnoresTheFastPathGate
```

New in this round:

```text
PASS TestTheImplicitFastPathGuardIsExactlyTheIOSPlatformCheck
PASS TestTheImplicitFastPathFiresExactlyWhereTheGuardAllowsIt
PASS TestTheExplicitBypassRuleSurvivesThePlatformGuard
```

`gofmt -l` over the touched packages is clean, `go build ./...` (untagged and tagged) and
`go vet ./route ./option ./adapter` are clean.

### 6.5 CI

Every gate below ran on `cc21e75485465b7b81c217ef997e56f66c91735e`, the production commit:

| workflow | run | result |
|---|---|---|
| Verify — gofmt, structural gates (including the new `iOS fast-path guard`), untagged and tagged builds, the build matrix and the race suite | [40150190306](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40150190306) | success |
| Android Core ARM64 | [40150190303](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40150190303) | success |
| Mobile contract verification | [40150190317](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40150190317) | success |
| Release contract verification | [40150190301](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40150190301) | success |
| macOS arm64 — `go test` of `./route ./adapter/... ./option ./common/trafficclass` under the macOS tags, then the core build and smoke test | [40150235723](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40150235723) | success |

Linux is covered by the Verify build matrix and race suite on `ubuntu-latest`; Windows by the full
local run in section 6.4 on the same tree; macOS by the dispatched run, whose unit-test step names
`./route`, `./adapter/...`, `./option` and `./common/trafficclass` explicitly. The dispatched run's
deep checks (`go vet`, race, fuzz, Cronet A/B) are behind its `deep_checks` input and stayed
skipped; the race suite ran in Verify instead.

### 6.6 NOT_VERIFIED

* Real iOS device behaviour (no device, and none is required by the task): whether NEProxySettings
  flows and TUN flows behave identically on hardware. The claims here are about what the pinned
  dispatcher does with a verdict, which is source-level and reproducible on any host.
* A `GOOS=ios` **test run**. The iOS posture is proven by compilation and by control A, not by
  executing the suite as an iOS binary.

## 7. Performance, stated honestly

No iOS throughput number is claimed, and none was measured. What changed is per-flow setup work, not
the byte-moving path:

* removed on iOS: one chain policy scan (map lookups), one AI tag match attempt per transparent group
  in the chain, and the `canFastBypass` predicate chain plus its `CanBypass` call;
* unchanged: splice attempt, copy loops, packet handling, QUIC, DNS, sniffing, trackers;
* the historical micro-benchmarks for the eligibility gate (about 14-41 ns/flow depending on what was
  measured) and for the whole pre-match decision (about 135 ns/flow) are separate measurements and
  must not be added together; they are context for the scale, not a claim about this change;
* on iOS the mechanism is now **zero instructions**, because the compiler removes the calls. That is
  the honest description of the gain: less code executed, not a faster transfer.

## 8. Git record

| item | value |
|---|---|
| observed HEAD at the start of this round (evidence only, not a baseline) | `d94aca96ee5b936ab0868f16743569d72e505fd7` |
| branch | `testing` (no new branch, no `main` change) |
| this round's commit | `cc21e75485465b7b81c217ef997e56f66c91735e` — `refactor(route): compile the implicit Direct Fast Path out on iOS` |
| later commit | the commit that adds this document (documentation only, no code, no workflow) |
| files changed | `route/fast_path.go`, `route/route.go`, `route/fast_path_platform_posture_test.go`, `option/fast_path.go`, `adapter/experimental.go`, `scripts/ci/verify-ios-fast-path-guard.sh`, `.github/workflows/verify.yml`, this document |
| tags / releases | none created, none moved |
| dependency pins | none changed (`go.mod` / `go.sum` untouched) |
| submodules | none changed |
| working tree after the push | clean |

## 9. Independent findings

1. **`PreMatchBypass` carries an `Outbound` field that no consumer reads.** `adapter.JudgeFlow`
   ignores it for the bypass action (it only forwards `Destination`/`UDPTimeout`/`NewTracker` for
   `PreMatchFlow`). It is harmless, and it is why the removal changes no ownership.
2. **The `ActionBypass + Port` rewrite is the only reason the old code needed the "no Port" rule.**
   `adapter/router.go` has a long comment about it; the rewrite is real in the pinned sing-tun,
   which is worth knowing before anyone "simplifies" that comment away.
3. **`TestSpliceAttemptIsIndependentOfFastPath` already proves the splice boundary for all three
   `fast_path` states** on this memory platform; this round did not duplicate it, only re-ran it.
4. **macOS has the same dispatcher equivalence, and is deliberately not changed.** It would be the
   same subtraction with the same evidence, but it changes a shipping platform's behaviour and that
   is a product decision, not an inference from an equivalence proof. Recorded here as a finding,
   not applied.
5. **The `fast_path` map is still built on iOS** (in `box.go`) and is now read by nothing. It is
   startup-only state, so it was left alone: moving the platform check into configuration loading
   would spread the condition across one more file for no runtime gain, and the field's validation
   is a cross-platform contract that should not become iOS-specific.

## Result

```text
SCOPE = IOS_FAST_PATH_SIMPLIFICATION_ONLY
BRANCH = testing
BASELINE_PINNED = NO
IOS_REDUNDANT_IMPLICIT_BYPASS_ELIMINATED = PASS
SYSTEM_PROXY_UNCHANGED = PASS
SPLICE_AND_COPY_UNCHANGED = PASS
TRAFFIC_CLASS_UNCHANGED = PASS
EXPLICIT_BYPASS_AND_L0_UNCHANGED = PASS
LIFECYCLE_OWNERSHIP = PASS
CROSS_PLATFORM_REGRESSION = PASS
CONFIG_COMPATIBILITY = PASS
CI = PASS
PRODUCTION_CHANGE = route: the implicit Direct Fast Path decision is compiled out on iOS by a
                   compile-time !C.IsIos guard; no other platform, no configuration key, no
                   dependency and no submodule changed
FINAL_SHA = cc21e75485465b7b81c217ef997e56f66c91735e
ORIGIN_TESTING_MATCH = YES
OVERALL = DONE
```
