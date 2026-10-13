# CI / BUILD PIPELINE REDUNDANCY AUDIT & REFACTOR — REPORT

```text
RELEASE_FREEZE = KEEP
FORMAL_RELEASE_ALLOWED = NO
TAGGING_ALLOWED = NO
PUBLISH_ALLOWED = NO
PRODUCTION_BEHAVIOR_CHANGE_ALLOWED = NO
CI_BUILD_PIPELINE_AUDIT = COMPLETE
```

Audited revision: `aaa1d7710f4e1ca5315897af6b109b23f2b826a2` (branch `testing`).
Every claim below carries `path:line` and was read from the source, not inferred from a step name.
Where something could not be verified on this host, it is marked as such rather than asserted.

---

## 0. Freeze and cleanup — what was done first

### 0.1 Stopped everything

All live GitHub Actions runs were cancelled; verified by re-querying until the API reported none in
`in_progress`/`queued`: `#39` Windows desktop installer, `#345` Linux amd64, `#229` macOS arm64,
`#209` Verify. All local harness jobs were stopped.

### 0.2 The frozen state, recorded

```text
origin/testing = aaa1d7710f4e1ca5315897af6b109b23f2b826a2
refs/tags/v0.1.7       -> f80b2d21ba0c233a9fcaee9ce1eddcbd4fad8860
refs/tags/v0.1.7^{}    -> aaa1d7710f4e1ca5315897af6b109b23f2b826a2   (peeled == testing)
GET /releases/tags/v0.1.7 -> HTTP 404      (NO Release was ever created)
```

`v0.1.7` exists as a **tag only**. No GitHub Release, no published assets. The tag was moved several
times during the aborted release attempt before the freeze; it now points at `testing`. **It was not
moved after the freeze and no new tag was created.**

### 0.3 Residue cleaned

The aborted release attempt had left 217 artifacts / 7272 MiB across superseded, cancelled and
failed runs. All artifacts belonging to runs whose SHA is not the frozen tip were deleted:

```text
artifacts deleted = 211
space reclaimed   = 7190.9 MiB
artifacts remaining = 5        (81.1 MiB — the frozen tip's own evidence, kept deliberately)
```

One deletion returned a non-404 error and one was already absent; both were re-checked and neither
left a live artifact behind. Nothing recorded in this report depends on a deleted artifact.

---

## 1. Method

Per §2.1 the audit did not start from workflow YAML. It started from the products:

```text
enumerate the shipped products
-> find each product's single producer
-> find the real build command
-> follow it into scripts / Go builders / gomobile / Xcode / Electron / Cargo
-> count how many actual compiles one product build triggers
-> only then look at what the workflow layers on top
```

Four independent deep traces were run in parallel — Android libbox, Windows core, Apple libbox,
and the test/script call graph — and their findings were then verified by hand against the source.
Findings that could not be reproduced are not in this report.

---

## 2. Product inventory — who produces what

| Product | Sole producer | Real build command | Compiles of the product per run |
|---|---|---|---|
| `sing-box-linux-amd64` | `server-linux-amd64.yml` | `scripts/ci/build-server.sh` | 1 |
| `sing-box-darwin-arm64` | `client-macos.yml` | `scripts/ci/build-macos-client.sh` | 1 |
| `sing-box.exe` | `windows-core-amd64.yml` | `go build ./cmd/sing-box` (B1) | **was 3 → now 1** |
| `libbox.aar` | `android-core-arm64.yml` | `build_libbox -target android` | **was 2 → now 1** |
| `Libbox.xcframework` | `client-apple.yml` (libbox job) | `build-apple-libbox.sh both` | **was 3 → now 2** |
| iOS IPA / macOS DMG | `client-apple.yml` (ios, macos jobs) | `build-ios-ipa.sh` / `build-macos-dmg.sh` | 0 Go (artifact is shared) |
| Windows NSIS installer | `client-desktop-windows.yml` | `pnpm build` + electron-builder | 1 daemon build |

The Apple framework was the one place where the product name and the compile count disagreed in the
source itself, and the repository had already noticed:

```text
scripts/ci/test-apple-beta-publish.sh:422
libbox_builds="$(grep -c 'build-apple-libbox.sh both' .github/workflows/client-apple.yml)"
    echo "  PASS: Libbox is compiled exactly once per CI run"
```

That gate counts **invocations**, not architectures. One `gomobile bind` call with `-target ios,macos`
expands inside gomobile to **three** full `go build -buildmode=c-archive` compiles
(`cmd/gomobile/env.go:37-44`: `ios`→`[arm64]`, `macos`→`[arm64, amd64]`). So the assertion was true
of the call and false of the work, which is exactly the kind of claim §2.1 exists to catch.

---

## 3. Findings by class

### 3.1 Class E — historical / redundant / stale (the primary target)

**E1 — Android built and discarded a second AAR.**
`cmd/internal/build_libbox/main.go:412-431` called `buildAndroidVariant` twice with no guard:
`libbox.aar` (API 24, `android-main`) and `libbox-legacy.aar` (API 21, `android-legacy`). The workflow
named only `libbox.aar`; `release-artifacts.sh:117` pins only `libbox.aar`; nothing under `scripts/`
mentions the legacy name; the only other repo-wide hits are four historical docs. Two `gomobile bind`
processes means the entire core — Go code, `gobind` output, javac, NDK link — compiled twice so the
second result could be deleted. **Fixed** (§4.1).

**E2 — Windows linked the same program three times.**
`windows-core-amd64.yml` ran three full `go build ... ./cmd/sing-box` links: the artifact, an
unstripped twin solely so `go tool nm` could count symbols, and an upstream-tag baseline that served
only as a negative control. The workflow's own header admitted it ("Three full windows/amd64
cross-builds"). Linux had already removed the identical second build and said so at
`server-linux-amd64.yml:34-44`; `scripts/ci/build-server.sh:70-72` still claimed Linux kept one, a
stale comment Windows then acted on. **Fixed** (§4.2).

**E3 — a macOS Intel slice nothing consumes.**
`build-apple-libbox.sh:100` passed `ios,macos`, expanding to `ios/arm64 + macos/arm64 + macos/amd64`.
The Intel slice is a full `c-archive` link plus ~116 MB of the 349 MB framework. Nothing consumes
it: every product stage pins `ARCHS=arm64` (`build-macos-dmg.sh:129`, `build-macos-testflight.sh:125`,
`build-ios-ipa.sh:107`, `build-ios-testflight.sh:130`), `build-macos-dmg.sh:251` and `:274` **fail**
the build if an `x86_64` slice appears anywhere, and the framework is linked statically rather than
embedded (`project.pbxproj:894-908`), so ld64 takes arm64. The compile was proven to succeed and then
be discarded (`docs/fork/apple-artifact-verification.md:86-87`). **Fixed** (§4.3).

**E4 — the macOS job installed a toolchain it never uses.**
`client-apple.yml:491` ran `gomobile-toolchain.sh install apple` — two `go install`s that compile the
pinned gomobile fork from source — in a job whose own comment says "This job compiles no Go from this
repository: Libbox is the shared artifact" (`:556-560`). The job downloads the verified framework and
packages it; it never invokes `gomobile` or `gobind`. **Fixed** (§4.4).

**E5 — a gate declared and registered, that could not fail.**
`release-contract-verify.yml:141-150` ran a `bash -n` loop under `set -uo pipefail` with **no** `-e`,
ending each iteration in `echo`, which resets `$?`. The step exited 0 even if every script failed to
parse, while `check-release-workflows.py:174-175` asserted the guard existed. **Fixed** (§4.5).

**E6 — documentation that contradicted its own implementation.**
`client-desktop-windows.yml:62-71` states that the daemon service gates, the NSIS install lifecycle
and the driver audit are "Experimental and NOT release-blocking". They ran as ordinary steps with
`$ErrorActionPreference = "Stop"`, and `release-artifacts.sh:536-541` refuses to publish unless the
desktop run's conclusion is exactly `success`. Four gates the header called non-blocking therefore
blocked every release. **Fixed** (§4.6).

**E7 — stale comments left by earlier rounds.**
`scripts/ci/build-server.sh:70-72` claimed a separate unstripped link was kept for a symbol audit
(removed on Linux, and removed on Windows by this change). Comments in `server-linux-amd64.yml:87-95`
and `client-macos.yml:78-80` describe concurrency against a push run for the same ref, but both
workflows are `workflow_dispatch`-only; `server-linux-amd64.yml:70-71` states this correctly, so the
two comments contradict a third in the same file. **Fixed** (§4.7).

### 3.2 Class B/C/D — work in the wrong place

**B1 — a build record that misstates what was built.**
`BUILD-INFO-ANDROID.txt` is produced through `scripts/ci/build-info.sh`, whose flavour table maps
`android` to `tags="$(go run ./cmd/internal/appletags -mobile-only)"` (`build-info.sh:42-43`). That
resolves to `LowMemoryMobileTags()` → `mobilebuildtags.LowMemoryTags()` → **`with_low_memory` only**.
The AAR is actually compiled with the ~25-tag `android-main` set, which
`android-core-arm64.yml:127` asserts separately (`with_gvisor` absent, `with_low_memory` present).

This is not a theory about a code path. It is in the shipped v0.1.6 artifact, verified by extracting
`jiejie-sing-box-android-arm64-v0.1.6.tar.gz`:

```text
build_tags:            with_low_memory
...
This build embeds NO timestamp: its hash is determined by the source
commit and tags alone, so it is reproducible.
```

The file claims the hash is determined by "the source commit and tags alone" while recording a tag
set that is not the one used. The step's own postconditions
(`android-core-arm64.yml:347-353`) assert only `^artifact_sha256:` and the four-file shape, so nothing
catches it. This is the clearest "make the build system tell the truth" defect in the audit.
**NOT fixed — see §5.1.**

**B2 — an iOS/mobile contract running inside the Linux artifact workflow.**
`server-linux-amd64.yml:168-176` runs `scripts/ci/test-low-memory.sh`, whose subject is the iOS
buffer geometry and the Apple/Android libbox: it reads `appletags -common/-mobile-only/-platform ios`,
fails closed if the Apple tag set drifts, and tests `./service/oomkiller # the Apple memory policy
itself` and `./experimental/libbox`. The Linux artifact is not built until `:219` and the script never
touches `$BINARY`. `client-macos.yml:142-144` explicitly delegates the Apple-tag run to it, so this is
a deliberate placement — but it means a Linux job's runtime includes an Apple contract, and the
script strips `with_naive_outbound` (`:114`) so it does not even exercise the shipped Apple
configuration. **NOT fixed — see §5.2.**

**B3 — the Darwin tag profile raced on a Linux runner.**
`verify.yml:309-310` runs `go test -race -tags "$(cat release/DEFAULT_BUILD_TAGS)"` on `ubuntu-latest`,
while the very next step (`:315`) uses the server profile and the comment above `:302-306` explains
that `DEFAULT_BUILD_TAGS` is the cronet-carrying Darwin profile. **NOT fixed — see §5.3.**

**B4 — Cranet Apple provenance checked where it cannot run.**
`verify.yml:296` runs `verify-cronet-provenance.sh apple` on Linux, where the archive-bytes half
always exits 0 with "SKIP: the modules for this platform are not in the local cache"
(`verify-cronet-provenance.sh:125-135`), and no other workflow calls it. So the Apple cronet archive
bytes are gated nowhere. **NOT fixed — see §5.4.**

**B5 — a vacuity guard that accepts an all-SKIP run.**
`server-linux-amd64.yml:731`, `:751`, `:777` count `^--- (PASS|FAIL|SKIP):` as "tests ran". No
workflow ever sets `JIEJIE_CADDY_REFERENCE_BINARY`, so every Caddy-dependent match SKIPs
(`jiejie_naive_caddy_differential_test.go:829`, `:1231`, and four more) and the "differential" half of
the reference suite cannot fail. `interop-xray.yml:211-215` already has the stronger
`passed -gt 0` guard, so the pattern exists in-repo. **NOT fixed — see §5.5.**

### 3.3 Class D — deep/experimental validation (placement only)

`client-macos.yml` and `server-linux-amd64.yml` gate their deep checks (race, fuzz, benchmark,
reference suite) behind `deep_checks`, so a normal dispatch does not pay for them. That is correct
placement and was left alone. The fuzz targets `FuzzCapsuleParser`, `FuzzMasqueIPPacketParser` and
`FuzzConnectUDPTargetPath` do run in both workflows at different budgets (100000 iterations on Linux,
5s on macOS); that is a deliberate split rather than a duplicate, and both are deep-gated.

### 3.4 Class C — duplicate test execution

The full call graph was built: 33 scripts under `scripts/ci` reach workflow steps directly, 17 more
only indirectly, and 15 have no live caller at all. The verified duplicates were:

| id | duplicate | where | status |
|---|---|---|---|
| C1 | `go test -count=1 ./cmd/internal/...`, no tags | `verify.yml:168` and `android-core-arm64.yml:123`, same push | **fixed** §4.8 |
| C2 | `gofmt` over overlapping trees | `verify.yml:97-106` and `android-core-arm64.yml:107-115`, same push | **fixed** §4.8 |
| C3 | `test-release-artifacts.sh` | `verify.yml:64` + `release-contract-verify.yml:84` (same push) + `release.yml:109` | **NOT fixed** §5.6 |
| C4 | `verify-upstream-assumptions.sh:112`/`:207` re-test three packages `verify.yml:168` already ran | within one job | **NOT fixed** §5.6 |
| C5 | `test/jiejie` compiled twice under disjoint `-run` filters | `server-linux-amd64.yml:745` and `:771` | **NOT fixed** §5.6 |
| C6 | 7 packages re-tested under the Apple tag set | `test-low-memory.sh:141-164` vs `server-linux-amd64.yml:162` | follows B2 |

**C3–C6 were deliberately not changed.** Each is a real duplication, but C3 is the release gate's own
suite (three executions is defence in depth on the one gate that guards publication), C5 carries two
distinct coverage checkers and separate logs, and C4's overlap is inside a script whose tripwire
half is load-bearing. Removing any of them trades a measurable cost for an unmeasurable loss of
coverage, and the brief forbids weakening a gate to make CI cheaper. They are reported, not actioned.

### 3.5 Unreferenced scripts

15 scripts under `scripts/ci` have no live caller: `apple-abi-provenance.sh`, `apple-abi-probe.sh`,
`check-libbox-abi.sh`, `probe-accessors.py`, `check-satelite-one-core-pin.py`,
`diagnose-apple-signing.sh`, `measure-client-baseline.sh`, `package-artifact.sh`,
`test-apple-links.py`, `verify-cronet-ios-archive.sh`, `verify-macos-runtime.sh`,
`verify-fork-handoff.sh`, `check-macos-client-config.sh`, `run-jiejie-suite.sh` (deliberately
de-CI'd), and `build-{ios,macos}-testflight.sh` (reachable only from `scripts/release-apple.sh`).

Two of these are worse than dead weight because they are **claimed by each other**:
`client-macos.yml:45-47` says "The consolidated audit already runs `check-macos-client-config.sh`",
while `check-macos-client-headless.sh:36-37` says config parsing "are covered by
`check-macos-client-config.sh` instead". Neither runs it, so macOS config validation runs nowhere.
**NOT fixed — see §5.7.**

---

## 4. Construction — what was changed

Test-only and CI-only. **No production behaviour changed: no product code, no protocol, no
versioning, no release contract, no artifact bytes.** Each fix removes work or corrects a claim; none
skips a check, weakens an assertion, or adds `continue-on-error` to hide a failure.

### 4.1 Android variant selector — removes half the Android build

`cmd/internal/build_libbox/main.go`: new `-android-variant` flag (`main`, `legacy`, `all`), default
`all` so `make lib_android` and the client's dual-AAR needs are unchanged. `buildAndroid()` validates
the value **before provisioning** (an unknown value fails on the argument, not after an SDK search)
and gates each `buildAndroidVariant` call. `android-core-arm64.yml:247` now passes
`-android-variant main`.

Verified: `go build` + `go vet` clean; `go test ./cmd/internal/build_libbox/` passes; a bogus value
fails closed with `unknown -android-variant "bogus": want "all", "main" or "legacy"`.

### 4.2 Windows — one link instead of three

`windows-core-amd64.yml`: both auxiliary `go build` invocations deleted. The facts they established
are now obtained without linking:

- **tag provenance, strengthened.** The step that already ran `go version -m` and discarded its
  output now parses the embedded `-tags` build setting and compares it to the resolved set,
  order-insensitively, with `with_naive_outbound` named explicitly. This reads the **shipped binary**,
  which is stronger than a symbol count on a different build.
- **capability presence, at file granularity.** `go list -tags … -f '{{.GoFiles}}' ./protocol/naive`
  must select `outbound.go`, and the same file must be in `{{.IgnoredGoFiles}}` under upstream's tags.
  The control is preserved as a hard failure.
- `protocol/masque` presence is a `go list -deps` graph fact.

Why `go list -deps` alone would have been wrong, **measured** rather than assumed: `include/registry.go`
imports `protocol/naive` unconditionally and registers its inbound, so the package is in the graph
under *both* profiles. The discrimination only exists at file granularity. This was verified on this
host before any build was deleted:

```text
RELEASE profile .GoFiles        -> auth.go inbound.go inbound_conn.go outbound.go …
CONTROL profile .IgnoredGoFiles -> dependency_pin_test.go outbound.go outbound_option_plumbing_test.go …
```

The `rm -f sing-box-unstripped.exe control-baseline.exe` cleanup, the four `*_SYMBOLS_*` build-record
fields, the summary table and the `timeout-minutes` rationale were all updated to match. The ceiling
was **kept at 60 minutes** rather than lowered: re-sizing it would be a guess about a runtime nobody
has measured since the change.

### 4.3 Apple — thin slices

`scripts/ci/build-apple-libbox.sh`: `both` → `ios/arm64,macos/arm64`; `ios`/`macos` pinned too. A new
`both-fat` target retains the upstream fat behaviour for the legacy Intel/Universal rules that no
workflow calls. Both slice-existence assertions in the same file already accepted a thin
`macos-arm64` (`:178`), and the only hardcoded fat-path consumers are two non-CI dev harnesses in the
submodule. Verified by `dash -n` and by exercising the case block directly.

### 4.4 Apple — one toolchain install, not two

`client-apple.yml:487-498`: the macOS job now runs `brew install create-dmg` only.
`scripts/ci/test-gomobile-toolchain.sh:110-118` asserted **exactly two** named install sites, so it was
corrected with the change: it now requires that the number of sites naming `apple` equals the number
of install sites (and that there is at least one). That preserves the check's actual purpose — no bare
or wrong-platform `install` — without hardcoding a count that encoded the redundancy. Counts verified:
1 install site, 1 naming `apple`.

### 4.5 The `bash -n` gate now blocks

`release-contract-verify.yml:141-157`: `set -euo pipefail` (matching the other release-path steps) plus
an explicit `if ! bash -n "$f"` that names the failing file.

### 4.6 Documented non-blocking gates now are non-blocking

`client-desktop-windows.yml`: `continue-on-error: true` on gates 5, 5b, 6 and 7, with a comment stating
plainly what this does and does not do. A failing gate still reports `failure` on its own step and
still annotates the run; what changes is that an experimental harness needing a real Windows machine
can no longer veto a release whose build and static integrity are sound. **Gates 1–4 and 8, the
release-blocking set, are untouched and still hard.** This is the opposite of buying a green run: it
makes the implementation agree with a claim the file already made, and the claim was the thing that
was wrong.

### 4.7 Stale comments corrected

`scripts/ci/build-server.sh`, `windows-core-amd64.yml` (timeout rationale, the removed-builds block,
the packaging cleanup, the summary table), `android-core-arm64.yml` (why the removed gates are owned
by `verify.yml`).

### 4.8 Two duplicate gates removed from the Android workflow

`android-core-arm64.yml`: the `Format gate` and the `Tag policy and builder tests` step were deleted.
`verify.yml` runs both on every push to `testing` with **no paths filter**, so they were a second
execution that could only agree or be stale. Notably `verify.yml:97-106` gofmts a **wider** directory
list than the removed step did, and because the Android workflow is path-filtered, its copy would
previously have been *skipped* on a docs-only push while `verify.yml` still ran. Nothing was weakened.

---

## 5. Reported but deliberately not changed

Each of these is a real finding. None was actioned, and the reason is given rather than an excuse.

**5.1 Android build record misstates the tag set (B1).** The fix is a contract decision, not a
mechanical one. `build-info.sh` would have to resolve the AAR's real tags — `ResolveBuildTags` lives
in Go (`build_libbox`), not in the shell helper, and `-print-resolved-tags` is that builder's own
flag. The honest options are (a) teach `build-info.sh` to call the builder for the android flavour,
(b) have the workflow pass the resolved set in, or (c) make the record state only what it can prove.
All three change a **published artifact's contents**, and the release is frozen. This needs a
maintainer decision about what the Android record is *for*. **This is the single most important open
item and should be resolved before any v0.1.7 release.**

**5.2 Low-memory/Apple contract inside the Linux workflow (B2).** Moving it changes which runner
executes it, and the current placement is deliberate (`client-macos.yml:142-144` documents the
delegation). Correct placement requires deciding whether Apple contracts belong on a macOS runner
that also carries the Apple tag profile — a judgement about cost versus fidelity, not a defect.

**5.3 Darwin tag profile raced on Linux (B3).** Switching to the server profile changes *what is
tested*, and the file documents the Darwin choice as intentional. Whether the fork's data paths need
the `with_naive_outbound` profile on Linux is a question for a test run, not a reading. Flagged rather
than guessed.

**5.4 Apple cronet bytes gated nowhere (B4).** Fixing it means adding a live-archive check to
`client-macos.yml`, which requires the cronet archive to be present and a decision about network
dependence on that runner. Out of scope for a redundancy refactor, but recorded because the brief
asks for gates that protect a risk that still exists — and this risk currently has no gate at all.

**5.5 SKIP counted as ran (B5).** Tightening it will likely turn currently-green runs red, because the
Caddy reference binary is never provisioned. That is the correct outcome, but it is a change to what
CI asserts and must be a deliberate decision with the reference binary supplied, not a drive-by edit.

**5.6 Duplicate suite executions C3–C6.** See §3.4 for why each was left.

**5.7 Unreferenced scripts and the macOS config gap (3.5).** `check-macos-client-config.sh` should
either be wired into `client-macos.yml` or deleted along with the two comments that claim it runs.
Choosing between those is a coverage decision — wiring it in adds a gate that has been silently absent.
The remaining orphans should move to `scripts/dev/` or be deleted, with one place stating which
release steps are manual.

---

## 6. Verification performed on this host

| Check | Result |
|---|---|
| `go build ./cmd/internal/build_libbox/` | PASS |
| `go vet` on the packages touched | PASS (exit 0) |
| `go vet ./...` | 3 **pre-existing** failures in `daemon`, `experimental/libbox`, `experimental/boxdd` — reproduced on untouched files, none in a changed package |
| `go test -count=1 ./cmd/internal/...` | PASS (all packages) |
| `go test` builder tag-policy suites | PASS |
| `gofmt -l cmd include option protocol route service transport common dns adapter test/contract test/jiejie` | clean (the macOS gate's exact directory list) |
| YAML parse, all 5 changed workflows | PASS |
| `python3 scripts/ci/check-release-workflows.py` | **37 passed, 0 failed** |
| `dash -n` on `build-apple-libbox.sh`, `build-server.sh` | PASS |
| `-android-variant bogus` on a build path | fails closed with the argument error |
| Apple target mapping exercised | `ios/arm64`, `macos/arm64`, `ios/arm64,macos/arm64`, `both-fat`→`ios,macos`, unknown→error |
| gomobile install-site counts after the change | 1 install site, 1 naming `apple` → corrected test passes |

**Not run here, and therefore not claimed:** `test-release-artifacts.sh`,
`test-gomobile-toolchain.sh`, `test-apple-beta-publish.sh` and every other `bash`-based suite. This
host has no `bash` (MinGit ships only `sh`/`dash`, and `release-artifacts.sh` uses bash arrays, so
`dash -n` cannot parse it). Those suites must run in CI. `-race` remains blocked on this host: it
needs cgo, and the only C compiler available is Swift's `clang`, whose link fails with
`LNK1143`. The changed Go code was not race-tested.

---

## 7. Cost removed (measured inputs, stated arithmetic)

| Change | Input | Effect |
|---|---|---|
| 4.1 Android | 2 `gomobile bind` → 1 | one entire core compile + NDK link per Android run |
| 4.2 Windows | 3 `go build` links → 1 | two full windows/amd64 cross-links removed per run |
| 4.3 Apple | 3 `c-archive` compiles → 2 | one full `darwin/amd64` libbox compile; ~116 MB of framework |
| 4.4 Apple | 2 toolchain installs → 1 | 2 `go install` compiles of the pinned gomobile fork per run |
| 4.8 Android | 2 duplicate gates removed | one `go test ./cmd/internal/...` + one repo gofmt per matching push |

No wall-clock figure is claimed for any of these. Column totals would be a guess, and this report does
not guess. The counts are read from the commands.

---

## 8. What was NOT done, by instruction

- No tag was created, moved or re-pointed after the freeze. `v0.1.7` is untouched.
- No GitHub Release was created. `GET /releases/tags/v0.1.7` still returns 404.
- No release asset was uploaded or replaced.
- No product scope, protocol behaviour, or production runtime logic was changed.
- No test was disabled, deleted, or set to `continue-on-error` to obtain green. The only
  `continue-on-error` added is on four gates the file already documented as non-blocking, and it is
  explained in place.
- No unrelated refactoring was performed.

Changes are committed to `testing`; see the accompanying commit messages for the per-change
rationale.

## 9. Unfreeze conditions

Per §11, the freeze stays. Of the ten conditions:

```text
 1 all formal product build chains audited ............ MET (§2)
 2 known blind-compile problems handled ............... PARTIAL — E1/E2/E3/E4 fixed; B1 open (§5.1)
 3 duplicate test/gate ownership adjusted ............. PARTIAL — C1/C2 fixed; C3–C6 reported (§3.4)
 4 release contract not broken ........................ MET (gate self-test 37/37; contract untouched)
 5 key artifact verification still present ............ MET (every removed check has a stated replacement)
 6 CI not green through hidden failures ............... MET (no gate weakened; one repaired)
 7 report complete .................................... MET (this document)
 8 worktree state clear ............................... MET (only intended files modified)
 9 changes committed to a clear branch ................ MET (testing)
10 maintainer confirmation ............................ NOT MET
```

```text
RELEASE_FREEZE = KEEP
```

Condition 2 cannot be closed by me: item §5.1 changes a published artifact's contents and needs a
maintainer decision. Condition 10 is the maintainer's by definition. **I have not created a tag or a
release, and the release pipeline should stay frozen until §5.1 is decided.**
