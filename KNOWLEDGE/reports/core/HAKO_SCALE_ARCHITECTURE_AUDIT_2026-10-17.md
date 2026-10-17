# HAKO / ProjectClash scale audit — what a large configuration actually costs here

**Tree:** `testing` at `42222237dca0a6f510c3c2cea50bdd5b736f8846` (the round's `BASE`), Windows
amd64, go1.26.8 toolchain against this module's `go 1.25.5`, default `GOGC`, no `GOMEMLIMIT`.

**Date:** 2026-10-17.

**Scope:** a reading of the ProjectClash "100k nodes / 2M rules" work — streaming config decode,
compact immutable rule indexes, static-description-vs-runtime-resource separation, lazy provider
catalogues, bounded background probes — turned into measurement of *this* fork, plus one production
change that the measurement justified.

## Evidence levels

| Claim | Level |
|---|---|
| Every table row below | `VERIFIED_MEASURED` — produced by a test in this tree, with the command in the row's section |
| The outbound and background-worker inventories | `VERIFIED_SOURCE` — file:line readings, no build (static audit round) |
| The production change | `VERIFIED_UNIT`; the test was proved sensitive by a mutation, recorded below |
| Any Apple/NetworkExtension behaviour | **`UNPROVEN`** — no device, no signed build, no Apple toolchain in this environment |

Nothing in this report is `VERIFIED_CI`: this tree contains no workflow-run record, so a job
declaration is not evidence that the job ran.

---

## 1. The one-paragraph answer

The fork has already absorbed most of what the ProjectClash work is about. Domain and IP rule-sets
are stored and loaded as compact immutable structures — **8.7 bytes per domain live, 10.9 bytes per
prefix live at 100k** — with no `[]string` of the list at any point; the heavy outbound families are
the only ones that hold runtime resources before first dial; and there is already a sleep authority
(`common/power`) plus a per-box probe budget. What is *not* absorbed is (a) the **load transient**,
which is 4–8× the steady structure and lives almost entirely inside the compressed-stream decode, and
(b) **two background network workers that answered to no power state at all**. This round measured (a)
and closed (b) — both workers, each with a sensitivity-proved test — and refused the rest with reasons.

---

## 2. The baseline object-cost table

The measurement is a committed test, not a scratch benchmark:

```sh
go test ./route/rule/ -run 'TestRuleSetScaleCensus$' -v
SING_BOX_SCALE_CENSUS=1 go test ./route/rule/ -run 'TestRuleSetScaleCensusLarge' -v
go test ./route/rule/ -run 'TestRuleSetLoadStageBreakdown' -v
```

Fixture: one default rule holding a `domain` list or an `ip_cidr` list, encoded to a version-5
binary rule-set (`.srs`) and loaded through `NewLocalRuleSet` — the production path a `rule_set` in a
config takes, not a matcher in isolation. Domain labels are short near-random host labels sharing 64
registered domains × 8 TLDs, so the reversed trie is realistically compact; prefixes are scattered
across the whole address space so the range table is the size of the list (a `10.0.1.0/24,
10.0.2.0/24, …` fixture is normalised into a handful of ranges and understated the loader 15× when
this file first ran — see the generator's own note).

`live` = reachable heap after two forced collections with the set loaded. `load peak` = **total
allocation of the load**, measured with the collector disabled for the duration, so nothing the load
allocates can be reclaimed before it is read. `reload peak` = the same figure for publishing a second
generation of the same size on top of the first; the old generation is already live and is therefore
*not* part of the delta, so the true heap during a reload is `live + reload peak`.

### Domain rule-sets

| entries | `.srs` on disk | steady live | load peak | reload peak | GC during load | match hit | match miss |
|---|---|---|---|---|---|---|---|
| 1 000 | 3.6 KiB | 37.6 KiB | 116.9 KiB | 115.8 KiB | 0 | 398 ns | 305 ns |
| 10 000 | 23.2 KiB | 119.0 KiB | 528.6 KiB | 527.6 KiB | 0 | 380 ns | 310 ns |
| 100 000 | 218.1 KiB | 887.2 KiB | 4.1 MiB | 4.1 MiB | 2 | 419 ns | 311 ns |
| 1 000 000 | 1.6 MiB | 8.3 MiB | **42.5 MiB** | **42.5 MiB** | 10 | 429 ns | 338 ns |

### IP-CIDR rule-sets

| entries | `.srs` on disk | steady live | load peak | match hit | match miss |
|---|---|---|---|---|---|
| 1 000 | 5.3 KiB | 13.0 KiB | 118.9 KiB | 67 ns | 58 ns |
| 10 000 | 49.7 KiB | 123.6 KiB | 792.9 KiB | 79 ns | 65 ns |
| 100 000 | 501.6 KiB | 1.0 MiB | **7.8 MiB** | 79 ns | 72 ns |

### What those numbers say

1. **The steady representation is already what the ProjectClash work is reaching for.** 8.7
   bytes/domain and 10.9 bytes/prefix at 100k, read from a succinct set and a sorted range table,
   both immutable once built. A million-domain set is 8.3 MiB live. There is no `[]string`, no
   `[]map`, no per-rule wrapper object — the v3+ format stores the matcher, and `srs.Read` reads it
   straight off the decompressed stream.
2. **Loading a million-domain set costs 42.5 MiB to produce 8.3 MiB: 5.1×.** On a 40 MiB
   NetworkExtension budget that single load is over the limit by itself, and a reload needs
   `live + reload peak` ≈ 50.8 MiB. This, not the steady heap, is the rule-set problem on Apple.
3. **Lookup cost is flat.** 380–430 ns per domain and 65–80 ns per prefix at every scale, so the
   compact representation did not buy memory by spending time.
4. **A rule-set does not cost a goroutine until it is started.** The census asserts that the
   goroutine count is unchanged across a load, at every size: the file watcher is constructed by
   `NewLocalRuleSet` and only started by `StartContext`.

### Cold nodes

Same instrument, one thousand `trojan` outbounds — the shape a 100k-node profile is actually made of:
a dialer, a TLS config, a transport object, and nothing eager. Constructed, started through all four
stages, never dialled (`go test -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run TestAConstructedNodeCostsNoThread .`):

| nodes | goroutines | live heap | per node |
|---|---|---|---|
| 1 000 | **3 → 2** (no growth) | +2 035 840 B | **2 035 B** |

That is the ProjectClash "static description, not a running protocol" claim, holding in this tree and
now pinned by a test rather than by an audit paragraph. A thousand configured nodes cost two megabytes
and zero threads; the five endpoint families that break this property are named in §3.

### Where the 5× goes

Same fixture, 10 000 domains, each stage measured with the same instrument:

| stage | allocation | owner |
|---|---|---|
| file bytes read | 27.0 KiB | this repository (`filemanager`) |
| inflate alone (`zlib` → `io.Discard`) | 52.8 KiB | the zlib decoder's window; fixed cost |
| `srs.Read` → rule options | **518.1 KiB** | `common/srs` decode + the sing module's matcher decode |
| options → rule objects | **1.6 KiB** | this repository (`NewHeadlessRule`) |
| steady live | 23.9 KiB | — |

Of the 528.6 KiB that load allocates, 52.8 KiB is the inflate floor (measured by deflating the same
payload with nothing else running) and **465.3 KiB is building the structure** — and the layer this
repository owns for turning options into rules costs 1.6 KiB of it. The mechanism is visible in the
source and is the same in both halves: no array on that path is pre-sized. `varbin.ReadSliceCount`
grows a nil slice **one element at a time** (`var result []T` then `slices.Grow(result, 1)` in a
loop, sing module), and `common/srs/ip_set.go` appends into two nil slices for a count it already
knows. Geometric growth costs roughly 2–5× the final array, on three arrays per matcher plus the
range tables, which is exactly the 4–8× observed.

**Consequence for the next round:** the domain half of this cannot be fixed in this repository — it
is the sing module's decode loop — and the existing iOS-only mmap path already sidesteps it entirely
by slicing the file instead of growing arrays. See §5 for why that path is *not* simply switched on
everywhere.

---

## 3. What the outbounds actually cost before they are used

Static audit of `protocol/*`, `transport/*`, `common/dialer`, `common/tls` (`VERIFIED_SOURCE`, every
claim carries file:line in the audit round; the load-bearing frame is
`adapter/outbound/manager.go:600` — every configured outbound's constructor runs at config load — and
`adapter/outbound/manager.go:538` — **every** outbound is `Start`ed through all four stages whether or
not it is ever dialled. "Never used" here means "constructed *and* started").

| family | before first dial | class |
|---|---|---|
| **wireguard** | gVisor stack + `wireguard-go` `device.NewDevice` = **3×NumCPU+2 goroutines** immediately, then 1 bound UDP socket + a bind-receive goroutine at Start | A+B+C |
| **tailscale** | the whole `tsnet.Server` stack at `PostStart`, **including dialling the coordination server** | A+B+C |
| **openvpn / openconnect** | gVisor stack at Initialize, then a **live VPN session** + 2–3 goroutines at PostStart | A+B+C |
| **masque** | gVisor stack + a live QUIC CONNECT-IP session | A+B+C |
| **tor** | a child process + a local listener + a control socket | A+B/C |
| **urltest / loadbalance with `url`** | 1 goroutine at start **and one HTTP probe per member** | A+B |
| selector, shadowtls, dns, block, socks, anytls, hysteria, hysteria2, tuic, ssh, http, vmess, vless, trojan, shadowsocks, snell | **0 goroutines, 0 fds, 0 timers** — a heap struct of order KB | A |

Repo-wide, **exactly one per-node ticker exists in `protocol/`** (`protocol/group/urltest.go:623`) and
it is created on first `Touch()` and self-reclaimed after `idleTimeout`. No outbound constructor or
`Start` opens a socket except the five endpoint families above.

So the ProjectClash premise "a node in the config must not be a node's worth of runtime" is **already
true for the node kinds a 100k-node profile is actually made of**, and the remaining answers are the
five endpoint families and the eager URL-test round. Those are named in §7 as next-round work rather
than attempted here: each one moves a *start-time configuration error* (a `listen_port` that cannot
bind, a `validateTunnelMTU` failure, a `interval > idleTimeout` rejection) into a first-dial failure,
which this round's brief forbids, and none of the five can be verified on Windows at all.

---

## 4. What the background workers do, and which of them answer to sleep

Static inventory of every long-lived worker in the core (`VERIFIED_SOURCE`, full table with file:line
in the audit round). The four conclusions that mattered:

1. **One deliberate success already exists.** Rule-set refresh is **one** goroutine and **one** timer
   for *all* remote sets (`route/rule/rule_set_updater.go`), the next deadline is the minimum over
   per-set intervals, and every non-initial pass blocks on `powerGovernor.WaitProviderRefresh`. N
   rule-sets cost one worker. That is the pattern the rest of the tree should follow.
2. **Per-group, not per-node.** URL-test health checking costs one ticker + one goroutine *per group*,
   with a per-round batch capped at 10 and a per-box admission coordinator of 10
   (`common/urltest/coordinator.go`, `C.URLTestConcurrencyLimit`). 100 000 nodes do not produce
   100 000 goroutines: the fan-out is capped twice over.
3. **No unified budget, and that is tolerable.** Ten disjoint pools exist (URL-test coordinator, batch,
   rule-set `task.Group(5)`, `dialGovernor(24, 3 s window)`, per-connection `trafficsched`, per-transport
   DNS `ConnPool`, the pause callback list, `power.Governor`, `runtimecoord`, the daemon subscriber
   rings). Inventing an eleventh — a global governor over all of them — was considered and **refused**:
   none of the existing pools can be expressed in another's terms, and `route/dial_governor.go` shows
   how much state a budget needs before it earns its keep.
4. **Two network workers answered to no power state at all**, and they were the real gap. Both are
   fixed in this round:
   - `dns.client.backgroundRefreshDNS` — an optimistic cache renewal, one round trip per stale entry,
     zero references to `power`/`pause`/`Allow()` in the whole `dns` package. See §5.1.
   - `service/api` dashboard refresh — a periodic download with no gate, on a timer that fires whether
     or not anybody is looking. See §5.2.

   Both were verified by inspecting the packages for *any* power reference rather than by reading the
   call sites, because the failure mode here is a worker that never asked — not one that asked wrong.

   Two more are gated but in a band that does not stop traffic: URL-test health checks are parked only
   by `pause.RegisterTicker` (a *ticker* mechanism — invisible to `time.AfterFunc`), and the power
   governor's own `Quiescent` policy deliberately still permits health checks and probes. Neither is a
   defect: both are liveness, and the policy comment says so.

---

## 5. Direction: adopted

### 5.1 The optimistic DNS refresh now obeys the power lifecycle

`dns/client.go`, `dns/optimistic_refresh_power_test.go`. Commit `fix(dns): …`.

| question the brief asks | answer |
|---|---|
| 1. what was the original problem | a stale-but-servable cache hit schedules a background renewal; nothing consulted the sleep state, so a sleeping device woke the radio to renew a record nobody was resolving, once per stale entry |
| 2. what is the measurement | the unit test counts transport round trips: **1 → 0** with the device paused, and back to 1 after a wake (`VERIFIED_UNIT`, and the suppression assertion was proved sensitive by mutation — see below) |
| 3. why the current architecture produced it | `backgroundRefreshDNS` is the one worker in `dns` that dials; the package resolved `NetworkManager` at `Start` and no power concept at all |
| 4. what HAKO inspired it | the "background work must be admission-controlled and sleep-aware" half of the brief; the *bounded concurrency* half was already met |
| 5. why this implementation | it reuses the fork's existing authority (`service.FromContext[*power.Governor]`, registered in `box.go`) instead of adding a listener, a queue or a second lifecycle, so there is no new owner to keep consistent |
| 6. who creates it | the governor is created by `box.New`; the client only *reads* it — once, in `NewClient`, before the client is shared, so the field the refresh goroutine reads is immutable. `Start` was the first attempt and the debug audit moved it: see `HAKO_SCALE_DEBUG_AUDIT_2026-10-17.md`, "Bugs found" 1 |
| 7. who closes it | nobody new. A nil governor is a supported state and preserves the previous behaviour byte-for-byte, which is what the CLI and every non-Apple caller have |
| 8. reload / transition / sleep | reload: the governor is fresh per Box, so a replacement Box gets its own and the seeding from the held pause level is the daemon's existing behaviour. Transition: unchanged — the pre-existing ownership and `startedStable` guards still run first. Sleep: suppressed; wake: released, through the governor's own stagger |
| 9. failure degradation | the refresh is skipped and the entry is left stale-but-servable. The forward path is untouched, so a suppressed refresh costs freshness and never correctness — the rule `familyHealth` established for every cache in this tree |
| 10. what the tests prove | three tests: a control that the refresh does run while ACTIVE (so the suppression test is not vacuous), the property under pause **plus the recovery after wake**, and a compatibility test that a client with no governor behaves exactly as before |

**The predicate, and why the two obvious ones are wrong.** `Allow().NetworkProbe` is *true* in
`StateQuiescent`, and `recomputeLocked` puts the governor there for the whole of a sleep — a
NetworkProbe gate would read as protection and be none. `Active()` alone is false in `StateWaking`
too, which is the band where the governor is deliberately letting categories go one at a time. So:
Quiescent and DeepIdle are hard refusals, the waking band defers to `Allow().NetworkProbe`, and
anything else defers to `Active()` (which also covers a closed governor, whose own documentation says
it must not authorise work).

**Sensitivity, measured.** With the gate defeated (`if false && c.backgroundRefreshSuppressed()`),
`TestOptimisticRefreshIsSuppressedWhileTheDeviceIsPaused` fails with `expected: 1, actual: 2` — one
extra round trip, which is the defect. Restored, all three pass. That mutation is the reason to
believe the test rather than the comment.

**What this does not do:** it does not cap concurrency (there is nothing to cap — one refresh per key,
`LoadOrStore`-guarded), it does not touch the forward path, and it does not change any timer cadence.
A refresh still fires while the device is awake, however many stale entries there are; that bound is
the cache size, which is already capped.

### 5.2 The dashboard updater now waits for the same window

`service/api/dashboard.go`, `service/api/dashboard_power_test.go` — the second of the two ungated
network workers, and the last one: after this the only periodic **network** workers in the core are
the rule-set refresh and this, and both defer to the governor.

It waits rather than skips, which is the difference from 5.1 and is deliberate. The loop is one
goroutine that already exists, so blocking it holds no resource, and waiting makes the download happen
the moment the device wakes instead of up to a full interval later. `WaitProviderRefresh` is the
governor's own category for a provider-shaped download, so no predicate had to be invented.

**The first update is deliberately NOT deferred**, and that is pinned by its own test. On a fresh
profile the dashboard directory is empty, so that fetch is what fills the panel the operator is
looking at; deferring it to a wake would be a regression dressed as a saving. Same rule as the
rule-set refresh, different reason: that one is correctness, this one is the first paint.

**Sensitivity, measured.** With the gate defeated (`if false && !initial && …`) the suppression test
fails with `expected: 2, actual: 11` — nine downloads inside a 200 ms window on a sleeping device.
Restored, all three pass.

### 5.3 The cold-node property is now a test, not an audit paragraph

`outbound_cold_cost_test.go` (root package). The audit in §3 concluded that a light node holds no
runtime resources; that conclusion was static, and a static conclusion decays the moment somebody adds
a `go func()` to a constructor. A thousand `trojan` nodes — constructed, started through all four
stages, never dialled — cost **0 goroutines and 2 035 bytes each**.

The five endpoint families that break the property are deliberately NOT asserted: asserting their
current cost would ratchet behaviour the next round is meant to remove.

**Measurement correction, recorded because it almost shipped as a number.** The first version reported
`+10 464 bytes, 10 per node` — a thousand trojan outbounds at ten bytes each, which is obviously
impossible. The compiler was free to treat the node slice as dead after its last syntactic use, so the
collection inside the heap reading reclaimed all thousand nodes before measuring them;
`runtime.KeepAlive` after the reading is what makes the number mean what it says. A census that
reports a number nobody sanity-checks is worse than no census.

---

## 6. Direction: examined and refused

Each of these was on the brief's list, is implementable, and was refused with a reason. A refusal with
a measurement is a result.

1. **A compact/mapped representation for rule-sets.** Not needed: the representation is already
   compact (8.7 B/domain live) and immutable. What is left is the *load transient*, which is not a
   representation problem — §2 shows 465 of the load's 528 KiB goes into building the structure, and
   the arrays that grow are written by `varbin.ReadSliceCount` in the sing module, so an alternative
   format in this repository cannot remove it.
2. **Widening the existing mmap path past iOS.** `common/srs/mmap.go` + `domain.Mmap` already
   implement the whole idea: the file is mapped read-only, the succinct set and the IP ranges are
   *sliced* out of the mapping, and nothing is grown. It is enabled only for `ios`, and the reason is
   in the platform file: `mmap_unix.go` unmaps through `unix.Munmap` and unlinks the temp file
   immediately after mapping, which is what makes the lifetime trivial. Windows cannot delete a mapped
   file, so the same code would leave one temp file per rule-set per generation on disk, and there is
   no way to verify the Apple path from this environment. Refused as an unverifiable lifecycle change,
   not as a bad idea.
3. **Deferring WireGuard / Tailscale / OpenVPN / OpenConnect / MASQUE device creation.** The largest
   single win in the inventory (3×NumCPU+2 goroutines and a UDP socket per idle WireGuard node) and
   the one this round could not verify: it needs a real TUN and a signed Apple build, and it moves
   `listen_port` binding and MTU validation from Start to first dial, which the brief forbids without
   evidence. Named as next round's first item.
4. **Deferring the URL-test initial round.** N groups × M probes at boot is real. Refused because the
   round's *configuration-validity* checks deliberately live at construction (`interval < 0`,
   `interval > idleTimeout`, `ParseMeasurementTarget`) with in-source rationale, and because a group
   with no measurement ranks by min-delay over empty history, i.e. it silently picks the first member
   on the first flow. Both are semantic changes, not optimisations.
5. **A global background-work governor.** Refused: ten disjoint pools exist, none expressible in
   another's terms, and the one existing budget (`dialGovernor`) needed 24 slots, a 3-second window
   and a soft deadline to be useful. A global one would be a new lifecycle with no measurement behind
   it.
6. **Streaming top-level config decode (the YAML-section idea).** Not measured yet on this tree: the
   brief ranks it last and requires evidence first, and no measurement in this round showed the
   config parse path to be a problem. Left to a round that measures it.
7. **Pre-sizing the IP range slices in `common/srs/ip_set.go`.** Implementable and refused: the 4/6
   split of the entries is not known until they are read, so pre-sizing both to the total costs
   40 bytes/entry instead of the ~8–32 the data needs — worse than the growth it removes.
8. **Apple goroutine start-stack tuning.** Out of scope by the brief's own ordering, and there is no
   Apple memory benchmark in this repository to justify it.

---

## 7. Known risks

- The DNS gate changes *freshness*, not correctness, but it does change behaviour: a device that sits
  paused for hours will hold staler entries, and the first query after a wake may pay one upstream
  round trip it would previously have pre-paid. That is the intended trade and it is not quantified on
  device (`UNPROVEN`).
- The dashboard gate changes *when* a panel updates, not whether it works: the files on disk keep being
  served, so a paused device shows the previous dashboard until the wake. On a device that stays paused
  for days the panel can be visibly old. That is the intended trade and it is not quantified on device
  (`UNPROVEN`).
- The waking band is exercised with a 20 ms stagger in the dashboard test and 50 ms in the DNS test,
  against a production 15 s stagger. The *ordering* is proved; the *duration* is a policy value that no
  test pins.
- Both suppression tests use a bounded window for the negative half (200 ms of "nothing happened"). The
  control test in each file is what makes that window a measurement rather than an absence of evidence —
  it proves the same fixture lands several updates in a window of that length when the gate is open.
- The census bounds in `TestRuleSetScaleCensus` are deliberately loose (100 bytes/domain against a
  measured 12). They fail when the representation changes shape, not when it changes by a byte.
- The census's `load peak` disables the collector for the duration of one load. That is bounded by one
  fixture (42.5 MiB at a million domains) and restored by a `defer`; it must not be reused around
  anything larger without re-reading that helper.

## 8. Next round

In the order the measurements justify:

1. **WireGuard `Initialize`/`Start` deferral and OnDemand suspend-to-release** — the biggest recoverable
   block (3×NumCPU+2 goroutines, 1 fd, a gVisor stack per idle node), and the suspend path currently
   releases only what `Down()` releases: the crypto workers and the netstack stay. Needs a real TUN;
   cannot be done from Windows.
2. **The rule-set load transient on the non-iOS platforms** — one of: a sing-module pre-sizing fix, or
   widening the mmap path with a per-platform lifetime story. 42.5 MiB → 8.3 MiB is the size of the
   prize for a million-domain set.
3. **The URL-test initial round at boot**, once the "config errors stay at Start" boundary is written
   down as a rule for this subsystem rather than argued per change.
4. **A config-parse measurement**, so §9 of the brief can be answered with a number instead of a
   preference.

## 9. Reproducing everything here

```sh
# the census that produced §2 (CI-sized rows; ~0.6 s)
go test ./route/rule/ -run 'TestRuleSetScaleCensus$' -v
# the stage breakdown that localises the transient
go test ./route/rule/ -run 'TestRuleSetLoadStageBreakdown' -v
# the 100k / 1M sweep (minutes, hundreds of MiB of fixtures; not part of make test)
SING_BOX_SCALE_CENSUS=1 go test ./route/rule/ -run 'TestRuleSetScaleCensusLarge' -v
# the two production changes
go test ./dns/ -run 'TestOptimisticRefresh' -v
go test ./service/api/ -run 'TestThe.*Dashboard' -v
# the cold-node census (root package: needs the physical tags)
go test -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run TestAConstructedNodeCostsNoThread . -v
# the suites this round re-ran in full
go test ./dns/ ./route/rule/ ./service/api/ -count=1
go test -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" . -count=1
```
