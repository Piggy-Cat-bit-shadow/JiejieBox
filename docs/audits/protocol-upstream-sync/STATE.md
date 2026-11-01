# State — upstream sync

| field | value |
|---|---|
| Stage | 01A + 01B complete; **02A + 02B complete** |
| Date | 2026-11-02 |
| Branch | `testing` (default branch; repository has no `main`) |
| Phase 01 | Mieru SDK `v3.39.0`: `fac646ca`, 01B coverage `bc349d83`; see `phase-01.md` |
| Phase 02 code | `f13f46e43dcd09dea17dcdfe3bcc08ac31aba949` — cronet pin bump; dependency commit `bd2b70516b92` |
| Phase 02 docs | `e78720d3` (02A map) + the 02B review commit |
| CI on `e78720d3` | Verify (incl. cronet provenance, dependency pins, tagged `-race`): success; Android Core ARM64: success; Mobile contract: success; Release contract: success |
| Handoff docs | `git log -1 -- docs/audits/protocol-upstream-sync/` (this directory) |
| Next | **03 — Naive server / shared HTTP** |

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

## Not completed / deferred (with owner phase)

| Item | Why deferred | Owner |
|---|---|---|
| Naive native `-3`/`-4`/`-5` (sleep/resume, QUIC window/cleanup, eMudhra block) | requires rebuilding the controlled Chromium archives; no toolchain here | later native pass |
| Apple/Windows native runtime for the new resolution path | no device / Apple build on this host | CI/device pass |
| Mieru server `tcp_idle_timeout` config surface | new opt-in feature (option/schema/docs/tests) | 06 |
| Mieru post-release UDP-associate fixes | not in `v3.39.0`; own UoT codec | 06 |
| XHTTP `dialStreamUp` non-200 upload status | real candidate, needs design + test (X-04) | 04 |
| XHTTP xhttp-box tree diff | independent oracle not diffed yet | 04/05 |
| forwardproxy authenticated-CONNECT idle semantic | server-side, local code not yet read (N-03) | 03 |
| Naive server QUIC BBR / HTTP/2 inbound commits | server-side (N-04, `a349a0d76`, `3c4a5a6ed`) | 03 |
| Hysteria2/TUIC/AnyTLS/VLESS/SS/Snell patch-id checks | candidate rows only | 06 |
| MASQUE upstream `dev` shared-layer diff | upstream not stable; premise corrected | 06 |

## Residual limitations

- **Native gap is real.** The current archive is Chromium `154.0.8037.49` before the official
  `-3`/`-4`/`-5` fixes. The `-5` certificate blocklist is proven absent; no Go-level certificate-name
  block was added to imitate it.
- **Apple runtime is unverified.** The resolution port is Go-only and the tagged Windows tests pass,
  but only an Apple build/run can prove the engine path there.
- **Naive `Session` half-close** (from phase 01's Mieru analogue; separate protocol) is not part of
  this state.
- **No device/WAN evidence**: unit, loopback and build/race evidence only, by contract.

## Blockers / environment facts

- `-race` needs `CGO_ENABLED=1`; mingw64 gcc is at `C:\msys64\mingw64\bin`.
- `web_fetch` cannot reach GitHub hosts from this environment; GitHub facts came through the GitHub
  API (MCP). The cronet fork and the naiveproxy submodule were inspected by clone.
- No WAN, production endpoint, VPS, subscription or real-traffic test was used.
- No release was published and no workflow was dispatched by hand. Pushes run the declared
  workflows; results are recorded from the runs themselves.

## Next-phase inputs

1. `UPSTREAM_LEDGER.md` §3 rows N-01…N-07; N-03/N-04 and the sing-box inbound commits are phase 03.
2. `protocol/naive/inbound.go`, `inbound_conn.go`, `protocol/naive/quic/`, `transport/http`; read
   `KNOWLEDGE/modules/naive-http-transport.md` §2 first.
3. `klzgrad/forwardproxy` `e747854` (authenticated CONNECT idle) and `c096d6a` (server QUIC BBRv1).
4. Do not re-open the client resolution port (`bd2b705`) without an Apple runtime failure.
