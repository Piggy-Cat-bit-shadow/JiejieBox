# APPLE_TF_PIPELINE_REFACTOR_REPORT

```text
APPLE_PIPELINE_REFACTOR = PARTIAL
RELEASE_FREEZE = KEEP

FORMAL_RELEASE_CREATED = NO
TAG_MOVED = NO
RELEASE_ASSETS_PUBLISHED = NO
NO_APP_STORE_CONNECT_UPLOAD = YES   (nothing was uploaded; no archive was built)
```

Scope honesty up front: the **source-authority refactor (Phase 2) is implemented**, the **iOS
DerivedData double-resolve is fixed**, and the audit of the rest is complete with evidence. The
**contract-test rewrite (Phase 3/§35), the libbox-only mode (Phase 4) and the upload decoupling
(Phase 6) are NOT DONE** — §F says exactly why for each, and none of it is guesswork.

---

## A. Source model

```text
OLD_AUTHORITY        clients/apple gitlink, read with `git ls-tree HEAD clients/apple`
NEW_AUTHORITY        the Apple client repository (APPLE_CLIENT_REPOSITORY), resolved to an exact SHA
DEFAULT_REF          refs/heads/$APPLE_CLIENT_BRANCH  (main)
RESOLVED_APPLE_SHA   frozen per build in build/apple-client-source.env (gitignored)
OVERRIDE_BEHAVIOR    APPLE_CLIENT_REF (or APPLE_CLIENT_SHA) = 40-hex SHA | tag | branch
SUBMODULE_ROLE_AFTER still present, still works: local default checkout, developer convenience,
                     history. NOT the build authority. APPLE_CLIENT_REQUIRE_GITLINK=1 restores
                     the old hard equality.
```

### Why the old model made the pin manual

`apple_sha()` read `git ls-tree HEAD clients/apple`, and `assert-shared-source` refused unless
`resolved == gitlink`. A build could therefore only ever MIRROR a decision someone had already made
by hand in a second repository: commit the UI repo, come back to core, move the gitlink, commit,
push, pull. That is the loop §4 describes, and no amount of CI polish removes it while the gitlink is
the authority.

### What changed in `scripts/ci/apple-client-source.sh`

| concern | before | after |
|---|---|---|
| SHA source | gitlink | `resolve_sha()`: override, else frozen state, else branch tip |
| resolve count | every subcommand re-read the gitlink | **once**; `resolve` writes the state file, everyone else reads it |
| branch tip | never read | `ls_remote_sha()` asks the remote for exactly one ref, preferring the peeled `^{}` line so a TAG resolves to its commit |
| gitlink equality | hard failure | reported as a note, or hard failure under `APPLE_CLIENT_REQUIRE_GITLINK=1` |
| checkout | failed if the tree was not already at the gitlink | fetches the resolved commit and moves the tree |
| new action | - | `ls-remote` (resolve the default revision, print it, freeze nothing) |

**Resolve-once is the correctness boundary, not a performance tweak.** Without it iOS can resolve A,
someone can push B, and macOS can resolve B — one release, two UI commits, which is §5.1's forbidden
state. `resolve_sha()` is the single resolution point and the freeze, and `show()` calls it exactly
once so its own output cannot disagree with itself across a concurrent push.

### Authority annotated, not silently changed

`release/apple-client-refs.env` documents the new authority and the override. Its existing invariant
is **preserved**: the suite asserts the file contains no `*_SHA=` assignment, and it still does not —
the SHA lives in the resolved state file, not back in the refs file, so "a second copy of the commit
that can disagree with the authority" is still impossible.

---

## B. Before / after TF graph

```text
BEFORE (verified in the audit)
  publish-apple-beta.sh
    resolve + assert-shared-source            (publish:74, 86)
    dispatch client-apple.yml, WAIT for the WHOLE run
    require libbox + ios + macos all success   (apple-ci-job-gate.py:34)
      libbox   -> Libbox artifact      <-- the only thing downloaded (publish:248)
      ios      -> unsigned iOS app     <-- not consumed by TF
      macos    -> unsigned macOS app   <-- not consumed by TF
    download Libbox
    release-apple.sh testflight
      resolve + assert-shared-source again     (release:176, 177)
      checkout ios + checkout macos again      (release:187, 189)
      install Libbox into the macOS client again (release:238)
      check-apple-shared-libbox.sh again       (release:251)
      iOS archive   -> verify -> export+upload   (release:318)
      macOS archive -> verify -> export+upload   (release:320)

AFTER (target; only the source/DD parts are implemented)
  publish-apple-beta.sh
    resolve APPLE_CLIENT_SHA ONCE and freeze it
    require libbox ONLY                          <-- NOT DONE, §F-2
    download Libbox
    release-apple.sh testflight
      iOS archive (fresh DD shared with its resolve)   <-- DONE
      macOS archive                                    <-- unchanged, deliberately
      uploads                                            <-- NOT parallelised, §F-3
```

---

## C. Build count

```text
OLD                                        per core SHA / per release
  Libbox (CI)                                    1
  unsigned iOS app (CI)                          1
  unsigned macOS app (CI)                        1
  iOS TF archive (local)                         1
  macOS TF archive (local)                       1
  -------------------------------------------------
  total                                          5

NEW (target)
  Libbox (CI)                                    1
  iOS TF archive (local)                         1
  macOS TF archive (local)                       1
  -------------------------------------------------
  total                                          3
```

`unsigned iOS/macOS for TF preflight = 0` is **NOT MET** — it needs §F-2.

---

## D. Timing

**No measurement is claimed.** Nothing here was executed on macOS: this host cannot run
`xcodebuild`, and §37 requires a real archive for the DerivedData claim rather than shell unit tests.
The required series (`SOURCE_RESOLVE_TIME`, `LIBBOX_WAIT_TIME`, `SWIFTPM_RESOLVE_IOS`,
`IOS_ARCHIVE_TIME`, `IOS_UPLOAD_TIME`, `SWIFTPM_RESOLVE_MACOS`, `MACOS_ARCHIVE_TIME`,
`MACOS_UPLOAD_TIME`, `TOTAL_WALL_CLOCK`) is therefore **NOT COLLECTED**, and §31's "no data, no
claim" is respected by writing none.

---

## E. Provenance

**NOT YET WIRED** (§F-1). The pieces the new model needs to record are now available — `resolve`
emits `APPLE_CLIENT_SHA`, `APPLE_CLIENT_REPOSITORY`, `APPLE_CLIENT_BRANCH`, `APPLE_CLIENT_REF` and
`APPLE_CLIENT_STATE`, and the frozen state file carries the first three — but no builder or summary
consumes them yet, so §7's binary identity is **not** implemented and no sample is shown.

`LIBBOX_PARENT_SHA` and `APPLE_CLIENT_SHA` remain separate concepts exactly as §21 requires: Libbox
comes from the core SHA, the Swift UI from the Apple client SHA.

---

## F. Newly discovered optimizations

Each was checked against §24 (same class, evidenced, no behaviour change, no safety loss, no scope
creep). "DONE" means implemented on this host; "RECORDED ONLY" means it needs a Mac or a bigger
change, with the reason.

### F-0. Three NON-TEST gitlink gates would have blocked the first real build — **DONE**

Switching the authority is not enough on its own: three places outside the test suite still
hard-asserted `gitlink == resolved`, and each would have failed the first build before any gate ran.

```text
publish-apple-beta.sh:82-84    [ "$gitlink_sha" = "$IOS_APPLE_SHA" ] || fail
client-apple.yml:136-147       libbox job: actual != recorded  -> exit 1
client-apple.yml:250-268       iOS job: actual != $APPLE_CLIENT_SHA -> exit 1
```

All three now compare the checkout against the **resolved** source, which is the guarantee that
actually matters — the tree that gets built is the tree that was selected — and report the gitlink
difference instead of failing on it. `APPLE_CLIENT_REQUIRE_GITLINK=1` restores the hard equality in
all three. `gitlink_sha` is still read in `publish-apple-beta.sh`, because the publish contract test
greps for it and because a submodule far behind the resolved source is worth printing.

For the libbox job, whose tree is the one `build-apple-libbox.sh` compiles from, the gate also
**moves** the submodule to the resolved commit via `checkout ios` when it is somewhere else. Without
that, the job would have verified one commit and built another.

### F-1. `publish-apple-beta.sh` and `release-apple.sh` repeat each other's source work — RECORDED ONLY

One publish resolves the source and asserts the shared source **twice**, checks out both trees
**twice**, installs Libbox into the macOS client **twice**, and runs `check-apple-shared-libbox.sh`
**twice**:

```text
resolve + assert-shared-source   publish:74,86      release:176,177
checkout ios / checkout macos    publish:269,271    release:187,189
Libbox install into macOS client publish:276        release:238
check-apple-shared-libbox.sh     publish:277        release:251
```

The freeze from §A already removes the *resolution* duplicate (the second call now reads the state
file instead of asking the remote), so part of this is fixed as a side effect. The rest is
checkout/install duplication that needs the two scripts' responsibilities redrawn — a change to their
interface, not a bug fix. RECORDED ONLY.

### F-2. The publish requires unsigned iOS/macOS that it never consumes — RECORDED ONLY

`apple-ci-job-gate.py:34` hard-codes `REQUIRED_JOBS = ("libbox", "ios", "macos")`, and its `main()`
reads stdin only — there is no argument parsing and no subset support. The publish downloads only
`jiejiebox-libbox-$local_sha` (`publish:248`) yet waits for all three jobs.

Fixing it means: a mode argument on the gate, a `libbox-only` input on `client-apple.yml` with the
`ios`/`macos` jobs conditioned on it, and `publish-apple-beta.sh` dispatching that mode. That is a
**CI behaviour change that must be exercised by a real dispatch to be worth anything** (§37: "must
prove TF publish dispatches Libbox-only, does not build unsigned IPA, does not build unsigned DMG").
It is implementable here but not *verifiable* here, and shipping an unverified change to the
release-gating job set is exactly the kind of thing this brief's §29 warns against. RECORDED ONLY.

Also recorded: the dispatch at `publish:184` passes **no** `-f build_ios/-f build_macos`, relying on
the workflow defaults, while its own failure message (`:230-231`) tells the operator to pass both.
That inconsistency should be resolved in the same change.

### F-3. The two archives are fully serialized — RECORDED ONLY, deliberately

`release-apple.sh:318` then `:320`, two child processes, no `&`, no `wait`, no `coproc`. They cannot
overlap, so two independent `xcodebuild archive` runs cost their sum.

**Not parallelised on purpose.** §17 forbids it without measurement: on a resource-limited Mac it
risks CPU contention, RAM pressure, swap and thermal throttling, and "wall-clock dropped" cannot be
demonstrated from this host. RECORDED ONLY, with the measurement that would license it.

### F-4. iOS resolved the package graph and then deleted it — **DONE**

`build-ios-testflight.sh` resolved into `$work/dd` (ios:103-106), deleted that whole tree
(ios:122 — `SourcePackages` checkouts, the resolved graph, the build-settings cache), and re-archived
with the same `-derivedDataPath "$work/dd"` (ios:129), so Xcode re-resolved inline. The dedicated
resolve step — whose entire purpose is to survive a transient network failure — was discarded, and
its own retry loop was not fail-closed.

Fixed as §13 prescribes, not by "just not deleting it": one **fresh unique** DerivedData per run,
used by both steps.

```text
derived_data="$work/dd-<build>-<core-sha>-<apple-sha>-$$"
rm -rf "$derived_data"; mkdir -p "$derived_data"
resolve -> "$derived_data"
archive -> the SAME "$derived_data"
```

The stale-signing-identity cause is addressed by construction: a directory deleted-if-it-exists and
named per run cannot be a previous run's cache, so the archive never reads the development-identity
cache the old comment describes, and this run's package graph survives. The retry loop now **fails
closed** after three attempts with a message saying it is a network problem, not a project one.

**§13.1's A/B is NOT RUN.** It requires a real `xcodebuild archive` on macOS and a check that the
build still requests an Apple Distribution profile rather than a Development one. Until that is run,
this fix is `VERIFIED_SOURCE` only, and it must not be read as a signing-behaviour proof.

### F-5. macOS DerivedData must stay separate — CONFIRMED, nothing changed

`build-macos-testflight.sh` mutates `$work/dd/SourcePackages/checkouts/.../Package.swift` to strip
the SwiftLint plug-in (macos:97-107) and deliberately keeps that directory (macos:114-117). It is
**mutable state**, so §14's rule holds and the two platforms keep separate directories. No sharing
was introduced. The fixed-name `$work/dd` was left alone here on purpose: its contents are mutated
in place, so changing its naming would change what a re-run reuses, and that is not this task.

### F-6. Two jobs clone the Apple source, one clone each — RECORDED ONLY

`client-apple.yml:91` (libbox job) and `:214` (iOS job) each check out `submodules: recursive`, so
the Apple submodule and its nested Runestone are cloned twice with no shared object store between
jobs; the macOS job (`:472`) checks out with `submodules: false` and gets its tree from
`apple-client-source.sh checkout macos`, which is the one path that already shares an object store
(`--shared` clone plus `objects/info/alternates`).

GitHub-hosted jobs cannot share a working tree, so closing this means an artifact or cache handoff
between jobs — a new failure mode for a saving that is not a release blocker. RECORDED ONLY. §25's
"one fetch, two worktrees" is already satisfied *within* a job and cannot be extended *across* jobs
without that handoff.

### F-7. Nested submodules follow the resolved commit — CONFIRMED

Both checkouts run `submodule update --init --recursive` **after** detaching to the resolved SHA
(ios path and macOS path), so `Frameworks/Runestone` comes from the revision the resolved client
records rather than from its own default branch. §26 holds. No change was needed.

### F-8. `--archive-only` exists; archive/upload are not separable — RECORDED ONLY

Both builders parse `--archive-only` (ios:29-35, macos:31-37) and exit before export (ios:231-234,
macos:244-247), but **no** `--upload-existing-archive` exists: `archive_path` is derived internally
and unconditionally `rm -rf`'d before rebuild (ios:114, macos:117), so an already-built archive
cannot be re-uploaded by any flag. §19's `archive != upload` split needs the export/upload half
extracted, which is a real change to both builders plus `release-apple.sh` — and its whole point is
parallel uploads, which §37 says must be verified with a mock uploader rather than a real App Store
Connect publish. RECORDED ONLY.

### F-9. A THIRD copy of the Apple pin exists, unwired — RECORDED ONLY

`check-libbox-abi.sh:155` carries `VERIFIED_APPLE_SHA="40dd48795579718008518ac51af320b1d61377ac"` and
`:477` compares it against the gitlink. That is a second, independent copy of the Apple revision —
exactly the "second value that can disagree with the authority" the refs-file rule exists to prevent
— and it has **no caller anywhere in the repository**, so it cannot fail a build. It will mislead the
next reader who greps for the pin, though. RECORDED ONLY: deciding whether to re-point it at the
resolved SHA or delete the check is a separate judgement, and `check-libbox-abi.sh` is outside this
task's file list.

---

## G. Verification

```text
gofmt / Go tests                     NOT APPLICABLE   no Go changed
dash -n apple-client-source.sh        PASS
dash -n build-ios-testflight.sh       PASS
dash -n check-apple-source-selection.sh  PASS
dash -n publish-apple-beta.sh         NOT POSSIBLE (pre-existing bash-ism, see below)
YAML parse client-apple.yml           PASS (jobs: libbox, ios, macos)
check-release-workflows.py            80 passed, 0 failed
resolver action list                  matches the suite's pinned list (updated for ls-remote)
refs-file "_SHA= is forbidden" rule   still satisfied (the SHA lives in the resolved state file)
real xcodebuild archive               BLOCKED_BY_ENVIRONMENT - no macOS, no Xcode
App Store Connect upload              NOT PERFORMED (forbidden this round)
```

`publish-apple-beta.sh` cannot be shell-checked here and that is **pre-existing**, not a result of
this change: line 204 uses the bash here-string `read -r a b c <<<"$(...)"`, which `dash -n` rejects
outright. The repository's own parser gate is `bash -n` inside `test-apple-signing.sh`, which is a
bash script this host cannot run either. The edited region was reviewed by eye and uses only
`[`/`if`/`fi`/`echo`, which are POSIX.

**`check-apple-source-selection.sh` will FAIL as it stands.** It is a ~626-line suite, 65 runtime
checks, whose fixtures are built on gitlink semantics — it creates a bare git repo, commits an Apple
gitlink into a fake parent, and asserts at roughly 22 points that the resolved source **is** that
gitlink, with the branch tip deliberately one commit ahead so the tip answer fails by construction
(`:362-367`, `:371`, `:385-386`, `:415-416`, `:428-431`, `:442`, `:455-459`, `:463`, `:476-480`,
`:558-566`, plus the fixture premise at `:333-336`). Those assertions now describe the model this
task removed. I updated the one assertion that is a pure interface pin (the action list) and left the
rest **unchanged rather than half-rewritten**, because a partially converted suite that still passed
would hide exactly the behaviour change it exists to catch. Rewriting it is §35 and is the next
concrete step; §F-1 and §F-2 must land with it.

Reusable parts of that suite, for whoever does the rewrite: its scratch/`mktemp` plumbing, its
local-git fixtures and its `check`/`expects_fail` helpers need no change; only the fixtures'
premise and the authority assertions do. Note also that `test-apple-beta-publish.sh` (97 checks)
**mutates `clients/apple` while it runs** — it appends to `Extension/Info.plist` and restores it — so
do not run it when the tree must stay untouched.

---

## H. §39 success criteria

```text
 1 Apple default source = UI repo main latest ............ MET
 2 one resolve per run ................................... MET
 3 iOS/macOS always the same exact SHA ................... MET
 4 explicit SHA override reproduces an old build ......... MET (APPLE_CLIENT_REF; unexercised)
 5 parent gitlink is no longer the forced authority ...... MET
 6 submodule not removed ................................. MET (kept, documented, opt-in check)
 7 provenance records core SHA + UI SHA .................. NOT MET (F-1)
 8 TF publish no longer requires unsigned iOS/macOS ...... NOT MET (F-2)
 9 missing Libbox triggers Libbox-only ................... NOT MET (F-2)
10 Libbox not recompiled on a UI update .................. MET by construction (Libbox keys on
                                                            the core SHA; the UI SHA is not an input)
11 iOS fresh DD shared between resolve and archive ....... MET (source-verified; A/B NOT RUN)
12 macOS mutable DD does not pollute iOS ................. MET (separate dirs; nothing shared)
13 upload separable from archive ......................... NOT MET (F-8)
14 no signing / entitlement / App Store gate weakened .... MET (no gate touched)
15 no scope creep to product logic or other platforms .... MET (Apple CI/scripts only)
```

8 of 15 met. §2 requires `READY_FOR_MAINTAINER_REVIEW` only on completion, so:

```text
APPLE_PIPELINE_REFACTOR = PARTIAL
RELEASE_FREEZE = KEEP
```

No tag was created or moved, no Release was created, no asset was published, and **nothing was
uploaded to App Store Connect**.
