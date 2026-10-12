# Data-path optimization: Direct Offload, traffic class, upload scheduler

**Scope:** `common/dialer/profile.go`, `protocol/direct/outbound.go`, `route/bypass_verdict.go`,
`common/trafficclass`, `common/trafficsched`, `route/dial_governor.go`, `route/traffic_class.go`.

**Provenance:** `common/trafficsched` (30 commits, 18 files) and `common/trafficclass` are
**fork-created**; `traffic_scheduler` and `traffic_class` **do not exist in official sing-box**, so a
configuration using either is not loadable upstream. Direct Offload is a fork extension whose
eligibility rules are *stricter* than upstream's.

**This subsystem is CI-critical:** `verify.yml:310` runs a dedicated `-race` step over
`./common/trafficclass/... ./common/trafficsched/... ./common/dialer/... ./protocol/direct/...`, and
`scripts/ci/verify-upstream-assumptions.sh` carries a tripwire inventory for it
(`docs/UPSTREAM-SYNC.md`).

**Load this module when changing:** `traffic_scheduler.upload_rate`, `traffic_class`, the bypass
decision, TUN `ActionBypass` handling, or any "let the OS route this" optimisation.

---

## Part A — Direct Offload

### A.1 The rule

```text
SEMANTIC EQUIVALENCE, NOT CONFIG EMPTINESS
```

A dial option disqualifies a flow **only if the userspace path would actually apply it to that flow**.
An option the flow never reaches is not a reason to refuse.

This is not academic. The shipped topology declares exactly one direct outbound:

```json
{ "type": "direct", "tag": "direct", "domain_resolver": "local-agh" }
```

Comparing that configuration against an empty one refused **every** flow through it, including the
literal-IP flows that never consult a resolver.

### A.2 Two layers, two questions

| Layer | Question | Owner |
|---|---|---|
| Router | May this **flow** skip the userspace path, given its inbound, sniffed domain, FakeIP state, trackers and rule verdict? | `route/route.go` — `canFastBypass` |
| Dialer profile | Would this **socket configuration** be reproduced by the platform's own connect, for this flow? | `common/dialer/profile.go` — `SocketSemantics` |

Neither re-implements the other. A profile that tried to answer the policy question would guess at
metadata it cannot see; a router that re-read dial options would be a second interpretation of them.

### A.3 The classification, and where it came from

The table was read off `common/dialer/default.go` and `resolve.go` — **where each field is actually
applied**, not off the field names.

| Scope | Options | Why it disqualifies |
|---|---|---|
| Always | `detour`, `bind_interface`, `inet4/6_bind_address`, `bind_address_no_port`, `protect_path`, `routing_mark`, `netns`, `connect_timeout`, `network_strategy`, `network_type`, `fallback_network_type`, `fallback_delay` | A bind, a mark, a namespace, a dial deadline or interface selection — none reproducible by the platform performing the connect |
| TCP only | `tcp_fast_open`, `tcp_multi_path`, `disable_tcp_keep_alive`, `tcp_keep_alive`, `tcp_keep_alive_interval` | Applied to the TCP dialer; a **UDP** flow through the same outbound is still a plain connect |
| UDP only | `reuse_addr`, udp source port, `udp_fragment` | `reuse_addr` reaches `listenConfig`, never `dialer.Control`; `udp_fragment` is a datagram socket option |
| Resolution only | `domain_resolver`, `domain_strategy` | Consulted only when a name is resolved — see the exception |
| Ignored | `udp_fragment_default` | The direct constructor sets it; treating it as an operator statement would refuse every direct outbound in this fork |

**The exception that is easy to get wrong:** a **hard single-family** strategy (`ipv4_only`,
`ipv6_only`) filters the **literal** destination too — the dial path applies it to the original
attempt, not only to resolved candidates. A profile that stopped at "a literal needs no resolver"
would bypass a connection the userspace path would have refused to dial. So the effective strategy is
asked of the **live dialer**, and only a *restriction* counts: `prefer_ipv4` is a preference and
permits everything.

### A.4 Failing closed, twice

1. **At runtime.** A field that is set but absent from the classification table produces
   `BlockerUnclassified`, so the omission costs a missed optimisation rather than a discarded socket
   semantic.
2. **In review.** `TestEveryDialerOptionIsClassified` reflects over the options struct and fails until
   every field is classified, with the scope meanings printed in the failure message. The table may
   also not name a field that no longer exists, so a rename cannot leave a stale entry behind.

### A.5 Which layers exist, and where a bypass actually happens

| Layer | What carries the bytes | Per-byte cost to this process | Honoured where |
|---|---|---|---|
| **L0** | the TUN's own route sets (`route_address_set` / `route_exclude_address_set`) | zero | **everywhere**, by installing OS routes |
| **L1** | the router's semantic bypass — this section | zero **only in redirect mode** | Linux `auto_redirect` (nfqueue) |
| **L2** | userspace copy with a kernel socket pair (splice / `sendfile`) | two syscalls per chunk | every TUN stack |
| **L3** | userspace copy through buffers | a copy plus two syscalls per chunk | every TUN stack |

> **L1 does not bypass in TUN mode.** `tun.ActionBypass` is honoured by the Linux nfqueue handler,
> where it maps to `NfRepeat` with the output mark and the packet leaves by its original path. In every
> TUN stack the pinned sing-tun fork provides, `flow_dispatch.go:398-438` sends `ActionBypass` to its
> `default:` arm → `installAccept` (`:435-437`, `:449`), and all three stacks use that one dispatcher
> (`stack_system.go:267`, `stack_go.go:190`, `stack_gvisor.go:103`). Only `nfqueue_linux.go:284-285`
> honours it.
>
> Two caveats a reader should carry: **the gVisor stack is retired by build tag**
> (`cmd/internal/build_libbox/tag_policy_test.go:16-83`; `go.mod:337-359`), and
> `stack_gvisor_icmp.go:124`, `:186` groups `ActionBypass` with `ActionFlow` — but a **Port-less**
> verdict, which is what `adapter/router.go:131` emits, falls through identically to accept.
>
> **So for a TUN client, the eligibility decision has no data-plane effect.** A flow it marks
> bypassable is still proxied in userspace. The decision has a real effect only in Linux redirect mode.

Held by behavioural tripwires against the pinned sing-tun revision, not by reading it:
`TestANewTrackerOnABypassIsNeverCreated`, `TestABypassFlowIsNeverCounted` (`protocol/tun`), plus
`native_bypass_dispatcher_test.go`, `native_bypass_trace_test.go`.

**L0 is not "the router, then a bypass".** It runs *before* the router, so it decides with an address
and nothing else. It cannot express a domain, a process or a protocol condition, and it must not be
used to compile a `DIRECT` rule into a route set unless that rule is already authoritative and
IP-only.

**L0's one boundary — FakeIP.** Because L0 runs first, a FakeIP address (a placeholder this process
minted, with no route in the platform's table and a domain only the router can recover) would be
handed to the OS and black-holed. Two ordinary configurations reach that: a configured
`route_address_set` (everything outside bypasses) and a `route_exclude_address_set` that happens to
cover `198.18.0.0/15`. `JudgeFlow` therefore falls through to the router for a FakeIP destination, at
the cost of two prefix comparisons, once per flow, skipped entirely when no FakeIP transport is
configured.

**DNS stays first.** The order in `JudgeFlow` is: configured DNS address → DNS by port → the route
sets → the router. That ordering is what keeps DNS-based ad filtering working. It makes **no** claim
about detecting DoH/DoT/DoQ and does not block UDP/443 to intercept QUIC.

### A.6 Two silent no-ops that were fixed — trap material

* A bypass carrying the direct outbound's **ICMP `Port`** was rewritten to `ActionFlow` by the pinned
  sing-tun's `judgeAndInstall` (`ff80ddea6`). The ruling: `ActionBypass` carrying a `Port` is
  rewritten to `ActionFlow`; a bypass silently becoming a userspace flow is how the bug reached
  production once already. Guarded by `TestBypassWithAPortBecomesAFlow` (`protocol/tun`) and
  `fast_bypass_verdict_test.go` (`route`).
* `JudgeFlow` checked route address sets **before** `dnsHijackByPort`, so `8.8.8.8:53` left sing-box
  entirely (`497d37727`). Measured as 4 hijacks where 5 (or 0) was wanted.

### A.7 What is measured

Decision hit rates at the real pre-match entry point, over a model flow mix:

| Flow shape | Weight | minimal | dashboard-enabled |
|---|---|---|---|
| literal IPv4 TCP | 55 | allowed | refused: tracker |
| literal IPv6 TCP | 15 | allowed | refused: tracker |
| literal IPv4 UDP | 15 | allowed | refused: tracker |
| sniffed domain TCP | 8 | refused: sniffed domain | refused: sniffed domain |
| FakeIP TCP | 5 | refused: FakeIP | refused: FakeIP |
| connected UDP | 2 | refused: UDPConnect | refused: UDPConnect |
| **total** | **100** | **3/6 shapes, 85% of weight** | **0/6 shapes** |

**The dashboard number is the finding.** The tracker guard is absolute: it refuses on the *existence*
of a tracker, not on what the tracker would record — so attaching the Clash API or a dashboard
disables L1 for every flow. That is the correct first-round choice (silently losing connection
statistics is not an acceptable optimisation), but it means **the L1 gain is real only for setups
without an API**.

L0, measured where it lives: no route sets → 0/4 shapes; `route_address_set: 10/8` → 2/4;
`route_exclude_address_set: 198.16/12` → 1/4; FakeIP, any configuration → never.

Cost, from `route/direct_offload_bench_test.go` on an Apple M1:

| | Cost |
|---|---|
| Decision, all conditions pass | 14–41 ns, **0 allocs** |
| Decision, refused by a cheap condition | ~5–11 ns, 0 allocs |
| Outbound profile alone | 5.3 ns, 0 allocs |
| **Flow setup, bypassed** | **119 ns, 0 allocs** |
| **Flow setup, userspace** | **1461 ns, 2856 B, 26 allocs** |
| TCP copy, kernel socket pair (L2) | 4742 / 5001 MB/s for 4 / 32 MiB |
| TCP copy, userspace buffers (L3) | 3952 / 5567 MB/s for 4 / 32 MiB |
| UDP per datagram (1400 B) | ~66 ns, 1 alloc |

> **The "userspace flow setup" row is a known-bogus number and is quoted here only to be retired.**
> `route/direct_offload_bench_test.go:135-138` names it: the old benchmark measured its own `net.Pipe`
> fixture, *"reported 26 allocations and 2856 B per operation, and the code under test was none of
> them. A benchmark whose number describes its own fixture is worse than no benchmark, **because it is
> quoted**."* It was fixed by `22d46966c` two days **after** the document quoted it, and
> `docs/fork/direct-offload.md:210` still carries it. Re-measured: **104 B, 2 allocs**. The other rows
> are historical M1 figures and reproduce only loosely on other hardware — re-measure before quoting.
>
> **The one cost claim with real suite enforcement is a `TEST`**, not a benchmark:
> `TestBypassDecisionUsesNoAllocations` (`direct_offload_bench_test.go:351`) uses four
> `testing.AllocsPerRun` + `require.Zero` calls covering `canFastBypass`, `CanBypass`, a FakeIP
> **refusal**, and the profile lookup.

**L2 and L3 are indistinguishable on loopback, and that is not a benchmark defect.** Loopback is the
friendliest case for a userspace copy and the least favourable for a zero-copy one. Each benchmark
asserts which path it measures, so they cannot silently converge.

One cost that was found and fixed: the eligible path logged a debug line whose arguments were built
**before** the logger decided the level was off — five allocations and ~95 ns per eligible flow, more
than the decision itself, on exactly the flows the feature targets (`e27d58711`).

### A.8 Scope, and what is deliberately not in it

Offload applies to a TUN inbound, TCP or UDP, a literal destination, a **single-outbound chain** whose
leaf implements `adapter.BypassableOutbound`, and a flow that survives every eligibility condition.

Not in this round: tracker accounting on the native path (a tracker refuses); outbound chains (a group
introduces selection, lifecycle and accounting semantics of its own, and "the selected leaf happens to
be direct" does not make those equivalent); `UDPConnect` (its own socket and NAT semantics); traffic
class (a native direct flow is outside the upload scheduler's managed domain by construction).

### A.9 Attribution

* `route.BypassVerdict` names the router condition that refused.
* `(*direct.Outbound).BypassBlockers` names the socket-level reason set behind `CanBypass`, including
  the ambient network policy and the self-address guard.

Both are diagnostics. **Nothing logs per flow**, and the decision builds no strings.

---

## Part B — traffic class and the upload scheduler

### B.1 The pipeline is ONE unit

```text
common/trafficclass          → route/dial_governor.go        → common/trafficsched
classify the flow              bound the post-transition        pace the managed upload
                               dial burst
```

`route/traffic_class.go` holds the policy map; `option/traffic_class.go` the config. Do not treat
these as three independent packages: a change to the class vocabulary moves all three.

**Class resolution:** the resolved chain is scanned **outermost to leaf**; the first explicit policy
wins; an explicit `default` **suppresses automatic detection for the whole chain**. Because the class
is resolved from the chain the flow actually took, two flows through one group on different members
can carry different classes.

### B.2 The upload scheduler

**Managed domain:** userspace-proxied upload flows only. A flow reaches the package only when the
kernel-splice copy path is not available to it (`route.ConnectionManager.uploadStreamGate`), so
nothing here competes with splice or with the native direct bypass. It is **not** machine-wide QoS.

**`upload_rate` is applied exactly as written.** There is **no hidden safety factor**: a configured
`8 MiB/s` admits `8 MiB/s` of managed upload.

**Absent, `0`, or an empty `traffic_scheduler` section leaves the scheduler inert** — it observes
every managed byte and admits all of them immediately, with **no queue, no lock and no timer**. A
configuration that does not ask for shaping does not get it, and remains loadable by official
sing-box.

**When a rate IS configured the shaper runs continuously**, for as long as the process does. That is
deliberate; see the measurement below.

Accepted spellings: `8388608` and `"8388608"` (bytes/s); `"8 MiB/s"`, `"8MiB"` (binary unit, the `/s`
is decorative — the field is always a rate); `"20 MB/s"` (decimal); `"20 Mbps"`, `"16000kbps"` (bits,
converted at 8 bits to the byte). A negative value, an unknown unit, a fractional value, or a
conversion that would overflow `int64` **fails the configuration** rather than being accepted and
ignored — for a rate limit, a silently ignored value means the configuration claims a policy the
process does not apply.

### B.3 The four measurements that settled the design

Recorded in `common/trafficsched/doc.go` with the experiments in `contention_test.go`/`rig_test.go`.
The metric is the receiver-visible delivery latency of a small high-priority message under contention,
because that is the product goal.

**1. Reordering does not work. Shaping does.** (4 NORMAL flows flooding a 2 MB/s shared FIFO through a
64 KiB per-flow acceptance window)

| mode | p99 | queue mean | bulk |
|---|---|---|---|
| no gate (baseline) | 146 ms | 257 KiB | — |
| gate installed, no rate | 150 ms | 257 KiB | — |
| `ModeAdmission` | 145 ms | 257 KiB | — |
| `ModeService` | 155 ms | 257 KiB | — |
| `ModePacedNormalOnly` @ 85% | 14 ms | 17 KiB | 1.74 MB/s |
| `ModePaced` (aggregate) @ 85% | **9 ms** | 15 KiB | 1.72 MB/s |
| `ModePaced` (aggregate) @ 70% | 8 ms | 14 KiB | 1.42 MB/s |

The first four rows are indistinguishable from doing nothing, and they cannot do better: the bytes
delaying the high-priority message are already inside the sender's acceptance window, out of
userspace's reach.

**2. A pacer that starts when interactive traffic arrives protects the SECOND request.** Measured as
the first 256-byte high-priority write after a second of uninterrupted bulk upload: no gate p50
127 ms; `ModeAdmission` (armed) 128 ms; `ModePacedNormalOnly` (continuous) **2 ms**; `ModePaced`
(aggregate, continuous) **2 ms**. Hence: the paced modes do not arm at all.

**3. Shaping must cover the high-priority lane too.** Two HIGH bulk flows and one small HIGH probe:
no gate p99 114 ms; `ModePacedNormalOnly` @85% 73 ms; `ModePaced` (aggregate) @85% **8 ms**. Shaping
only the NORMAL lane leaves the queue exactly as deep as it was. The aggregate mode is what ships.

**4. A write larger than the bucket is charged, not exempted.** The charge is split: the shared bucket
pays what the write had to see (at most one burst, so no flow can hold another hostage) and the flow
that sent it pays the rest in its own future. A flow writing 256 KiB at a time into a 64 KiB bucket is
admitted at 1.84 MB/s against a configured 1.70, where an unshaped control reaches 2.10.

### B.4 A learned rate is researched and NOT shipped

`adaptive_research_test.go` is gated behind the `trafficresearch` build tag, **which nothing
references** — not a workflow, not a Makefile target, not a script, not a release profile. Fifty-one
tests in this package run with **zero skips**; the research tests are simply not compiled.

**A test fails if the research controller ever becomes reachable from production code.** Read that
claim precisely, because it is narrower than it sounds:

* the guard (`common/trafficsched/adaptive_controller_guard_test.go:20-63`,
  `TestAdaptiveControllerIsNotInProduction`) is **untagged, so it runs in every release run**;
* it is a **source-text grep over non-`_test.go` files in that one directory for the identifier
  `adaptiveRate`**;
* it carries a positive control (the identifier must exist in `adaptive_research_test.go`), pins
  `//go:build trafficresearch` on the research file, and pins `type RateSource interface` in
  `scheduler.go` — so the **seam** it protects is guarded too.

What it will **not** catch: a learned controller under a different name, in a different package, or
wired in through `SetRateSource` from `box.go`. It is a source-text guard on one identifier, which is
also the practice `docs/UPSTREAM-SYNC.md:89-94` argues against — and the honest reading is that here
it is the *cheapest* available control, not a behavioural one.

**Why the tag exists** (`bb21a854c`, "test(traffic): stop the adaptive characterisation gating
releases"): `TestAdaptiveRatePrototype` **fails deterministically on a GitHub Linux runner and passes
on macOS from identical source** — the 85% oracle reports 1.7719296 MB/s against a required 1.8605. The
alternative to tagging it out was weakening the assertion, and the commit explicitly refused to do
that.

**What a future AI loses by not knowing this:** it will re-derive the AIMD controller, re-run the same
experiment, see `-tags trafficresearch` fail on Linux, and "fix" it by relaxing the oracle.

Why the learned rate is not shipped: the obvious observable — how long a write blocked — measures the
wait produced by **contention**, not the rate of the path. On a 2.00 MB/s link it reports 1.29 MB/s
with one flow and 0.35 MB/s with four, tightly, both times: a stable, precise, **wrong** number. A
prototype AIMD controller follows a moving capacity but settles **at** the capacity, and at the
capacity the queue is built:

| | HIGH p95 | bulk |
|---|---|---|
| fixed rate at 85% of capacity | **8 ms** | 1.69 MB/s |
| fixed rate at 100% of capacity | 42 ms | 1.99 MB/s |
| learned rate (AIMD prototype) | 62 ms | settles at 100% |

> AIMD needs a queue to exist in order to detect that it is too fast, and this feature exists to
> remove the queue.

**Those tables are historical records.** They are reproducible from `contention_test.go`, but they are
emitted with `t.Logf` and the assertions are on *other* quantities (median bytes ahead of the probe,
the bucket bound, `rate × 1.15`, the queue-mean ratio). **Reading them as asserted guarantees is a
mistake.**

### B.4b The mode ladder — and the only mode that ships

Four modes exist (`common/trafficsched/scheduler.go:29-51`). **The only production reference to any
mode is `route/conn.go:102` = `ModePaced`.** The other three exist only in tests and the manual
harness, as measured control groups. `option/traffic_scheduler.go` has **exactly one field**
(`UploadRate`, `:50`) — **there is no `mode` key, and adding one would expose a mode that was measured
as indistinguishable from doing nothing.**

### B.4c Two measured properties of the paced path that no document and no test records

Measured with `BenchmarkStreamWriteTax/gate-paced-generous` (rate `1<<40`, i.e. a rate that can never
bind) at the release baseline:

| Write size | Paced gate | Inert gate |
|---|---|---|
| 256 B | 5,462 ns, 1 B/op, **0 allocs** | ~9.8 ns above the bare writer |
| 4 KiB | **1,423,806 ns**, 257 B/op, **3 allocs/op** | — |
| 64 KiB | **1,404,768 ns**, 255 B/op, **3 allocs/op** | — |

Two consequences, and both matter:

1. **The paced path allocates**, about 3 allocations per write at 4 KiB and above — while
   `bench_test.go:81-83` states that a shaped write must never allocate ("a shaped write that
   allocated would turn a rate limit into a garbage source"), and `doc.go:99-106`'s "no allocation on
   the uncontended path" is **true of the inert path only**. No CI test covers the paced path's
   allocations.
2. **The paced path has a fixed ≈1.40 ms per-write floor that is independent of both the write size
   and the configured rate** — 4 KiB and 64 KiB cost the same, ≈1.4 × `paceTick`
   (`scheduler.go:78`, a 1 ms tick). The inferred mechanism: `chargeLocked` sets `f.owedUntil` a few
   nanoseconds ahead (`:662-677`), `flowTimeReadyLocked` then reports not-ready (`:701-710`), and the
   only wake path is the 1 ms `paceWakeLoop` tick (`:869-892`). **Practical consequence: one managed
   flow writing 64 KiB at a time tops out at roughly 47 MB/s whatever rate is configured above it.**

**So installing any positive rate costs far more per write than the inert gate, before any shaping
happens.** That is not a reason to avoid the feature — the feature's whole point is the queue — but it
is a reason not to describe the rate as "a harmless ceiling".

### B.5 Cost when nothing contends

A write costs about **9 ns** more than writing to the bare writer on the **inert** path, with **no
allocation**. That figure reproduces (+9.8 ns at 256 B, +5.6 ns at 4 KiB). The finer claim that "about
2.5 ns is the wrapper and the rest is two atomic loads" does not reproduce as stated — treat the split
as a historical M1 decomposition, not a measurement.

---

## Part C — the dial governor

`route/dial_governor.go`.

A path change invalidates connections in bulk: every stream that was using the old path fails around
the same moment and is re-dialled at the same moment — a thundering herd of TCP, TLS and tunnel
handshakes landing while the radio is already busy re-establishing.

**The constants:** window **3 s**, slot limit **24**, budget **500 ms**. The window is opened **only
by a draining transition** (`route/conn_reclaim.go:313-317`) and applied at `route/conn.go:274`, `:518`.

**Outside the window there is nothing to bound.** The zero value is inert, and this is provable rather
than asserted: `open := time.Now().Before(g.windowEnd)` is **false for the zero `time.Time`**, so
`enter` returns `noopRelease` (`dial_governor.go:114`, `:126-128`). Pinned by `dial_governor_test.go:40`.

**Interactive traffic is never delayed.** A dial whose class says a human is waiting — `interactive`
or `realtime` — is never queued, whatever the governor is doing (`:89-91`). Only the classes with no
latency expectation can wait: `default` and `bulk`. **`ClassDefault` is deliberately on the bounded
side** (`:85-88`), because it is what an unconfigured flow resolves to (most traffic), and exempting it
would leave the governor covering only what an operator happened to label — not where the herd comes
from.

**The bound is soft.** After the 500 ms budget a dial stops waiting and proceeds unbounded, so the
governor can only ever smooth the start of a burst — never delay recovery indefinitely, never
serialise it globally.

**It waits on a channel, not a timer.** It used to retry every 6–18 ms until its budget ran out: a
poll producing timer wakeups, scheduler wakeups and mutex contention for the whole 500 ms — in a fork
whose stated goal is *fewer* radio and CPU wakeups on a phone. A waiter now sleeps until something
actually happens: a slot is released, the budget expires, the caller's context is cancelled, or the
window closes. The `released` channel is buffered with one token so wakeups coalesce (`:67-70`,
`:173-177`).

`enter` returns a release function that the caller **MUST** call exactly once on every path including
errors.

> **Two inaccuracies in the governor's own comment, recorded so they are not "fixed" by mistake.**
> `dial_governor.go:23-24` says *"one atomic-free time comparison takes it out of the dial path
> entirely"*. That is true of the **priority** path — `isPriority` returns first at `:106-108`, before
> anything is taken — but a **bounded-class dial outside the window still takes two mutexes**
> (`:112` → `settings()` locking `:183-190`, then `:113`) and **allocates a channel on the first such
> dial** (`:120-122`). The comment describes the common interactive path, not every path.
>
> Separately, `route.isPriority` (`:89`) duplicates `trafficclass.Class.IsHighPriority()`
> (`common/trafficclass/class.go:88`) — **two implementations of one mapping**, one used by the
> governor and one by the scheduler (`scheduler.go:300`). Editing only one of them is a real trap.

---

## Part D — Evidence, provenance, cautions

### Evidence

| Guarantee | Level | Re-check |
|---|---|---|
| `TestEveryDialerOptionIsClassified` (reflection, both directions) | `VERIFIED_UNIT` | `go test -count=1 ./common/dialer/...` |
| Zero-value profile refuses; family strategy applies to a literal | `VERIFIED_UNIT` | `TestZeroProfileRefuses`, `TestFamilyStrategyAppliesToALiteralAddress` |
| The bypass decision costs 0 allocs (a test, not only a benchmark) | `VERIFIED_UNIT` | `route/direct_offload_bench_test.go`, `route/bypass_capability_cost_test.go` |
| `ActionBypass` is honoured by no TUN stack; a tracker on one is never created | `VERIFIED_UNIT`, behavioural trace | `protocol/tun/native_bypass_*_test.go` |
| A bypass carrying a `Port` becomes an `ActionFlow` | `VERIFIED_UNIT` | `TestBypassWithAPortBecomesAFlow`, `route/fast_bypass_verdict_test.go` |
| The scheduler shapes aggregate, admits no over-rate, releases on transition | `VERIFIED_UNIT` | `go test -count=1 ./common/trafficsched/...` |
| The research controller cannot reach production | `VERIFIED_UNIT` (fails CI if it does) | `common/trafficsched/adaptive_research_test.go` |
| The gate preserves the extended-writer path and reports MTU truthfully | `VERIFIED_UNIT` | `common/trafficsched` gate tests (11) |
| Trace-class lane mapping | `VERIFIED_UNIT` | `common/trafficclass`, `route` |
| Race-freedom of these packages | the CI step exists (`verify.yml:310`) but **no run record is in the repository**, and `-race` is `BLOCKED_BY_ENVIRONMENT` on this host | Linux runner |
| Real-link shaped throughput | **historical record** — the harness is a manual engineering tool (`common/trafficsched/harness`) | needs a real link |

### Provenance

| Commit | Type | What it did |
|---|---|---|
| `d2b34f1cf` | FEATURE | the bypass/socket-semantics profile |
| `3dea03fd5` | FEATURE | the per-flow verdict |
| `8a6e5f3cc` | FEATURE | eligibility model |
| `ff80ddea6` | FIX | a bypass carrying the ICMP `Port` was rewritten to `ActionFlow` |
| `497d37727` | FIX | DNS hijack ordering before the route address sets |
| `386e95a70` | DOC | records the TUN-mode limitation |
| `544c1ab5e` → `94df98f92` | FEATURE | the scheduler and `upload_rate` |
| `4e34d0091` | FEATURE | `upload_rate` accepted spellings |
| `bd7b1ed19` | FIX | `writePeriod` saturates instead of converting `float64` ns to `Duration` |
| `912ed1efa` | TEST | records an intermittent failure |
| `a10078557`, `bb21a854c` | FIX | scheduler lifecycle and counters (`Close` zeroes counters) |

All **OWN** except: `e4f847ecf` is **EXTERNAL-ADAPTED** — the QUIC tuning opt-in was derived from
reading `quic-go/masque-go` and `quic-go/connect-ip-go`, which set only `EnableDatagrams` and
`InitialPacketSize`. That attribution has a quoted source, which is why `EXTERNAL-ADAPTED` is used.

### AI cautions

1. **Do not tighten the bypass rule into "config emptiness".** The shipped topology would refuse
   every flow, including literal IPs.
2. **Do not loosen it by ignoring a hard single-family strategy on a literal destination.**
3. **Do not add a `DialerOptions` field without classifying it.** `BlockerUnclassified` refuses at
   runtime and the reflection test fails in review — that is the design.
4. **Do not remove the tracker guard to make L1 "work" in dashboard setups.** It refuses on the
   *existence* of a tracker, deliberately.
5. **Do not describe L1 as working in TUN mode.** It has no data-plane effect there; the only way to
   change that is a sing-tun change.
6. **Do not teach L0 about domains, processes or protocols.** It runs before the router with an
   address and nothing else.
7. **Do not remove the FakeIP fall-through in `JudgeFlow`** — a FakeIP destination would be
   black-holed by the OS.
8. **Do not reorder DNS before/after the route sets.** DNS must reach the DNS router.
9. **Do not add a "safety factor" to `upload_rate`.** The number is the rate; a hidden factor makes
   the configuration a lie.
10. **Do not make the shaper arm on contention.** The measurement says a pacer that starts on
    interactive traffic protects the second request, not the first.
11. **Do not shape only the NORMAL lane.**
12. **Do not exempt a write larger than the bucket.**
13. **Do not make the research controller reachable.** A test fails if you do — and the reason is
    that its observable measures contention, not capacity.
14. **Do not install the scheduler gate on a kernel-splice flow.** It cannot delay such a flow and
    would only remove the fast path.
15. **Do not remove `ClassDefault` from the bounded side of the dial governor.**
16. **Do not turn the dial governor's soft bound into a hard one**, and do not reintroduce polling.
17. **Do not treat the LANE gate as machine-wide QoS** in any user-facing text.
