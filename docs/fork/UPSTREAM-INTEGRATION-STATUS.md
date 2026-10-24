# Upstream integration status

- generated: 2026-10-24T09:45:00+08:00 (phase 01/06)
- fork testing code: `6af4a4a0925a98abe6dc8d0308d5317052c648e1`
- upstream observed: `824dbcfb4dd0a95921840b1737b950cde590241e` (2026-10-23T22:39:57+08:00)
- behind_by: **109**; ahead_by: 2583
- open semantic: **20**; open dependency blockers: **5**

**Ledger correction.** The previous status said 23 open semantic rows while the report said 22. The
audit JSON is the source of truth: 22 `MISSING_FIX` rows, and `3454d6d2411d` (MASQUE inbound
backpressure) is `PRESENT`/`NO_ACTION` in the audit. The markdown table had counted it as open. This
phase closed 2 of the 22, so 20 remain.

## Closed in phase 01

| id | upstream | what landed | evidence | not verified |
|---|---|---|---|---|
| MAIN-18aa2cdfe742 | `18aa2cdfe742` | `iproute2_table_index` defaults only when `auto_route`; system VPN device no longer claims table 2022 / rule priority; sing-tun dev adds `::/0` for a no-auto-route IPv6 address, clamps the rule priority above the redirect rules and deletes by (family, oif, table) | `protocol/tun/inbound_route_test.go`, `transport/device/system_tun_options_test.go`, sing-tun `tun_linux_test.go` (linux build) | real Linux route/rule table with two TUNs (`NOT_VERIFIED_ON_DEVICE`) |
| MAIN-ef0b632920ff | `ef0b632920ff` | 8-slot semaphore around the binder `JudgeFlow`, released by defer | `experimental/libbox/redirect_linux.go`, `redirect_judge_linux_test.go` (linux build) | Android binder behaviour on device |

## Dependency closure (sing-tun)

| id | status | evidence |
|---|---|---|
| DEP-TUN-0 | COMPLETED | `dev` tip 4c4e5a2d6e19; port commit 1559fbd506eb; merge 17a6a699750980284cae5d4e04a30720c543ccc2 from `sync/upstream-fixes-2026-10-24` 88e6d8160cf5. The sync branch was a rebase of dev's commits onto a newer upstream base, so the resolved tree equals the sync tree; dev keeps `(handled, remove)`, PacketBufferPort, return batching, NAT/fragment and the Go-stack close contract. Two follow-up style commits (17db765, 4c4e5a2) make the lint workflow pass on every platform target. |
| DEP-TUN-1 | COMPLETED | `1559fbd`: closed TCP sessions expire on `tcpClosingTimeout`, a late SYN gets a fresh mapping, concurrent sweep re-checks map identity. Tests `stack_system_nat_test.go`. |
| DEP-TUN-2 | COMPLETED | `1559fbd`: no-auto-route IPv6 default route, priority clamp above `redirectRouteRulePriority`, delete by identity. Tests `tun_linux_test.go`. |
| DEP-TUN-3 | COMPLETED | `1559fbd`: bounded held packets (16/flow, 512/stage), async `JudgeFlow`, resume pump on System/Mixed/Go/gvisor and nfqueue, close drops judgements. Tests `flow_dispatch_judge_test.go`. |
| DEP-SING-1 | OPEN | `pause.Manager` context-aware wait; netns/ICMP control |
| DEP-QUIC-1 | OPEN | audit mod.12 vs fork; preserve buffer pinning |
| DEP-QUIC-2 | OPEN | blocked by DEP-QUIC-1 |
| DEP-CRONET-1 | OPEN | atomic root/all/lib + ABI matrix |
| DEP-TAILSCALE-1 | OPEN | upgrade external module if API present |

## MASQUE inbound backpressure

Wiring is present: `WaitWriteInboundBuffers` → `transport/device` → `protocol/masque`
(`ClientEndpoint`/`ServerEndpoint`), with the non-blocking fallback kept.

Real-path evidence added this phase:

- `transport/masque/inbound_backpressure_real_test.go` — a real capsule is parsed by the session,
  the full device parks the read loop, a drained slot releases exactly one write, `Close` returns
  `os.ErrClosed`, and a handler with no wait contract uses the non-blocking path.
- sing-tun `tun_memory_wait_test.go` — the underlying `MemoryTun`: full queue parks, a freed slot
  wakes, `Close` wakes with `os.ErrClosed`, the non-blocking path drops at the bound.

Not verified: a live QUIC client/server datagram session on a device.

## Effective pins

Root and nested `test` module both replace `github.com/sagernet/sing-tun` with
`github.com/Piggy-Cat-bit-shadow/sing-tun v0.0.0-20261024014015-4c4e5a2d6e19`. `go list -m -json`
shows the same `Replace.Dir` in both; `go mod tidy`/`verify` are clean and `go build ./...` passes.

## CI

- sing-tun **lint: SUCCESS** on `4c4e5a2` (run 40050850848) after the gci/gofumpt/modernize fixes.
- sing-tun **test: FAILURE** on `4c4e5a2` (run 40050850903). macOS passes; the three Ubuntu jobs and
  Windows fail `TestGoConnKeepalive`, `TestGoKernelZeroWindowHalfClose` and
  `TestGoKernelPacketMapping`. These are pre-existing: the pre-merge `dev` 7539c98 fails the same
  set plus `TestGoKernelFlowControl` and `TestGoKernelPacket` (manual-dispatch baseline run
  40050607262). No new test fails after the convergence.
- The baseline comparison branch `ci-baseline-7539c98` was deleted after the run.
- Main repo workflows (`Verify`, `Mobile contract verification`, `Android Core ARM64`) are
  **PENDING** at ledger write time.

## Open semantic rows (20)

| id | prio | upstream | next action |
|---|---|---|---|
| MAIN-42cae14c63d1 | P0 | Fix Apple TLS engine connection leak | Port the `F_DUPFD_CLOEXEC` engine change; leak grows per TLS connection. |
| MAIN-cc022098fdec | P0 | Fix WireGuard pause and sleep handling | Needs DEP-SING-1 (`pause.Manager` context-aware wait) first. |
| MAIN-1cc885e0400e | P1 | Provide default route to Tailscale | Bump tailscale for `netmon.SetDefaultRouteFunc` and register it. |
| MAIN-38bf6879bbad | P1 | Fix network strategy for ICMP | Control-func equivalent plus default-strategy EPERM tolerance. |
| MAIN-40bc411e8d02 | P1 | Fix connection owner lookup on legacy Android | Libbox change plus Android client switch away from procfs. |
| MAIN-42f487bd0d04 | P1 | Fix IPv6 literal host handling | Bundles http3 `SetNextProtos(h3)` and derp URL dialer changes. |
| MAIN-54c60dc7e6cc | P1 | Fix DHCP DNS search domain expansion | Expand only when `mDNS.CountLabel(name)==1`; validate first. |
| MAIN-6fb6770cec52 | P1 | Keep systemd-resolved servers when configuration is unchanged | Split `checkResolved` into scopes with an `equal()` early return. |
| MAIN-73698e437479 | P1 | Update built-in certificates | Generated data only; run the generator. |
| MAIN-755150a5131e | P1 | Fix config format output | Reproduce DERP `stun.listen_port` loss through `format`. |
| MAIN-7a0ae5f35889 | P1 | Fix tailscale short name and DNS route matching | Restore local-route NXDOMAIN and MagicDNS short-name preference. |
| MAIN-9c24112d12d5 | P1 | Fix unshare network namespace with capabilities | Port all four pieces together. |
| MAIN-a130b482d41f | P1 | Flush systemd-resolved cache on DNS changes | Use the sing-tun `LinuxTUN.FlushDNSCache` now present on dev. |
| MAIN-a8a2964592c7 | P1 | Fix systemd-resolved detection for local DNS server | Always attempt the resolved resolver; add `SystemdResolved` detection. |
| MAIN-b6fc3a486355 | P1 | Remove unimplemented direct rule action | Delete the action or implement direct dialing. |
| MAIN-c8ba005e84d2 | P1 | Reject Tailscale SSH user switch without privilege | Fail closed plus a clear diagnostic. |
| MAIN-db4e5da711ea | P1 | Check DNS address filter rules on cache hit | Restore the checker contract on cache hits. |
| MAIN-972a052ef537 | P2 | Remove unused DNS search expansion | Decide which of the two copies survives. |
| MAIN-ae4826a387e6 | P2 | Remove command server listen retry | Only changes read-only `sBasePath` timing. |
| MAIN-b02b21b3f3aa | P2 | Run systemd service as root | Packaging: drop `User=`/capability lines and helper files. |

## Next executable action

`MAIN-42cae14c63d1` (Apple TLS engine connection leak, P0). `MAIN-cc022098fdec` stays blocked on
DEP-SING-1.

## Evidence notes

- `go test` was run on Windows for `protocol/tun`, `transport/device` and `transport/masque`; the
  sing-tun packages were run with `-race` where the C toolchain is available.
- Linux-only files (`tun_linux.go`, `libbox/redirect_linux.go` and their tests) cross-compile
  (`GOOS=linux go test -c`); they were not executed on a Linux device, which is stated per row.
- `internal/winipcfg` tests fail in this environment with `ERROR_NOT_FOUND` (no suitable interface)
  and are unrelated to this phase; they are `BLOCKED_BY_ENVIRONMENT`, not PASS.
- `TestGoKernelPacketBatchIdle` fails identically on the pre-merge `sync/upstream-fixes-2026-10-24`
  tree, so it is a pre-existing host issue, not a regression from the convergence.
