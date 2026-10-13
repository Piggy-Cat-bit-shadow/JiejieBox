# CI_BUILD_PIPELINE_FINAL_CLOSURE

```text
CURRENT_SHA = e7847b3b5  (testing)
RELEASE_FREEZE = KEEP
FORMAL_RELEASE_CREATED = NO
TAG_MOVED = NO
RELEASE_ASSETS_PUBLISHED = NO
```

Final status is stated in §9, after the validation results.

---

## 1. FILES_CHANGED

This round, relative to `769ca1831` (the fast_path debug tip):

| path | purpose |
|---|---|
| `scripts/ci/build-info.sh` | Android `build_tags` from the builder's own resolution; `build_profile` + `mobile_tags` |
| `.github/workflows/android-core-arm64.yml` | verify the RECORD against the builder; both product decisions restated against it |
| `.github/workflows/client-desktop-windows.yml` | runtime gates removed; unpacked tree uploaded; header/gating-status/record corrected |
| `.github/workflows/client-desktop-windows-runtime-audit.yml` | **new.** owns gates 5 / 5b / 6 / 7 as ordinary steps |
| `.github/workflows/mobile-contract.yml` | **new.** owns the mobile/Apple source contract |
| `.github/workflows/server-linux-amd64.yml` | low-memory gate removed; three coverage guards converted |
| `.github/workflows/client-macos.yml` | stale reference to the gate's old home corrected |
| `.github/workflows/verify.yml` | runs the new coverage-guard self-test |
| `.github/workflows/release-contract-verify.yml` | `bash -n` covers the two new scripts |
| `scripts/ci/require-executed-coverage.sh` | **new.** discovered/executed/passed/skipped/failed accounting |
| `scripts/ci/require-executed-coverage.test.sh` | **new.** its fixtures |
| `scripts/ci/check-release-workflows.py` | four new contract groups (see §7) |
| `scripts/ci/test-release-artifacts.sh` | desktop fixture carries the third artifact name |

No production Go code was changed. No protocol, runtime or artifact behaviour was changed.

---

## 2. Android BUILD-INFO — before / after

**Before** (this is the SHIPPED v0.1.6 artifact, extracted from
`jiejie-sing-box-android-arm64-v0.1.6.tar.gz`):

```text
build_tags:            with_low_memory
...
This build embeds NO timestamp: its hash is determined by the source
commit and tags alone, so it is reproducible.
```

One tag, for an AAR compiled with 26 — while claiming its hash was determined by "the source commit
and tags alone".

**Cause.** `build-info.sh` resolved the android flavour through
`go run ./cmd/internal/appletags -mobile-only`, which is the Apple mobile-only OVERLAY, not the set
the AAR is built with.

**After.** The android branch reads the builder:

```sh
build_profile="android-main"
tags="$(go run ./cmd/internal/build_libbox -print-resolved-tags "$build_profile")"
mobile_tags="$(go run ./cmd/internal/appletags -mobile-only)"
```

```text
build_profile:         android-main
build_tags:            with_quic,with_wireguard,...,grpcnotrace,with_low_memory   (26 tags)
mobile_tags:           with_low_memory
```

**One source of truth, verified.** Both the record and the workflow's assertion call
`build_libbox -print-resolved-tags android-main`, which reaches `ResolveBuildTags("android-main")` —
the function the builder itself calls before handing the set to gomobile. Measured on this host:

```text
build_libbox -print-resolved-tags android-main   26 tags, with_low_memory present, with_gvisor absent
appletags -mobile-only                           with_low_memory
```

An empty resolved set is now a hard failure. `v0.1.6` is untouched; this takes effect from the next
build. The release gate pins only the record's FILENAME, so no contract changed.

---

## 3. Windows Desktop — before / after

**Before.** One workflow, one job, gates 1-4 + 5 + 5b + 6 + 7 + 8. The four runtime gates had been
given `continue-on-error: true` in the previous round so they could not block a release. That made a
failure invisible to the run's conclusion instead of giving it an honest status.

**After.** Two workflows:

```text
client-desktop-windows.yml                  gates 1-4, 8        every gate here means
                                                                "the artifact is wrong"
client-desktop-windows-runtime-audit.yml    gates 5, 5b, 6, 7   ordinary steps; a failure
                                                                turns THAT workflow red
```

```text
Build workflow produces installer:              YES
Build workflow contains runtime gates:          NO  (asserted)
Runtime Audit contains 5 / 5b / 6 / 7:          YES (asserted)
Runtime failures are real workflow failures:    YES - no suppression anywhere in that file
continue-on-error used to hide them:            NO  (asserted, code-only)
release depends only on the Build workflow:     YES (asserted - the gate never names the audit)
artifact lookup still works:                    YES (§7, shape selection exercised)
```

**Release relation.** `release-artifacts.sh` still requires the INSTALLER workflow's run to conclude
`success` and never consults the audit. So a build failure blocks a release and a red runtime audit
does not invalidate a successfully built installer — the behaviour the `continue-on-error` version
was imitating, now a property of WHICH WORKFLOW the release reads.

**One real cost, stated.** The audit needs the packaged tree (gates 5/5b/7 read the signed daemon and
drivers; gate 6 tears its installation down, so "install then audit" is unavailable). The build
workflow therefore also uploads `release/win-unpacked` — measured at **317993 KiB ≈ 310 MiB** in the
build log — as `jiejiebox-windows-unpacked-audit-input`. That name matches neither release pattern,
asserted by shape. The build record no longer concatenates the runtime records: it describes the
ARTIFACT, so its contents must not depend on whether a hosted VM could register a service.

---

## 4. Mobile low-memory ownership — before / after

```text
before:  server-linux-amd64.yml, step "low-memory data paths", BEFORE the Linux binary was built
after:   mobile-contract.yml    (dispatch + push to testing, path-filtered)
```

```text
Linux artifact no longer invokes test-low-memory.sh:  YES (asserted)
new owner really invokes it:                          YES (asserted)
trigger/path ownership sensible:                      YES - the 22 paths it reads
test still fails closed:                              YES - script unchanged
tag/profile fidelity documented:                       YES - see below
```

**Who owns it now:** `mobile-contract.yml`. **When:** dispatch, or a push to `testing` touching the
tag sources, `service/oomkiller`, `experimental/libbox`, the data-path packages it runs, or the
script. **Where:** `ubuntu-latest`. **Which shipped configuration:** the mobile TAG SET —
`with_low_memory`, where `buf.BufferSize` is 16 KiB instead of 32 KiB.

**What it does NOT prove, stated rather than glossed:** it does not compile or link anything for
Apple. It is the Go data-path suites under the mobile tag set, not under an Apple toolchain, and it
does not exercise Cronet, Xcode, the iOS SDK or any Darwin-only constraint. The workflow header says
so in the same words. A macOS runner would buy the tag fidelity it already has and only add the
Darwin-only constraints `client-macos.yml` already covers.

Nothing was traded: the script is byte-identical, still fails closed if the Apple build stops using
`with_low_memory`, and no coverage was removed.

---

## 5. Release self-test call count — before / after

Counted with `git grep`, not estimated:

```text
verify.yml:64                     push-triggered self-test
release-contract-verify.yml:84    push-triggered self-test  (the full suite, tee'd to a log)
release.yml:109                   dispatch-time self-test, before the real gate runs
```

```text
AUTOMATIC_RELEASE_GATE_SELFTEST_RUNS_BEFORE = 3
AUTOMATIC_RELEASE_GATE_SELFTEST_RUNS_AFTER  = 3   (UNCHANGED - see below)
```

**NOT ACTIONED, and the reason is evidence rather than budget.** The brief recommends making
`release-contract-verify.yml` the single owner and dropping the suite from `release.yml`. That is
not available without reversing an invariant the suite itself enforces:

```text
scripts/ci/test-release-artifacts.sh:1118-1123
# The gate test must run before the release acts, and on every push.
if grep -q 'test-release-artifacts.sh' "$wf" &&
   grep -q 'test-release-artifacts.sh' "$repo_root/.github/workflows/verify.yml"; then
  report "the gate test runs in release.yml and in verify.yml" 0 0
```

Removing it from `release.yml` fails that check; removing it from `verify.yml` fails it too. The
`verify.yml` copy is also separately required by `check-release-workflows.py:176`. So the three
executions are a deliberate, self-tested decision — the release's own checker is itself checked
immediately before it is trusted — not three accidental copies, and the brief's classification of
"test the checker → test the checker → test the checker again" does not match what these three are.

**Nothing was weakened.** `release.yml` continues to run the real gate at ten call sites
(`check-tag`, `release-version`, `publish-flag`, `plan`, `download`, `desktop`, `verify`, `flatten`,
`check-upload`, plus the release creation), so the release path is verified by execution and not only
by its self-test.

---

## 6. Reference PASS/SKIP semantics — before / after

**Before.** Three guards counted `grep -cE '^--- (PASS|FAIL|SKIP):'` and treated non-zero as "the
suite ran". That counts REPORTED tests, so an all-SKIP run satisfied it. The reference suite printed
`PASS: reference suite (N differential tests)` where every one of those N was a SKIP — the Caddy
differential tests skip without `JIEJIE_CADDY_REFERENCE_BINARY`, and no workflow sets it.

**After.** `scripts/ci/require-executed-coverage.sh` reports all five counts and requires
`passed > 0`:

```text
label: discovered=D executed=E passed=P failed=F skipped=S
  a FAIL            -> fail: the tests ran and something broke
  discovered == 0   -> fail: the -run filter is stale, nothing matched
  passed == 0       -> fail: NOT_EXECUTED - everything matched, nothing ran
```

The reference-suite message names the likely cause, so the operator is told which remedy applies:
provision the reference binary, or stop claiming differential coverage in that step.

The existing split is preserved: `check-reference-coverage.sh` still treats a SKIP as a reported
result for its ACCOUNTING check, and acceptance remains the workflow's assertion. `interop-xray.yml`
already required `PASS > 0` and is untouched — it is the shape the other two now follow.

**This makes the reference step RED where it was previously green.** That is the intended change. It
affects the `deep_checks` path of `server-linux-amd64.yml` only, which is dispatch-gated and off the
artifact fast path, so no product build turns red.

---

## 7. Validation

### 7.1 Run on this host

```text
python3 scripts/ci/check-release-workflows.py        59 passed, 0 failed
gofmt (the macOS gate directory list)                clean
go build ./...                                       PASS
go vet <touched packages>                            PASS
go test ./cmd/internal/...                           PASS
YAML parse, every changed workflow                   PASS
dash -n, every changed shell script                  PASS
require-executed-coverage.sh logic, 6 cases          PASS (POSIX harness, see below)
release-gate artifact shape selection, 3 names       PASS
```

The new coverage guard cannot run under this host's shell (its shebang is bash; MinGit ships only
`sh`/`dash`). Its logic avoids bash-only syntax, so it was exercised directly under a POSIX shell:

```text
all-SKIP log            -> rc 1, "NOT_EXECUTED - every one of the 2 matched test(s) skipped"
one pass among skips    -> rc 0, "PASS (1 passed, 1 skipped)"
failure beside a pass   -> rc 1, "1 test(s) FAILED"
empty log               -> rc 1, "matched NO tests"
results-free log        -> rc 1, "matched NO tests"
indented subtest result -> rc 1, not counted as top-level coverage
```

### 7.2 Compile counts

These are counts of FULL PRODUCT COMPILES per complete release (four products), read from the
commands that issue them, not estimated from wall clock.

```text
                                  before this work   after the previous round   after this round
Android libbox (gomobile bind)              2                    1                      1
Windows core (go build ./cmd/sing-box)      3                    1                      1
Apple libbox (c-archive slices)             3                    2                      2
-------------------------------------------------------------------------------------------
FULL_PRODUCT_COMPILES_BEFORE = 8
FULL_PRODUCT_COMPILES_AFTER  = 4
```

This round changed only what the ANDROID RECORD says, not how many times anything is compiled, so it
moves neither number. No product compile was removed or added here.

### 7.3 Test duplicates

Counted as executions of the same suite/package set per push to `testing`, verified with `git grep`:

```text
                                                    before   after
test-release-artifacts.sh (push executions)            2        2     (see §5)
gofmt over the same trees                              2        1
go test ./cmd/internal/... (no tags)                   2        1
fuzz FuzzCapsuleParser / MasqueIPPacket / ConnectUDP   2        2     (different budgets, deliberate)
./protocol|route|transport packages, server vs macos   2        2     (different profiles, deliberate)
-------------------------------------------------------------------
AUTOMATIC_TEST_DUPLICATES_BEFORE = 4
AUTOMATIC_TEST_DUPLICATES_AFTER  = 2
```

This round added no duplicate. The two remaining are not redundancy: the fuzz targets run at
different budgets per platform, and the shared packages are compiled under the server profile on
Linux and the Darwin profile on macOS, which is two different configurations rather than two copies
of one.

No wall-clock figures are given anywhere in this report, by instruction.

---

## 8. Remaining known issues

1. **`-race` has not observed the fast_path concurrency tests.** Blocked by this host's C toolchain
   (three attempts). CI's race steps do not include `./protocol/group/` and do not run the new
   `route` tests; the step that would is `client-macos.yml`'s deep check, dispatch-only behind
   `deep_checks`.
2. **The release self-test still runs three times.** See §5 — it is enforced by the suite itself, so
   it is a decision to revisit deliberately rather than a cleanup.
3. **The runtime audit cannot be verified here.** It is a Windows workflow; neither it nor the
   installer build can run on this host. Its YAML parses and its contracts are asserted, but its
   first real execution is on a runner.
4. **`v0.1.6`'s Android record remains wrong.** Fixed from the next build only, by decision.
5. **Carried over and deliberately untouched** (brief §3): Darwin tag profile in `verify.yml`, Apple
   Cronet archive-byte provenance, the orphan-script sweep, the macOS config-validation gap, fuzz
   and benchmark architecture.

---

## 9. Release decision

```text
FINAL_CLOSURE = COMPLETE
RELEASE_FREEZE = READY_FOR_MAINTAINER_REVIEW

FORMAL_RELEASE_CREATED = NO
TAG_MOVED = NO
RELEASE_ASSETS_PUBLISHED = NO
```

All five required items are done: the Android record now names the tags the AAR is compiled with and
is checked against the builder; the Windows runtime audit is a workflow of its own with no
suppression, and the release reads only the build workflow; the mobile contract has one owner and is
out of the Linux artifact job; the SKIP-only coverage claim is gone; and the release self-test's
three executions are documented as an enforced decision rather than silently changed.

No version tag was created or moved, no Release was created, and no asset was published. The freeze
stays until a maintainer decides otherwise.
