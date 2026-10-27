# Project-wide bug audit — 2026-10-27

**Status: audit completed on `testing`; fixes committed; no version bump, no tag, no release.**

This is the whole-project pass the previous close-out should have been. It is deliberately not a
macOS note with a project heading: it starts from the knowledge base's module/invariant map, measures
the delta between that baseline and `HEAD`, runs the project's own gates plus static analysis, and
records both what was fixed and what remains a recorded limit.

## 1. Method and the baseline caveat

| Step | What was used |
|---|---|
| Route | `KNOWLEDGE/INDEX.md` → `modules/README.md` → the module(s) each finding touches |
| Rules | `KNOWLEDGE/INVARIANTS.md` A1–A20, `ACCEPTANCE.md` known-failing/standing limits |
| Delta | `git log/diff v0.1.9..HEAD` (753 commits, 241 non-test `.go` files) and `v0.1.24..HEAD` (108 files) |
| Gates | `go build`, `go test -run '^$' ./...`, the full `route`/`libbox`/`dns`/`power` suites, `go vet`, `golangci-lint` (repo config + a strict errcheck/errorlint/bodyclose/nilerr config), `cmd/internal/...`, `test/` module vet, workflow structure check |
| CI | GitHub `Verify`, macOS arm64 unit tests/build, Apple client libbox/macOS build |

**The knowledge base describes `v0.1.9`; `HEAD` is `v0.1.28`.** The modules are therefore a map, not a
current measurement. Several claims were re-measured and corrected in place (§4); a full
`REFRESH.md` re-baseline is still a separate operation and was not performed.

## 2. Findings and disposition

| # | Area | Finding | Evidence | Disposition |
|---|---|---|---|---|
| 1 | macOS lifecycle | A lid-close resume had no network reset: `Pause`/`Wake` ignored Darwin, so the outbound/DNS pools kept sockets the sleep black-holed | `command_server.go`, `route/network.go`; the v0.1.28 path-monitor work only covered handovers | **Fixed** (`0410c9d`, refined after review): macOS device axis, episode-keyed `SystemResumed` (a second sleep is not swallowed), **duration-aware** reset (medium below 30 s; dead-path at/above, so a long-sleep active black hole is closed), `common/suspendwatch` for the headless client; macOS CI green |
| 2 | Generated artifact | `docs/schema.json` advertised Naive `User.Username`/`Password` while the structs carry `json:"username"`/`json:"password"` | `make schema` regenerated: exactly 2 lines differ; consumers are outside the tree | **Fixed**: artifact regenerated; `cmd/sing-box/cmd_schema_artifact_test.go`; `verify.yml` step |
| 3 | Apple ABI / release chain | `VERIFIED_APPLE_SHA` stayed at `6e559a4` while the gitlink moved to `3aedd741`; the ABI gate that would have failed layer 5 ran from nothing | `check-libbox-abi.sh --skip-client` on the tree: `FAIL: apple gitlink is 3aedd741…` | **Fixed**: constant moved to `3aedd741` (range changes quota caption/title/path monitor, no ABI call site); `--pin-only` wired into `verify.yml`; full gate wired into `client-apple.yml` |
| 4 | Test integrity | Three URLTest assertions checked `uint16 >= 0`; they cannot fail and were the only assertion in their block | `staticcheck SA4003` at `delay_contract_test.go:102,181`, `urltest_test.go:280` | **Fixed**: tautologies removed; request counts remain the observable contract |
| 5 | Recorded splice defect | `docs/internal/fork-splice-silent-stall.md` and `buffer-ownership.md` said the `Pipe2` setup failure was **not fixed**; the sing pin already carries `cebf939` (`handed=false`, `splicePipe2` seam, regression) | pinned sing source in the module cache; `sing-fork` history | **Record corrected**; no core code change needed |
| 6 | DNS / Windows | `source_windows.go` used an unchecked `err.(syscall.Errno)`; a wrapped error would **panic** | `errorlint` on the file | **Fixed**: `errors.As` with a non-matching branch |
| 7 | DNS / cache | `dns/client.go` compared `err != ErrNotCached` while the router already used `errors.Is`; a wrapped miss would be treated as a failure | `errorlint` | **Fixed**: `!errors.Is(err, ErrNotCached)` |
| 8 | Route / platform | Three `err != os.ErrInvalid` / `err != windows.ERROR_BUFFER_OVERFLOW` comparisons would miss wrapped errors | `errorlint` | **Fixed**: `errors.Is` in `network.go`, `router.go`, `network_environment_windows.go` |
| 9 | Dead/stale ownership code | OpenVPN `newResolvers` was appended and never read (the scope already owns the resolvers); OpenConnect's `DNSTransport.endpoint` was never set; DNS's `reverseMappingGenerationCurrent` was dead; oomkiller's `runtimeMemoryLimitForPolicy`, appletags' `canonicalAppleTagString`, and build_libbox's `resolvedTags` were unused | `unused` / `SA4010` | **Removed**; the comments that claimed they were used are corrected |
| 10 | Monitor teardown | `platformDefaultInterfaceMonitor` checked `closed` only on entry; a delivery in `RefreshInterfaces` could write after `Close` | code review; new test | **Fixed** (`0410c9d`): re-check under the write lock |
| 11 | CI reliability | The v0.1.28 macOS gate failed `TestHalfCloseVerdictAndTeardownRaceSettleOnce`; `released` and its verdict were two atomic adds | CI log; `9c1b7fc` | **Fixed**: one lock for the settle counters; concurrent regression test added |
| 12 | macOS device chain | The NetworkExtension pin had `NWPathMonitor` on a concurrent queue and chose `availableInterfaces.first` | `26e3c009` / Apple `3aedd74` | **Fixed in the client pin**; no new client source required this round |

## 3. Areas checked and found clean (with the check that says so)

| Area | Evidence |
|---|---|
| Lifecycle, `Scope`, start/close ordering | full `route`/root suites; `go test -short ./route` 80.799 s; the 440.690 s full run before the final edits |
| Path selection, `physicalpath`, groups, fast_path | `route`/`common/physicalpath` suites; `go test -run '^$' ./...`; no new findings |
| DNS cache generations, FakeIP, adblock, demand | `dns` suite 8.0 s; `errors.Is` fixes above; no generation regression |
| Sniff ownership / fragmented QUIC | `common/sniff` suite; no new findings |
| Data path, traffic class/scheduler, dial governor | `common/trafficsched` in the full run; `route` suite; inert-when-unset contract unchanged |
| Buffers / splice | the recorded splice defect is fixed in the pin; the remaining `handed` semantics are documented |
| MTU / geometry | `common/bufgeom` in the full run; no new findings |
| Windows daemon / service | `go vet`; `go build`; no source changed this round; the trust boundary still has no e2e test (recorded limit) |
| Build/CI/release | `go build -tags OTHERS ./...`; all test binaries compile; `cmd/internal/...`; `test` module vet; `check-release-workflows.py` 133/133 |
| Static analysis | repo lint: 154 findings, triaged; strict lint: 66 remaining, all reviewed (see §5) |

## 4. Knowledge/record corrections made

* `docs/internal/fork-splice-silent-stall.md` — status changed from "cannot be fixed" to fixed in
  sing `cebf939`, with the pinned version.
* `KNOWLEDGE/modules/buffer-ownership.md` — the "not fixed" row now records the fix and the pin.
* `KNOWLEDGE/modules/README.md` — the schema row now records the real drift (`User`, not `fast_path`)
  and the test/gate that close it.
* `KNOWLEDGE/INVARIANTS.md` A11 — the pin check is wired; the remaining gap is the release-acceptance
  engine, not an absent verifier.
* `KNOWLEDGE/modules/ci-release.md` — the "still unwired" list no longer includes the ABI gate;
  `--pin-only` is wired, the full gate is in `client-apple.yml`; the measured pin is `3aedd741`.
* `KNOWLEDGE/modules/libbox-platform-contract.md` — same correction in §6, the evidence row and
  caution 16.
* `KNOWLEDGE/INVARIANTS.md` A20 — release acceptance is judged by `release-artifacts.sh`, the release
  path's actual authority; `verify-release-acceptance.sh` is a separate handoff checker with a narrow
  default set. The previous wording called the release path unprotected because that separate tool is
  not invoked, which conflated two different gates.

## 5. Remaining findings, deliberately not fixed

| Item | Why it remains |
|---|---|
| Active TCP no-forward-progress stall | The mechanism is proven and reproduced, but the only proposed gate (blocked writer + grace) is not separable from healthy backpressure; `ACTIVE_TCP_STALL_INVESTIGATION.md` records the evidence and the refusal to guess a timeout |
| `verify-release-acceptance.sh` is not the release path authority (A20/A12 corrected) | The release path's authority is `release-artifacts.sh`: it includes `client-apple.yml` under `RELEASE_EXTRA_PLATFORMS=true` and fails on a completed non-success or cancelled run. The separate strict handoff checker's narrow default set is a tool-ownership gap between two checks, not an unprotected release path |
| `errcheck` close-return findings | 50, almost all cleanup paths (`defer conn.Close`, scope closes) where the error cannot affect the caller; changing them to logged errors would add noise, not correctness |
| `bodyclose` in libbox/http and xhttp | Both close through `GetContent` / `drainAndClose`; the analyzer cannot see the helper |
| `nilerr` in `reportPathSize` | Deliberately best-effort: an entry that cannot be stat'ed contributes zero |
| `errorlint` on gRPC `io.EOF`, the DNS verdict tree walk, and `context.Cause` | Direct values by contract or deliberate manual tree walk; `errors.Is/As` would change the semantics being tested |
| Dead helpers in `common/dialer` and `scripts/` | No correctness claim depends on them; removing them is not part of a bug fix |
| Physical Mac, iPhone, Android device behaviour | No device; the macOS chain is built and unit-tested but the lid-close measurement is still `UNVERIFIED` |
| Android/iOS client source | Separate repositories; the core side is verified by the Apple libbox/macOS build, the client call-site layer runs in `client-apple.yml` |

## 6. Evidence snapshot

Local (`DEFAULT_BUILD_TAGS_OTHERS`, Windows):

```text
go build -tags "$OTHERS" ./...                                  PASS
go test -run '^$' -tags "$OTHERS" ./...                         PASS (all test binaries compile)
go test -p 1 -short ./route ./experimental/libbox ./dns ...     PASS
    route 80.799s, libbox 1.212s, dns 8.089s, urltest 7.678s, ...
go test ./route (full)                                          PASS 440.690s
go vet ./...                                                    only the three deliberate unsafe sites
cmd/internal/... tests, test/ module vet                        PASS
check-release-workflows.py                                      133 passed, 0 failed
check-libbox-abi.sh --pin-only                                  PASS (android ec61030, apple 3aedd741)
```

CI for the macOS fix commit `0410c9d` (before this audit's follow-ups): Verify
[#40485312923](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40485312923) **success**;
macOS arm64 [#40485385846](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40485385846)
**success**; Apple client [#40485553778](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40485553778)
**success** (libbox + macOS DMG; iOS skipped by input).

CI for the audit fixes (`0149950`): Verify
[#40491621645](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40491621645) **success**
(11m23s), including the new "generated schema matches its source" and "client gitlinks match the
ABI contract pins" steps. Apple client
[#40491667334](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/40491667334) **success**:
the libbox job built the framework, passed the full "libbox ABI gate against the client call sites"
step, and the macOS DMG built; iOS skipped by input. The docs commit after the code commit was
pushed with `[skip ci]`, so it did not start a second source run.

No tag was created, `release/JIEJIE_VERSION` stays `0.1.28`, and no GitHub Release was touched. The
v0.1.28 release was published before this round, by the previous gate.
