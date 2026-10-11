# Apple Parent Finalize Report

Parent-side integration of the already-consolidated Apple client, on an isolated branch.
Apple repository was **read-only** throughout this round.

---

## 1. Executive Summary

| | |
|---|---|
| **Parent branch** | `jiejiebox/apple-finalize` |
| **Parent branch remote** | `origin/jiejiebox/apple-finalize` |
| **Apple canonical branch** | `main` |
| **Apple main SHA** | `bb5309b4a6c5b06382e1277a6e7ac0fbeadc9a72` |
| **Parent gitlink** | `bb5309b4a6c5b06382e1277a6e7ac0fbeadc9a72` |
| **MATCH** | **YES** |
| **Push** | `jiejiebox/apple-finalize` pushed; `testing` untouched and not pushed |

The Apple `main` SHA is **not** hardcoded anywhere in the process that produced this report. It
was resolved at integration time:

```bash
git ls-remote https://github.com/Piggy-Cat-bit-shadow/sing-box-for-apple.git refs/heads/main
```

> **The commands are the authority, not the numbers above.** A SHA written into a document is a
> snapshot; a SHA read from the repository is the fact. To re-establish the pinned revision:
>
> ```bash
> git ls-tree HEAD clients/apple                          # the pin
> scripts/ci/apple-client-source.sh sha ios               # must equal it
> scripts/ci/apple-client-source.sh sha macos             # must equal it
> scripts/ci/apple-client-source.sh assert-shared-source  # must PASS
> grep '^VERIFIED_APPLE_SHA=' scripts/ci/check-libbox-abi.sh   # must equal it
> ```

The parent branch tip quoted here is the tip **at verification time**. This report is itself a
commit on that branch, so a later commit will move the tip; the dependency chain is
`Apple main → parent gitlink → VERIFIED_APPLE_SHA`, and only the four commands above can be
trusted to answer it. This round deliberately did not enter the "rewrite the report to chase its
own SHA" loop.

---

## 2. Scope

```text
Apple parent integration only          done
Core hardening untouched               done - no core branch read, merged or modified
Other parent branches untouched        done - only a new worktree and a new branch were created
Apple repository untouched             done - no write, no push, no tag move, no history change
```

Explicitly **not** done this round: no Apple commit history work, no Hako UI change, no iPad/macOS
UI ownership change, no core hardening merge, no rebase or force-push of `testing`, no new
features, no repository-wide refactor.

### Device testing is not available, and nothing here claims otherwise

**Mac and iPad real-device testing is unavailable**, and iPad/macOS have no simulator path either
for the purposes of this project's acceptance. No result in this report is a device result:

```text
iPhone / iPad / Mac device acceptance   NOT RUN - unavailable
NetworkExtension / Libbox device I/O    NOT RUN - needs signing and a device
iPad / macOS runtime behaviour          NOT RUN - unavailable
```

Everything reported here is a **build, static, or gate** result:

```text
SFI/SFM compile          build result          (re-used from the Apple round; no Apple file changed here)
layer 1-2 Go declaration static scan
layer 4 Apple sweep      static scan of 666 Swift sources
layer 5 client gitlink   git ref comparison
source selection         pure resolver + scratch-repo fixtures
```

The one thing that *would* have been a runtime result — whether the device pause latches, whether
the Hako shell renders as designed, whether the Hako visual target is met — is `NEEDS_DEVICE` and
is out of reach on this host. It must not be inferred from a green build, and this report does not
infer it.

---

## 3. Before / After

```text
origin/testing old pin:            clients/apple = 2a189686ae382d860f643ec53b64cbdb6cca562e
finalize branch new pin:           clients/apple = bb5309b4a6c5b06382e1277a6e7ac0fbeadc9a72

origin/testing APPLE_CLIENT_BRANCH:   jiejiebox/integrated   (branch no longer exists)
finalize branch APPLE_CLIENT_BRANCH:  main

origin/testing VERIFIED_APPLE_SHA:    2b23330d489b9f6b45e98f903f458842e8961594  (unrelated to the pin)
finalize branch VERIFIED_APPLE_SHA:   bb5309b4a6c5b06382e1277a6e7ac0fbeadc9a72  (== the pin)
```

`origin/testing` was **entirely un-integrated**, not partially: it carried the pre-consolidation
pin, a branch name that has been deleted, `IOS_APPLE_BRANCH=hako-ui` (also deleted), and an ABI
constant naming a revision that was neither the gitlink nor on the product line. All three had to
be built fresh rather than inherited.

### The two local-only `testing` commits, classified

`git log --oneline origin/testing..testing` returned two commits. Both are Apple-scoped; neither
was inherited, per §5.

| Commit | Scope | Classification | Disposition |
|---|---|---|---|
| `837b14923` | `scripts/ci/build-apple-libbox.sh`, `scripts/ci/check-libbox-abi.sh` | `APPLE_PARENT_INTEGRATION` (build helper) | **Not carried** — see §9 |
| `9f9eea55d` | `clients/apple`, `release/apple-client-refs.env`, `scripts/ci/check-libbox-abi.sh` | `APPLE_PARENT_INTEGRATION` (pin + metadata) | **Rebuilt fresh** on this branch |

---

## 4. Files Changed

One commit, three paths — exactly the set §18 names.

| File | Change |
|---|---|
| `clients/apple` | gitlink `2a189686ae38` → `bb5309b4a6c5b06382e1277a6e7ac0fbeadc9a72` |
| `release/apple-client-refs.env` | `APPLE_CLIENT_BRANCH=jiejiebox/integrated` → `main`; `IOS_APPLE_BRANCH=hako-ui` **deleted** and replaced by `HAKO_DESIGN_BASELINE_TAG=archive/hako-design-v1` |
| `scripts/ci/check-libbox-abi.sh` | `VERIFIED_APPLE_SHA` `2b23330d489b` → `bb5309b4a6c5b06382e1277a6e7ac0fbeadc9a72`; the stale comment block above it rewritten |

```text
 clients/apple                  |  2 +-
 release/apple-client-refs.env  | 18 ++++++++++++++----
 scripts/ci/check-libbox-abi.sh | 18 ++++++++++--------
 3 files changed, 25 insertions(+), 13 deletions(-)
```

### Why `IOS_APPLE_BRANCH` was deleted rather than repointed

It was already **unread**. `platform_branch` in `apple-client-source.sh` answers with
`APPLE_CLIENT_BRANCH` for both platforms and explicitly documents that it does *not* fall back to
`IOS_APPLE_BRANCH`, because that name records a pre-merge historical branch. So removing it
changes no behaviour, while keeping it would leave release metadata naming a branch that no longer
exists. The lineage it recorded is preserved as a tag, which cannot be deleted out from under a
release the way a branch can.

### Why the ABI constant had to move in the same commit

Layer 5 of `check-libbox-abi.sh` asserts `git ls-tree HEAD clients/apple` equals
`VERIFIED_APPLE_SHA`. Moving the gitlink alone would have turned that layer red and read as a
regression introduced by this round, rather than as the stale constant it was. The constant is a
statement about which revision was verified, not a verification of it — layer 4 is the
verification, and §6 records that it ran.

### Confirmed absent from the commit (§19)

`core protocol implementation`, `sniff`, `route`, `device-axis Go logic`, `Android`, `Windows`,
`unrelated docs`, `other submodules`. `clients/android` and `clients/desktop` are untouched, and
their submodules are not initialised in this worktree so they cannot have been staged by accident.

---

## 5. Source-selection Verification

```text
sha ios:                bb5309b4a6c5b06382e1277a6e7ac0fbeadc9a72
sha macos:              bb5309b4a6c5b06382e1277a6e7ac0fbeadc9a72
assert-shared-source:   PASS: iOS and macOS share Apple source bb5309b4a6c5b06382e1277a6e7ac0fbeadc9a72
```

Both equal `FINAL_APPLE_SHA`, and `assert-shared-source` passes, so the model is still

```text
ONE repository   ONE source SHA   TWO products
iOS  -> parent clients/apple gitlink
macOS -> the same gitlink
```

No per-platform Apple SHA was reintroduced. `resolve` confirms it:

```text
APPLE_CLIENT_BRANCH=main
APPLE_CLIENT_SHA=bb5309b4a6c5b06382e1277a6e7ac0fbeadc9a72
IOS_APPLE_BRANCH=main      IOS_APPLE_SHA=bb5309b4a6c5b06382e1277a6e7ac0fbeadc9a72
MACOS_APPLE_BRANCH=main    MACOS_APPLE_SHA=bb5309b4a6c5b06382e1277a6e7ac0fbeadc9a72
```

`apple-client-source.sh verify ios` → `PASS: ios Apple source clients/apple @ bb5309b4a6c5…`.
`verify macos` → `build/apple-client-macos is not a git checkout`, which is the expected answer
when the macOS working tree has not been materialised; it is created by `checkout macos` at build
time, not by this pin gate.

---

## 6. ABI Verification

Run: `scripts/ci/check-libbox-abi.sh` (no `--skip-client`, so layer 4 attempts the caller sweep).

| Layer | Result |
|---|---|
| 1–2 Go declaration | **PASS** — all 9 contract methods bound with a non-pointer-bearing result |
| 3 generated Java binding | **SKIP** — `gobind` not on PATH (host limitation, not a failure) |
| 4 client call sites — **Apple** | **PASS** — client found at `clients/apple`, **666 Swift sources scanned, 0 violations** |
| 4 client call sites — **Android** | **SKIP** — no Android checkout (`clients/android` uninitialised) |
| 5 client gitlink — android | **PASS** (`ec61030d3df7`) |
| 5 client gitlink — apple | **PASS** (`bb5309b4a6c5`) |
| Overall | `PARTIAL` — because a layer was SKIPPED, which is deliberately not reported as PASS |

### Apple-owned vs Android pre-existing

```text
Apple-owned failure:              NONE
Android pre-existing failure:     0 failures; 2 layers SKIPPED (checkout absent)
```

The gate's own rule is that "I could not look" must not read the same as "I looked and it was
fine", so a run with an uninitialised client exits `PARTIAL` and not `PASS`. That is reported as
`SKIP_WITH_REASON` rather than smoothed over. **Android was not modified to make this gate
greener**, per §14.

Note that the Apple call-site sweep genuinely ran and genuinely passed against the newly pinned
revision — this is the layer that is *not* satisfied by "the AAR built", which is the defect the
gate exists for.

---

## 7. Build / Regression Gate

| Check | Status |
|---|---|
| `apple-client-source.sh sha ios` == FINAL_APPLE_SHA | **PASS** |
| `apple-client-source.sh sha macos` == FINAL_APPLE_SHA | **PASS** |
| `apple-client-source.sh assert-shared-source` | **PASS** |
| `apple-client-source.sh resolve` (one SHA, both platforms) | **PASS** |
| `apple-client-source.sh verify ios` | **PASS** |
| `check-apple-source-selection.sh` | **PASS — 42 passed, 0 failed** |
| `check-libbox-abi.sh` layers 1–2 | **PASS** |
| `check-libbox-abi.sh` layer 4 Apple sweep (666 sources) | **PASS** |
| `check-libbox-abi.sh` layer 5 both clients | **PASS** |
| `check-libbox-abi.sh` layer 3 (`gobind` absent) | **SKIP_WITH_REASON** |
| `check-libbox-abi.sh` layer 4 Android (checkout absent) | **SKIP_WITH_REASON** |
| `check-apple-shared-libbox.sh` | **SKIP_WITH_REASON** — needs an *installed* `Libbox.xcframework` in each client, which exists only after a Libbox build; the check asserts the framework is present, and no framework has been built in this worktree |
| `apple-ci-job-gate.py` | **SKIP_WITH_REASON** — consumes CI job/run data on stdin; no run data available outside CI |
| SFI (iOS) / SFM (macOS) compile smoke | **PASS, re-used rather than re-run** — see below |

### On the compile smoke

Both products were built successfully from the **same Apple commit this pin names**
(`bb5309b4a6c5…`: `SFI` generic iOS → `** BUILD SUCCEEDED **`, `SFM` generic macOS →
`** BUILD SUCCEEDED **`) during the preceding Apple consolidation round, on this host. §16 permits
scoping this round to the parent integration gate when the Apple tree has just been built on the
same machine, and §17 forbids re-running the full Apple UI acceptance. This round therefore did
not rebuild.

The compile smoke is a property of the Apple tree, and since this round changed **no** Apple file,
that result still holds for this pin. `check-apple-source-selection.sh` independently confirms
both platforms resolve to that one commit, including that a moving branch tip cannot move the
release source.

### Pre-existing non-Apple red items

None encountered in the gates this round ran. The two pre-existing Apple-repository-side red items
recorded in the previous report (the `upstream-file-edited` negative case and
`check-hako-primary-route.sh`) belong to the Apple repository's own scripts, which this round was
forbidden to touch and did not run.

---

## 8. Remote Verification

```text
local HEAD:            see below - quoted at verification time
remote finalize HEAD:  identical
remote gitlink:        bb5309b4a6c5b06382e1277a6e7ac0fbeadc9a72
Apple main:            bb5309b4a6c5b06382e1277a6e7ac0fbeadc9a72
```

Verified by re-fetching and comparing, not by trusting the push output:

```bash
git fetch origin
git rev-parse HEAD
git rev-parse origin/jiejiebox/apple-finalize
git ls-tree origin/jiejiebox/apple-finalize clients/apple
```

The remote gitlink resolves to the Apple `main` SHA, so a checkout of the pushed branch gets the
consolidated client.

### Three-way agreement before push (§9 and §20)

```text
git ls-files -s clients/apple      -> bb5309b4a6c5…   (index, checked before commit)
git diff --cached --submodule=short -> 2a189686ae38 -> bb5309b4a6c5…
git ls-tree HEAD clients/apple      -> bb5309b4a6c5…   (checked after commit)
git -C clients/apple rev-parse HEAD -> bb5309b4a6c5…   (submodule checkout)
```

All four agree. This check exists because a previous round committed a message that described the
new SHA while the commit still recorded the old one — `git add` on a submodule re-stages whatever
the submodule currently has checked out, which silently undid an `update-index` performed
immediately before it. The index was therefore asserted **before** the commit and the tree
asserted **after** it, and the submodule was checked out to the target SHA *before* it was staged.

### `testing` is untouched

```text
origin/testing   未动
origin/jiejiebox/apple-finalize   新增
```

No push to `testing`, no merge into `testing`, no rebase, no force-push. Whether and when to absorb
this branch into `testing` is left to the operator.

---

## 9. Remaining Work

Only the three items §24 permits:

1. **Core hardening consolidation** — untouched by this round and still open. That is the core
   repository's own audit (`origin/jiejiebox/v0.1.6-hardening` against `origin/testing`), and it is
   not Apple work.
2. **Absorbing `jiejiebox/apple-finalize` into the eventual `testing`.** Deliberately not done
   here; the branch exists for the operator to review and merge.

3. **Known pre-existing non-Apple items**, recorded rather than fixed:

   - `scripts/ci/check-libbox-abi.sh` layer 3 SKIPs without `gobind`, and layer 4 SKIPs for Android
     without that submodule initialised. Both are host/checkout conditions. The gate reports
     `PARTIAL` rather than `PASS` for exactly this reason.
   - `scripts/ci/build-apple-libbox.sh` still defaults its framework **source** to the repository
     root (`Libbox.xcframework`), while `cmd/internal/build_libbox` writes to
     `_libbox_build/Libbox.xcframework`. The failure mode is quiet: the build succeeds, the check
     reports "Libbox.xcframework was not produced", and the install step copies a stale directory
     or fails while the freshly built framework sits one directory away. The fix is a two-token
     change to a default plus an explicit source on the install path. It is **independent of the
     pin** and was intentionally kept out of this commit so that the pin commit stays auditable;
     it is the reason `check-apple-shared-libbox.sh` cannot pass before a Libbox build here.

No Apple UI work is reopened by this report, and none should be opened from it.
