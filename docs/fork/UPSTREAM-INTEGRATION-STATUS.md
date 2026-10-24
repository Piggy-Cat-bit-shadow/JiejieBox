# Upstream integration status

- generated: 2026-10-24T16:10:00+08:00 (phase 03/06)
- fork testing code: `56a804e6eedb761b1b8b067465f4eb1c7f8f616d`
- fork testing docs: `eb9c8eebd300240559e72b649fd9185ff0525b19`
- upstream observed: `a6952ccf6907c18e18917fcb763a433e8e36937b` (2026-10-24T10:08:02+08:00, "Remove stale workflow")
- behind_by: **110**; ahead_by: 2606
- open semantic: **17**; open dependency blockers: **2**

**Phase 03 closes DEP-QUIC-1 and DEP-QUIC-2** and leaves CRONET and TAILSCALE as the two
dependency blockers. No semantic audit row is closed this phase; 17 of the audit's 22 `MISSING_FIX`
rows remain.

## Dependency closure (QUIC, phase 03)

`quic-go` has two intentionally separate module lines. Only the Sagernet line is consumed here:

| line | module | role | this phase |
|---|---|---|---|
| `jiejie-masque-mod9` | `github.com/sagernet/quic-go` | sing-box/sing-quic release line | fast-forwarded to `ce02ee5c` (mod.10-12 + tests + fork verify workflow) |
| `jiejie-masque-stable` | `github.com/metacubex/quic-go` | independent MASQUE projects (connect-ip-go, jiejie-masque-unified) | untouched, remains repository default |

Merging the two was rejected with evidence: different module paths, 137 conflicting production files
on a trial merge, and a second incompatible owned-DATAGRAM API. The release line is pinned by SHA;
the default branch stays the MetaCubeX line so independent projects keep their baseline.

## Closed in phase 02

| id | upstream | what landed | evidence | not verified |
|---|---|---|---|---|
| MAIN-cc022098fdec | `cc022098fdec` | pause.Manager WaitActiveContext and race-free active channel (sing dev 533bbb8); WireGuard waits on bindCtx, interruptible retry, pause applied at publish | `service/pause/pause_test.go`, `client_bind_pause_test.go`, `pause_deadlock_test.go`; sing test workflow green | Android/Apple device suspend |
| MAIN-38bf6879bbad | `38bf6879bbad` | ICMPControl applies strategy/interface types; serialInterfaceControl candidate order; direct outbound one control | `icmp_strategy_test.go`, TUN ICMP loopback test, Linux unit run | real multi-interface ICMP socket |
| MAIN-9c24112d12d5 | `9c24112d12d5` | cwd pin, capability guard, CAP_NET_BIND_SERVICE, holder ignores INT/TERM/HUP, ownsUserNamespace nsenter hint | `linux-netns-verify` 40063708065: real unshare, signal, predicate, guard | non-root user namespaces under permissive host policy |

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
| DEP-QUIC-1 | COMPLETED | Sagernet line `jiejie-masque-mod9` at `ce02ee5c`: mod.10 EMSGSIZE read, mod.11 trust_anchors, mod.12 application-limited, on top of the owned-DATAGRAM/ceiling work. Targeted tests, local `go test`/`-race`, linux/darwin/windows/android builds. `jiejie-masque-stable` is the separate `github.com/metacubex/quic-go` line, unmodified; repository default restored to it. |
| DEP-QUIC-2 | COMPLETED | `sing-quic/main` at `5e5a027`: upstream `7e0a5869` plus the ceiling/owned-datagram commits, resolving quic-go to `ce02ee5c`. Build/tests/race and four-OS builds pass. |
| DEP-CRONET-1 | OPEN | atomic root/all/lib + ABI matrix |
| DEP-TAILSCALE-1 | OPEN | upgrade external module if API present |

## MASQUE inbound backpressure

Wiring is present: `WaitWriteInboundBuffers` �`transport/device` �`protocol/masque`
(`ClientEndpoint`/`ServerEndpoint`), with the non-blocking fallback kept.

Real-path evidence added this phase:

- `transport/masque/inbound_backpressure_real_test.go` �a real capsule is parsed by the session,
  the full device parks the read loop, a drained slot releases exactly one write, `Close` returns
  `os.ErrClosed`, and a handler with no wait contract uses the non-blocking path.
- sing-tun `tun_memory_wait_test.go` �the underlying `MemoryTun`: full queue parks, a freed slot
  wakes, `Close` wakes with `os.ErrClosed`, the non-blocking path drops at the bound.

Not verified: a live QUIC client/server datagram session on a device.

## Effective pins

Root and nested `test` module both replace `github.com/sagernet/sing-tun` with
`github.com/Piggy-Cat-bit-shadow/sing-tun v0.0.0-20261024025840-dcf84577d0eb` and
`github.com/Piggy-Cat-bit-shadow/sing v0.9.6-0.20261024041103-4200a04a089a`, `github.com/Piggy-Cat-bit-shadow/quic-go v0.61.1-0.20261024063732-ce02ee5cc210` and `github.com/Piggy-Cat-bit-shadow/sing-quic v0.7.2-0.20261024074641-5e5a0274bd6c`. `go list -m -json`
shows the same `Replace.Dir` in both; `go mod tidy`/`verify` are clean and `go build ./...` passes.

## CI

- quic-go: Actions is disabled for the repository, so no workflow run exists. On `ce02ee5c`:
  `go build ./...`, `go test ./...`, `go test -race ./...` and linux/darwin/windows/android builds
  pass locally.
- sing-quic: no Actions run. On `5e5a027`: build, `go test ./...`, `-race` and the same four-OS
  builds pass locally.
- Main repo **Verify SUCCESS** on `56a804e6eed` (run 40075546655), **Android Core ARM64 SUCCESS**
  (run 40075546704), **Mobile contract verification SUCCESS** (run 40075546654).
- **linux-masque-verify RED** on `56a804e6eed` (run 40072195794): `TestMASQUESelfToSelf/HTTP2` and
  `TestMASQUEPacketTooBig` time out. The phase-02 baseline run `40073782175` fails the same two
  cases, while HTTP3, HTTP1, version fallback, advertised routes, no-route, reconnect and stuck
  client PASS. `transport/masque` and `protocol/masque` pass under `-race`; the Linux gate also
  exposed and fixed a nil-signal crash in the resolved DNS resolver.

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

`DEP-CRONET-1` (phase 04, atomic root/all/lib + ABI matrix), then `MAIN-42cae14c63d1` (Apple TLS engine connection leak, P0). `MAIN-cc022098fdec` is closed; its device-level suspend/resume caveat is recorded above.

## Evidence notes

- `go test` was run on Windows for `protocol/tun`, `transport/device` and `transport/masque`; the
  sing-tun packages were run with `-race` where the C toolchain is available.
- Linux-only files (`tun_linux.go`, `libbox/redirect_linux.go` and their tests) cross-compile
  (`GOOS=linux go test -c`); they were not executed on a Linux device, which is stated per row.
- `internal/winipcfg` tests fail in this environment with `ERROR_NOT_FOUND` (no suitable interface)
  and are unrelated to this phase; they are `BLOCKED_BY_ENVIRONMENT`, not PASS.
- `TestGoKernelPacketBatchIdle` fails identically on the pre-merge `sync/upstream-fixes-2026-10-24`
  tree, so it is a pre-existing host issue, not a regression from the convergence.
