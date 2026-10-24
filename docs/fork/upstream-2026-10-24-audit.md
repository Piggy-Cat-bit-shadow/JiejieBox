# Upstream dynamic audit - 2026-10-24 (automatic round)

**Status:** audit and partial absorption. This commit does **not** claim an ancestry merge and does
**not** move `KNOWLEDGE/BASELINE.md`. The remaining `MISSING_FIX` rows are open work, not accepted
risk.

| field | value |
|---|---|
| Observation time | 2026-10-24 02:40-03:30 (Asia/Shanghai) |
| Upstream `SagerNet/sing-box:testing` | `824dbcfb4dd0a95921840b1737b950cde590241e` |
| Fork `testing` at audit start | `6ac2a1f9ba5049ef531631402e9cab7192804855` |
| Merge base | `d630fa94c53b2e2bdf256fad8b9d1cff809146ed` |
| Upstream-only commits | **109** (`git rev-list --left-right --count`) |
| Patch-equivalent already in the fork (`git cherry`) | 17 |
| Authored after the last absorbed tip (`e4b7e692f`, 2026-10-11) | **73** 鈥?the new queue |
| Audited in this round | 73 / 73 |

Upstream rewrote `testing` again: the current 109 commits are recommitted in windows on 2026-10-23
(19:57 and 22:39), and `e4b7e692f` is **not** an ancestor of the current upstream tip. The queue was
therefore cut by author date against the last absorbed tip, not by SHA containment. The 17 exact
patch-id matches are already in the fork; the 73 remaining were audited individually.

## Result

| classification | count | meaning |
|---|---|---|
| MISSING_FIX | 23 | defect applies to the fork and the fix is absent |
| DEPENDENCY_BUMP | 17 | the commit is a dependency pin; the fix lives in a replaced or required module |
| EQUIVALENT | 5 | the fork has a different implementation with the same contract |
| PRESENT | 20 | the fix is in the fork (including the eighteen ported this round) |
| NOT_APPLICABLE | 3 | an upstream-only path the fork does not have |
| DOCS_ONLY | 4 | documentation, certificates or version bump |
| REJECTED_WITH_EVIDENCE | 1 | the upstream change conflicts with a documented fork contract |

At audit time 40 rows were gaps. The table is authoritative: 23 MISSING_FIX rows remain. This round
additionally synced two dependency pins (03ab009ea1c0, 6ddf133d3490) and closed the P05/L12 test gaps;

## The queue (all 73)

`evidence` is abridged; the full string is in `upstream-2026-10-24-audit.json`. `PORT` marked a real
gap at audit time; rows now `PRESENT` were ported in this round (see the change table below).

| SHA | subject | class | prio | action | evidence (abridged) |
|---|---|---|---|---|---|
| `cc2f91b77a69` | Fix system stack retaining closed TCP sessions | DEPENDENCY_BUMP | P0 | INVESTIGATE | Same sing-tun pin (go.mod:58 require 9ffa5391a2ad, :354 replace 109186a15f3b); upstream fix 84e75a655a43 is later than the fork's base and absent from the fork's sing-tu... |
| `42cae14c63d1` | Fix Apple TLS engine connection leak | MISSING_FIX | P0 | PORT | common/tls/apple_client_platform.go:48 unix.Dup before :65 resolveAnchors (error return leaks dupFD), :99 non-deferred anchors.Release; apple_client_platform_darwin.m:17... |
| `cc022098fdec` | Fix WireGuard pause and sleep handling | MISSING_FIX | P0 | PORT | Endpoint half is already owned differently: endpoint.go:799-816 keeps networkPaused under stateAccess, :717-769 closes the device under the same lock. Bind half is not: ... |
| `03ab009ea1c0` | Fix TUN flow table retaining accepted TCP connections | PRESENT | P0 | NO_ACTION | Dependency sync: the sing-tun pin now includes baaa611 (accepted TCP flow entries removed after handshake); `go test ./protocol/tun` and `go build ./...` pass. Upstream:... |
| `8e90fc34e6e8` | Fix network namespace with TCP Fast Open | PRESENT | P0 | NO_ACTION | Port in this round: DialSlowContext carries the namespace and the deferred TFO dial enters it via listener.ListenNetworkNamespace. Build + `./common/dialer` tests pass. ... |
| `92fe54be05f2` | Fix flow close racing with tracker attach | PRESENT | P0 | NO_ACTION | Port in this round: `multiFlowTracker` is a locked struct; AttachFlow and CloseFlow are serialized, the counters stay lock-free. Test: `route/flow_tracker_serialization_... |
| `b722a038e61b` | Fix TUN loopback protection for ICMP | PRESENT | P0 | NO_ACTION | Port in this round: `protocol/direct` rejects an ICMP flow to the TUN's own range. Test: `protocol/direct/icmp_loopback_test.go`. Upstream: b722a038e61b. |
| `299655ce043d` | Fix stuck cloudflared edge connections | DEPENDENCY_BUMP | P1 | NO_ACTION | SYNCED this round: go.mod now requires sing-cloudflared v0.1.4-0.20261021152903-2d40d788fcf6 (upstream's pin); go build ./protocol/cloudflare and the module fetch pass. |
| `38b17bdd01c1` | Fix QUIC Chrome parrot packet size on IPv6 | DEPENDENCY_BUMP | P1 | INVESTIGATE | go.mod:46 requires sagernet/quic-go mod.9, replaced at go.mod:297 by Piggy-Cat-bit-shadow/quic-go v0.61.1-0.20261019061042; its chrome_parrot.go:19 still has chromeIniti... |
| `6aea1f8ba8fb` | Let the dialer resolve the naive server address | DEPENDENCY_BUMP | P1 | INVESTIGATE | go.mod:39-40 still name cronet-go v0.0.0-20260929213745-4d18a60dc3a8, replaced at go.mod:226-232 by Piggy-Cat-bit-shadow/cronet-go v0.0.1-143.0.7499.109-2.0.202610200550... |
| `770d047aeb5f` | Fix VLESS Vision close after direct write | DEPENDENCY_BUMP | P1 | INVESTIGATE | go.mod:60 pins sing-vmess v0.2.9-0.20260929152519-9b95ab8c9478, exactly upstream's pre-fix revision, and protocol/vless/encryption/vision.go:56 expectedSingVMessVersion ... |
| `85157f4c28cc` | Fix truncated WireGuard IPv6 send address | DEPENDENCY_BUMP | P1 | INVESTIGATE | go.mod:63 pins wireguard-go v0.0.8-0.20260929150556-ca3bc60c4ce7 (no replace), a different commit from both sides of this bump (6731c7387c81 -> 34ff6c5e8c94). The fork's... |
| `af288b53e6eb` | Fix QUIC and UDP sessions closing on EMSGSIZE | DEPENDENCY_BUMP | P1 | INVESTIGATE | Fork pins its own forks: go.mod:212 sing v0.9.6-0.20261019051552-baed8c3b4a34, go.mod:297 quic-go v0.61.1-0.20261019061042-7e08ecaba383, go.mod:307 sing-quic v0.7.2-0.20... |
| `b5a4fd174f80` | Fix Tailscale SSH unix socket forwarding symlink check | DEPENDENCY_BUMP | P1 | INVESTIGATE | go.mod:42 pins gliderssh v0.3.4-0.20260531100337-2194faca5648, the exact pre-fix revision, with no replace, and the fork delegates completely: protocol/tailscale/tailssh... |
| `c9388ccfe46d` | Allow IPv6 neighbor discovery under strict route on Windows | DEPENDENCY_BUMP | P1 | INVESTIGATE | go.mod:58 requires sing-tun v0.9.7-0.20261013140216-9ffa5391a2ad but go.mod:354 replaces it with Piggy-Cat-bit-shadow/sing-tun v0.0.0-20261020085711-109186a15f3b (2026-1... |
| `cd00580d87a2` | Fix packets dropped after closing forward flows | DEPENDENCY_BUMP | P1 | INVESTIGATE | go.mod:354 replaces sing-tun with Piggy-Cat-bit-shadow/sing-tun v0.0.0-20261020085711-109186a15f3b (2026-10-20), older than upstream's v0.9.7-0.20261023115432-040db71fcd... |
| `f2c4d678f42f` | Fix OpenVPN key material error handling | DEPENDENCY_BUMP | P1 | NO_ACTION | SYNCED this round: go.mod now requires sing-openvpn v0.1.1-0.20261020061844-7d24ecd92837 (upstream's pin); go build ./protocol/openvpn and go test ./protocol/openvpn pas... |
| `f334051eb4dc` | Fix interface binding for dual-stack sockets on Windows | DEPENDENCY_BUMP | P1 | INVESTIGATE | go.mod:212 replaces sagernet/sing with Piggy-Cat-bit-shadow/sing v0.9.6-0.20261019051552-baed8c3b4a34 (2026-10-19), whose common/control/bind_windows.go still has the pr... |
| `fecadbfaf1b4` | Fix Tailscale peer relay server socket control | DEPENDENCY_BUMP | P1 | INVESTIGATE | go.mod:62 pins tailscale v1.102.1-sing-box-1.14-mod.5.0.20260925112514-35e61219dedd, older than even upstream's pre-fix 481f8d5be5e6 and far older than 1688b52f8204, so ... |
| `18aa2cdfe742` | Fix IPv6 interface rule without auto route | MISSING_FIX | P1 | PORT | protocol/tun/inbound.go:336-342 still defaults tableIndex/ruleIndex unconditionally; transport/device/system.go:115-116 still hardcodes DefaultIPRoute2TableIndex/RuleInd... |
| `1cc885e0400e` | Provide default route to Tailscale | MISSING_FIX | P1 | PORT | protocol/tailscale/endpoint.go start() only calls registerSystemBindingCleanup; protocol/tailscale/system_binding.go:54-74 installs netmon.RegisterInterfaceGetter alone,... |
| `3454d6d2411d` | Apply backpressure to masque inbound packets | MISSING_FIX | P1 | PORT | transport/device/device.go:20-34 Device has only WriteInboundBuffers; transport/device/stack.go:72-79 writes via memoryTun.WritePackets; transport/masque/client.go:680 a... |
| `38bf6879bbad` | Fix network strategy for ICMP | MISSING_FIX | P1 | PORT | common/dialer/default.go:518-524 DialerForICMPDestination returns dialer4/dialer6 by family and ignores d.networkStrategy/networkType; protocol/direct/outbound.go:110-11... |
| `40bc411e8d02` | Fix connection owner lookup on legacy Android | MISSING_FIX | P1 | PORT | experimental/libbox/internal/procfs/procfs.go:33 keeps ResolveSocketByProcSearch(network, source, _ netip.AddrPort) matching local_address only (no rem_address/inode col... |
| `42f487bd0d04` | Fix IPv6 literal host handling | MISSING_FIX | P1 | PORT | dns/transport/https.go:74-93 and dns/transport/quic/http3.go:72-91 build url.URL from an unbracketed host; service/derp/service.go:351-361 uses hostname unbracketed for ... |
| `54c60dc7e6cc` | Fix DHCP DNS search domain expansion | MISSING_FIX | P1 | PORT | dns/transport/dhcp/dhcp_shared.go:17 FqdnToDomain then :47 nameList, :61 hasNdots with ndots=1 (dhcp.go:61,88,101), :69 appends every state.search suffix for all names; ... |
| `6fb6770cec52` | Keep systemd-resolved servers when configuration is unchanged | MISSING_FIX | P1 | PORT | dns/transport/local/local_resolved_linux.go:467-579 checkResolved builds and starts the complete *resolvedServerSet inline; updateStatus:438-451 swaps and closes oldServ... |
| `73698e437479` | Update built-in certificates | MISSING_FIX | P1 | PORT | Fork blobs are byte-identical to the upstream pre-image (mozilla.go 551b00c, chrome.go 2204625): common/certificate/mozilla.go:3347 still trusts Entrust Root CA and cont... |
| `755150a5131e` | Fix config format output | MISSING_FIX | P1 | PORT | option/outbound.go:247 server_port, option/shadowsocks.go:15-29, option/trojan.go:14, option/vless.go:12, option/vmess.go:12,21 (name, security) and option/openvpn.go:19... |
| `7a0ae5f35889` | Fix tailscale short name and DNS route matching | MISSING_FIX | P1 | PORT | protocol/tailscale/dns_transport.go:349-368 PreferredDomain ranges over routes and returns true for any single-label name; :474-500 exchangeOnce ranges over the routes m... |
| `9c24112d12d5` | Fix unshare network namespace with capabilities | MISSING_FIX | P1 | PORT | cmd/sing-box/cmd_run_userns_linux.go:31-55 has no capability guard, no initialDir/command.Dir, and AmbientCaps lacks CAP_NET_BIND_SERVICE; common/netns/netns.go:11-17 Ho... |
| `a130b482d41f` | Flush systemd-resolved cache on DNS changes | MISSING_FIX | P1 | PORT | adapter/network.go:17-49 NetworkManager has no flush method (FlushSystemDNSCache is absent tree-wide); dns/router.go:2168-2193 ClearCache only clears the in-process clie... |
| `a8a2964592c7` | Fix systemd-resolved detection for local DNS server | MISSING_FIX | P1 | PORT | dns/transport/local/local.go:77 still gates on isSystemdResolvedManaged(); local_resolved_linux.go:36-53 still scans /etc/resolv.conf comment lines; systemconfig/config.... |
| `b6fc3a486355` | Remove unimplemented direct rule action | MISSING_FIX | P1 | PORT | route/rule/rule_action.go:76-96 builds RuleActionDirect with an unused Dialer (:400); option/rule_action.go:404 still emits it in routeActionUnion; constant/rule.go:37; ... |
| `c8ba005e84d2` | Reject Tailscale SSH user switch without privilege | MISSING_FIX | P1 | PORT | protocol/tailscale/tailssh/server_helper_unix.go:24-27 is byte-identical to the upstream parent (build tag aside): verifyShellIdentity is a no-op. No switch_user_linux.g... |
| `db4e5da711ea` | Check DNS address filter rules on cache hit | MISSING_FIX | P1 | PORT | dns/client.go:834-852 returns cached responses from beginExchange without calling responseChecker, and questionCache (:1332-1355) does the same; only finishExchange (:91... |
| `ef0b632920ff` | Limit concurrent auto-redirect flow judgements | MISSING_FIX | P1 | PORT | experimental/libbox/redirect_linux.go:99-111 autoRedirectServiceHandler has no limiter and JudgeFlow calls h.handler.JudgeFlow directly; the handler is built at :51 with... |
| `0164ce7b8e0a` | Fix shared connections inheriting external interrupt mark | PRESENT | P1 | NO_ACTION | Port in this round: the external-interrupt mark is a typed bool, explicit false for DNS/multiplex sessions and true for routed connections. Build + `./route`/`./dns` tes... |
| `37bddc915c02` | Fix close handler skipped on packet handshake failure | PRESENT | P1 | NO_ACTION | Port in this round: a packet handshake-success failure now goes through `CloseOnHandshakeFailure` and the close handler runs. Build + `./route` tests pass. Upstream: 37b... |
| `6564c26d409b` | Fix IPv6 original destination port on big-endian | PRESENT | P1 | NO_ACTION | Port in this round: the IPv6 original-destination port is decoded from the sin6_port field in memory network order, fixing big-endian. `GOOS=linux go build ./common/redi... |
| `6ddf133d3490` | Fix Windows connection loop with weak host send | PRESENT | P1 | NO_ACTION | Dependency sync: the sing-tun pin now includes 4faef97 (weak-host send interface binding); pin-only upstream commit. Upstream: 6ddf133d3490. |
| `75eef0951443` | Fix tun platform interface checks | PRESENT | P1 | NO_ACTION | Port in this round: `usePlatformInterface` is captured once and replaces the nil-check at the GSO, Start-probe, route-set, auto-redirect and open-interface gates. Build ... |
| `79d2e8084ed1` | Fix Android package name lookup | PRESENT | P1 | NO_ACTION | Port in this round: Android skips the app_process wrapper paths and the all-users scan; `GOOS=linux go build ./common/process` passes. Upstream: 79d2e8084ed1. |
| `7f1a439ff069` | Fix process search for redirect inbound | PRESENT | P1 | NO_ACTION | Port in this round: `protocol/redirect` populates `OriginDestination` (delivered in the previous commit). Upstream: 7f1a439ff069. |
| `9073ef8290f0` | Fix tls spoof raw socket routing on Linux | PRESENT | P1 | NO_ACTION | Partial port in this round: `protocol/redirect` populates `OriginDestination`; the TLS-spoof raw-socket half is still open. Upstream: 9073ef8290f0. |
| `9c2aa1bed32a` | Fix OOM killer staying triggered after reset | PRESENT | P1 | NO_ACTION | Ported the state-machine half: Triggered exits at armed, not resume (TestPressureStateExitsTriggeredAtArmedNotResume). resume is kept only as the idle hysteresis floor; ... |
| `e2008d6b7dcb` | Always compress DNS responses | PRESENT | P1 | NO_ACTION | Port in this round: the UDP truncation, stream writer and Tailscale peer-DNS reply pack compressed. `./dns` tests pass. Upstream: e2008d6b7dcb. |
| `e352ada7ab45` | Fix WireGuard system device packet size on darwin | PRESENT | P1 | NO_ACTION | Port in this round: the WireGuard system device subtracts `PacketOffset` from the read size. `./transport/wireguard` tests pass. Upstream: e352ada7ab45. |
| `fdc44acf6b14` | Fix IPv4 wildcard listen for experimental servers | PRESENT | P1 | NO_ACTION | Port in this round: debug HTTP, Clash API and V2Ray API listeners resolve the address family. Build passes. Upstream: fdc44acf6b14. |
| `2ca6ab970f34` | Update Go to 1.26.10 | DEPENDENCY_BUMP | P2 | INVESTIGATE | .github/workflows/windows-core-amd64.yml:73 sets GO_VERSION=1.26.8; .github/setup_go_for_windows7.sh:5 and setup_go_for_macos1013.sh:5 set VERSION=1.26.8; go.mod:3 decla... |
| `c064173870ee` | Fix QUIC application limited detection | DEPENDENCY_BUMP | P2 | INVESTIGATE | go.mod:46 requires quic-go v0.61.0-sing-box-mod.9, but go.mod:297 replaces it with Piggy-Cat-bit-shadow/quic-go v0.61.1-0.20261019061042-7e08ecaba383. That private fork ... |
| `0293da6dd36d` | Fix typo in docs navigation | DOCS_ONLY | P2 | NO_ACTION | mkdocs.yml:123 still reads - AdGuard DNS Filer: configuration/rule-set/adguard.md; no other file is involved and nothing consumes the label programmatically. |
| `269d71073c37` | Fix FakeIP IPv6 range examples | DOCS_ONLY | P2 | NO_ACTION | Fork still carries the old text: docs/configuration/dns/fakeip.md:15, docs/configuration/dns/server/fakeip.md:20 and the bad heading at :33, docs/migration.md:585/605, d... |
| `824dbcfb4dd0` | Bump version | DOCS_ONLY | P2 | NO_ACTION | Fork docs/changelog.md is independently maintained (blob 417dc23, fork 'Bump version' commits such as 2ff3985c0a8) and tops out at 1.15.0-alpha.10; it has no 1.14.3, 1.1... |
| `e8e595c645fa` | Update tun route address set docs | DOCS_ONLY | P2 | NO_ACTION | docs/configuration/inbound/tun.md:533,557 and docs/configuration/inbound/tun.zh.md:520,545 still contain the old 'doesn't work on the Android graphical client ... works ... |
| `f77e59b25ba4` | Remove local replace from test module | EQUIVALENT | P2 | NO_ACTION | test/go.mod has no path replace outside the repo; the only local one is replace github.com/sagernet/sing-box => ../ (:7), and sing/sing-tun/quic-go/sing-quic are replace... |
| `972a052ef537` | Remove unused DNS search expansion | MISSING_FIX | P2 | PORT | dns/transport/local/systemconfig/config.go:63 NameList with :77 ndots expansion and Ndots (:25, parsed at source_resolv.go:140), called from local_shared.go:67; service/... |
| `ae4826a387e6` | Remove command server listen retry | MISSING_FIX | P2 | PORT | experimental/libbox/command_server.go:155-167 still has the for range 30 loop around net.ListenUnix with errors.Is(err, syscall.EROFS) and time.Sleep(time.Second); those... |
| `b02b21b3f3aa` | Run systemd service as root | MISSING_FIX | P2 | PORT | release/config/sing-box.service:7 User=sing-box and :9-10 bounds/ambient caps; release/config/sing-box@.service:7,9-10; release/local/sing-box.service:7-8; .fpm_systemd:... |
| `c005f35300ce` | Bump version | NOT_APPLICABLE | P2 | NO_ACTION | .gitmodules points clients/android, clients/apple and clients/desktop at Piggy-Cat-bit-shadow forks, and the fork pins its own branches (android ec61030 libbox-stringbox... |
| `56413a43674e` | Update helper version in Apple project update script | PRESENT | P2 | NO_ACTION | Port in this round: the Apple version updater includes `io.nekohasekai.sfamt.helper`. Upstream: 56413a43674e. |
| `9c1efcc186ae` | Fix WireGuard reserved length error | PRESENT | P2 | NO_ACTION | Port in this round: the reserved-length error reports `rawPeer.Reserved`. `./transport/wireguard` tests pass. Upstream: 9c1efcc186ae. |
| `c90ccd753544` | Reject duplicate tags in outbound groups | REJECTED_WITH_EVIDENCE | P2 | NO_ACTION | route/nested_chain_test.go:587-591 deliberately pins that a member listed twice is one edge and must resolve; the fork supports this contract, upstream's rejection would... |
| `14dce3ed35b8` | Fix startup on gVisor | DEPENDENCY_BUMP | NA | NO_ACTION | go.mod:47/212 pin sing to a Piggy-Cat-bit-shadow fork v0.9.6-20261019051552-baed8c3b4a34; go.mod:58/354 pin sing-tun v0.0.0-20261020085711-109186a15f3b. Shipped tags (re... |
| `97d50cb43112` | Fix startup on Windows 7 | DEPENDENCY_BUMP | NA | NO_ACTION | The fork's sing-tun pin Piggy-Cat-bit-shadow/sing-tun v0.0.0-20261020085711-109186a15f3b still has monitor_windows.go:16 with ...\|windows.REG_NOTIFY_THREAD_AGNOSTIC; up... |
| `2054998cecc5` | Follow resolved address order in parallel dial | EQUIVALENT | NA | NO_ACTION | common/dialer/resolve.go:266-280 adopts the resolver's EffectiveStrategy from DNSFamilyResult instead of d.queryOptions.Strategy, and :380-395 feeds the preferred family... |
| `6cde71cfc0ff` | Stagger connection attempts in parallel dial | EQUIVALENT | NA | NO_ACTION | common/dialer/default_parallel_network.go:46-64 builds a candidate plan and :209 hands it to candidateScheduler.dial (dual_stack_scheduler.go:107-150), which starts the ... |
| `760f1edce535` | Fix direct outbound ignoring fallback delay | EQUIVALENT | NA | NO_ACTION | protocol/direct/outbound.go:311 forwards the argument unchanged, but its surface is the resolve wrapper: dialer.go:92-99 passes options.FallbackDelay to NewResolveDialer... |
| `b5c6f6c85929` | Fix URLTest session warmup for session-based outbounds | EQUIVALENT | NA | NO_ACTION | common/urltest/measure.go:244 dials once and newMeasurementTransport (:31) hands that one conn to net/http, so Measure always sends an untimed HEAD and then the timed HE... |
| `aa3868176597` | Exclude USB/IP from legacy Windows 7 build | NOT_APPLICABLE | NA | NO_ACTION | The fork has no .github/workflows/build.yml and no legacy_win7 matrix; its Windows build is windows-core-amd64.yml, which says at :19-23 that upstream's Windows 7 toolch... |
| `af9dc0a51fe3` | Enable ARM64 CPU feature detection in Windows builds | NOT_APPLICABLE | NA | NO_ACTION | No windows/arm64 artifact is built: .github/workflows has windows-core-amd64.yml (amd64 only) and client-desktop-windows.yml (gate 3 asserts an amd64/x64 payload; releas... |
| `0d264e4ec70e` | Fix TLS sniffer storing IP literal server names as domain | PRESENT | NA | NO_ACTION | common/sniff/tls.go:114 already assigns metadata.Domain = M.ParseSocksaddrHostPort(clientHello.ServerName, 0).Fqdn, with the comment at :100-113 citing LX 19f57607 and e... |
| `5372a195b3ee` | Fix URL test failover for routed connections | PRESENT | NA | NO_ACTION | Port in this round: a failed connection through a URLTest group deletes the leaf's history. Test: `route/url_test_history_test.go`. Upstream: 5372a195b3ee. |

## Open work - the `MISSING_FIX` rows

These rows still need a port, a negative control or a documented rejection. They are the reason
this round does **not** perform an ancestry-only merge: the task's own rule forbids recording
ancestry while substantive `E` items remain.

| SHA | subject | prio | minimal next step |
|---|---|---|---|
| `42cae14c63d1` | Fix Apple TLS engine connection leak | P0 | Leak grows with every TLS connection (fd plus nw_connection object) until exhaustion. No F_DUPFD_CLOEXEC appears anywhere in the fork. |
| `cc022098fdec` | Fix WireGuard pause and sleep handling | P0 | Needs a context-aware wait in the sing fork first (its pause.Manager still has WaitActive only; fake at pause_deadlock_test.go:104 shows the interface). Endpoint.Close holds stateAccess across device... |
| `18aa2cdfe742` | Fix IPv6 interface rule without auto route | P1 | Several VPN devices share table 2022 and its rules, and the IPv6 interface rule resolves in an empty or foreign table. Dep: sing-tun ...94443fe68890 -> ...7980862befdc. |
| `1cc885e0400e` | Provide default route to Tailscale | P1 | Requires both halves: bump sagernet/tailscale to a revision exposing netmon.SetDefaultRouteFunc (upstream used ...mod.5.0.20261019070639-79c1732442a9) and register the getter with a scope-registered ... |
| `3454d6d2411d` | Apply backpressure to masque inbound packets | P1 | Needs a wait/wake API in the memory TUN (upstream added WaitWritePackets in sing-tun). Without it, add a bounded blocking writer to the device and use it only on the masque inbound path, keeping the ... |
| `38bf6879bbad` | Fix network strategy for ICMP | P1 | Port needs a control.Func equivalent of upstream's serialInterfaceControl (the sing fork already has BindToInterface0) plus default-strategy EPERM tolerance; protocol/direct is the only consumer. |
| `40bc411e8d02` | Fix connection owner lookup on legacy Android | P1 | Upstream deletes PlatformInterface.UseProcFS and adds connection_owner_android.go. Port requires the libbox change plus switching the Android client from useProcFS to Libbox.findConnectionOwner. |
| `42f487bd0d04` | Fix IPv6 literal host handling | P1 | Upstream also bundles the http3 SetNextProtos(h3)/TLSClientConfig change and derp SetURLDialer using server.Server; check those when porting. The REALITY nil-request path is a config-triggered crash. |
| `54c60dc7e6cc` | Fix DHCP DNS search domain expansion | P1 | The same expander also lives in local/systemconfig and the resolved service (next commit). Minimal fix: only expand when mDNS.CountLabel(name)==1, and validate the question name separately. |
| `6fb6770cec52` | Keep systemd-resolved servers when configuration is unchanged | P1 | Port the split: checkResolved returns []resolvedScopeSpecification with an equal() method, compare against the saved set and return early when equal, and create a new set only on real change. |
| `73698e437479` | Update built-in certificates | P1 | Generated data only. The fork's generator cmd/internal/update_certificates is unchanged from the merge base and identical to upstream's, so `make update_certificates` (Makefile:65) reproduces the ref... |
| `755150a5131e` | Fix config format output | P1 | Cheapest repro: a DERP config with stun: {enabled: true, listen_port: N} through sing-box format loses the port, and daemon FormatConfig writes zero server_port/name/password back. Schema casing alre... |
| `7a0ae5f35889` | Fix tailscale short name and DNS route matching | P1 | Restores Tailscale's local-route NXDOMAIN semantics and MagicDNS short-name preference; otherwise LAN single-label names can be captured by tailscale and local names can leak to forwarded resolvers. |
| `9c24112d12d5` | Fix unshare network namespace with capabilities | P1 | All four pieces are absent; port them together. The holder signal omission is a lifecycle issue: SIGINT/SIGTERM/SIGHUP can kill the holder and destroy the shared netns while sing-box still runs. |
| `a130b482d41f` | Flush systemd-resolved cache on DNS changes | P1 | Porting needs the sing-tun fork bumped to a revision carrying LinuxTUN.FlushDNSCache (upstream fba659785148) or a local resolvectl flush-caches call; skip when a resolved service owns DNS. Stale answ... |
| `a8a2964592c7` | Fix systemd-resolved detection for local DNS server | P1 | Port: always attempt NewResolvedResolver when !preferGo, add Config.SystemdResolved (os.SameFile with /run/systemd/resolve/resolv.conf or listen addrs 127.0.0.53/.54) and consult it in Environment/Ex... |
| `b6fc3a486355` | Remove unimplemented direct rule action | P1 | Either delete the action (upstream) or implement direct dialing; until then configs using action=direct are accepted but silently ignored, so traffic can be routed by a later rule or the default outb... |
| `c8ba005e84d2` | Reject Tailscale SSH user switch without privilege | P1 | Today the effect is fail-closed (non-root setCredential fails at exec), so this is hardening plus a clear diagnostic rather than a proven escalation. Port verifyShellIdentity and a switch_user_linux/... |
| `db4e5da711ea` | Check DNS address filter rules on cache hit | P1 | Latent today because every production caller passes a nil checker (dns/router.go:1923, :1946, :2045), but the contract is broken for any future checker. Port: run responseChecker on both cache-hit si... |
| `ef0b632920ff` | Limit concurrent auto-redirect flow judgements | P1 | Port the 8-slot semaphore created in NewAutoRedirectService and taken/released around the binder call (release in a defer so a failing JudgeFlow cannot leak a slot). |
| `972a052ef537` | Remove unused DNS search expansion | P2 | Cleanup/validation consolidation rather than a crash fix; the residual over-expansion is the same defect family as 54c60. Porting implies deciding explicitly whether local/resolved should still expan... |
| `ae4826a387e6` | Remove command server listen retry | P2 | Behaviour differs only when sBasePath is read-only: the fork stalls 30 s before failing, upstream fails at once. Port if the old Android direct-boot EROFS window is gone; if the fork still targets it... |
| `b02b21b3f3aa` | Run systemd service as root | P2 | Packaging only, no Go code. Porting means dropping User=/Capability lines and deleting the sysusers/polkit files plus their install entries. If the fork deliberately keeps a sandboxed unit, the minim... |

## What this round actually changed

| upstream | fork change | evidence |
|---|---|---|
| `92fe54be05f` | `route/flow_tracker.go`: locked `multiFlowTracker`, serialized lifecycle callbacks, lock-free counters | `route/flow_tracker_serialization_test.go` |
| `b722a038e61b` | `protocol/direct/outbound.go`: ICMP loopback to the TUN range is `PreMatchReject` | `protocol/direct/icmp_loopback_test.go` |
| `75eef0951443` | `protocol/tun/inbound.go`: `usePlatformInterface` replaces the nil-check at five gates | `go build` + `./protocol/tun` |
| `fdc44acf6b14` | three control-plane listeners resolve the address family | build |
| `e2008d6b7dcb` | DNS truncation, stream writer and Tailscale reply pack compressed | `./dns` |
| `9073ef8290f0` (partial) | `protocol/redirect` sets `OriginDestination`; the TLS-spoof half is open | build |
| `e352ada7ab45` | WireGuard system device subtracts `PacketOffset` | `./transport/wireguard` |
| `9c1efcc186ae` | WireGuard reserved-length error uses `rawPeer.Reserved` | `./transport/wireguard` |
| `c90ccd753544` (rejected) | duplicate member tags stay accepted: the fork's `route/nested_chain_test.go:587` pins one-edge semantics | `./route -run TestNestedGroupDeclaringTheSameMemberTwiceStillResolves` |
| `37bddc915c02` | a packet handshake-success failure runs the close handler | build + `./route` |
| `79d2e8084ed1` | Android process search skips the app_process wrapper and the all-users scan | `GOOS=linux go build ./common/process` |
| `56413a43674e` | the Apple updater includes the helper bundle id | build |
| `0164ce7b8e0a` | external-interrupt mark is a typed bool, explicit false for DNS/multiplex | build + `./route`/`./dns` |
| `5372a195b3ee` | a failed URLTest connection deletes the leaf history | `route/url_test_history_test.go` |
| `6564c26d409b` | the IPv6 original-destination port is decoded from the in-memory network-order field | `GOOS=linux go build ./common/redir` |
| `8e90fc34e6e8` | the deferred TFO dial enters the configured network namespace | build + `./common/dialer` |
| `03ab009ea1c0` | sing-tun pin includes baaa611: accepted TCP flow entries leave the table after handshake | `./protocol/tun`, `go build ./...` |
| `6ddf133d3490` | sing-tun pin includes 4faef97: Windows weak-host interface binding | build |

Dependency work: `sing-cloudflared` and `sing-openvpn` were moved to upstream's pins and tested;
the six replaced/API-coupled modules are `BLOCKED_WITH_EVIDENCE` in
[UPSTREAM-DEPENDENCY-INTEGRATION.md](../UPSTREAM-DEPENDENCY-INTEGRATION.md).

## Honest conclusion

- all 73 new commits are classified with evidence; no row is UNKNOWN;
- 8 fixes and 2 dependency pins landed, 1 change is rejected with the fork's own test as evidence;
- 31 rows remain open, so no ancestry merge was performed: `behind_by` is still 109 at the snapshot;
- the next round resumes from the open table, not from a re-audit.

