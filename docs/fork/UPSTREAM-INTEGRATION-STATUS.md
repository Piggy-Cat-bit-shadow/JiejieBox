# Upstream integration status

- generated: 2026-10-24T13:10:00+08:00 (phase 02/06)
- fork testing code: `1255f5aa086d4facd5d6111ff900c866a062a5ee`
- fork testing docs: `eb9c8eebd300240559e72b649fd9185ff0525b19`
- upstream observed: `a6952ccf6907c18e18917fcb763a433e8e36937b` (2026-10-24T10:08:02+08:00, "Remove stale workflow")
- behind_by: **110**; ahead_by: 2597
- open semantic: **17**; open dependency blockers: **4**

**Phase 02 closes three rows** (`cc022098fdec`, `38bf6879bbad`, `9c24112d12d5`) and
**DEP-SING-1**. 17 of the audit's 22 `MISSING_FIX` rows remain; the audit JSON is still the source of
truth for classification.

## Closed in phase 02

| id | upstream | what landed | evidence | not verified |
|---|---|---|---|---|

## Closed in phase 01

| id | upstream | what landed | evidence | not verified |
|---|---|---|---|---|
| MAIN-18aa2cdfe742 | `18aa2cdfe742` | `iproute2_table_index` defaults only when `auto_route`; system VPN device no longer claims table 2022 / rule priority; sing-tun dev adds `::/0` for a no-auto-route IPv6 address, clamps the rule priority above the redirect rules and deletes by (family, oif, table) | `protocol/tun/inbound_route_test.go`, `transport/device/system_tun_options_test.go`, sing-tun `tun_linux_test.go`, and the privileged `tun_linux_privileged_test.go` (two real TUN devices, rules, routes, cleanup) on a Linux runner | Android device only |
| MAIN-ef0b632920ff | `ef0b632920ff` | 8-slot semaphore around the binder `JudgeFlow`, released by defer | `experimental/libbox/redirect_linux.go`, `redirect_judge_linux_test.go` (PASS on a Linux runner) | Android binder behaviour on device |

## Dependency closure (sing-tun)

| id | status | evidence |
|---|---|---|
| DEP-TUN-0 | COMPLETED | `dev` tip dcf84577d0eb; port commit 1559fbd506eb; merge 17a6a699750980284cae5d4e04a30720c543ccc2 from `sync/upstream-fixes-2026-10-24` 88e6d8160cf5. The sync branch was a rebase of dev's commits onto a newer upstream base, so the resolved tree equals the sync tree; dev keeps `(handled, remove)`, PacketBufferPort, return batching, NAT/fragment and the Go-stack close contract. Style commits 17db765/4c4e5a2 and the privileged test/workflow dcf8457 follow. |
| DEP-TUN-1 | COMPLETED | `1559fbd`: closed TCP sessions expire on `tcpClosingTimeout`, a late SYN gets a fresh mapping, concurrent sweep re-checks map identity. Tests `stack_system_nat_test.go`. |
| DEP-TUN-2 | COMPLETED | `1559fbd`: no-auto-route IPv6 default route, priority clamp above `redirectRouteRulePriority`, delete by identity. Tests `tun_linux_test.go`. |
| DEP-TUN-3 | COMPLETED | `1559fbd`: bounded held packets (16/flow, 512/stage), async `JudgeFlow`, resume pump on System/Mixed/Go/gvisor and nfqueue, close drops judgements. Tests `flow_dispatch_judge_test.go`. |
| DEP-SING-1 | COMPLETED | `sing/dev` 4200a04 (merge a965eff of the previously pinned side branch, pause port 533bbb8, socks test cleanup 4200a04). `service/pause` tests pass under `-race` locally and in the sing test workflow (all six jobs). |
| DEP-QUIC-1 | OPEN | audit mod.12 vs fork; preserve buffer pinning |
| DEP-QUIC-2 | OPEN | blocked by DEP-QUIC-1 |
| DEP-CRONET-1 | OPEN | atomic root/all/lib + ABI matrix |
| DEP-TAILSCALE-1 | OPEN | upgrade external module if API present |

## MASQUE inbound backpressure

Wiring is present: `WaitWriteInboundBuffers` �?`transport/device` �?`protocol/masque`
(`ClientEndpoint`/`ServerEndpoint`), with the non-blocking fallback kept.

Real-path evidence added this phase:

- `transport/masque/inbound_backpressure_real_test.go` �?a real capsule is parsed by the session,
  the full device parks the read loop, a drained slot releases exactly one write, `Close` returns
  `os.ErrClosed`, and a handler with no wait contract uses the non-blocking path.
- sing-tun `tun_memory_wait_test.go` �?the underlying `MemoryTun`: full queue parks, a freed slot
  wakes, `Close` wakes with `os.ErrClosed`, the non-blocking path drops at the bound.

Not verified: a live QUIC client/server datagram session on a device.

## Effective pins

Root and nested `test` module both replace `github.com/sagernet/sing-tun` with
`github.com/Piggy-Cat-bit-shadow/sing-tun v0.0.0-20261024025840-dcf84577d0eb` and
`github.com/Piggy-Cat-bit-shadow/sing v0.9.6-0.20261024041103-4200a04a089a`. `go list -m -json`
shows the same `Replace.Dir` in both; `go mod tidy`/`verify` are clean and `go build ./...` passes.

## CI

- sing-tun: unchanged from phase 01. Lint green; test red only on the pre-existing Go-stack set
  (subset of the pre-merge baseline), with the privileged dual-TUN workflow green.
- sing: **test SUCCESS** on `4200a04` (run 40061343168, rerun of one flaky SOCKS pool assertion; all
  six Linux/macOS/Windows jobs pass). **lint FAILURE** (run 40061343203) on repository-wide
  pre-existing gci/gofumpt/modernize/paralleltest issues; `service/pause` itself is clean.
- Main repo **Verify SUCCESS** on `1255f5aa086` (run 40063702396), **Android Core ARM64 SUCCESS**
  (run 40063702432), **Mobile contract verification SUCCESS** (run 40063710577, dispatched; the push
  run was cancelled by the newer commit).
- **linux-netns-verify SUCCESS** on `1255f5aa086` (run 40063708065): real unshare namespace holder,
  holder signal policy, root user-namespace predicate, capability guard, ICMP strategy and WireGuard
  pause tests.

## Open semantic rows (17)

| id | prio | upstream | next action |
|---|---|---|---|
| MAIN-42cae14c63d1 | P0 | Fix Apple TLS engine connection leak | Port the `F_DUPFD_CLOEXEC` engine change; leak grows per TLS connection. |
| MAIN-1cc885e0400e | P1 | Provide default route to Tailscale | Bump tailscale for `netmon.SetDefaultRouteFunc` and register it. |
| MAIN-40bc411e8d02 | P1 | Fix connection owner lookup on legacy Android | Libbox change plus Android client switch away from procfs. |
| MAIN-42f487bd0d04 | P1 | Fix IPv6 literal host handling | Bundles http3 `SetNextProtos(h3)` and derp URL dialer changes. |
| MAIN-54c60dc7e6cc | P1 | Fix DHCP DNS search domain expansion | Expand only when `mDNS.CountLabel(name)==1`; validate first. |
| MAIN-6fb6770cec52 | P1 | Keep systemd-resolved servers when configuration is unchanged | Split `checkResolved` into scopes with an `equal()` early return. |
| MAIN-73698e437479 | P1 | Update built-in certificates | Generated data only; run the generator. |
| MAIN-755150a5131e | P1 | Fix config format output | Reproduce DERP `stun.listen_port` loss through `format`. |
| MAIN-7a0ae5f35889 | P1 | Fix tailscale short name and DNS route matching | Restore local-route NXDOMAIN and MagicDNS short-name preference. |
| MAIN-a130b482d41f | P1 | Flush systemd-resolved cache on DNS changes | Use the sing-tun `LinuxTUN.FlushDNSCache` now present on dev. |
| MAIN-a8a2964592c7 | P1 | Fix systemd-resolved detection for local DNS server | Always attempt the resolved resolver; add `SystemdResolved` detection. |
| MAIN-b6fc3a486355 | P1 | Remove unimplemented direct rule action | Delete the action or implement direct dialing. |
| MAIN-c8ba005e84d2 | P1 | Reject Tailscale SSH user switch without privilege | Fail closed plus a clear diagnostic. |
| MAIN-db4e5da711ea | P1 | Check DNS address filter rules on cache hit | Restore the checker contract on cache hits. |
| MAIN-972a052ef537 | P2 | Remove unused DNS search expansion | Decide which of the two copies survives. |
| MAIN-ae4826a387e6 | P2 | Remove command server listen retry | Only changes read-only `sBasePath` timing. |
| MAIN-b02b21b3f3aa | P2 | Run systemd service as root | Packaging: drop `User=`/capability lines and helper files. |

## Next executable action

`MAIN-42cae14c63d1` (Apple TLS engine connection leak, P0), then `MAIN-1cc885e0400e`
(Tailscale default route). `MAIN-cc022098fdec` is closed in this phase; its remaining device-level
suspend/resume check is recorded above as not verified.

## Evidence notes

- `go test` was run on Windows for `protocol/tun`, `transport/device` and `transport/masque`; the
  sing-tun packages were run with `-race` where the C toolchain is available.
- Linux-only files (`tun_linux.go`, `libbox/redirect_linux.go` and their tests) cross-compile
  (`GOOS=linux go test -c`); they were not executed on a Linux device, which is stated per row.
- `internal/winipcfg` tests fail in this environment with `ERROR_NOT_FOUND` (no suitable interface)
  and are unrelated to this phase; they are `BLOCKED_BY_ENVIRONMENT`, not PASS.
- `TestGoKernelPacketBatchIdle` fails identically on the pre-merge `sync/upstream-fixes-2026-10-24`
  tree, so it is a pre-existing host issue, not a regression from the convergence.
