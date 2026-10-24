# Phase 02 — sing pause API, WireGuard suspend/close, ICMP strategy and Linux namespaces

```text
PHASE = 02/06
RESULT = DONE_WITH_ENVIRONMENTAL_GAPS
SING_DEV_SHA = 4200a04a089a8271a2243971790f15c262f7bcfe
MAIN_TESTING_SHA = 1255f5aa086d4facd5d6111ff900c866a062a5ee (code); eb9c8eebd300240559e72b649fd9185ff0525b19 (docs)
EFFECTIVE_SING_PIN = github.com/Piggy-Cat-bit-shadow/sing v0.9.6-0.20261024041103-4200a04a089a (root and test module)
WIREGUARD_PAUSE = CLOSED: bind waits on bindCtx, interruptible retry, pause applied at publish; close-while-paused and pause-before-first-packet tests pass (race)
ICMP_STRATEGY = CLOSED: ICMPControl applies strategy/interface types on the control path; direct outbound uses one destination-independent control; Linux unit + TUN loopback tests pass
LINUX_NETNS_CAPABILITIES = CLOSED_CODE_AND_LINUX: cwd pin, capability guard, CAP_NET_BIND_SERVICE, holder ignores INT/TERM/HUP, ownsUserNamespace nsenter hint; real unshare test PASS on Linux runner
TESTS_AND_CI_ON_FINAL_CODE_SHA = main Verify 40063702396 SUCCESS, Android 40063702432 SUCCESS, Mobile 40063710577 SUCCESS, linux-netns-verify 40063708065 SUCCESS (all on 1255f5aa086); sing test 40061343168 SUCCESS, sing lint 40061343203 pre-existing failure
STATE_LEDGER_UPDATED = YES
NEXT_PHASE_READY = YES
```

## 1. sing fork (`Piggy-Cat-bit-shadow/sing`)

`dev` was behind two things: the main repo pinned `baed8c3` from the side branch
`fix/splice-setup-failure-fallback-v2`, and `dev` itself did not contain that branch. Phase 01's pin
therefore named a revision the default branch did not carry.

- `a965eff` merges that side branch into `dev` (SOCKS lazy-reply fix, varbin growth, stricter splice
  proof). Conflicts were the two files both sides added for the splice fix; the branch versions are
  newer and stricter.
- `533bbb8` ports upstream `5dc452e` "pause: Make waits race-free and cancellable": one `active`
  channel, idempotent pause/wake, `WaitActiveContext(ctx)`; `WaitActive` delegates with the manager
  context. Deterministic tests cover parked-while-paused, waiter cancel, manager-context cancel,
  source independence, idempotent events, already-cancelled waiters and a concurrent soak; all pass
  under `-race`.
- `4200a04` closes the UDP associate connection in the SOCKS pool test so its server `WaitGroup`
  does not hang cleanup on Windows.

`sing` CI on `4200a04`: **test workflow green on all six jobs** (Linux/macOS/Windows × Go 1.25/1.26)
after rerunning one flaky SOCKS pool assertion. The **lint workflow remains red on repository-wide
pre-existing gci/gofumpt/modernize/paralleltest issues**; `service/pause` is clean.

## 2. Main repo (`testing`)

- `09b2ee37cdd` pins sing to `v0.9.6-0.20261024041103-4200a04a089a` in root and `test/go.mod`.
  `go mod tidy`/`verify`, `go list -m -json` and `go build ./...` pass for Windows/Linux.
- `301ed13f603` WireGuard: `ClientBind.waitActive` uses `WaitActiveContext(c.bindCtx)` and
  `sleepRetry` is a `select` on `done`/timer. A sender parked by a real paused manager is released by
  `Close` (negative control).
- `0097eb9ad32` applies an in-effect pause at publish: `bindListenPort` brings the device up before
  the callback can see it, so the publish step reads `IsNetworkPaused()` and takes it down. Test:
  pause before `Start`, assert `networkPaused`, wake and assert it clears.
- `9ab2bc07427` ICMP: `DefaultDialer.ICMPControl()` and `serialInterfaceControl` use the configured
  strategy and interface types with the same `BindToInterface0` control; candidates are tried in
  order on the one socket, the current default interface needs no bind, and EPERM tolerance is scoped
  to the default strategy. Direct outbound installs one control for both families. Tests cover
  candidate order, fallback types, default-interface skip, EPERM scope, empty set and both families;
  `TestICMPLoopbackToTheTunRangeIsRejected` still passes.
- `20f0e4d455f` Linux: `initialDir` + `command.Dir`, capability guard for a rootless user namespace,
  `CAP_NET_BIND_SERVICE`, holder ignores `SIGINT`/`SIGTERM`/`SIGHUP`, and `ownsUserNamespace()` drives
  the `nsenter -U --preserve-credentials` hint instead of `euid==0`.
- `1255f5aa086` makes the route test doubles implement `WaitActiveContext` (the interface change
  broke their compile; the Mobile low-memory gate builds that package).
- `.github/workflows/linux-netns-verify.yml` is the manual Linux gate for these root-only checks.

Main CI on `1255f5aa086`: Verify, Android Core ARM64, Mobile contract verification and
`linux-netns-verify` all SUCCESS.

## 3. What is not verified

- Device suspend/resume for WireGuard on Android/Apple; the close-while-paused and
  pause-before-first-packet behaviours are covered by deterministic tests.
- A real ICMP socket on a multi-interface device; the strategy decision and the Linux control path
  are covered, and the TUN loopback rejection still passes.
- Non-root user-namespace creation on a kernel/AppArmor policy that permits it. The runner blocks
  unprivileged user namespaces, so the root/unshare and capability-guard halves were executed and
  the non-root path is code-reviewed only (`NOT_VERIFIED_LINUX_NETNS` for that half).
- `sing` lint is not green; it was red before this phase and the failures are repository-wide.

## 4. Ledger

`docs/fork/UPSTREAM-INTEGRATION-STATUS.json` is authoritative: 17 open semantic rows (was 20; the
audit's 22 minus the five closed across phases 01–02) and 4 open dependency blockers
(DEP-QUIC-1/2, DEP-CRONET-1, DEP-TAILSCALE-1). `DEP-SING-1` is completed at sing `dev` `4200a04`.

Next: `MAIN-42cae14c63d1` (Apple TLS engine leak), then `MAIN-1cc885e0400e` (Tailscale default
route). No phase-03 work was started.
