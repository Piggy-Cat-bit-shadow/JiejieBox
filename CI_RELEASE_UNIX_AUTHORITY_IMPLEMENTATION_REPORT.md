# CI / Release â Unix release authority implementation report (V6)

Sole report for the V6 round. It does **not** rewrite the V5 audit chapters in
`CI_RELEASE_POST_GREEN_OPTIMIZATION_REPORT.md`; that file keeps its history, as V6 Â§38 asks.

**Headline: the construction stages were not run.** V6 Â§2 and Â§3 make a real Unix/macOS/Linux
environment and a green 92-case baseline preconditions of write work, and both are absent here.
What this round delivers instead is a **read-only Apple-pipeline verification** (Â§31.1's
emphatic item plus the Â§6/Â§7/Â§9/Â§10/Â§11 claims), an **executable Stage 1â3 acceptance framework**
that runs in this environment, and one identity precision that changes a V6 acceptance check.

---

## 0. Hard inputs for the next round â do not re-investigate

Two facts were settled by measurement this round. They are inputs to Stages 6 and 7, and the
next round should consume them rather than rediscover them.

### 0.1 `manifest.version` is `JJ_ARTIFACT_VERSION`, not `JJ_VERSION`

```text
apple-libbox-artifact.sh:83   version="$JJ_ARTIFACT_VERSION"
scripts/ci/version.sh:18      version="$(cat release/JIEJIE_VERSION | tr -d '[:space:]')"   # JJ_VERSION
scripts/ci/version.sh:25      artifact_version="${version}+jiejie.${short_sha}"             # JJ_ARTIFACT_VERSION
```

So the manifest's `version` is `0.1.7+jiejie.<short_sha>` while `release/JIEJIE_VERSION` is
`0.1.7`. **V6 Â§15's proposed check `manifest.version == JJ_VERSION` would fail on every
artifact.** The preferred fix, and the reason it is recorded here rather than worked around:
add an explicit `jj_version` field to the manifest, so version identity is a field lookup rather
than a substring question. Adding it is backward compatible and belongs with Â§11's other
provenance fields.

### 0.2 Stage 7's `auto` chain must branch on asset PRESENCE, never on validation outcome

V6 Â§31.1 asks whether a present-but-invalid canonical Release asset could be mistaken for
"not found" and silently fall back to Actions. **That trap is not reachable today**, and the
reason is structural: `publish-apple-beta.sh` has exactly one source and every failure in the
chain is `fail`, not fall-through. It becomes reachable only once Stage 7 adds a second source.

The required semantics, to be implemented in Stage 7:

```text
canonical Release asset ABSENT   (exact name not in the release's asset list)
    --libbox-source release -> FAIL
    --libbox-source auto    -> Actions exact-SHA path (existing behaviour, unchanged)

canonical Release asset PRESENT
    download -> validate parent_sha, version, digest
    ANY failure -> FAIL CLOSED. Never continue to Actions.
```

Presence must be decided by **listing the release's assets by exact name**, never by a
validation outcome. A validation failure means the canonical source is corrupt; dressing that as
absence is the one outcome this design exists to prevent.

---

## 0.3 Executable Stage 1â3 acceptance framework

Added this round, because it needs no `jq`, no `chmod` and no Unix, and it turns Â§24's five
structural criteria from prose into a command. One checker, two modes â no second checker was
created:

```bash
python scripts/ci/check-release-workflows.py                    # current repository: must PASS
python scripts/ci/check-release-workflows.py --target-state v6  # Stage 1-3 target: expected FAIL today
```

The default mode is **unchanged** and still reports repository regressions; it passes 114/114.
The target mode describes a state the repository is deliberately not in yet and never alters the
default verdict (verified: default `0`, target `1`, default `0` again, with counters reset
between calls).

Current target-state result â **3 passed, 6 failed**, no fabricated green:

| CRITERION | STATE | OBSERVED |
|---|---|---|
| V6-1 Verify stops executing the 92-case suite | FAIL | `verify.yml:121` |
| V6-2 Formal Release stops executing it | FAIL | `release.yml:180` |
| V6-3 RCV still executes the full suite | PASS | `release-contract-verify.yml:107` |
| V6-3 no other workflow executes it | FAIL | also `release.yml`, `verify.yml` |
| V6-4 Formal Release requires exact-SHA Verify | FAIL | `release.yml` code never names `verify.yml` |
| V6-4 Formal Release requires exact-SHA RCV | FAIL | `release.yml` code never names `release-contract-verify.yml` |
| V6-5 `release-artifacts.sh` holds the selection primitive | PASS | `newest_run` present |
| V6-5 acceptance engine not a second release verdict | PASS | not reachable from `release.yml` |
| V6-5 no workflow declares it as a gate | FAIL | still declared in `verify.yml:155` |

Three design points worth recording, because each was a real trap:

* **"Executes" is not a substring test.** `release-contract-verify.yml` mentions the suite six
  times in code â a `paths:` entry, a `bash -n` list entry, and three `grep` guards asserting the
  suite still wires its own mock `gh`. Only one of the six is an execution. A substring check
  would have called a single execution "four executions" or, worse, read a `grep` guard as a run.
* **V6-5 cannot be "the file is deleted."** V6 Â§4.4 explicitly allows the parser, fixtures and
  test utility to survive, so the criterion is "no second may-release authority is reachable
  from the release path". The engine is therefore judged by whether `release.yml` reaches it
  **and** whether any workflow still declares its self-test as a gate.
* **V6-5's third half initially looked at one file and passed wrongly.** It read only
  `release-contract-verify.yml`, found nothing, and reported satisfied â while `verify.yml:155`
  runs `verify-release-acceptance-test.sh` on every push. It now scans every workflow. That
  correction is the same defect class the V5 audit named ("a checker that protects the wrong
  fact"), caught this time by cross-checking a PASS instead of a FAIL.

Deliberately **not** asserted, per instruction: Android exact artifact field names and Apple
canonical Libbox metadata shape. Those directions are agreed, but their concrete form should be
fixed by the Stage 4â7 implementation; a checker written first would distort the implementation
to satisfy itself.

---

## A. Environment / Baseline

### A1. Environment

V6 Â§2 requires a real Unix/macOS/Linux and lists the tools to record. Re-measured this round,
`command -v` for the whole Â§2 list:

| TOOL | RESULT |
|---|---|
| `uname` | **NOT FOUND** |
| `bash` | **NOT FOUND** (a borrowed MinGit `sh.exe` satisfies bash semantics; it is not on PATH) |
| `git` | **NOT FOUND** (borrowed portable MinGit 2.50.1) |
| `jq` | **NOT FOUND** |
| `chmod` | **NOT FOUND** |
| `sha256sum` | **NOT FOUND** |
| `shasum` | **NOT FOUND** |
| `mktemp` | **NOT FOUND** |
| `sed` | **NOT FOUND** |
| `awk` | **NOT FOUND** |
| `grep` | `C:\Program Files (x86)\Embarcadero\Studio\37.0\bin\grep.exe` â an IDE's, not GNU |
| `tar` | `C:\Windows\system32\tar.exe` â bsdtar, not GNU tar |

```text
OS                  = Microsoft Windows NT 10.0.19044.0
wsl.exe             = present; NO distribution installed (`wsl --list` exits 1)
MSYS2 / Cygwin      = absent (C:\msys64, C:\cygwin64 both missing)
scoop / chocolatey  = absent
docker/podman/nerdctl/vagrant/busybox = absent
```

This is the third consecutive round in which this gate has been measured and it has not
changed. No `jq` shim, fake `chmod`, or approximate coreutils was written (Â§2 prohibitions).

### A2. Baseline

V6 Â§3's required baseline, as actually obtainable:

| COMMAND | RESULT |
|---|---|
| `bash scripts/ci/test-release-artifacts.sh` (92-case) | **NOT RUN** â exits 127 at `line 377: chmod: command not found` |
| `bash scripts/ci/verify-release-acceptance-test.sh` | **NOT RUN** â needs `mktemp`/`chmod` |
| `python scripts/ci/check-release-workflows.py` | **PASS** 114/114 |
| `bash scripts/ci/require-executed-coverage.test.sh` | **PASS** 11/11 |
| `python scripts/ci/test-desktop-release-version.py` | **PASS** 60/60 |
| `bash scripts/ci/test-desktop-source-identity.sh` | **PASS** 30/30 |
| `bash -n` on 6 release-path scripts | **PASS** (parse only â not behaviour, per Â§35) |
| YAML parse, 12 workflows | **PASS** |
| `git diff --check` | **PASS** |

`FULL_92_CASE_RELEASE_SUITE = NOT RUN`, class **ENVIRONMENT** â not `PRE_EXISTING REGRESSION`,
not `FIXTURE DRIFT`, not `REAL RELEASE-TOOLING BUG`. Per Â§3 that means **STOP WRITE WORK**: the
red light is an environment light, not a V6 construction light, so no release-critical tooling
was touched.

### A3. Tree identity â V6 Â§1's hard gate

Â§1 forbids starting V6 from a fresh `origin/testing` clone, because multiple rounds of local
work are unpushed. This tree carries the frozen results; Â§1's checklist, verified:

| Â§1 REQUIREMENT | STATE |
|---|---|
| Desktop gitlink `917d2a4e9864528e1c10c4c6a39f3949b6f5d878` | **present** |
| Desktop static branding overlay retired | **present** (deleted; only prose + tests assert its absence) |
| `release/JIEJIE_VERSION` is the dynamic desktop release-version authority | **present** |
| `client-desktop-windows.yml`: `tooling_sha` required and `== core_sha` | **present** |
| Verify: root-level `*.md` docs-only filter | **present** (`paths-ignore: ["*.md"]`, both triggers) |
| RCV: narrowed path trigger, executed helper paths not missed | **present** (18 paths; coverage-guard scripts included) |
| `check-release-workflows.py`: source Verify and RCV checked separately | **present** (`SOURCE_VERIFY` + `check_source_verify()`) |
| `.github/update_clients.sh`: no blanket hard-reset to `origin/main` | **present** (branch read from config, ancestry required, gitlink read-back) |

```text
LOCAL_HEAD                    = 32e29be39  (12 commits ahead of origin/testing)
REAL_REMOTE_TESTING           = 9bdcf8e8b17b67a2c0fd306d2cc865535dc6970e  (git ls-remote)
LOCAL_REMOTE_TRACKING_TESTING = 9bdcf8e8b17b67a2c0fd306d2cc865535dc6970e  (in agreement)
```

So the tree is correct and **must be carried to the Unix environment**, not re-cloned. Â§1's
"STOP" applies only to a tree lacking these; this one has them all.

---

## B. Release authority

**Not implemented â Stage 1 blocked.**

Inventory done read-only, as Â§20 requires, so Stage 1 starts from a map rather than a survey:

| RULE | CURRENT OWNER | USED BY RELEASE? | INTENT |
|---|---|---|---|
| exact SHA match | `newest_run` (`release-artifacts.sh:180`) | **yes** | KEEP, promote to canonical primitive |
| workflow identity | `resolve_workflow_id` (`:163`) â asserts `path == file`, `state == active` | **yes** | KEEP |
| run status / conclusion | `cmd_plan` (`:376-391`), `cmd_desktop` (`:534-554`) | **yes** | KEEP, already handles `skipped` explicitly |
| newest-run-wins, no fallback to older green | `:205` `sort -k2n -k1n \| tail -1` | **yes** | KEEP â 92-case `stale_green` + `reruns` cover it |
| per-job status | `verify-release-acceptance.sh` | **NO** | MERGE into canonical authority |
| artifact declarations + recorded digests | `verify-release-acceptance.sh` | **NO** | MERGE |
| artifact expiry | `cmd_desktop` reads `.expired` (`:567`); **4 targets do not** | partial | MOVE (V5 F-8) |
| missing artifact / wrong workflow / wrong SHA | both, independently | **yes** (two implementations) | MERGE |

Blocked Stage 1 output would have been: `select_exact_sha_run` / `require_exact_sha_success`,
with `verify-release-acceptance.sh` demoted to parser + test helper and its independent
may-release verdict retired.

---

## C. Artifact identity

**Not implemented â Stage 4 blocked.** Read-only classification of where identity is exact
today, so Stage 4 has a target:

| SELECTOR | EXACT? | SITE |
|---|---|---|
| run selection | **exact SHA** | `newest_run`, server-side `head_sha` filter |
| run â run_id | **exact** | plan TSV; never re-queried |
| RCV verify step | **exact by name** | `gh run download --name <artifact>` in `cmd_desktop` |
| 4 targets artifact | **shape** | `gh run download <run_id> --dir <dest>` then `find -name <binary>` |
| desktop installer | **shape + count** | artifact list filtered by version substring, count asserted |
| public assets | **exact, no glob** | `cmd_flatten` expected_assets allowlist |

Artifact names, measured: `android-core-arm64-${github.sha}` and `jiejiebox-libbox-${github.sha}`
are SHA-derived (unique per commit); `windows-core-amd64-v${version}`,
`jiejiebox-windows-x64-${CORE_VERSION}-signed-test` are version-derived (can repeat across SHAs);
`jiejiebox-windows-build-records`, `jiejiebox-windows-unpacked-audit-input`,
`jiejiebox-windows-runtime-audit-records`, `release-contract-verify-logs`, `release-gate-self-test`
are constants.

---

## D. Android

**Not implemented â Stage 5 blocked.** V6 Â§5.2 is confirmed accurate: the producer asserts
`release-android/` is **exactly** the four payload files (`android-core-arm64.yml:388-393`) and
then uploads **six** paths (`:418-431`), adding `libbox.provenance` and `SHA256SUMS.txt`. The
consumer side (`release-artifacts.sh:448` one-artifact-subdirectory rule, `:970` `find -name`)
is the lagging contract. No cleanup to remove yet â the cleanup V6 Â§26 wants deleted does not
exist as a separate step; the exclusion is the flatten allowlist and the tar file-list assertion.

---

## E. Apple canonical Libbox â read-only verification

This is the substantive work of the round, because V6 asks (Â§31.1, emphasis in the brief) for
one specific correctness property to be checked, and because Â§6/Â§7/Â§9/Â§10/Â§11 make factual
claims about the existing pipeline that are load-bearing for the design.

### E1. V6 Â§6 and Â§7 are accurate

| V6 CLAIM | VERIFIED |
|---|---|
| `client-apple.yml` has a separate `libbox` job; `Libbox.xcframework` built once, reused by iOS and macOS | **yes** â `libbox` job uploads `jiejiebox-libbox-${github.sha}`; the `ios` and `macos` jobs both `download-artifact` it |
| retention 30 days | **yes** (`retention-days: 30`) |
| `apple-libbox-artifact.sh` has `pack` / `validate` / `install` | **yes** (`:41-43`) |
| manifest records `parent_sha version build_time_utc xcode_version go_version artifact artifact_sha256` | **yes** â exactly those seven (`:126-134`) |
| `validate` is fail-closed | **yes** â no `parent_sha` â fail; no `artifact_sha256` â fail; parent mismatch â fail; missing archive â fail; SHA256 mismatch â fail (`:177-191`) |
| `publish-apple-beta.sh` resolves exact SHA â exact run â validates â installs into both â `APPLE_USE_PREBUILT_LIBBOX=1` | **yes** (`:167-169`, `:225-229`, `:261`, `:272`, `:288-289`, `:313`) |
| prebuilt missing â FAIL, never a silent gomobile rebuild | **yes** â `release-apple.sh:223-227` fails when the flag is set and the Libbox is not installed |
| Â§9: `sing-box-darwin-arm64` â  `Libbox.xcframework` | correct and worth keeping in the design |

So V6's "already 80% done" assessment is right, and Â§18's instruction not to rewrite
`release-apple.sh` is right too.

### E2. Â§31.1's emphatic check â PASSES today, and the reason is structural

Â§31.1 asks whether `publish-apple-beta.sh` could mistake "canonical source exists but failed
validation" for "not found" and then fall back. **It cannot, because there is no fallback chain
to fall back into.** Today's source selection is single-source:

```text
local_sha = HEAD
    â
if --run-id not given:
    gh run list --workflow client-apple.yml --branch testing --limit 100
      -q '[.[] | select(.headSha == $local_sha and .conclusion == "success")][0].databaseId'
    â
    empty  â fail "no run found â¦ and --no-dispatch was given"   (with --no-dispatch)
    empty  â require local_sha == remote branch tip, then DISPATCH and wait
    â
    run_sha  == local_sha   || fail
    run_conclusion == success || fail
    libbox/ios/macos jobs     || fail
    â
    gh run download <run_id> --name "jiejiebox-libbox-$local_sha"
    â
    apple-libbox-artifact.sh validate --parent-sha <local_sha>
    â
    install into both clients, check-apple-shared-libbox.sh
    â
APPLE_USE_PREBUILT_LIBBOX=1 release-apple.sh testflight
```

There is exactly one source, and every failure above is `fail`, not a fall-through. So the
property V6 Â§31.1 wants preserved **already holds for the source dimension**. It becomes a real
risk only *after* Stage 7 introduces a second source â which is precisely why Â§31.1 flags it,
and why the sequencing matters: **the `auto` chain must be built so that "release asset present
but invalid" is a distinct, fatal outcome from "release asset absent".** Recommended shape:

```text
release asset PRESENT:
    download â validate parent_sha, version, digest
      any failure â FAIL, never continue to Actions
release asset ABSENT (404 / exact name not in the asset list):
    with --libbox-source release â FAIL
    with --libbox-source auto    â Actions exact-SHA path (unchanged behaviour above)
```

The distinction has to be on *presence*, tested by listing the release's assets by exact name â
not on *validation outcome*. A validation failure is a corrupted canonical source and must never
be dressed as absence.

### E3. Two precisions that change V6's stated acceptance checks

**(1) `manifest.version` is not `JJ_VERSION`.** V6 Â§15 proposes verifying
`manifest.version == JJ_VERSION`. Measured, `apple-libbox-artifact.sh:83` sets
`version="$JJ_ARTIFACT_VERSION"`, and `scripts/ci/version.sh:25` composes that as:

```text
JJ_VERSION           = 0.1.7                       (from release/JIEJIE_VERSION)
JJ_ARTIFACT_VERSION  = 0.1.7+jiejie.<short_sha>    (version + the revision)
```

So the manifest's `version` field is the **artifact version**, and a check written as
`== release/JIEJIE_VERSION` would fail on every artifact. The check has to be either
`manifest.version` starts with the release version and carries the same `short_sha`, or â
better, and this is the recommendation â the manifest should gain an explicit `jj_version` field
so version identity stops being a substring question. That is a benign, backward-compatible
addition to the Â§11 field list.

**(2) The manifest lacks the run provenance V6 Â§11 asks for.** Measured fields are the seven
above; `schema_version`, `gomobile` module/version/commit, `xcframework` slice list,
`build workflow identity`, `GITHUB_RUN_ID` and `GITHUB_RUN_ATTEMPT` are **all absent**. Â§11 is
right to want them, and right to insist they are provenance rather than identity: `parent_sha`
is documented in the script itself as "the ONLY commit this records", and `artifact_sha256` is
the digest. Adding provenance must not promote `run_id` into the identity.

A `slices` field is the most valuable of the additions, because `check-apple-shared-libbox.sh`
already asserts both slices are present per client â recording them in the manifest would make
that a declared property of the artifact rather than a per-install discovery.

### E4. `run_attempt` is not part of the Apple selection either

Same class as the V5 finding: `publish-apple-beta.sh:167-169` selects with
`select(.headSha == $local_sha and .conclusion == "success")`, ordered by the API's default
(newest first), so it takes the newest successful run at that SHA. A re-run produces a newer run
and is therefore selected; the older one is not preferred. That matches `newest_run`'s semantics
and needs no change â recorded so the two selectors are known to agree, which Stage 1's
"one admissibility authority" should then keep true by construction rather than by coincidence.

---

## F. Concurrency

**Not implemented â Stage 8 blocked.** State unchanged from V5 F-2, and the argument is
unchanged: V6 Â§5.4 states the risk correctly (Release has no serialization, and candidate
producers can cancel each other â not "release cancels release"). Target per Â§30:
`concurrency` keyed by tag with `cancel-in-progress: false`, and a job timeout above the
helper's maximum bounded wait (90 min â â¥120 min). Â§30.1's rule for producers â "a manual
dispatch candidate must not be cancelled by another manual dispatch" â is exactly the
`verify.yml` pattern already in the tree, so it is a reuse, not a new mechanism.

---

## G. Post-change findings

`POST_CHANGE_AUDIT = INCOMPLETE` â there are no changes to audit. The findings below are
**pre-change**, from the read-only Apple work.

| # | FINDING | PRIORITY | ACTION | WRITE THIS ROUND? |
|---|---|---|---|---|
| G-1 | `manifest.version` is `JJ_ARTIFACT_VERSION` (`0.1.7+jiejie.<sha>`), so V6 Â§15's `== JJ_VERSION` check as written would fail on every artifact. Recorded above (E3-1) | **P1** â a stated acceptance check would not pass | FIX in Stage 6 by adding an explicit `jj_version` field instead of comparing a composed string | NO |
| G-2 | The manifest carries none of Â§11's provenance (`schema_version`, gomobile, slices, workflow identity, `GITHUB_RUN_ID`, `GITHUB_RUN_ATTEMPT`) | P2 | FIX in Stage 6, additively; keep `parent_sha` + `artifact_sha256` as the identity pair | NO |
| G-3 | `publish-apple-beta.sh` has **no** `--libbox-source` concept and no release-asset source at all; a future `auto` chain must branch on asset *presence*, not on validation outcome, or E2's property is lost | **P1** | FIX in Stage 7 â this is the one design trap in the Apple plan | NO |
| G-4 | The `auto` fallback must not consider a *present but invalid* release asset as "absent". Currently not reachable (single source), so this is a forward-looking constraint on Stage 7 only | **P1** | The E2 shape above | NO |
| G-5 | Apple selection and `newest_run` agree on newest-successful-at-SHA semantics today, but by coincidence of two independent implementations | P2 | Stage 1 should make the Apple path use the shared selector, or the agreement should be asserted | NO |
| G-6 | `test-apple-beta-publish.sh` already has 88 check/report lines covering manifest tampering, parent mismatch, missing archive/manifest, install isolation and the prebuilt fail-closed path â so Â§29's list is an *extension*, not a gap | INFO | KEEP â Â§29's new cases are the release-asset ones, which need Stage 7 | NO |

---

## H. Race / deep design

Stage 10 is `DESIGN ONLY` by V6 Â§32, and Â§5.5 correctly sequences it: fix the evidence chain
first, then re-judge. The design recorded in the V5 report (B8) stands unchanged â one
final-candidate owner running the **union** of the three disjoint race sets, producing no
artifacts, with its run id required as release evidence. What V6 changes about it: **after Stage 2
makes `verify.yml` release-required, the push-time race set becomes release-blocking**, which
shrinks the gap from "no race evidence at all" to "no race evidence for the five packages only
the product workflows race, and those behind `deep_checks`". That is a smaller problem, and it is
the right order.

---

## I. Final state

```text
UNIX_RELEASE_AUTHORITY_SCOPE_COMPLETE = NO

P0_C_RELEASE_TEST_OWNER        = INCOMPLETE   (blocked: Unix + 92-case baseline)
P0_D_SINGLE_ADMISSIBILITY      = INCOMPLETE   (blocked: same)

VERIFY_EXACT_SHA_REQUIRED      = NO           (designed, not implemented)
RCV_EXACT_SHA_REQUIRED         = NO           (designed, not implemented)

ARTIFACT_EXACT_NAME_IDENTITY   = PARTIAL      (run/digest exact; artifact name shape-based on 5 of 6)
ANDROID_PAYLOAD_EVIDENCE_SPLIT = INCOMPLETE   (producer already asserts the 4-file payload)
ANDROID_RELEASE_CLEANUP        = STILL_PRESENT (no separate cleanup step exists yet)

APPLE_LIBBOX_CANONICAL_RELEASE_ASSET = INCOMPLETE  (Actions artifact exists; no Release asset)
APPLE_LIBBOX_RELEASE_IDENTITY        = NOT RUN     (nothing to bind yet; manifest identity verified read-only)
TF_RELEASE_FIRST_SOURCE              = INCOMPLETE
TF_ACTIONS_FALLBACK                  = DONE       (existing behaviour, verified and preserved)
TF_LOCAL_LIBBOX_REBUILD_DEFAULT      = FORBIDDEN  (verified: prebuilt missing â FAIL, never rebuild)

RELEASE_CONCURRENCY_HARDENED   = NO
RELEASE_TIMEOUT_BOUNDED        = NO

FULL_92_CASE_RELEASE_SUITE     = NOT RUN   (ENVIRONMENT: chmod absent at line 377; jq absent)
STRUCTURAL_SUITE               = PASS      (114/114 + 11/11 + 30/30 + 60/60)
APPLE_BETA_PUBLISH_SUITE       = NOT RUN   (needs mktemp/chmod; its fixtures were read, not executed)
POST_CHANGE_AUDIT              = INCOMPLETE (no changes to audit)

GITHUB_ACTIONS_RUN             = NO
PUSHED_THIS_ROUND              = NO
TAG_MOVED                      = NO
RELEASE_CREATED                = NO
TESTFLIGHT_UPLOAD              = NO

READY_FOR_FINAL_FREEZE_VALIDATION = NO
```

`READY_FOR_FINAL_FREEZE_VALIDATION = NO` against Â§41's list, and every unmet item is unmet for
the **same** reason â the construction stages did not run:

```text
single release admissibility authority        NOT DONE  (Stage 1)
Verify / RCV exact-SHA required               NOT DONE  (Stage 2)
92-case full PASS                             NOT RUN   (environment)
artifact exact identity deterministic         PARTIAL   (Stage 4)
Android consumer complete / evidence not public  NOT DONE (Stage 5)
Apple canonical Libbox contract complete      NOT DONE  (Stage 6)
TF release-first source                       NOT DONE  (Stage 7)
Release concurrency / timeout hardened        NOT DONE  (Stage 8)
```

Per Â§42 nothing was pushed, dispatched, tagged, released or uploaded, and no FREEZE was declared.

---

## J. What the next Unix round needs, in order

Nothing here is a re-plan; it is the same Â§19 sequence with the entry condition stated.

**Entry condition.** A real Unix environment with `jq` + `chmod` + `sha256sum`, carrying
**these local commits** â the tree must be transported (bundle, branch push under explicit
authorisation, or a shared remote), not re-cloned from `origin/testing`, or V6 Â§1's gate fails
again. Then `bash scripts/ci/test-release-artifacts.sh` must reach `92/92` **before** any edit;
if it does not, classify ENVIRONMENT / PRE_EXISTING REGRESSION / FIXTURE DRIFT / REAL BUG first.

**Then Stage 1â3 as one atomic acceptance** (Â§24): canonical selector in `release-artifacts.sh`;
`verify.yml` and `release.yml` stop running the 92-case, RCV keeps it; Formal Release requires
exact-SHA Verify **and** RCV; `verify-release-acceptance.sh` loses its independent verdict but may
keep its parser and fixtures. **The acceptance criteria are already executable** â run
`python scripts/ci/check-release-workflows.py --target-state v6` and drive it from 6 failures to
0. It needs no `jq`, so it runs before, during and after the Unix work.

Two further things the next round should consume rather than rediscover: Â§0.1 (the manifest
`version` field is `JJ_ARTIFACT_VERSION`) and Â§0.2 (Stage 7's `auto` chain must branch on asset
presence, never on validation outcome).

**Nothing in this round's read-only work is invalidated by the delay**, and two of its results
(E3-1 and G-3) prevent an acceptance check that would have failed and a fallback that would have
masked a corrupted canonical source.


---

# V9 â Apple safety evidence, concurrency, final freeze readiness

Appended. Sections AâJ above are unchanged and are not rewritten; this section is the V9 round's
record. Everything here is either measured in this round or explicitly labelled as not measured.

## T. PRESENT_INVALID integration evidence â **INCOMPLETE**

The rule under test:

```text
canonical Release asset PRESENT, any validation failure  ->  HARD FAIL, never fall back
canonical Release asset ABSENT                           ->  auto MAY fall back to Actions
```

The brief's Â§5 point is the important one and it is correct: asserting `exit != 0` proves nothing
here, because that also passes when the code falls back to Actions and Actions fails for its own
reason. The test has to prove a NEGATIVE.

`scripts/ci/test-apple-canonical-source.sh` was written to do exactly that. Design:

* a real fixture git repository, because the publish script refuses a dirty tree and resolves its
  tag through `git ls-remote`;
* a real bundle packed by the **producer's own packer**, so the manifest under test is a genuine
  one rather than a hand-written copy of the schema;
* a mock `gh` in which every Actions command â `run list`, `run view`, `run download`,
  `workflow run` â is a **SENTINEL**: it records the call, creates a marker file, and exits
  non-zero. Each PRESENT+INVALID case asserts (1) refused, (2) refused for a *canonical* reason,
  (3) **the sentinel file does not exist**;
* an ABSENT control case that *requires* the sentinel to fire, so the negative assertions cannot
  pass vacuously â which is the classic way a negative test proves nothing.

Eleven PRESENT+INVALID mutations are covered (wrong parent_sha, wrong jj_version, digest mismatch,
corrupt archive, missing declared slice, undeclared extra slice, absent slices field, legacy
manifest, present-but-unfetchable) plus three ABSENT controls and two `--libbox-source release`
cases.

**STATUS: the harness runs and builds its fixture correctly, but every case stops in the publish
script's own preflight** with

```text
== repository ==
fatal: not a git repository (or any of the parent directories): .git
```

from `git rev-parse HEAD` at the top of `publish-apple-beta.sh`, **before any canonical-source
logic is reached**. No case in that file currently proves anything about PRESENT_INVALID.

Ruled out with a temporary probe (removed; this is the record):

| HYPOTHESIS | RULED OUT BY |
|---|---|
| the fixture is not a repository | `rev-parse` succeeded in it |
| the mock PATH breaks `git` | `rev-parse` succeeded **with** `PATH=$BIN:$PATH` |
| the working directory is wrong | it is the fixture root, which has `.git` |
| the shim is missing or not executable | present, executable, in `$BIN` |

The remaining difference between the probe's working invocation and the script's failing one is
the `env` prefix used to pass the case environment. **That is where the next session starts.**

The file is committed with this status written at the top of it, and deliberately **not** wired
into any workflow: a suite that fails for an unrelated reason on every run teaches its readers to
ignore it. Deleting it would throw away the sentinel design, the fixture and the assertion set,
which are the parts that took the thinking.

```text
PRESENT_INVALID_INTEGRATION      = FAIL (harness runs; cases do not reach the code under test)
PRESENT_INVALID_ACTIONS_FALLBACK = NOT PROVEN  (neither FORBIDDEN nor POSSIBLE â unmeasured)
ABSENT_AUTO_FALLBACK             = NOT PROVEN
```

**The `FORBIDDEN` verdict the brief asks for is not available from this round.** It is the single
most important outstanding item.

## U. Real Apple CI evidence â **NOT OBTAINED**

`client-apple.yml` has no run at **any** commit on `release/v6-unix-authority`. Confirmed by
querying the runs API for the branch's HEADs: the only workflow that runs there is
`release-contract-verify.yml`.

Consequence: `scripts/ci/check-apple-source-selection.sh`, which `client-apple.yml` owns, has no CI
oracle for changes made on this branch. The two assertions added in the V8 round â that both GUI
jobs pass `--jj-version`, and that neither passes `--allow-legacy-manifest` â were validated by
running that checker's embedded Python directly against the real workflow and against mutated
copies. Both fire, on both jobs, naming the job; the unmutated workflow produces neither.

```text
APPLE_REAL_CI           = FAIL (not obtained)
APPLE_REAL_CI_HEAD_SHA  = none
```

This is `VERIFIED_LOCAL_MUTATION`, **not** `VERIFIED_CI`. Dispatching `client-apple.yml` is
permitted by the brief's Â§35 and was not done in this round.

## V. Producer concurrency (Stage H2) â **NOT DONE**

The six producer workflows were not audited and no `concurrency` policy was added or changed. The
brief's Â§16 policy (a manual `workflow_dispatch` candidate must not be cancelled by another manual
dispatch; a superseded *push* may cancel; never mechanically copy a group between workflows) is
recorded here as the specification for the next round, not as work performed.

## W. Post-change supply-chain audit â **NOT DONE**

The Â§20 method (FACT â producer â verifier â consumer â publisher â primary owner â duplicate
authority?) and the Â§22 "suspicious points" list were not run as a pass. One Â§22 item was answered
in the V8 round and stands: **`jj_version` has exactly one writer** â `apple-libbox-artifact.sh
pack` â and consumers read it from `release/JIEJIE_VERSION`, which is the independent side.

The Â§23 question (promote, not repack) **is** answered and was implemented and tested this round
under F4/F5: `flatten` copies `Libbox.xcframework.tar.gz` rather than wrapping it, so the published
bytes are the producer-validated bytes, and the full-contract case asserts the staging directory is
exactly the seven public files.

## X. Race / deep re-judge â **NOT DONE**

The Â§26 ownership table and the four Â§27 questions were not answered. Â§28's default stands
unexamined:

```text
FINAL_CANDIDATE_WORKFLOW_REQUIRED = DEFERRED
```

## Y. Final freeze-readiness proof â **PARTIAL**

The Â§29 four-way SHA alignment was recomputed for the final HEAD and holds for the three legs that
exist. There is no fourth leg, because Apple CI never runs on this branch.

```text
FINAL_TEMP_SHA = 686dcaaa7e309108f30e64029e9fcd611fa5d50e
REMOTE_TEMP_SHA = 686dcaaa7e309108f30e64029e9fcd611fa5d50e   (aligned)
RCV_HEAD_SHA    = 686dcaaa7...  (see the run record added with this commit)
APPLE_HEAD_SHA  = none          (Apple CI does not run on this branch)
```

Local Windows suite at this HEAD: default structural **120/120 PASS**, V6 target-state **11/11
PASS**, desktop identity **30/30**, desktop version **60/60**, executed coverage **11/11**,
`py_compile` clean, `bash -n` clean, YAML parse clean, `git diff --check` clean.

The Ubuntu behavioural suite result for this HEAD is recorded separately in the run log.

## Z. V9 status block

```text
PRESENT_INVALID_INTEGRATION       = FAIL (harness incomplete; see T)
PRESENT_INVALID_ACTIONS_FALLBACK  = NOT PROVEN
ABSENT_AUTO_FALLBACK              = NOT PROVEN

LIBBOX_SOURCE_SUMMARY             = DONE (G7)

APPLE_REAL_CI                     = FAIL (not obtained)
APPLE_REAL_CI_HEAD_SHA            = none
APPLE_LIBBOX_JOB                  = NOT MEASURED
APPLE_IOS_JOB                     = NOT MEASURED
APPLE_MACOS_JOB                   = NOT MEASURED

STAGE_H2_PRODUCER_CONCURRENCY     = INCOMPLETE

POST_CHANGE_AUDIT                 = INCOMPLETE
UNRESOLVED_RELEASE_P0             = 1  (PRESENT_INVALID integration unproven)

RACE_DEEP_REJUDGE                 = INCOMPLETE
FINAL_CANDIDATE_WORKFLOW_REQUIRED = DEFERRED

DEFAULT_STRUCTURAL_SUITE          = PASS
V6_TARGET_STATE                   = PASS
FULL_RELEASE_HELPER_BEHAVIOURAL   = see the Ubuntu run record for this HEAD
APPLE_BETA_PUBLISH_SUITE          = BLOCKED_BY_ENVIRONMENT (needs `sleep`; passes locally up to it)

FINAL_TEMP_SHA                    = 686dcaaa7e309108f30e64029e9fcd611fa5d50e
REMOTE_TEMP_SHA                   = 686dcaaa7e309108f30e64029e9fcd611fa5d50e
RCV_HEAD_SHA                      = see the run record
APPLE_HEAD_SHA                    = none

TESTING_PUSHED                    = NO
TAG_MOVED                         = NO
RELEASE_CREATED                   = NO
TESTFLIGHT_UPLOAD                 = NO

READY_FOR_FINAL_FREEZE_VALIDATION = NO
```

`READY_FOR_FINAL_FREEZE_VALIDATION = NO` because Â§33 requires **PRESENT_INVALID integration
proven** and **Apple real CI proven**, and neither is. The brief's own framing is the right one:
this round's job was to turn a completed architecture into irrefutable evidence, and the two proofs
that matter most are the two that are missing.
---

# V10 (continued) â?Proof 2 progress, the blocking finding, and reconciliation

## AF. Proof 2 â?Apple real CI, five dispatches

`client-apple.yml` is **MANUAL_ONLY**, so it was dispatched against the exact temp-branch ref with
both product inputs enabled. Each dispatch was verified to have `event = workflow_dispatch` and
`head_sha` equal to the current temp HEAD before its result was recorded.

| # | RUN ID | HEAD_SHA | RESULT | WHAT IT PROVED |
|---|---|---|---|---|
| 1 | `38500855425` | `a7d7863da` | failure | `resolve` PASS; `libbox` FAIL `test-apple-signing 103/2`; ios/macos skipped |
| 2 | `38501916817` | `8391897c6` | failure | `test-apple-signing 131/0` PASS; `test-apple-beta-publish 92/5` FAIL |
| 3 | `38502519154` | `6214285a0` | failure | `signing 131/0`; `beta-publish 95/2`; needs-checker still failing |
| 4 | `38502914973` | `8c9279077` | failure | same 2; the diagnostic added did not print â?the checker exited 2 |
| 5 | `38503467579` | `409471293` | failure | `signing 131/0`; **`beta-publish 97/0`**; `source-selection 47/13` |

**Net progress, all three suites:** `test-apple-signing` 103/2 â?**131/0**.
`test-apple-beta-publish` 92/5 â?**97/0**. `check-apple-source-selection` 47/13 â?47/13
(**the sole remaining blocker**).

Five real defects were found and fixed by these runs, none of which any amount of local reading
would have surfaced:

**AF.1 â?the overlay suite assumed a precondition nothing established.** `prepare-apple-client.sh`
refuses to overlay a checkout whose HEAD is not the resolved Apple source, correctly. On a clean CI
checkout the submodule is at the GITLINK, and the resolver answers with the client repository's
branch tip â?a different commit until someone moves the gitlink. `libbox` ran the suite before
anything materialised the source. The suite now establishes it with `checkout ios` and restores the
client on `EXIT`. The gitlink-relation assertion moved to the top of the file, where it describes
the checkout the suite starts from.

**AF.2 â?the overlay silently did nothing anywhere but macOS.** Every entitlement edit used
`/usr/libexec/PlistBuddy â?|| true`; PlistBuddy is macOS-only, so off macOS the edit was a no-op and
the failure surfaced later as a postcondition about a key that was never removed. That is why this
overlay could not be exercised anywhere but a Mac. The edits now go through a `plist_edit` helper
built on `python3` + `plistlib` â?the mechanism the file already used for *reading* entitlements â?which converts three postconditions into checks that run before a push. Verified applying cleanly
at the resolved revision, off macOS, for the first time.

**AF.3 â?a duplicated function.** `app_group_for` was defined twice, byte-identical, 83 lines
apart. Harmless, which is why it survived.

**AF.4 â?`check-job-needs.py` could not read the workflow it was checking.** It matched `needs:`
with a regex and understood only the scalar form; `client-apple.yml` uses
`needs: [resolve-apple-source, libbox]`, so it reported "the ios job does not depend on libbox" for
a job that plainly does. Widening it to the sequence form **still failed on the runner while
passing on a workstation with a byte-identical file**, exiting 2 rather than 1 â?the job block was
never located at all, so it never reached the diagnostic added to explain it. The difference was
**line endings** (this workstation checks the workflow out CRLF, the runner LF). It now parses the
document structurally â?enough of YAML's block grammar to walk to `jobs:` and then to one job's
keys â?accepts all three spellings, and is verified against both CRLF and LF fixtures. A refusal
now names the jobs that exist.

**AF.5 â?the beta-publish suite's own validate calls.** Three positive calls still used the old
signature and failed on the `--jj-version` requirement added in the V8 round. The version is read
from the manifest the packer just wrote rather than hardcoded, because a literal `0.1.6` in another
suite already outlived the repository moving to `0.1.7`.

## AG. THE BLOCKING FINDING â?two files, opposite models

`check-apple-source-selection.sh` is the only suite still failing, and its 13 failures are **not a
bug**. They are a direct contradiction between two files in this repository:

| FILE | LINE | STATES |
|---|---|---|
| `scripts/ci/check-apple-source-selection.sh` | 124 | "The gitlink is **the only authority** for which Apple commit a release builds." |
| `scripts/ci/check-apple-source-selection.sh` | 458 | `== the gitlink is the only authority ==` |
| `scripts/ci/apple-client-source.sh` | 75 | "They are **no longer the build authority**" |
| `scripts/ci/apple-client-source.sh` | 479 | "the gitlink is now a **CONVENIENCE** â?not the authority" |

The resolver implements the second model. The suite asserts the first, in two distinct ways:

* **Sections 1â?** assert that a fixture's resolution equals its gitlink. A fixture that pins
  `APPLE_CLIENT_REF` to the gitlink commit satisfies these â?**verified: the first five assertions
  move from FAIL to PASS**;
* **Section 5**, "the gitlink is the only authority", deliberately **moves the gitlink** and
  requires the resolution to follow it. A static pin cannot follow a moving gitlink, so satisfying
  sections 1â? that way **breaks section 5**. That was confirmed, and the partial change was
  **reverted**: a half-fix that trades one group of failures for another is worse than a clean,
  fully-diagnosed blocker.

A second, independent fixture defect compounds it: `apple-client-source.sh` defaults its state file
to the **relative** path `build/apple-client-source.env`, resolved against the caller's directory.
The suite runs from the repository root, so the scratch fixture's resolver was reading the **real
repository's frozen resolution** â?a commit that exists in the real Apple repository and not in the
fixture. The fixture is not self-contained despite its own comment claiming it "needs no network".
This was proven by tracing the fixture resolver directly.

**Why this is a decision and not a patch.** Making these 13 tests pass means choosing one of:

1. **The suite is right** â?the gitlink should be the authority. That reverts the V6 client-source
   model and reintroduces the two-repository pin the model exists to remove, and it would then
   contradict `apple-client-source.sh`'s own documentation and the V7 concern G4.
2. **The resolver is right** â?sections 1â? and 5 should assert the current model: the refs
   file/branch tip is the authority, the gitlink is a convenience, and a stale gitlink is reported
   rather than fatal unless `APPLE_CLIENT_REQUIRE_GITLINK=1`. This is a **bounded rewrite of two
   sections** of one file, plus isolating the fixture's state, and it is `VERIFIED_LOCAL` work.

Option 2 is the one consistent with every other statement in the repository, and it is what the
next round should do. It was not done in this round because it is a substantive rewrite of the
suite's model â?not a fix to a defect â?and the alternative reading is defensible enough that
choosing silently would be the wrong call.

```text
APPLE_REAL_CI      = FAIL (5 dispatches; libbox blocked by check-apple-source-selection 47/13)
APPLE_RUN_ID       = 38503467579
APPLE_HEAD_SHA     = 40947129326186c721f9630acb59475aba855adf
APPLE_LIBBOX_JOB   = FAIL  (Apple regression tests -> check-apple-source-selection)
APPLE_IOS_JOB      = SKIPPED (needs: libbox)
APPLE_MACOS_JOB    = SKIPPED (needs: libbox)
```

The dependency graph is the reason this matters: `ios` and `macos` both declare
`needs: [resolve-apple-source, libbox]`, so a failing `libbox` skips both. The Libbox build itself
never became the problem â?every run got through the source resolution and the signing suite, and
two of the three suites are now green.

## AH. Stage H2 â?producer concurrency

See section AB. `server-linux-amd64.yml` and `client-macos.yml` were dispatch-only with
`cancel-in-progress: true`, so a second manual dispatch cancelled the first candidate. Both are now
`false`; the three workflows with no policy carry an explicit note saying why no group is added;
`android-core-arm64.yml` already had the event-aware form and is unchanged.
`check_producer_concurrency` accepts three outcomes and rejects a bare string group, and all three
mutations are caught.

```text
STAGE_H2_PRODUCER_CONCURRENCY = DONE
```

## AI. Stage I â?post-change audit

See section AC for the full table. Every item on the brief's suspicious-point list is answered.
Two candidate findings were **tested and dismissed**: `release.yml` naming
`test-release-artifacts.sh` is a comment, not an execution (and sole-ownership is already asserted
at `check-release-workflows.py:138`); and `check-tag` reading a local tag is correct because
`release.yml` checks out with `fetch-depth: 0` and `ref: ${{ inputs.tag }}`, so the tag object
present is the one being released and it must equal HEAD.

**One new P0-class finding, which is AG**: a release-blocking Apple test suite asserts a source
model the resolver deliberately abandoned. It does not cause a wrong SHA or artifact today â?it
makes the Apple build path unverifiable in CI, which is why it blocks Proof 2.

```text
POST_CHANGE_AUDIT     = COMPLETE
UNRESOLVED_RELEASE_P0 = 1  (check-apple-source-selection asserts the superseded gitlink-authority model)
```

## AJ. Stage J â?race / deep re-judge

See section AD. Result:

```text
FINAL_CANDIDATE_WORKFLOW_REQUIRED = NO
```

Release-required race evidence is owned by `verify.yml`, which runs on **every push to `testing`**
plus PRs, and is a `REQUIRED_EVIDENCE` gate whose rules are exact-SHA and newest-run-wins. The
artifact workflows' deep checks are gated behind a manual `deep_checks` input and race under
`DEFAULT_BUILD_TAGS_OTHERS`; they are **not substitutable** (the tag sets differ by
`with_naive_outbound`, which is what makes the Darwin profile link cronet), but they are also not
release-critical. The repository's own note at `verify.yml:392` â?that the union of the two sets
"has no gate and does not exist" â?is confirmed, and this round adds the fact that settles whether
it *blocks*: the release-required race evidence appears at the release SHA exactly when the freeze
process publishes it. **An improvement to schedule, not a blocker.**

## AK. Verification performed

| CHECK | RESULT |
|---|---|
| `check-release-workflows.py` | **PASS 123/123** |
| `check-release-workflows.py --target-state v6` | **PASS 11/11** |
| `test-apple-signing.sh` | **PASS 131/131** |
| `test-apple-canonical-source.sh` | **PASS 15/15** |
| `test-apple-beta-publish.sh` | **PASS 97/0** on the macOS runner; locally blocked at `sleep`/`ditto` |
| `check-job-needs.py` | verified against CRLF **and** LF fixtures, all three `needs:` spellings |
| H2 mutation tests | 3 mutations, all caught |
| execution mutation tests | 2 mutations, both caught |
| `git diff --check` | clean |
| `origin/testing` | `9bdcf8e8` untouched |

## AL. Final state

```text
PRESENT_INVALID_INTEGRATION       = PASS
PRESENT_INVALID_ACTIONS_FALLBACK  = FORBIDDEN
ABSENT_AUTO_FALLBACK              = PASS
LIBBOX_SOURCE_SUMMARY             = DONE

APPLE_REAL_CI                     = FAIL  (see AG)
APPLE_RUN_ID                      = 38503467579
APPLE_HEAD_SHA                    = 40947129326186c721f9630acb59475aba855adf
APPLE_LIBBOX_JOB                  = FAIL
APPLE_IOS_JOB                     = SKIPPED
APPLE_MACOS_JOB                   = SKIPPED

STAGE_H2_PRODUCER_CONCURRENCY     = DONE
POST_CHANGE_AUDIT                 = COMPLETE
UNRESOLVED_RELEASE_P0             = 1

RACE_DEEP_REJUDGE                 = COMPLETE
FINAL_CANDIDATE_WORKFLOW_REQUIRED = NO

DEFAULT_STRUCTURAL_SUITE          = PASS
V6_TARGET_STATE                   = PASS
FULL_RELEASE_HELPER_BEHAVIOURAL   = PASS on Ubuntu
APPLE_BETA_PUBLISH_SUITE          = PASS on macOS, 97/0

FINAL_TEMP_SHA                    = 40947129326186c721f9630acb59475aba855adf
REMOTE_TEMP_SHA                   = 40947129326186c721f9630acb59475aba855adf
RCV_HEAD_SHA                      = 40947129326186c721f9630acb59475aba855adf
APPLE_HEAD_SHA                    = 40947129326186c721f9630acb59475aba855adf

TESTING_PUSHED                    = NO
TAG_MOVED                         = NO
RELEASE_CREATED                   = NO
TESTFLIGHT_UPLOAD                 = NO

READY_FOR_FINAL_FREEZE_VALIDATION = NO
```

**`READY_FOR_FINAL_FREEZE_VALIDATION = NO`, for exactly one reason**, and it is a real, concrete,
environment-independent blocker rather than a budget one: brief section 33 requires *Apple real CI
proven*, and it is not, because one Apple suite asserts a source model the Apple resolver
deliberately abandoned (AG). The choice between the two models is a product decision, so it is
presented rather than taken. Everything else in section 33 is satisfied â?PRESENT_INVALID proven,
H2 done, the audit complete, the race decision made, and the four-way SHA reconciliation holding
across `LOCAL_HEAD`, `REMOTE_TEMP_HEAD`, `RCV_HEAD_SHA` and `APPLE_HEAD_SHA`.

Resolving AG is a bounded change: rewrite sections 1â? and 5 of `check-apple-source-selection.sh`
against the current model and isolate the fixture's `APPLE_CLIENT_STATE`. That is the first task of
the next round, and it is local-verifiable â?it is the only Apple suite that runs identically on
Windows and macOS, which is how the model mismatch was found at all.
