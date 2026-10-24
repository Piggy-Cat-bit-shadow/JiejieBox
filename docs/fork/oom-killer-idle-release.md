# OOM killer idle release — root cause and fix (2026-10-24)

**START_HEAD:** `07edc609388b1a2c6d6291735ac567b99869f4bf`
**FINAL_HEAD:** the commit carrying this report
**BRANCH:** `testing`

## 1. Confirmed root cause

`service/oomkiller/timer.go` released idle memory when:

```text
not below resume  &&  >=1s since last release  &&  (goroutines/connections shrank || no GC since last release)
```

`FreeOSMemory()` itself runs a GC and the code then stores the new GC cycle count. On a stable
footprint with no other GC, `idleGC` is true again on the next tick, so the condition is
self-sustaining: one `TrimMemory` + `FreeOSMemory` per second at 33 MB forever.

The canonical NetworkExtension policy makes 33 MB eligible for this path: `resume = 30 MiB` (so
`belowResume` is false at 33) while `armed = 40 MiB`; the resume hysteresis boundary was being used
as the everyday admission threshold. The logs (`33 MB -> 33 MB`, 31 of 33 consecutive samples one
second apart) are the direct symptom; they do not by themselves prove zero bytes were released,
which is why the fix is about the trigger, not about lowering the floor.

## 2. Fix

`releaseIdleMemory` now requires an **observed opportunity**:

- a meaningful drop in goroutines or connections since the previous sample, or
- a footprint drop of at least 512 KiB since the previous sample, or
- real pressure (`usage >= armed` in limit/NE mode, or `available <= armed` in available mode).

A completed GC is no longer a trigger at all. After a release with less than 512 KiB of measured
gain, a 30-second low-yield cooldown suppresses ordinary repeats; an armed/triggered footprint
bypasses that cooldown. The release now records the real byte delta and says "no measurable gain"
rather than unconditionally "released idle memory". The idle path joins the existing
`pressureCallbacks` barrier (Add while holding `access` after the `timer != nil` check), so
`Close`/`Reload` cannot return while a trim/GC is still touching the network; a `releaseAccess`
mutex makes the idle cleanup single-flight.

A second defect in the same package: `nextPressureState` left `Triggered` only when usage was
below `resume` (30 MiB). A process at a stable 33 MiB therefore stayed `Triggered` forever after one
reset, keeping the crisis pressure flag and the stronger release path armed. The exit condition is
now `armed` (40 MiB), matching upstream `9c2aa1bed32a`; `resume` remains the idle hysteresis floor
used by `belowResume`. Test: `TestPressureStateExitsTriggeredAtArmedNotResume`.

No change was made to `TrimMemory`'s downstream behaviour, to the critical `ReleaseMemory` path, or
to the policy thresholds. Upstream `9c2aa1b` removes the `resume` field from the state machine; that
is a different change and was not adopted here — the iOS crisis hysteresis is a safety contract, and
the bug was its misuse as an everyday admission gate.

## 2.1 Adversarial round (2026-10-24, after the first fix)

- **Double executor removed**: the first fix called `t.network.TrimMemory` and then an injected
  `trimMemory`; a test or future caller that set both would trim twice. The injected executor now
  wins, and the network manager is the fallback.
- **Single-flight**: `releaseAccess` serializes the whole decision + trim + GC. A second tick that
  sees an opportunity waits; it does not run a parallel `TrimMemory`/`FreeOSMemory`.
- **Close barrier**: the idle path joins `pressureCallbacks` (Add under `access` after the
  `timer != nil` check). `TestStopWaitsForAnInFlightIdleRelease` blocks the trim and proves
  `stop()` does not return until it finishes.
- **Race**: `go test -race ./service/oomkiller` passes with `CGO_ENABLED=1` and the mingw gcc.

## 2.2 Final closure round (branch oom-final-2026-10-24)

Two more defects were confirmed from the current source and fixed on the dedicated branch:

- **Stable armed footprint ran the idle loop every second.** `releaseIdleMemory` treated
  `usage >= armed` as a pressure override of the low-yield cooldown, so a process sitting at 41 MiB
  (armed 40, trigger 45) with zero benefit executed `TrimMemory + FreeOSMemory` every second. The
  idle path no longer considers pressure at all: it releases only on a measured opportunity
  (goroutine/connection drop or >=512 KiB footprint drop). Crisis handling at/above trigger stays in
  `poll`'s state machine. Test:
  `TestIdleReleaseDoesNotThrashAtArmedFootprint` (33 -> 41 stable, no second trim/GC).
- **Triggered `poll` cleanup was outside the stop barrier.** `poll` did destructive work after
  unlocking without joining `pressureCallbacks`, so `stop()` could return while an old poll was
  still resetting the network/publishing Critical. The triggered path now Adds under `access`
  (after the `timer != nil` check) and Dones after cleanup; `stop()` waits. Test:
  `TestStopWaitsForTriggeredPollCleanup` (blocking `cleanupHook`, stop must not return).
- **Log accuracy**: a release with 0 < delta < 512 KiB now says "gained only ... cooling down"
  instead of the unqualified "released idle memory".

`go test -race ./service/oomkiller` passes on this branch.

## 3. Tests

`service/oomkiller/idle_release_test.go`:

| test | assertion |
|---|---|
| `TestIdleReleaseDoesNotRepeatAtAStableFootprint` | five samples at 33 MB, no drop, no pressure → zero trims, zero forced GCs |
| `TestIdleReleaseRunsOnceForARealDropThenCoolsDown` | 35 → 33 MB → exactly one release; a second 33 MB sample is suppressed by the cooldown |
| `TestIdleReleaseDoesNotThrashAtArmedFootprint` | a stable 41 MB armed footprint is not an idle opportunity and causes no second trim/GC |
| `TestPressureStateExitsTriggeredAtArmedNotResume` | Triggered exits at 39 MB (below armed), stays at 41 MB; normal/armed entry unchanged |
| `TestIdleReleaseIsSingleFlight` | a second tick blocks behind the first trim; exactly one trim runs |
| `TestStopWaitsForAnInFlightIdleRelease` | `stop()` waits for the trim that already passed the active check |

`go test ./service/oomkiller` and `go test -race ./service/oomkiller` (CGO + mingw) pass. The
package's existing Darwin policy/lifecycle tests also pass unchanged.

## 4. Layered reclaim and lifecycle closure (2026-10-24, final round)

This round started from `489ed2c4c16` (the previous report's final commit) and treats the earlier
idle-trigger fix as closed. It adds the missing **lifecycle closure** and a real **layer decision**,
and fixes three defects that were reproduced against the starting tree before any change.

### 4.1 Newly confirmed defects (reproduced at the start SHA)

| defect | deterministic repro | why it was unsafe | fix |
|---|---|---|---|
| a non-Triggered `poll` published the pressure state AFTER releasing the lifecycle lock, outside `pressureCallbacks` | `TestNonTriggeredPollCannotPublishPressureAfterStop`: park the poll after its decision, run `stop()` and publish `None`, release | `Close` could return, `Service.stopTimer` publish `None`, then the old poll write `Warning` back onto a torn-down instance | every accepted poll now Adds under `access`, publishes while it still holds the lock, and Dones after telemetry; `stop()` waits |
| a Triggered cleanup did not share the idle path's serialisation | `TestCrisisReclaimWaitsForAnInFlightIdleTrim`: block idle `TrimMemory`, deliver a 46 MiB sample, observe that `releaseMemory` entered concurrently | `TrimMemory` walks the same pools that `ReleaseMemory` resets; one event could produce two forced GCs and a close racing a reset | one executor + `releaseAccess` held by **every** destructive action |
| the timer was re-armed before a slow cleanup, so a later poll could apply an older sample over the Triggered transition | `TestStaleObservationCannotMoveTheStateBackwards`: block the first sample, let the second apply 41 MiB, then release the first with 46 MiB | state machine could move backwards from evidence that predated the state already applied | per-poll observation sequence; older observations are dropped |
| the rate prediction re-armed Triggered on every poll while growth continued | `TestRateTriggerDoesNotResetAgainWhileTriggered` | one prediction could become a reset every minInterval for as long as growth continued | prediction is now an EDGE into Triggered, like the threshold |
| the first ordinary sample after a reset saw the reset's own drop as a fresh opportunity | `TestCrisisCoolsDownTheFollowingIdleDrop` | one crisis produced a second Trim + GC immediately after its own aggressive pass | post-crisis cooldown; the drop from the reset is not an opportunity |
| `NetworkManager.TrimMemory` walked endpoints and outbounds itself AFTER the router's reference-manager walk | `TestTrimMemoryDelegatesToTheRoutersSingleWalk` | every trim closed each pool twice; "reached once" was untestable and lock traffic doubled | one walk: the router's; local fallback only when no router exists |
| a platform `TrimMemory` and `ReleaseMemory` dispatched independently could interleave on the same pools | `TestReleaseMemoryWaitsForAnInFlightTrim` | a release could reset a pool while the trim was closing it | `memoryReclaimAccess` serialises the two MEMORY passes; network-transition resets stay independent on purpose |

The two starred repros (`TestNonTriggeredPollCannotPublishPressureAfterStop` and
`TestCrisisReclaimWaitsForAnInFlightIdleTrim`) were run against `489ed2c4c16` and **failed** there:
the first observed pressure `1` (Warning) after `None`, the second observed the crisis cleanup enter
while the idle trim was still blocked. They pass after the change.

### 4.2 One reclaim executor

```text
poll (observe, under access) ──┐
Darwin notifyPressure ─────────┼──► pending request (at most one) ──► worker ──► releaseAccess
crisis threshold/rate ─────────┘        idle < pressure < crisis              (one action at a time)
```

- `poll` still observes and transitions under `access`. It never performs a reclaim itself.
- A pending request is replaced by a more urgent one (`pressure` replaces `idle`, `crisis` replaces
  either) and the barrier token moves with it, so a burst of notifications cannot build a queue.
- The in-flight action is never interrupted - `CloseIdleConnections` has no safe cancellation - but
  the idle pass checks for a queued crisis at each LAYER boundary and returns early, so the
  destructive pass does not wait for a pool walk and a GC it does not need.
- `stop()` sets the stopped flag under `access`, **drops** queued requests (they have not touched
  anything), waits for in-flight work, then joins the worker goroutine. Dropping is the deliberate
  half: a Close must not begin a network reset for a service that is being torn down.
- `notifyPressure` no longer calls `releaseMemory()` and `poll()` inline. It queues the pressure
  action and accelerates the next observation by resetting the timer to `minInterval`, which removes
  the callback-vs-timer state-transition overlap and the duplicate GC.

### 4.3 The layers, as implemented and measured

| layer | action | entry point | reaches a socket? | test evidence |
|---|---|---|---|---|
| L0 observe | sample, state machine, telemetry, cooldown | `poll` | no | 1000 ticks at 33 MiB: 0 trims, 0 GC (`TestStableThirtyThreeMiBOverAThousandTicks`) |
| L1 cache | purge expired DNS exact cache (only when optimistic stale serving is off), expired NXDOMAIN verdicts, expired reverse mapping, 200 ms process memo | `NetworkManager.TrimCaches` -> `Router.TrimExpiredCaches` -> `dns.Router.TrimExpiredCaches` | no | `TestTrimExpiredCachesKeepsLiveAndRemovesLapsed`, `...PreservesOptimisticStaleEntries`, `TestRouterTrimExpiredCachesDelegatesToDNS` |
| L2 pools | `CloseIdleConnections` over outbounds, endpoints, DNS transports and the HTTP client pool | `NetworkManager.TrimMemory` -> `Router.TrimIdleResources` | yes, idle only | `TestTrimMemoryDelegatesToTheRoutersSingleWalk`, `TestTrimMemoryWithoutARouterClosesEachOwnerOnce` |
| L3 runtime | `FreeOSMemory` only when L1+L2 did not reach the 512 KiB floor AND (at/above armed OR `heap/free` >= floor) | `runIdleReclaim` | no | `TestIdleReclaimStopsAtTheCacheLayer`, `...EscalatesToPools...`, `...ForcesGCOnlyWhenEarlierLayersAreInsufficient`, `...SkipsGCWhenTheRuntimeHasNothingToReturn`, `...ForcesGCAtArmedEvenWhenRuntimeIsQuiet` |
| L4 crisis | `ReleaseMemory` (network reset) + cache flush + Critical + GC, re-checked against the live sample first | `runCrisisReclaim` | destructive | `TestStopWaitsForTriggeredPollCleanup`, `TestRepeatedTriggeredPollsResetOnce`, `TestQueuedCrisisIsRecheckedBeforeResetting`, `TestStopDropsAPendingCrisis...` |

The layer pass measures from one starting footprint and stops at the first layer that reaches the
floor. A cache trim that returns a megabyte therefore does **not** also close pools or force a GC;
a quiet cache trim falls through, and the GC still has to justify itself against pressure or the
runtime's own free-span figure.

### 4.4 Resource map: what the walks actually reach

Verified by source inspection; the load-bearing question is whether a release can terminate work
that is still carrying traffic.

| owner | what it holds | active/idle discriminator | release | decision |
|---|---|---|---|---|
| `dns.Client.cache` | exact DNS answers (`freelru`, per-entry lifetime) | optimistic stale answers are served inside `optimisticTimeout`; expired entries otherwise unreadable | `PurgeExpired` | L1, **skipped when optimistic is enabled** |
| `dns.Client.nxdomainCache` | name-wide negative verdicts | lapsed verdict is removed on read, never served | `PurgeExpired` | L1 |
| `dns.Router.dnsReverseMapping` | address -> domain | expired entry unreadable | `PurgeExpired` under `dnsEnvironmentAccess` | L1 |
| `Router.processCache` | process lookup memo, 200 ms lifetime | expired entry unreadable | `PurgeExpired` | L1 |
| `common/httpclient.Manager` | pooled connections behind provider/rule-set/dashboard/API | each transport's own `CloseIdleConnections`; manager copies the list and holds no lock while closing | `CloseIdleConnections` | L2 |
| HTTP/1.1, H2 fallback, H3 fallback transports | keep-alive / sessions | transport-specific idle check; `ManagedTransport` is `Reset`-aware (nil epoch => no-op) | `CloseIdleConnections` | L2 |
| DNS TCP/UDP/TLS/QUIC/H3 | serial/multiplexed query connections | QUIC: `sharedUsers == 0 && sharedWaiters == 0`; multiplexer: pool's own idle check | `CloseIdleConnections` | L2 |
| XHTTP XMUX (`transport/v2rayxhttp`) | multiplexed sessions | `getOpenUsage() == 0` | `CloseIdleConnections` | L2 |
| VLESS/VMess/Trojan/AnyTLS/SSH/ShadowTLS/SS | multiplex or stream pool | SSH: `streams > 0`; others delegate to the mux pool's idle check | `CloseIdleConnections` | L2 |
| Hysteria1/2, TUIC (forked `sing-quic`) | QUIC sessions | `streams == 0 && len(udpConnMap) == 0` | `CloseIdleConnections` | L2 |
| MASQUE endpoint | the tunnel itself | suspend/resume is a tunnel action, not a pool release | only `SetKeepIdleConnections` | **excluded**: a trim must not suspend the tunnel |
| NaiveProxy (`cronet-go`) | native H2/H3 client / fallback state machine | no idle-release API in the binding | none | **not reachable** without an SDK change |
| Mieru (`enfein/mieru`) | SDK-owned multiplexer | no idle-release API in the SDK | none | **not reachable** without an SDK change |
| FakeIP store (bbolt) | address -> domain mappings that clients keep | clearing would break an address a client is still using | none | **excluded** |
| persistent bbolt DNS cache | on-disk answers | its own cleanup loop owns expiry | none | **excluded**: disk I/O is not a memory pass |
| TUN / packet buffers / object pools | working set of the datapath | not idle ownership; "releasing" only re-allocates | none | **excluded** |

### 4.5 Intentional behaviour changes

- A queued reclaim is **dropped** at stop rather than executed. This is the only way to keep a
  network reset from running against a service that is closing. In-flight work still completes.
- `notifyPressure` no longer polls synchronously; the next observation is scheduled at
  `minInterval`. The pressure action and the observation are therefore ordered, not concurrent.
- `NetworkManager.TrimMemory` no longer includes the cache pass; the platform trim path and the OOM
  scheduler call `TrimCaches` explicitly. The old libbox comment already promised "drops cached
  state", which the old code did not do.
- `ReleaseMemory` also walks endpoints (it previously walked only outbounds). The endpoint walk can
  only drop resources with no active user traffic, by the `IdleConnectionReleaser` contract.
- A queued crisis re-checks the LIVE footprint before resetting and stands down if the process has
  fallen below the armed line. The trigger was observed before the in-flight idle pass finished;
  resetting a network for memory that pass already released would terminate healthy paths for a
  problem that no longer exists.
- A reload carries the observable pressure state and the sampling interval, but NOT a dropped
  callback's `forceMinInterval` / `pendingPressureBaseline` intent. The callback belonged to the old
  delivery; inheriting it would make the new instance poll fast or measure growth from a sample it
  never took. Test: `TestReloadDoesNotCarryAPendingPressureIntent`.

### 4.6 Regression evidence

All commands run on this branch at the final commit:

```text
go test -count=1 ./service/oomkiller
CGO_ENABLED=1 CC=gcc go test -race -count=1 ./service/oomkiller
go test -short -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./route
go test -short -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./dns
go test -race -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run 'TestTrimMemory|TestReleaseMemory|TestTrimCaches|TestRouterTrimExpired' ./route
go test -race -count=1 -run 'TestTrimExpiredCaches' ./dns
go build -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./...
go test -run '^$' -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./...
go vet -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./service/oomkiller ./route ./dns ./adapter
```

`./route` is run with `-short` because `TestBackpressure_KernelGiveUpWindow` is a six-minute kernel
observation by design, not a failure.

The Darwin callback tests moved out of the `darwin && cgo` file and now run on every platform;
the dispatch source itself (the C half) is still only compiled on an Apple build.

Deterministic action counts (`GC_TRIM_COST`), all from the tests above, with synthetic footprints:

| scenario | trims | forced GCs | resets |
|---|---|---|---|
| stable 33 MiB, 1000 ticks | 0 | 0 | 0 |
| stable 41 MiB, 100 ticks with release/cooldown forced into the past | 0 | 0 | 0 |
| 39 <-> 41 MiB, 40 transitions | 1 (first descent only) | 0 (below armed, no runtime evidence) | 0 |
| one Triggered episode | 0 (crisis path) | 1 | 1 |
| the 33 MiB drop immediately after that reset | 0 | 0 | 0 |

Real bytes freed, CPU time and energy on a device: **N/A** - the tests use injected footprints and
no iOS hardware was available.

## 5. Limitations

- No iOS device or NetworkExtension run. The lifecycle fixes are deterministic and the packages
  build everywhere reachable here, but `DEVICE_VALIDATION = NOT_VERIFIED_ON_DEVICE`.
- No A/B throughput/power measurement. The layer counts are real; the byte claims are not measured
  on hardware.
- NaiveProxy, Mieru and MASQUE are not layered: none exposes an idle ownership boundary this tree
  can release without suspending or resetting the tunnel. They need SDK/protocol-level lease support.
- The persistent bbolt DNS cache and the FakeIP store are deliberately untouched; their cleanup is
  owned by their own storage loops, and clearing FakeIP mappings would break live address bindings.
- Darwin-specific C (the dispatch source) was not compiled locally; the callback LOGIC is covered
  cross-platform, and an Apple CI build is the remaining compile check.

```text
OVERALL = DONE_WITH_LIMITATIONS
TARGET_BRANCH = oom-final-2026-10-24
TESTING_WRITTEN = NO
MAIN_WRITTEN = NO
START_HEAD = 489ed2c4c16448306bcae9df37c57f5dadfb4ac0
ROOT_CAUSE_33MB_REGRESSION = PASS (0 trims/0 GC over 1000 ticks at 33 MiB; opportunity gate retained)
ARMED_STABLE_41MB_NO_THRASH = PASS (0 actions over 100 ticks with cooldown forced expired)
POLL_STOP_PUBLISH_RACE = PASS (barrier for every accepted poll; pressure published under access)
ALL_RECLAIM_PATHS_COORDINATED = PASS (one executor; idle/pressure/crisis serialised; pending dropped at stop)
DARWIN_CALLBACK_LIFECYCLE = PASS (logic tested cross-platform; C source compile not run locally)
LAYERED_RECLAIM = L1 expired caches -> L2 idle pools -> L3 forced GC only with pressure/runtime evidence; each layer measured, stops at first floor; L4 crisis stays a network reset
POOL_OWNERSHIP_SAFETY = PASS for the walked owners (active checks cited in 4.4); Naive/Mieru/MASQUE not reachable
GC_TRIM_COST = 0/0 at 33 MiB (1000 ticks); 0/0 at 41 MiB (100 ticks); 1/0 for 39<->41 (40 transitions); 1/1 for one crisis. Bytes/CPU/energy: N/A (no device)
TESTS_RACE_BUILD = PASS: go test oomkiller; -race oomkiller; -short route/dns; targeted -race route/dns; go build ./...; go test -run '^$' ./...; go vet (adapter, route, dns, oomkiller)
CI_FINAL_SHA = see the run recorded in the final reply
DEVICE_VALIDATION = NOT_VERIFIED_ON_DEVICE
UNRESOLVED = iOS device trace; Naive/Mieru/MASQUE idle ownership needs SDK support; persistent bbolt/FakeIP stores intentionally excluded
```
