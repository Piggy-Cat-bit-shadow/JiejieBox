# State — upstream sync

| field | value |
|---|---|
| Stage | 01A/01B, 02A/02B, 03A/03B, 04A/04B, **05A** complete |
| Date | 2026-11-02 |
| Branch | `testing` (default branch; repository has no `main`) |
| Phase 01 | Mieru SDK `v3.39.0`: `fac646ca`, 01B coverage `bc349d83`; see `phase-01.md` |
| Phase 02 code | `f13f46e43dcd09dea17dcdfe3bcc08ac31aba949` — cronet pin bump; dependency commit `bd2b70516b92` |
| Phase 02 docs | `e78720d3` (02A map) + the 02B review commit |
| Phase 03 code | `e8ec5c67` — H2/H3 CONNECT pseudo-header rejection now answers 400 (03B) |
| Phase 04 code | `96bd665d` stream-up refusal; `b5c12db3` refusal precedence; `669fa275` status-matrix tests |
| Phase 05 | audit-only; bounded-async pipeline rejected on evidence (`phase-05.md`, ledger X-09) |
| CI on `f15d4473` | Verify, Android Core ARM64, Mobile contract, Release contract: success |
| Handoff docs | `git log -1 -- docs/audits/protocol-upstream-sync/` (this directory) |
| Next | **06 — remaining protocols / convergence** |

## Completed

### Phase 01 (Mieru)

- Synced the stale checkout to the then-current `origin/testing`; read the knowledge base in the
  required order; no `KNOWLEDGE` refresh.
- Bumped `enfein/mieru` `v3.38.1 → v3.39.0` in both modules; no adapter rewrite was justified.
- Pinned the removed 10 s client read deadline with `TestMieruClientReadSurvivesLongUnidirectionalUpload`
  (red on `v3.38.1`, green on `v3.39.0`); 01B added empty-UDP and idle-underlay tests. Full suite and
  `-race` passed; CI (tagged `-race` over `./protocol/mieru/...`) passed.
- Recorded the SDK half-close limitation (no `CloseWrite`); it is not fixable in the adapter alone.

### Phase 02 (Naive client / Cronet)

- Audited the upstream candidates: every cronet-go fix listed in the phase brief
  (`617d38f`, `172bb4e`, `f2dd023`, `fe74d92`, `2137940`, `795c32b`, `049f290`) is an ancestor of the
  fork; the sing-box items are a dependency bump, already-mapped window options, or server-side
  (phase 03).
- Found and ported the one missing upstream Go fix: `fa319c4` "Let the dialer resolve the server
  address". Fork commit `bd2b70516b92`; JiejieBox pins both cronet modules to it (`f13f46e4`).
- The Naive server name is now resolved by the fork dialer (sing-box DNS policy / dual-stack
  scheduling) instead of Chromium's single answer; the outbound comment that claimed the dialer
  receives an IP was corrected.
- Added fork unit tests: the fake-address DNS wrapper answers A for the server name without reaching
  the upstream resolver, and the socket callback maps the fake address back to the configured server.
- Verified: fork `go build/test -tags with_purego`; JiejieBox tagged `protocol/naive` tests and
  `-race`; module tidy/readonly graph in both modules; `release-freeze.py verify` PASS; untagged
  `include` build.
- Established the exact native gap: the `naiveproxy` submodule is `97ae698` (Chromium
  `154.0.8037.49`, 2026-09-25), which predates the official `-3`/`-4`/`-5` tags; the `-5` eMudhra
  blocklist entry is proven absent from `cert_verify_proc_blocklist.inc`.
- **02B hostile review**: confirmed the port lives in the Go binding path Chromium actually calls;
  the fork's `DialEarlyWithQUIC`/force-H3/`extra_headers`/`CloseAllConnections` and the H3 fallback
  are untouched; the helper adds no goroutine/buffer; A/AAAA for the server name never reaches the
  upstream resolver and the dialer resolves the name once; the 29 native libs and
  `cronet_native.shas` are unchanged; CI's cronet-provenance and dependency-pin gates passed. No
  defect found, so no 02B code change was made.

### Phase 03 (Naive server / shared HTTP)

- Audited every candidate against the current source: `forwardproxy e747854` (authenticated-CONNECT
  idle), `c096d6a` (H3 BBRv1), `sing-box 3c4a5a6ed` (H2 wrapper), `2f139af2d` (inbound close),
  `d9b435fb6` (padding bytes), `627912a78` (QUIC ALPN race). All are already covered — most with
  tests that pin the exact behaviour; the BBR capability exists as the opt-in
  `quic_congestion_control: bbr` while the default deliberately stays library Cubic.
- Verified no Caddy timer was transplanted: header waiting is `net/http.ReadHeaderTimeout` (request
  phase only); H1 CONNECT is hijacked and H2/H3 CONNECT is a handler-held stream, so an established
  tunnel is not subject to it.
- Recorded the protocol-inherent limitation: HTTP/2 and HTTP/3 expose no response-half-close; the
  reference full-closes on target EOF too. H1 preserves `CloseWrite` through `hijackedConn`.
- Verification: `-race -tags with_quic` package run passed (1.923 s); 20 s padding fuzz (477k execs)
  and 15 s authority fuzz (892k execs) passed; CI on `f15d4473` was fully green.
- **03A made no production change**: every candidate is either covered or a deliberate,
  documented divergence; forcing a patch would have changed reference behaviour without evidence.
- **03B found and fixed one real rejection-path defect**: the H2/H3 CONNECT branch rejecting
  `:scheme`/`:path` only logged, so net/http sent its implicit 200; it now writes 400 first
  (`e8ec5c67`). Tests cover H2/H3 400 and the 407 control for a well-formed unauthenticated
  CONNECT. Other 03B attacks (framing, flush/half-close, limits, authority/forwarded headers,
  build tags) map to existing tests; no second defect. Package `ok 0.373s`, tagged `-race`
  `ok 1.908s`; real H2/H3 interop remains `BLOCKED_BY_ENVIRONMENT`.

### Phase 04 (XHTTP correctness)

- Fixed the concrete gap: `dialStreamUp` ignored a non-200 upload response, so a refused stream-up
  parked the dial until its context (red test measured 3.00 s) and left writes without an error.
- The fix closes the refusal body without draining, breaks the upload pipe with the status, cancels
  the conn context so the pending download ends, and fails a not-yet-returned dial immediately.
  The refusal has a dedicated channel queued before the cancel; `awaitRaise` lets only that channel
  win when the cancellation closes `created` at the same time. The first revision shared the
  transport-error channel and could report `context canceled` instead of the status (caught by the
  existing download-failure test); `b5c12db3` fixes the precedence.
  A 200 body is still drained as before.
- Added `stream_up_refusal_test.go`: failed dial on upload 400 (body closed, XMUX usage 0, download
  aborted), post-raise write/read both see the refusal, and the pipe-level write error.
- Audited the other client-side candidates: `dffc7ada5` `GetBody` already present; `eef6e63bc`
  `WaitReadCloser` has no local equivalent; `77f98eba0` state is atomically guarded; the trailing
  slash rule is already conditional; `h2Params` is server-only and `OPTIONS` is not rejected by
  the client method path.
- Verified: package suite `ok 17.823s`, `-race` `ok 19.043s`, `go vet` clean; no local Xray
  server binary was used (fake RoundTripper contract tests only).
- **04B hostile review**: status matrix 400/404/413/500 with a body whose `Read` blocks forever
  (proves the refusal path closes without draining), post-raise refusal, and the cancellation tie.
  The tie exposed a real precedence defect in the first 04 revision (the cancellation could replace
  the server status / mask a download failure); `b5c12db3` separates the refusal channel. The matrix
  hardening is tests-only (`669fa275`). Final runs: package `ok 17.791s`, `-race` `ok 18.948s`.
  No other defect; real Xray interop remains `BLOCKED_BY_ENVIRONMENT`.

### Phase 05 (XHTTP data path / XMUX)

- Re-read the write path and measured the current invariant: `maxInflight=1` at every controlled
  RTT (0/30/50/100/200 ms), batches at `sc_max_each_post_bytes`; per-POST construction is
  `1789 ns/op`, `2203 B/op`, 15 allocs, against a 1 MiB batch.
- Evaluated the lx.25 bounded-async pipeline (16 in flight) against the current serial design:
  the historical RTT sweep is 1.18x/1.23x/1.07x/1.01x/0.97x fork/reference, so there is no clear
  win; pipelining would add reorder, close-drain and error-priority semantics to every POST.
- **Decision: `NO CHANGE`** for the pipeline; no independent defect found in `sc_*` handling,
  backpressure, pending-memory release, H1 serialisation or XMUX breaker/slot accounting.
- Verification: cadence probe `PASS` 9.247 s with `maxInflight=1`; package/`-race` numbers from the
  04B run are the current-code gate (no code changed in 05).

## Not completed / deferred (with owner phase)

| Item | Why deferred | Owner |
|---|---|---|
| Naive native `-3`/`-4`/`-5` (sleep/resume, QUIC window/cleanup, eMudhra block) | requires rebuilding the controlled Chromium archives; no toolchain here | later native pass |
| Apple/Windows native runtime for the new resolution path | no device / Apple build on this host | CI/device pass |
| Mieru server `tcp_idle_timeout` config surface | new opt-in feature (option/schema/docs/tests) | 06 |
| Mieru post-release UDP-associate fixes | not in `v3.39.0`; own UoT codec | 06 |
| XHTTP xhttp-box tree diff | independent oracle; not needed for the serial-vs-pipeline decision | 06 (optional) |
| Hysteria2/TUIC/AnyTLS/VLESS/SS/Snell patch-id checks | candidate rows only | 06 |
| MASQUE upstream `dev` shared-layer diff | upstream not stable; premise corrected | 06 |

## Residual limitations

- **Native gap is real.** The current archive is Chromium `154.0.8037.49` before the official
  `-3`/`-4`/`-5` fixes. The `-5` certificate blocklist is proven absent; no Go-level certificate-name
  block was added to imitate it.
- **Apple runtime is unverified.** The resolution port is Go-only and the tagged Windows tests pass,
  but only an Apple build/run can prove the engine path there.
- **HTTP/2 and HTTP/3 response half-close is not available** in `net/http`; the tunnel is fully
  closed on target EOF, which matches the reference (ledger S-07).
- **No device/WAN evidence**: unit, loopback and build/race evidence only, by contract.

## Blockers / environment facts

- `-race` needs `CGO_ENABLED=1`; mingw64 gcc is at `C:\msys64\mingw64\bin`.
- `web_fetch` cannot reach GitHub hosts from this environment; GitHub facts came through the GitHub
  API (MCP). The cronet fork and the naiveproxy submodule were inspected by clone.
- No WAN, production endpoint, VPS, subscription or real-traffic test was used.
- No release was published and no workflow was dispatched by hand. Pushes run the declared
  workflows; results are recorded from the runs themselves.

## Next-phase inputs

1. `UPSTREAM_LEDGER.md` §4–6 (Hysteria2, TUIC, AnyTLS, VLESS, SS, Snell, MASQUE) and
   `phase-05.md` §5.
2. `protocol/hysteria2/`, `protocol/tuic/`, `protocol/anytls/`, `protocol/vless/`,
   `protocol/shadowsocks/`, `protocol/snell/`, `protocol/masque/`, `transport/masque/`; route
   through `KNOWLEDGE/INDEX.md` and the corresponding modules before reading source.
3. Do not re-open X-04 (stream-up refusal), X-09 (packet-up pipeline), the Naive server S-rows or
   the client resolution pin (`bd2b705`) without new upstream or measurement evidence.
4. The native Naive `-3`/`-4`/`-5` gap and the Apple runtime verification remain open; they are not
   protocol-sync work.
