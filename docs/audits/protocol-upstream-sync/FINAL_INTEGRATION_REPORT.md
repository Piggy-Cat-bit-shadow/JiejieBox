# FINAL_INTEGRATION_REPORT — JiejieBox upstream sync cycle

| | |
|---|---|
| Cycle | 01A/01B, 02A/02B, 03A/03B, 04A/04B, 05A, 06A/06B |
| Branch | `testing` (default; repository has no `main`) |
| Date | 2026-11-02 |
| Head at report time | `7211ad71` (cycle head, including the 05B review; `STATE status=completed`) |
| Status | **completed** — residuals listed in §4, no release/deploy |

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

## 2. Accepted / rejected decisions

Accepted:

* Mieru `v3.39.0` (the client read-deadline fix is real and test-pinned).
* cronet `fa319c4` (server name resolved by the sing-box dialer, not Chromium's synthetic address).
* Naive H2/H3 CONNECT rejection status (a real "looks accepted" bug).
* XHTTP stream-up refusal failure propagation (real stall; four statuses covered).
* `sing-vmess` Vision close fix (dependency bump, ABI audited).

Rejected / no change (with evidence):

* packet-up bounded-async pipeline (X-09): no measured net win for this deployment; adds
  reorder/close/error semantics to every POST.
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

### 3.1 Final 05B / 06B checks

* 05B: the packet-up/XMUX attack matrix maps to existing tests; targeted `-race` run
  (`TestPacketUp|TestSplitConn|TestXmux|TestBreaker`) `ok 11.158s`; no code defect.
* 06B: Vision ABI tripwires and `go test ./protocol/vless/...` pass against `sing-vmess a0c58542`.
* `go mod verify` passes in both modules; `release-freeze.py verify` PASS (`0.1.36`).
* Cross-phase `-race` on the two previous protocol surfaces still passes with the new dependency
  graph: Naive `ok 1.951s`, XHTTP `ok 19.060s` (`with_quic`).
* Git: branch `testing`, HEAD == remote, clean tree, no tag at HEAD, no gitlink change.
* CI on `6d4164f8`: Verify, Android Core ARM64 and Mobile contract all success.

## 4. Known residual risks

1. **MPTCP listener default (new finding)**: Go 1.25.5 enables MPTCP for Linux listeners by default
   (`defaultMPTCPEnabledListen = true`), while dialers default off. Xray v26.10.31 disabled MPTCP as
   an urgent security default. JiejieBox does not call `SetMultipathTCP(false)` on its listeners.
   This is not a VLESS wire bug and was not patched across every listener in this cycle; it is
   recorded for the listener owner as a deployment risk.
2. **Native Naive gap**: Chromium `154.0.8037.49` predates the official `-3/-4/-5` fixes; the eMudhra
   blocklist entry is proven absent. Rebuilding the controlled archives is required.
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
