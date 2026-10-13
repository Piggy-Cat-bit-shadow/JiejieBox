# L0_DIRECT_COVERAGE_DEBUG_REPORT

```text
DEBUG_BASE_SHA   = dfdd71e71db34c67a6451cea51914196b3322d4e   (TASK_BASE_SHA)
DEBUG_START_SHA  = 3230bc548d2ea40bce2de0813130ffc86b3e1797   (previous round's HEAD)
BRANCH           = perf/l0-direct-coverage
DEBUG_HEAD       = this report only
```

---

## 0. The finding that determines everything else

The previous round changed **one file, and it is documentation**:

```text
$ git diff --stat dfdd71e71..perf/l0-direct-coverage
 L0_DIRECT_COVERAGE_IMPLEMENTATION_REPORT.md | 313 ++++++++++++++++++++++++++++
 1 file changed, 313 insertions(+)

$ git diff --name-only dfdd71e71..perf/l0-direct-coverage -- '*.go'
 NONE - no .go file differs from the base
```

**No production code, no test, no workflow, no script.** Its conclusion was
`L0_DIRECT_EXPANSION = NO_SAFE_CHANGE`, and that conclusion is correct — but it is correct for a
reason the report states only in passing: there is nothing to debug, because nothing was built.

So the debugging question had to change. "Did this round introduce a DNS bypass?" is unanswerable in
an interesting way: a round that changed no code cannot have introduced one. The question that
actually has content is the one the brief's §26 asks and its §"最终 Debug 哲学" implies:

> **Are the L0/DNS/FakeIP boundaries actually guarded by tests that can fail — i.e. was the previous
> round's `NO_SAFE_CHANGE` a verified conclusion, or an unexamined one?**

That is what this report answers, by reverse-breaking every boundary and recording which named test
turns red.

---

## 1. Verdict model, rebuilt from current source

Read from `protocol/tun/inbound.go:1031-1106` and `adapter/router.go:68-137`, not from the report and
not from the knowledge base.

| situation | correct behaviour | source | verified |
|---|---|---|---|
| configured DNS address + UDP, any port | `ActionHijackDNS` | `inbound.go:1053-1055` | YES |
| configured DNS address + TCP, any port | `ActionAccept` — stays in sing-box, stream DNS takes it | `inbound.go:1057` | YES |
| `dnsHijackByPort` + UDP/53 | `ActionHijackDNS` | `inbound.go:1065-1068` | YES |
| `dnsHijackByPort` + TCP/53 | `ActionAccept` | `inbound.go:1070` | YES |
| `dnsHijackByPort` + non-53 | no special handling; route sets decide | `inbound.go:1065` port guard | YES |
| `dnsHijackByPort` disabled | UDP/53 is ordinary traffic | `inbound.go:1065` | YES |
| FakeIP destination | L0 inapplicable — route sets skipped entirely | `inbound.go:1092` | YES |
| include set non-empty and miss | `ActionBypass`, Router call count 0 | `inbound.go:1094-1098` | YES |
| include set empty | **no** include bypass | `inbound.go:1094` `len(...) > 0` | YES |
| exclude set hit | `ActionBypass` | `inbound.go:1099-1103` | YES |
| include hit **and** exclude hit | `ActionBypass` (include falls through, exclude fires) | `inbound.go:1094` → `1099` | YES |
| v4-mapped destination | canonicalised FIRST, then all of the above | `inbound.go:1050` | YES |
| domain / process / protocol dependent | Router | L0 has no such input | YES |
| group selected direct | Router | no L0 path reaches selection | YES |
| special socket semantics | Router; the only interpreter is `CanBypass` | `adapter/outbound.go:88-113` | YES |
| L0 uncertain | Router | `inbound.go:1105` | YES |

The implementation **matches this table on every row.** No conflict was found, so there was no bug to
prefer over the implementation.

---

## 2. Adversarial verification: reverse-breaking each boundary

A boundary is only guarded if a test can fail when it is broken. Eight mutations were applied to
`protocol/tun/inbound.go`, the focused L0 suite was run, and the file was restored from git after
each. The restore is verified by blob hash — `90b7824d65024ec9aa5f48f2267219c69b80d382`, identical to
`HEAD` — and the tree is pristine at the end.

| # | mutation | breaks | result | tests that turned red |
|---|---|---|---|---|
| M1 | canonicalisation removed | v4-mapped fail-open on all four comparisons | **RED** | `TestL0AuthoritativeIPPolicyWinsBeforeReverseMapping`, `TestMappedDnsAddressIsHijacked`, `TestMappedFakeIPIsStillGuardedFromTheRouteSets`, `TestMappedDestinationReachesTheRouteSets` (4 top-level tests, from a full-package run) |
| M2 | configured-DNS guard fires only on port 53 | a DNS query on a non-53 port bypassed by a route set | **RED** | `TestDNSHijackAppliesToEveryPort`, `TestJudgeFlowDNSPrecedenceTruthTable/addr/udp` |
| M3 | FakeIP guard removed | placeholder handed to the platform and black-holed | **RED** | `TestFakeIPIsNotBypassedByRouteAddressSet`, `...RouteExcludeAddressSet`, `TestL0HitReport`, `TestMappedFakeIPIsStillGuarded…` |
| M4 | include-set miss falls through to Router | L0 stops being authoritative | **RED** | `TestJudgeFlowDNSPrecedenceTruthTable/route-set_miss_bypasses`, `TestL0AuthoritativeIPPolicyWinsBeforeReverseMapping`, `TestFakeIPIsNotBypassedByRouteAddressSet` |
| M5 | exclude-set hit falls through to Router | an exclude hit is routed instead of carried | **RED** | `TestJudgeFlowDNSPrecedenceTruthTable/exclude_hit_bypasses`, `TestL0AuthoritativeIPPolicyWinsBeforeReverseMapping`, `TestFakeIPIsNotBypassedByRouteExcludeAddressSet` |
| M6 | empty include set treated as a bypass | with no include set, **every** flow bypasses | **RED** | `TestJudgeFlowDNSPrecedenceTruthTable`, `.../empty_route_set`, `.../byport_disabled/udp53` |
| M7 | TCP DNS `ActionAccept` → `ActionHijackDNS` | stream DNS architecture changed | **RED** | `TestDNSHijackByPortBeatsDirectFastPath`, `TestDNSHijackByPortTCPBeatsRouteAddressSetMiss`, `TestDNSHijackByPortTCPBeatsRouteExclude`, `TestDNSHijackByPortWorksForIPv6`, `TestJudgeFlowDNSPrecedenceTruthTable` |
| M8 | sanity: reference a non-existent action name | — | **DID NOT COMPILE** | (as intended — proves the harness runs the compiler rather than reporting a stale result) |

```text
L0_DIRECT_MUTATIONS_TESTED   = 7
L0_DIRECT_MUTATIONS_CAUGHT   = 7
L0_DIRECT_MUTATIONS_VACUOUS  = 0
```

**Every boundary this task names is guarded by a test that fails when the boundary is broken.** The
guards are not decorative.

### A correction to my own first pass, recorded rather than hidden

The first pass reported `M1 canonicalisation removed → GREEN (NOT CAUGHT - vacuous)`. That was **my
harness's fault, not the tests'**: the `-run` filter was `TestMappedAddress|TestJudgeFlow`, and the
tests are named `TestMappedDnsAddressIsHijacked`, `TestMappedFakeIPIsStillGuardedFromTheRouteSets`,
`TestMappedDestinationReachesTheRouteSets` — none of which contains `TestMappedAddress`, so the
filter matched only one unrelated test and the mutation looked uncaught. Re-run against the whole
package, it is caught by **four** top-level tests. Left in this report because "the filter matched
nothing so the boundary looks unguarded" is precisely the vacuity failure this task exists to catch,
and it nearly produced a false accusation against the tests.

---

## 3. Boundary-by-boundary results

```text
DNS_BYPASS        = PASS
```

Configured DNS address is checked **before** the route sets (`inbound.go:1053`), on **every** port,
for both transports: UDP → `ActionHijackDNS`, TCP → `ActionAccept`. A route set that would otherwise
bypass the address cannot, because the guard returns first. Verified by M2 (RED) and by
`TestDNSHijackBeatsRouteExcludeAddressSet`, `TestDNSHijackAddressBeatsRouteAddressSetMiss`,
`TestDNSHijackByPortBeatsRouteAddressSetMiss`. **`never ActionBypass` holds.**

```text
FAKEIP_BYPASS     = PASS
```

`routeSetsApply` (`inbound.go:1092`) makes **both** route-set branches inapplicable for a FakeIP
address, and it is evaluated after the DNS guards but before any bypass. A set covering
`198.18.0.0/15` — which the code comments note is not far-fetched — cannot hand a placeholder to a
platform that has no route for it. Verified by M3 and M4 (both RED) and by
`TestFakeIPIsNotBypassedByRouteAddressSet` / `...RouteExcludeAddressSet` /
`TestMappedFakeIPIsStillGuardedFromTheRouteSets`.

```text
ROUTE_SET_UPDATE  = PASS (by source reading + existing lifecycle tests)
```

`updateRouteAddressSet` rebuilds **both** slices under `routeAddressSetAccess.Lock`;
`JudgeFlow` reads **both** under `RLock` (`inbound.go:1073-1076`). A reader can therefore never see
the include slice from one generation beside the exclude slice from another — the mixed-snapshot
failure mode the brief names. Unchanged by this round, and covered by
`callback_lifecycle_test.go` and `scope_ownership_test.go`. **Not re-measured under `-race`; see §5.**

```text
V4_MAPPED_FAILOPEN = PASS
```

Canonicalisation is the **first** statement (`inbound.go:1050`), and it is load-bearing for all four
comparisons. M1 turns **four** top-level tests red (`TestL0AuthoritativeIPPolicyWinsBeforeReverseMapping`
plus the three `TestMapped*` tests). This was the brief's highest-priority bug class and it is the
best-defended one.

```text
GROUP_CHAIN_SAFETY = PASS (not applicable to L0)
```

No L0 path reaches group selection: `JudgeFlow` steps 1-5 act on an address, and `resolveOutbound`
(`route/route.go:247`) — the only thing that reads `Selected`/`SelectForFlow` — is reached from the
Router, which L0 returns before. A group is never consulted at L0, so "group selected direct" cannot
be over-promoted here.

```text
SOCKET_SEMANTICS  = PASS (not applicable to L0)
```

L0 does not consult an outbound at all; the only interpreter of socket semantics is
`CanBypass` / `adapter.BypassableOutbound`, which is L1. No new bypass path was added, so no
semantics can be lost by one.

---

## 4. Answering the three required questions

**1. Did this round actually increase L0, rather than calling L1 L0?**

**It did not increase L0 at all — and that is the correct outcome, now verified rather than asserted.**
The diff is documentation-only (`git diff --name-only -- '*.go'` is empty). The report's own reasoning
for why no safe expansion exists is confirmed independently in §2: the reason is not timidity, it is
that the rule walk is first-match-wins (`route/route.go:1191-1193`) while L0 sees one address and
therefore structurally cannot reproduce ordering, so promoting an explicit IP/CIDR direct rule would
shadow an earlier domain rule. No L1 concept was relabelled as L0.

**2. Is DNS/FakeIP bypassed by any new path?**

**No new path exists.** The previous round added no code, so there is no new path to bypass anything.
Independently of that, the pre-existing DNS and FakeIP guards are load-bearing and test-guarded: M2,
M3, M4 and M7 each break one and each turns named tests red. `never ActionBypass` for a configured DNS
address holds on every port and both transports.

**3. Is it worth moving to device validation?**

**Not for anything this round produced — there is nothing of this round to validate.** The L0
decision surface is unchanged, so a device run would re-measure pre-existing behaviour. Device
validation would be worthwhile for a *future* change that actually touches L0, and the case for that
change has not been made: §2 of the previous report's inventory stands, and its only semantically
eligible candidate is disqualified by rule ordering.

---

## 5. What is NOT verified

```text
RACE              = BLOCKED_BY_ENVIRONMENT
DEVICE_VALIDATION = UNVERIFIED
```

**`RACE`.** `-race` requires cgo on this host; the only C compiler is Swift's clang, which hands the
link to MSVC's `link.exe` and fails with `LNK1143: invalid or corrupt file: no symbol for COMDAT 0x6`.
Redirecting it to the bundled LLVM linker gets further and then fails on the linker script Go injects
(`lld-link: ignoring unknown argument '-T'` → `fix_debug_gdb_scripts.ld: unknown file type`). Three
attempts across earlier rounds. **Not claimed as PASS.** No concurrency-relevant code was changed in
either round, so nothing here depends on it — but the mixed-snapshot claim in §3 is a source-reading
argument backed by non-race lifecycle tests, not a race-detector measurement.

**`DEVICE_VALIDATION`.** No iOS/Android device or NetworkExtension run. Nothing in this audit needs
one: L0 returns its verdict to sing-tun, and the reachability argument rests on the profile's own
`includedRoutes`, which is platform configuration rather than code in this tree.

```text
PERF_REGRESSION = NO (nothing changed to regress)
```

No benchmark was run and none is meaningful: the previous round changed no code, so there is no
before/after pair. The brief's warning about "benchmark 测了 fixture" is respected by not producing a
benchmark at all rather than by producing one against this host.

---

## 6. Corpus limit, stated

Mutable verification is only as good as its mutation corpus. Seven mutations were tried, chosen to
match the bug classes the brief enumerates, and all seven were caught. That is evidence of strong
guarding **on those seven**, not proof that no unguarded boundary exists elsewhere in `JudgeFlow` —
an eighth mutation outside this corpus could still survive. The corpus is listed in §2 so its coverage
is inspectable rather than implied.

---

## 7. OUT_OF_SCOPE_FINDING

Recorded, not fixed, per the brief's §2.

**`KNOWLEDGE/BASELINE.md` is stale and now materially wrong.** It records the post-baseline delta as
"none. `v0.1.6..HEAD` is 5 commits and **no `.go` source changed**", so that the modules still
describe the tree. The delta is now **71 commits**, and production Go under `route/` has changed
(`route/route.go` +45, `route/router.go` +16, new `route/fast_path.go` +149). The `route/` module
descriptions therefore no longer describe `HEAD`, and a reader who trusts that line — as AGENTS.md
rule 4 authorises them to — would be misled. `protocol/tun/**`, `common/dialer/**` and the DNS/FakeIP
paths are still unchanged since the baseline, so this does not affect the L0 conclusions above.

Not fixed here because AGENTS.md rule 5 and §7.4 both forbid refreshing the knowledge base as part of
normal development: a refresh is an explicit maintenance operation with its own protocol
(`KNOWLEDGE/REFRESH.md`).

---

## 8. Git state

```text
branch                 perf/l0-direct-coverage  (testing NOT modified; still dfdd71e71)
production code        unchanged by this round
mutation targets       restored; blob 90b7824d… identical to HEAD
working tree           only the two pre-existing submodule entries
tag                    not moved
release                not created, no assets published
```

Report: `L0_DIRECT_COVERAGE_DEBUG_REPORT.md`
