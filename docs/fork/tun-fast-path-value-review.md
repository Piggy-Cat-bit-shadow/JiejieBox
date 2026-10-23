# TUN fast path: value review, real path map, and the D0 verdict

Status: **D0_VALUE = NO_DISTINCT_FAST_PATH** for the group `fast_path` feature. The only real
TUN data-plane difference this fork has is the post-handshake splice/`sendfile` handover, and that is
already automatic, already default, and not what `fast_path` controls. No production behaviour was
changed in this round; the code changes are a verification fix and tests, and they are listed in
[§7](#7-what-this-round-changed).

Every claim below carries one of three labels:

| Label | Meaning |
| --- | --- |
| `CURRENT_CONFIRMED` | Re-measured in this round against the tree and the module the build resolves. |
| `HISTORICAL_EVIDENCE` | From an earlier round, still consistent with the code, not re-measured. |
| `NEEDS_REVALIDATION` | From an earlier round, or unobservable in this environment. Not used as a basis for a decision. |

The measurement environment is one Windows machine (Windows, amd64, Ryzen 7 4800H), Go 1.25.5 from
the local toolchain, `release/DEFAULT_BUILD_TAGS_OTHERS`. Real device, Linux nfqueue, macOS, iOS and
Android are **not** available here and are marked NOT_VERIFIED rather than PASS.

---

## 1. Snapshot

Measured before any edit (`CURRENT_CONFIRMED`):

| Field | Value |
| --- | --- |
| Repository | `Piggy-Cat-bit-shadow/sing-box` |
| Branch | `testing` (the only long-term source branch) |
| `HEAD` / `origin/testing` | `87c43f8ad23f2b7849f1287f0ec00cde5b2d40b3` (2026-10-22 17:36 +0800) |
| Worktree | clean (`git status --porcelain=v1 -uall` empty) |
| Submodules | android `ec61030`, apple `cdaaf22`, desktop `d502365` - untouched |
| Toolchain | `go1.25.5 windows/amd64`, `GOTOOLCHAIN=local` |
| Build tags | `release/DEFAULT_BUILD_TAGS_OTHERS` (the Darwin profile in `DEFAULT_BUILD_TAGS` cannot build on Windows) |
| `require github.com/sagernet/sing-tun` | `v0.9.7-0.20261013140216-9ffa5391a2ad` - **not the code that is built** |
| `replace github.com/sagernet/sing-tun` | `github.com/Piggy-Cat-bit-shadow/sing-tun v0.0.0-20261020085711-109186a15f3b` |
| `go list -m -json` resolved dir | `...\sing-tun@v0.0.0-20261020085711-109186a15f3b` (`Replace.Dir` is set) |
| Pinned fork commit | `109186a15f3b2b57df7e8634818873ab5b927c46` (fork branch `fix/listenconfig-ipv6-only-selfheal`) |

The replace line is load-bearing for every conclusion below: the upstream pin in `go.mod` is not the
source of the running binary. The two commits are also not interchangeable - the fork's
`flow_dispatch.go` differs from upstream's (the fork adds forward-NAT and return-path work).

The `direct-offload.md` claim that the L1 behaviour was "re-verified at
`v0.9.7-0.20261006124248-d769a7080ca2`, the revision this fork pins" had gone stale: the fork has
moved its pin several times since (2026-10-20 is the current one). The claim was re-measured against
the current pin in this round and the document was corrected.

---

## 2. The question, and the answer in one paragraph

The brief's central question is whether ordinary TUN has **two physically different data paths**
called "fast" and "normal". It does not. `tun.ActionBypass` is consumed by exactly one transport in
the pinned sing-tun - Linux `auto_redirect` through nfqueue, where it becomes `NfRepeat` with the
output mark and the packet leaves the queue for the OS to route. All three TUN stacks - and `stack`
unset, which is the default, selects the Go stack (`stack.go`: `case "", "go"` -> `NewGo`) - treat
`ActionBypass` exactly like `ActionAccept`: the dispatcher installs an accept entry, does not consume
the packet, and the stack terminates the connection in userspace. The verdict is a control-plane
authorisation with no data-plane counterpart in ordinary TUN.

The group `fast_path` field does not even change that verdict on a shipped configuration. It is
inert in the strongest sense: see §4. What is real, and what the brief asks us to look for, is the
**post-handshake splice handover**, which is attempted for every TUN connection that reaches
`ConnectionManager.NewConnection` (TCP, TLS-rewrite flows excepted) and `NewPacketConnection` (UDP) -
no group, no field, no opt-in. That is the optimisation that already applies to all eligible TUN
traffic.

---

## 3. The real path map

### 3.1 Control verdict / actual owner / who moves the bytes

| Path | Control verdict | Actual owner of the flow | Bytes move by | Label |
| --- | --- | --- | --- | --- |
| System proxy (`mixed`/SOCKS/HTTP -> router -> outbound -> socket) | `PreMatchBypass` is refused at `canFastBypass`'s first check (`InboundType != tun`) | userspace connection manager | userspace copy (the inbound stream is not a `*tun.GoConn`, so splice is refused) | `CURRENT_CONFIRMED` (code), unchanged this round |
| Ordinary TUN, default `go` stack | `JudgeFlow` -> router -> `ActionAccept` or `ActionBypass` | `*tun.GoConn` / `tun.UDPNatConn` in the go stack | userspace copy, or the kernel after a splice handover | `CURRENT_CONFIRMED` |
| Ordinary TUN, `gvisor` stack | same router; `TCPForwarder.Forward` honours only Reject/Drop | gVisor netstack connection | userspace copy / splice attempt | `HISTORICAL_EVIDENCE` (code path unchanged) |
| Ordinary TUN, `system` stack | same router; not consumed -> userspace | local NAT listener | userspace copy / splice attempt | `HISTORICAL_EVIDENCE` |
| Linux `auto_redirect` (nfqueue) | same router, `JudgeFlow` called by the nfqueue handler | the kernel, after `NfRepeat` + output mark | kernel routing | `CURRENT_CONFIRMED` (source), **NOT_VERIFIED** on device |
| L0 route sets (`route_address_set` / `route_exclude_address_set`) | decided in `JudgeFlow` BEFORE the router; `ActionBypass` | the OS route table (the packet never reaches the TUN) | kernel | `CURRENT_CONFIRMED` (source), **NOT_VERIFIED** on device (needs root) |
| TUN Flow / `tun.Port` | `ActionFlow` + `Port` | a `Port` implementation | `Port` writes the packet | ICMP only in production (`direct.PreMatchFlow` answers `PreMatchFlow` only for `NetworkICMP`), `CURRENT_CONFIRMED` |
| TUN TCP splice | decided after handshake, no verdict involved | the kernel, after `GoConn.Splice` accepts | `splice`/`sendfile` between two descriptors | `CURRENT_CONFIRMED` (source + tests); kernel movement after acceptance is unobservable in-process |
| TUN UDP splice | decided after the first datagram | `UDPNatConn.Splice` + `Port` | kernel `sendmsg`/splice with NAT | `HISTORICAL_EVIDENCE` + tests |
| TUN buffered copy | the fallback when splice is refused | userspace `connectionCopy` | userspace buffers | `CURRENT_CONFIRMED` (tests) |

The two rows that are easy to conflate, and are not the same thing:

- **L0** keeps packets out of the TUN by installing OS routes. It is not a verdict outcome; the
  `ActionBypass` it returns is what makes the TUN stack ignore the packet, which in ordinary TUN is
  the same as `ActionAccept` for the *data plane* but happens to arrive before the router. What
  carries the traffic is the route table, not sing-box.
- **Linux nfqueue** really does hand the packet back to the kernel routing path. A verdict-based
  bypass in a TUN stack would instead have to write the packet back to the TUN device, where the
  same routing table that sent it there would send it back - the loop hazard
  `native-bypass-trace.md` records. That is why the TUN case is not implemented and why the
  nfqueue case does not generalise to it.

### 3.2 The ordinary-TUN call chain, in the order the code executes it

```text
device
  -> sing-tun ForwardStage.Dispatch                     (flow table lookup; per-packet)
  -> ForwardStage.judgeAndInstall                       (first packet of a flow only)
       -> Inbound.JudgeFlow                             protocol/tun/inbound.go:1027
           1. canonicalise v4-mapped destination         :1046
           2. configured DNS address                     :1049
           3. dns_hijack by port 53                      :1061
           4. FakeIP guard                               :1088
           5. route_address_set / exclude set  -> ActionBypass, router not consulted   :1089-1100
           6. adapter.JudgeFlow -> Router.PreMatch
       -> (if ActionBypass && Port != nil) ActionFlow    flow_dispatch.go:391
       -> switch:
            ActionFlow + Port  -> create flow, forward into the Port, consumed=true
            ActionReject/Drop  -> consumed
            default (Accept, Bypass)-> installAccept, consumed=false   flow_dispatch.go:449-451
  -> userspace stack terminates the connection (GoConn / UDP NAT / gVisor / system NAT listener)
  -> Inbound.NewConnectionEx / NewPacketConnectionEx
  -> Router.RouteConnectionEx / RoutePacketConnectionEx  (router runs again from scratch)
  -> outbound dial (direct / proxy)
  -> ConnectionManager.NewConnection                     route/conn.go:346
       -> route/conn.go:394  spliceConnection attempt (TCP; skipped only for TLS rewrite)
       -> route/splice.go:145 accept -> return before the copy loops
       -> otherwise the buffered copy loops               route/conn.go:451-452
```

The two facts this chain establishes:

1. `ActionBypass` and `ActionAccept` share the `default` arm at `flow_dispatch.go:449-451`; there is
   no branch that writes the packet back to the platform. The negative controls that pin this are
   listed in §5.
2. The router runs again for the connection itself. The pre-match verdict is discarded - it was
   never an ownership decision on this path. That is why the eligibility decision's only cost is its
   own evaluation.

### 3.3 Where each claimed "fast" mechanism actually lives

| Mechanism | Gate | Owner after the gate |
| --- | --- | --- |
| Direct Fast Path (`ActionBypass`) | router eligibility | same userspace connection in TUN; the kernel in nfqueue only |
| Splice (TCP) | `GoConn` source + kernel-socket target + no TLS rewrite | kernel |
| Splice (UDP) | `UDPNatConn` source + `tun.SpliceSocket` target | kernel |
| `sendfile`/`copyDirect` | both ends syscall-capable | kernel, for proxied flows too |
| Traffic class scheduler | `traffic_class` policy | userspace pacing, upload only |
| L0 | `route_address_set` | OS route table |

Only the middle three move bytes **outside this process**, and none of them is gated by `fast_path`.

---

## 4. What `fast_path` actually does: the policy layer is inert

### 4.1 The structural argument (`CURRENT_CONFIRMED`)

The production tree contains exactly one outbound that implements `adapter.BypassableOutbound`:

```text
protocol/direct/outbound.go:440   func (h *Outbound) CanBypass(...)
```

and exactly two groups that declare `adapter.FastPathAuthorizedGroup` (both report
`FastPathTransparent() == true`):

```text
protocol/group/selector.go:46
protocol/group/urltest.go:50
```

Given that, `resolveFastPathRequest` (`route/fast_path.go:158`) cannot change a verdict:

1. **Leaf is not `direct`** -> `:164` returns false before any policy is read. The policy map is
   never consulted.
2. **Leaf is `direct` behind only transparent groups** -> `directFastPathLeafIsIntrinsic` (`:168`,
   `:63`) returns true without reading a policy. This is deliberate: commit `afd5b5220c8` made the
   fast path an intrinsic property of `direct` so that wrapping it in a selector for grouping does
   not silently lose it.
3. **Leaf is `direct` behind an opaque group** -> the authorisation may or may not be granted, but
   `canFastBypass`'s chain gate (`route/route.go:921-925`) refuses because the opaque group may own
   the dial/retry/accounting. The verdict is `BypassRefusedOutboundChain` either way.
4. **A bare `direct` leaf** -> the chain gate does not apply (`len(chain) == 1`); the policy has no
   input at all.

The one shape where the tri-state field can change the authorisation value is a chain with a
transparent prefix, an opaque group and a direct leaf - and there the verdict is already refused by
the chain gate. So the field changes neither the decision nor the diagnostic on any shipped
configuration. The only code that can observe it is a test double with a bypassable non-`direct`
leaf (`bypassableNonDirectOutbound`), which has no production counterpart.

This is now pinned by tests (`route/fast_path_production_equivalence_test.go`):

- `TestFastPathPoliciesCannotChangeAnyProductionVerdict` - every production chain shape x
  unset/true/false x AI/ordinary tag x TCP/UDP -> the same `PreMatchAction`, and the shape's own
  action.
- `TestFastPathPoliciesCannotChangeAnyProductionBypassVerdict` - the same at `BypassVerdict` level,
  including the refusal reason.
- `TestDirectIntrinsicAuthorisationIsPolicyIndependent` - on every transparent-direct shape the grant
  is intrinsic for every policy; withholding it refuses the flow, so the grant is load-bearing and
  comes from the shape.
- `TestExplicitFastPathFalseDoesNotSuppressTheDirectChainToday` - the one conflict with the field's
  own documentation, pinned in the direction the shipped code implements.
- `TestDirectIsTheOnlyProductionBypassableOutbound` /
  `TestFastPathTransparentGroupsAreTheOnlyPolicyCarriers` - source scans that fail when a second
  bypassable leaf or a new declaring group appears, because that is the moment this whole section
  stops being true.

### 4.2 Behaviour table

Rows are the shapes a user can actually configure; columns are the three states of the field.
"bypass" means the router returns `PreMatchBypass` (which ordinary TUN then treats as accept).

| Chain | `unset` | `true` | `false` | Difference |
| --- | --- | --- | --- | --- |
| `direct` (single leaf) | bypass | bypass | bypass | none |
| `selector -> direct` | bypass | bypass | bypass | none |
| `urltest -> direct` | bypass | bypass | bypass | none |
| `selector -> urltest -> direct` | bypass | bypass | bypass | none |
| `selector -> loadbalance -> direct` | refused (chain) | refused (chain) | refused (chain) | none |
| `loadbalance -> direct` | refused (chain) | configuration rejected: `fast_path` cannot be placed on `loadbalance` | configuration rejected | none |
| any `-> proxy` leaf | refused (not bypassable) | refused (same) | refused (same) | none |
| same `direct` leaf reached through two different groups | per-flow, each chain resolves its own value | same | same | none in outcome |

The last row is the one piece of the design that **is** implemented and tested and still matters for
the future: the authorisation is resolved per flow from the complete chain
(`route/route.go:977-984`), never cached on the leaf, so one residential/direct leaf can be reached
through an AI group and a plain group and the two answers stay independent
(`TestFastPathPolicyFollowsTheGroupNotTheLeaf`, `TestFastPathPolicyIsNotCachedOnTheLeafTag`).

### 4.3 The conflict the brief asked about

`option/fast_path.go` documents the tri-state as "unset = automatic, true = authorise, false =
refuse, even if the tag looks like AI". `directFastPathLeafIsIntrinsic` is evaluated **before** the
policy scan, so on `selector -> direct` an explicit `false` is found by the scan
(`resolveFastPathPolicy` returns "decided, false") and then overruled by the intrinsic rule.

That is the shipped contract, deliberately tested since `afd5b5220c8`, and this round did **not**
change it. The consequence, stated plainly:

- `false` cannot suppress the one bypass leaf the product has.
- On Linux `auto_redirect` - the only mode where the verdict has a real data-plane effect - a user
  who writes `false` on a `selector -> direct` group still gets the native bypass. That is the one
  place where "the name of the setting and its behaviour" can be read as pointing in opposite
  directions.

The options, with the migration cost of each, are in §8. The recommendation is to let an explicit
`false` win over the intrinsic grant and keep `unset`/`true` exactly as they are, because that is
what the field's documentation already promises; it was not applied unattended because it changes the
meaning of an existing published configuration key, which is a product decision rather than a
performance fix.

---

## 5. Evidence: experiments, negative controls, and what they do not prove

### 5.1 Negative controls (`CURRENT_CONFIRMED`, re-run this round)

| Control | Test | What it shows |
| --- | --- | --- |
| `ActionBypass` vs `ActionAccept` at the shared dispatcher | `protocol/tun/native_bypass_dispatcher_test.go::TestDispatcherConsumesOnlyAFlow`, `TestDispatcherTreatsBypassAndAcceptIdentically` | identical consumption, judge count, write-back and tracker creation |
| `ActionBypass` vs `ActionAccept` through the real go stack | `protocol/tun/native_bypass_trace_test.go::TestBypassReachesTheSameDataPathAsAccept` | the application's SYN is never handed back; only stack-generated replies are written |
| `fast_path` unset/true/false through the real router, real stack, real peer, real bytes | `e2e/tun_fast_path_dataplane_test.go::TestFastPathVerdictDoesNotChangeTheTUNDataPath` | all three produce `ActionBypass`; the handler is still delegated to; the echo peer still receives bytes; the socks outbound is never reached |
| verdict reversal (`ActionBypass` -> `ActionAccept`) | same test, `tracker-attached` subtest | attaching a tracker changes the verdict and nothing else: same bytes, same handler delegation, same outbound chain |
| UDP verdict | same file, `TestBypassVerdictStillDeliversUDP` | a bypassed UDP datagram comes back through the userspace NAT, not from the platform |
| splice refused -> copy still delivers both directions | `route/tcp_splice_diagnostics_test.go::TestTCPSpliceDiagnostics_FallbackKeepsCountersAndOwnership` | the fallback keeps byte integrity and releases the connection |
| the tri-state cannot change a verdict | `route/fast_path_production_equivalence_test.go` (five tests, §4.1) | the policy layer is inert on every production shape |
| the eligibility predicate allocates nothing | `route/direct_offload_bench_test.go::TestBypassDecisionUsesNoAllocations` | 0 allocs/op is a test, not a benchmark reading |

The two e2e rows and the dispatcher row divide the chain explicitly: the new fixture writes packets
straight into the stack, so it covers the Handler seam and not `ForwardStage.Dispatch`; the
dispatcher test covers the dispatcher's treatment of the verdict; the stack trace covers the stack's.
No one of them claims another's layer.

### 5.2 Costs, measured on this machine (`CURRENT_CONFIRMED`, Windows/amd64)

`go test -run '^$' -bench ... -benchmem -count=5 -benchtime=300ms ./route`:

| Benchmark | Median | Allocs |
| --- | --- | --- |
| `BenchmarkBypassDecision` (all conditions pass) | 83.7 ns/op | 0 |
| `BenchmarkBypassDecisionRefusedEarly` | 3.8 ns/op | 0 |
| `BenchmarkBypassDecisionRefusedLate` | 5.3 ns/op | 0 |
| `BenchmarkPreMatchDirectFlow/{eligible,ordinary}` | 80-85 ns/op (indistinguishable) | 0 |
| `BenchmarkOutboundBypassProfile` | 66 ns/op | 0 |
| `BenchmarkFlowSetupBypassed` | 217 ns/op | 0 |
| `BenchmarkFlowSetupUserspace` | 840 ns/op | 2 allocs, 104 B |

Two readings matter and one does not:

- The eligibility decision costs **~84 ns and zero allocations per flow** on this machine; the
  refused paths are 4-5 ns. The earlier Apple M1 figures (14-41 ns) were `HISTORICAL_EVIDENCE` and
  are directionally consistent; the absolute values are platform-specific.
- Gating the decision on "the caller cannot honour a bypass" would save at most those ~84 ns for
  eligible flows and would break Linux `auto_redirect`, which is the one caller that can honour it.
  The `BenchmarkPreMatchDirectFlow` pair shows the computation and its refusal cost the same thing,
  so there is no order-of-magnitude saving to collect. **No gate was added.**
- `FlowSetupBypassed` vs `FlowSetupUserspace` is a **model**, not the TUN path: the second row is
  the cost of the connection the userspace route creates, and in TUN mode an eligible flow still
  pays it (the verdict is discarded). It must not be quoted as a TUN throughput gain.

### 5.3 What has NOT been verified

| Item | Status | Why |
| --- | --- | --- |
| Linux `auto_redirect` nfqueue bypass on a real kernel | **NOT_VERIFIED** | No Linux host with CAP_NET_ADMIN here; source inspection only (`nfqueue_linux.go:284`) |
| L0 route installation and exclusion | **NOT_VERIFIED** | Needs root and a real TUN device |
| Kernel actually moving bytes after an accepted splice | **NOT_VERIFIED** | Not observable in-process; the diagnostics record `Accepted`, not kernel activity, and say so (`TestSpliceSnapshot_SummaryDoesNotClaimKernelSpliceSuccess`) |
| Real NIC throughput / CPU per GiB / battery for splice vs copy | **NOT_VERIFIED** | No device under test; the loopback benchmark is dominated by loopback and cannot be extrapolated |
| macOS / iOS / Android / Windows Wintun device behaviour | **NOT_VERIFIED** | This machine can only drive the in-memory TUN and the Windows loopback peer |
| `ActionBypass` in the Android platform auto-redirect implementation | **NEEDS_REVALIDATION** | The platform handler is outside this repository's test surface |

### 5.4 Gates run in this round (local, `CURRENT_CONFIRMED`)

| Gate | Result |
| --- | --- |
| `go test -count=1 -tags <others> ./route ./protocol/tun ./option ./e2e` | PASS (`route` 441 s, `protocol/tun` 3.6 s, `option` 0.06 s, `e2e` 22 s) |
| `go test -race -count=3` on the fast-path/boundary test set (`./route`) | PASS |
| `go test -race -count=3` on the verdict/boundary test set (`./protocol/tun`) | PASS |
| `go test -race -count=2` on the e2e TUN set | PASS |
| `go test -race -count=1 ./route/... ./protocol/tun/... ./option/...` | `route`, `protocol/tun`, `option` PASS; `route/rule` FAILED **once** on `TestTheUpdaterDoesNotOutliveItsClose` (expected 3 fetches, saw 4). The same test passes 10/10 when run alone under `-race`, it is in an untouched package, and the mechanism is an HTTP fetch that was already in flight when `Close` returned and completed before the assertion read the counter. Recorded as a pre-existing, load-sensitive test flake (`OUT_OF_SCOPE_DISCOVERY`); it is not caused by, and does not touch, anything in this review. |
| `go test -count=10` on the fast-path/policy set | PASS |
| `go build -tags <others> ./...` | PASS |
| `go test -run '^$' ./e2e ./route ./protocol/tun` (untagged test binaries, the CI compile gate) | PASS |
| `gofmt -l` on every changed file, `go vet` on the changed packages | clean |
| GitHub Actions `Verify` on the first pushed SHA `91f6c8fdbeb` (it contains every code change; the commits after it are documentation only) | PASS in 18m40s: gofmt, untagged and tagged builds, build matrix, both race steps. `Android Core ARM64` and `Mobile contract` also passed on that SHA. |

`-race` required a mingw-w64 gcc (`CGO_ENABLED=1 CC=...gcc`); the default `go env CGO_ENABLED` on this
host is `0`, so an unqualified `go test -race` fails with "requires cgo". That is an environment
property, not a repository one, and the CI runner has its own toolchain.

---

## 6. D0 decision

| Path | Platform / mode | Theoretical gain | Currently actually executed | A/B performance gain | Safe fallback | Decision |
| --- | --- | --- | --- | --- | --- | --- |
| System proxy lightweighting | all | none (it does not use the TUN stack) | unchanged | not measured this round (not the target) | n/a | keep, out of scope |
| L0 route exclusion | all, with route sets | zero in-process cost for excluded destinations | OS routes; `ActionBypass` before the router | NOT_VERIFIED on device | route removal on close | keep as a separate mechanism; never counted as a TUN data-plane win |
| Linux nfqueue L1 | Linux, `auto_redirect` | removes the flow from userspace | `NfRepeat` + output mark | NOT_VERIFIED on device | `NfAccept` when the queue is not enabled | keep; platform-specific |
| Plain TUN `ActionBypass` | every platform | none | accept entry; userspace connection | **0%** (dispatcher + stack traces) | n/a (same path as accept) | `NONE_DEMONSTRATED` - do not describe as acceleration |
| TUN Flow / `Port` | ICMP through `direct` today | kernel/Port-carried packets | real for ICMP only | not measured | normal path | keep; do not cite for TCP/UDP |
| TUN TCP splice | all TUN stacks | two syscalls per chunk instead of a copy | attempted automatically for every non-TLS-rewritten TCP flow | loopback benchmark is inconclusive by construction | buffered copy | keep as the default; the real automatic optimisation |
| TUN UDP splice | all TUN stacks | same | attempted automatically per session | not measured this round | userspace UDP NAT | keep |
| TUN buffered copy | all | none | the fallback | reference | n/a | keep |
| Group `fast_path` field | all | none today | changes no verdict on any production shape | **0%** | n/a | keep for compatibility and future bypassable leaves; document as inert (`this document`, §4) |

**D0_VALUE = NO_DISTINCT_FAST_PATH.**

Consequences applied in this round:

- No new auto/manual fast-path scheduler, no gate, no redefinition of `fast_path` semantics.
- The control-plane claim is corrected in code comments, tests and this document: a bypass verdict
  is an authorisation, not an acceleration, in ordinary TUN.
- The tests that would notice a change in that premise are now explicit (§4.1), so a future
  bypassable outbound cannot silently invalidate this review.

What the user's design intentions mean against the evidence:

1. **System proxy stays light** - unchanged; it is refused by the first eligibility check and never
   enters a TUN-specific path. `CURRENT_CONFIRMED`.
2. **TUN automatic mode picks the best executable path** - it already does for the one mechanism
   that matters: splice is attempted for every flow, not gated by any group. There is no
   additional automatic fast path to turn on. `CURRENT_CONFIRMED`.
3. **Manual group priority expresses intent** - the layer exists, is per-flow, follows the group
   and not the leaf, and is fully tested; today its value is compatibility and forward-compatibility,
   not acceleration. `CURRENT_CONFIRMED` (by the equivalence tests).
4. **Safe fallback** - a refused verdict returns to the normal path at the same place it always
   did; a refused splice falls back to the copy loops; a refused port creates the normal
   connection. No fallback was added or removed. `CURRENT_CONFIRMED` for the paths tested here.

---

## 7. What this round changed

**No production behaviour changed.** The diff is verification and documentation.

| Change | Why |
| --- | --- |
| `protocol/tun/dns_hijack_consumer_contract_test.go`: `singTunModuleDir` now resolves the module from `go list -m` (the `Replace.Dir`) instead of reading the `require` line and `$HOME/go/pkg/mod` | The old helper asserted properties of the module the build does **not** use, and would `t.Skip` when the cache lived anywhere else. A skipped tripwire reads as a pass. `TestPinnedSourceIsTheReplacedFork` now pins the fix. |
| `route/fast_path_production_equivalence_test.go` (new) | Pins §4 and fails the moment a second bypassable leaf or a precedence change makes the field observable. |
| `e2e/tun_fast_path_dataplane_test.go` (new) | Carries the verdict through the real router, the real Go stack and real bytes on both TCP and UDP; includes the verdict-reversal control. |
| `e2e/harness_test.go`: `startChainUntracked` and the tracker flag | The default harness attaches a `flowRecorder` to every box, which makes `canFastBypass` refuse every flow with `BypassRefusedTracker`. Without this, an e2e test cannot reach a bypass verdict at all. |
| `e2e/tun_chain_test.go`: a pluggable `judgeFlow` and a delegation counter | Lets a test drive the real router's `PreMatch` from packets, and observe that the flow reached userspace. |
| `docs/fork/direct-offload.md` | Corrected the stale "re-verified at" pin; added the current review pointer and the inert-field finding. |
| `docs/fork/native-bypass-trace.md` | Added the current pin, the product-level test, and the harness limitation note. |
| This document | The D0 review the brief requires. |

Deliberately **not** done, with reasons:

- **No gate on the eligibility decision.** It saves at most ~84 ns per eligible flow and cannot be
  keyed on the inbound type without breaking the one transport that honours the verdict. Measured,
  not assumed.
- **No redefinition of `fast_path: false`.** It is a product decision; see §8.
- **No rewrite of the TUN data plane to make `ActionBypass` real.** The mechanism would either loop
  (the routing table sends the packet back into the TUN) or require per-destination route exclusion,
  which is L0 by another name. Recorded in `native-bypass-trace.md` and unchanged.
- **No deletion of the field.** It decodes, validates, round-trips and is ready for a bypassable
  leaf that is not `direct`; deleting a public JSON key would break configurations for no benefit.

---

## 8. Recommendation on `fast_path: false` (not applied)

The conflict in §4.3 has two defensible resolutions. Both are compatible with existing
configurations at the JSON level; only the behaviour of an explicit `false` on a direct chain
changes.

**Option A (recommended): an explicit `false` wins over the intrinsic grant.**

Precedence becomes: explicit policy anywhere in the chain, outermost first -> then intrinsic
direct -> then the automatic tag matcher. `unset` and `true` behave exactly as today; only a group
that explicitly wrote `false` stops authorising the bypass attempt.

- Migration cost: a configuration that writes `false` on a `selector -> direct` group will, after the
  change, keep those flows in userspace on Linux `auto_redirect` instead of bypassing them silently.
  That is the behaviour the field's documentation already promises; the visible effect is a slower,
  fully tracked path for exactly the flows the operator asked to keep off the native path. No JSON
  changes, no new key.
- Test cost: `fast_path_direct_inheritance_test.go::TestDirectFastPathSelectorExplicitFalseDoesNotDisableDirect`
  and `TestExplicitFastPathFalseDoesNotSuppressTheDirectChainToday` change expected value; every
  other test in `route/fast_path_*_test.go` stays.
- Risk: the bypass is irreversible once taken, so "false keeps it off" is the safe direction; the
  only cost is performance on a configuration that asked for less performance.

**Option B: document the intrinsic rule as the contract and keep `false` inert.**

- Migration cost: none.
- Risk: a user-facing key whose documented value cannot occur is exactly the "the name and the
  behaviour point in opposite directions" failure the brief forbids. It is defensible only while
  `direct` is the sole bypassable leaf - and when a second one appears, `false` starts working
  anyway, which is a silent semantic change at a distance.

Option A was not applied here because it changes an existing published key's meaning, and the brief
explicitly reserves that decision. The change is small: `resolveFastPathRequest` reorders two
blocks, and the tests above pin the new contract.

---

## 9. Reproduce

```powershell
$env:GOROOT='C:\Deepseek\_gotool\go'; $env:GOPATH='C:\Deepseek\_gotool\gopath'
$env:PATH="$env:GOROOT\bin;$env:PATH"; $env:GOTOOLCHAIN='local'
cd C:\Deepseek\integration-release-next
$tags = (Get-Content .\release\DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()

go list -m -json github.com/sagernet/sing-tun        # Replace.Dir must be the fork
go test -count=1 -tags $tags ./route ./protocol/tun ./option ./e2e
go test -count=1 -tags $tags ./route -run 'TestFastPathPoliciesCannotChange|TestDirectIntrinsic|TestExplicitFastPathFalse|TestDirectIsTheOnly|TestFastPathTransparentGroups|TestDispatcherConsumes|TestBypassReachesTheSameDataPath|TestTCPSpliceDiagnostics_Fallback'
go test -count=1 -tags $tags ./e2e -run 'TestFastPathVerdictDoesNotChangeTheTUNDataPath|TestBypassVerdictStillDeliversUDP'
go test -run '^$' -bench 'BenchmarkBypassDecision|BenchmarkPreMatchDirectFlow|BenchmarkFlowSetup' -benchmem -count=5 -tags $tags ./route
```

The benchmark numbers in §5.2 are the raw output of the last command on the machine described in the
header; they are not device measurements.
