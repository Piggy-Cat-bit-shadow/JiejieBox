# Anywhere construction: fixed issues and regression checklist

Companion to [`anywhere-debug-audit.md`](anywhere-debug-audit.md). Every row is a command that was
actually run or a source check that was actually made; "not run" is not written as PASS.

## Fixed in this round

| ID | Severity | Root cause | Files | Fix commit | Regression test | Red proof | Stress/race | CI/device |
|---|---|---|---|---|---|---|---|---|
| BUG-02-1 | P2 | `srs.Read` stops at the last rule; the streaming loader published without reading to EOF, accepting a body truncated after the rules (the read-all path returned `unexpected EOF`) | `route/rule/rule_set_remote.go` | `1f1775e37d257f6c66cb849478fa57b5c0592eae` | `TestRemoteRuleSetStreamDoesNotPublishATruncatedBody` | Test fails with the drain removed (`fetch` returns nil); passes on pre-round-1 code and on the fix; asserts old generation stays and new one does not | No `-race` (cgo blocked); no shared state added | CI on `48a58f4`: `verify`, `Android ARM64 core`, `Apple tags and the low-memory data paths` all **success**; no device run |
| TEST-02-1 | — | The streaming branch's parse-failure path had no deterministic test | `route/rule/rule_set_remote_stream_test.go` | `742da5e8df3f3429002e3924ebfcd6933777c47a` | `TestRemoteRuleSetStreamRejectsUnparseableBodies` (empty + garbage) | Both cases return an error and publish nothing | same | same |
| BUG-02-2 | P3 (pre-existing) | `srs.Read` stopped after the declared rules, leaving the zlib Adler-32 unverified; a trailer-truncated or corrupt file parsed | `common/srs/binary.go` | `6b50757c032bb83b1cde192398a4ee0bf105a3c7` | `TestReadRejectsTruncatedStream`, `TestReadRejectsCorruptChecksum` | Both tests fail on the old parser (cut 1/2/4 and flipped last byte parsed), pass after the EOF drain; complete payload still parses | `go test ./common/srs`; `route/rule` and `route -short` pass | CI on the parser-fix head: see below |

## Checked with no defect

| ID | Check | Method | Result |
|---|---|---|---|
| NEG-02-1 | Empty body | test | `srs.Read` magic/EOF error; no publish |
| NEG-02-2 | Non-SRS body | test | `srs.Read` rejects; no publish |
| NEG-02-3 | Truncation **inside** the rules | source: `srs.Read` returns the read error before `publishRules`; the empty/garbage tests cover the same failure branch | parse error; no publish (source-reasoned, not a dedicated fixture) |
| NEG-02-4 | Cache-file path unchanged | source diff + existing `route/rule` tests | no code path change |
| NEG-02-5 | Source/JSON path unchanged | source diff + existing `route/rule` tests | no code path change |
| NEG-02-6 | `initial_path` / cache-restore path unchanged | source read (`StartContext` still calls `loadBytes`) | no change |
| NEG-02-7 | Partial publication on build/validate error | source read (`publishRules` local slice, assign under one lock) + existing validation test | old generation stays |
| NEG-02-8 | Callback ordering / duplicate notifications | source read (callbacks copied under lock, called after unlock) | unchanged |
| NEG-02-9 | Concurrent fetch | source read (initial fetch before updater; updater serial) | not reachable |
| NEG-02-10 | Connection reuse | source read (`CloseIdleConnections` after non-initial refresh) | drain is an EOF check, not a keep-alive optimisation; no claim |
| NEG-02-11 | Allocation bound | `TestRemoteRuleSetStreamsBinaryWithoutCache` | ~125 KB for a 4 MiB tail, bound 2 MiB |
| NEG-02-12 | iOS buffer geometry | `go test -tags with_low_memory ./route/rule` | PASS |
| NEG-02-13 | Cancellation while `srs.Read` is blocked mid-body | `TestRemoteRuleSetStreamAbortsWhenTheRequestIsCanceled` (server sends half, blocks, caller cancels) | error, no publish, old generation stays |

## Environment-blocked / not run

| ID | Item | Reason | What it would have proved |
|---|---|---|---|
| ENV-02-1 | `go test -race ./route/rule` locally | CGO disabled, no mingw-w64 gcc | data-race freedom of the fetch/publish path. **Resolved remotely**: `verify` runs `go test -race ... ./route/...` and passed on `48a58f4` |
| ENV-02-2 | `scripts/ci/test-low-memory.sh` | no bash on this host | the full Apple tag set + package list. **Resolved remotely**: the Apple CI job on `48a58f4` passed |
| ENV-02-3 | canonical Apple tag set (`appletags -platform ios`) | Windows linker rejects `tfo-go` `net.(*netFD).init` with `tfogo_checklinkname0`/`badlinkname` | the exact iOS tag build. **Resolved remotely**: the Apple CI job on `48a58f4` passed |
| ENV-02-4 | iOS/Android device `phys_footprint` | no device/SDK | actual extension memory; this round claims allocation accounting only |
| ENV-02-5 | HTTP/2 / HTTP/3 server variants | not separately simulated | transport-independent body drain; the `io.ReadCloser` contract is shared |

## Command log (final tree)

| Command | Result |
|---|---|
| `go test ./route/rule -run TestRemoteRuleSetStream -v` | PASS (4 tests, 2 subcases) |
| `go test ./route/rule -count=1` | PASS |
| `go test ./common/srs -count=1` | PASS (checksum/truncation tests included) |
| `go test ./route -count=1 -short` | PASS, 61.170 s |
| `go test -tags with_low_memory ./route/rule -count=1` | PASS |
| `go test ./route -count=1 -timeout 12m` | PASS, 421.389 s |
| `go vet ./route/rule` | clean |
| `gofmt -l route/rule` | clean |
| `go build ./route/...` / `go build -tags with_low_memory ./route/...` | clean |
| red check: drain removed | `TestRemoteRuleSetStreamDoesNotPublishATruncatedBody` FAIL (`nil` error) |
| parity check: pre-round-1 file from `d846fb6^` | same test PASS (`unexpected EOF`) |
| GitHub CI on `48a58f4` | `verify` success; `Apple tags and the low-memory data paths` success; `Android ARM64 core` success |
