# FINAL_INTEGRATION_REPORT — JiejieBox upstream sync cycle

| | |
|---|---|
| Cycle | 01A/01B, 02A/02B, 03A/03B, 04A/04B, 05A, 06A/06B |
| Branch | `testing` (default; repository has no `main`) |
| Date | 2026-11-02 |
| Head at report time | `decdcef4` + this final closeout docs commit |
| Status | **CLOSED — Native Naive security updates explicitly deferred to the next Chromium upgrade** (see §6; the current-tree risk is recorded, not claimed fixed) |

## 1. What was absorbed

| Phase | Commit(s) | Actual change | Evidence |
|---|---|---|---|
| 01A | `fac646ca` | `enfein/mieru` `v3.38.1 -> v3.39.0` in both modules | 10 s client read-deadline regression test red on v3.38.1, green on v3.39.0; full/race suites |
| 01B | `bc349d83` | empty-UDP and idle-underlay coverage; SDK half-close limit recorded | unit tests, `-race`, CI |
| 02A | `f13f46e4` (dependency `bd2b705`) | cronet-go `fa319c4` port: the dialer, not Chromium, resolves the Naive server name | fork tests `TestFakeServerAddressWrapper`, `TestDialDestinationMapsTheFakeServerAddress`; JiejieBox tagged tests/race; provenance gate |
| 02B | `f15d4473` (docs) | hostile review, no code defect | cronet binding `-race`; lifecycle/DNS/fallback tests; pin/lock checks |
| 03A | `e684354f` (docs) | server candidate audit: all covered, no forced patch | race 1.923 s; 20 s/15 s fuzz; TCP-only and `with_quic` builds |
| 03B | `e8ec5c67` | H2/H3 CONNECT with `:scheme`/`:path` now answers **400**; it used to log and let net/http send an implicit 200 | red 200 -> green 400; 407 control; package 0.373 s, tagged race 1.908 s |
| 04A | `96bd665d` | stream-up upload non-200 now fails the stream: closes the body, breaks the pipe, cancels the download | red dial parked; package 17.8 s, race 19.0 s |
| 04B | `b5c12db3`, `669fa275` | refusal/cancellation precedence fixed; 400/404/413/500 matrix with a never-reading body; post-raise read/write | red/green; matrix; race |
| 05A | `d6beda62` (docs) | packet-up pipeline evaluated and **rejected**: `maxInflight=1` at 0–200 ms, post construction 1.8 µs/15 allocs vs a 1 MiB batch; historical RTT ratios 1.18/1.23/1.07/1.01/0.97x | cadence probe 9.247 s; benchmarks; package/race from 04B |
| 05B | `7211ad71` (docs) | queue-state review: concurrency/coalescing/close-stage, non-200/failure/GOAWAY, bounded backpressure, XMUX isolation mapped to existing tests; no code defect | targeted `-race` `ok 11.158s` |
| 06A | `7f322a90` | `sing-vmess` -> `a0c58542` ("Fix vision close after direct write"); Vision ABI tripwire re-audited and deliberately updated | VLESS/Vision tests; `go mod tidy`; release freeze PASS |
| 06A docs | phase-06 commit | convergence report; no release/deploy | this report |
| 06B | `6d4164f8` + final docs | hostile review of the 06 delta only; no new defect; cycle closed | Vision tripwires; `go mod verify`; cross-phase Naive/XHTTP `-race`; CI |
| 06C | `b218345e` (+ `2195ddc7`, `10d61856` CI) | **security**: MPTCP disabled by default on every listener using `common/listener`; `tcp_multi_path: true` remains the explicit opt-in | Ubuntu runner `TestListenTCPMultiPathIsOptIn --- PASS` (not skipped); Windows package test; cross-compile/vet |

## 2. Accepted / rejected decisions

Accepted:

* Mieru `v3.39.0` (the client read-deadline fix is real and test-pinned).
* cronet `fa319c4` (server name resolved by the sing-box dialer, not Chromium's synthetic address).
* Naive H2/H3 CONNECT rejection status (a real "looks accepted" bug).
* XHTTP stream-up refusal failure propagation (real stall; four statuses covered).
* `sing-vmess` Vision close fix (dependency bump, ABI audited).

Rejected / no change (with evidence):

* packet-up bounded-async pipeline (X-09): no measured evidence that the gain outweighs the
  reorder/backpressure/close complexity. The historical RTT ratios are a cross-implementation
  comparison, not an upper bound on another pipeline's gain; the decision is `NO CHANGE`, not a
  "3% ceiling".
* Naive server `forwardproxy` idle/BBR/H2/close/padding/ALPN candidates: already covered; BBR is an
  explicit opt-in; H3 idle timeout matches the reference (Caddy's `quic.Config` leaves it unset).
* Xray `WaitReadCloser`/race/`GetBody`/trailing-slash/`h2Params`/`OPTIONS`: no surviving gap in the
  client (see ledger).
* AnyTLS/Snell/Shadowsocks/Snell sing-box commits: ancestors of this checkout or pins already at
  module HEAD; no port needed.
* MASQUE endpoint rewrite: upstream is dev-only; the local data plane is independent. The lx.28
  close-wedge recipe was not reproduced.
* Native Naive `-3/-4/-5`: blocked on rebuilding the controlled Chromium archives.

## 3. Verification summary

* Tagged package tests and `-race` for Mieru, Naive (client/server), XHTTP, VLESS/Vision, Hysteria2,
  TUIC, AnyTLS, Shadowsocks, Trojan.
* Fuzz: Naive padding 20 s/477k execs; CONNECT authority 15 s/892k execs.
* Builds: untagged, `with_quic`, Windows profile (`with_purego`, `with_naive_outbound`), tag matrix.
* Dependency integrity: `go mod tidy` clean in root and `test`; `release-freeze.py verify --lock
  release/source-lock.json` PASS; cronet provenance gate; 29 controlled native libs unchanged;
  `cronet_native.shas` unchanged.
* CI: each pushed head's declared workflows were run; final head results are recorded in
  `STATE.md`/`phase-06.md`. The one unrelated route flake on a docs-only head was rerun and passed;
  it is recorded rather than hidden.

### 3.1 Final 05B / 06B / 06C checks

* 05B: the packet-up/XMUX attack matrix maps to existing tests; targeted `-race` run
  (`TestPacketUp|TestSplitConn|TestXmux|TestBreaker`) `ok 11.158s`; no code defect.
* 06B: Vision ABI tripwires and `go test ./protocol/vless/...` pass against `sing-vmess a0c58542`.
* `go mod verify` passes in both modules; `release-freeze.py verify` PASS (`0.1.36`).
* Cross-phase `-race` on the two previous protocol surfaces still passes with the new dependency
  graph: Naive `ok 1.951s`, XHTTP `ok 19.060s` (`with_quic`).
* 06C: `go test ./common/listener/` (Windows) `ok`; Linux cross-compiles and `go vet` passes; the
  Ubuntu runner executes `TestListenTCPMultiPathIsOptIn` and records `--- PASS` (the kernel supports
  MPTCP, so the assertion ran rather than skipped).
* Git: branch `testing`, HEAD == remote, clean tree, no tag at HEAD, no gitlink change.

## 4. Known residual risks

1. **MPTCP listener default — fixed for managed listeners (`b218345e`) and the API/debug listeners
   (`d3f38495`).** Go 1.25.5 enables MPTCP for Linux listeners by default; Xray v26.10.31 disabled it
   as an urgent security default because a vulnerable kernel's MPTCP listener is remotely
   exploitable. `common/listener` covers product inbounds (`tcp_multi_path` remains the explicit
   opt-in), and the debug HTTP, Clash API, v2ray API, libbox pprof and boxdd TCP listeners now use
   `common/netx.ListenTCP`. Loopback-only and Unix/UDP endpoints are unchanged with source evidence.
2. **Native Naive gap — explicitly deferred**: Chromium `154.0.8037.49` predates the official
   `-3/-4/-5` fixes; the `-5` eMudhra blocklist commit is not in the current native branch and the
   local toolchain cannot rebuild the archives. This is an accepted, recorded security risk, not a
   done item; the next Chromium upgrade must verify it (see §6.1).
3. **No device/WAN/interop evidence**: all evidence is unit, loopback, race, fuzz, build and CI.
   Real Xray/H2/H3/Naive interop is `BLOCKED_BY_ENVIRONMENT`.
4. **Apple runtime** for the cronet resolution port is unverified.
5. **MASQUE** upstream is dev-only; endpoint alignment remains deferred until a stable tag.
6. **Mieru server** `tcp_idle_timeout` and post-release UDP-associate work are not in `v3.39.0`.

## 5. Where the evidence lives

* `docs/audits/protocol-upstream-sync/phase-01.md` … `phase-06.md`
* `UPSTREAM_LEDGER.md` — M/X/N/S/V/H/T/A/SN/Q rows, including rejected candidates and reasons
* `STATE.md` — current head, commits, deferred items, next phase
* `KNOWLEDGE/` — intentionally **not** refreshed; this cycle did not move the baseline

No release, tag, VPS action, production traffic test or credential was used.

## 6. Final close-out (2026-11-02)

### 6.1 Native Naive gap: deferred to the next Chromium upgrade

Current pins are unchanged: root/all Go binding `bd2b70516b92`, 29 native `lib/*` modules
`8c68ce8` (submodule `naiveproxy` `97ae698`). The newer native build branch
`cronet-go-dev-v154` is `be1be963`, but it does **not** contain the official `-5` security fix:

* official `-5` blocklist commit `23953c90e3b9f08f8d0c6ede92ae35f357af6f73` ("Block compromised
  eMudhra intermediates") is not an ancestor of `be1be963`; `git grep -i eMudhra` over its
  `src/net/cert` is empty.
* official `-3`/`-4` commits `2655ebc3`, `dad448f3`, `e78d109a`, `20954520`, `367dae72`,
  `77144595` are also not ancestors of that branch. The branch has its own custom Cronet APIs and
  overlapping QUIC/tunneling work, but equivalence is not proven; a rebuild is required to verify.
* the newer prebuilt `d5437a0` ("Build from fa319c4f", submodule `be1be963`) only changes
  Apple/tvOS archives; the Linux, Windows and Android `libcronet` bytes are identical to `8c68ce8`,
  so pinning it would not close the blocklist gap and would not be a native upgrade.
* local toolchain check: `gn`, `ninja`, `gclient` and `depot_tools` are missing; the `naiveproxy`
  submodule is not initialized; the available Swift `clang` is not the Chromium toolchain. A
  controlled Chromium rebuild (and the 29-module/provenance refresh) cannot run here.

**Status: `DEFERRED` by decision to the next Chromium upgrade; the project is closed with this
explicitly recorded boundary.** The native `-3`/`-4`/`-5` refresh is a separate task. Its mandatory
checks (blocklist actually present, `-3`/`-4` audited separately, 29 archives rebuilt, pins/locks/
provenance refreshed) are in
`KNOWLEDGE/reports/protocol-upgrade/FINAL_CLOSEOUT_2026-11-02.md`. No pin or checksum was fabricated.

### 6.2 Direct TCP listeners: remaining entry points closed

`common/netx.ListenTCP` pins `net.ListenConfig.SetMultipathTCP(false)` and is now used by every
direct TCP listener that can be configured to bind beyond loopback:

| Endpoint | Change |
|---|---|
| debug HTTP (`debug_http.go`, `options.Listen`) | `netx.ListenTCP` |
| Clash API (`experimental/clashapi/server.go`, external controller) | `netx.ListenTCP` |
| v2ray API (`experimental/v2rayapi/server.go`, configured listen) | `netx.ListenTCP` |
| libbox pprof (`experimental/libbox/pprof.go`, `:port`) | `netx.ListenTCP` |
| boxdd dev TCP listener (`experimental/boxdd/server.go`, `listenAddress`) | `netx.ListenTCP` |

Verified `NO CHANGE` with source evidence: libbox command server (Unix socket, or TCP on
`127.0.0.1` when a port is set), libbox service port finder (`127.0.0.1`), proxy bridge
(`127.0.0.1`), OpenConnect auth callback (`127.0.0.1`), boxdd Unix worker, and the Tailscale binding
(UDP `ListenPacket`, not TCP).

Linux test `TestListenTCPDoesNotUseMPTCP` passes on the Ubuntu runner
(`--- PASS: TestListenTCPDoesNotUseMPTCP`); the existing listener test
`TestListenTCPMultiPathIsOptIn` also PASS. CI step now runs both packages:
`go test -v -count=1 ./common/listener/ ./common/netx/`.

### 6.3 Corrected phase-05 wording

The 3% figure in the historical RTT sweep is a **relative cross-implementation gap**, not an upper
bound on another pipeline's gain. `phase-05.md` and ledger X-09 now say the rejection is based on
insufficient evidence that the gain outweighs reorder/backpressure/close complexity, not on a 3%
ceiling. No pipeline was added.

### 6.4 Verification for this close-out

| Command / evidence | Result |
|---|---|
| `go test ./common/netx ./experimental/clashapi ./experimental/v2rayapi ./experimental/libbox ./experimental/boxdd` | `ok` (netx/v2rayapi no test files on Windows) |
| `go test -race` same packages with cgo | `ok` |
| `GOOS=linux GOARCH=amd64 go test -c ./common/netx` | compiled |
| Ubuntu CI `TestListenTCPDoesNotUseMPTCP` / `TestListenTCPMultiPathIsOptIn` | PASS |
| `go mod tidy -diff` (root + test) | no diff |
| CI on `d3f38495` | Verify, Android Core ARM64, Windows daemon, Release contract, Mobile contract: **success** |
