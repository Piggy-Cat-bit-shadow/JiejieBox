# CI / Release 闂?Apple supply chain implementation report (V8)

Sole report for the V8 round. Continues from the green V7 baseline
(`9f43d3209864c555ba133faf6e1b2cb73be4c089` on `release/v6-unix-authority`). Does not rewrite the
earlier reports.

Entry state verified before any edit: local `HEAD` == temp-branch remote head == `9f43d320`, and
`origin/testing` still `9bdcf8e8`. `git status` clean apart from three pre-existing untracked
`KNOWLEDGE/modules/*.md`. Windows structural baseline re-run green: default **120/120**, V6
target-state **11/11**, desktop identity **30/30**, desktop version **60/60**, coverage guard
**11/11**, all workflows parse, `git diff --check` clean.

---

## A. What this round implemented

### A1. Stage F1/F2 闂?the Libbox manifest records identity and provenance as separate layers

The manifest was seven flat fields with one ambiguous name in it. It is now explicitly layered:

```text
IDENTITY      parent_sha, artifact_sha256, jj_version     a consumer must MATCH these
REVISION      version (JJ_ARTIFACT_VERSION)               the artifact's own label
PROVENANCE    workflow, run_id, run_attempt, go_version,
              gomobile_version, gomobile_source,
              xcode_version, build_time_utc, slices        recorded, never matched
```

**`jj_version` is new, and it exists because `version` cannot answer the question a release asks.**
`version` is `JJ_ARTIFACT_VERSION` = `0.1.7+jiejie.<short_sha>`, so `version == release/JIEJIE_VERSION`
is false on **every artifact ever built**. A check written that way is not approximately right; it
can only fail. This supersedes the earlier plan for that check, which the brief flagged as a trap.

**`slices` is discovered, not assumed.** The packer lists the xcframework's own slice directories
and then *requires* an iOS slice and a macOS slice to be present. Nothing hardcodes Xcode's slice
directory names 闂?a hardcoded list would be wrong the first time a platform is added. Recording
them turns "does this framework carry what the clients need" from something rediscovered at every
install into a property of the artifact.

**`run_id` is provenance and deliberately not identity.** A re-run of the same commit produces a
different run id for byte-identical output; binding to it would make the artifact irreproducible
by design.

### A2. Stage F3 闂?validation checks the declaration against the archive

`validate` now:

* requires `schema_version = 2` by default, with `--allow-legacy-manifest` as an explicit opt-out
  (never implied) for genuinely old artifacts and for the suite's legacy case. Inferring
  `jj_version` by chopping a suffix off `version` was rejected: it would make a release unable to
  distinguish *"this artifact declares the release version"* from *"this artifact's label happens
  to parse into something that looks like it"*;
* requires `jj_version` and matches it against `--jj-version`;
* **extracts the tarball and compares the declared slices to the real directory listing.** A
  declaration never compared to the bytes is decoration: a missing slice means a client cannot
  link, and an undeclared slice means the manifest is not describing what is about to be installed.

Both `client-apple.yml` GUI jobs now pass `--jj-version`, read from `release/JIEJIE_VERSION` rather
than from the manifest 闂?so one side of every comparison is an independent authority.

### A4. Stage F4/F5 闂?Apple became a release target, promoted rather than repacked

`client-apple.yml` joined `TARGET_WORKFLOWS` under `RELEASE_EXTRA_PLATFORMS`, with its own fields:

```text
kind         xcframework-manifest   the field that says this target is not a binary
binary       Libbox.xcframework     the DIRECTORY the payload holds
buildinfo    manifest.json
tarball      (empty)                it has no tag-derived name to compose
```

`kind` exists so the branch in generic code is on **what a target is**, not on its workflow name; a
second manifest-based target needs no new special case. It is declared for **every** target,
including the four whose value is the default 闂?see finding 6.

**Verification delegates to the producer.** `verify_apple` calls `apple-libbox-artifact.sh validate`
as a subprocess rather than reimplementing it. The release contributes the two facts only it knows
(the commit and the release version from the tag) and keeps ownership of the **file set** 闂?exactly
the bundle, its manifest, its checksum list and its build record, and nothing else. A release-side
copy of the schema rules would be a second owner of one fact, which is the failure this whole body
of work removes.

**Promotion, not repackaging.** `flatten` *copies* `Libbox.xcframework.tar.gz`. Repacking would
produce different bytes under the same name, so the file the release page offered would no longer be
the file whose digest the manifest records and that `verify` checked 闂?and `SHA256SUMS` would then
describe an artifact nobody validated.

---

## A5. What the Ubuntu oracle found, in order

Six real defects, none of which any amount of local reading would have surfaced:

1. **`--jj-version` was rejected by nothing and required by everything.** No 闂?this one was the
   plan; recorded here because it is the reason the field exists.
2. **`target_field` died on `kind` for four targets.** `FAIL: the release contract has no 'kind'
   for workflow 'server-linux-amd64.yml'`. The tempting fix 闂?defaulting an absent `kind` to
   `binary` 闂?would have put a special case in the shared lookup and silently defaulted the *next*
   forgotten field. The table was incomplete; the lookup was right.
3. **The suite aborted with no verdict** immediately after `download all five products`, killing
   every case after it. Same signature as the earlier duplicate-binary abort: a plan read. Replaced
   with filesystem discovery, exactly as before.
4. **`BUILD-INFO-LIBBOX.txt` was missing from the payload contract.** The two-sided message named
   it precisely. Narrowing the assertion to exclude it would have been the easy way to green and
   would have quietly dropped a published record.
5. **The failure text was truncated at 600 characters**, so `expected exit 0, got 1` was the whole
   report. Widened to 6000; this is what made the next two findings possible in one run each
   instead of three runs of guessing.
6. **The fixture hardcoded `0.1.6` while the repository says `0.1.7`** 闂?   `the Libbox artifact declares jj_version '0.1.7', but this release is '0.1.6'`. The Apple packer
   recorded the *real* version, the release compared it against the fixture's copy, and both the
   producer's validator and the release refused. The fixture now reads `release/JIEJIE_VERSION`, so
   it cannot drift from the thing it models.


`publish-apple-beta.sh` gains `--libbox-source {auto,release,actions}`, default `auto`.

The tag is resolved with `git ls-remote` (peeled if annotated, never the local ref) and required to
point at the commit being published. The release's assets are then **listed by exact name**, and
the outcome is one of three states:

```text
ABSENT           no asset with that exact name
PRESENT          the asset exists
PRESENT_INVALID  the asset exists and fails a check
```

`PRESENT` is not `VALID` until the unpacked artifact passes **the same validator, with the same
expectations**, that the Actions path uses. Every failure after `PRESENT` is **fatal**:
`canonical_state` is set before the download, so control cannot reach the fallback afterwards. A
canonical asset that is present but corrupt, mislabelled, or built from another commit means the
release's own Apple core is broken 闂?and falling back to Actions would publish something the
release says it is not.

Two details that are load-bearing rather than cosmetic:

* **Sections 3 and 4 are skipped entirely on the canonical path**, not merely tolerated. Section 4
  begins with `rm -rf "$input_dir"`, so running it would delete the validated artifact and
  substitute a different one 闂?the exact substitution this change exists to prevent.
* **The install path names `$validated`, not `$input_dir`.** The bytes proved and the bytes
  installed are the same bytes by construction, rather than by both happening to sit under one
  directory. Reading the manifest from one directory and installing from another is precisely the
  divergence `check-apple-shared-libbox.sh` exists to catch 闂?and it would catch it only after both
  clients had been built.

---

## B. Status against the brief

| STAGE | STATE | NOTE |
|---|---|---|
| F1 manifest schema | **DONE** | schema_version 2, layered identity/revision/provenance |
| F2 producer records slices + provenance | **DONE** | slices discovered then required |
| F3 validate checks declaration against archive | **DONE** | schema gate, jj_version, slice extraction |
| F4 Formal Release validates Apple Libbox | **DONE** | fifth target; `verify_apple` delegates to the producer's validator |
| F5 libbox asset in the release allowlist | **DONE** | `Libbox.xcframework.tar.gz`, promoted not repacked |
| G1 `--libbox-source` model | **DONE** | `auto`/`release`/`actions` |
| G2 `auto` prefers canonical, Actions only on ABSENT | **DONE** | |
| G3 `PRESENT_INVALID` fatal, never a fallback | **DONE** | single fatal path, state set before download |
| G4 `release` requires the asset | **DONE** | fatal on an unreadable release too |
| G5 `actions` unchanged / fail-closed | **DONE** | sections 3闂? untouched |
| G6 `--libbox-source actions` restores previous behaviour | **DONE** | by construction |
| G7 TestFlight source logging | **PARTIAL** | `libbox_from` is set; not yet printed in the summary block |
| H2 release-blocking producer concurrency | **NOT DONE** | |
| I post-change supply-chain audit | **NOT DONE** | |
| J race/deep final decision | **NOT DONE** | |
| K final-freeze readiness proof | **NOT DONE** | |

`READY_FOR_FINAL_FREEZE_VALIDATION = NO  (F1-F5, G1-G6 done and green on Ubuntu at 83f7258f3; G7, H2, I, J, K and the Stage G state-machine test outstanding)` 闂?F4/F5 (the release does not yet promote the canonical
Libbox), G7, H2, I, J, K are outstanding.

---

## C. Verification performed

Every command below ran in this environment. `git` is portable MinGit 2.50.1, `bash` is MinGit's
`sh.exe` (bash 5.2.37), `python` is the bundled 3.12.14.

| COMMAND | RESULT |
|---|---|
| `check-release-workflows.py` | **PASS 120/120** |
| `check-release-workflows.py --target-state v6` | **PASS 11/11** |
| `bash -n` on 16 release-path and Apple scripts | all parse |
| `yaml.safe_load` on all workflows | all parse |
| `test-desktop-source-identity.sh` | **PASS 30/30** |
| `test-desktop-release-version.py` | **PASS 60/60** |
| `git diff --check` | clean |
| `test-apple-beta-publish.sh` (partial, local) | passes up to a missing `sleep` stub; remainder needs the Ubuntu oracle |

**RUN on the GitHub Ubuntu oracle, current HEAD:** `38492804243` = **success**, the full 105-case
release-helper suite, including the four cases that exercise Apple as a release target:

```text
PASS  full contract: plan sees all five products
PASS  full contract: download all five products
PASS  full contract: verify five products and the installer
PASS  full contract: the staging directory is exactly the seven public files
PASS  full contract: the manifest names exactly the six public files
```

The 105 count is 103 behavioural cases plus the split assertions; earlier rounds reported 92 as the
suite grew. Six defects were found and fixed across seven oracle runs, all listed in 閹间精5.

**NOT RUN, and why:** the complete Apple suite (`test-apple-beta-publish.sh`) still needs `sleep`
and further Unix semantics this host lacks - it passes locally up to the first `sleep` and its
remainder is `BLOCKED_BY_ENVIRONMENT`. The new `PRESENT_INVALID` state machine in
`publish-apple-beta.sh` is `VERIFIED_SOURCE` (read and syntax-checked) but has **no test fixture**,
so it is not `VERIFIED_INTEGRATION`.

**What was NOT verified and must not be claimed:** the new manifest schema and the canonical-asset
state machine have **not been exercised end to end**. They are `VERIFIED_SOURCE` (read and
syntax-checked) but not `VERIFIED_INTEGRATION`. The Apple suite has no fixture for `PRESENT_INVALID`
yet, which is the single most important behaviour in Stage G.

---

## D. Findings recorded, not fixed

1. **`test-apple-beta-publish.sh` has no fixture for the new three-state model.** The producer
   changes are backwards compatible (`--allow-legacy-manifest` exists, `--jj-version` is only
   required when the schema is), so the existing 88 assertions should still pass 闂?but the new
   behaviour is untested. This is the highest-value next test.
2. **`check-apple-source-selection.sh` asserts the exact string
   `apple-libbox-artifact.sh validate build/apple-libbox`**, and the GUI jobs now pass an extra
   argument. The assertion is a substring check so it likely still matches, but it should be
   confirmed on Ubuntu rather than assumed.
3. **The canonical bundle name is derived, not declared.** `jiejie-libbox-apple-<jj>.tar.gz` is
   constructed in the consumer; the producer does not yet emit it. Stage F4 must make those two
   agree in one place rather than in two.
4. **`libbox_from` is set but never printed**, so a published run does not say which source it
   used. G7.

---

## E. Final state

```text
TEMP_BRANCH = release/v6-unix-authority
HEAD        = 83f7258f3f6f7150f7049ed03e2593aada28b1e0

Stage A-E            = DONE (unchanged, not reopened)
Stage F1/F2/F3/F4/F5 = DONE
Stage G1-G6          = DONE
Stage G7             = PARTIAL (libbox_from is set, not printed)
Stage H2/I/J/K       = NOT DONE

V6 target-state      = 11/11 PASS
default structural   = 120/120 PASS
105-case suite       = PASS on Ubuntu at 83f7258f3  (run 38492804243)

origin/testing       = 9bdcf8e8 (untouched)
tag                  = untouched
GitHub Release       = none
TestFlight upload    = none

READY_FOR_FINAL_FREEZE_VALIDATION = NO  (F1-F5, G1-G6 done and green on Ubuntu at 83f7258f3; G7, H2, I, J, K and the Stage G state-machine test outstanding)
```

The remaining work is F4/F5 (wire `apple-libbox` into `release-artifacts.sh` and the allowlist),
which is the step that actually makes the canonical asset part of a release; the tests for the
schema and the three-state model; G7's one line; H2; and then I/J/K.

---

## F. Mutation checks performed on the assertions added this round

A check that cannot fail is not a check, so the two new Apple assertions were mutation-tested
against copies of `client-apple.yml`, using the checker's own embedded Python extracted and run
directly (its owning workflow does not run on this branch, see the final note below):

| MUTATION | RESULT |
|---|---|
| `--jj-version "$jj"` deleted from both GUI jobs | `the ios/macos job does not validate Libbox against the release version` |
| `--allow-legacy-manifest` added to both GUI jobs | `the ios/macos job opts out of the schema-2 requirement...` |

Both fire, on both jobs, and name the job. The unmutated workflow produces neither.

## G. A gap this round exposed but did not close

`client-apple.yml` does not run on `release/v6-unix-authority` - no run exists for it at any commit
on this branch - so `scripts/ci/check-apple-source-selection.sh`, which it owns, has **no CI oracle
for changes made on this branch**. The two assertions above were therefore validated by running the
checker's Python directly against the real workflow and against mutated copies, which is
`VERIFIED_LOCAL_MUTATION`, not `VERIFIED_CI`.

This is recorded rather than worked around. Dispatching the workflow is out of scope for this
round, and the honest statement is that the assertion logic is proven and its CI wiring is not.
