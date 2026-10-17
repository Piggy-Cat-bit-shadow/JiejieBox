# HAKO scale round — adversarial debug audit

**Tree:** `testing` at `e092ecec73d` (the construction round's head), `BASE` = `42222237dca`.
Windows amd64, go1.26.8, default `GOGC`. Date 2026-10-17.

**Subject:** only what the construction round introduced, and only its boundaries with the systems it
touches. That round was six commits over eight files: two production changes (two sleep gates) and two
censuses. This audit adds three — one hardening, two test groups — and no new behaviour beyond the
hardening.

---

## Scope

**Checked, in full:**

| area | what was checked |
|---|---|
| `dns.client.backgroundRefreshDNS` + the sleep gate | ownership of the new field, ordering of the read vs the goroutine that uses it, every governor state including **closed**, the in-flight registry under refusal, the compatibility path with no governor |
| `service/api` dashboard loop + the sleep gate | the same, plus the first-update exemption, the teardown path (a loop parked inside the governor when its owner closes), and the factory wiring |
| `route/rule` census | that it measures the production load path, that its fixture is representative, that it leaks nothing |
| `outbound_cold_cost_test.go` | that its census can see what it claims, that the fixture is a real outbound lifecycle |
| hot paths the changes sit next to | `BenchmarkDNSExactCacheHit`, `BenchmarkDNSNXDomainNameCacheHit`, `BenchmarkDNSCacheMiss`, `BenchmarkLookupReverseMapping*`, A/B against `BASE` |

**Not checked, and not applicable — this round introduced none of them:**

`config`/parsing streaming · compact or mmap rule-set representation · lazy outbound runtime ·
idle reclaim · provider/catalog state · probe admission · Apple-only runtime tuning.

The book's checklists for those areas (its §§3, 4, 5, 6, 7, 8, 9, 10, 11) are therefore **not
satisfied by this round — they are untouched by it.** The construction round measured that this tree
already has a compact rule-set representation and already-lazy light outbounds, and it *refused* to
add lazy runtime, mmap or a probe scheduler. Nothing in that refusal is re-litigated here; the
measurements behind it are in `HAKO_SCALE_ARCHITECTURE_AUDIT_2026-10-17.md`.

**Also not checked, and cannot be here:** `-race` (no C toolchain), any Apple/NetworkExtension
behaviour (no device, no signed build), anything requiring a real TUN.

---

## New invariants

These are the rules this round adds. Each has a test that fails if it is broken, and each test was
proved sensitive by mutation.

1. **A speculative background refresh does not run while the device is suppressed.** `Quiescent` and
   `DeepIdle` are hard refusals; the waking band defers to the governor's own `Allow().NetworkProbe`
   stagger; anything else (including a **closed** governor) defers to `Active()`.
2. **A refused refresh leaves nothing behind.** The in-flight registry entry is deleted on every path,
   including the refusal path, so a device that stays asleep does not accumulate one dead key per
   stale entry it touches.
3. **A periodic download waits for a window rather than skipping one.** The dashboard loop parks on
   `WaitProviderRefresh` and resumes on the wake; it does not sleep through a whole interval.
4. **The first pass is never deferred.** Both workers exempt their first run — rule-set refresh
   because routing needs the rules, the dashboard because the panel is empty until it lands.
5. **A field read by a background goroutine is resolved at construction, not in `Start`.** Written once
   before the object is shared, it needs no ordering argument; assigned in `Start`, it would.
6. **A configured-but-undialled light node costs no thread.** Pinned at 1 000 nodes: 0 goroutines,
   2 035 bytes each.

---

## Bugs found

**No defect reached production behaviour.** Five candidates were examined; one structural weakness was
removed. Each item below states what was actually found, not what was feared.

### 1. `Client.powerGovernor` was written in `Start`, in an object already shared — REMOVED (hardening)

**Trigger:** none today. **Root cause:** the field is read by the refresh goroutine, and it was
assigned in `Client.Start()`, which runs after the router already holds the client. The correctness
argument was temporal — "`Start` runs before any query, and `initializeMemoryCache` in the same
function writes `c.cache`, so the client already requires Start-before-query" — and that argument is
true and load-bearing, but it is exactly the kind of ordering assumption that is cheap to remove and
expensive to re-derive.

**Fix:** resolved once in `NewClient` from the service registry, so the field is immutable for the
object's life. The constructor dependency this creates (`box.New` registers the governor at `box.go:346`
and builds the DNS router at `box.go:569`) is named in the code and pinned by
`TestTheClientResolvesTheGovernorAtConstruction`, which fails if the read moves back into `Start`.

**Evidence:** mutation E below. **Not a race-detector finding** — see Remaining risks.

### 2. The dashboard gate could have been inert and invisible — PRE-EMPTED

`newDashboard` reads the governor from the service registry. If that read had been wrong (or removed,
or the registration reordered), the gate would be a no-op while every test that *hands the dashboard a
governor* kept passing. This is the failure mode the book warns about under "inert code": a field
nobody fills passes every test that does not look at the field.

**Fix:** `TestTheServiceResolvesTheGovernorFromItsContext` drives the real `NewService` factory and
asserts the field, both wired and unwired. Mutation D proves it is discriminating.

### 3. A refused refresh could have leaked its in-flight key — PRE-EMPTED, pinned

`backgroundRefreshDNS` guards one refresh per cache key with `LoadOrStore` and deletes in a `defer`.
The gate is placed *after* the `defer`, so the refusal path still cleans up. That ordering is the
whole invariant and nothing tested it: moving the gate above the `defer` (mutation B) leaks 100 keys
and blocks those keys from ever refreshing again.

**Fix:** `TestASuppressedRefreshLeavesNothingBehind` schedules 100 refused refreshes and requires the
registry to drain, reporting the count it found.

### 4. A parked dashboard loop could have outlived its owner — VERIFIED CLEAN, now pinned

The loop blocks inside `WaitProviderRefresh` for the whole of a sleep. `common/power.Close()` closes
`stateChanged`, so a parked waiter wakes, sees `closed`, and `waitFor` returns false; the loop then
returns. That path was correct and untested. `TestTheDashboardUpdateLoopExitsWhenItsOwnerCloses`
asserts the loop returns and issues no fetch on the way out; mutation C (treating the refusal as
"try again") makes it fail.

### 5. The cold-node census reported a number that was obviously impossible — FIXED during the round

Recorded here because it is the shape of defect this audit exists to catch, and it was in a *test*:
the first version measured `+10 464 bytes, 10 per node` for a thousand trojan outbounds. Two causes,
both now fixed and commented: the heap baseline was taken after construction, and the compiler was
free to drop the node slice before the heap reading, so the collection reclaimed all thousand nodes
first. `runtime.KeepAlive` is what makes the number mean what it says. The audit's check was simply
asking whether 10 bytes could possibly be right.

---

## Concurrency audit

| window | verdict | evidence |
|---|---|---|
| DNS gate read vs refresh goroutine | **clean by construction**: field written once in `NewClient`, before the client is published | `TestTheClientResolvesTheGovernorAtConstruction`; mutation E |
| dashboard field read vs loop goroutine | **clean by construction**: written in `newDashboard`, the goroutine is started later by `Start` | `TestTheServiceResolvesTheGovernorFromItsContext` |
| refresh refused vs refresh in flight for the same key | **clean**: one `LoadOrStore` per key, deleted on every path | `TestASuppressedRefreshLeavesNothingBehind`; mutation B |
| dashboard loop parked vs owner `Close` | **clean**: `stateChanged` closed by `Close`, waiter returns false, loop returns | `TestTheDashboardUpdateLoopExitsWhenItsOwnerCloses`; mutation C |
| governor state read vs concurrent transitions | inherited from `common/power`, which owns it under one mutex; this round only *reads* | `go test ./common/power/` |
| init vs close, reload vs callback, transition vs capture | **not applicable** — this round added no lazy initialization, no callback registration and no generation-bound state | — |

Forced interleaving rather than scheduler luck, as the book requires: the dashboard teardown test
parks the loop inside the governor *deterministically* (sleep, then Close), and the residue test joins
on the registry rather than on a timer.

## Lifecycle audit

| object | creator | owner | start | first use | idle | close | reload | failure rollback | background workers | OS resources |
|---|---|---|---|---|---|---|---|---|---|---|
| `dns.Client.powerGovernor` | `box.New` | **the Box**, via the registry; the client holds a non-owning reference | not started here | read by the refresh goroutine | — | nothing: a closed governor reports `Active()==false` and the gate refuses | a reload builds a new Box, therefore a new governor; the old client does not survive it | nil governor is a supported state | none | none |
| `dashboard.powerGovernor` | `box.New` | same | — | read by the update loop | — | `WaitProviderRefresh` returns false on a closed governor and the loop returns | same | nil governor is a supported state | none | none |
| the refresh goroutine | `backgroundRefreshDNS` | the client's context | one per cache key | — | — | client ctx cancel | ctx is per Box | — | 1 short-lived goroutine per key, capped by the cache size | none |
| the dashboard loop | `dashboard.start` | the service, via the scope | one per dashboard | — | parked on sleep | `scope` → ctx cancel | ctx is per Box | `start` returns the error and the scope rolls back | 1 goroutine | none |

No new object is created by this round. Both gates are **reads** of an object the Box already owned
before this round; neither adds a worker, a timer, a mapping, a socket or a goroutine.

## Resource audit

| resource | before | after this round |
|---|---|---|
| goroutines | 1 refresh goroutine per stale key, 1 dashboard loop | unchanged |
| timers | the dashboard's one `time.Timer` | unchanged |
| in-flight registry entries (`dns.backgroundRefresh`) | deleted on every path | still deleted on every path, now tested (100 refusals → 0 entries) |
| goroutines for 1 000 cold trojan nodes | 0 | 0 (measured, pinned) |
| goroutines for a 1M-domain rule-set load | 0 | 0 (measured, pinned) |

## Scale verification

From the construction round, unchanged and re-run this round:

| scale | result |
|---|---|
| 1M domain rule-set | 8.3 MiB live, 42.5 MiB load, 0 goroutines |
| 100k prefix rule-set | 1.0 MiB live, 7.8 MiB load |
| 1 000 cold nodes | 0 goroutines, 2 035 B/node |
| 1 000 nodes × 4 start stages | no goroutine growth at any stage |

The two changes of this round are O(1) per background operation and do not scale with node, rule or
key count.

## Compatibility

| promise | verdict | evidence |
|---|---|---|
| no governor → previous behaviour exactly | **holds** | `TestOptimisticRefreshIsUnchangedWithoutAGovernor`; the dashboard's nil path is the same one the CLI takes |
| config, formats, updater, outbound semantics, network strategy, sleep/wake, Clash mode | **untouched** — this round added two `if` statements and two fields, no format, no option, no default | `git diff BASE..HEAD` |
| hot-path cost | **no measurable change**: alternating A/B over three rounds on `BenchmarkDNSExactCacheHit` reads −2.4%, +1.2%, +1.4% (mean ≈ 0, noise band ±2.4%) on a 3 µs benchmark; the other DNS benchmarks moved −1% to −5% | see below |
| cross-platform build | Windows and the tagged root package build and test; no platform-specific file was touched | `go build ./...`, `go vet -tags … .` |

The A/B method, because a single delta on a 3 µs benchmark is noise: `git worktree` at `BASE`, the same
`-benchtime`/`-count`, three alternating rounds, means compared. The mechanism agrees with the
measurement — the gate executes only inside the refresh goroutine, which a fresh cache hit never
reaches (`BenchmarkDNSExactCacheHit` uses a 3600 s TTL).

## Remaining risks

1. **`-race` has never run on these changes.** `CGO_ENABLED=0` and no `gcc` on this host:
   `go test -race` fails with `-race requires cgo`. Every race claim above is therefore
   **`BLOCKED_BY_ENVIRONMENT`**, resting on construction-time immutability plus `-count=50` stress
   (`dns` 50×, `service/api` 50×, censuses 5×), not on the detector. This is the single largest gap in
   this audit and it must be closed on a host with a C toolchain before release.
2. **No device evidence.** That an Apple NetworkExtension actually stops waking for these two workers
   is `UNPROVEN`. The gate's behaviour is proved at the governor API; the battery or jetsam *benefit*
   is not measured anywhere.
3. **The waking band's duration is policy, not proof.** Tests use a 20–50 ms stagger against a
   production 15 s stagger. The ordering is proved; the number is not.
4. **The negative windows are bounded windows.** Both suppression tests assert "nothing happened for
   200 ms". The control test in each file is what makes that a measurement rather than an absence of
   evidence, but a refresh scheduled later than the window would pass.
5. **`session`-shared vs per-Box ownership of the governor** is inherited from the daemon and not
   re-verified here; the construction round recorded it, and `daemon/device_axis_reload_test.go`
   covers the reload seeding.

## Release verdict

**READY for this round's scope, with one blocking condition for the round's *evidence*, not its code.**

- The two production changes are correct as far as source, construction-time immutability, forced
  interleaving and 50× repetition can establish; five mutations confirm the tests discriminate.
- No new lifecycle, no new worker, no new resource, no format change, no hot-path cost.
- **Blocking for a release claim:** `-race` must be run once on `./dns/` and `./service/api/` on a host
  with cgo. Until then this audit's race conclusions are `BLOCKED_BY_ENVIRONMENT`, and the report says
  so rather than implying otherwise.
- **Not claimed:** any device-measured saving. The construction round's device gaps are unchanged.

The book's closing question — *can this run a month, hundreds of reloads, daily sleep/wake, and stay
bounded and correctly owned?* — is answerable in the affirmative for **what this round added**: two
`if` statements over an immutable reference to an authority the Box already owned, with no state of
their own. It is **not** answerable for the tree as a whole, and this round did not attempt to make it
so.

---

## Appendix: the five mutations

Each mutation was applied to production code, run against the test that is supposed to catch it, and
reverted. A test that cannot fail is not evidence.

| # | mutation | expected | measured |
|---|---|---|---|
| A | `backgroundRefreshSuppressed`'s `default` returns `false` instead of `!governor.Active()` | `TestOptimisticRefreshIsSuppressedWhenTheGovernorIsClosed` fails | FAILED, "a closed governor still reported its last state" |
| B | the gate moved **above** `defer c.backgroundRefresh.Delete(key)` | `TestASuppressedRefreshLeavesNothingBehind` fails | FAILED, "100 suppressed refreshes left their key in the in-flight registry" |
| C | the dashboard's `WaitProviderRefresh` refusal ignored instead of returned | `TestTheDashboardUpdateLoopExitsWhenItsOwnerCloses` fails | FAILED, "the update loop did not exit after the governor was closed" |
| D | `powerGovernor:` line removed from `newDashboard` | `TestTheServiceResolvesTheGovernorFromItsContext` fails | FAILED, "expected &power.Governor{…}, actual (*power.Governor)(nil)" |
| E | the DNS read moved back from `NewClient` into `Start` | `TestTheClientResolvesTheGovernorAtConstruction` fails | FAILED, same shape as D |

Two earlier mutations of the *original* gates were run when those landed and are recorded in their
commits: DNS 1 → 2 round trips, dashboard 2 → 11 requests in a 200 ms window.

## Appendix: commands

```sh
# every test this round added or touched
go test ./dns/ -run 'TestOptimisticRefresh|TestASuppressedRefresh|TestTheClientResolves' -v
go test ./service/api/ -v
go test ./route/rule/ -run 'TestRuleSetScaleCensus$|TestRuleSetLoadStageBreakdown' -v
go test -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run TestAConstructedNodeCostsNoThread . -v

# repetition, as a substitute for the race detector this environment cannot run
go test ./dns/ -run 'TestOptimisticRefresh|TestASuppressedRefresh' -count=50
go test ./service/api/ -count=50

# the A/B, against a worktree at BASE
git worktree add --detach /tmp/base 42222237dca
go test ./dns/ -run '^$' -bench 'BenchmarkDNSExactCacheHit$' -benchtime=200ms -count=10
```
