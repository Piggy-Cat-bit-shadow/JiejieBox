# Phase 05 — Tailscale, DNS correctness and systemd-resolved integration

```text
PHASE = 05/06
RESULT = PARTIAL_WITH_SPECIFIC_BLOCKER
MAIN_SHA = 0275cf6aa8b2e94f81c449681207196038bb4e3e (code); ledger commit follows
TAILSCALE_EFFECTIVE_PIN = github.com/sagernet/tailscale v1.102.1-sing-box-1.14-mod.5.0.20261022222424-1688b52f8204 (official module, no fork)
TAILSCALE_ROUTE_DNS_SSH = CLOSED_CODE_AND_UNIT: netmon/tsnet default-route hooks; short-name/search-domain split; CAP_SETUID/CAP_SETGID preflight
SYSTEMD_DETECT_REUSE_FLUSH = CLOSED_CODE_AND_LINUX_UNIT: resolv.conf identity + listen addresses; comparable scope specs; NetworkManager/TUN flush path
DHCP_NDOTS_AND_SEARCH_CLEANUP = CLOSED_CODE_AND_UNIT: single-label-only expansion, original name last; unused path removed
DNS_CACHE_CHECKER = CLOSED_CODE_AND_UNIT: checker runs on stale and fresh cache hits without querying upstream
TARGETED_TESTS_AND_FINAL_CODE_CI = main Verify 40086590102, Android 40086590068, Mobile 40087286164 on 0275cf6; linux-dns-verify 40086299564 on a3c2b59
STATE_LEDGER_OPEN = 5 semantic rows; 0 dependency blockers
NEXT_PHASE_READY = YES
```

## 1. Tailscale dependency and route

`go.mod` moves from the Sep 25 pseudo-version to the official dev module
`v1.102.1-sing-box-1.14-mod.5.0.20261022222424-1688b52f8204`. That revision was chosen with
evidence: the `main` line at `79c1732442` has the global default-route override but has dropped the
sing-box `tsnet` patches (`ControlFunc`, `NetstackHandler`), while the dev line keeps them and
carries the per-monitor `netmon.Hooks.DefaultRoute` plus `tsnet.Server.DefaultRouteFunc`. No fork
was created.

The route is answered from the same `NetworkManager` that supplies the interface getter:
`systemBinding` exposes `DefaultRoute` to tailcat's `netmon.Hooks`, and the endpoint assigns
`DefaultRouteFunc` for the normal `tsnet.Server`. Cleanup remains on the binding scope, so a closed
Box cannot leave a closure over a torn-down manager. The stub test drives the hook and asserts the
stub interface, alongside the existing interface-getter install/read/clear test.

## 2. Tailscale DNS and SSH

- `tailnetDomains.match` separates three cases: a known host, a local/forwarded route suffix, and a
  single-label name expanded through the search domains. `TestTailnetDomainsMatch` covers the known
  host, expansion on/off, local and forwarded suffixes, and unknown single/multi-label names.
- `verifyShellIdentity` now rejects a different uid/gid before exec when the process is not root, and
  `checkSwitchUserPrivilege` requires `CAP_SETUID|CAP_SETGID` for root. `switch_user_test.go` proves
  non-root refusal and same-user allowance; the helper tags were corrected from `with_gvisor` to
  `with_tailscale` so a Linux build without gvisor compiles the guard.

## 3. systemd-resolved

- Detection no longer scans comments in `/etc/resolv.conf`: `systemconfig` compares the resolv.conf
  file identity with the systemd-resolved uplink file and checks the 127.0.0.53 listen addresses,
  publishing `SystemdResolved` as part of `Config` and its `Equal` identity.
- `resolvedScopeSpecification` captures interface, DoT mode, domains, default-route, fallback and the
  server list; `updateStatus` returns early when the new specifications compare equal, so an
  unchanged configuration does not close and rebuild the server scopes.
- `NetworkManager.FlushSystemDNSCache` walks inbounds; the TUN inbound calls
  `LinuxTUN.FlushDNSCache` only when no resolved service owns the cache, and the resolver's own
  update also flushes. `dns/router.go` skips the call when no manager/context is installed (unit
  fixtures).
- Tests: `TestResolvedScopeSpecificationEqual` covers identical and changed port/domains/default
  route; `TestConfigEqualIncludesSystemdResolved` pins the flag in the identity. The Linux gate runs
  both.

## 4. DHCP ndots / search and DNS cache hits

- `nameList` expands only a single-label name (`CountLabel == 1`), validates each candidate, and
  appends the original name last. `TestNameList` covers single-label, multi-label, a trailing-dot
  FQDN, empty and duplicate search domains, and an IPv6 reverse name.
- `beginExchange` now runs `responseChecker` on stale and fresh cache hits, returns
  `ErrResponseRejectedCached` without querying upstream, and does not evict the entry. The
  `questionCache` path already had the checker. `TestCacheHitRunsTheResponseChecker` proves the
  rejection, the no-upstream property and the subsequent accepted hit.

## 5. CI and limits

- Main code `0275cf6`: Verify `40086590102`, Android Core ARM64 `40086590068`, Mobile contract
  verification `40087286164` — all SUCCESS.
- `linux-dns-verify` `40086299564`: DNS/cache/search, DHCP, resolved equality and
  Tailscale/SSH with `with_tailscale` — SUCCESS.
- Not executed, and recorded as such:
  1. A live tailnet/control-plane session; route and DNS behaviour are unit-tested only.
  2. A real systemd-resolved D-Bus cache flush and resolver handover on a Linux host.
  3. A real DHCP lease with search domains.

The next phase is 06: the five remaining main-repo rows (certificates, config format, direct rule
action, command-server retry, systemd service) plus ancestry/CI closure. No ancestry merge was
started here.
