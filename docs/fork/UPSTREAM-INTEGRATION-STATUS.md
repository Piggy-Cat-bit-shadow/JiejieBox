# Upstream integration status

- generated: 2026-10-24T20:00:00+08:00 (phase 06/06)
- fork testing code: `c3c23d1059c8834f68d268995557c99be6d787a0`
- fork testing docs: `0ea3ea30ec31e8a247535a9c9d08a4bcdd9aa3b2`
- upstream observed: `a6952ccf6907c18e18917fcb763a433e8e36937b` (2026-10-24T10:08:02+08:00, "Remove stale workflow")
- behind_by: **0**; ahead_by: 2643
- open semantic: **0**; open dependency blockers: **0**

**Phase 06 closes the last five main-repo rows** (5 -> 0): certificates, format round-trip, the
ghost direct action, command-server retry, and the systemd root policy as a documented rejection.
Ancestry is merged: `behind=0`, with the audit evidence in the phase 06 report.

## Dependency closure (QUIC, phase 03)

`quic-go` has two intentionally separate module lines. Only the Sagernet line is consumed here:

| line | module | role | this phase |
|---|---|---|---|
| `jiejie-masque-mod9` | `github.com/sagernet/quic-go` | sing-box/sing-quic release line | fast-forwarded to `ce02ee5c` (mod.10-12 + tests + fork verify workflow) |
| `jiejie-masque-stable` | `github.com/metacubex/quic-go` | independent MASQUE projects (connect-ip-go, jiejie-masque-unified) | untouched, remains repository default |

Merging the two was rejected with evidence: different module paths, 137 conflicting production files
on a trial merge, and a second incompatible owned-DATAGRAM API. The release line is pinned by SHA;
the default branch stays the MetaCubeX line so independent projects keep their baseline.

## Closed in phase 06

| id | upstream | what landed | evidence | not verified |
|---|---|---|---|---|
| MAIN-73698e437479 | `73698e43747939c38d260e40b4db63ce333bcd6a` | chrome.go/mozilla.go regenerated from the checked-in generator sources (CCADB Mozilla IncludedCA and Chrome RootCACertificatesIncludedByRS); TestIncludedRootCAsAreValid parses every PEM, rejects duplicates and pins 92/161 roots plus SHA-256 of the canonical DER sequence | no live update_certificates network run; data source is the upstream generated commit |
| MAIN-755150a5131e | `755150a5131e5d38ed271bbf2a3bb57b0e5de94c` | omitempty on server_port/security/name/priority and compact DERP STUN port form; format round-trip tests at option and libbox FormatConfig level preserve 443/8443 and omit zero keys; fork sing version needs the option-less action nil guard; restored explicitly | none |
| MAIN-b6fc3a486355 | `b6fc3a486355f4298dabd9b99083848c00db081b` | direct action type/options/constructor/reference/rule handling and schema removed; TestDirectRuleActionIsRejected proves the ghost now fails config decode; route-aware fixtures use reject instead | operators with existing action:direct configs now get a clear error, not silent no-op |
| MAIN-ae4826a387e6 | `ae4826a387e6de58d0625b312851ec0d7526cb26` | CommandServer.Start returns the listen error immediately; 30s retry and syscall/time sleeps removed; TestCommandServerListenFailsImmediately bounds failure <2s on an unusable base path | Android direct-boot EROFS device |
| MAIN-b02b21b3f3aa | `b02b21b3f3aaf97f60a96942ec5cf2f755fb1aeb` | kept User=sing-box, capability bounding/ambient set, sysusers account and scoped polkit rule; TestPackagedServiceKeepsLeastPrivilege locks the policy; root is not required for TUN/routes: CAP_NET_ADMIN/CAP_NET_RAW cover them; split DNS is scoped to the sing-box user | systemd boot on a packaged host; deployments needing root-only features must override the unit deliberately |

## Closed in phase 05

| id | upstream | what landed | evidence | not verified |
|---|---|---|---|---|
| MAIN-1cc885e0400e | `1cc885e0400e6739af0189e5edcc430bd88cbeba` | tailscale dev module 1688b52 supplies netmon.Hooks.DefaultRoute and tsnet.Server.DefaultRouteFunc; system_binding answers both from NetworkManager.InterfaceMonitor().DefaultInterface(); cleanup stays on the binding scope; TestTheInterfaceGetterIsInstalledReadAndCleared drives the hook against a stub interface | real tailnet interface switching/on-demand resume |
| MAIN-7a0ae5f35889 | `7a0ae5f35889c88eade4410c6d981427328c188f` | tailnetDomains.match separates route suffixes, known hosts and single-label search expansion; TestTailnetDomainsMatch covers known host, search expansion on/off, local/forwarded suffixes and unknown names | live MagicDNS NXDOMAIN against a tailnet |
| MAIN-c8ba005e84d2 | `c8ba005e84d2b8f2bcf75c32b5aeda64f6fb06ec` | checkSwitchUserPrivilege requires CAP_SETUID\|CAP_SETGID; verifyShellIdentity rejects a different uid before exec; TestNonRootSwitchUserIsRejected and TestSameUserIsNotASwitch run on the Linux gate | root with partial capabilities on a real host |
| MAIN-a8a2964592c7 | `a8a2964592c7bb55117377eb030a2ab4912829b0` | detection uses /etc/resolv.conf identity against systemd-resolved uplink plus 127.0.0.53 listen addresses; Config.SystemdResolved participates in Equal; Linux gate compiles/runs local packages | containers without resolved and real symlinked resolv.conf variants |
| MAIN-6fb6770cec52 | `6fb6770cec52bf266a95aad387d2071a42978218` | resolvedScopeSpecification.equal compares interface, DoT mode, domains, default-route, fallback and server list; TestResolvedScopeSpecificationEqual covers identical and changed port/domains/default-route | live config-order churn |
| MAIN-a130b482d41f | `a130b482d41f4b3eeac5091567a4b26540700037` | NetworkManager.FlushSystemDNSCache walks inbounds; TUN calls LinuxTUN.FlushDNSCache only when no resolved service owns the cache; resolver update flushes too; dns/router.go guard covers fixtures without a context | real resolved cache invalidation timing |
| MAIN-54c60dc7e6cc | `54c60dc7e6ccbe1b122630201401524280792914` | nameList only expands a single-label name and appends the original name last; TestNameList covers single-label, multi-label, FQDN, empty/duplicate search and IPv6 reverse | real DHCP lease search list |
| MAIN-972a052ef537 | `972a052ef5379bf828a5fe22576a2a25110a6559` | removed the unused local/search expansion path; the explicit search contract remains in nameList and resolved domains | none |
| MAIN-db4e5da711ea | `db4e5da711ea49cc46583987a2792a7c1c7bc05a` | beginExchange now runs responseChecker before returning a cache hit, in both stale and fresh branches; questionCache already did; TestCacheHitRunsTheResponseChecker proves rejection does not query upstream and does not evict | production checker rule updates (currently nil in most paths) |
## Closed in phase 04

| id | upstream | what landed | evidence | not verified |
|---|---|---|---|---|
| MAIN-42cae14c63d1 | `42cae14c63d1b8645d787f3f93515820c94f01e7` | common/tls/apple_client_platform.go: F_DUPFD_CLOEXEC after the anchor lookup, deferred anchor release, dup after ownership transfer; apple_client_platform_darwin.m: autoreleasepool, NW_RETURNS_RETAINED ownership, release the connection object on every path; apple_client_platform_leak_test.go: 30 successful + 10 failed handshakes, fd count flat; macOS gate 40081595184 PASS | iOS device entitlements/NetworkExtension |
| MAIN-40bc411e8d02 | `40bc411e8d024e3f8c8f435c2a2ad5e4c0e04bce` | experimental/libbox/connection_owner_android.go: socket inode/procfs fallback moved out of service.go; experimental/libbox/internal/procfs/procfs.go: table search ranks exact, local and unspecified UDP rows, skips zero inode/uid; procfs_test.go pins native-endian address + big-endian port spelling; linux-platform-verify 40081597501 PASS; android/arm64 cross-compile PASS | real legacy Android device / cross-user procfs permissions |
| MAIN-42f487bd0d04 | `42f487bd0d0482b1b9fd47207cdbbab2dadbfcd3` | dns/transport/https.go, dns/transport/quic/http3.go, service/derp/service.go: helper renders IPv6 literals bracketed, domains/IPv4 untouched; common/tls/reality_client.go, transport/v2rayhttp/client.go, transport/v2rayhttpupgrade/client.go: host/SNI/URL use the parsed address; https_host_test.go, http3_host_test.go, mesh_host_test.go cover domain/IPv4/IPv6, default/non-default/zero port; linux-platform-verify 40081597501 PASS | HTTP/3 ALPN negotiation against a real IPv6-only resolver |
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
| DEP-CRONET-1 | COMPLETED | `cronet-go/main` `8aa139e`: root/all at `8aa139e`, 29 lib modules at `8c68ce8`. All 37 native blobs are byte-identical to the df0c319 build and no cgo/include changed; real native naive self/ECH/concurrency/QUIC tests pass. Archives were not rebuilt, and that is recorded rather than faked. |
| DEP-TAILSCALE-1 | COMPLETED | official `sagernet/tailscale` dev `v1.102.1-sing-box-1.14-mod.5.0.20261022222424-1688b52f8204`, no fork; netmon/tsnet default-route hooks and the sing-box patches are both present. |

## MASQUE inbound backpressure

Wiring is present: `WaitWriteInboundBuffers` ?`transport/device` ?`protocol/masque`
(`ClientEndpoint`/`ServerEndpoint`), with the non-blocking fallback kept.

Real-path evidence added this phase:

- `transport/masque/inbound_backpressure_real_test.go` ?a real capsule is parsed by the session,
  the full device parks the read loop, a drained slot releases exactly one write, `Close` returns
  `os.ErrClosed`, and a handler with no wait contract uses the non-blocking path.
- sing-tun `tun_memory_wait_test.go` ?the underlying `MemoryTun`: full queue parks, a freed slot
  wakes, `Close` wakes with `os.ErrClosed`, the non-blocking path drops at the bound.

Not verified: a live QUIC client/server datagram session on a device.

## Effective pins

Root and nested `test` module both replace `github.com/sagernet/sing-tun` with
`github.com/Piggy-Cat-bit-shadow/sing-tun v0.0.0-20261024025840-dcf84577d0eb` and
`github.com/Piggy-Cat-bit-shadow/sing v0.9.6-0.20261024041103-4200a04a089a`, `github.com/Piggy-Cat-bit-shadow/quic-go v0.61.1-0.20261024063732-ce02ee5cc210`, `github.com/Piggy-Cat-bit-shadow/sing-quic v0.7.2-0.20261024074641-5e5a0274bd6c`, cronet-go root/all `8aa139e` with all 29 lib modules at `8c68ce8`, and the official `github.com/sagernet/tailscale v1.102.1-sing-box-1.14-mod.5.0.20261022222424-1688b52f8204` (no fork). `go list -m -json`
shows the same `Replace.Dir` in both; `go mod tidy`/`verify` are clean and `go build ./...` passes.

## Ancestry and branch closure (phase 06)

- Merge base before: `d630fa94c53b2e2bdf256fad8b9d1cff809146ed`.
- Merge commit: `1649aa4f04b` (`-s ours`); merge base after and upstream tip: `a6952ccf6907c18e18917fcb763a433e8e36937b`.
- `git rev-list --count HEAD..upstream/testing` = **0**; GitHub compare base `a6952ccf690` head `1649aa4f04b`: `behind_by=0`, `ahead_by=2642`.
- Why `ours` is not a fake zero: the audited baseline was `824dbcf`; upstream moved exactly one commit to
  `a6952cc` (`Remove stale workflow`), and `.github/workflows/stale.yml` is already absent. The 73
  audited rows were closed across phases 01–06 (22 MISSING_FIX, 17 dependency bumps, 21 present,
  5 equivalent), plus 22 exact patch-id matches. The tree preserves the fork contracts by design.
- Dependency defaults: sing-tun `dev` = pin; sing `dev` = pin; sing-quic `main` = pin; cronet-go
  `main` = pin; quic-go default is the independent `jiejie-masque-stable` line (`beb42da7`), while
  the consumed pin `ce02ee5c` lives on `jiejie-masque-mod9`; tailscale is the official module.
- No construction branch was created in phase 06. Retained: `oom-final-2026-10-24` (checked out in
  `integration-release-next`), `perf/xhttp-rtt-pipeline-probe` (checked out in
  `singbox-consolidation`), `wip/p0ab-stale-base` (5 unique commits, predates this window),
  `origin/pending/device-axis-reload-window-tests` (1 unique commit). `_p01_main` is clean; the
  detached release/bench worktrees are pre-existing and were not touched.
## CI

- Main repo **Verify SUCCESS** on `0275cf6aa8b` (run 40086590102), **Android Core ARM64 SUCCESS**
  (run 40086590068), **Mobile contract verification SUCCESS** (run 40087286164).
- **linux-dns-verify SUCCESS** on `a3c2b59` (run 40086299564): DNS cache/search tests, DHCP
  expansion, resolved scope equality and the Tailscale/SSH tests with `with_tailscale`.
- Phases 01�C04 CI and fork evidence remain as recorded above; no earlier result is invalidated by
  this phase.

## Open semantic rows (0)

None. Every applicable audited row is closed or rejected with evidence; see the phase 06 report.

## Next executable action

None. All applicable audit rows are closed, dependency blockers are zero, and ancestry is zero-behind.

## Evidence notes

- `go test` was run on Windows for `protocol/tun`, `transport/device` and `transport/masque`; the
  sing-tun packages were run with `-race` where the C toolchain is available.
- Linux-only files (`tun_linux.go`, `libbox/redirect_linux.go` and their tests) cross-compile
  (`GOOS=linux go test -c`); they were not executed on a Linux device, which is stated per row.
- `internal/winipcfg` tests fail in this environment with `ERROR_NOT_FOUND` (no suitable interface)
  and are unrelated to this phase; they are `BLOCKED_BY_ENVIRONMENT`, not PASS.
- `TestGoKernelPacketBatchIdle` fails identically on the pre-merge `sync/upstream-fixes-2026-10-24`
  tree, so it is a pre-existing host issue, not a regression from the convergence.
