# Upstream integration status

- generated: 2026-10-24T08:35:25.430756+08:00
- fork testing: `1bccb81a8ab95b7371ac91507774e1b53322fa96`
- upstream observed: `824dbcfb4dd0a95921840b1737b950cde590241e` (2026-10-23T22:39:57+08:00)
- behind_by: **109**
- open semantic: **23**; open dependency blockers: **6**

## Next executable action

DEP-TUN-0: audit and merge sing-tun sync/upstream-fixes-2026-10-24 (88e6d81) into dev (7539c98); then move the main pin and close DEP-TUN-1..3

## Open semantic rows

| id | prio | upstream | next action |
|---|---|---|---|
| MAIN-42cae14c63d1 | P0 | Fix Apple TLS engine connection leak | Leak grows with every TLS connection (fd plus nw_connection object) until exhaustion. No F_DUPFD_CLOEXEC appears anywhere in the fork. |
| MAIN-cc022098fdec | P0 | Fix WireGuard pause and sleep handling | Needs a context-aware wait in the sing fork first (its pause.Manager still has WaitActive only; fake at pause_deadlock_test.go:104 shows the interface). Endpoin |
| MAIN-18aa2cdfe742 | P1 | Fix IPv6 interface rule without auto route | Several VPN devices share table 2022 and its rules, and the IPv6 interface rule resolves in an empty or foreign table. Dep: sing-tun ...94443fe68890 -> ...79808 |
| MAIN-1cc885e0400e | P1 | Provide default route to Tailscale | Requires both halves: bump sagernet/tailscale to a revision exposing netmon.SetDefaultRouteFunc (upstream used ...mod.5.0.20261019070639-79c1732442a9) and regis |
| MAIN-3454d6d2411d | P1 | Apply backpressure to masque inbound packets | Needs a wait/wake API in the memory TUN (upstream added WaitWritePackets in sing-tun). Without it, add a bounded blocking writer to the device and use it only o |
| MAIN-38bf6879bbad | P1 | Fix network strategy for ICMP | Port needs a control.Func equivalent of upstream's serialInterfaceControl (the sing fork already has BindToInterface0) plus default-strategy EPERM tolerance; pr |
| MAIN-40bc411e8d02 | P1 | Fix connection owner lookup on legacy Android | Upstream deletes PlatformInterface.UseProcFS and adds connection_owner_android.go. Port requires the libbox change plus switching the Android client from usePro |
| MAIN-42f487bd0d04 | P1 | Fix IPv6 literal host handling | Upstream also bundles the http3 SetNextProtos(h3)/TLSClientConfig change and derp SetURLDialer using server.Server; check those when porting. The REALITY nil-re |
| MAIN-54c60dc7e6cc | P1 | Fix DHCP DNS search domain expansion | The same expander also lives in local/systemconfig and the resolved service (next commit). Minimal fix: only expand when mDNS.CountLabel(name)==1, and validate  |
| MAIN-6fb6770cec52 | P1 | Keep systemd-resolved servers when configuration is unchanged | Port the split: checkResolved returns []resolvedScopeSpecification with an equal() method, compare against the saved set and return early when equal, and create |
| MAIN-73698e437479 | P1 | Update built-in certificates | Generated data only. The fork's generator cmd/internal/update_certificates is unchanged from the merge base and identical to upstream's, so `make update_certifi |
| MAIN-755150a5131e | P1 | Fix config format output | Cheapest repro: a DERP config with stun: {enabled: true, listen_port: N} through sing-box format loses the port, and daemon FormatConfig writes zero server_port |
| MAIN-7a0ae5f35889 | P1 | Fix tailscale short name and DNS route matching | Restores Tailscale's local-route NXDOMAIN semantics and MagicDNS short-name preference; otherwise LAN single-label names can be captured by tailscale and local  |
| MAIN-9c24112d12d5 | P1 | Fix unshare network namespace with capabilities | All four pieces are absent; port them together. The holder signal omission is a lifecycle issue: SIGINT/SIGTERM/SIGHUP can kill the holder and destroy the share |
| MAIN-a130b482d41f | P1 | Flush systemd-resolved cache on DNS changes | Porting needs the sing-tun fork bumped to a revision carrying LinuxTUN.FlushDNSCache (upstream fba659785148) or a local resolvectl flush-caches call; skip when  |
| MAIN-a8a2964592c7 | P1 | Fix systemd-resolved detection for local DNS server | Port: always attempt NewResolvedResolver when !preferGo, add Config.SystemdResolved (os.SameFile with /run/systemd/resolve/resolv.conf or listen addrs 127.0.0.5 |
| MAIN-b6fc3a486355 | P1 | Remove unimplemented direct rule action | Either delete the action (upstream) or implement direct dialing; until then configs using action=direct are accepted but silently ignored, so traffic can be rou |
| MAIN-c8ba005e84d2 | P1 | Reject Tailscale SSH user switch without privilege | Today the effect is fail-closed (non-root setCredential fails at exec), so this is hardening plus a clear diagnostic rather than a proven escalation. Port verif |
| MAIN-db4e5da711ea | P1 | Check DNS address filter rules on cache hit | Latent today because every production caller passes a nil checker (dns/router.go:1923, :1946, :2045), but the contract is broken for any future checker. Port: r |
| MAIN-ef0b632920ff | P1 | Limit concurrent auto-redirect flow judgements | Port the 8-slot semaphore created in NewAutoRedirectService and taken/released around the binder call (release in a defer so a failing JudgeFlow cannot leak a s |
| MAIN-972a052ef537 | P2 | Remove unused DNS search expansion | Cleanup/validation consolidation rather than a crash fix; the residual over-expansion is the same defect family as 54c60. Porting implies deciding explicitly wh |
| MAIN-ae4826a387e6 | P2 | Remove command server listen retry | Behaviour differs only when sBasePath is read-only: the fork stalls 30 s before failing, upstream fails at once. Port if the old Android direct-boot EROFS windo |
| MAIN-b02b21b3f3aa | P2 | Run systemd service as root | Packaging only, no Go code. Porting means dropping User=/Capability lines and deleting the sysusers/polkit files plus their install entries. If the fork deliber |

## Dependency blockers

| id | repo | target | blocked by | next |
|---|---|---|---|---|
| DEP-TUN-0 | Piggy-Cat-bit-shadow/sing-tun | dev | - | audit 49/26 divergence, merge sync into dev preserving (handled,remove) and NAT/buffer ownership, push dev, move main pin |
| DEP-TUN-1 | Piggy-Cat-bit-shadow/sing-tun | dev | DEP-TUN-0 | system stack closed-session NAT cleanup |
| DEP-TUN-2 | Piggy-Cat-bit-shadow/sing-tun | dev | DEP-TUN-0 | IPv6 interface rule without auto route |
| DEP-TUN-3 | Piggy-Cat-bit-shadow/sing-tun | dev | DEP-TUN-0 | JudgeFlow without blocking packet processing |
| DEP-SING-1 | Piggy-Cat-bit-shadow/sing | dev | - | pause.Manager context-aware wait; netns/ICMP control |
| DEP-QUIC-1 | Piggy-Cat-bit-shadow/quic-go | jiejie-masque-stable | - | audit mod.12 vs fork; preserve buffer pinning |
| DEP-QUIC-2 | Piggy-Cat-bit-shadow/sing-quic | main | DEP-QUIC-1 | absorb applicable fixes, keep MASQUE logic |
| DEP-CRONET-1 | Piggy-Cat-bit-shadow/cronet-go | main | - | atomic root/all/lib + ABI matrix |
| DEP-TAILSCALE-1 | sagernet/tailscale | N/A | - | upgrade external module if API present; do not create a fork by default |
