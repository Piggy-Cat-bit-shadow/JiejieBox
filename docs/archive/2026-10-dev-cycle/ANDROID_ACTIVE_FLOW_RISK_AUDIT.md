# Android active-flow risk audit

> Companion to `ACTIVE_TCP_STALL_INVESTIGATION.md`. That document proves the core mechanism; this
> one answers whether the Android client shares it, is protected from it, or makes it worse.

```text
ANDROID_RISK = SHARED_CORE_BUG
```

Repository audited: `Piggy-Cat-bit-shadow/satelite-one`
(local working copy `<WORKSPACE_ROOT>\安卓客户端\src\satelite-one-main`, KB at
`<WORKSPACE_ROOT>\安卓客户端\work\satelite-one\KNOWLEDGE`).

---

## 1. The mechanism Android would share

From the core investigation, in one paragraph: a proxied TCP flow whose peer goes silent without
FIN, RST or error leaves both `connectionCopy` goroutines parked in `Read` with no deadline. The
only thing in the whole core that ends such a flow is `ConnectionManager.Reclaim`, and that has
exactly one production call site — `route/network.go:914`, inside the **network-reset** path. No
transition, no reclaim, and the core owns no steady-state timer that could substitute.

**This code is platform-neutral.** `route/conn.go`, `route/conn_reclaim.go` and
`common/bufio`'s copy engine are the same Go source on Android, iOS and Windows. There is no
platform build tag in the copy loop and none in `Reclaim`. So by construction, if the mechanism is
real it is real on Android.

It is also stronger than "the flow goes quiet". The investigation's §4a measured the state directly:
a stalled flow has both copy goroutines parked on the **same outbound descriptor**, one blocked
writing it and one blocked reading it, with no progress in either direction. That is an ACTIVE FLOW
NO-FORWARD-PROGRESS STALL rather than an idle connection, and it is reached by ordinary backpressure
rather than by anything platform-specific — so nothing about Android changes whether it can occur.

The question is therefore not *whether* Android contains the code, but **whether Android supplies
the reclaim trigger more or less often than the other platforms do.**

---

## 2. What the Android app forwards, verified

`bg/PlatformFacts.kt` is the only writer and forwarder of platform facts to the core. Verified by
reading the file, not by trusting the KB:

| App method | Line | Core call | Core effect |
|---|---|---|---|
| `onScreenChanged` | 224 | `sink.setScreenOn(on)` | `PlatformEvents.SetScreenOn` |
| `onForegroundChanged` | 230 | `sink.setAppForeground(fg)` | `PlatformEvents.SetAppForeground` |
| `onMemoryTrim` | 218 | `sink.memoryTrim(level)` | `PlatformEvents.MemoryTrim` |
| `onUserPresent` | 234 | `sink.reportDeviceWake()` | `PlatformEvents.ReportDeviceWake` |
| `attach` | 153–154 | `setScreenOn`, `setAppForeground` | the initial level |

Then, in the core:

```text
SetScreenOn / SetAppForeground
   → recomputeDeviceLocked()
   → deviceActionPause → target.DevicePause()   (common/power Governor.DevicePaused, :471)
   |  deviceActionWake  → target.DeviceWake()    (common/power Governor.DeviceWake,    :490)
   → NEVER Reclaim
```

`applyDeviceAction` (`common/runtimecoord/platform_events.go:454`) has exactly two cases, and
neither is a reclaim. The power governor does not call `Reclaim` either:

```text
$ grep -n 'Reclaim' common/power/governor.go
(no matches)
```

**So none of the four facts Android forwards can reclaim a stalled flow.** Screen-off pauses;
screen-on wakes; neither touches the connection list. Android's only route into `Reclaim` is a real
**network change** — a Wi-Fi↔cellular handover, a gateway change, a new SSID — arriving through the
core's network-environment path.

---

## 3. Android's trigger set, compared

| Platform | Route into `Reclaim` | Frequency on an ordinary day |
|---|---|---|
| **iOS** | NetworkExtension sleep/wake produces a network-environment change | **every screen-off/screen-on cycle** |
| **Windows** | `EVENT_SUSPEND` → `ResetNetwork` (`route/network.go:1259`) | every suspend (laptop lid), and **nothing** on resume |
| **Android** | connectivity change only | only on a real network handover |

Android is therefore **not** protected. It sits in the same class as Windows — the reported case
that had *no* sleep/wake and *did* stall. The one comfort is that a phone moves between Wi-Fi and
cellular often enough that a stall is frequently cleared by an unrelated handover, which would make
the symptom look intermittent and be attributed to "the network changed" rather than to a bug.

This is the opposite of a mitigation: it is why the bug would be hard to attribute on Android.

---

## 4. Android-specific amplifiers

These do not create the stall — the core does that — but they change how often it is reached and how
long it lasts:

1. **Doze / App Standby.** A VPN service is exempt from Doze's network restrictions, so the tunnel
   stays up and sockets stay open while the device is idle. That is exactly the condition under
   which a silent half-open goes unnoticed: the socket is open, the app is frozen, and nothing sends
   anything to discover that the path is gone.
2. **OEM process freezing.** Aggressive OEM background killers can freeze or kill the app while the
   `VpnService` fd survives. The core's read loop is then parked on a socket whose peer state has
   moved on, with no userspace activity to notice.
3. **Wi-Fi → cellular with a tunnel up.** The handover *does* produce a reclaim, so this case
   self-heals — which is worth stating plainly, because it means an Android user reporting "it fixes
   itself after a while" is describing the one trigger Android reliably has.
4. **Screen-off with the app backgrounded.** `recomputeDeviceLocked` deliberately *defers* the wake
   when the screen returns with the app not in front (`wakePending = true`, `deviceActionNone`). The
   nudge is released only when the app comes forward. A user who wakes the phone, uses another app,
   and later returns to a stalled stream has therefore seen no wake at all in the core's terms.

---

## 5. The Android app is correctly designed, and that is not enough

The ownership rule the KB states is upheld in the code:

- `PlatformFacts` forwards **raw facts** only. There is no threshold, mapping or branch in the trim
  path, and none in the screen or foreground paths.
- **No Kotlin-layer socket policy exists.** A scan of the app sources for `closeAll`,
  `resetNetwork`, `wakeAll` returns two hits, both in `ConnectionsViewModel.closeAll()` and
  `ConnectionsPage.kt` — a **user-initiated** "close all connections" button. That is a legitimate
  UI action, not a lifecycle policy.
- `core-integration.md` records `BLOCKED_BY_CORE_ABI` for a synthesised resume edge, and the app does
  not synthesise one.

So the app does the right thing: it reports facts and lets the core decide. The defect is that the
core's decision, for this failure mode, is *nothing*. **A correctly-wired client cannot compensate
for a core that has no steady-state liveness path**, and adding Kotlin-side socket killing to
compensate would violate the stated ownership boundary and the brief's §14 forbidden list.

This is why the classification is `SHARED_CORE_BUG` and not `CLIENT_WIRING_BUG`: there is no wiring
error to fix on the Kotlin side.

---

## 6. Why not the other classifications

| Classification | Verdict | Reasoning |
|---|---|---|
| `SHARED_CORE_BUG` | **SELECTED** | the mechanism is in platform-neutral core code, is proven reproducible, and Android reaches it with fewer escape hatches than iOS |
| `CLIENT_WIRING_BUG` | Rejected | the facts are forwarded correctly and completely; the app has no policy that could be wrong here, and the KB's ownership rule is upheld in the sources |
| `PROTECTED_BY_DIFFERENT_PATH` | Rejected | Android has **no** protection. `SetScreenOn`/`SetForeground`/`MemoryTrim`/`ReportDeviceWake` all bypass `Reclaim`. The one thing that would protect it — TUN kernel splicing — does not apply, because the reported flows arrive through `mixed-in`, which `spliceConnection` refuses (`spliceReasonSourceNotGoConn`) |
| `NEEDS_DEVICE_FOR_TRIGGER_ONLY` | Rejected as the *classification* | a device is needed to observe the symptom, not to explain the risk. The code path is fully readable and the mechanism is reproduced on this host |

---

## 6a. Re-confirmation after the server-chain audit

The brief requires this conclusion to be re-checked against the final root cause rather than carried
over. It has been, and the answer depends on which question is asked — so both are stated separately.

**The core defect is shared, and that does not change.** `route.ConnectionManager` and
`connectionCopy` carry no build tag, so Android, iOS, Windows and the VPS all execute the same copy
path with the same absent steady-state liveness mechanism, and a flow in that state is never reclaimed.
Nothing in the server-chain finding removes the gap from the Android binary.

**But a shared vulnerability is not a shared root cause of the reported incidents.** If
`FIRST_STALL_LAYER` turns out to be the VPS→residential hop, the reported stalls began downstream of
every client, and Android — like Windows and iOS — is only where the backpressure surfaced:

```text
ANDROID_RISK (exposure to the shared core gap)  = SHARED_CORE_BUG      [unchanged]
ANDROID'S ROLE IN THE REPORTED INCIDENTS        = SYMPTOM LAYER ONLY   [if the first stall is server-side]
```

These do not conflict, and collapsing them into one label is what the brief warns against. Android
still carries the defect and would still stall unaided if its own peer went silent; that is a different
claim from "Android caused the reported stalls".

**One thing changes, and it strengthens the case rather than weakening it.** If the fix turns out to be
server-side only, Android inherits **nothing** — it stays exposed to the core gap, because a server fix
does nothing for a client whose own peer goes silent. That makes Android's exposure more consequential,
not less, and it is an argument for fixing the core rather than only the server.

A downgrade to `PROTECTED_BY_DIFFERENT_PATH` would be wrong in either world: Android has no protection,
and §2 shows that none of the four facts it forwards can reach `Reclaim`.

---

## 7. What a device would add

`DEVICE_ONLY_REMAINING` for Android:

1. The sequences in the brief's §16 D — `SCREEN_OFF`, `SCREEN_ON`, `USER_PRESENT`, and
   `SCREEN_OFF`/`SCREEN_ON` with no `USER_PRESENT` — against a **blackholed** flow. The prediction
   from §2 is that **none of them ends the flow**, because none reaches `Reclaim`. A positive result
   would falsify the analysis and is the most valuable experiment available.
2. The same sequences against a **healthy** idle flow, to confirm the control.
3. `foreground` false↔true interleaved with a network event, to check the deferred-wake path
   (`wakePending`) does not accidentally produce a reclaim.

Prediction to test: `SCREEN_OFF → SCREEN_ON → USER_PRESENT` on a stalled flow leaves it **still
stalled**; only a genuine connectivity change clears it.

---

## 8. Consequence for the fix

The investigation's §4a changed what a fix can be keyed on. It is no longer necessary to guess at a
timeout: a stalled flow can be **identified** as one whose two copies are mutually parked on a single
outbound descriptor, one writing and one reading, with no progress. Nothing Android-specific is
involved in that signal — it is a property of the shared copy loop — so the fix is written once, in
the core, and every platform inherits it.

If that fix is adopted, Android inherits it with **no app change**, which is the strongest argument
for fixing it in the core rather than in any client. A client-side workaround would have to be
written three times, would each violate its platform's ownership boundary, and would still be
guessing at a policy the core already has the state to decide (`managedConnState.lastActive`,
already maintained, already whole-second; and `ReclaimIdle`, defined, tested, and with no production
caller).

The Android-side work this leaves is **verification, not implementation**: confirm on a device that
the sequences in §7 leave a blackholed flow stalled, and confirm that a healthy idle flow is not
touched.

```text
ANDROID_RISK                  = SHARED_CORE_BUG
ANDROID_APP_WIRING_DEFECT     = NONE FOUND
ANDROID_EXTRA_PROTECTION      = NONE
ANDROID_TRIGGER_SET           = connectivity change only
ANDROID_WORSE_THAN_IOS         = YES (iOS gets a reclaim on every wake; Android does not)
ANDROID_SAME_CLASS_AS_WINDOWS  = YES (the reported stalling case, which had no sleep/wake)
APP_CHANGE_REQUIRED_FOR_FIX    = NO
ANDROID_WORK_REMAINING         = VERIFICATION ONLY (no implementation)
```
