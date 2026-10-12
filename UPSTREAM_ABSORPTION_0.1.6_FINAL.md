# UPSTREAM ABSORPTION FINAL

## Baseline

| field | value |
|---|---|
| FORK_REPO | `https://github.com/Piggy-Cat-bit-shadow/sing-box.git` |
| WORK_BRANCH | `audit/upstream-absorb-0.1.6-91ca1652d` |
| BASE_BRANCH | `jiejiebox/v0.1.6-hardening` |
| BASE_SHA | `91ca1652d9de94eaf1bb8d1aaa09437b1974ac10` |
| UPSTREAM_REMOTE | `https://github.com/SagerNet/sing-box.git` |
| UPSTREAM_BRANCH | `testing` |
| MERGE_BASE | `7a3d4e4a8e71bd7fa824959efdb57b4f39738802` (2026-10-02) |
| UPSTREAM_HEAD_BEFORE | `e4b7e692f4545890bfc9d43960328e1449fa803b` |
| UPSTREAM_HEAD_FINAL | `e4b7e692f4545890bfc9d43960328e1449fa803b` (unchanged) |
| WORKTREE_CLEAN_BEFORE | YES |
| COMMITS: upstream since merge-base | 54 |
| COMMITS: fork since merge-base | 1697 |

Two windows sit in that range. Window 1 (`6e3c86f51..7054cac51`, 15 commits, 2026-10-09) was
absorbed by an earlier round; window 2 (`d630fa94c..e4b7e692f`, 38 commits, 2026-10-11) is this
round's subject. The second pass required by the construction prompt found upstream had not moved,
so no new commits entered during the work.

**Method note, because the first attempt at coverage was wrong.** A `git log --grep` for the
window-1 subjects reported every one of them MISSING, which would have been a false alarm: the fork
replays upstream work under its own commit messages and committers. Coverage was therefore
established by comparing, for every file an upstream commit touches, the fork's blob at `HEAD` with
that commit's blob — 330 file-slots across the window — and then reading every divergence. That
comparison found 51 byte-identical slots and 138 divergent ones, and the audit of the divergences is
what produced `7e13f4617`. **A subject-line search is not an absorption check.**

## Absorbed

| upstream | area | mode | local commit | tests |
|---|---|---|---|---|
| `74182ce8e` "Fix per-destination backpressure to endpoints" | endpoint headroom + OpenVPN server queue | ABSORB_NOW (restore) | `a73164297` | `protocol/openvpn/data_channel_contract_test.go` (6 assertions, 2 mutations verified) + `TestOpenVPNSelfToSelf` 4/4 |
| `194ebd18b` "tun: Fix inconsistent DNS mode behavior without auto_route" | docs only — the code half lives in sing-tun and is already at the required revision | ABSORB_NOW (docs) | `4e921b6a4` | none needed (no code) |
| `a691a4402` "Refactor lifecycle to scoped cleanup" — the interface-getter reset, and nothing else | tailscale process-global leak | ABSORB_NOW (one artefact) | `7e13f4617` | `protocol/tailscale/system_binding_cleanup_test.go` (3 assertions, 1 mutation verified) |

### What `7e13f4617` fixed, and how it was found

Window 1 was already synced, so this item was found only by a **blob-level** comparison of every
file the window's commits touch (330 file-slots) — not by matching commit subjects, which had
produced a false "missing" verdict for the whole window. One artefact of `a691a4402` was genuinely
absent: `netmon.RegisterInterfaceGetter(nil)` on the endpoint's close path.

`newSystemBinding` registers a getter with tailscale's netmon when the platform owns the network
interfaces, and that writes a **package global** (`netmon/state.go`'s `altNetInterfaces`). Nothing in
this tree ever cleared it, so after a Box closed the global still held a closure over that Box's
`NetworkManager` — and it survived into a **later** Box whose platform interface does not
re-register, at which point netmon reads interfaces out of a torn-down manager. Silent,
cross-instance, and needs two sequential Boxes to observe.

The fork's own refactor moved the registration into `newSystemBinding`, which took upstream's reset
statement with it and dropped it. The fix returns the cleanup from `newSystemBinding` and ties it to
the lifetime each caller owns: `scope.Add` for the endpoint and the tailcat inbound, and
`context.AfterFunc` on `nodeContext` for the tailcat **outbound**, which builds its node lazily on
first use and therefore has no Start stage to register on. The body was split so that all four return
points carry the cleanup — one silently dropping it is the same class of omission as the bug.

Test note: `netmon.GetInterfaceList()` returns the registered getter's result when one is set and the
system's interfaces otherwise, which is what makes the global's state observable from a test. The
first mutation attempt (never return the cleanup) does not compile, because the eager call sites
would then leave it unused; making the cleanup a **no-op** is the mutation that turns the test RED.


### What `a73164297` actually fixed — and why it was the most valuable item in the round

The fork's merge `c575718bf` ("merge(upstream): sync phase 2", 2026-10-04) resolved
`protocol/openvpn/{client,server}.go` and `protocol/openconnect/client.go` back to their
**pre-`74182ce8e`** state. Nothing failed to compile, every symbol the missing code needed was still
present, and `git log -S` / `--full-history` cannot see it because the removal happened inside a
merge resolution. The loss was invisible until a user started an OpenVPN **server**:

```
go test -tags "<DEFAULT_BUILD_TAGS_OTHERS>" -run TestOpenVPNSelfToSelf ./test/
--- FAIL: TestOpenVPNSelfToSelf (14.79s)   4/4 subtests
start outbound: start endpoint/openvpn-server[openvpn-server]: missing openvpn outbound queue
```

`sing-openvpn@f330676d6d4a` makes `ServerOptions.NewOutboundQueue` **required**
(`ErrMissingOutboundQueue`, `server.go:50-52`) and nothing under `protocol/openvpn` assigned it,
while the method that assignment makes reachable (`ServerEndpoint.routeOutbound`) was still in the
file, unreferenced. After the fix:

```
TestOpenVPNSelfToSelf      4/4 FAIL (14.79s) -> 4/4 PASS (2.66s)
go build -tags "<tags>" ./...                     ok
go vet ./protocol/openvpn/ ./protocol/openconnect/ ./transport/device/   ok
```

Detection note: because the presence of *symbols* was never the problem, the regression test asserts
on the **wire** — it calls the production assembly step (`applyDeviceWires`) and checks that the
installed factory reaches the device's own queue, measured by a recording device. A first version
that only compared the method against the library interface **kept passing while the assignment was
deliberately deleted**; that version was discarded.

## Already Present

Cross-cutting items verified by content (blob comparison), not by commit subject. Sibling audits
produced the per-file evidence; the numbers below are the ones they measured.

| upstream | area | mode | evidence |
|---|---|---|---|
| `a691a4402` "Refactor lifecycle to scoped cleanup" | lifecycle | FORK_STRONGER | `adapter/lifecycle.go:70-96` scope state machine + `closeDone`; `:126-138` Add runs late cleanup; `:174-183` Start refuses success after close; `:218-233` Close joins and returns the same error. Upstream has no state, no `closeDone`, no Add guard, no Start recheck, and its second concurrent Close returns `nil` mid-drain. 35/115 blobs byte-identical, ~90 files carry every added line; every other absent line is superseded (the debounce timer it stopped was deleted outright; `netns.SetControlFunc`/`SetListenPacketFunc` are never set because `hooks()` replaced them; `scope.Add(device.Close)` is at `openvpn/client.go:615`, `openvpn/server.go:170`, `openconnect/client.go:433`). The ONE genuinely absent artefact — the netmon interface-getter reset — is absorbed above. |
| `3ad7a4255` "Ignore closed and canceled errors in scope cleanup" | lifecycle | FORK_STRONGER | `:251-253` expands before judging, `:264-274` uses upstream's predicate **plus** a Debug record upstream discards |
| `a364ff479` "Remove unimplemented hot reload from managers" | lifecycle | ALREADY_ABSORBED | no `LegacyStart` / `NewLifecycleService` / `Manager.Remove` / `dependByTag` anywhere; duplicate managers already refuse |
| `02537831e` "Move auto-redirect and bridge index allocation out of constructors" | lifecycle | FORK_STRONGER | bridge half present (`backend.go:114-135`, called from all three platform backends); the tun half's discard path is covered more generally by `adapter/inbound/manager.go:62-85` |
| `f9fcb8964` "Fix usage and cache files overwritten when loading fails" | lifecycle | ALREADY_ABSORBED | the exact upstream strings are in `service/ccm/service.go`, `service/ocm/service.go`, `service/ssmapi/server.go`, with `scope.Add` in the else branch of all three |
| `6b0ca9a24`, `7ad8567e6` idle-connection work | lifecycle | FORK_STRONGER | the fork's walk is wider — it reaches outbounds, endpoints, DNS transports and the HTTP client manager through four trigger paths (`route/reference.go:440-493`) |
| `52f9bad03`, `2993177ed` protocol input validation | validation | ALREADY_ABSORBED | 14 guard sites verified individually; `common/srs/binary.go` uses `E.New` instead of panic; `http/server_h2.go` rejects status < 100 |
| `2c28efdca`, `c8d9ae4ac` rule/JSON validation | validation | ALREADY_ABSORBED | `schema/schema.go` blob-identical; `rule_item_cidr.go` blob-identical |
| `4892145b7` "Fix DNS sniffer matching non-DNS packets" | sniff | ALREADY_ABSORBED | `common/sniff/dns.go` blob **byte-identical** to upstream. All 14 checks present (header length, QR/opcode/AA/TC, RA/Z/rcode, QDCOUNT, ANCOUNT/NSCOUNT, full question parse, full additional-RR parse, trailing garbage, NeedMoreData-vs-invalid). **No fork hardening exists here** — the file is upstream's, including its four residuals. |
| `5618bef17` anytls UDP framing | deps | ALREADY_ABSORBED | the commit moved only `go.mod`; the fork's pin `v0.0.0-20260928104022-580984e4d8cb` was read in the module cache and contains every hunk (no `vectorised` field, no `WriteVectorised`, `writeRequest` at `session.go:491-515`, deadline before lock, `endWrite`-before-FIN, pooled-only `CloseIdleConnections`, no password validation) |
| `439502694` anytls migration | deps | FORK_STRONGER | pin equal to upstream head; the old client-metadata hack is gone (`client_metadata.go` deleted, no `rewriteClientMetadata`); the fork adds `first_read_timeout.go`, `preauth_log.go`, inbound fallback + `fallback_for_alpn` |
| `615cb02b1` on-demand resume | resume | ALREADY_ABSORBED | MASQUE `client.go:568-573`, OpenConnect `:503-508`, OpenVPN `:682-687` all `if keep { Resume() } else { Suspend() }`; Tailscale `endpoint.go:630-639` has **no** `systemInterface` gate and a `resumePending` CAS that prevents double Resume, with the `WantRunning` revert at `:746-749`; WireGuard `transport/wireguard/endpoint.go:603-619` has no `System` gate |
| `6e3c86f51`, `777b5eb37` iOS screen state / idle-flush removal | resume | FORK_STRONGER | `command_server.go:354,:395,:476,:504,:532-538` with `IsTvOS` guards and `WakeNow`; `idleFlushed` appears nowhere in the tree |
| `a74b38ec0`, `b1dbabc49` buffered cachefile + FakeIP consistency | cache | ALREADY_ABSORBED | `experimental/cachefile/pending.go` buffering; `dns/transport/fakeip/store.go` reservation window; `cachefile/fakeip.go:144` `bytes.Equal` guard |
| `ac17266e5` rule-set mmap on iOS | rule-set | ALREADY_ABSORBED | `common/srs/mmap*.go` present and threaded through `readRule`/`writeRule`/`readDefaultRule`/`writeDefaultRule` |
| `9b2e3e790`, `8f4ab21a2` go TUN stack / remove gVisor | tun | ALREADY_ABSORBED | `option/tun.go` identical; `with_gvisor` is absent from all three `release/DEFAULT_BUILD_TAGS*`; gvisor is `// indirect` exactly as upstream |
| `f95aa6ac1` "Update dependencies" | deps | ALREADY_ABSORBED | every pin it set was superseded later in the same range; net effect nil |
| `a0d775ea6` "Fix zero UDP checksum" | deps | ALREADY_ABSORBED | the code is in sing-tun; the revision that commit pinned has no checksum code, and the fork's newer base has `flow_dispatch.go` `completeChecksum` |
| `bff5acf29` "Handle connected UDP EOF on BSD" | dialer | ALREADY_ABSORBED | `udp_conn.go` blob identical; `control.IsUDPEOF` present |
| `827f50cd4`, `f553bdfae`, `6cea6c95a` process search | process | ALREADY_ABSORBED | plural `findProcessPaths` with a per-uid freelru (further than upstream); the Windows searcher uses the 3-arg `FindSocketOwner` |
| `89f9b561f` tailcat, `2c28efdca` schema, `ab5be3736` HTTP rewrite, `61ff9f222` H2 extended CONNECT | protocol | ALREADY_ABSORBED / FORK_STRONGER | `ab5be3736` is a large divergence: the fork is +557 lines in `capsule.go`, +310 in `client.go`, +213 in `client_h3.go` and ~20 production files upstream does not have. `80c117141` is FORK_STRONGER (the fork narrows the dependency to a 1-method `authBannerSender` seam and deliberately omits `BannerCallback`). |
| `b83089ecf` MASQUE, `0199239c5` TLS SHA-256 pinning, `d2a4dc29f` forward NAT | masque/tls/tun | ALREADY_ABSORBED | symbol and blob checks per file |
| `78d44d52d` "Reset network on DNS server changes" | DNS | REJECT (deliberate fork position) | the fork declines both halves: `route/dns_only_change_is_not_a_network_transition_test.go` pins "same interface, only DNS servers change → no transition, no reset", and the goal is met inside `systemconfig/source_darwin.go:178-215` instead |
| `0199239c5` as described in the brief | — | BRIEF ERROR | the brief labelled this "Add MASQUE support". Its real subject is "Add full certificate SHA-256 pinning for TLS"; MASQUE is `b83089ecf`. Recorded so the label is not reused. |

## Fork Stronger

| upstream | fork enhancement that must be preserved |
|---|---|
| `a691a4402` | scope state machine, `closeDone` join, Add-runs-late-cleanup, Start-refuses-success-after-close, Close returns the same error |
| `3ad7a4255` | expand-before-filter and the retained Debug record |
| `6b0ca9a24` | the four-owner idle walk (`route/reference.go:440-493`) and the `IdleConnectionReleaser` / `IdleConnectionKeeper` split |
| `02537831e` | `adapter/inbound/manager.go:62-85` rejecting a duplicate before construction |
| `80c117141` | `tailssh` `PreAuthConnCallback` **with `BannerCallback` absent**, and the `authBannerSender` seam |
| `439502694` | anytls inbound fallback, `fallback_for_alpn`, `first_read_timeout`, `preauth_log` |
| `6e3c86f51` | `IsTvOS` guards, `WakeNow`, `RecordScreenState` / `RecordLockState` |
| `ab5be3736` | the fork's own `transport/http` modules and the `maxHeaderBytes`-carrying `ConfigureHTTP3ListenerFunc` |

## Rejected / Not Applicable

| upstream | reason |
|---|---|
| `78d44d52d` | deliberately declined; pinned by `route/dns_only_change_is_not_a_network_transition_test.go`. Adopting it would re-introduce the reset that test exists to prevent. |
| `7054cac51` "Bump version" | submodule pointers, changelog and version strings — owned by the release process, not by this absorption. |
| `67fb235ee` "roothide iOS jailbreak", `1a773a425` "USB/IP legacy macOS", `d630fa94c` "Wayland tray" | the fork's Apple/build/release paths are its own; the upstream patches target a different workflow. |
| `f95aa6ac1`, `a0d775ea6` | superseded in-range; nothing to absorb. |

## Architectural Remainder

**NONE.** Nothing in this round was deferred for being too large. The one genuinely architectural
item considered — whether the headroom/backpressure work required the new sing-tun stack — was
settled by measurement rather than assumption: the fork already pins the sing-tun revision that
provides `tun.OutboundQueue`, `MemoryTunOptions.{Headroom,RearSpace,Route}`, `LinuxTUN.FrontHeadroom`
and `DarwinTUN.BatchRead(front, rear)`; `GOPROXY=off go build` of all six affected packages exits 0
against the current pin; and upstream's own sing-tun move in this range touches only
`monitor_{darwin,shared,windows}.go` plus a new `dnsinfo/` package the fork imports nowhere. **No
dependency bump was required, and none was made.**

## Tests

- **PASS**: `TestOpenVPNSelfToSelf` 4/4 (was 4/4 FAIL); `go build -tags "<tags>" ./...`;
  `go vet` on the touched packages; `./protocol/openvpn/`, `./protocol/openconnect/`,
  `./transport/device/`, `./transport/openvpn/`, `./transport/openconnect/`,
  `./protocol/tailscale/` (×5), `./adapter/`, `./route/`, root package.
- **FAIL**: none.
- **FLAKY / host-dependent**: `TestOpenVPNDockerInterop` and the other Docker-backed OpenVPN
  fixtures were **not run** — no Docker daemon was reachable
  (`open //./pipe/docker_engine: The system cannot find the file specified`).
- **NOT_RUN**: the rest of `./test/` (only the OpenVPN self-to-self fixture was executed); cross
  builds for Apple/Android/Windows; `-race` (needs cgo, and no C compiler is installed on this host).

## Dependency audit

No dependency was changed. Every pin was checked against the reason it would need to move:

| module | fork pin | action | reason |
|---|---|---|---|
| `sing-openvpn` | `v0.1.1-0.20260929151220-f330676d6d4a` | **unchanged** | already the exact revision providing `DataPacketHeadroom`, `OutboundQueue`, `RouteOutbound` *and* the hard `ErrMissingOutboundQueue` requirement |
| `sing-openconnect` | `v0.1.1-0.20260929151226-29757281a247` | unchanged | already provides `PacketHeadroom` / `PacketRearHeadroom` |
| `sing-tun` | `v0.9.7-0.20261007151655-7539c9855f19`, replaced by `Piggy-Cat-bit-shadow/sing-tun v0.0.0-20261008172655-8dde9c8cbe27` | unchanged | `GOPROXY=off` build of all six affected packages exits 0; upstream's move adds only `dnsinfo/` (imported nowhere in the fork) and `monitor_*` changes. The fork's replacement differs from its declared base in exactly `stack_go.go` + `stack_system.go`. |
| `sing-quic` | fork replace on a 2026-09-29 base | unchanged | upstream did not move it in this range |
| `sing-anytls` | `v0.0.0-20260928104022-580984e4d8cb` | unchanged | equal to upstream head, and contains the UDP-framing fix |
| `tailscale`, `wireguard-go`, `cronet-go`, `gvisor` | — | unchanged | already match upstream |

Method note: network **was** available, so the upstream revisions were downloaded and their real
source read rather than inferred from dates. `go.mod` and `go.sum` were verified unchanged
afterwards.

## Final

```
UPSTREAM_REVIEW_COMPLETE = YES
SAFE_FIXES_ABSORBED = YES
UNREVIEWED_RELEVANT_UPSTREAM = NO
ARCHITECTURAL_REBASE_REQUIRED = NO
READY_FOR_0_1_6_FREEZE = YES
```

### One caveat that belongs in the verdict

`READY_FOR_0_1_6_FREEZE = YES` is this round's answer for the items it was asked about. It is **not**
a statement that the fork has no other defects: a sibling audit found a latent nil-logger path in
`adapter/lifecycle.go`'s `Scope.Start` (`LogElapsed` dereferences the logger inside a
`time.AfterFunc`, so a nil logger would panic in the timer goroutine about a second later). It is not
reachable in production — all 13 `NewScope` call sites pass a real logger and no call site passes
`nil` — so it is recorded here rather than fixed, because the two nil checks that already exist
(`Add`, `Close`) show the author considered the case and changing `Start`'s signature would be a
behavioural change outside this round's scope.

### What upstream still owes this fork

Nothing that this round could absorb. `common/sniff/dns.go` is byte-identical to upstream, so the
fork has **no** hardening of its four residuals (compression-pointer handling relies on miekg/dns's
own bound; per-check `len(header) > N` guards; a valid header can stall the sniffer up to 65535
bytes; no per-RR validation). Those are upstream's, and fixing them would be new work rather than
absorption.
