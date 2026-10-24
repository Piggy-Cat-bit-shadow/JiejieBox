# Upstream integration — final report (task stopped 2026-10-24)

**This is not a completion report.** The user asked to end the upstream-sync task and write the final
report. The task is **not** at its own `DONE` bar: 22 applicable `MISSING_FIX` rows remain, the
dependency forks are only partially merged, no ancestry merge was performed, and `behind_by` is 109.

```text
STATUS = IN_PROGRESS (ended by user request)
BRANCH = testing
UPSTREAM_LATEST_OBSERVED = 824dbcfb4dd0a95921840b1737b950cde590241e (2026-10-23 22:39 +0800)
FORK_TESTING_FINAL_SHA = <the commit carrying this report; code SHA 763e9a28e9c0e67d40e7a73bb47d67dc65bb5bdc>
BEHIND_BY = 109
OPEN_SEMANTIC_FIXES = 22
OPEN_DEPENDENCY_BLOCKERS = 5 forks + 3 conflicted sing-tun commits
DEPENDENCY_BRANCHES =
  sing-tun/dev: 7539c9855f19 (default)
  sing-tun/sync/upstream-fixes-2026-10-24: 88e6d8160cf5 (pinned by main)
  sing/dev, sing-quic/main, quic-go/jiejie-masque-stable, cronet-go/main: untouched
EFFECTIVE_PINS = sing-tun -> Piggy/sing-tun v0.0.0-20261023233026-88e6d8160cf5
MAIN_REPO_CI = code SHA 763e9a2: CI triggered on push; previous code SHA 508407c had Verify/Android/Mobile all success
BRANCH_CLEANUP = none (sing-tun sync branch still exists; dev not merged)
UNVERIFIED_PLATFORMS = iOS device, Linux nfqueue/L0, accepted splice, Cronet native ABI
NEXT_EXECUTABLE_ACTION = audit and safely merge sing-tun origin/sync/upstream-fixes-2026-10-24 (88e6d81) into origin/dev (7539c98), then move the main pin to the dev merge commit
```

## 1. What was actually closed

### Main repo (code)

- `fast_path:false` precedence over the intrinsic direct grant; tri-state pinned by production-shape,
  adversarial, and exact-verdict tests.
- `multiFlowTracker` lifecycle serialization (P0); ICMP TUN-loopback rejection; platform-interface
  policy instead of a nil check; wildcard control-plane listeners; DNS response compression; redirect
  `OriginDestination`; WireGuard packet offset and reserved-length error; URLTest failure history;
  external-interrupt typed bool; netns+TFO deferred dial; cached-drain terminal failure; splice
  single-flight/Close barrier.
- OOM: stable-footprint 1 s `FreeOSMemory` loop removed (real opportunity + 512 KiB benefit floor +
  30 s low-yield cooldown + pressure override); `Triggered` exits at `armed`, not `resume`;
  single-flight and Close barrier; `-race` passes on the package.
- Composite Fast Path audit: `NO_EXECUTABLE_SHORTCUT` for system proxy/TUN→proxy direct bypass;
  TUN→direct splice attempt + Copy fallback proven; explicit `action: bypass` vs tracker doc
  corrected; F11 third-buffer test.
- **MASQUE inbound backpressure wiring**: `Device.WaitWriteInboundBuffers`, stack implementation on
  `MemoryTun.WaitWritePackets`, system-stack segment routing, `ClientEndpoint`/`ServerEndpoint`
  blocking variants, and `transport/masque` preferring the blocking handler while keeping the
  non-blocking fallback. Test: `transport/masque/inbound_wait_test.go`.

### Dependency (sing-tun)

Pushed branch `sync/upstream-fixes-2026-10-24` at `88e6d81`, carrying 9 adapted upstream fixes:

- accepted TCP flow entries removed after handshake (`baaa611`), with the `(handled, remove)`
  conflict resolved for this fork's dispatcher;
- systemd-resolved cache flush API (`fba6597`);
- Windows weak-host interface binding (`4faef97`);
- memory TUN blocking inbound write (`ce08a18`);
- TPROXY redirect port reject (`0c41c36`);
- unsupported netlink groups (`94443fe`);
- Android package list watcher (`26d8194`);
- Windows 7 registry watcher (`fe26f6a`);
- ICMP flow drop on write error (`da867c7`).

Main `replace` is pinned to the branch head; root and nested `test` modules are tidy, and
`go build ./...` / `go test ./protocol/tun` pass.

## 2. What is still open

22 `MISSING_FIX` rows, grouped:

- **P0**: Apple TLS engine FD/object leak (`42cae14`), WireGuard pause/sleep (`cc022098`).
- **TUN/DNS**: IPv6 interface rule without auto route (`18aa2cdf`), systemd-resolved detection and
  keep-servers (`a8a2964`, `6fb6770`), resolved cache flush main-repo call (`a130b48`), DHCP search
  expansion (`54c60dc`), DNS cache-hit checker (`db4e5da`).
- **Tailscale**: default route (`1cc885e`), short-name/DNS route (`7a0ae5f`), SSH user switch
  (`c8ba005`).
- **Platform**: Android legacy procfs (`40bc411`), IPv6 literal URL (`42f487b`), Linux netns
  capabilities (`9c24112`), auto-redirect JudgeFlow concurrency (`ef0b632`).
- **Config/ops**: config format round-trip (`755150a`), `direct` rule action (`b6fc3a4`), built-in
  certificates (`73698e4`), search expansion cleanup (`972a052`), command-server retry (`ae4826a`),
  systemd service user policy (`b02b21b`).

Dependency gaps:

- `sing-tun/dev` is **not** merged with the pinned sync branch: 26 dev-only / 49 sync-only commits,
  with add/add conflicts in the go stack (`stack_go.go`, `stack_go_engine.go`, `stack_go_io_*`,
  `stack_go_memory_arena.go`, `tun_memory.go`) and content conflicts in `flow_dispatch.go`,
  `udp_nat.go`, `redirect_iptables.go`. The merge was attempted and aborted cleanly; no partial merge
  remains.
- `84e75a6` (system stack closed-session NAT), `7980862` (IPv6 interface rule), `09dd03d`
  (JudgeFlow non-blocking) are still unported.
- `sing`, `sing-quic`, `quic-go`, `cronet-go` have not been touched; `tailscale` has not been
  upgraded.
- No ancestry merge and no GitHub `behind_by=0` check; the upstream tip is not an ancestor of
  `testing`.

## 3. Why this stopping point is honest

The task's own `DONE` bar requires zero applicable `MISSING_FIX`, effective dependency pins with the
fork fixes actually on the default branches, a final-SHA green CI, and a three-way ancestry check.
None of the last three hold. Using `git merge -s ours upstream/testing` now would make GitHub show
`behind_by=0` while 22 real gaps remain, which the task explicitly forbids. No such merge was done.

The workspace is clean; `origin/testing` equals the local branch; the aborted sing-tun merge left no
worktree changes. The authoritative machine-readable ledger is
[`UPSTREAM-INTEGRATION-STATUS.json`](UPSTREAM-INTEGRATION-STATUS.json); the audit evidence is
[`upstream-2026-10-24-audit.md`](upstream-2026-10-24-audit.md).

## 4. Exact resume point

```sh
# 1. sing-tun: merge the pinned sync branch into dev, preserving both sides
git -C C:/Deepseek/sing-tun-fork switch -c merge-sync-into-dev origin/dev
git -C C:/Deepseek/sing-tun-fork merge --no-ff origin/sync/upstream-fixes-2026-10-24
# resolve the go-stack add/add conflicts, flow_dispatch (handled,remove), udp_nat, redirect_iptables
# 2. push dev, move the main replace to the dev merge SHA, sync test/go.mod
# 3. then 84e75a6 -> 7980862 -> 09dd03d -> MASQUE live backpressure -> remaining 22 rows
# 4. only after that: ancestry merge and the three-way behind_by=0 check
```
