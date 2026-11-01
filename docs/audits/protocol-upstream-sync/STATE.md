# State — phase 01 (upstream sync)

| field | value |
|---|---|
| Stage | 01A construction + 01B debug |
| Date | 2026-11-02 |
| Branch | `testing` (default branch; repository has no `main`) |
| Start base | `7e6cc9cc` local → fast-forwarded to `0ab035904de5536273f6070a766b4fc657d7d77c` |
| Code commit | `fac646ca20eca25a0129c5580df3d0497b44963c` — Mieru SDK v3.39.0 + regression test |
| 01B commit | `bc349d83b2b8fdbd9ebb16b9bc3c5c7437effbdd` — empty-datagram and idle-underlay attack tests |
| Handoff docs | `git log -1 -- docs/audits/protocol-upstream-sync/` (this directory) |
| CI on `c231dd51` | Verify: success; Android Core ARM64: success; Mobile contract: success on rerun (attempt 1 failed an unrelated timing assertion) |
| Next | **02 — Naive client / Cronet** |

## Completed

- Fetched `origin/testing`, fast-forwarded the safe stale checkout (no local changes existed).
- Read the knowledge base in the required order (`AGENTS.md` → `KNOWLEDGE/README.md` →
  `BASELINE.md` → `INDEX.md` → `modules/README.md` → `modules/mieru.md`); no `KNOWLEDGE` refresh
  was performed (normal development rule).
- Confirmed the delta since the knowledge baseline `0067b03e` touches docs, CI and release tooling
  only — no Go source changed — so `modules/mieru.md` still describes the current adapter.
- Audited `enfein/mieru` `v3.38.1 → v3.39.0` at source level; bumped both Go modules; no adapter
  rewrite was justified.
- Added `TestMieruClientReadSurvivesLongUnidirectionalUpload`; red on `v3.38.1`, green on
  `v3.39.0`.
- Verified: full package suite, `-race` suite, module tidiness, read-only graph resolution,
  `go mod verify` in both modules, build of `protocol/mieru` + `include`, and
  `release-freeze.py verify` against `release/source-lock.json` (PASS — Mieru is an upstream module,
  not one of the seven locked fork modules, so the lock correctly did not move).
- Pushed and observed the push CI: `Verify` success (including gofmt, dependency-pin, nested-module,
  tagged/untagged build and race jobs) and `Android Core ARM64` success. `Mobile contract
  verification` failed once on an unrelated `transport/http` scheduler-timing assertion
  (`2.903s` vs `>3s`) and passed on a rerun of the same commit; no code was changed for it.
- Wrote `UPSTREAM_LEDGER.md`, `FORK_CANDIDATES.md`, `phase-01.md`.
- **01B hostile review**: re-verified pins, `go.sum`, `test/` module, replaces and the release lock;
  re-checked the ledger's AnyTLS/Snell/MASQUE/forkop/X-04 claims at source; audited the removed
  read deadline's ownership consequences (`Decrypt` allocates plaintext via `aead.Open(nil, …)`, so
  the bufpool release is safe); confirmed CI's tagged race step actually runs `./protocol/mieru/...`.
  No production-code defect found.
- Added two attack tests and a recorded limitation: `TestMieruEmptyUDPDatagramRoundTrip` (RFC 8085
  zero-length datagram + frame alignment) and
  `TestMieruClientObservesServerClosingAnIdleUnderlay` (underlay closed after 900 ms idle, blocked
  read fails before its deadline, new dial rebuilds). Both pass, including under `-race`
  (full suite 96.286 s; race 101.161 s).

## Not completed / deferred (with owner phase)

| Item | Why deferred | Owner |
|---|---|---|
| Mieru server `tcp_idle_timeout` config surface | new opt-in feature (option/schema/docs/tests), no defect | 06 |
| Mieru post-release UDP-associate fixes | not in `v3.39.0`; JiejieBox uses its own UoT codec | 06 |
| XHTTP `dialStreamUp` non-200 upload status | real candidate, needs a design + test (row X-04) | 04 |
| XHTTP xhttp-box tree diff | independent oracle not diffed yet | 04/05 |
| Naive client 154-series source/archive comparison | controlled native archives | 02 |
| forwardproxy authenticated-CONNECT idle semantic | server-side, local code not yet read | 03 |
| Hysteria2/TUIC/AnyTLS/VLESS/SS/Snell patch-id checks | candidate rows only | 06 |
| MASQUE upstream `dev` shared-layer diff | upstream not stable; premise corrected | 06 |

## Residual limitations

- **Half-close**: the v3.39.0 Mieru `Session`/client expose no `CloseWrite`, so a TCP write-half-close
  cannot be propagated through the adapter. This predates the upgrade and has no adapter-only fix;
  it is recorded rather than silently ignored.
- **Server `tcp_idle_timeout`**: not exposed as a JiejieBox option (row M-03); the inbound keeps its
  previous default behaviour.
- **No device/WAN evidence**: the results above are unit, loopback and CI build/race evidence, not
  interoperability with an external Mieru server or real network conditions.

## Blockers / environment facts

- `-race` needs `CGO_ENABLED=1`; mingw64 gcc is at `C:\msys64\mingw64\bin`. Without it, the check
  is `BLOCKED_BY_ENVIRONMENT`, not PASS.
- `api.github.com` / `raw.githubusercontent.com` / `github.com` are not reachable through
  `web_fetch` in this environment; GitHub evidence was gathered through the GitHub API (MCP).
- No WAN, production endpoint, VPS, subscription or real-traffic test was used.
- No release was published and no workflow was dispatched by hand. The commits are pushed to
  `origin/testing` as a normal code push; `verify.yml` (no path filter), `android-core-arm64.yml`
  (`go.mod` / `protocol/**`) and `mobile-contract.yml` ran by their declared triggers. `Verify` and
  `Android Core ARM64` passed; `Mobile contract` passed on rerun after the unrelated timing flake
  described above.

## Next-phase inputs

1. `UPSTREAM_LEDGER.md` §3 (rows N-01…N-06) and `FORK_CANDIDATES.md` §2.
2. `protocol/naive/*`, `transport/http` (read via `KNOWLEDGE/modules/naive-http-transport.md` first).
3. Pin facts: root `go.mod` four-module cronet replace, `cronet_native` in
   `release/source-lock.json`, and `release/dependencies.json` policy `controlled`.
4. Warning: row N-03 is phase 03 work, not phase 02.
