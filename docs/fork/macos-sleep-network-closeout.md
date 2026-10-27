# macOS lid-close network recovery — close-out

**Status: fixes applied on `testing`, no version bump, no tag, no release.**

The report covers the macOS "closed the lid, screen off for a while, no network after wake" defect and
the three smaller defects found while tracing it end to end. It records what was changed, what was
proven on this host, and what still needs a physical Mac.

## 1. The failure chain

A closed lid suspends the whole process: the NetworkExtension, the headless client, and the Go
runtime inside either. Nothing in that process is told which sockets died, and the machine usually
comes back on the same Wi-Fi network:

```text
sleep
  ├─ every outbound pool still holds its sockets
  ├─ the radio is off, so those sockets are black-holed
  └─ no goroutine runs, so no keepalive or deadline fires
wake
  ├─ the interface is the SAME: name, index, gateway, SSID
  ├─ NWPathMonitor has no transition to report (or the client dedups an identical path)
  └─ the core has no other wake signal  ->  no reset  ->  stale pools keep being handed out
```

The only thing in the core that ends such a flow is `ConnectionManager.Reclaim`, reached only from a
network reset. On Linux/Windows a real power listener supplies the event; on macOS nothing did.

## 2. What was wrong, and what changed

### 2.1 macOS never entered the device axis

`CommandServer.Pause`/`Wake` returned for anything that was not Android or iOS
(`if !(C.IsAndroid || C.IsIos) || C.IsTvOS { return }`). So a Mac that slept had no sleep episode, no
reuse verdict, and no reset — `docs/fork/post-wake-reuse.md` §6 recorded exactly that gap.

Changed:

* `experimental/libbox/command_server.go` — `Pause`, `Wake` and `restoreDeviceLevel` accept
  `C.IsDarwin`. `Wake` lifts the device level (`Box.DeviceWoke`) and calls the new
  `SystemResumed` on the network manager. `Pause` and `Wake` now also keep the reload mirror in
  step on Android/macOS; a mirror left saying "paused" would have been re-applied by the next
  reload and re-entered the level on an awake machine.
* `route/network.go` — `NetworkManager.SystemResumed` releases a latched device level, re-observes
  the environment and applies a **duration-aware** reset: below 30 seconds the medium drain preserves
  a stream that may have survived a brief sleep; at 30 seconds and above the **hard (dead-path)**
  reset closes every managed flow, including an active TCP stream that a drain cannot prove dead.
  The sleep episode number is the dedup key: the wake and the watchdog reports for one sleep share
  it, and a second sleep gets a new number, so a fixed coalescing window can neither double-reset nor
  swallow an independent resume.

### 2.2 A headless Mac has no sleep signal at all

The CLI has no NetworkExtension, so even the fixed `Pause`/`Wake` pair is never called.

Changed:

* `common/suspendwatch` (new) — a detector that compares the wall clock with the monotonic clock.
  On Darwin the monotonic clock stops while the machine sleeps, so a wall advance the monotonic
  clock did not see is a suspend, not scheduler latency. A backward wall step re-primes the
  detector instead of manufacturing a verdict.
* `route/network.go` — on `C.IsDarwin`, `NetworkManager` starts the watcher in `PostStart` (5 s
  interval, 5 s threshold) and stops it through the scope. A firing watcher logs
  `system resumed after <gap> suspended; resetting the network` and calls `SystemResumed`.
  The watchdog is a watchdog: it is not gated on sleep state.

### 2.3 The default-interface monitor could publish after Close

`platformDefaultInterfaceMonitor.updateDefaultInterface` checked `closed` when it entered, then
spent real time in `RefreshInterfaces`, and wrote the default interface afterwards without
re-checking. A `Close` inside that refresh was invisible, so a dead monitor could overwrite the
wrapper shared with the monitor that replaced it.

Changed: `experimental/libbox/monitor.go` — the fence is re-checked under the same lock the write
takes. `experimental/libbox/monitor_close_fence_test.go` holds the refresh open, closes the
monitor, and asserts that neither the shared field nor a callback was written.

### 2.4 The half-close diagnostics could split one release

This was the red macOS gate on v0.1.28 (`TestHalfCloseVerdictAndTeardownRaceSettleOnce`, attempt 1):
`released == peer reset + keepalive timeout` was promised by every snapshot, but `recordReleased`
published `released` and the verdict as two independent atomic adds. A reader between them saw
`released=1` with an empty partition. Fixed in `9c1b7fc` (one mutex for the settle counters and
`observed`); `route/conn_halfclose_diagnostics_test.go` adds the concurrent regression and the
timeout half.

## 3. Evidence

| Check | Result |
|---|---|
| `go build -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./...` | **PASS** |
| `go test -run '^$' -tags "$OTHERS" ./...` (every test binary compiles and links) | **PASS** |
| `go test ./common/suspendwatch -count=5` | **PASS** |
| `go test ./route -run 'TestSystemResumed' -count=1` | **PASS** — 9 tests: duplicate coalescing, a second sleep immediately after the first, watchdog detections, short-sleep medium, long-sleep dead path, and the watchdog-gap policy |
| `go test ./experimental/libbox -run 'TestPlatformMonitor' -count=1` | **PASS** |
| `go test -p 1 ./route ./experimental/libbox ./common/suspendwatch ./common/power . -count=1` | **PASS** — route 440.690 s, libbox 1.203 s, suspendwatch 0.078 s, power 4.781 s, root 10.188 s |
| `go test ./route -short -count=1` on the final source | **PASS** — 80.643 s; skips only the six-minute kernel measurement the full run above covers |
| CI `Verify` for `0410c9d` (run `40485312923`) | **PASS** — the Linux source gate: gofmt, builds, test binaries, upstream assumptions, race checks |
| CI `macOS arm64` for `0410c9d` (run `40485385846`) | **PASS** — `gofmt`, unit tests under the macOS tags (including the route suite and the previously flaky half-close test), build, tag/version agreement, headless smoke and capability audit |
| CI `Apple client` libbox job (run `40485553778`) | **PASS** — Libbox built from this fork with cgo on macOS, Apple regression tests passed |
| CI `Verify` for the review closeout `fc287fb` (run `40500862220`) | **PASS** — source gate and race checks; the first race attempt hit the repository's known 10 s `TestTeardown_OneSidedCompletionNeverRunsOnClose` load flake, and the rerun passed |
| CI `macOS arm64` for `fc287fb` (run `40500894327`) | **PASS** — unit tests under the macOS tags (9 resume tests plus the watcher prime regression), build, tag/version agreement, headless smoke and capability audit |
| CI `Apple client` for `fc287fb` (run `40500904125`) | **PASS** — libbox, the full ABI gate against the client call sites, and the macOS DMG |
| Physical Mac (lid close, same Wi-Fi, wake) | **NOT RUN — `UNVERIFIED`** |

The macOS-specific branch cannot be executed on this Windows host: `C.IsDarwin` is a compile-time
constant, and the NetworkExtension callbacks need a signed Apple build. The code paths are compiled
by `go build` on every platform, and the decision logic is unit-tested; the device line above is the
measurement that remains.

## 4. What a Mac run should show

1. Start the app or the headless client with a configuration whose outbound pools are in use.
2. Close the lid for at least a minute, then wake the machine on the same Wi-Fi.
3. The log should show one of:
   * `system resumed after <gap> suspended; resetting the network` (headless path), or
   * the NetworkExtension `wake()` path's reset, with the device level lifted.
4. Follow it with exactly one reset (`updated default interface` / DNS re-pin), not a stream of
   resets: medium below 30 seconds, dead-path at or above. A second lid-close/wake inside the
   watchdog interval must produce its own reset: the dedup key is the sleep episode, not a clock
   window.
5. A new request after wake must work without restarting the tunnel. After a sleep of 30 seconds or
   more an active stream is expected to be closed by the dead-path reclaim - that is the proof that
   no pre-sleep socket is left black-holed, not a failure; the application reconnects. A shorter
   sleep may preserve a stream that is still healthy.

If step 3 shows the watchdog line but step 4 repeats, the threshold is interacting with a platform
whose wall/monotonic clocks behave differently from the recorded Darwin behaviour — capture the gap
and the two clock readings before changing the constant.

## 5. Deliberately not changed

* `release/JIEJIE_VERSION` stays `0.1.28`; no tag, no publish, no artifact.
* The Apple client pin is unchanged: the fix is in the core and in the macOS client's existing
  `sleep()`/`wake()` call sites, so no new Apple source is required.
* The `macOS` resume path is **duration-aware**: below 30 seconds the medium drain preserves a
  stream that may still be healthy; at 30 seconds and above every managed flow is closed, which is
  the only action that proves the pre-sleep sockets are gone. The medium drain remains the policy for
  an interface/environment transition and for short sleeps.
