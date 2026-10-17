# Power and mobile lifecycle

**Scope:** `common/power/governor.go`, `common/runtimecoord/platform_events.go`, `box_lifecycle.go`,
`experimental/libbox/platform_events.go`, `service/oomkiller`, `internal/memmetrics`, and the reuse
policy in `route/reference.go`.

**Provenance:** `common/power` is a **fork-created package** (7 files, ~2,579 insertions, first commit
`aac340808`); `internal/memmetrics` is fork-created. The Apple device axis, the Android trim mapping
and the reuse-band policy are fork work.

**Load this module when changing:** screen state, lock state, sleep/resume, device wake, memory trim,
`ReuseSuspect`, the pause/keepalive policy, or any timer that runs while the device may be idle.

---

## 1. The boundary: the client reports facts, the core decides policy

This is real, and it is enforced by where the classification lives rather than by convention.

| Concern | Owner | Anchor |
|---|---|---|
| What a platform *reports* | the platform, through `experimental/libbox/platform_events.go` — a **thin forwarder**: `MemoryTrim(level int32)` `:234`, `SetAppForeground(bool)` `:243`, `SetScreenOn(bool)` `:261`, `ReportDeviceWake()` `:269`, `Close()` `:277` | `:92` builds `boxPlatformTarget`; `:194 NewPlatformEvents` routes into `runtimecoord.PlatformEvents` |
| What a level *means* | **the core** — `TrimLevel` constants and `TrimLevel.Action()` | `common/runtimecoord/platform_events.go:152-220` |
| Coalescing windows | **the core** — `trimCoalesceWindow = time.Second` (`:223`), `releaseCoalesceWindow = adapter.RecoveryWindow` (`:231`) | as cited |
| The device state machine | **the core** — `common/power.Governor` | `governor.go:53 State`, `:93 Allow`, `:109 ReuseAction`, `:141 ReuseBoundary`, `:168 Policy`, `:201 ReuseFreshness.Classify`, `:239 WakeStagger`, `:263 DefaultPolicy`, `:471 DevicePaused`, `:490 DeviceWake`, `:525 SleepStarted`, `:563 Resumed`, `:659 NetworkPaused`, `:671 NetworkWake`, `:707 ObserveTraffic`, `:861 ReuseEpoch`, `:883 AddReuseObserver`, `:894 WaitActive`, `:931 WaitProviderRefresh` |

**The classification of a raw platform number lives in the core, not the client.** If a client sent
"you should trim", policy would have moved into three client codebases; instead a client sends the
number the OS gave it and the core decides.

---

## 2. Level, edge, snapshot — four vocabularies that must not be merged

| Vocabulary | Level API | Edge API | Snapshot / reconcile API |
|---|---|---|---|
| `common/power.Governor` (the state machine) | `DevicePaused` `:471`, `NetworkPaused` `:659` | `DeviceWake` `:490`, `SleepStarted` `:525`, `Resumed` `:563`, `NetworkWake` `:671` | `State` `:53`, `Policy` `:168`, `ReuseBoundary` `:141` |
| `pause.Manager` (sing's level+edges) | its level getters | its edge callbacks | — |
| `box_lifecycle.go` (the Apple mapping) | `screenState(on)` `:173-179`, `lockState(locked)` `:193-199` | `slept()` `:94-101` (edge-then-level), `resumed()` `:141` (**publishes nothing**), `woke()` `:156-163` (boundary-then-level) | `reconcileScreenState` / `reconcileLockState` `:228-252` — **level-only** |
| `common/runtimecoord/platform_events.go` (Android/foreground) | the trim ladder below | the screen/foreground edges | the event snapshot |

### Why `reconcileScreenState` / `reconcileLockState` must be level-only

A **current-state read is not an edge**. If a re-read published an edge, every poll would look like a
fresh transition. The pinned statement is the invariant:

> **Apple snapshot reconciliation is level-only. It must not fabricate sleep/wake edges, and it must
> not release a pause based only on a screen read.**

`645d96b4` is the commit: *"a current-state screen read reconciles the level, it does not start a
sleep."* `60681f27` is its pair: *"a resume does not end a sleep episode, so it must not spend its
boundary."*

### Why a display turning **on** is not an unlock

`box_lifecycle.go:23-36`, `:173-199`: **display-on publishes only a resume *edge*; only an unlock
releases the level.** A future change that equates the two re-creates the bug the split was made to
fix.

### `resumed()` deliberately publishes nothing

```go
func (l lifecycle) resumed() {}
```

`box_lifecycle.go:141`. The measured story is at `:109-137`: a **28 m 24 s** sleep with 13 extension
resumes produced **13 anchors and a 28.29 s boundary** — the measurement was destroyed by the resumes
themselves. This is why the resume path is a no-op and why the sleep boundary is spent on the sleep
**edge**, not on a resume.

**The anchor and the per-episode boundary latch are the same field.** That is the structural reason a
later attempt to gate the sleep edge on `IsDevicePaused()` produced **zero** boundaries over a ten-minute
lock (strictly worse than the bug it fixed) and was reverted (`e20875359` → `e661258a6`, both kept as
`ARCHIVE-ONLY`). `docs/fork/p1c-iphone-device-closure.md:289-291` states it.

### Device evidence (the only physical-device run recorded in this repository)

Device: **"Jiejie" — iPhone 17 Pro Max (`iPhone18,2`), iOS 26.4.2 (23E261), physical, wired.**

| Build | Episode | Callbacks | Boundaries published | Measurement |
|---|---|---|---|---|
| pre-fix | one 28 m 24 s lock | 26 `ne-sleep`/`ne-wake` (13 cycles), 0 screen events | **12** | **28.29 s** (wrong duration) |
| post-fix `60681f277` | one 13 m 13 s lock | 20 callbacks (10 cycles) | **1** | `reuse: epoch 1, sleep 13m13.492448s` — **exact to the second** |
| **rejected** `e20875359` | ~10 min locked | — | **0** | strictly worse than the bug; reverted |

iPad and macOS were **`NOT_RUN` / `SIGNING_OUT_OF_SCOPE`**. This is one device, one OS build, one
product. It is `VERIFIED_DEVICE` for that configuration and **`UNVERIFIED` for every other**.

---

## 3. The Android trim ladder

`common/runtimecoord/platform_events.go:152-220`.

| Constant | Value | `Action()` |
|---|---|---|
| `TrimRunningModerate` | 5 | `TrimProgressive` |
| `TrimRunningLow` | 10 | `TrimProgressive` |
| `TrimRunningCritical` | 15 | **`TrimRelease`** |
| `TrimUIHidden` | 20 | `TrimProgressive` — **but see below** |
| `TrimBackground` | 40 | `TrimProgressive` |
| `TrimModerate` | 60 | `TrimProgressive` |
| `TrimComplete` | 80 | `TrimProgressive` |
| anything else | — | **`TrimIgnore`** |

The `TrimIgnore` default carries its own rationale: *"a trim issued for a level whose meaning we do not
know is a cost paid for a guess."* **Only `TrimRunningCritical` earns the reset-shaped pass.**

**`TrimUIHidden` is explicitly not a memory signal** (`:171-178`). The UI being hidden says nothing
about memory pressure, and treating it as pressure would trim every time a user switches apps.

---

## 4. Memory policy — three distinct actions, and the one that is forbidden

| Action | May it reset the network? | What it is for |
|---|---|---|
| **Trim** (`TrimIdleResources`, `TrimProgressive`) | **NO** | idle-only reclamation |
| **Release** (`ReleaseMemory`, `TrimRelease`) | **YES** | a real memory emergency |
| **Reuse boundary** (`RetireSuspect`) | no reset, but a stronger reuse decision | a sleep/reuse boundary, **not a trim substitute** |

The invariant, and it is a hard one:

> **Memory trim is idle-only: it must not reset the network, dial, wake suspended work, or terminate
> active flows.** `ReuseSuspect` is a stronger sleep/reuse-boundary action, not a trim substitute.

`route/reference.go:154-197` `onReuseBoundary` obeys it: it **does not** reset the network, does not
terminate a stream, does not touch on-demand tunnels, does not dial.

**And the honest correction:** the invariant is about the **trim** path, not about every path.
`service/oomkiller/timer.go:324-337` calls `ReleaseMemory`, which **does** reset the network — and it
is *supposed* to. `route/conn_reclaim.go:18` records the intent in the code:
`ReleaseMemory (OOM killer) -> ResetNetwork + idle … aggressive, and deliberate`. The killer itself is
disabled by default (`option/oom_killer.go:13`, `json:"-"`). So:

**Trim ≠ Release. A future change that makes the trim path reset the network is the bug; a future
change that makes the Release path stop resetting it is also a bug.**

### The four pools

`runtimecoord`'s boundary walk must reach **all four**: outbounds, endpoints, DNS transports, and the
HTTP client manager. The fourth was unreachable for four review passes because the walk asserted the
bundled `IdleConnectionKeeper` instead of `IdleConnectionReleaser` — see `core-lifecycle-runtime.md` §4
and `buffer-ownership.md` B22.

---

## 5. Post-wake reuse bands

The policy is `common/power/governor.go`'s, and the verdict is one of exactly three values —
`ReuseKeep`, `ReuseSuspect`, `ReuseRetire` (`governor.go:111-124`) — derived from
`DefaultPolicy`'s `SuspectAfter: 5 * time.Second` and `RetireAfter: 15 * time.Second`
(`governor.go:289-290`):

| Sleep duration | Verdict | Behaviour |
|---|---|---|
| `< 5 s` | `ReuseKeep` | nothing. **The epoch does not advance and no observer runs** — the connection a person is about to use again is still pooled. |
| `>= 5 s` | `ReuseSuspect` | the epoch advances and the verdict is **published**, but pools are **kept by policy**. The middle band is a *published state, not an action*: churning a pool a short sleep usually survives costs a handshake for no correctness gain. |
| `>= 15 s` | `ReuseRetire` | the epoch advances and **idle** reusable resources are retired, so the next demand dials a path known to be new. **Active flows are untouched and nothing dials.** |

**An unknown sleep duration takes the strict verdict** (`TestTheBridgeUsesThePolicyBandsAndNothingElse`).

`adapter.ReuseSuspect` exists because **an open stream is not proof of liveness** — it can be open and
silent across a sleep. Its contract is "no new work on what you hold now, keep the work already on it,
tear it down when the last stream leaves".

A keeper *"may only close a resource with no active user traffic, and can never dial"*. `naive` is
deliberately **not** a keeper; `sing-mux` has no safe no-new-stream state; Cronet and Naive offer only
destructive close. That is why `IdleConnectionReleaser` is the assertion, not a full keeper.

Pinned by `common/power.TestReuseKeepsEveryResourceForAShortSleep`,
`common/power.TestReuseBandsAreThePolicy`, `box.TestTheBridgeUsesThePolicyBandsAndNothingElse`,
`route.TestTheSuspectBandDrainsNothing`.

**Background work does not wake an idle resource** — demand is what wakes it
(`docs/fork/runtime-lifecycle-phase1.5.md:30,64`). A background timer that wakes a suspended resource
is a defect even when the timer's work is useful.

---

## 6. The timer census — which timers may be deferred and which must not

`docs/fork/power-timer-audit.md`. Getting this wrong in either direction is a defect: deferring a
watchdog hides a leak; not deferring a poll burns radio time.

| Timer | Cadence | Verdict |
|---|---|---|
| `service/oomkiller` poll | 100 ms | **a watchdog, NOT speculation.** *"The honest fix if it is wanted is a slower cadence or an event-driven trigger … not suppression by sleep state."* |
| `internal/memmetrics` sampling | 1 ms | **benchmark-only** |
| `service/ssmapi/server.go` | 1 min | deferrable — **but the FIRST pass is deliberately not deferred** |
| `route/rule/rule_set_updater.go` | — | deferrable — **first pass also not deferred** |
| `service/api/dashboard.go` | 24 h (default) | deferrable — **first pass also not deferred**, and it WAITS rather than skipping: `WaitProviderRefresh` parks the loop, so the download lands on the wake instead of a whole interval later |
| `dns_cache` | 1 h | must **not** be gated |

**Three periodic timers are deferrable, and for all three the first pass runs regardless.** The
"first pass is not deferred" rule is the one that keeps a deferral from delaying initial correctness.

Two workers in this area are **not timers**, and are gated by state rather than by waiting, so they do
not belong in the table above:

- `dns.client.backgroundRefreshDNS` — scheduled from a cache HIT, not by a clock. Refused outright
  while the governor is `QUIESCENT`/`DEEP_IDLE` or closed, and released through
  `Allow().NetworkProbe` in the `WAKING` band. Skipping costs freshness only.
- `transport/wireguard/recovery.go` — polls, but only while a session is stale, and it is **not**
  gated: a rebind is already limited to one per window by `runtimecoord`, and cancelling it during a
  sleep would trade a wasted rebind for an unrecovered tunnel. Accepted, not deferred.

`HAKO_SCALE_DEBUG_AUDIT_2026-10-17.md` carries the tests and mutations behind both gates.

---

## 7. `ReuseSuspect`

| Property | Value |
|---|---|
| Order | `RetireSuspect()` **first**, then `CloseIdleConnections` (`route/reference.go:496-511`, `:547-556`) |
| Pool count | exactly **one**; `route/reusable_owner_inventory_test.go:49,96,117` forbids a second |
| Trigger | a **sleep/reuse boundary**, via `powerGovernor.AddReuseObserver(m.onReuseBoundary)` (`:142-144`) |
| Must not | reset the network, terminate a stream, touch on-demand tunnels, or dial |

---

## 8. Evidence

| Guarantee | Level | Re-check |
|---|---|---|
| Trim ladder values and actions; unknown ⇒ ignore; `TrimUIHidden` is not memory pressure | `VERIFIED_SOURCE` + `VERIFIED_UNIT` | `go test -count=1 ./common/runtimecoord/...` |
| Governor state machine and bands | `VERIFIED_SOURCE` + `VERIFIED_UNIT` | `go test -count=1 ./common/power/...` |
| Reuse verdict published **before** the level is released | `VERIFIED_UNIT`, and the test proves the band was exercised | `-tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -run TestTheReuseVerdict .` |
| Sleep edge recorded **before** the level is paused | `VERIFIED_UNIT`, with a vacuity guard | `-run TestTheSleepEdge .` |
| 100 sleep/wake cycles, 100 network transitions, no resurrection after Close | `VERIFIED_UNIT` (no `-race`) | `-run 'TestOneHundred\|TestNoResurrectionAfterClose' .` |
| The Apple device axis | **`VERIFIED_DEVICE`** for one iPhone 17 Pro Max / iOS 26.4.2 run; **`UNVERIFIED`** for iPad, macOS, and any other OS | the device record above |
| Real TUN, real NetworkExtension, network transitions | **`UNVERIFIED`** — zero executed evidence; a release blocker in `docs/RELEASE-CERTIFICATE.md` | needs root + Linux, and a signed Apple build |
| Race-freedom | **`BLOCKED_BY_ENVIRONMENT`** | needs cgo |

The ordering test is the strongest-designed file in the set: it shares an `orderingSequence` recorder
between the pause callback and the reuse observer and asserts
`[]string{"level-paused","reuse-verdict","level-released"}`, then **guards against the assertion being
vacuous** with `require.NotEqual(… ReuseKeep …)` and `require.Equal(uint64(1), governor.ReuseEpoch())`.
The sleep test observes the order from **inside** the level call via a `probingAxis` and guards
vacuity with `require.True(t, axis.probed, …)`. **Copy that pattern** — an ordering test that does not
prove the code path was exercised is worthless.

---

## 9. Provenance

| Commit | Type | What it did |
|---|---|---|
| `aac340808` | LIFECYCLE | the sleep authority |
| `c62342717`, `540d7cb94`, `4a95e0aeb`, `e966daea2`, `a804916ef`, `d93b68560` | FIX | pool release at `DEEP_IDLE`; the unreachable `DEEP_IDLE` branch; a stale deadline |
| `6ebb5799a` | FIX | stale deadline |
| `89b4b2c25` | FIX | `DEEP_IDLE` was unreachable |
| `0ff90482` | FIX | spends the device resume edge on an **edge latch**, not on the screen level |
| `e4327dca` | LIFECYCLE | gives the Apple device axis a real publisher and a real release |
| `4c7d96c0` | FIX | carries the Apple device axis on the **release line**, not only on a branch |
| `645d96b4` | FIX | a current-state screen read reconciles the level, it does not start a sleep |
| `60681f27` | FIX | a resume does not end a sleep episode, so it must not spend its boundary |
| `e20875359` → `e661258a6` | FEATURE → **REVERT** | the `IsDevicePaused()` sleep-edge gate, reverted on device evidence. Both archives kept. |
| `1331e8567`, `1f85dbc94` | FIX | generation-based rebind budget; `TrimMemory` does **not** advance the network generation while `ReleaseMemory` does |
| `5f56f02c`, `09964183`, `93823c3d` | MEMORY | the trim path; all four pools reachable |
| `3c6dd135b`, `f8b0af5f6`, `48dd4469e`, `10f9e17d2`, `eac9cc83d`, `b5c0009d1`, `95159084c`, `666470cbe`, `b5c39a11e`, `c2d93396e`, `da14572a7`, `1965bbf35` | TEST | the leak-census / test-integrity family — **several of these fixed tests that could not fail** |
| `12aa6db064f`, `dbf8de8b2a5` | FEATURE | the first two **consumers** of the authority: the optimistic DNS refresh (refused by state) and the dashboard download (parks on `WaitProviderRefresh`). Tests and five mutations in `reports/core/HAKO_SCALE_DEBUG_AUDIT_2026-10-17.md` |
| `ff897d35772` | FIX | resolves the DNS governor at construction instead of in `Start`: the field a background goroutine reads is made immutable rather than ordered |

All **OWN**. The upstream family `Use screen state to end device pause on iOS` (×11 in this range) is
`UPSTREAM-INHERITED`/orphaned — see `BASELINE.md` §provenance.

---

## 10. AI cautions

1. **Do not merge level and edge.** A current-state read is not a transition; `resumed()` publishes
   nothing on purpose.
2. **Do not make a display-on an unlock.**
3. **Do not "fix" the sleep boundary by gating on a paused state.** That was tried, measured at zero
   boundaries on a real device, and reverted — the anchor and the latch are one field.
4. **Do not make the trim path reset the network, dial, wake suspended work or kill active flows.**
5. **Do not make `ReleaseMemory` stop resetting the network.**
6. **Do not treat `TrimUIHidden` as memory pressure.**
7. **Do not default an unknown trim level to a progressive action.** Unknown must be ignored.
8. **Do not classify levels in the client.** The core owns the mapping.
9. **Do not add a periodic timer without checking the census**; if it is a watchdog it must not be
   gated on sleep state.
10. **If a timer is deferrable, keep the FIRST pass undeferred.**
11. **Do not assert `IdleConnectionKeeper` where only `IdleConnectionReleaser` is implemented.**
12. **Do not add a second `ReuseSuspect` pool.**
13. **Do not let background work wake an idle resource.** Demand wakes it.
14. **Do not write an ordering test without a vacuity guard.** The fork's own best test exists because
    an ordering assertion can pass without the path ever running.
15. **Do not generalise the one iPhone run to iPad, macOS or another OS build.** The record is one
    device.
16. **Do not cite `verify-evidence/` for any of this** — see `ci-release.md` §3.
