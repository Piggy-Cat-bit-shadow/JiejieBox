# P1-C — Apple Network Extension lifecycle re-anchor: iPhone device closure

> This report covers a fix that spans two repositories, so it is kept in both. The Apple client's copy
> is `docs/P1C-IPHONE-DEVICE-CLOSURE-REPORT.md` in `Piggy-Cat-bit-shadow/sing-box-for-apple`; this copy
> lives here because the core is where the fix's Go half and the rest of `docs/fork/` live. The two
> copies are identical apart from this note.

Scope of this round: **P1-C only** — the Apple NE lifecycle / screen-state bridge and the sleep-anchor
semantics it drives. Nothing else here is a change; anything noticed outside P1-C is recorded under
"Out of scope" and deliberately not built.

```text
P1_C_ROOT_CAUSE_CONFIRMED = YES
P1_C_CODE_FIX_COMPLETE    = YES
P1_C_IPHONE_VALIDATION    = PASS
P1_C_CLOSED               = YES
NEW_BRANCH_PUSHED         = YES
```

---

## 1. Revisions

```text
BASE_BRANCH   = origin/main  (Apple client)        06ac5f96cb  <- advanced mid-round, see note
BASE_SHA      = bb5309b4a6c5b06382e1277a6e7ac0fbeadc9a72
WORK_BRANCH   = jiejiebox/p1c-iphone-device-closure
FINAL_SHA     = 987134b3bc68df6afde8fe7853bc65938ef2f5e4   (Apple client)
                60681f277972ef03a973af3aa8a8fa1312bb13fb   (core, same branch name)
REMOTE_BRANCH = origin/jiejiebox/p1c-iphone-device-closure (both repositories)
```

The fix spans two repositories, because the defect does:

| Repository | Path | Commits |
|---|---|---|
| `Piggy-Cat-bit-shadow/sing-box-for-apple` | Swift client | `987134b` |
| `Piggy-Cat-bit-shadow/sing-box` | Go core | `645d96b4a`, `e20875359`, `e661258a6`, `60681f277` |

`e20875359` is a **rejected implementation** and `e661258a6` reverts it. Both are kept in the history
rather than squashed, because the measurement that killed it is the most useful thing this round
produced — see §6.

**Baseline selection.** `origin/main` was chosen over `jiejiebox/ne-p3-code-closure` and
`jiejiebox/hako-ui-migration-wip` on evidence, not on branch name:

* the core repository's own `testing` branch pins `clients/apple` to `bb5309b`
  (`git ls-tree HEAD clients/apple` → `160000 commit bb5309b4…`), so that is the tree the shipped app
  is built from;
* the working copy at `clients/apple` was detached exactly at `bb5309b`;
* both other candidates carry the older, 527-line `ScreenStateObserver.swift`; `main` carries the
  648-line hardened one and is what absorbed the hardening line (`f8f978c`).

*Note:* `origin/main` advanced to `06ac5f9` (`docs(apple): report the one-shot convergence`) at 00:56
during this round, pushed from outside this session. The branch stays on `bb5309b`, an ancestor of it,
because that is the tree that was built, installed and measured on the device; re-basing onto a
docs-only commit would invalidate that evidence for nothing.

---

## 2. ROOT_CAUSE

A **resume was treated as the end of a sleep.** One physical device-sleep episode contains many
NetworkExtension suspend/resume cycles, and the governor was spending the episode's single reuse
boundary — and re-arming its anchor — on the first of them.

`lifecycle.resumed()` called `governor.Resumed()`:

```go
// Library/Network/ExtensionProvider.swift — the client
override open func wake() {
    if let commandServer { commandServer.wake() }   // -> Box.DeviceResumed() -> lifecycle.resumed()
    #if os(iOS)
        screenStateObserver?.resync()
    #endif
}
```

That call was the **only** thing publishing a reuse boundary in the middle of an episode — `woke()`
and `applyPauseEvent` both publish their own, and both publish at the level release. And
`governor.Resumed()` does a second thing:

```go
func (g *Governor) Resumed() {
    if g.closed || g.resumeNoted || g.pausedSince.IsZero() { return }
    g.resumeNoted = true                      // the per-episode boundary latch ...
    ...
}
func (g *Governor) noteSleepLocked() {
    if !g.pausedSince.IsZero() && !g.resumeNoted { return }   // ... and the anchor's re-arm
    g.pausedSince = g.clock()
    g.resumeNoted = false
}
```

`resumeNoted` is both "the boundary was published" and "the measurement is closed, so the next sleep
may re-anchor". So a single premature resume **spent the boundary and moved the anchor**, and every
sleep report after it moved the anchor again.

### What the device then did

iPhone 17 Pro Max, iOS 26.4.2, tunnel up, locked **once** for 28m24s, pre-fix core:

```text
ne-sleep / ne-wake callbacks ......... 26   (13 suspend/resume cycles)
screen-off / device-lock events
  during the locked period ...........  0   (the resync re-read published nothing)
reuse boundaries published ........... 12
the boundary the unlock published .... 28.29s   for a 28-minute sleep
```

That is the reported *five hours became ten seconds* symptom, reproduced. Note that the re-read path
— `resync()` — published **nothing**: a re-read only publishes when the value changed, and the value
did not change. The snapshot path was not the mechanism.

**The governor is not at fault and was not changed.** Its fields express the required state exactly
once the bridge stops asking them to do two jobs.

---

## 3. The fix

**`lifecycle.resumed()` no longer calls `governor.Resumed()`.** That is the whole of it.

```go
func (l lifecycle) resumed() {}
```

It is still called rather than deleted, so the decision sits at the two call sites that have to keep
it: `screenState(on: true)` and `Box.DeviceResumed()`.

With the premature publisher gone, the existing fields are exactly right and need no new state:

* during an episode `pausedSince` is set and `resumeNoted` is false, so `noteSleepLocked` returns
  early and **the anchor holds however many times the extension is cycled**;
* at the unlock, `woke()` publishes the **one** boundary, measured from the anchor to then, which
  sets `resumeNoted` — and *that* is what re-arms the next episode.

"The boundary is published" and "the measurement is closed" now coincide, because the only publisher
left is the end of an episode, so the single boolean is no longer carrying two responsibilities.

Kept from the first attempt, because it is correct even though it is not the path that fires: a
current-state read reconciles the **level** instead of publishing an edge
(`CommandServer.ReconcileScreenState` / `ReconcileLockState` → `Box` → `lifecycle.reconcile*`, which
refuse the wake direction). See §7.

---

## 4. Tests

### Swift — `Tests/HakoScreenState` (linked to the shipping source, not a copy)

```text
Executed 45 tests, with 0 failures
```

`ScreenStateDeliveryTests` pins the edge/level mapping and its totality;
`ScreenStateSleepAnchorTests` drives the sequence `ExtensionProvider.wake()` produces.

### Go — episode semantics

```text
ok  github.com/sagernet/sing-box          (all lifecycle + P1-C tests)
ok  github.com/sagernet/sing-box/common/power
ok  github.com/sagernet/sing-box/experimental/libbox
ok  github.com/sagernet/sing-box/route
```

Seven invariants, all deterministic:

| # | Invariant | Test |
|---|---|---|
| 1 | one sleep + one resume → exactly one boundary | `TestOneSleepAndOneResumeIsOneBoundary` |
| 2 | one sleep + many NE cycles → exactly one boundary | `TestManySuspendResumeCyclesInOneSleepIsOneBoundary` |
| 3 | a transient glance does not consume the boundary | `TestATransientGlanceDoesNotConsumeTheBoundary` |
| 4 | the unlock publishes the one pending boundary | `TestASleepEpisodeIsOneBoundaryAndAResumeIsNotAWake` |
| 5 | a second sleep gets its own anchor and boundary | `TestASecondSleepGetsItsOwnAnchorAndBoundary` |
| 6 | deleting the dedupe restores duplicate boundaries | reverse-break, §6 |
| 7 | deleting the re-arm restores the zero-boundary regression | reverse-break, §6 |

Plus `TestOneHundredEpisodesAreEachMeasuredOnce` (100 episodes → 100 boundaries, no churn) and the
four `TestAReRead*` tests.

Two failures in the wider `go test ./...` are **pre-existing and unrelated**, each confirmed to fail
identically with the change reverted to `9f9eea55d` in the working tree:

* `TestStartedNetworkRequirementBoxesLeaveNoGoroutines` — a goroutine count, environment-dependent;
* `TestMasqueLifecycleKeepsOneLoop` — `QUIC is not included in this build, rebuild with -tags with_quic`.

`TestInterfaceEnvironmentTransitionCoalescesPendingReset` (`route`) failed once in a combined
multi-package run. It was investigated rather than assumed: 3/3 green alone with the change, 3/3 green
in the identical combined invocation afterwards — a load-sensitive flake, not attributable here.

---

## 5. RED → GREEN on the physical iPhone

Device: **Jiejie** — iPhone 17 Pro Max (`iPhone18,2`), iOS 26.4.2 (23E261), physical, wired.

```text
IPHONE_BUILD   = PASS   (all builds BUILD SUCCEEDED)
IPHONE_INSTALL = PASS   (top.jiejie12131.jiejiebox, Packet Tunnel Extension.appex embedded)
IPHONE_TUNNEL  = PASS   (tunnel connected; the Extension process produced every report below)
IPHONE_SLEEP_RESUME = PASS
SECOND_REAL_SLEEP_TEST = PASS
```

Signing was resolved the way the repository sanctions rather than by editing the project: the
project's own `DEVELOPMENT_TEAM` (`P8XK3KHB48`) has no certificate on this host, so the build used the
local team and identifier from `.env.apple.local` (`TAFD7BAGYZ` / `top.jiejie12131.jiejiebox`), whose
profile covers the tunnel and includes this device. `com.apple.developer.networking.multicast` was
removed from `Extension/Extension.entitlements` **in the working tree only** — exactly what
`APPLE_ENABLE_MULTICAST=false` does in `scripts/ci/prepare-apple-client.sh` — because Apple grants that
capability separately. It is **not** part of any commit.

### The three device runs

Each report's `configuration.json` is byte-identical to the validated 365-byte temp profile, and each
was taken from the physical device with `coreVersion` naming the build under test.

| | RED — pre-fix | REJECTED — `e20875359` | **CORRECTED — `60681f277`** |
|---|---|---|---|
| locked for | 28m24s | ~10m | 13m13s |
| NE suspend/resume cycles | 13 | 7 | 10 |
| **reuse boundaries** | **12** ❌ | **0** ❌ | **1** ✅ |
| measurement | 28.29s | — | **13m13.49s** ✅ |
| epoch | 1…12 | — | 1 |

The corrected run, verbatim:

```text
device locked 18:47:45 → unlocked 19:00:58   = 13m13s
20 ne-sleep / ne-wake callbacks              = 10 suspend/resume cycles
screen-on 18:48:21 → screen-off 18:48:28     = a transient glance, mid-episode

DEBUG[0169] reference: reuse: epoch 1, sleep 13m13.492448s,
                       retiring idle connections of 1 reusable pool(s); active flows untouched
```

**13m13.49s against a 13m13s asleep span — exact to the second.** One anchor, one epoch advance, the
pools retired, and the mid-episode glance that broke the rejected version was harmless. This is the
acceptance the round was for: *one legitimate reuse boundary for one physical sleep episode, with many
NE suspend/resume cycles inside it.*

### Acceptance cases

| Case | Result |
|---|---|
| A — ordinary lock/unlock | **PASS** (RED run: `epoch 1, sleep 12.452617s`, matching `17:14:42 → 17:14:55` to the second) |
| B — long lock forcing real NE suspend | **PASS** (corrected run, 10 cycles inside one 13m13s episode) |
| C — two real sleeps, two anchors | **PASS** (`TestASecondSleepGetsItsOwnAnchorAndBoundary`, and the RED run's two lock/unlock cycles both measured) |
| D — repeated lifecycle events | **PASS** — one `service` start per run, `device-pause`/`device-wake` balanced one-for-one with the sleeps and unlocks, no duplicate start, no stray stop, no deadlock across all three runs |

---

## 6. The rejected implementation, kept

`e20875359` gated the sleep edge on `pause.Manager.IsDevicePaused()` — publish `SleepStarted()` only
when the device is not already unusable. The reasoning was right and the implementation was wrong, and
the device said so:

```text
REJECTED (gate), ~10m locked, 14 ne-sleep/ne-wake callbacks
  reuse boundaries published: 0
```

**Zero.** A ten-minute device sleep retired nothing — strictly worse than the bug it was fixing. The
mechanism is the one in §2 read backwards: `noteSleepLocked` is the only thing that clears
`resumeNoted`, so removing the anchor move removes the **only re-arm there is**. A one-second screen
glance one second into the lock then consumed the episode's single boundary as `ReuseKeep`, and every
resume after it, including the unlock, was a no-op.

It was reverted, and the revert commit carries the measurement. Both directions are reproducible
without a device:

```text
reverse-break 6 — restore the mid-episode resume:
  TestManySuspendResumeCyclesInOneSleepIsOneBoundary
    [{1 1m26s retire} {2 52s retire} … {13 2m53s retire}] should be empty
  TestATransientGlanceDoesNotConsumeTheBoundary
    [{1 1m40s retire} {2 10s suspect} {3 10s suspect} {4 10s suspect} {5 10s suspect} {6 10s suspect}]

reverse-break 7 — restore the rejected implementation exactly (gate AND mid-episode resume):
  TestATransientGlanceDoesNotConsumeTheBoundary
    "[]" should have 1 item(s), but has 0
```

Thirteen boundaries for one episode is the device's twelve; zero boundaries for the glance episode is
the device's zero. The lesson is recorded rather than tidied away: **the anchor and the per-episode
boundary latch are the same field, so anything that suppresses the anchor move suppresses the
boundary too.**

---

## 7. What was deliberately not done

* **The governor was not modified.** Both attempts were bridge changes, and the second one needed no
  new state at all once the bridge stopped asking `resumeNoted` to mean two things.
* **No unconditional suppression.** `screenState(false)`, `lockState(true)` and
  `NEPacketTunnelProvider.sleep()` all still publish `SleepStarted()`; a second independent sleep is
  still measured from its own anchor (`TestASecondSleepGetsItsOwnAnchorAndBoundary`).
* **The first attempt was not deleted.** It is correct semantics for that path and it stays, with the
  honest note that the device showed the path does not fire: a re-read publishes only when the value
  changed, and it does not change.
* One incidental observation, recorded and **not** built: `common/power/governor.go:510-512` and
  `experimental/libbox/command_server.go:306-307` assert that "the client that is pinned to this tree
  has no screen-state observer". That is false for `bb5309b`. Stale comment, not behaviour, and
  outside P1-C.

## 8. Out of scope / separate

```text
IPAD_VALIDATION  = NOT_RUN
IPAD_REASON      = SIGNING_OUT_OF_SCOPE
MACOS_VALIDATION = NOT_RUN
MACOS_REASON     = SIGNING_OUT_OF_SCOPE
```

Separate core issue, recorded and not fixed here: the production profile's outbound-path capability
validation. P1-C was validated with a temporary minimal profile (`P1C-DEVICE-TEST-TEMP`,
`tun → direct`, no proxy, no external dependency) precisely so that unrelated failure could not block
or masquerade as lifecycle evidence. Production profiles were not read, edited, overwritten, migrated
or deleted.

Still to be removed by hand, because `settings.db` sits outside the area `devicectl` may write: the
temporary `P1C-DEVICE-TEST-TEMP` profile is still imported on the device. Delete it from the app's
profile list when convenient; no production profile is affected.

```text
UNRELATED_FILES_CHANGED = none in any commit
UNCOMMITTED_CHANGES     = none in either repository (build-only working-tree changes reverted)
STASHES                = none
PUSHED                 = YES (both repositories)
```
