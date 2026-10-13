# CI / Release post-green optimization report

Continues `CI_RELEASE_POST_GREEN_OPTIMIZATION_PROMPT_V3_CONTINUE.md`. This is the **only**
report for this round; no second, competing report was created.

The round is **incomplete**: P0-A and P0-B are done and locally verified; P0-C, P0-D and all
of P1 are **BLOCKED_BY_ENVIRONMENT**, for a reason given in §4 that is a missing tool rather
than a missing decision.

---

## 1. Checkpoint reconciliation — the recorded checkpoint no longer matched the tree

The brief's first instruction was to reconcile the checkpoint before building anything, and
to read real values rather than trust short SHAs. Doing that found three discrepancies that
the brief's §1.2–§1.4 assert as facts. They are recorded here rather than silently
reconciled, per `AGENTS.md` ("the conflict is a defect to record").

| Brief says | Measured at start | Verdict |
|---|---|---|
| parent HEAD prefix `abd75e0e2`, 2 commits ahead of `origin/testing` | `ccab3645`, **0** ahead, `git status` clean apart from 3 untracked files | **superseded** |
| `clients/desktop` gitlink = `917d2a4` | `32f915ba` | **did not match** |
| `dashboard` gitlink = `3d32338f` | matches (nested inside the desktop tree) | matches |
| android `ec61030d3`, apple `40dd48795` | match | matches |
| worktree clean | 3 untracked `KNOWLEDGE/modules/*.md` | differs |

`abd75e0e2` and `e5bb66c5f` are **not present as objects** in the repository and are not in
any reflog; the reflog shows two `reset: moving to origin/testing` entries followed by a fresh
run of commits for the v0.1.7 release. So those two local commits were lost in this
environment, and the tree moved on to a genuine later state: `origin/testing` had advanced to
`ccab3645`, and `HEAD == origin/testing`, tagged `v0.1.7`.

**Consequence for the brief's model.** §1.3's "do not reset/rebase/squash the two local
commits" cannot be honoured and does not need to be: they no longer exist, and their *intent*
(the gitlink move) had been dropped, which is what P0-A had to restore. `PARENT_PUSHED = NO`
still holds — `origin/testing` advanced by some other push, not by this round.

### The real defect the checkpoint was hiding

The gitlink was `32f915ba`, which is not a fork commit at all:

```text
32f915ba  "Bump version 1.14.2"   2026-09-24   (reachable from the fork repo, but upstream content)
917d2a4   "feat(updates): own the Jiejiebox release feed and installer matcher"  (jiejiebox/main)
```

`32f915ba`'s `src/main/updates.ts` reads
`https://api.github.com/repos/SagerNet/sing-box/releases` and matches update assets with
`startsWith("SFW-")`. A release built from it would ship a client whose updater polls
**upstream's** feed looking for an installer name **this fork does not publish**. The two
halves of that contract (§1.2) were both absent, and `32f915ba`'s `version.json` still said
`1.14.2` — so the retired overlay's anchor would have appeared to be satisfied while the
product was wrong. This is the single most consequential finding of the round.

---

## 2. Header values

```text
START_HEAD            = ccab3645d60e9cba13b8e3a47b85dd1c1ca60d5e
END_HEAD              = 90e5f255 (resolve with `git log`; a file cannot name the hash of the
                                   commit that introduces it without being amended afterwards)
ORIGIN_TESTING        = ccab3645d60e9cba13b8e3a47b85dd1c1ca60d5e
LOCAL_COMMITS_BEFORE  = 0
LOCAL_COMMITS_ADDED   = 5
WORKTREE_CLEAN        = NO  (3 pre-existing untracked KNOWLEDGE/modules/*.md, not ours)
```

The five commits added, oldest first:

```text
dd67746c  refactor(desktop): retire the branding overlay and pin the maintained fork
89fbb7d7  refactor(release): make the desktop tooling identity exact
bef83635  docs(ci): report the post-green optimization round
1230fdf4  test(ci): record the P0-C duplication as a check instead of a plan
979d0268  docs(ci): fold the final measurements into the round report
```

```text
TAG_MOVED                         = NO
RELEASE_CREATED                   = NO
GITHUB_ACTIONS_RUN                = NO
workflow_dispatch                 = NO
rerun                             = NO
PUSHED_THIS_ROUND                 = NO
PARENT_PUSHED                     = NO
PREVIOUS_DESKTOP_FORK_PUSH        = 917d2a4e9864528e1c10c4c6a39f3949b6f5d878  (historical, not this round)
```

---

## 3. Ownership, before and after

| Concern | Before | After |
|---|---|---|
| Desktop static branding (23 rules) | parent-owned overlay script | **maintained fork** `Piggy-Cat-bit-shadow/sing-box-for-desktop` @ `917d2a4`, pinned as a submodule |
| Desktop dynamic release version | overlay's `version_rule`, anchored on the upstream literal `1.14.2` | `release/JIEJIE_VERSION` → `inject-desktop-release-version.py`, anchored on nothing |
| Desktop source identity | not checked | `prepare-desktop-client.sh` + `check-desktop-source-identity.sh` (parent commit vs client HEAD vs dashboard HEAD; stamp is the only mutation) |
| Desktop tooling identity | floating `release_branch`, default `testing` | exact `tooling_sha`, required, must **equal** `core_sha` |
| Desktop version contract tests | `test-desktop-overlay-version.py` (through the overlay) | `test-desktop-release-version.py` (60 cases, fixture-only) |
| Identity regression tests | did not exist | `test-desktop-source-identity.sh` (30 cases, real git fixtures) |
| Release Contract Verification push trigger | every push to `testing` | release-tooling `paths`, declared once and aliased; `workflow_dispatch` retained |
| Release helper test ownership | Verify + Release Contract + Formal Release | **unchanged — P0-C deferred** |
| Release qualification authority | `release-artifacts.sh` + `verify-release-acceptance.sh` | **unchanged — P0-D deferred** |

---

## 4. Why P0-C, P0-D and P1 are blocked, and what unblocks them

P0-C and P0-D both require the 92-case release-helper suite
(`scripts/ci/test-release-artifacts.sh`) to be run **before and after** editing
`release-artifacts.sh`. That is not possible in this environment, and the brief is explicit
that a stage which cannot be completely verified locally must not be entered.

Measured availability of the tools that suite and its workflow need:

| Tool | Status here |
|---|---|
| `bash` | present (via MinGit's `sh.exe`, which is `bash 5.2.37`) |
| `git` | present (MinGit 2.50.1, portable) |
| `python3` | present (3.12.14, bundled) |
| `mktemp`, `sed`, `awk`, `grep`, `cut`, `tr`, `sort` | present |
| **`jq`** | **MISSING** — searched all of `C:\Deepseek`, `C:\Users\Jie`, `C:\Program Files`, `C:\ProgramData` |
| **`chmod`** | **MISSING** — same search |
| **`sha256sum`** | **MISSING** (the suite has a fallback at line 445, so not fatal) |
| `tar` | resolves to `C:\Windows\system32\tar.exe` (bsdtar, not GNU tar) |

Running the suite stops immediately:

```text
./scripts/ci/test-release-artifacts.sh: line 377: chmod: command not found
```

`jq` is the deciding one. The suite installs a **mock `gh`** whose output is parsed with
`jq`, and that mock is what makes the suite unable to reach GitHub — the workflow itself
asserts the mock is wired (`grep -q 'MOCK_GH' …`). Writing a Python `jq` shim to make the
suite run would replace the very component under test with my own approximation and could
produce a **false green on the release path**, which the brief rates worse than no result
("不要为了绿灯削弱 fail-closed"). Installing git or a coreutils bundle is forbidden by §15.

**BLOCKED_BY_ENVIRONMENT**, named precisely: `test-release-artifacts.sh` cannot execute
because `jq` and `chmod` are absent. What it would have proved for P0-C is that moving the
helper suite's ownership out of Verify and replacing Formal Release's re-run with an
exact-SHA evidence check leaves the 92 cases passing; for P0-D, that folding
`verify-release-acceptance.sh`'s judgement into `release-artifacts.sh`'s `select_exact_sha_run`
primitive leaves them passing.

Unblocked by any one of: a GitHub runner (a `workflow_dispatch` of *Release contract
verification*), or a machine with `jq`, `chmod` and `sha256sum` on `PATH`.

### P0-A / P0-B were verified in full, so they were not held back

Every gate the brief asks for at §3.5 and §4 was run and is green; the evidence is in §5. The
one gate that was **not** runnable is named there too, and it was not claimed as a pass.

---

## 5. Local test results

Every command below was executed in this environment. `git` is MinGit 2.50.1 (portable, from
an existing tool directory — **nothing was installed**), `python` is the bundled 3.12.14, and
`bash` is MinGit's `sh.exe`, which reports itself as `bash 5.2.37(1)-release`.

| # | Command | Exit | Result |
|---|---|---|---|
| 1 | `python scripts/ci/test-desktop-release-version.py` | 0 | **60 passed, 0 failed** |
| 2 | `bash scripts/ci/test-desktop-source-identity.sh` | 0 | **30 passed, 0 failed** |
| 3 | `python scripts/ci/check-release-workflows.py` | 0 | **48 passed, 0 failed** |
| 4 | `bash -n scripts/ci/prepare-desktop-client.sh` | 0 | syntax OK |
| 5 | `bash -n scripts/ci/check-desktop-source-identity.sh` | 0 | syntax OK |
| 6 | `bash -n scripts/ci/test-desktop-source-identity.sh` | 0 | syntax OK |
| 7 | `bash -n scripts/ci/release-artifacts.sh` | 0 | syntax OK |
| 8 | `bash -n scripts/ci/lib.sh` | 0 | syntax OK |
| 9 | `bash -n scripts/ci/test-release-artifacts.sh` | 0 | syntax OK |
| 10 | `python -m py_compile` on all 3 changed Python files | 0 | syntax OK |
| 11 | `yaml.safe_load` on all 10 workflows | 0 | all parse |
| 12 | `bash -n` on every `shell: bash` step of `client-desktop-windows.yml` | 0 | 16/16 OK (9 `pwsh` steps skipped — not bash) |
| 13 | `git diff --check` | 0 | no whitespace errors |
| 14 | `scripts/ci/prepare-desktop-client.sh` (end to end, real submodules) | 0 | `PASS: desktop client prepared` |
| 15 | `scripts/ci/test-release-artifacts.sh` | — | **BLOCKED_BY_ENVIRONMENT** (`jq`, `chmod` missing) |

Two results worth keeping, because each is a check catching a real thing rather than a green
tick:

* **#14 first run FAILED, correctly.** Before the P0-A commit, the script refused with
  `client HEAD = 917d2a4… / parent records = 32f915ba…` — it caught that the parent commit
  still recorded the old gitlink, which is exactly the silent submodule failure §1.5 makes a
  hard gate. It passed only after the gitlink was committed.
* **Idempotence is asserted, not assumed.** Re-running the stamp with the same version writes
  nothing and still re-proves the file, and re-running with a different version overwrites
  cleanly; both are separate cases.

The stale-string sweep from §3.5 is clean: `apply-desktop-branding-overlay`,
`UPSTREAM_DESKTOP_VERSION`, `parent-owned overlay`, `pinned upstream desktop` and `1.14.2`
appear in **no active CI or release tooling**. The only remaining occurrences are prose that
explains the retirement and the tests that assert its absence. Dated records in
`docs/fork/` and the transcript quoted in `KNOWLEDGE/modules/ci-release.md` are retained with
a note, as the brief allows.

---

## 6. How the P0-A decision was made (not by re-auditing the 23 rules)

The brief forbids re-auditing the 23 overlay rules. It did not need to be done, because the
question "are these rules native in the fork?" has a behavioural answer:

1. A detached worktree was created at `917d2a4`, with its nested `dashboard` submodule at the
   recorded `3d32338f`.
2. The **complete, unmodified** overlay was run against it with `--version 0.1.5` — the value
   the tree itself carries.

```text
overlay rules: 24 applied/confirmed
nothing to write: the client already carries this overlay
```

3. `git status` in the probe tree was empty afterwards.

So all 24 rules resolve as "already" on the pinned fork revision, by the overlay's own
semantics rather than by substring matching — which is the correction §1.1 records. The probe
worktree was removed; the real tree was never used for the experiment.

This also gives the exact list of what the fork now owns: the updater feed and asset matcher,
the installer name, `executableName: sing-box`, the three unsigned-build changes in
`package.ts`, the `afterPack.cjs` change, the visible branding in `electron-builder.yml` and
the dashboard, and the seven `installer.nsh` diagnostics.

## 7. Design decisions worth recording

**The injector validates the source version's SHAPE, never its value.** The overlay anchored
on `UPSTREAM_DESKTOP_VERSION = "1.14.2"` and the fork's own file says `0.1.5`, so the old
anchor was already the wrong question, and replacing it with `0.1.5` would have created a new
historical literal — exactly what §3.1 forbids. The new rule is: the baseline may be any valid
version, and must be present. The test suite asserts `0.1.5`, `0.1.7`, `1.14.2`, `0.2.0-rc1`
and `10.20.30` are all accepted, so a future fork release that bumps its own version cannot
break the build.

**Source identity is split between the shell script and the injector.** The injector needs no
repository context, which is what lets it be tested from a bare `version.json` fixture in CI
where two levels of submodule are empty. The gitlink checks need git, the parent and a
superproject, so they live in `prepare-desktop-client.sh`. Both halves are named in the same
comments so neither is mistaken for the other.

**The check compares against the parent COMMIT, not the index.** `git rev-parse
"HEAD:clients/desktop"` rather than `git ls-files -s`. A workflow builds a commit, and a
staged-but-uncommitted gitlink is precisely the defect this round found.

**`tooling_sha` is a separate input even though it must equal `core_sha`.** §4 permits a
second identity only if architecture demands it, and asks not to add one without reason. The
reason is narrow and stated in the workflow: the identity check must fetch
`refs/tags/v<version>` into a repository without disturbing the core's worktree. The equality
is asserted **on the values first**, so this buys a second checkout, not a second identity.

---

## 8. Item status

### P0

| Item | Status |
|---|---|
| P0-A desktop dynamic version injection + retire the old overlay | **DONE** |
| P0-B Windows Desktop tooling identity → exact | **DONE** |
| P0-C release helper test ownership + Formal Release exact-SHA evidence | **BLOCKED** — see §4. Defects measured and recorded as checks in `check-release-workflows.py` |
| P0-D merge release qualification dual authority | **BLOCKED** — see §4. Both authorities confirmed present and recorded |

### P1 (in the brief's order)

| # | Item | Status |
|---|---|---|
| 1 | Verify: move the two duplicate race/deep checks out | DEFERRED (P0 incomplete) |
| 2 | Verify: narrow the docs-only trigger | DEFERRED (P0 incomplete) |
| 3 | Release Contract: narrow push paths | **DONE** (folded into P0-A, and it is a prerequisite for the new suites being triggered at all) |
| 4 | Android payload / evidence separation | DEFERRED (P0 incomplete) |
| 5 | Release exact artifact-name selection | DEFERRED (P0 incomplete) |
| 6 | delete the Android scoped cleanup | DEFERRED (P0 incomplete) |
| 7 | delete `RELEASE_EXTRA_PLATFORMS` / two-platform legacy semantics | DEFERRED (P0 incomplete) |
| 8 | Formal Release wait strategy | DEFERRED (P0 incomplete) |

Item 3 was done early deliberately: P0-B adds two suites to Release Contract Verification, and
without the `paths` narrowing those suites would run on every push to `testing`. Landing the
narrowing in the same change keeps the workflow's trigger honest rather than adding load and
narrowing it later.

---

## 9. Discovered optimizations

| Finding | Evidence | Fixed? | Why / why not |
|---|---|---|---|
| The pinned desktop gitlink pointed at upstream content (`32f915ba`), so the shipped updater would poll `SagerNet/sing-box` and match `SFW-` | `git show HEAD:src/main/updates.ts`; GitHub API compare `32f915ba...jiejiebox/main` = ahead 2 | **YES** — P0-A | Both halves of the release feed contract were absent, not just one |
| `prepare-desktop-client.sh` and the overlay described the client as "pinned to upstream SagerNet/sing-box-for-desktop" | script headers | **YES** — P0-A | Documentation describing the old model is the drift the brief lists |
| `client-desktop-windows.yml` gate 2 asserted an *overlay* had set `executableName` and `forceCodeSigning` | gate 2 step names and comments | **YES** — P0-A | It now asserts the properties directly, and additionally asserts the fork's release feed and asset matcher, which nothing checked before |
| `release-artifacts.sh`'s dispatch hint named `core_sha`/`core_version` only | `release-artifacts.sh:512` | **YES** — P0-B | With `tooling_sha` required, the hint would have produced a dispatch that fails gate 1 |
| The tag-to-commit proof was asserted by a string match in `check-release-workflows.py`; moving it into the helper made that check assert the wrong thing | checker output after extraction | **YES** — P0-B | It now asserts the helper that owns the proof still contains it, so it cannot be lost from both places |
| `check-release-workflows.py` labelled a `release-contract-verify.yml` check "verify workflow: …" | `VERIFY` constant vs step content | **NO** — recorded | Cosmetic naming; changing it would touch an unrelated checker. Worth a follow-up |
| `python3` on a Windows runner is usually only `python`, while `release-contract-verify.yml` calls `python3` | `scripts/ci/prepare-desktop-client.sh` already resolves `python3`/`python` for exactly this reason | **NO** — recorded | It runs on `ubuntu-latest`, where `python3` exists. Not a defect today, but the two conventions in one repository is a latent trap |
| The 3 untracked `KNOWLEDGE/modules/*.md` files are byproducts of a partially-applied knowledge refresh | untracked; `AGENTS.md` §5 forbids refreshing `KNOWLEDGE/` during development | **NO** — recorded | Deciding whether to commit, delete or finish them is a knowledge-base maintenance operation, not this round's work. Left exactly as found |
| The 92-case release helper suite runs **three** times per release, measured at `verify.yml:64`, `release-contract-verify.yml:105` and `release.yml:109` | grep of all three workflows | **PARTLY** — recorded as two failing-on-removal checks | Removing the duplication is P0-C, which is blocked. Recording it means the day it is fixed, `check-release-workflows.py` fails on the stale assertions and forces them to be replaced by the exact-SHA evidence check |
| Formal Release re-proves the product it is publishing instead of citing evidence already obtained at that SHA | `release.yml:109` runs the full suite on the publishing runner | **NO** — recorded | Same blocker |
| Two independent release-qualification authorities: `verify-release-acceptance.sh` makes its own admissibility judgement (its own exact-SHA test at line 294) while `release-artifacts.sh` selects runs with its own `newest_run` primitive | both files present; primitives named differently | **NO** — recorded | §6 prefers `release-artifacts.sh` grow one `select_exact_sha_run`/`require_exact_sha_success` primitive that all three consumers share. That is a refactor of the very file whose gate cannot be run here |

---

## 10. Final state — and why the readiness flag is NO

```text
CI_RELEASE_REFACTOR_LOCAL         = INCOMPLETE
GITHUB_ACTIONS_RUN                = NO
PUSHED_THIS_ROUND                 = NO
PARENT_PUSHED                     = NO
TAG_MOVED                         = NO
RELEASE_CREATED                   = NO
READY_FOR_FINAL_FREEZE_VALIDATION = NO
```

**NO** because P0-C and P0-D are not done, and §19's completion criteria include the release
helper having a single test owner and candidate evidence having one exact-SHA authority.
Neither is true yet.

What *is* true, and is the bulk of §19's list:

```text
Desktop static source owner      = maintained fork            ACHIEVED
Desktop dynamic version owner    = release/JIEJIE_VERSION     ACHIEVED
Desktop tooling identity         = exact                      ACHIEVED
Verify                           = lightweight source gate      unchanged (P1 deferred)
Product workflows                = artifact owner              unchanged
Release                          = promotion only              unchanged (P0-C/D)
Android payload/evidence         = not separated               (P1 deferred)
Release targets                  = not yet canonical            (P1 deferred)
```

Two further things the final freeze will need, unchanged by this round and repeated here so
they are not lost: the `[skip ci]` dependency sync means the frozen SHA must obtain its **own**
Action evidence (§17), and `release-contract-verify.yml` keeps `workflow_dispatch` for exactly
that purpose — which the push-path narrowing deliberately did not remove.
