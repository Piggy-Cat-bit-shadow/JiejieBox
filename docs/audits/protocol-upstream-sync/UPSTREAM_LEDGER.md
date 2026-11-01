# Protocol upstream-sync ledger

| | |
|---|---|
| Stage | 01A — upstream reuse map + Mieru stable update |
| Executed | 2026-11-02 (Asia/Shanghai) |
| Branch / base | `testing` / `0ab035904de5536273f6070a766b4fc657d7d77c` (fast-forwarded from the stale local `7e6cc9cc`; working tree was clean) |
| Toolchain | `go1.25.5 windows/amd64`; `-race` runs with `CGO_ENABLED=1` + mingw64 gcc |
| Implementation in this phase | **Mieru only** (row M-01 and the two adjacent decisions below) |
| Candidate rows for later phases | XHTTP (04/05), Naive (02/03), Hysteria2/TUIC/AnyTLS/VLESS/SS/Snell/MASQUE (06). These are *source-level candidates*, not authorisation to change code. |
| Companion files | `FORK_CANDIDATES.md`, `phase-01.md`, `STATE.md` |

This ledger is written to be consumed directly by the next stage. Every row names the source
revision, the JiejieBox status at the time of this audit, and the decision. A row is only
`MERGED` when code and a test landed in this branch; everything else is explicitly deferred,
inapplicable, or rejected.

## 0. Vocabulary

**Coverage** (what JiejieBox already has):

| label | meaning |
|---|---|
| `ALREADY_PRESENT` | the behaviour already exists locally (same or stronger), with a path/symbol |
| `PARTIAL` | some of the behaviour exists; the row names the missing part |
| `MISSING` | the behaviour does not exist locally |
| `INAPPLICABLE` | the upstream change is in a component JiejieBox does not ship (e.g. Xray's server), or is architecturally incompatible |
| `CONFLICT` | taking it as-is would violate a JiejieBox invariant (Scope, ownership, ABI, build tags) |

**Decision**: `MERGED`, `ALREADY_COVERED`, `DEFERRED`, `NO CHANGE`, `REJECTED`.

**Evidence labels**: `VERIFIED_SOURCE` (read the source/diff at the named revision),
`VERIFIED_UNIT` (local test run), `VERIFIED_GIT` (ancestry/pin inspection), `VERIFIED_CI` (named
run), `BLOCKED_BY_ENVIRONMENT`, `UNVERIFIED`.

Network constraint for this pass: `api.github.com` / `raw.githubusercontent.com` / `github.com`
resolve to a non-public address from this environment and `web_fetch` refuses them. GitHub facts
below therefore come through the GitHub API (MCP), and the URL is cited for humans. Where a value
was not fetched it is marked `UNVERIFIED`; nothing is filled from memory.

---

## 1. Mieru — phase 01 implementation

Upstream: `enfein/mieru` v3.39.0 = `bcd14b88d0fb7e7707bb80e5d7f6c33675d26c06` (2026-10-23), the
latest stable tag at execution time (v3.38.2 is a CI-only tag, not a release; v3.39.0-rc.1 is a
prerelease). JiejieBox previously pinned `v3.38.1` in both the root and `test/` modules.

| ID | Source / ref | What actually changes | Fork candidates | JiejieBox status | Files / symbols | Applicability & net value | License | Decision | Landed / evidence | Follow-up |
|---|---|---|---|---|---|---|---|---|---|---|
| **M-01** | `enfein/mieru` `66f54e2963ed5a6501edfe79d301b14065a15fe3` (2026-10-19, in v3.39.0) — "fix: remove client read timeout (bug #321)" | `pkg/protocol/session.go`: deletes `serverRespTimeout` and the `writeChunk` branch that stored `readDeadline = now+10s` after every **client** write. `Session.Read` captures that deadline once and returns `stderror.ErrTimeout` when it fires, so a read that started after a write expired 10 s later even while the upload was healthy. Upstream issue #321 reproduces this as a one-way iperf3 upload that stops at 10 s. | `ushan0v/sing-box-forkop` pins `mieru/v3 v3.33.0` with a simpler adapter (no fallback / generation lifecycle); **no patch to reuse**. `uskg8/sing-box-extended` has no `protocol/mieru`. | `MISSING` before this phase (the pin carried the defect); the adapter itself needs no change — it never re-armed the deadline. | root `go.mod:15`; `test/go.mod:62`; `protocol/mieru/outbound.go`, `fallback.go` (dial/read path unchanged) | Every Mieru TCP flow through the client. Long uploads, quiet interactive peers and any request whose reply takes >10 s stopped at the 10 s mark. Fixing it in the SDK is the smallest correct change. | GPL-3.0 (source headers) | **MERGED** | `fac646ca20eca25a0129c5580df3d0497b44963c`; red/green below | Phase 02/06: watch dead-peer detection. Removing the implicit read deadline removes an accidental failure detector; upstream relies on session heartbeats + TCP. Do not add an adapter timeout without evidence. |
| **M-02** | `enfein/mieru` `18ca10d` (add `internal/bufpool`), `a2711f6` (use it in underlay write), plus read-path acquisitions in `pkg/protocol/underlay_stream.go`, `underlay_packet.go`, `apis/common/packet_over_stream.go` | Replaces per-segment `make([]byte, n)` on the underlay read/write paths and the packet-over-stream frame with a size-classed `sync.Pool`. Ownership is explicit: `Acquire` → write/read → `Release`; `Decrypt` returns independently owned plaintext, so the encrypted buffer is released after decryption. | none needed | `MERGED` through the SDK bump; **no adapter code change**. JiejieBox's UoT codec (`protocol/mieru/uot.go`) already bypasses the SDK's `PacketOverStreamTunnel`/`UDPAssociateWrapper` triple allocation, so there is no double buffering to unwind. | SDK internal; JiejieBox `protocol/mieru/uot.go` | Lower per-segment allocation on Mieru stream/packet underlay writes. Value is upstream-measured; not re-measured here. | GPL-3.0 | **MERGED** (SDK bump) | same commit | If phase 05/06 needs a number, measure allocations locally; do **not** claim a WAN speed-up. |
| **M-03** | `enfein/mieru` `2b7d62b`/`f7c2c81`/`773d774` (in v3.39.0) — server `tcp_idle_timeout` | New `ServerAdvancedSettings.tcp_idle_timeout` (std `time.ParseDuration`, minimum 1m, empty = disabled), `Mux.SetServerTCPIdleTimeout`, `IdleTrackingConn` wrapper on server TCP underlays, and a `closeIfIdle` path in `cleanUnderlay`. Default is **off**; without the setting the wrapper records activity and closes nothing. | none | `DEFERRED`: JiejieBox builds `appctlpb.ServerConfig` but has no option/schema field for it. | `protocol/mieru/config.go:387-400` (`buildServerConfig` sets `AdvancedSettings` only for `userHintIsMandatory`); `option/mieru.go` | Useful for server-side idle-resource retirement, but it is a new config surface (option + schema + docs + tests), not a defect fix, and it does not change existing behaviour. | GPL-3.0 | **DEFERRED** | — | Phase 06 (or an explicit feature request): add `tcp_idle_timeout` to `MieruInboundOptions`, validate ≥1m, set `AdvancedSettings.TcpIdleTimeout`; test that a >1m idle underlay closes while an active one survives. |
| **M-04** | `enfein/mieru` post-v3.39.0 `main`: `eaad5d5` (empty UDP payload in socks5 UDP associate), `0fe6d64` (UDP associate handle refactor), `addba18` (`UDPAssociateWrapperOptions`) | Not in v3.39.0. `eaad5d5` is a real bug fix: a zero-length UDP datagram was returned as `io.EOF` by `UDPAssociateWrapper.ReadFrom`. | none | `INAPPLICABLE` to our path: JiejieBox's UoT codec (`protocol/mieru/uot.go`) is its own framing, not `apis/common.UDPAssociateWrapper`; the equivalent behaviour is now pinned locally by `TestMieruEmptyUDPDatagramRoundTrip` (01B). | `protocol/mieru/uot.go`, `packetconn.go`, `udp_edge_test.go` | The JiejieBox codec already delivers a zero-length datagram and keeps frame alignment; no upstream change is needed for it. | GPL-3.0 | **NO CHANGE** (local behaviour covered) | `protocol/mieru/udp_edge_test.go`; `bc349d83` | Re-check the next stable only if the client path starts using `UDPAssociateWrapper`; do not pin a pseudo-version past v3.39.0. |
| **M-05** | v3.39.0 source read: handshake bound | `PostDialHandshake` still does `common.SetReadTimeout(conn, 10*time.Second)` before reading the SOCKS5 reply and clears it after (`apis/internal/handshake.go:47-50`). The removed `serverRespTimeout` was a *second*, steady-state deadline. | — | `ALREADY_PRESENT` contract: `protocol/mieru/fallback.go:717-750` documents/serialises this as the handshake bound; `outbound.go:25-29` (`dialDrainTimeout`) relies on it. | `protocol/mieru/fallback.go`, `outbound.go`; existing `fallback_e2e_test.go:673` asserts Close returns in <7 s while a handshake hangs | Confirms no adapter change is needed and no comment became stale. | GPL-3.0 | **NO CHANGE** | full suite green | — |
| **M-06** | v3.39.0 source read: server wrapper | `serverWrapTCPConn` now wraps the raw TCP conn in `common.NewIdleTrackingConn` unconditionally, but `cleanUnderlay` only calls `closeIfIdle` when `serverTCPIdleTimeout > 0`. | — | `INAPPLICABLE` until M-03 is implemented; no default behaviour change. | SDK internal | — | GPL-3.0 | **NO CHANGE** | — | Covered by M-03's test when that lands. |

### Mieru evidence (this branch)

```
# red: the new contract test against the old pin
go get github.com/enfein/mieru/v3@v3.38.1
go test ./protocol/mieru/ -run TestMieruClientReadSurvivesLongUnidirectionalUpload -count=1
    --- FAIL: ... (11.05s)
        upload_idle_test.go:116: Received unexpected error: TIMEOUT

# green: v3.39.0
go test ./protocol/mieru/ -count=1                       ok  github.com/sagernet/sing-box/protocol/mieru  91.161s
go test -race ./protocol/mieru/ -count=1                 ok  github.com/sagernet/sing-box/protocol/mieru  97.309s
go mod tidy -diff            (root and test/)           exit 0
go list -mod=readonly -m all (root and test/)           exit 0
go mod verify                (root and test/)           all modules verified
go build ./protocol/mieru/ ./include/                   exit 0
```

The regression test is `protocol/mieru/upload_idle_test.go`: it arms the former deadline with a
write, starts a read that waits for a reply, uploads for 11 s, and only then lets the server answer.
It is skipped under `-short` because the defect window is the SDK's former 10 s deadline.

---

## 2. XHTTP — candidates for phase 04 (correctness) and 05 (data path)

Authority: `XTLS/Xray-core`, `transport/internet/splithttp`. JiejieBox ships a **client only**
(`transport/v2rayxhttp`), so server-side Xray changes are not implementable here; they are listed
anyway because they change what the client must interoperate with. Forks: `Leadaxe/sing-box-lx`
(lx.25 is the design JiejieBox's comments already cite) and `flyzstu/xhttp-box` (independent
replant onto sing-box v1.14.4).

| ID | Source / ref | Change | Fork candidates | JiejieBox status | Files / symbols | Applicability & net value | License | Decision | Landed / evidence | Follow-up |
|---|---|---|---|---|---|---|---|---|---|---|
| **X-01** | `XTLS/Xray-core` `316e07a53822aa23661cb3f8b30e1132911c5b36` "XHTTP server: Add `h2Params` (#7295)" 2026-10-31 | Xray **server** can tune HTTP/2 parameters per XHTTP server. | none | `INAPPLICABLE` (no XHTTP server in JiejieBox). | — | Interop only: a server using non-default `h2Params` must still carry the client's streams. | MPL-2.0 | **NO CHANGE** | — | Phase 04: add an interop fixture/contract case for an HTTP/2 peer with non-default stream windows if a server config becomes available; do not implement a server. |
| **X-02** | `XTLS/Xray-core` `e7a69fa85a18146e83e008b6ec0a72e996dc6cf9` "XHTTP server: Accept OPTIONS as `uplinkHTTPMethod` (#7290)" 2026-10-31 | Xray server now accepts `OPTIONS` as the uplink method. | none | `ALREADY_PRESENT` on the client side: `meta.go:121-131` upper-cases the configured method and only special-cases `GET`; `option/v2ray_xhttp.go:98` exposes it. | `transport/v2rayxhttp/meta.go`, `option/v2ray_xhttp.go` | The client can already emit `OPTIONS`; the change is server acceptance. | MPL-2.0 | **NO CHANGE** | — | Phase 04: one interop test that a configured `uplink_http_method=OPTIONS` reaches the wire unchanged (no fallback). |
| **X-03** | `XTLS/Xray-core` `eef6e63bc16d6e97fd98178556d49aad7978809a` "XHTTP client: Fix a data race in WaitReadCloser (#6694)" 2026-09-07 | Xray replaced a channel + `recover()` late-bound reader with `done.Instance` + `atomic.Pointer`, closing a race between `Set` and `Close`. | none | `INAPPLICABLE` as a patch: JiejieBox has no `WaitReadCloser`; its late-bound reader is guarded by `created`/`readerErr` (`conn.go:585-600`, `730-742`) with dedicated deadline/deadlock tests. | `transport/v2rayxhttp/conn.go` | Different mechanism, same class of race. JiejieBox's own race tests are the authority here. | MPL-2.0 | **ALREADY_COVERED** (design) | — | Phase 04: keep the existing `-race` contract tests in scope; do not port the type. |
| **X-04** | `Leadaxe/sing-box-lx` `f2e24e14b6798e5b790ad2797d10e94dbde9daef` "lx(xhttp): client audit against Xray — a refused or ended stream-up upload fails Write…" 2026-10-28 (lx.25) | lx makes a stream-up upload that the server refused/ended fail the write path. | This is the fork candidate itself. | `PARTIAL`: `dialStreamUp`'s upload goroutine only fails the conn on a transport error; a non-200 `upResp` is drained and ignored (`conn.go:224-231`), unlike the download path (`:213-217`) and stream-one (`:164-168`). | `transport/v2rayxhttp/conn.go:224-231` | A proxy that answers the upload POST with 4xx/5xx (refused stream, wrong path, nginx error page) currently leaves the conn looking healthy until later. | GPL-3.0 | **DEFERRED** | — | **Phase 04 first item**: port the failure propagation (compare lx's implementation), add a test with a scripted RoundTripper returning 502 for the upload and 200 for the download, and verify the write fails with a transport-class error. |
| **X-05** | `Leadaxe/sing-box-lx` `fad4fd0633e309cf303ee88d5be7c2780ac4b87f` "docs(lx): SPEC 141 — packet-up upload pipeline" 2026-10-28; `21f7178a…` SPEC 142 | lx.25's packet-up upload pipeline (queue writes into one POST) and the client audit fixes. | lx.25 | `ALREADY_PRESENT` in shape: JiejieBox has a packet-up pipeline (`conn.go` packet uplink, `packetup_batch_test.go`, `packetup_pipeline_probe_test.go`, `packetup_uplink_debug_test.go`) and comments cite SPEC 002/061/072/077. | `transport/v2rayxhttp/conn.go`, `packetup_*_test.go` | The design is absorbed; equivalence of edge cases must be checked, not re-implemented. | GPL-3.0 | **ALREADY_COVERED** (design) | — | Phase 05: diff lx.25's exact batching/flush conditions against `packetUplink` and keep only gaps that survive a local test. |
| **X-06** | `flyzstu/xhttp-box` `597f5303769169c273f177f40b8fbf30adf063db` "feat: 重植本地补丁集至上游 v1.14.4 基线" 2026-10-30 (HEAD `2e7e53d`, 2026-10-31) | Independent `transport/v2rayxhttp` tree replanted onto sing-box v1.14.4, with its own weighted group and log rotation. | `xhttp-box` | `UNVERIFIED` — not diffed this phase. | candidate: `transport/v2rayxhttp/*` in the fork | A second independent client implementation is the best oracle for compatibility bugs; useful for phase 04/05 comparison. | GPL-3.0 (sing-box fork) | **DEFERRED** | — | Phase 04: fetch the fork at `2e7e53d` and diff `transport/v2rayxhttp` against JiejieBox only for behavioural deltas; never import its group/log code. |
| **X-07** | `XTLS/Xray-core` `8267cf953ad67110bec87d106dc2f89d438fb338` "Transport: Refactor to be based on Finalmask's dialer & listener (#6754)" 2026-09-20 | Large transport-layer refactor in Xray. | — | `CONFLICT`: JiejieBox's `transport/v2rayxhttp` is built on sing/common dialers and its own XMUX; adopting the Finalmask structure would rewrite the transport layer. | — | Not a patch; a redesign. | MPL-2.0 | **REJECTED** (as a port target) | — | Re-read only if a concrete interop break is traced to this refactor. |
| **X-08** | `XTLS/Xray-core` `d1140de2734e5000331748bbbab640c2d7d336ac` "Chore: Regenerate *.pb.go files" 2026-10-31 | Generated protobuf refresh. | — | `INAPPLICABLE` (no Xray protobufs in JiejieBox's XHTTP client). | — | — | MPL-2.0 | **NO CHANGE** | — | — |

---

## 3. Naive — candidates for phase 02 (client/Cronet) and 03 (server/shared HTTP)

Authorities: `klzgrad/naiveproxy` (client, BSD-3-Clause) and `klzgrad/forwardproxy` (server,
Apache-2.0). JiejieBox's client is the custom `Piggy-Cat-bit-shadow/cronet-go` tree
(Chromium 143 base + JiejieBox patches, four modules, native archives controlled by
`release/source-lock.json`); its inbound is its own `protocol/naive` + `transport/http`, not Caddy.

| ID | Source / ref | Change | Fork candidates | JiejieBox status | Files / symbols | Applicability & net value | License | Decision | Landed / evidence | Follow-up |
|---|---|---|---|---|---|---|---|---|---|---|
| **N-01** | `klzgrad/naiveproxy` `23953c90e3b9f08f8d0c6ede92ae35f357af6f73` "Block compromised eMudhra intermediates" 2026-10-22 | Trust-store block list update for Chromium's certificate verifier. | — | `UNVERIFIED`: lives in the Cronet/Chromium native layer; JiejieBox builds from the fork's Chromium-143 archive, not from naiveproxy's Chromium-154 branch. | `cronet-go` native modules (controlled pin `8c68ce89873c`) | Security-relevant, but changing it means rebuilding the Chromium/Cronet archive, which this repository deliberately treats as controlled. | BSD-3-Clause | **DEFERRED** | — | Phase 02: determine whether the fork's Chromium 143 archive already carries the equivalent revocation data; if not, record as `BLOCKED_BY_ENVIRONMENT` (missing Chromium toolchain), not as "fixed". |
| **N-02** | `klzgrad/naiveproxy` `367dae72…` "naive: Set quic settings" (2026-10-06), `20954520…` "net/quic: Enable dynamic scaling of window size" (2026-10-06), `dad448f3…` "Try to fix crash queued reads after disconnect" (2026-10-05), `2655ebc3…` "Fix quic locally trusted cert roots" (2026-10-05), `77144595…` "naive: Soften old tunnel cleanup" (2026-10-07) | Chromium-side QUIC window/heartbeat and tunnel-cleanup changes. | — | `UNVERIFIED` whether the fork's 143 base + patches contain these; JiejieBox does have Go-side H3 fallback and single-engine handling (`protocol/naive/h3_fallback.go`, `single_engine_option_test.go`). | `protocol/naive/*`, `protocol/naive/quic/*`, cronet-go native archives | Potentially direct value for H3 throughput and old-tunnel cleanup; the Go side cannot emulate a native QUIC change. | BSD-3-Clause | **DEFERRED** | — | Phase 02: inspect the fork's Chromium revision and patch list (source pins, not version numbers); only then decide whether the controlled archive must move. Do not silently drift `cronet_native`. |
| **N-03** | `klzgrad/forwardproxy` `e747854e78743dba85c039b38ed185fb11d93667` "Do not break authenticated stream after read_body_idle" 2026-10-22 | Backports Caddy's idle-timeout override and applies a 24 h idle timeout to **authenticated** CONNECT tunnels, so Caddy's `read_body_idle` no longer kills a healthy authenticated stream. | — | `PARTIAL`/`UNVERIFIED`: JiejieBox's Naive inbound is not Caddy; its own CONNECT stream handling lives in `protocol/naive/inbound.go` and `inbound_conn.go`. Whether an idle deadline is applied to an authenticated CONNECT stream must be checked against the local code, not assumed from upstream. | `protocol/naive/inbound.go`, `inbound_conn.go`, `transport/http` | If JiejieBox applies a shorter read-idle to an authenticated tunnel, long-idle Naive sessions break; that is the exact failure upstream just fixed. | Apache-2.0 | **DEFERRED** | — | **Phase 03 first item**: read the local CONNECT stream deadline handling, decide whether the semantic (authenticated stream may stay idle; hard deadlines still apply) holds; add a test only if a real gap is found. |
| **N-04** | `klzgrad/forwardproxy` `c096d6a00cb28e019cc1995b04bdc6a9311d8024` "Use sing-quic BBRv1 for QUIC" 2026-10-06 | Server-side QUIC congestion control switches to sing-quic's BBRv1. | — | `UNVERIFIED`: JiejieBox's Naive H3 inbound uses `protocol/naive/quic`; whether it configures a congestion controller must be read locally. | `protocol/naive/quic/` | Possible server-side throughput alignment; requires the local QUIC option plumbing to accept it. | Apache-2.0 | **DEFERRED** | — | Phase 03: check `inbound_init.go`/QUIC config for congestion-control options; add only if the local stack already supports the knob. |
| **N-05** | `SagerNet/cronet-go` `f2dd023ae55765eb4633914f39c44b5466b6f1ab` "Fix Naive UDP relay cleanup on socket close", `172bb4ede18c54588cf52b16930812e355fbc3b5` "Fix bidirectional stream cancel racing with destroy", `049f2909701ca088c9569f6b303bba5376a2b2ff` "Fix Windows UDP socket close callback registration" (2026-09-10…12) | Three client-side lifecycle/UDP fixes in cronet-go. | `Piggy-Cat-bit-shadow/cronet-go` HEAD `c9c0e6ec884588e72c7970341e2de65b0c307e59` | `ALREADY_PRESENT`: all three are ancestors of the fork HEAD (`git merge-base --is-ancestor`), which is the module the build actually uses (`replace` at `go.mod:231`). Upstream's newest tag is `v150.0.7871.63-3` (2026-09-12); JiejieBox deliberately pins a 143-based fork. | `go.mod:231`, `cronet-fork` | No action: the pin already carries the fixes. The version string is not comparable across Chromium lines. | GPL-3.0 (SagerNet wrapper; Chromium parts keep their own notices) | **ALREADY_COVERED** | `VERIFIED_GIT` | Phase 02: re-check ancestry after any cronet pin move; never "upgrade" by number. |
| **N-06** | `klzgrad/naiveproxy` release line | Upstream stable 154 series (security patch 2026-10-22); JiejieBox's fork is Chromium 143 + custom patches. | — | `CONFLICT` with a naive fast-forward: the fork is not a stale copy of upstream `main`; its patches (`DialEarlyWithQUIC`, per-CONNECT H3/H2 choice, `quic_fallback_to_h2`, `insecure_concurrency_single_engine`, receive windows, `CloseAllConnections`) must not regress. | `proto/naive/outbound.go`, `protocol/naive/*`, cronet-fork | Any upgrade is a controlled-archive project, not a version bump. | BSD-3-Clause | **NO CHANGE** | — | Phase 02 owns the comparison plan; evidence must be source `pin + archive`, not release numbers. |

---

## 4. Hysteria2 / TUIC — candidates for phase 06

Authority for Hysteria2 moved: `apernet/hysteria` now resolves to **`HyNetworks/hysteria`**
(stable tag `app/v2.13.0`, 2026-10-05; HEAD `fb25dc852ebbc70658e67889f411887a82b7c281`,
2026-10-27). TUIC's authority `tuic-protocol/tuic` is dormant (HEAD `8e118f2`, 2025-03-13; last
release 2023). The active implementation line for both is `SagerNet/sing-quic` (HEAD `7e0a5869`,
2026-10-20; latest tag `v0.7.1`), which JiejieBox forks three commits ahead of.

| ID | Source / ref | Change | Fork candidates | JiejieBox status | Files / symbols | Applicability & net value | License | Decision | Landed / evidence | Follow-up |
|---|---|---|---|---|---|---|---|---|---|---|
| **H-01** | `HyNetworks/hysteria` `4849cda3…` (multi-packet QUIC ClientHello sniffing), `62d10167…` (port-hopping redirects), `75aa2f3c…` (HTTP-proxy 10 s cutoff), `4a0f102b…` (upstream sing-tun) | Wire/behaviour changes on the reference implementation, not on the Go module JiejieBox compiles. | none found | `INAPPLICABLE` as direct patches; JiejieBox consumes `sing-quic/hysteria2`. | `protocol/hysteria2/*` | Only relevant if `sing-quic` adopts the same semantics; the reference server is not shipped. | MIT (`UNVERIFIED` this run) | **NO CHANGE** | — | Phase 06: check `sing-quic` main after `7e0a5869` for equivalents before importing anything from the reference implementation. |
| **H-02** | `SagerNet/sing-box` `d41e1745e6d0fabdc8083556f98309461535f622` "Fix UDP fragmentation" (2026-09-27), `a691a440…` lifecycle refactor (2026-10-01) | Upstream sing-box fixes in its Hysteria2 wrapper. | — | `UNVERIFIED`: JiejieBox's `protocol/hysteria2` is a fork-shaped copy with five extra ceiling/parrot tests; the two commits may already be in its base. | `protocol/hysteria2/` | Could matter for UDP fragmentation and cleanup ordering; compare before porting. | `sing-box` GPL-3.0 | **DEFERRED** | — | Phase 06: `git log`/patch-id the local file history against these two commits; port only a surviving gap. |
| **T-01** | `SagerNet/sing-quic` `75c3ac4f…` "Fix idle connection close racing with new streams" (2026-10-02), `e702a735…` "Fix protocol input validation" (2026-09-29) | TUIC implementation fixes in the module. | — | `ALREADY_PRESENT`: JiejieBox's fork base is `7e0a5869` (2026-10-20), which is after both commits; the local outbound comment pins the previous base. | `protocol/tuic/*`, `go.mod:318` | No action; the pin is deliberately ahead of the last upstream tag. | GPL-3.0 | **ALREADY_COVERED** | `VERIFIED_GIT` | Phase 06: re-check when `sing-quic` cuts a tag newer than `v0.7.1`. |
| **T-02** | `tuic-protocol/tuic` repository state | No commits since 2026-09-01; spec is the de-facto frozen reference (`SPEC.md`). | none | `INAPPLICABLE` (no upstream movement). | — | — | GPL-3.0 (`UNVERIFIED`) | **NO CHANGE** | — | Phase 06: treat `sing-quic/tuic` as the implementation of record. |

---

## 5. AnyTLS / VLESS / Shadowsocks / Snell — candidates for phase 06

| ID | Source / ref | Change | Fork candidates | JiejieBox status | Files / symbols | Applicability & net value | License | Decision | Landed / evidence | Follow-up |
|---|---|---|---|---|---|---|---|---|---|---|
| **A-01** | `SagerNet/sing-anytls` `580984e4d8cb10f1c5e88d7ec14cfe4d955d66e6` (2026-09-28, HEAD; module has **no tags**) | Padding-scheme parsing fix, UDP-over-TCP framing/session lifecycle fixes. | — | `ALREADY_PRESENT`: JiejieBox pins exactly that SHA (`go.mod:48`). | `protocol/anytls/*` | No newer stable exists; only SHA pinning is possible. | GPL-3.0 | **ALREADY_COVERED** | `VERIFIED_GIT` | Phase 06: re-fetch HEAD; if it moves, evaluate the delta rather than bumping blindly. |
| **A-02** | `SagerNet/sing-box` `0159d28b4098b18715c09958408c324e6f2d2490` "Migrate anytls into our own library" (2026-10-23), `410b21b0…` "Fix AnyTLS splitting UDP packets into multiple frames" (2026-09-24) | Upstream moved AnyTLS metadata/padding into its own library and deleted `protocol/anytls/client_metadata.go`. | — | `PARTIAL`: JiejieBox imports `sing-anytls` directly and adds `first_read_timeout.go` / `preauth_log.go`; behaviour parity with the new upstream library is not established. | `protocol/anytls/*`, `go.mod:48` | Upstream's motivation was memory/quality; JiejieBox's local additions may already cover part of it. | GPL-3.0 | **DEFERRED** | — | Phase 06: compare `sing-anytls` pin vs upstream library on padding/UDP framing; do not replace the adapter wholesale. |
| **V-01** | `XTLS/Xray-core` `v26.10.31` (2026-10-31), urgent security release: MPTCP default-off; `v26.10.29` withdrawn | Security-driven default change in Xray's socket layer. The release body also warns that Android clients' Socks/HTTP inbounds are mildly exposed when "allow LAN connections" is enabled. | none | `UNVERIFIED`: JiejieBox is a Go stack and may not expose MPTCP at all; no Xray server is shipped. | `protocol/vless/*` (protocol only); Android Socks/HTTP inbounds in the client repo | Likely `INAPPLICABLE` to the protocol path, but the Go runtime / socket layer must be checked before dismissing it. | MPL-2.0 | **NO CHANGE** | — | Phase 06: one targeted check of Go MPTCP defaults (`GODEBUG=multipathtcp`), listener socket options and the Android Socks/HTTP LAN setting; record the result, do not port Xray's fix. |
| **V-02** | `github.com/sagernet/sing-vmess` latest tag `v0.2.8`; JiejieBox pin `v0.2.9-0.20260929152519-9b95ab8c9478` (2026-09-29) | VLESS/VMess shared module; post-tag pseudo-version. | none | `UNVERIFIED` whether the pin equals module HEAD. | `protocol/vless/*`, `go.mod:60` | No newer **tagged** stable exists. | GPL-3.0 | **DEFERRED** | — | Phase 06: `go list -m -versions` + HEAD check; only bump for a named fix. |
| **S-01** | `shadowsocks/shadowsocks-rust` `dc43297211b1a4098b473a985a7278717f1d7f0e` (2026-10-22, half-closed tunnel idle bound / fd leak with SIP003 plugins) | Rust reference implementation fix. | — | `INAPPLICABLE` as a patch: JiejieBox uses `sing-shadowsocks*`, not the Rust implementation. | `protocol/shadowsocks/*` | The same half-close lifetime question applies to the Go path; check it locally instead of porting Rust code. | MIT (`UNVERIFIED`) | **NO CHANGE** | — | Phase 06: verify the Go half-close path has no unbounded idle lifetime; add a test only if the local code shows the gap. |
| **S-02** | `SagerNet/sing-box` `07130365d1415c23fca3a2170d52c7c61c56166d` "Improve idle connection management" (2026-09-03), `52f9bad0…` "Fix protocol input validation" (2026-09-28) | Upstream sing-box improvements in common/protocol paths. | — | `UNVERIFIED`: `protocol/shadowsocks` has local multi-inbound/relay/MTU files that upstream does not, so a clean comparison is needed. | `protocol/shadowsocks/{inbound,inbound_multi,inbound_relay,outbound,stream_mtu}.go` | Potential idle/fd correctness value, not a mechanical cherry-pick. | GPL-3.0 | **DEFERRED** | — | Phase 06: patch-id the two commits against the local tree; port only verified gaps. |
| **SN-01** | `SagerNet/sing-snell` `bc5a12ac736f235b2de2926ecd2791cc925e6b8c` (2026-09-04, HEAD; module has **no tags**) | "Add SetKeepIdleConnections, CloseIdleConnections and ContextWithKeepSession to clients" — exactly JiejieBox's pin. | — | `ALREADY_PRESENT` (`go.mod:57`). | `protocol/snell/{inbound,outbound}.go` | No newer commit exists. | GPL-3.0 | **ALREADY_COVERED** | `VERIFIED_GIT` | Phase 06: re-fetch HEAD. |
| **SN-02** | `SagerNet/sing-box` `0164ce7b8e0a60a0542ee118bdae6451d7362ae9` "Fix shared connections inheriting external interrupt mark" (2026-10-20), `33e63afb…` "snell: Fix UDP requests to domain destinations" (2026-08-24) | Upstream snell wrapper fixes. | — | `UNVERIFIED`: JiejieBox's `protocol/snell` is a thin wrapper; whether it shares the interrupt-mark path is unknown. | `protocol/snell/*` | Possible correctness fix; compare the wrapper shape first. | GPL-3.0 | **DEFERRED** | — | Phase 06: diff the wrapper against upstream; port only if the same pattern exists locally. |

---

## 6. MASQUE — phase 06 **premise correction**

The 总纲 says "MASQUE 工程基线主要对齐 sing-box；只考虑共享 HTTP/QUIC 层…". At execution time that
premise is outdated in one important way: **official sing-box now has MASQUE on `dev`**, merged
2026-10-23 (`306235ebc3d46117fbe436d9906cb8388cad01b0`, "Add MASQUE support", +4662/−1363). It is
**not** in stable `v1.14.4`. "Align to sing-box" therefore means align to upstream's new
`protocol/masque` endpoint and the shared `transport/http` capsule/client layer — still not a new
MASQUE project, and still not a reason to replace JiejieBox's much larger endpoint/data plane.

| ID | Source / ref | Change | Fork candidates | JiejieBox status | Files / symbols | Applicability & net value | License | Decision | Landed / evidence | Follow-up |
|---|---|---|---|---|---|---|---|---|---|---|
| **Q-01** | `SagerNet/sing-box` `306235eb…` (2026-10-23, dev only), `3454d6d2…` backpressure (2026-10-18), `0976c36c…` per-destination backpressure (2026-09-25) | New upstream `protocol/masque/{client,server,endpoint}.go`, `option/masque.go`, `test/masque_test.go`; reworks `transport/http/capsule.go`, `client.go`, `client_h2.go`. | — | `ALREADY_PRESENT` in a larger, independent form: `protocol/masque` (~30 files incl. 46 KB client, DNS assignment/bootstrap race) and `transport/masque` (55 files: session, capsule, DoHPath/SVCB, pools); shares `transport/http`. | `protocol/masque/*`, `transport/masque/*`, `transport/http/*` | Upstream is a small endpoint; JiejieBox's is a product-scale data plane. The shared HTTP/3 capsule layer is where alignment can reduce duplication; the endpoint API alignment is a compatibility question, not a rewrite. | GPL-3.0 | **DEFERRED** | — | **Phase 06**: (1) diff `transport/http/capsule.go` + `client.go` against upstream dev; (2) check whether the backpressure fixes correspond to local tests; (3) decide endpoint/API alignment only after a stable tag ships MASQUE. |
| **Q-02** | `Leadaxe/sing-box-lx` `1858657b04cc5977de06295ea21fb205380df883` "lx(masque): SPEC 146 — Box.Close hung behind Outbound.Close…" 2026-11-01 (lx.28) | Fixes a `Box.Close` deadlock: `ensureSession` held `runMu` across an h3 dial through a detour, and quic-go's single-use `Transport.Close` waited for its listener after a no-op read deadline; adds `close_wedge_lx_test.go`. | lx.28 | `UNVERIFIED`: JiejieBox has its own close-boundary tests (`close_boundary_h3_test.go`, `close_boundary_test.go`) and a different MASQUE stack; the same wedge may or may not exist. | `protocol/masque`, `transport/masque` | Directly relevant failure mode (close behind a detour dial with a no-op deadline conn); a good adversarial test recipe even if the patch does not apply. | GPL-3.0 | **DEFERRED** | — | Phase 06: reproduce the wedge scenario locally (detour conn whose `SetReadDeadline` is a no-op) and only then port the sync change. |

---

## 7. Cross-cutting queue for later phases

1. **Phase 02 first**: read the cronet fork's source pins + native archive manifest, not version
   numbers; decide whether any 154-series native change must be rebuilt (controlled archives).
2. **Phase 03 first**: the authenticated-CONNECT idle semantic (N-03) and the local CONNECT
   deadline handling.
3. **Phase 04 first**: X-04 (`dialStreamUp` non-200), then X-06's independent implementation diff.
4. **Phase 05**: X-05 equivalence, plus local allocation measurements if the bufpool change needs
   a number.
5. **Phase 06**: H-02 / A-02 / S-02 / SN-02 patch-id comparisons; MASQUE Q-01/Q-02 only after the
   shared-layer diff.
6. **Do not** treat any row as "the fork is behind" — the rows are hypotheses with a named file and
   a named upstream revision, to be re-verified before code is touched.

## 8. Reproduce this audit

```sh
git -C C:\Deepseek\jiejiebox-docs log -1 --format='%H %ci %s'
git -C C:\Deepseek\jiejiebox-docs status --porcelain
go version   # go1.25.5
```

Upstream evidence is in `FORK_CANDIDATES.md` (fork heads and what they carry) and in the commit
URLs cited in each row. Local Mieru evidence is reproduced in §1.
