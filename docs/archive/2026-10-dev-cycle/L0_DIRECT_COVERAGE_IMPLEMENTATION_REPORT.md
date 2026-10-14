# L0_DIRECT_COVERAGE_IMPLEMENTATION_REPORT

```text
L0_DIRECT_EXPANSION = NO_SAFE_CHANGE
PRODUCTION_CODE_CHANGED = NO
```

---

## A. Baseline

```text
TASK_BASE_SHA = dfdd71e71db34c67a6451cea51914196b3322d4e   (origin/testing)
branch        = perf/l0-direct-coverage   (created from TASK_BASE_SHA; testing NOT modified)
HEAD          = dfdd71e71 at audit time; this report is the only commit on the branch
working tree  = the two pre-existing submodule states only ( M clients/apple, m clients/desktop),
                present before this task and untouched by it
```

### Knowledge-base delta, per AGENTS.md

The knowledge baseline is `a267e99e` (tag `v0.1.6`), and `KNOWLEDGE/BASELINE.md` records the
post-baseline delta as **"none. `v0.1.6..HEAD` is 5 commits and no `.go` source changed."**

**That statement is no longer true, and AGENTS.md rule 1 applies — source wins.** The delta is now 71
commits, and production Go under `route/` has changed:

```text
route/fast_path.go   | 149 +++++++++++++++++++++++++++++   (new file)
route/route.go       |  45 +++++++++++++---                (canFastBypass gained a parameter + branch)
route/router.go      |  16 ++++++++                        (fastPathPolicies field + setter)
```

So the `route/` module descriptions are **stale** and were not relied on. `protocol/tun/**`,
`common/dialer/**` and the DNS/FakeIP paths are **unchanged** since the baseline
(`git diff --name-only v0.1.6..HEAD -- protocol/tun common/dialer dns` is empty for non-test files),
so the knowledge base remains a usable description of those. Every claim below is verified against
current source regardless.

---

## B. Current L0 real path

Read from `protocol/tun/inbound.go:1031-1106` (`Inbound.JudgeFlow`) and
`adapter/router.go:68-137` (`JudgeFlow`). The doc comment at `inbound.go:962-1030` matches the code
exactly; this is a re-verification, not a transcription.

```text
JudgeFlow(network, source, destination, firstPacket)
  1. destination = canonicalAddrPort(destination)              inbound.go:1050
       v4-mapped (::ffff:a.b.c.d) -> four-byte form. FIRST, deliberately: every comparison below is
       written against the four-byte form and each of them fails OPEN on the mapped one.
  2. slices.Contains(t.dnsHijackAddress, destination.Addr())   inbound.go:1053
       UDP -> ActionHijackDNS        TCP -> ActionAccept  (STAYS in sing-box: the stream DNS path)
       Applies on EVERY port, not just 53.
  3. t.dnsHijackByPort && port == 53 && (TCP || UDP)           inbound.go:1065
       UDP -> ActionHijackDNS        TCP -> ActionAccept
  4. routeSetsApply := fakeIPStore == nil || !fakeIPStore.Contains(addr)   inbound.go:1092
       a FakeIP placeholder makes step 5 INAPPLICABLE
  5. if routeSetsApply:                                        inbound.go:1093-1104
       len(routeAddressSet) > 0 && !any(routeAddressSet).Contains(addr) -> ActionBypass
       any(routeExcludeAddressSet).Contains(addr)                      -> ActionBypass
  6. otherwise -> adapter.JudgeFlow(t.router, ...)              inbound.go:1105
       -> Router.PreMatch: reverse mapping, domain/process/protocol policy, and the router-level
          Direct Fast Path (L1) eligibility
```

**L0 terminates evaluation.** A step-5 hit returns `ActionBypass` and the Router's call count is
zero — not "consulted then ignored".

### The three layers, verified as distinct

| layer | what it is | who decides | source |
|---|---|---|---|
| **L0** | the platform's own routing table carries the flow | `JudgeFlow` steps 1-5, address only | `protocol/tun/inbound.go:1093-1104` |
| **L1** | router-level Direct Fast Path (`ActionBypass` from `PreMatch`) | `canFastBypass`, full metadata | `route/route.go:786` |
| **L2/L3** | userspace forwarding / kernel-assisted copy | `route/conn.go`, `route/splice.go` | not touched |

### One finding about L0's reach, verified and NOT a defect

`routeAddressSetPrefixes()` (`inbound.go:786`) is consumed by exactly one caller —
`protocol/tun/redirect_platform_linux.go:64`, the Linux auto-redirect path. On iOS/Android the route
sets therefore do **not** configure the platform's routing table; they act purely as `JudgeFlow`'s
bypass criterion, and what actually reaches the TUN is decided by the NetworkExtension's own
`includedRoutes` from the user's profile.

That is consistent, not broken: on a default-route TUN every destination arrives at `JudgeFlow`, so a
step-5 hit really does leave sing-box, and the platform routes it out of the tunnel because the
tunnel's own routes are what sent it in. Recorded because it is the kind of asymmetry that looks like
a coverage gap and is not one.

---

## C. Candidate extension inventory

Every row of the brief's §5 table, resolved against source. **`safe` means: can be promoted to L0
without a new policy engine and without changing what any existing configuration means.**

| # | candidate | L0-promotable | source proof | reason |
|---|---|---|---|---|
| 1 | literal IP decided by `route_address_set` | **ALREADY L0** | `inbound.go:1094-1098` | this is the existing capability |
| 2 | literal IP decided by `route_exclude_address_set` | **ALREADY L0** | `inbound.go:1099-1103` | existing capability |
| 3 | FakeIP | **NO** | `inbound.go:1092` | the placeholder has no platform route; only the router can unmap it. Guard is *before* step 5, so no new derivation may take effect earlier |
| 4 | sniffed-domain dependent | **NO** | L0 runs before `Router.PreMatch`; sniffing happens inside `preMatchFlow` (`route/route.go:531`) | the domain is not known at L0 time, and `metadata.Domain` will disqualify the flow at L1 anyway |
| 5 | reverse-mapping dependent | **NO** | same; reverse mapping is a Router concern | `TestL0AuthoritativeIPPolicyWinsBeforeReverseMapping` pins that L0 wins *before* it, which is the existing product decision |
| 6 | process / protocol dependent | **NO** | L0 has no process or protocol input | `metadata.ProcessInfo` and `metadata.Protocol` are produced in `prepareMatchMetadata` / sniffing, both after L0 |
| 7 | group selected `direct` | **NO** | `resolveOutbound` (`route/route.go:247`) walks `Selected`/`SelectForFlow`; selection is control-path | §4.4. A selector's choice is a decision, and bypassing it skips the group's lifecycle |
| 8 | `direct` + special socket semantics | **NO** | `adapter.BypassableOutbound` doc (`adapter/outbound.go:88-113`) + `protocol/direct/outbound.go:319` | the platform cannot reproduce `bind_interface`, `routing_mark`, TFO/MPTCP, network strategy. The existing single interpreter is `CanBypass` |
| 9 | **explicit static IP/CIDR terminal direct rule** | **NO — and this is the decisive one** | see below | shadowed by earlier rules |
| 10 | `route.final = direct` | **NO** | `inbound.go:1105` reaches the rule walk at `route/route.go:1191` | earlier domain/process/proxy rules still match; `final` is only the fallback |

### Why #9 is unsafe — the decisive proof

The rule walk is a **first-match-wins loop**:

```text
route/route.go:1191   for currentRuleIndex, currentRule := range r.rules {
route/route.go:1193       if !currentRule.Match(metadata) { continue }
                          <action taken; evaluation ends>
```

Consider:

```json
{"route": {"rules": [
  {"domain_suffix": ["example.com"], "action": "route", "outbound": "proxy"},
  {"ip_cidr": ["93.184.216.34/32"],  "action": "route", "outbound": "direct"}
]}}
```

Today, a flow to `93.184.216.34` that sniffed as `example.com` matches rule 0 and is **proxied**.
Promoting rule 1 to L0 would match the same flow on its **address alone**, before any rule runs, and
bypass it to the platform — silently converting a proxied flow into a direct one, and doing so
precisely for the addresses an operator listed as direct while believing their domain rules still
won.

L0 cannot see rule order (it sees one address), so it cannot reproduce first-match-wins. The brief
forbids this in §4.3 ("不要把任意 domain DIRECT 规则编译成 L0") and again in §4.1's ordering
requirements, and the existing tests encode the same boundary — `TestL0CannotExpressAnythingButAddresses`
asserts exactly that L0 cannot express anything other than an address decision.

The one sub-case that would be semantically identical — an IP-only direct rule in a configuration
with **no earlier rule that could match** — is degenerate (a single-rule config), provides no
generalisable coverage, and would require L0 to reason about the whole rule list, which is a policy
engine. Not worth building; see §D.

### P0: is any *existing* L0 configuration silently not working?

Checked for the failure modes the brief names — implementation defect, canonicalisation, update,
stale snapshot, wrong verdict:

```text
canonicalisation        v4-mapped -> four-byte happens FIRST (inbound.go:1050), before every
                        comparison; TestMappedAddress* and adapter/judge_flow_mapped_test.go cover it
update / stale snapshot updateRouteAddressSet (inbound.go:773-778) rebuilds BOTH slices under
                        routeAddressSetAccess.Lock, and JudgeFlow reads BOTH under RLock
                        (inbound.go:1073-1076) -> a reader can never see include from one
                        generation beside exclude from another
empty include set       len(...) > 0 guards it: no include set means NO include bypass, which is
                        the correct reading of "no include policy"
include + exclude       include-pass then exclude-hit still bypasses (inbound.go:1099 is reached
  overlap               after 1094 falls through) — the documented precedence
FakeIP vs broad prefix  step 4 makes step 5 inapplicable, so a set covering 198.18.0.0/15 cannot
                        hand a placeholder to the platform
DNS vs L0               steps 2-3 precede step 5 on every port, so a query to a configured DNS
                        server is never bypassed
```

**No defect found.** All 20 named L0/DNS/FakeIP tests pass unchanged (§F).

### P1 and P2

**P1 (safe expansion of an explicitly declared IP-only policy):** the only class that qualifies
semantically is #9, and it is disqualified by rule ordering. Nothing else in the inventory is both
IP-only and currently un-promoted. No safe expansion exists.

**P2 (optimise `JudgeFlow` itself):** the only cost candidate is the `RWMutex` read at
`inbound.go:1073`, taken on every flow that reaches step 5. Replacing it with an atomic snapshot is
the obvious idea and the brief pre-emptively constrains it. **Not done, because there is no measured
problem:** the brief requires benchmark/allocation/profile evidence for P2, there are **no benchmarks
in `protocol/tun` at all** (`git grep 'func Benchmark' -- protocol/tun` → empty), and the read is an
`RLock`/`RUnlock` around a two-word slice assignment — no allocation, no radix work, uncontended in
the steady state. Inventing a benchmark to justify a speculative lock change is the "假 benchmark" the
brief's §10 forbids, so the idea is recorded here rather than implemented.

---

## D. Final construction decision

```text
WHAT_CHANGED                  = nothing. No production code, no test, no workflow.
WHY_THIS_IS_SAFE              = the safe set is already L0; the unsafe set is unsafe for a reason
                                that L0 structurally cannot express (rule ordering)
WHAT_WAS_NOT_CHANGED          = everything, including all four hard boundaries (§4) and the
                                JudgeFlow step order
```

The audit's answer to the brief's central question — *which class of traffic can be safely promoted
from the full Router path to L0 without a new policy engine?* — is: **none that is not already
there.** L0's existing surface is exactly the set of facts that are authoritative on an address
alone (a configured route set), plus the two guards that must precede it (DNS hijack, FakeIP). Every
other DIRECT-shaped flow depends on a fact L0 cannot see — order, domain, process, protocol, group
selection, socket semantics — and promoting any of them would convert an operator's proxy rule into
a bypass.

This is the outcome the brief's §0 explicitly prefers over a plausible-looking patch:
**`L0_DIRECT_EXPANSION = NO_SAFE_CHANGE`**.

---

## E. Files changed

**None.** No production file, test file, workflow or script was modified. `git status` on the branch
shows only the two pre-existing submodule entries.

A deliberate non-change worth naming: the three `route/` files that `fast_path` added since the
knowledge baseline were audited for L0 interaction and have **none** — `route/fast_path.go` is reached
only from `canFastBypass`, which is L1 and runs inside `preMatchFlow`, strictly after L0 has already
returned. So the recent route-side work does not touch this task's object.

---

## F. Tests

All run against TASK_BASE_SHA with the `AGENTS.md`-correct tag set (an `OTHERS`-derived list; the
Darwin `release/DEFAULT_BUILD_TAGS` is not used, and `badlinkname`/`with_naive_outbound` are excluded
because they cannot link on this host — the latter needs cronet, which has no Windows build).

| command | result |
|---|---|
| `go test -count=1 -tags <OTHERS-derived> ./protocol/tun/...` | **PASS** (3.379 s) |
| `go test -count=1 -tags <OTHERS-derived> ./common/dialer/...` | **PASS** (13.019 s) |
| `go test -count=1 -tags <OTHERS-derived> -short ./route/...` | **PASS** (58.139 s + 1.032 s) |
| the 20 named L0 / DNS / FakeIP tests, verbose | **PASS** (all 20, listed below) |
| `gofmt` (the CI gate directory list) | clean |

The named set, each passing unchanged:

```text
TestL0CannotExpressAnythingButAddresses        TestL0AuthoritativeIPPolicyWinsBeforeReverseMapping
TestL0HitReport                                TestL0DoesNotDiscardProcessOrProtocolRulesOnTheNonL0Branch
TestDNSHijackAddressBeatsDirectFastPath        TestDNSHijackByPortBeatsDirectFastPath
TestDNSHijackBeatsRouteExcludeAddressSet       TestDNSHijackAppliesToEveryPort
TestDNSHijackByPortBeatsRouteExclude           TestDNSHijackByPortTCPBeatsRouteExclude
TestDNSHijackByPortBeatsRouteAddressSetMiss    TestDNSHijackByPortTCPBeatsRouteAddressSetMiss
TestDNSHijackAddressBeatsRouteAddressSetMiss   TestDNSHijackByPortDisabledDoesNotHijack
TestDNSHijackByPortIgnoresNonTCPAndNonUDP      TestDNSHijackByPortWorksForIPv6
TestNonDNSRouteAddressSetMissStillBypasses     TestFakeIPIsNotBypassedByRouteAddressSet
TestFakeIPIsNotBypassedByRouteExcludeAddressSet TestJudgeFlowDNSPrecedenceTruthTable
```

**`-race`: BLOCKED_BY_ENVIRONMENT.** `-race` requires cgo on this host; the only C compiler is
Swift's clang, which hands the link to MSVC's `link.exe` and fails with
`LNK1143: invalid or corrupt file: no symbol for COMDAT 0x6`. Redirecting it to the bundled LLVM
linker gets further and then fails on the linker script Go injects
(`lld-link: ignoring unknown argument '-T'` → `fix_debug_gdb_scripts.ld: unknown file type`). Three
attempts, recorded in the earlier debug report. **Not claimed as PASS.** No concurrency-relevant code
was changed this round, so nothing here depends on it; had a change been made to the route-set
locking, this would have been a blocker rather than a note.

---

## G. Performance

**No new measurement is claimed, and none was needed** — no code changed, so there is nothing to
measure against. The brief's §10 is explicit that a historical knowledge-base benchmark is not a
current measurement, and there are no `protocol/tun` benchmarks in the tree to re-run.

What is stated as source reading rather than measurement: `JudgeFlow` steps 1-3 are a slice scan and
a port/network comparison; step 4 is two prefix comparisons and is skipped entirely when
`fakeIPStore == nil`; step 5 is two radix lookups; the route-set snapshot is read under an `RLock`
with no allocation. The doc comment's cost claims at `inbound.go:1024-1030` are consistent with the
code, but **they are the author's claim, re-read, not a measurement I took.**

---

## H. Remaining limits

```text
L0 coverage benefit                    = NONE PROPOSED. No expansion was implemented, so there is
                                          no before/after coverage matrix to give. Reporting a
                                          percentage here would be invented.
coverage benefit if #9 were attempted  = STRUCTURAL ONLY / NOT MEASURED ON DEVICE, and unsafe
                                          regardless (rule-ordering shadowing)
domain-dependent DIRECT                still requires the Router. Unchanged and correct: on a
                                          default-route TUN the domain is not known at L0 time.
process / protocol / group DIRECT      still requires the Router. L0 has no such input.
DoH / DoT / DoQ identification         NOT generically intercepted, and this round did not attempt
                                          it. §4.1 forbids it. A query to a configured DNS address
                                          is hijacked on every port; anything else is ordinary traffic
                                          unless the by-port rule is on.
real iOS NetworkExtension behaviour    NOT device-verified. Nothing in this audit depends on it:
                                          L0's decision is returned to sing-tun, and the
                                          reachability argument in §B rests on the profile's own
                                          includedRoutes, which is platform configuration rather than
                                          code in this tree.
`-race`                                BLOCKED_BY_ENVIRONMENT (see §F). Not PASS.
knowledge base                         NOT refreshed, per §7.4 and AGENTS.md rule 5. The stale
                                          "no .go source changed" line in KNOWLEDGE/BASELINE.md is
                                          recorded in §A and is a defect for a future refresh to fix,
                                          not to reconcile silently.
```

---

## Git state

```text
branch  perf/l0-direct-coverage   (NOT merged to testing; testing not modified)
commits this report only
tag     not moved
release not created, no assets published
```
