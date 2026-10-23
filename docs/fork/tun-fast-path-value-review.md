# TUN fast path: value review, real path map, and the D0 verdict

Status: **D0_VALUE = NO_DISTINCT_FAST_PATH** for the group `fast_path` feature. The only real
TUN data-plane difference this fork has is the post-handshake splice/`sendfile` handover - it is
attempted automatically and is not what `fast_path` controls, but its ACCEPTED half is a device
capability and is NOT_VERIFIED in this environment (§3.1). The field has exactly one observable
control-plane effect in production - an explicit `false` withholds the bypass attempt on the one
shape that can take it - and this round applied that precedence (see §4.3), because without it the
documented tri-state could not switch the attempt off anywhere. No ordinary-TUN byte moves a
different way because of the field, in either direction. The code changes are that precedence fix,
one verification fix and tests; they are listed in [§7](#7-what-this-round-changed).

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

The group `fast_path` field does not change that verdict's data-plane consequence either - but it is
no longer wholly inert: an explicit `false` withholds the attempt on the one shape that can take it,
and `unset`/`true` are equivalent. See §4. What is real, and what the brief asks us to look for, is
the **post-handshake splice handover**, which is attempted for every TUN connection that reaches
`ConnectionManager.NewConnection` (TCP, TLS-rewrite flows excepted) and `NewPacketConnection` (UDP) -
no group, no field, no opt-in. That is the optimisation that already applies to all eligible TUN
traffic, subject to the platform accepting the handover (a device capability; §3.1 records that this
environment's in-memory IO cannot).

---

## 3. The real path map

### 3.1 Control verdict / actual owner / who moves the bytes

| Path | Control verdict | Actual owner of the flow | Bytes move by | Label |
| --- | --- | --- | --- | --- |
| System proxy (`mixed`/SOCKS/HTTP -> router -> outbound -> socket) | `PreMatchBypass` is refused at `canFastBypass`'s first check (`InboundType != tun`) | userspace connection manager | userspace copy (the inbound stream is not a `*tun.GoConn`, so splice is refused) | `CURRENT_CONFIRMED` (code), unchanged this round |
| Ordinary TUN, default `go` stack | `JudgeFlow` -> router -> `ActionAccept` or `ActionBypass` | `*tun.GoConn` / `tun.UDPNatConn` in the go stack | userspace copy, or the kernel if a splice handover is accepted (accepted half NOT_VERIFIED here; see below) | `CURRENT_CONFIRMED` |
| Ordinary TUN, `gvisor` stack | same router; `TCPForwarder.Forward` honours only Reject/Drop | gVisor netstack connection | userspace copy / splice attempt | `HISTORICAL_EVIDENCE` (code path unchanged) |
| Ordinary TUN, `system` stack | same router; not consumed -> userspace | local NAT listener | userspace copy / splice attempt | `HISTORICAL_EVIDENCE` |
| Linux `auto_redirect` (nfqueue) | same router, `JudgeFlow` called by the nfqueue handler | the kernel, after `NfRepeat` + output mark | kernel routing | `CURRENT_CONFIRMED` (source), **NOT_VERIFIED** on device |
| L0 route sets (`route_address_set` / `route_exclude_address_set`) | decided in `JudgeFlow` BEFORE the router; `ActionBypass` | the OS route table (the packet never reaches the TUN) | kernel | `CURRENT_CONFIRMED` (source), **NOT_VERIFIED** on device (needs root) |
| TUN Flow / `tun.Port` | `ActionFlow` + `Port` | a `Port` implementation | `Port` writes the packet | ICMP only in production (`direct.PreMatchFlow` answers `PreMatchFlow` only for `NetworkICMP`), `CURRENT_CONFIRMED` |
| TUN TCP splice | decided after handshake, no verdict involved | the kernel, after `GoConn.Splice` accepts | `splice`/`sendfile` between two descriptors | attempt `CURRENT_CONFIRMED`; accepted path **NOT_VERIFIED** here (see below) |
| TUN UDP splice | decided after the first datagram | `UDPNatConn.Splice` + `Port` | kernel `sendmsg`/splice with NAT | `HISTORICAL_EVIDENCE` + tests |
| TUN buffered copy | the fallback when splice is refused | userspace `connectionCopy` | userspace buffers | `CURRENT_CONFIRMED` (tests) |

The TCP splice row needs one qualification this round measured. The attempt is automatic and every
TUN connection makes it, but the in-memory TUN **cannot** accept it: sing-tun's memory platform IO
reports `supportsSockets() == false`, and `GoConn.Splice` refuses on that first check. The product
level test therefore observes `splice_rejected` for both a direct and a proxied TUN flow, and the
bytes are delivered by the copy loops; a system-proxy flow observes `source_not_go_conn`. The
accepted branch and any kernel byte movement after it need a real device (Linux, Darwin, Windows
Wintun) and are NOT_VERIFIED here.

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

## 4. What `fast_path` actually does: an off switch, not a fast path

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

Given that, `resolveFastPathRequest` (`route/fast_path.go`) resolves as follows:

1. **Leaf is not `direct`** -> returns false before any policy is read. The policy map is never
   consulted; no value of the field can change this.
2. **An explicit policy anywhere in the chain** -> the first one, outermost to inner, is the chain's
   decision (`resolveFastPathPolicy`). On a transparent-direct chain an explicit `false` therefore
   withholds the attempt, and an explicit `true` takes it.
3. **No policy anywhere, leaf is `direct` behind only transparent groups** ->
   `directFastPathLeafIsIntrinsic` returns true. This is deliberate: commit `afd5b5220c8` made the
   fast path the default for `direct` so that wrapping it in a selector for grouping does not
   silently lose it. Silence is not a refusal.
4. **Leaf is `direct` behind an opaque group** -> the intrinsic rule does not apply, and even an
   outer explicit `true` cannot carry the flow: `canFastBypass`'s chain gate (`route/route.go:921-925`)
   refuses because the opaque group may own the dial/retry/accounting.
5. **A bare `direct` leaf** -> the chain gate does not apply (`len(chain) == 1`); no group exists
   that could carry the field.

So the field has exactly one observable control-plane effect in production: **`false` withholds the
bypass attempt on the transparent-direct shapes; `unset` and `true` are indistinguishable.** It can
never grant a bypass the shape forbids, and on ordinary TUN neither direction changes which bytes
move where (see `protocol/tun` and `e2e`). The effect becomes data-plane-visible only where the
transport honours the verdict, which today is Linux `auto_redirect`.

This is pinned by tests (`route/fast_path_production_equivalence_test.go`):

- `TestFastPathUnsetAndTrueAreIndistinguishable` - every production chain shape x unset/true x
  AI/ordinary tag x TCP/UDP -> the same action and the same full `BypassVerdict`, including the
  refusal reason.
- `TestFastPathFalseOnlySuppressesTheIntrinsicShape` - every combination containing an explicit
  false is resolved independently from the outermost explicit value: it withholds only the
  transparent-direct shapes, and everywhere else it cannot change even the refusal reason.
- `TestDirectIntrinsicAuthorisationRequiresSilence` - silence and true keep the grant, false
  withholds it, and withholding it refuses the flow, so the grant is load-bearing.
- `TestExplicitFastPathFalseSuppressesTheDirectChain` - the focused regression: the same chain takes
  the bypass with no policy and falls through with `false`.
- `TestExplicitFastPathFalseNeedsAGroup` - a bare direct leaf has no group to carry the field, and
  the chain gate does not apply to it.
- `TestDirectIsTheOnlyProductionBypassableOutbound` /
  `TestFastPathTransparentGroupsAreTheOnlyPolicyCarriers` - source scans that fail when a second
  bypassable leaf or a new declaring group appears, because that is the moment this whole section
  stops being true.

### 4.2 Behaviour table

Rows are the shapes a user can actually configure; columns are the three states of the field.
"bypass" means the router returns `PreMatchBypass` (which ordinary TUN then treats as accept).

| Chain | `unset` | `true` | `false` | Difference |
| --- | --- | --- | --- | --- |
| `direct` (single leaf) | bypass | bypass | bypass (no group to carry the field; box.go refuses the key on a leaf) | none |
| `selector -> direct` | bypass | bypass | **refused (chain)** | `false` withholds the attempt |
| `urltest -> direct` | bypass | bypass | **refused (chain)** | same |
| `selector -> urltest -> direct` | bypass | bypass | **refused (chain)** | same |
| `selector -> loadbalance -> direct` | refused (chain) | refused (chain) | refused (chain) | none |
| `loadbalance -> direct` | refused (chain) | configuration rejected: `fast_path` cannot be placed on `loadbalance` | configuration rejected | none |
| any `-> proxy` leaf | refused (not bypassable) | refused (same) | refused (same) | none |
| same `direct` leaf reached through two different groups | per-flow, each chain resolves its own value | same | same | none in outcome |

The per-flow row is the piece of the design that keeps this honest: the authorisation is resolved
from the complete chain (`route/route.go:977-984`), never cached on the leaf, so one residential or
direct leaf can be reached through an AI group and a plain group and the two answers stay
independent - including one group writing `false` and the other saying nothing
(`TestFastPathPolicyFollowsTheGroupNotTheLeaf`, `TestFastPathPolicyIsNotCachedOnTheLeafTag`).

### 4.3 The conflict the brief asked about, and the decision

`option/fast_path.go` documents the tri-state as "unset = automatic, true = authorise, false =
refuse". Before this round `directFastPathLeafIsIntrinsic` was evaluated **before** the policy scan,
so on `selector -> direct` an explicit `false` was found by the scan
(`resolveFastPathPolicy` returned "decided, false") and then overruled by the intrinsic rule. The
documented value could not occur anywhere: `false` could not suppress the one bypass leaf the
product has, and on Linux `auto_redirect` - the only mode where the verdict has a real data-plane
effect - a user who wrote `false` still got the native bypass.

**The decision applied in this round: an explicit `false` wins over the intrinsic grant.** The policy
scan now runs first, and the intrinsic rule applies only when no element of the chain states a policy.
`unset` and `true` behave exactly as before; the regression the intrinsic rule fixed (a silent
selector losing the fast path) still passes.

- Compatibility: the JSON shape is unchanged and no key moves. The only configurations whose
  behaviour changes are those that explicitly wrote `false` on a transparent-direct chain - and they
  now get what the key always said. On ordinary TUN the change is invisible in bytes; on Linux
  `auto_redirect` those flows stay in userspace, slower and fully observed, which is the safe
  direction for a decision the operator made.
- Tests updated: `TestDirectFastPathSelectorExplicitFalseDoesNotDisableDirect` became
  `TestDirectFastPathSelectorExplicitFalseSuppressesTheAttempt`; the nested-precedence test now
  asserts outermost-first including the intrinsic shape; the production-equivalence tests assert
  "unset == true" and "false only suppresses the intrinsic shape"; the e2e `false` case now expects
  `ActionAccept` with identical bytes.
- What `false` still cannot do: disable `direct` itself. The flow keeps its direct outbound and
  reaches the same destination; only the native-bypass attempt is withheld.

---

## 5. Evidence: experiments, negative controls, and what they do not prove

### 5.1 Negative controls (`CURRENT_CONFIRMED`, re-run this round)

| Control | Test | What it shows |
| --- | --- | --- |
| `ActionBypass` vs `ActionAccept` at the shared dispatcher | `protocol/tun/native_bypass_dispatcher_test.go::TestDispatcherConsumesOnlyAFlow`, `TestDispatcherTreatsBypassAndAcceptIdentically` | identical consumption, judge count, write-back and tracker creation |
| `ActionBypass` vs `ActionAccept` through the real go stack | `protocol/tun/native_bypass_trace_test.go::TestBypassReachesTheSameDataPathAsAccept` | the application's SYN is never handed back; only stack-generated replies are written |
| `fast_path` unset/true through the real router, real stack, real peer, real bytes | `e2e/tun_fast_path_dataplane_test.go::TestFastPathVerdictDoesNotChangeTheTUNDataPath` | both produce `ActionBypass`; the handler is still delegated to; the echo peer still receives bytes; the socks outbound is never reached |
| `fast_path: false` withholds the attempt and changes no bytes | same test, `false` subtest | the verdict flips to `ActionAccept` while bytes, handler delegation and the direct outbound chain stay identical |
| verdict reversal (`ActionBypass` -> `ActionAccept`) | same test, `tracker-attached` subtest | attaching a tracker changes the verdict and nothing else: same bytes, same handler delegation, same outbound chain |
| UDP verdict | same file, `TestBypassVerdictStillDeliversUDP` | a bypassed UDP datagram comes back through the userspace NAT, not from the platform |
| splice refused -> copy still delivers both directions | `route/tcp_splice_diagnostics_test.go::TestTCPSpliceDiagnostics_FallbackKeepsCountersAndOwnership` | the fallback keeps byte integrity and releases the connection |
| the transport decision, read from the live manager | `e2e/tun_splice_dataplane_test.go` | a direct TUN flow reaches the handover and is refused by the memory platform IO (`splice_rejected`), a proxied flow keeps a GoConn source, and a system-proxy flow is refused at the source (`source_not_go_conn`) - all three still deliver |
| exact refusal verdict and ordering | `route/fast_bypass_verdict_matrix_test.go` | every eligibility condition changes one input and reports its own verdict; seven combinations pin which condition wins |
| shared leaf, concurrent policy, selector switch (with `false`) | `route/fast_path_adversarial_test.go` | a refusing group and a passing group sharing one direct leaf never leak into each other, in either order or concurrently; `false` survives a membership switch |
| an explicit `action: bypass` rule | `route/fast_path_adversarial_test.go::TestAnExplicitBypassRuleIgnoresTheFastPathGate` | the explicit action is not the `fast_path` gate: it bypasses even with a tracker attached, by design, while the implicit path still refuses |
| multi-segment payload through a bypassed flow | `e2e/tun_fast_path_dataplane_test.go::TestFastPathMultiSegmentPayloadIsIntact` | an 8 KiB stream arrives byte-identical, acknowledged segment by segment |
| the policy layer's contract | `route/fast_path_production_equivalence_test.go` (seven tests, §4.1) | unset and true are indistinguishable on every production shape; false only suppresses the intrinsic shape; it can never grant |
| the eligibility predicate allocates nothing | `route/direct_offload_bench_test.go::TestBypassDecisionUsesNoAllocations` | 0 allocs/op is a test, not a benchmark reading |

The e2e rows and the dispatcher row divide the chain explicitly: the new fixture writes packets
straight into the stack, so it covers the Handler seam and not `ForwardStage.Dispatch`; the
dispatcher test covers the dispatcher's treatment of the verdict; the stack trace covers the stack's.
No one of them claims another's layer.

### 5.2 Costs, measured on this machine (`CURRENT_CONFIRMED`, Windows/amd64)

`go test -run '^$' -bench ... -benchmem -count=5 -benchtime=300ms ./route`:

| Benchmark | Median | Allocs |
| --- | --- | --- |
| `BenchmarkBypassDecision` (all conditions pass) | 85.5 ns/op | 0 |
| `BenchmarkBypassDecisionRefusedEarly` | 3.7 ns/op | 0 |
| `BenchmarkBypassDecisionRefusedLate` | 5.2 ns/op | 0 |
| `BenchmarkPreMatchDirectFlow/{eligible,ordinary}` | 80-86 ns/op (indistinguishable) | 0 |
| `BenchmarkOutboundBypassProfile` | 66 ns/op | 0 |
| `BenchmarkFlowSetupBypassed` | 218 ns/op | 0 |
| `BenchmarkFlowSetupUserspace` | 939 ns/op | 2 allocs, 104 B |

Two readings matter and one does not:

- The eligibility decision costs **~85 ns and zero allocations per flow** on this machine; the
  refused paths are 4-5 ns. The precedence change added one policy-map lookup before the intrinsic
  probe, which is inside the run-to-run noise (the pre-change median was 83.7 ns). The earlier Apple
  M1 figures (14-41 ns) were `HISTORICAL_EVIDENCE` and are directionally consistent; the absolute
  values are platform-specific.
- Gating the decision on "the caller cannot honour a bypass" would save at most those ~85 ns for
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
| An ACCEPTED TCP handover at all, in this environment | **NOT_VERIFIED** | sing-tun's memory platform IO reports `supportsSockets() == false`, so `GoConn.Splice` refuses at its first check; the product-level test observes `splice_rejected` and the copy fallback. A real device IO (Linux, Darwin, Windows Wintun) reports true |
| Real NIC throughput / CPU per GiB / battery for splice vs copy | **NOT_VERIFIED** | No device under test; the loopback benchmark is dominated by loopback and cannot be extrapolated |
| macOS / iOS / Android / Windows Wintun device behaviour | **NOT_VERIFIED** | This machine can only drive the in-memory TUN and the Windows loopback peer |
| `ActionBypass` in the Android platform auto-redirect implementation | **NEEDS_REVALIDATION** | The platform handler is outside this repository's test surface |

### 5.4 Gates run in this round (local, `CURRENT_CONFIRMED`)

| Gate | Result |
| --- | --- |
| `go test -count=1 -tags <others> ./route ./protocol/tun ./option ./e2e` | PASS (`route` 441.4 s, `protocol/tun` 3.6 s, `option` 0.06 s, `e2e` 22.5 s) |
| `go test -race -count=3` on the fast-path/boundary test set (`./route`) | PASS |
| `go test -race -count=3` on the verdict/boundary test set (`./protocol/tun`) | PASS |
| `go test -race -count=2` on the e2e TUN set | PASS |
| `go test -count=10` on the fast-path/policy set | PASS |
| `go test -count=10` on the new eligibility/order/adversarial route tests | PASS |
| `go test -count=3` on the new e2e controls (verdict, splice, multi-segment) | PASS |
| `go test -race -count=2` on the new route and e2e controls | PASS |
| `go test ./ -run TestFastPath` (box-level configuration) | PASS: refused on a leaf and on loadbalance, accepted on selector/urltest, ordinary routing unchanged |
| `go build -tags <others> ./...` | PASS |
| `go vet` on `./route ./option ./protocol/tun ./e2e` | clean |
| `go test -run '^$' ./e2e ./route ./protocol/tun` (untagged test binaries, the CI compile gate) | PASS |
| `gofmt -l` on every changed file | clean |
| `BenchmarkBypassDecision` after the precedence change | 85.5 ns/op, 0 allocs (one extra empty-map lookup, inside noise) |
| GitHub Actions `Verify` on every pushed SHA of this review | PASS. The push gate runs gofmt, untagged and tagged builds, the build matrix and both race steps; run IDs are in the round report. |

The full `-race` sweep of `./route/... ./protocol/tun/... ./option/...` was run once during this
review (on the tree before the precedence change; the change itself was gated with the targeted
`-race` runs above) and failed **one** unrelated test: `route/rule TestTheUpdaterDoesNotOutliveItsClose`
(expected 3 fetches, saw 4). The same test passes 10/10 when run alone under `-race`, it is in an
untouched package, and the mechanism is an HTTP fetch that was already in flight when `Close`
returned and completed before the assertion read the counter. It is recorded as a pre-existing,
load-sensitive test flake (`OUT_OF_SCOPE_DISCOVERY`); it is not caused by, and does not touch,
anything in this review.

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
| TUN TCP splice | all TUN stacks | two syscalls per chunk instead of a copy | attempted automatically for every non-TLS-rewritten TCP flow; acceptance is a platform-IO capability (device-only, NOT_VERIFIED here) | loopback benchmark is inconclusive by construction | buffered copy | keep as the default; the real automatic optimisation where the platform accepts it |
| TUN UDP splice | all TUN stacks | same | attempted automatically per session | not measured this round | userspace UDP NAT | keep |
| TUN buffered copy | all | none | the fallback | reference | n/a | keep |
| Group `fast_path` field | all | an explicit `false` can withhold the bypass attempt; `unset`/`true` cannot add one | `false` changes the verdict on transparent-direct chains; bytes unchanged in ordinary TUN | **0%** in ordinary TUN; real in Linux redirect mode | n/a (the flow continues on the normal path) | keep; `false` is a real off switch, `unset`/`true` are equivalent |

**D0_VALUE = NO_DISTINCT_FAST_PATH.**

Consequences applied in this round:

- No new auto/manual fast-path scheduler and no eligibility gate.
- The precedence fix: an explicit `false` now withholds the intrinsic attempt, so the documented
  tri-state is true wherever it can be true. `unset`/`true` are unchanged and remain equivalent.
- The control-plane claim is corrected in code comments, tests and this document: a bypass verdict
  is an authorisation, not an acceleration, in ordinary TUN.
- The tests that would notice a change in that premise are explicit (§4.1), so a future bypassable
  leaf cannot silently invalidate this review.

What the user's design intentions mean against the evidence:

1. **System proxy stays light** - unchanged; it is refused by the first eligibility check and never
   enters a TUN-specific path. `CURRENT_CONFIRMED`.
2. **TUN automatic mode picks the best executable path** - it already does for the one mechanism
   that matters: splice is attempted for every flow, not gated by any group, and whether the
   platform accepts the handover is a device capability rather than a policy. There is no
   additional automatic fast path to turn on. `CURRENT_CONFIRMED` for the attempt; accepted half
   NOT_VERIFIED here.
3. **Manual group priority expresses intent** - the layer is per-flow, follows the group and not the
   leaf, and now has a real off switch. There is still no manual setting that makes a flow faster
   than the automatic path, because there is no faster executable path to unlock. `CURRENT_CONFIRMED`.
4. **Safe fallback** - a refused verdict returns to the normal path at the same place it always
   did; a refused splice falls back to the copy loops; a refused port creates the normal
   connection. No fallback was added or removed. `CURRENT_CONFIRMED` for the paths tested here.

---

## 7. What this round changed

One production behaviour changed, narrowly and with tests: the `fast_path: false` precedence (§4.3).
Everything else is verification and documentation.

| Change | Why |
| --- | --- |
| `route/fast_path.go`: the explicit policy scan now precedes the intrinsic direct grant | The published tri-state could not switch the bypass attempt off anywhere. `unset`/`true` keep their behaviour; only an explicit `false` on a transparent-direct chain changes, and it changes to what the key documents. |
| `option/fast_path.go`: the tri-state comment states that precedence | The package documents the field's meaning; it has to agree with the resolver. |
| `protocol/tun/dns_hijack_consumer_contract_test.go`: `singTunModuleDir` now resolves the module from `go list -m` (the `Replace.Dir`) instead of reading the `require` line and `$HOME/go/pkg/mod` | The old helper asserted properties of the module the build does **not** use, and would `t.Skip` when the cache lived anywhere else. A skipped tripwire reads as a pass. `TestPinnedSourceIsTheReplacedFork` now pins the fix. |
| `route/fast_path_production_equivalence_test.go` (new) | Pins §4 and fails the moment a second bypassable leaf or a precedence change alters the contract. |
| `route/fast_bypass_verdict_matrix_test.go` (new) | Every eligibility condition changes one input and asserts its exact `BypassVerdict`; seven rows pin the order of the conditions. |
| `route/fast_path_adversarial_test.go` (new) | Shared leaf across refusing/passing groups (sequential and concurrent), `false` across a selector switch, and the explicit `action: bypass` asymmetry. |
| `e2e/tun_splice_dataplane_test.go` (new) | The transport decision read from the live manager: direct TUN attempt + platform refusal, proxied TUN source classification, system-proxy source refusal. |
| `e2e/tun_fast_path_dataplane_test.go`: multi-segment payload test | 8 KiB through a bypassed flow, byte-identical, acknowledged segment by segment. |
| `e2e/tun_fast_path_dataplane_test.go` (new) | Carries the verdict through the real router, the real Go stack and real bytes on both TCP and UDP; includes the `false` and tracker verdict-reversal controls. |
| `e2e/harness_test.go`: `startChainUntracked` and the tracker flag | The default harness attaches a `flowRecorder` to every box, which makes `canFastBypass` refuse every flow with `BypassRefusedTracker`. Without this, an e2e test cannot reach a bypass verdict at all. |
| `e2e/tun_chain_test.go`: a pluggable `judgeFlow` and a delegation counter | Lets a test drive the real router's `PreMatch` from packets, and observe that the flow reached userspace. |
| `docs/fork/direct-offload.md` | Corrected the stale "re-verified at" pin; added the current review pointer and the field's actual contract. |
| `docs/fork/native-bypass-trace.md` | Added the current pin, the product-level test, and the harness limitation note. |
| This document | The D0 review the brief requires, including the decision above. |

The adversarial acceptance round - the attack matrix, the exact-verdict ordering, the mutation
controls and the splice limitation - is recorded in
[tun-fast-path-adversarial-debug.md](tun-fast-path-adversarial-debug.md).

Deliberately **not** done, with reasons:
- **No gate on the eligibility decision.** It saves at most ~85 ns per eligible flow and cannot be
  keyed on the inbound type without breaking the one transport that honours the verdict. Measured,
  not assumed.
- **No rewrite of the TUN data plane to make `ActionBypass` real.** The mechanism would either loop
  (the routing table sends the packet back into the TUN) or require per-destination route exclusion,
  which is L0 by another name. Recorded in `native-bypass-trace.md` and unchanged.
- **No deletion of the field.** It decodes, validates, round-trips, and now has a working off switch
  plus a forward-compatible grant path for a future bypassable leaf that is not `direct`.

---

## 8. The `fast_path: false` decision, as applied

§4.3 recorded the conflict; this round resolved it. The applied rule is:

```text
explicit policy anywhere in the chain, outermost first
  > intrinsic grant for transparent groups over direct
  > automatic tag detection
```

Why this option and not "document the intrinsic rule and leave `false` inert":

- The field's own documentation promised `false = refuse`. A user-facing key whose documented value
  cannot occur is the failure mode where the name and the behaviour point in opposite directions.
- The bypass is irreversible once taken, so a user asking to keep a flow in userspace is asking for
  the safer path. Honouring that is strictly safer than ignoring it.
- The alternative becomes wrong by itself the moment a second bypassable leaf exists: `false` would
  start working then, a silent semantic change at a distance. Making it work now, with tests, means
  the behaviour is decided rather than discovered.

Compatibility and migration:

- No JSON change; no key moves; official sing-box still does not know the key.
- Only configurations that explicitly wrote `false` on a transparent-direct chain change, and only
  on a transport that honours the verdict (Linux `auto_redirect`). They move from a native bypass to
  the userspace direct path - slower, fully observed, and exactly what they asked for.
- On ordinary TUN the change is invisible in bytes: `false` changes the verdict, and the verdict has
  no data-plane effect there. The e2e test asserts both halves.
- Tests changed with the contract: the old "false does not disable direct" test became
  "false suppresses the attempt"; the nested precedence test now covers outermost-first on the
  intrinsic shape; the equivalence tests assert `unset == true` and "false only suppresses".
- If the decision is ever reversed, the tests fail with the contract named in the failure message,
  not silently.

---

## 9. Reproduce

```powershell
$env:GOROOT='C:\Deepseek\_gotool\go'; $env:GOPATH='C:\Deepseek\_gotool\gopath'
$env:PATH="$env:GOROOT\bin;$env:PATH"; $env:GOTOOLCHAIN='local'
cd C:\Deepseek\integration-release-next
$tags = (Get-Content .\release\DEFAULT_BUILD_TAGS_OTHERS -Raw).Trim()

go list -m -json github.com/sagernet/sing-tun        # Replace.Dir must be the fork
go test -count=1 -tags $tags ./route ./protocol/tun ./option ./e2e
go test -count=1 -tags $tags ./route -run 'TestFastPathUnsetAndTrue|TestFastPathFalseOnlySuppresses|TestDirectIntrinsicAuthorisationRequiresSilence|TestExplicitFastPathFalse|TestDirectFastPathSelectorExplicitFalse|TestDirectFastPathNested|TestDirectIsTheOnly|TestFastPathTransparentGroups|TestDispatcherConsumes|TestBypassReachesTheSameDataPath|TestTCPSpliceDiagnostics_Fallback'
go test -count=1 -tags $tags ./e2e -run 'TestFastPathVerdictDoesNotChangeTheTUNDataPath|TestBypassVerdictStillDeliversUDP'
go test -run '^$' -bench 'BenchmarkBypassDecision|BenchmarkPreMatchDirectFlow|BenchmarkFlowSetup' -benchmem -count=5 -tags $tags ./route
```

The benchmark numbers in §5.2 are the raw output of the last command on the machine described in the
header; they are not device measurements.
