# Phase 06A — other protocols and the final convergence

| | |
|---|---|
| Phase | 06A (construction / convergence) |
| Date | 2026-11-02 |
| Branch | `testing` (default; no `main`) |
| Start base | `a09c6f28` (clean, synced) |
| Code commit | `7f322a90` — `sing-vmess` bump for the Vision close fix |
| Next | `06B` — hostile review of this phase, then close the cycle |

## 1. Accepted delta: VLESS Vision close after a direct write

`SagerNet/sing-vmess` HEAD had one commit past JiejieBox's pin:

* `a0c58542bd9d99b00ea0c3cfacce6f03cf8a4017` — "Fix vision close after direct write" (2026-10-20):
  `VisionConn.directWrite` becomes an `atomic.Bool` (the field is written by `Write` and read by
  `Close`), and `Close` now closes `netConn` when a direct write happened instead of closing the
  buffered `Conn`. The old code could close the wrong layer after a direct write.

The bump is `v0.2.9-0.20260929152519-9b95ab8c9478 -> v0.2.9-0.20261019223527-a0c58542bd9d` in both
`go.mod` and `test/go.mod`; `go.sum` follows; `go mod tidy` is clean in both modules.

### The Vision ABI gate was honoured, not bypassed

`TestVisionTripwireSingVMessVersion` failed with the expected instruction. Before updating the
constant, the bridge was re-audited against the new module:

* `vless.tlsRegistry` is still declared with the same slice/callback shape (`vision_tripwire_test.go`
  source-shape and registry tests pass);
* `CommonConn.input`/`rawInput` names, types and order are unchanged
  (`TestVisionTripwireCommonConnABI` and the end-to-end alias test pass);
* `TestVisionTripwireVisionConnAliasesCommonConnFields` still shows `VisionConn.input`/`rawInput`
  aliasing the local fields.

Only then was `expectedSingVMessVersion` updated. The dependency's own diff touches `directWrite` and
adds `Close`; it does not touch the reflected ABI.

## 2. Other protocol candidates: resolution

| ID | Protocol | Verdict | Evidence |
|---|---|---|---|
| H-01 | Hysteria2 reference tree | `NO CHANGE` | the changes are in `HyNetworks/hysteria`, not in the `sing-quic` module JiejieBox compiles |
| H-02 | sing-box Hysteria2 fixes (`d41e1745`, `a691a440`) | `ALREADY_COVERED` | both commits are ancestors of this checkout's `HEAD` |
| T-01 | sing-quic TUIC fixes (`75c3ac4f`, `e702a735`) | `ALREADY_COVERED` | fork base `7e0a5869` postdates both; pin is `sing-quic` `258509488380` |
| T-02 | `tuic-protocol/tuic` | `NO CHANGE` | dormant; spec unchanged |
| A-01 | `sing-anytls` | `ALREADY_COVERED` | pin `580984e4` equals remote HEAD |
| A-02 | upstream AnyTLS migration (`0159d28b`) | `NO CHANGE` | JiejieBox imports the same `sing-anytls` library; the local adapter adds only timeouts/logging; no behavioural delta found |
| V-01 | Xray MPTCP default-off | `NO CHANGE` (risk recorded) | Go 1.25.5 has `defaultMPTCPEnabledListen = true` on Linux and `defaultMPTCPEnabledDial = false`; JiejieBox does not call `SetMultipathTCP(false)`, so Linux listeners may use MPTCP. Xray's fix is a socket-layer default, not a VLESS wire change; changing every listener is out of this phase |
| V-02 | `sing-vmess` | `MERGED` | `7f322a90`, §1 |
| S-01 | shadowsocks-rust SIP003 fd/idle fix | `NO CHANGE` | Rust-only; the Go path uses `bufio.Copy` and closes on either side's termination; the local copy geometry/MTU tests are the audit surface |
| S-02 | sing-box idle/input validation (`07130365`, `52f9bad0`) | `ALREADY_COVERED` | both commits are ancestors of `HEAD` |
| SN-01 | `sing-snell` HEAD | `ALREADY_COVERED` | pin `bc5a12ac` equals remote HEAD |
| SN-02 | sing-box Snell fixes (`0164ce7b`, `33e63afb`) | `ALREADY_COVERED` | both commits are ancestors of `HEAD`; the local wrapper already disables domain unmapping for the Snell client |
| Q-01 | sing-box MASQUE `dev` | `DEFERRED` | upstream MASQUE is dev-only (no stable tag); JiejieBox's data plane is independent and larger; no endpoint rewrite |
| Q-02 | lx.28 MASQUE close wedge | `NO CHANGE` | the local close-boundary tests exist; the specific no-op-`SetReadDeadline` detour wedge was not reproduced, so no speculative sync change |

## 3. Convergence checks

* `go.mod` / `test/go.mod`: only the `sing-vmess` pin changed; `go mod tidy` left no diff.
* `release/source-lock.json`: `release-freeze.py verify --lock release/source-lock.json` → **PASS**
  (`0.1.36`, 7 modules; clients/desktop dashboard SKIPPED because that checkout is absent).
* No gitlink/submodule change; no `replace` change; no new native archive; `cronet_native.shas`
  untouched.
* No Release, tag, deployment or workflow dispatch was created.

## 4. Verification

| Command | Result |
|---|---|
| `go test -tags "<DEFAULT_BUILD_TAGS_OTHERS>" ./protocol/vless/... ./protocol/hysteria2/... ./protocol/tuic/... ./protocol/anytls/... ./protocol/shadowsocks/... ./protocol/snell/... ./protocol/trojan/...` | all `ok` (snell has no test files) |
| `go test ./protocol/vless/...` | `ok` (including the Vision tripwires) |
| `go mod tidy` (root and test) | clean |
| `release-freeze.py verify --lock release/source-lock.json` | PASS |
| CI on the phase-06 head | recorded in `FINAL_INTEGRATION_REPORT.md` |

## 5. Residual work / blockers

* **MPTCP listener default** is a deployment risk, not a wire bug: Go enables MPTCP on Linux
  listeners by default; Xray's v26.10.31 release disables it. A deliberate `SetMultipathTCP(false)`
  belongs to the listener owner, not this protocol audit.
* **MASQUE** alignment stays deferred until upstream ships a stable tag; the local data plane is not
  replaced.
* **Native Naive `-3/-4/-5`** remains blocked on rebuilding the controlled Chromium archives.
* Real interop for XHTTP/H2/H3/Naive remains `BLOCKED_BY_ENVIRONMENT` (no local reference servers).
* Mieru server `tcp_idle_timeout` and post-release UDP-associate work remain 06-era deferrals; no
  new wire evidence this run.

## 6. Handoff

`FINAL_INTEGRATION_REPORT.md` is the cycle summary; 06B closes the cycle below.

## 7. 06B debug / cycle close (2026-11-02)

Scope was the 06 delta only: the `sing-vmess` bump, `expectedSingVMessVersion`, and the module pins.
No other protocol was re-audited.

| Check | Result |
|---|---|
| Vision ABI tripwires (`TestVisionTripwire*`, alias/e2e, registry shape, CommonConn ABI) | `ok` against `a0c58542` |
| `go test ./protocol/vless/...` | `ok` |
| `go mod verify` in root and `test` | `all modules verified` |
| `release-freeze.py verify --lock release/source-lock.json` | PASS (`0.1.36`) |
| Cross-phase `-race` on Naive + XHTTP (`with_quic`) | Naive `ok 1.951s`, XHTTP `ok 19.060s` |
| Git state | branch `testing`, HEAD == remote, clean tree, no tags at HEAD, no gitlink change |
| CI on `6d4164f8` | Verify, Android Core ARM64, Mobile contract: **success** |

No new defect was found in the 06 change; no further code change was needed for 06B. The cycle is
closed in `STATE.md` (`status=completed`). Residuals (native Naive `-3/-4/-5`, real interop, Apple
runtime, direct `net.Listen` endpoints, MASQUE dev-only, Mieru post-release work) remain as recorded
and are not defects introduced by this cycle.

## 8. Post-close security hardening: MPTCP listeners

The 06A table left `V-01` as `NO CHANGE` with a recorded risk because it was treated as a
listener-owner policy. The upstream evidence is stronger than that: Xray v26.10.31 is an **urgent
security release** whose body says a vulnerable Linux kernel's MPTCP listener is remotely
exploitable for root, that Go 1.24+ enables MPTCP on listeners by default, and that Android
Socks/HTTP inbounds with LAN access are also exposed.

Source check: `common/listener/listener_tcp.go` only called `SetMultipathTCP(true)` when
`tcp_multi_path` was set; with the option unset it left Go's Linux default (MPTCP on). That is the
vulnerable shape on every inbound using `common/listener`, including the client-local Socks/HTTP
inbounds.

Fix `b218345e`:

* `tcp_multi_path: true` still calls `SetMultipathTCP(true)` (explicit opt-in);
* otherwise the listener now calls `SetMultipathTCP(false)`, overriding Go's default and
  `GODEBUG=multipathtcp=...` for that listener;
* the Linux test checks the default listener is plain TCP via
  `getsockopt(SOL_MPTCP, MPTCP_INFO)` and that the explicit option still creates MPTCP, skipping if
  the kernel lacks MPTCP support.

Residual: not every socket in the tree goes through `ListenTCPWithOptions`. The debug/pprof HTTP
server, Clash API, libbox command/pprof servers, v2ray API, proxy bridge and boxdd still call
`net.Listen` directly; most are loopback-only, but they should be audited by their owners if exposed.
The central inbound path is covered.

| Command | Result |
|---|---|
| `go build ./common/listener/` | exit 0 |
| `go test ./common/listener/` (Windows) | `ok 2.070s` |
| `GOOS=linux GOARCH=amd64 go test -c ./common/listener/` | compiled |
| `GOOS=linux go vet ./common/listener/` | exit 0 |
| Linux runtime test | PASS on the Ubuntu runner (`TestListenTCPMultiPathIsOptIn --- PASS`; kernel MPTCP available, assertion not skipped) |
