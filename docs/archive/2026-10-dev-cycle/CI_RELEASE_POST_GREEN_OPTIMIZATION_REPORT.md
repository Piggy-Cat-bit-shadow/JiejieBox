# CI / Release post-green optimization report

Continues `CI_RELEASE_POST_GREEN_OPTIMIZATION_PROMPT_V3_CONTINUE.md`. This is the **only**
report for this round; no second, competing report was created.

The round is **incomplete**: P0-A and P0-B are done and locally verified; P0-C, P0-D and all
of P1 are **BLOCKED_BY_ENVIRONMENT**, for a reason given in §4 that is a missing tool rather
than a missing decision.

> ## Correction — this report's first revision was written against a STALE LOCAL REF
>
> The first revision of this file reported `origin/testing = ccab3645` and used that as the
> base for every claim. That was **wrong**: the local remote-tracking ref was stale. The user
> challenged it, and a reconciliation pass (§2a) established the truth:
>
> * `refs/remotes/origin/testing` said `ccab3645` — last moved 2026-10-12, by a **push**
>   reflog entry, so no fetch had refreshed it since.
> * `git ls-remote origin refs/heads/testing` says **`9bdcf8e8`**, which is also what the
>   brief's §2 said and what GitHub's API returns.
> * the local clone was **not** shallow, and `9bdcf8e8` was already present as an object — only
>   the ref was behind.
>
> So the brief was right and this report was wrong. Everything below is re-derived against
> `9bdcf8e8`, and the five commits have been replayed onto it. The stale-base branch is kept as
> `wip/p0ab-stale-base` for comparison. **The lesson is recorded rather than quietly fixed: a
> `git rev-parse refs/remotes/...` is not evidence about a remote.** It is evidence about the
> last fetch, and it is exactly the class of mistake this repository's `AGENTS.md` warns about
> ("Source and Git are facts" — but a stale ref is neither).

---

## 1. Reconciliation against the true remote

Measured with `git ls-remote` (the remote's own answer) and the GitHub API, not with local
remote-tracking refs:

| Value | Local ref said (stale) | **Truth** |
|---|---|---|
| `origin/testing` | `ccab3645` | **`9bdcf8e8b17b67a2c0fd306d2cc865535dc6970e`** |
| `origin/testing` subject | — | `sync(upstream): absorb sing-tun hairpin batching without losing fork fixes [skip ci]` |
| `refs/tags/v0.1.7` object | `46361ea8` | **`cf419b9601e9d2c9eb738f14f5ee1024df95da84`** |
| `refs/tags/v0.1.7^{}` (commit) | `ccab3645` | **`6f0d5519c74816f0a4c255f46a08fe0f298b3cd7`** |
| `clients/desktop` gitlink | `32f915ba` | **`18ef1ef6ccd831de0c64cca48e3cd33522271358`** |

Two things follow, and the second is the important one:

1. **`skip ci` sync landed.** `9bdcf8e8` is exactly the commit the brief's §2 named as the
   dependency sync, so §2 was accurate and this report's first revision was not.
2. **The tag moved.** `v0.1.7` on the remote names `6f0d5519`, not `ccab3645`. This round was
   forbidden to move a tag and did not; a previous round did, and
   `V0.1.7_FORMAL_RELEASE_REPORT.md` (at the true tip) records it. §1.3's "do not move/re-tag"
   constraint is therefore about *this* round only — which it honoured — and the tag's existing
   movement is a fact to be aware of, not something to undo.

The brief's §1.2–§1.4 checklist, re-measured at `9bdcf8e8`:

| Brief says | At the true tip | Verdict |
|---|---|---|
| desktop gitlink `917d2a4` | `18ef1ef6` | **does not match — and `18ef1ef6` is the wrong commit, see §3** |
| dashboard gitlink `3d32338f` | `3d32338f` | matches |
| android `ec61030d3` | matches | matches |
| apple `40dd48795` | matches | matches |
| `abd75e0e2` / `e5bb66c5f` local commits preserved | absent from the object database | lost before this round; see the correction note |

`abd75e0e2` and `e5bb66c5f` are genuinely gone — absent as objects, absent from the reflog.
They were replaced by the v0.1.7 release run now on the remote. Nothing in this round reset,
rebased, squashed or dropped anything that existed, beyond replaying this round's own five
commits onto `9bdcf8e8` (§2a), which is a fast-forward of the round's own work and not a
rewrite of published history.

---

## 2. Header values

```text
START_HEAD            = ccab3645d60e9cba13b8e3a47b85dd1c1ca60d5e   (the stale local ref, which
                                                                     is what this round began on)
TRUE_REMOTE_BASE      = 9bdcf8e8b17b67a2c0fd306d2cc865535dc6970e   (origin/testing, measured)
ORIGIN_TESTING        = 9bdcf8e8b17b67a2c0fd306d2cc865535dc6970e
END_HEAD              = resolve with `git log`; a file cannot name the hash of the commit that
                        introduces it without being amended afterwards
LOCAL_COMMITS_BEFORE  = 0
LOCAL_COMMITS_ADDED   = 5  (replayed onto TRUE_REMOTE_BASE; the pre-replay set is kept on
                            wip/p0ab-stale-base)
WORKTREE_CLEAN        = NO  (3 pre-existing untracked KNOWLEDGE/modules/*.md, not ours)
```

### 2a. The replay

`ccab3645` is an **ancestor** of `9bdcf8e8` (`git merge-base --is-ancestor` = 0), so this was
not a divergent rebase: the round's five commits were replayed onto a tip that simply had more
history. The replayed set, oldest first:

```text
c50f9d1a7  refactor(desktop): retire the branding overlay and pin the maintained fork
92efd831a  refactor(release): make the desktop tooling identity exact
685f4e1d4  docs(ci): report the post-green optimization round
35f4a6bcc  test(ci): record the P0-C duplication as a check instead of a plan
788271667  docs(ci): fold the final measurements into the round report
```

preceded, on `wip/p0ab-stale-base`, by the same five commits against the stale base:
`dd67746c`, `89fbb7d7`, `bef83635`, `1230fdf4`, `979d0268`. Both sets are kept so the replay is
auditable rather than asserted.

Two conflicts, both mechanical and both resolved in favour of the truth:

| File | Conflict | Resolution |
|---|---|---|
| `clients/desktop` | both sides moved the gitlink | resolved to `917d2a4` — see §4a |
| `.github/workflows/release-contract-verify.yml` | both sides extended the `bash -n` file list | kept the remote's fail-capable `if ! bash -n` loop (its fix: a bare `bash -n` reset `$?` and the gate could not fail) and added this round's two new scripts to its list |

Nothing was discarded from the remote side. The remote's improvement to that loop is a better
version of what this round was also touching, and it was kept.

```text
TAG_MOVED                         = NO   (by this round; the tag had already moved on the remote)
```
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
| **`jq`** | **MISSING** — searched all of `<WORKSPACE_ROOT>`, `<LOCAL_HOME>`, `C:\Program Files`, `C:\ProgramData` |
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

## 4a. The desktop pin: `18ef1ef6` → `917d2a4` (corrected twice)

This section was wrong twice before it was right. Both errors and both corrections are kept,
because the errors are the instructive part.

### Error 1 (revision 1): invented alarm

Revision 1 reported `origin/testing = ccab3645` from a stale ref and concluded from a gitlink
of `32f915ba` that the shipped updater would poll upstream's feed. That was corrected in §1.

### Error 2 (revision 2): "no common ancestor" — a shallow-clone artifact

Revision 2 reported:

```text
git merge-base 18ef1ef6 917d2a4                     -> (empty: no common ancestor)
git merge-base --is-ancestor 18ef1ef6 917d2a4       -> exit 1
git merge-base --is-ancestor 917d2a4 18ef1ef6       -> exit 1
```

and built an argument on it: "two diverged configurations, the owner must choose". **That is
false.** Measured now, against real history:

```text
917d2a4 parents        : 18ef1ef6              <- 18ef1ef6 is its DIRECT PARENT
18ef1ef6 parents       : 32f915ba
git merge-base A B     : 18ef1ef6
A(18ef) ancestor of B  : YES
diff A..B              : 1 file changed, 18 insertions(+), 2 deletions(-)
files A..B             : src/main/updates.ts
```

```text
18ef1ef6  ──►  917d2a4  ──►  refs/heads/jiejiebox/main
   └─ 32f915ba
```

So V4 §3.2 is correct: `18ef + overlay` is simply the earlier phase, and
`917d + no static parent overlay` is the final source ownership. This matches the brief exactly
and the report should never have disputed it.

**Root cause of error 2, and the rule it earns.** The desktop submodule was a `--depth 1` clone
from an earlier `submodule update --init --depth 1`. In a shallow repository `git merge-base`
cannot walk to a common ancestor and returns empty with exit 1 — which is indistinguishable, in
output alone, from "these commits are unrelated". It was read as a fact about the repository
when it was a fact about the clone. The check that would have caught it was already in hand and
was misread: `GitHub compare 18ef1ef6...jiejiebox/main` had returned `behind_by=0`, and
`behind_by=0` on that comparison means the base is an ancestor of the head, i.e. a
fast-forward. `behind_by=0` was the answer; `merge-base` was the noise.

> **Do not run ancestry tests in a shallow clone.** Either `git fetch --unshallow` first, or ask
> the API (`/compare`, or the commit's `parents`). `merge-base` returning empty means "no local
> path", not "no relationship".

### What survives, and it is the same decision

The decision V4 §3.1 fixes — keep `917d2a4`, do not revert to `18ef1ef6` — is unaffected and
this round keeps it. What changes is the *reason*, which is now the simple one:

* `917d2a4` is the tip of `jiejiebox/main`, the branch `.gitmodules` names as this submodule's
  source, and it is `18ef1ef6` plus the one commit that moves the updater contract into source;
* `18ef1ef6` is that same branch minus that commit, so pinning it would leave the updater
  contract to be re-applied at build time by the parent overlay — the arrangement P0-A retires;
* `git diff 18ef1ef6 917d2a4` is `src/main/updates.ts` and nothing else, so there is no third
  behaviour being traded away.

The reachability worry from revision 2 also dissolves: `18ef1ef6` is the parent of a
branch-reachable commit, so it is reachable from `jiejiebox/main`. It was never dangling.

The two gitlinks are proved, not assumed, exactly as §1.5 requires:

```text
desktop   HEAD 917d2a4e9864528e1c10c4c6a39f3949b6f5d878 == parent index gitlink   MATCH
dashboard HEAD 3d32338fdc35b050cd9728afa7f66c18206e4fb9 == client index gitlink   MATCH
```

**For the owner.** Nothing here is left for a decision: V4 §3.1 fixes the gitlink at `917d2a4`,
and this round keeps it. This section is retained as the record of how the pin was misjudged
twice, because the second misjudgement — trusting `merge-base` in a shallow clone — is a trap
that will recur on any submodule in this repository.

---

## 5. Local test results

Every command below was executed in this environment. `git` is MinGit 2.50.1 (portable, from
an existing tool directory — **nothing was installed**), `python` is the bundled 3.12.14, and
`bash` is MinGit's `sh.exe`, which reports itself as `bash 5.2.37(1)-release`.

| # | Command | Exit | Result |
|---|---|---|---|
| 1 | `python scripts/ci/test-desktop-release-version.py` | 0 | **60 passed, 0 failed** |
| 2 | `bash scripts/ci/test-desktop-source-identity.sh` | 0 | **30 passed, 0 failed** |
| 3 | `python scripts/ci/check-release-workflows.py` | 0 | **100 passed, 0 failed** (the remote's version of this checker is far larger than the one this round edited; the merge kept both) |
| 4 | `bash -n scripts/ci/prepare-desktop-client.sh` | 0 | syntax OK |
| 5 | `bash -n scripts/ci/check-desktop-source-identity.sh` | 0 | syntax OK |
| 6 | `bash -n scripts/ci/test-desktop-source-identity.sh` | 0 | syntax OK |
| 7 | `bash -n scripts/ci/release-artifacts.sh` | 0 | syntax OK |
| 8 | `bash -n scripts/ci/lib.sh` | 0 | syntax OK |
| 9 | `bash -n scripts/ci/test-release-artifacts.sh` | 0 | syntax OK |
| 10 | `yaml.safe_load` on all workflows in `.github/workflows/` | 0 | all parse (the true tip has more workflows than the stale base did) |
| 11 | `bash -n` on every `shell: bash` step of `client-desktop-windows.yml` | 0 | all OK (the `pwsh` steps are not bash and are skipped) |
| 12 | `git diff --check` | 0 | no whitespace errors |
| 13 | `scripts/ci/prepare-desktop-client.sh` (end to end, real submodules, after the replay) | 0 | `PASS: desktop client prepared` |
| 14 | `scripts/ci/test-release-artifacts.sh` | — | **BLOCKED_BY_ENVIRONMENT** (`jq`, `chmod` missing) |

Three results worth keeping, because each is a check catching a real thing rather than a green
tick:

* **#13 first run FAILED, correctly.** Before the P0-A commit, the script refused with
  `client HEAD = 917d2a4… / parent records = 32f915ba…` — it caught that the parent commit
  still recorded the old gitlink, which is exactly the silent submodule failure §1.5 makes a
  hard gate. It passed only after the gitlink was committed.
* **The same check proved its value again during the replay.** `b462bbfb0` on the true remote
  stages a gitlink move; a parent that records a different revision than the submodule holds is
  precisely what this check refuses, and it refuses it with the two hashes printed.
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
question "are these rules native in the fork?" has a behavioural answer. It was measured twice,
against **both** candidate pins, because the probe result is what justifies retiring the
overlay and it must not depend on which revision is pinned:

| Probe | Result |
|---|---|
| overlay vs `917d2a4` (`jiejiebox/main`, the pin) | `24 applied/confirmed` → **`nothing to write`**, probe tree CLEAN |
| overlay vs `18ef1ef6` (`917d2a4`'s parent, the earlier phase) | `24 applied/confirmed` → **`wrote: src/main/updates.ts`**, probe tree DIRTY |

Both rows are load-bearing, and together they are the whole argument for P0-A's shape:

* row 1 is why the overlay is redundant **on the pin we keep** — all 24 rules already resolve as
  "already", so the parent no longer owns any static branding;
* row 2 is why the overlay was still *necessary* one commit earlier — `18ef1ef6` kept upstream's
  `updates.ts`, so the parent had to supply the updater contract at build time. That is the
  "earlier phase" V4 §3.2 describes, and it is a strict subset of row 1's state.

Because `18ef1ef6` is `917d2a4`'s parent, this is a single progression rather than two
alternatives; §4a's error 2 said otherwise and is corrected there. The procedure, for `917d2a4`:

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
worktrees were removed; the real tree was never used for the experiment.

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
| The desktop gitlink on `testing` was `18ef1ef6` while `.gitmodules` names `jiejiebox/main`, whose tip is `917d2a4` — and `18ef1ef6` is `917d2a4`'s **direct parent**, one commit behind, differing only in `src/main/updates.ts` | `git rev-list --parents` = `18ef1ef6`; `git diff --shortstat` = 1 file, +18 −2; `git merge-base` = `18ef1ef6` | **YES** — re-pinned to `917d2a4` in P0-A, and **kept frozen per V4 §3.1** | The updater contract (`Piggy-Cat-bit-shadow/sing-box` feed + `Jiejiebox-v*.exe` matcher) belongs in source rather than re-applied per build by the parent overlay. `18ef + overlay` is the earlier phase; `917d + no static overlay` is the final ownership. Not a decision to re-open |
| The remote's `b462bbfb0` already made the `.gitmodules` URL change this round was asked for, so P0-A's `.gitmodules` hunk is a no-op against the true tip | `git show b462bbfb0 --stat` = `.gitmodules` + `clients/desktop` | **YES** — reconciled | The replay kept the remote's version; the only desktop-side change this round makes is the gitlink value |
| `prepare-desktop-client.sh` and the overlay described the client as "pinned to upstream SagerNet/sing-box-for-desktop" | script headers | **YES** — P0-A | Documentation describing the old model is the drift the brief lists |
| `client-desktop-windows.yml` gate 2 asserted an *overlay* had set `executableName` and `forceCodeSigning` | gate 2 step names and comments | **YES** — P0-A | It now asserts the properties directly, and additionally asserts the fork's release feed and asset matcher, which nothing checked before |
| `release-artifacts.sh`'s dispatch hint named `core_sha`/`core_version` only | `release-artifacts.sh:512` | **YES** — P0-B | With `tooling_sha` required, the hint would have produced a dispatch that fails gate 1 |
| The tag-to-commit proof was asserted by a string match in `check-release-workflows.py`; moving it into the helper made that check assert the wrong thing | checker output after extraction | **YES** — P0-B | It now asserts the helper that owns the proof still contains it, so it cannot be lost from both places |
| `check-release-workflows.py` labelled a `release-contract-verify.yml` check "verify workflow: …" | `VERIFY` constant vs step content | **NO** — recorded | Cosmetic naming; changing it would touch an unrelated checker. Worth a follow-up |
| `python3` on a Windows runner is usually only `python`, while `release-contract-verify.yml` calls `python3` | `scripts/ci/prepare-desktop-client.sh` already resolves `python3`/`python` for exactly this reason | **NO** — recorded | It runs on `ubuntu-latest`, where `python3` exists. Not a defect today, but the two conventions in one repository is a latent trap |
| The 3 untracked `KNOWLEDGE/modules/*.md` files are byproducts of a partially-applied knowledge refresh | untracked; `AGENTS.md` §5 forbids refreshing `KNOWLEDGE/` during development | **NO** — recorded | Deciding whether to commit, delete or finish them is a knowledge-base maintenance operation, not this round's work. Left exactly as found |
| The 92-case release helper suite runs **three** times per release, re-measured at the true tip: `verify.yml:64`, `release-contract-verify.yml:84` and `release.yml:180` | grep of all three workflows, excluding comment lines | **PARTLY** — recorded as failing-on-removal checks | Removing the duplication is P0-C, which is blocked. Recording it means the day it is fixed, `check-release-workflows.py` fails on the stale assertions and forces them to be replaced by the exact-SHA evidence check |
| Formal Release re-proves the product it is publishing instead of citing evidence already obtained at that SHA | `release.yml:180` runs the full suite on the publishing runner | **NO** — recorded | Same blocker. Note the remote has since reworked `release.yml` heavily (`32ead81bb`, `0549d87ff`) to bind the tooling to the tag — a good change that does **not** remove this re-run |
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

---

# V4 round (main-heading continuation)

Supersedes §7–§10 above where they disagree: the V4 sections are measured against the true
remote, and §4a records why the earlier ones were not.

## A. Real identity baseline

Measured at the start of this round with `git ls-remote` and the GitHub API. The local
remote-tracking ref is listed too, to keep the distinction visible.

```text
LOCAL_HEAD                      = 7aa5a36ddd806c1467026cb3b3f265868d7bcca5
REAL_REMOTE_TESTING (ls-remote) = 9bdcf8e8b17b67a2c0fd306d2cc865535dc6970e
LOCAL_REMOTE_TRACKING_TESTING   = 9bdcf8e8b17b67a2c0fd306d2cc865535dc6970e   (in agreement)
V0.1.7_OBJECT (local)           = 46361ea86bc579709e47bbd302ba2032928ae267
V0.1.7_PEELED_COMMIT (local)    = ccab3645d60e9cba13b8e3a47b85dd1c1ca60d5e
── for reference, the REMOTE tag ──
refs/tags/v0.1.7                 = cf419b9601e9d2c9eb738f14f5ee1024df95da84
refs/tags/v0.1.7^{}              = 6f0d5519c74816f0a4c255f46a08fe0f298b3cd7
```

The local `v0.1.7` still names `ccab3645` and the remote names `6f0d5519`. **This round did not
move it** and must not: the remote tag was moved by an earlier round
(`V0.1.7_FORMAL_RELEASE_REPORT.md`), and it is recorded here so the disagreement is not mistaken
for local drift. Any work that resolves release identity must use the remote peel.

The local ref was refreshed at the start of this round, so `LOCAL_REMOTE_TRACKING_TESTING` and
`REAL_REMOTE_TESTING` now agree. That agreement is a property of *this* moment, not a guarantee:
the rule from §1 of the brief stands, and `git ls-remote` remains the only answer to "what is the
remote now".

## B. Frozen design decisions

Kept exactly as V4 §3.1, §4 and §5 fix them. This round re-derived the reason for the first one
and did not change the decision.

```text
DESKTOP_GITLINK        = 917d2a4e9864528e1c10c4c6a39f3949b6f5d878   KEEP
STATIC_OVERLAY         = RETIRED                                    (deleted in c50f9d1a7)
DYNAMIC_VERSION_OWNER  = release/JIEJIE_VERSION                     (inject-desktop-release-version.py)
DESKTOP_TOOLING_SHA    = exact, and == core_sha                     (client-desktop-windows.yml)
```

The re-derivation, recorded because two earlier revisions of this report got it wrong: `917d2a4`
is `18ef1ef6` **plus one commit**, not a diverged alternative. `git rev-list --parents` gives
`18ef1ef6` as its direct parent and `git diff` gives one file, `src/main/updates.ts`, +18 −2. So
`18ef + overlay` is the earlier phase and `917d + no static overlay` is the final ownership, in a
single progression. The pin is not re-opened and no revert to `18ef1ef6` is proposed.

## C. P1 item status

| Item | Status | Note |
|---|---|---|
| P1-A Verify slimming | **PARTIAL** | Owner table built and recorded in `verify.yml`; both race steps **KEPT**. No race step had a usable non-push owner, so the brief's "no owner → do not delete, record it" applies. See D-1. |
| P1-B Verify docs-only trigger | **DONE** | `paths-ignore: ["*.md"]` on push and pull_request; 18/18 filter cases correct; the three machine-read `docs/fork/*.md` still trigger. |
| P1-C Release Contract push paths re-audit | **DONE** | Re-audited mechanically; **found and closed a real gap** (two executed files were unlisted). Paths kept at 18, not widened. |
| P1-D Android payload/evidence separation | **DEFERRED_BY_92_CASE_ENV** | Producer is already clean; completion requires `release-artifacts.sh`, which §6 freezes. Full reason and exact future patch scope in F. |
| P1-E `RELEASE_EXTRA_PLATFORMS` legacy semantics | **PARTIAL — DONE, not deleted** | Classified **ACTIVE_BEHAVIOUR** and kept. The two genuinely stale artefacts around it are fixed (D-2, D-3). The core deletion is DEFERRED_BY_92_CASE_ENV. |
| P1-F Formal Release wait strategy | **REPORT_ONLY** | Investigated; the mechanism already shares `is_in_flight` and already fails fast on a missing run. Remaining change is policy. See F. |

Nothing in this round touched `release-artifacts.sh` behaviour. The two `release-artifacts.sh`
edits are a comment and a `note` string — no exit code, no branch, no file set changed.

## D. Proactive discoveries

Each is from the brief's §0.1 list and §14, found by auditing adjacent areas rather than by
working the item list.

**D-1 — Race checks have no usable non-push owner.** (class `duplicate owner` / `check in the
wrong layer`; CHANGED = YES, as recorded comments + a new check)
Census of every `-race` invocation: verify.yml races trafficclass, trafficsched, dialer,
direct, tun, route, adapter, and separately `option`. server-linux-amd64.yml and
client-macos.yml race masque/http/naive/route/httpclient (+trafficsched on macOS). The overlap
looks like duplication, but three measurements block a MOVE: the artifact workflows' race legs
sit behind a manual `deep_checks` input and never run on a push; five packages raced here are
raced nowhere else (`option` too); and the two sides race under different tag profiles
(`DEFAULT_BUILD_TAGS` vs `_OTHERS`, differing by exactly `with_naive_outbound`). So the correct
destination is one final-candidate gate that races the union — which does not exist — and
deleting here would remove coverage rather than relocate it. The table, the three facts and the
required owner are now in `verify.yml` beside the steps, and asserted by the checker.

**D-2 — A contract comment that contradicted the code beneath it.** (class `stale comment`;
CHANGED = YES)
`release-artifacts.sh` said Windows, Android and the IPA/DMG were "deliberately NOT here" and
that adding them "is a product decision, not a bug fix" — while the case statement 10 lines below
adds Windows and Android, and `release.yml` sets that switch true. A reader would conclude the
release ships two platforms. Corrected to state the real contract: five deliverables, four
workflow targets, a two-target default that exists for fixture focus only.

**D-3 — A green message describing a contract the run did not check.** (class `stale message`;
CHANGED = YES)
`note "PASS: both platforms verified against commit $sha"` printed a hardcoded "both" inside a
loop that runs two, four or five times. It now names the platforms it verified, derived from the
same `target_field` table that produced the checks. Proved for both set sizes by extracting
`target_field` from the real script.

**D-4 — Four structural checks named the wrong workflow.** (class `structural test protects the
wrong fact`; CHANGED = YES)
`check-release-workflows.py` labelled checks "verify workflow" while reading
`release-contract-verify.yml`; there was no constant for `verify.yml` at all. A reader asking
about the push gate got an answer about the release-tooling gate, and the P0-C duplication check
added last round asserted the wrong subject while reading as coverage. Renamed per subject and a
real `check_source_verify()` added — which is what now guards D-5 and P1-B.

**D-5 — The push gate's own header claimed it does not run the race suite.** (class `stale
comment`; CHANGED = YES)
`verify.yml`'s header said it "deliberately does not run the full unit suite, the race suite or
any artifact step". Two of three true; the race claim has been false since the race steps were
added. A header describing a cheaper gate than the workflow is is how a push gate grows without
anyone deciding to. It now states the gate's purpose and its 40-minute budget, and names the race
suite as something it runs.

**D-6 — An orphaned script that would have silently unpinned two clients.** (class `command exits
0 but the intent is not proved`; CHANGED = YES)
`.github/update_clients.sh` hard-reset each client submodule to `origin main`, then `git add`
with no read-back. Measured: **two of the three** pinned revisions are not ancestors of their
fork's `main` — android `ec61030d` and desktop `917d2a4e` — so the reset would have moved them
off the line this repository pins, desktop off `jiejiebox/main` entirely. And `git add
<submodule>` exits 0 whether or not the gitlink moved. Rewritten to read the branch from the
submodule's config, require ancestry (fail closed otherwise), and read the staged gitlink back
against the worktree HEAD. Exercised for real: it fast-forwarded apple with a proved read-back,
skipped the unchecked-out android, and failed closed on desktop naming the divergence; the index
and worktree were then restored and all three gitlinks re-verified.

**D-7 — Recorded, not fixed: the release helper suite still runs three times.** (class `duplicate
complete test`; CHANGED = NO)
Re-measured at the true tip: `verify.yml:64`, `release-contract-verify.yml:84`,
`release.yml:180`. Unchanged from §9; the assertions recording it are still in place, and P0-C is
still frozen.

## E. Local verification

Every command was run in this environment. `git` is MinGit 2.50.1 (portable, borrowed — nothing
installed), `python` is the bundled 3.12.14, `bash` is MinGit's `sh.exe` reporting
`bash 5.2.37(1)-release`.

| COMMAND | EXIT | RESULT | PROVES | DOES NOT PROVE |
|---|---|---|---|---|
| `python scripts/ci/test-desktop-release-version.py` | 0 | 60/60 | the version-injection contract, including all fail-closed cases | that a real Windows build stamps correctly |
| `bash scripts/ci/test-desktop-source-identity.sh` | 0 | 30/30 | gitlink identity against real git fixtures | that a runner's submodule checkout behaves identically |
| `python scripts/ci/check-release-workflows.py` | 0 | **114/114** | the structural guards, now including the P1-B filter and the source-gate checks | any shell behaviour |
| `bash scripts/ci/require-executed-coverage.test.sh` | 0 | 11/11 | the coverage guard's own falsifiability | — (needs no `jq`, which is why it runs here) |
| `bash -n` × 5 release-path scripts + `update_clients.sh` | 0 | syntax OK | the files parse | that they behave |
| `yaml.safe_load` × all 12 workflows | 0 | parse | YAML validity; the filters were then read back from the parsed documents | GitHub's filter engine (modelled separately, below) |
| GitHub filter model over 18 paths | 0 | 18/18 correct | `*.md` reaches root reports and **not** `docs/fork/*.md`, `go.mod`, `.gitmodules`, gitlinks, workflows or scripts | GitHub's implementation, only its documented semantics |
| `bash .github/update_clients.sh` (real run) | 1 | fails closed on desktop, fast-forwards apple with read-back | the ancestry guard and the read-back both work on real submodules | nothing about the other two clients' future pins |
| gitlink read-back, all three clients | 0 | HEAD == index == worktree | §15's hard gate | — |
| `git diff --check` | 0 | clean | no whitespace damage | — |
| `bash scripts/ci/test-release-artifacts.sh` | — | **NOT RUN** | — | anything on the release path |

`bash -n` is explicitly **not** treated as release-behaviour evidence, per §17.

## F. Blockers, precisely

```text
P0-C, P0-D, P1-D, P1-E-core, P1-F-behaviour
    BLOCKER = jq + chmod absent from this machine
```

Measured, not inferred: `test-release-artifacts.sh` stops at
`line 377: chmod: command not found`, and the suite's mock `gh` output is parsed with real `jq`.
Both were searched for across `<WORKSPACE_ROOT>`, `<LOCAL_HOME>`, `C:\Program Files` and
`C:\ProgramData` and are absent. `sha256sum` is also missing but is **not** a blocker — the suite
has a fallback at line 445. `tar` resolves to `C:\Windows\system32\tar.exe` (bsdtar), untested
against the suite.

Not "Windows is unsupported": `bash`, `git`, `python3`, `mktemp`, `sed`, `awk` and `jq`-free
suites all work here. It is exactly two binaries.

**P1-D needs one more thing named**, so the future patch has a scope rather than a hint. The
producer is already correct: `android-core-arm64.yml` stages exactly the four payload files in
`release-android/` and asserts that directory's contents. The separation cannot be completed
producer-only because:

```text
scripts/ci/release-artifacts.sh:448   requires EXACTLY ONE artifact subdirectory per target
                                      ("produced N artifact directories in $dest, expected exactly 1")
                                      -> a second artifact NAME fails cmd_download immediately
scripts/ci/release-artifacts.sh:970   locates the payload with  find -name <binary>
scripts/ci/release-artifacts.sh:436   derives <binary> from target_field per workflow
scripts/ci/test-release-artifacts.sh  fixtures encode the current single-artifact layout
```

So the future patch is: download the payload artifact and the evidence artifact by exact name in
`cmd_download`, keep the one-subdirectory invariant per artifact, update the fixtures, and add
the negative cases the brief lists (payload missing, wrong artifact name, evidence never staged,
unexpected payload file). All of it is inside the frozen file.

## G. Final state

```text
CURRENT_WINDOWS_SAFE_SCOPE_COMPLETE = YES

P0_A = DONE
P0_B = DONE
P0_C = BLOCKED_BY_ENVIRONMENT
P0_D = BLOCKED_BY_ENVIRONMENT

GITHUB_ACTIONS_RUN = NO
PUSHED_THIS_ROUND  = NO
PARENT_PUSHED      = NO
TAG_MOVED          = NO
RELEASE_CREATED    = NO

READY_FOR_FINAL_FREEZE_VALIDATION = NO
READY_FOR_NEXT_UNIX_RELEASE_TOOLING_STAGE = YES
```

`CURRENT_WINDOWS_SAFE_SCOPE_COMPLETE = YES` in the §20 sense: every CI / Verify / workflow
optimization this environment can reliably verify is done, nothing that needs the 92-case suite
was touched, and every remaining blocker is located to a named binary or a named file.

`READY_FOR_FINAL_FREEZE_VALIDATION` stays **NO** for the same reason as §10: P0-C and P0-D are
outstanding, so the release helper does not yet have a single test owner and candidate evidence
does not yet have one exact-SHA authority.

`READY_FOR_NEXT_UNIX_RELEASE_TOOLING_STAGE = YES`: a machine with `jq` and `chmod` can proceed
directly to P0-C, P0-D, P1-D and the P1-E core deletion, with D-1's owner table and F's patch
scope as the starting point.

Worktree: 3 pre-existing untracked `KNOWLEDGE/modules/*.md` —
`PRE_EXISTING_UNTRACKED / UNCHANGED`, per §16.

---
---

# V5 round

Two lines, strictly separated. **PART A is the construction line and it did not run** — the
environment gate failed, and V5 §2 says to stop rather than fabricate a Unix. **PART B is the
read-only full-chain audit**, which is where this round's work is.

Nothing in PART B was changed, per §25. Every finding below is recorded with evidence and a
recommended action, and `WRITE THIS ROUND? = NO` throughout.

---

# PART A — UNIX RELEASE TOOLING CONSTRUCTION

## A1. Environment

**V5 §2 requires a Unix/macOS/Linux environment with `jq`, `chmod` and `sha256sum`. This
environment is none of those, and cannot become one in scope.** Measured:

| | |
|---|---|
| OS | `Microsoft Windows NT 10.0.19044.0` |
| `IsWindows` / `IsLinux` / `IsOSX` | `True` / `False` / `False` |
| shell | PowerShell 5.1.19041.7725; POSIX shell only via a borrowed MinGit `sh.exe` (bash 5.2.37) |

The §2 tool list, as actually found:

| TOOL | PATH | VERSION |
|---|---|---|
| `bash` | **absent from PATH**; `<WORKSPACE_ROOT>\IOS客户端\_tools\mingit\usr\bin\sh.exe` satisfies it (reports `bash 5.2.37(1)-release`) | 5.2.37 |
| `git` | **absent from PATH**; MinGit portable `…\mingit\cmd\git.exe` | 2.50.1.windows.1 |
| `jq` | **ABSENT** | — |
| `chmod` | **ABSENT** | — |
| `sha256sum` | **ABSENT**; `shasum` also absent | — |
| `mktemp` | MinGit `usr\bin\mktemp.exe` | present |
| `sed` | MinGit `usr\bin\sed.exe` | present |
| `awk` | MinGit `usr\bin\awk.exe` | present |
| `grep` | `C:\Program Files (x86)\Embarcadero\Studio\37.0\bin\grep.exe` (Embarcadero's, not GNU) | present |
| `tar` | `C:\Windows\system32\tar.exe` (bsdtar, not GNU tar) | present |

A Unix environment was searched for rather than assumed:

```text
wsl.exe                 present, but NO distribution installed:
                        `wsl --list --quiet` exits 1 printing the --install help
C:\msys64               absent
C:\cygwin64 / C:\cygwin absent
scoop / chocolatey      absent
docker / podman / nerdctl / vagrant / busybox   absent
ssh                     present (no local target)
```

`jq` and `chmod` were also searched for by name across `<WORKSPACE_ROOT>`, `<LOCAL_HOME>`,
`C:\Program Files` and `C:\ProgramData` in the previous round and are absent there too.

## A2. Baseline gates

**Not run, because §2 makes the baseline a precondition of write work and the precondition
failed.** Recorded honestly rather than approximated:

| GATE | RESULT | WHY |
|---|---|---|
| `bash scripts/ci/test-release-artifacts.sh` (92-case) | **NOT RUN** | stops at `line 377: chmod: command not found`; its mock `gh` is parsed with real `jq` |
| `check-release-workflows.py` | **PASS** 114/114 | no `jq`, no `chmod` needed |
| `test-desktop-source-identity.sh` | **PASS** 30/30 | pure git + bash |
| `test-desktop-release-version.py` | **PASS** 60/60 | pure Python |
| `require-executed-coverage.test.sh` | **PASS** 11/11 | no `jq` needed |
| shell syntax (`bash -n`, 6 scripts) | **PASS** | parse only — explicitly **not** behaviour proof (§28) |
| YAML parse (12 workflows) | **PASS** | — |
| `git diff --check` | **PASS** | — |

No `jq` shim, no approximate `chmod`, no suite deletion, no `|| true`, no downgrade of the gate
to syntax-only. Those are §28's prohibitions and they are the reason this table has a NOT RUN row
instead of a fabricated PASS row.

## A3–A5. P0-C, P0-D, Android consumer

**Not attempted. `STOP WRITE WORK` as §2 directs.** No file in `release-artifacts.sh`,
`test-release-artifacts.sh`, `release.yml`, `verify-release-acceptance.sh` or
`android-core-arm64.yml` was modified this round. The state is exactly as the previous round
left it, and §4.1 of that round's report still describes it accurately.

## A6. Construction final state

```text
P0_C             = INCOMPLETE   (blocked: jq + chmod absent)
P0_D             = INCOMPLETE   (blocked: same)
ANDROID_CONSUMER = INCOMPLETE   (blocked: same; needs release-artifacts.sh)
```

One useful thing PART B did produce for this line: **F-3 explains why the Android consumer is
a contract-lag problem rather than a broken producer**, which narrows the future patch. See B10.

---

# PART B — READ-ONLY END-TO-END ARCHITECTURE AUDIT

## B1. The real DAG

Audited fact-first (§9), not file-first. `12` workflows exist; the DAG they form is:

```text
                    candidate SHA (a commit on testing)
                              │
   ┌──────────────────────────┴───────────────────────────┐
   │                                                      │
   ▼ push(br:testing)                                     ▼ workflow_dispatch (manual)
┌──────────────────────┐                    ┌──────────────────────────────────────────┐
│ Verify (40 min)      │                    │ server-linux-amd64  (120m)  ─┐           │
│  source assumptions  │                    │ client-macos        ( 60m)  ─┤ 4 TARGET   │
│  compileability      │                    │ windows-core-amd64  ( 60m)  ─┤ WORKFLOWS │
│  race ×2  ◄── only   │                    │ android-core-arm64  ( 60m)  ─┘           │
│   push-time race     │                    │ client-desktop-windows (120m) ─ DESKTOP  │
└──────────────────────┘                    │ interop-xray (40m) · client-apple (4 jobs)│
   │                                        │ mobile-contract (30m, +push)              │
   │                                        └──────────────────────────────────────────┘
   │ push(paths:18)                                            │
   ▼                                                           ▼
┌──────────────────────────────┐                    ┌──────────────────────┐
│ Release Contract Verification│                    │ Actions artifacts    │
│  (20m) 92-case + structure   │                    │  + BUILD-INFO record │
└──────────────────────────────┘                    └──────────────────────┘
   │                                                           │
   │            ┌──────────────────────────────────────────────┘
   ▼            ▼
┌────────────────────────────────────────────────────────────────────────┐
│ release.yml  (workflow_dispatch; contents: write; NO timeout-minutes)  │
│  check-tag → plan(select run per target) → download → verify → flatten │
│  → check-upload → [publish if !dry_run] → read back published set       │
└────────────────────────────────────────────────────────────────────────┘
                              │
                              ▼
                    GitHub Release (public assets)
```

Per-edge properties, measured:

| EDGE | TRIGGER | INPUT IDENTITY | OUTPUT | EVIDENCE | FAILURE MODE | PRIMARY OWNER | 2nd CHECKER |
|---|---|---|---|---|---|---|---|
| source → Verify | push `testing` (`paths-ignore: *.md`) | branch tip | red/green | run conclusion | superseded by a later push (`cancel-in-progress`) | `verify.yml` | `release-artifacts.sh` only for a release SHA |
| source → RCV | push `paths:18` + PR + dispatch | branch tip | red/green | run conclusion | **not consulted by any release** | `release-contract-verify.yml` | none |
| source → 4 targets | **manual dispatch** | `github.sha` of the dispatched ref | artifacts | run conclusion + record | never dispatched → release dies fast | each product wf | `release-artifacts.sh plan` |
| source → desktop | **manual dispatch** | `core_sha` + `tooling_sha` inputs | installer + 3 artifacts | BUILD-INFO + provenance | same | `client-desktop-windows.yml` | `cmd_desktop` |
| artifacts → plan | — | `--sha` + workflow id | TSV of run ids | `newest_run` query | wrong/stale run selected | `release-artifacts.sh` | 92-case suite |
| plan → publish | `dry_run` input | selected run ids | release | `gh release view` read-back | partial publish | `release.yml` | `check-upload` |

Two structural facts worth naming, because they are the shape of the whole chain:

1. **The release path's inputs are all manually dispatched.** Nothing in the DAG produces a
   release artifact automatically. `release-artifacts.sh plan` therefore does not *schedule* —
   it *audits that someone already did*. That is a good design and it is why the fail-fast on a
   missing run (`plan` dies immediately, not after the 5400s timeout) is the right call.
2. **Release Contract Verification is never consulted.** `release-artifacts.sh` resolves run
   identity for the four target workflows and the desktop workflow and for nothing else. A
   candidate SHA can be released with RCV never having run on it. See F-1.

## B2. Ownership matrix — one fact → one primary owner

`DUP` = is the same fact fully proven more than once? `COST` = rough runtime of the heaviest owner.

| FACT | PRIMARY OWNER | SECONDARY RECHECK | DUP | COST | RISK |
|---|---|---|---|---|---|
| source formatting | `verify.yml` gofmt | — | no | seconds | low |
| module integrity | `check-go-module-integrity.sh` (Verify) | `mobile-contract.yml`? no | no | ~1m | low |
| build-tag policy | `go test ./cmd/internal/...` (Verify) | product wf compile | no | ~2m | low |
| upstream assumptions | `verify-upstream-assumptions.sh` (Verify) | — | no | ~3m | low |
| compileability | `verify.yml` (3 build steps + matrix) | each product wf | **YES (partial)** | ~8m Verify | low — different tag profiles, intentional |
| **race** | `verify.yml` ×2 **(push)** | server/macos `deep_checks` **(manual)** | **YES, disjoint sets** | 30m timeout | **high — see F-4** |
| interop | `interop-xray.yml` (manual) | none | no | 40m | medium — never runs on push |
| release tooling correctness | `release-contract-verify.yml` | **`verify.yml:64` AND `release.yml:180`** | **YES ×3** | 20m each | medium — F-2 |
| candidate SHA identity | `release-artifacts.sh check-tag` | `client-desktop-windows.yml` gate 1 | yes, consistent | seconds | low |
| tag identity | `check-tag` (tag→commit) | desktop gate 1 (`refs/tags/v*`) | yes, consistent | seconds | low |
| version identity | `release/JIEJIE_VERSION` | `release-artifacts.sh release-version`, desktop gate 1/2, `inject-desktop-release-version.py` | **4 readers, 1 writer** | — | low — one authority, multiple readers is correct |
| workflow run identity | `newest_run` (release-artifacts.sh) | `verify-release-acceptance.sh` **(2nd authority)** | **YES** | — | **high — F-6** |
| artifact name identity | producer (`upload-artifact`) | `cmd_desktop` **by shape** | **partial** | — | medium — F-5 |
| artifact digest | `release-artifacts.sh verify` | `SHA256SUMS` written by flatten | yes, by construction | — | low |
| binary arch | `audit-windows-pe-architecture.py`, magic check | — | no | seconds | low |
| BUILD-INFO | `build-info.sh` (producer) | `verify` reads commit/sha256 | no | — | low |
| provenance | `--expect-core-sha` / `--expect-version` | desktop `verify-desktop-daemon-provenance.py` | yes | — | low |
| desktop core/tooling identity | `check-desktop-source-identity.sh` | `check-desktop-source-identity` structure check | no | seconds | low |
| Android payload contract | `android-core-arm64.yml` (asserts exact 4 files) | **none in consumer** | no | — | **med — producer is stricter than consumer; F-3** |
| public asset allowlist | `cmd_flatten` | `check-upload` | yes, two halves | — | low |
| published asset read-back | `release.yml` `gh release view` | — | no | — | low |

**Facts with NO real owner at all** (the second half of §11's question):

| FACT | STATUS |
|---|---|
| Push-time race coverage of `option`, `trafficclass`, `dialer`, `protocol/direct`, `protocol/tun`, `adapter` | owned by `verify.yml` only, and by nothing at final-candidate time |
| A candidate SHA having RCV evidence | **no owner** — nothing requires it |
| Interop on a release candidate | `interop-xray.yml` exists but is never referenced by the release path |

## B3. Identity chain, segment by segment

```text
tag ──► candidate SHA ──► run head_sha ──► run_id ──► artifact ──► digest ──► BUILD-INFO ──► binary ──► upload
```

| SEGMENT | MECHANISM | VERDICT |
|---|---|---|
| tag → candidate SHA | `check-tag`: shape-check, resolve, **prove the peeled commit == checked-out commit**; plus `refs/tags/v<version>` fetched and compared to `CORE_SHA` in desktop gate 1 | **hard, deterministic** |
| candidate SHA → run | `newest_run` filters `head_sha=$sha` **server-side**, then re-asserts `head_sha`/`workflow_id` per row, and `cmd_desktop` re-asserts `head_sha` | **hard** — and the `--paginate` + re-assert pair is what makes it hard |
| run → run_id | plan writes `run_id` to the TSV; every later step takes the id as an argument | **hard** — the id is the handle, never re-queried |
| run_id → artifact (4 targets) | `gh run download <run_id> --dir <dir>` + **exactly one** artifact subdirectory + `find -name <binary>` | hard on count, **shape-based on name** (F-5) |
| run_id → artifact (desktop) | lists the run's artifacts via API, selects the installer artifact **by shape** (name contains a version substring), asserts exactly one | **shape-based** (F-5) |
| artifact → digest | `sha256_of` recomputed at flatten; `SHA256SUMS` names bytes about to be published | **hard** |
| digest → BUILD-INFO | `verify` reads `artifact_sha256` from BUILD-INFO and compares | **hard** |
| BUILD-INFO → binary | commit / sha256 assertions per platform | **hard** |
| binary → upload | files named **explicitly**, no glob; published set read back with `gh release view` and compared | **hard** |

**No floating branch, no `latest`, no display-name match, no unvalidated fallback, and no
re-query after selecting a `run_id`** — searched for specifically. The one place a *version*
string rather than a SHA is used to name an artifact is the desktop upload, and the consumer
resolves the run by SHA and then reads the artifact list live, so the name is not load-bearing.
This chain is genuinely strong, and it is stronger than the previous round's summary implied.

## B4. Trigger / concurrency / cost map

| WORKFLOW | TRIGGER | FILTER | CONCURRENCY (group / cancel) | TIMEOUT | race | deep_checks |
|---|---|---|---|---|---|---|
| `verify.yml` | push + PR + dispatch | `paths-ignore: *.md` | `verify-<ref>-<event>` / cancel unless dispatch | 40m | 2 | 4 |
| `release-contract-verify.yml` | push + PR + dispatch | `paths: 18` | **NONE** | 20m | 0 | 0 |
| `server-linux-amd64.yml` | dispatch | — | `linux-amd64-…-<deep\|fast>` / **cancel=true** | 120m | 1 | 17 |
| `client-macos.yml` | dispatch | — | `macos-arm64-…-<deep\|fast>` / **cancel=true** | 60m | 1 | 12 |
| `windows-core-amd64.yml` | dispatch | — | **NONE** | 60m | 0 | 0 |
| `android-core-arm64.yml` | dispatch + push | `paths: 17` | `<wf>-<ref>-<event>` / cancel unless dispatch | 60m | 0 | 0 |
| `client-desktop-windows.yml` | dispatch | — | **NONE** | 120m | 0 | 0 |
| `interop-xray.yml` | dispatch | — | **NONE** | 40m | 0 | 0 |
| `client-apple.yml` | dispatch | — | **NONE** | 10m,120m×3 | 0 | 0 |
| `mobile-contract.yml` | dispatch + push | `paths: 24` | `<wf>-<ref>` / cancel=true | 30m | 0 | 0 |
| `release.yml` | dispatch | — | **NONE** | **NONE** | 0 | 0 |
| `client-desktop-windows-runtime-audit.yml` | `workflow_run` + dispatch | — | **NONE** | 90m | 0 | 0 |

Nine of twelve have no concurrency block. Five of the twelve are `workflow_dispatch`-only and
therefore never cancel anything — harmless. The four that matter are `release.yml`,
`release-contract-verify.yml`, `client-desktop-windows.yml` and `client-apple.yml`; see F-2, F-7.

Cost concentration: `verify.yml` is 40 minutes at 2 race steps; `server-linux` and
`client-desktop-windows` are 120 minutes each and both are dispatch-only.

## B5. Artifact producer / consumer map

| PLATFORM | PRODUCER | STAGED FILES | ARTIFACT NAME | RETENTION | CONSUMER | DOWNLOAD | PUBLIC OUTPUT |
|---|---|---|---|---|---|---|---|
| linux | `server-linux-amd64.yml` | dist dir | `$(basename $OUT)` | 14d | `release-artifacts.sh` | `gh run download <rid>` | `jiejie-sing-box-linux-amd64-<tag>.tar.gz` |
| macos | `client-macos.yml` | dist dir | `$(basename $OUT)` | 14d | same | same | `…-macos-arm64-<tag>.tar.gz` |
| windows core | `windows-core-amd64.yml` | dist | `windows-core-amd64-v<version>` | 30d | same | same | `…-windows-core-amd64-<tag>.tar.gz` |
| android | `android-core-arm64.yml` | `release-android/` + root | `android-core-arm64-<sha>` | **90d (default)** | same | same | `…-android-arm64-<tag>.tar.gz` |
| windows desktop | `client-desktop-windows.yml` | `release/` + records + unpacked | `jiejiebox-windows-x64-<CORE_VERSION>-signed-test` + `-build-records` + `-unpacked-audit-input` | 90d / 90d / **7d** | `cmd_desktop` | `--name <installer_artifact>` | `Jiejiebox-v<version>-windows-x64.exe` |
| audit | `…runtime-audit.yml` | records | `jiejiebox-windows-runtime-audit-records` | 30d | **nobody** | — | — |
| apple | `client-apple.yml` | libbox / ipa / dmg | `jiejiebox-libbox-<sha>`, then `<IPA_NAME>`/`<DMG_NAME>` | 30d | **nobody in release** | — | not a GitHub Release asset |
| interop | `interop-xray.yml` | reports | `interop-artifacts-<version>` | 14d | **nobody** | — | — |

Findings this table produces: retention is **three different values plus two defaults** (7d / 14d / 30d / 90d / 90d) with no stated policy (F-8); three producers have no consumer at all,
which is fine for audits and interop but means their artifacts are pure storage cost; and the
Android producer already emits payload and evidence as two filesets in one artifact, which is
F-3.

## B6. Fail-closed audit

Searched for each pattern §15 names, with the answer:

| PATTERN | FOUND? | WHERE / VERDICT |
|---|---|---|
| `rm -f` used as proof | no | 3 sites, all temp-file cleanup **after** the extracted bytes were read and asserted |
| `rm -rf` in release path | yes, 4 sites | `cmd_download`/`cmd_desktop`/`cmd_flatten` clearing their **own** output dirs before use — safe, and each is followed by a write and a later listing assertion |
| `\|\| true` | 1 site | `grep -c . "$out" \|\| true` in `cmd_plan` — the count is then compared to `${#TARGET_WORKFLOWS[@]}` and dies on mismatch. **Correct** |
| `continue-on-error` | deliberately absent | asserted by `check-release-workflows.py`; the audit workflow's header records the rejected earlier design |
| grep no-match then PASS | guarded | the SIGPIPE class has its own audit (`CI_PIPEFAIL_SIGPIPE_AUDIT.md`) and the `if ! bash -n` fix from the previous round |
| find no-match then exit 0 | **fixed in consumer, not producer** | `find -name "$binary"` result is used as a directory; a miss would make `tar -C` fail — fail-closed by accident rather than by assertion. F-5 |
| `git add` submodule without read-back | **was present** | `.github/update_clients.sh` — fixed in the previous round, now asserts staged gitlink == worktree HEAD |
| `gh run download` wrong artifact still yields files | **shape-based on desktop** | `cmd_desktop` selects the run by SHA and the artifact by name-shape, then requires exactly one; a renamed artifact fails. Acceptable, but it is identity-by-shape (F-5) |
| latest-run fallback | no | `newest_run` is SHA-filtered server-side; no `latest` anywhere |
| empty variable continues | guarded | `[ -n "${id:-}" ]`, `[ -n "$row" ]`, 8-field TSV count checks |
| missing `jq` silently falls back | **no fallback exists** | `jq` absence is a hard failure. That is the correct design and it is also why PART A is blocked |

Net: **the release path is genuinely fail-closed.** The residual risks are not suppressors —
they are identity-by-shape (F-5) and dual admissibility authority (F-6).

## B7. Structural checker audit

`check-release-workflows.py` is now 114 checks. Audited for §16's five failure modes:

| MODE | FINDING |
|---|---|
| label ≠ actual file | **WAS PRESENT, fixed last round.** Four checks labelled "verify workflow" read `release-contract-verify.yml`. Now separated into `check_release_contract_workflow()` and `check_source_verify()`, and `SOURCE_VERIFY` exists for the first time |
| string grep protects the wrong owner | **WAS PRESENT, fixed last round.** The tag-to-commit proof moved into `check-desktop-source-identity.sh`, so the checker now asserts the *helper that owns it* still contains the proof, instead of asserting the workflow re-implements it |
| a comment can satisfy the checker | **still possible in principle.** `code_only()` strips whole-line comments but leaves trailing comments, and several checks are `needle in text` on the raw file rather than on `code_only(text)`. See F-9 — the specific reachable case is narrow |
| checker protects abandoned architecture | no — audited each assertion; the known-defect lines are labelled `KNOWN DEFECT` and are designed to fail when the defect is fixed |
| checker does not cover the real source workflow | **WAS PRESENT, fixed last round** — `verify.yml` had no checks at all; it now has 9 |

## B8. Final-candidate deep verification — the gap, designed

§18 asks whether a Final Candidate Verification workflow is needed. Audited answer: **yes, and
the gap is narrower and more specific than "Verify has race".**

What a candidate SHA can currently be released with, measured against the DAG:

| REQUIRED BY POLICY | ACTUALLY REQUIRED BY `release-artifacts.sh` |
|---|---|
| Verify success at the SHA | **no** |
| Release Contract Verification success at the SHA | **no** |
| interop at the SHA | **no** |
| race coverage at the SHA | **no** (product race legs are behind manual `deep_checks`; push race is not consulted) |
| the 4 target workflow runs succeed at the SHA | yes |
| the desktop run succeeds at the SHA | yes |
| artifact digest / arch / BUILD-INFO / allowlist | yes |

So the release gate is **complete about artifacts and silent about verification**. That is a
coherent design only if "the artifact workflows ran" is accepted as sufficient source evidence —
and for the manual, dispatch-only product workflows it largely is, because each one builds from
the SHA and asserts its own record. The genuine hole is narrower: **nothing ties the release to
Verify or RCV, so the two push-time gates are advisory with respect to publishing.**

Design (not built, per §18's "先给设计"):

```text
name:   Final candidate verification
trigger: workflow_dispatch only (inputs: candidate_sha, tooling_sha == candidate_sha)
builds: nothing - it must not produce artifacts
runs:   the UNION of release-blocking deep checks, once, at the frozen SHA:
          - race over the union set (verify's 7 packages + option + server's + macos's)
          - interop-xray's suites
          - (optionally) RCV at this SHA, if RCV's own evidence is to be required
evidence: one run id, exact SHA, exact workflow, completed + success
consumer: release-artifacts.sh gains a required-evidence rule naming this workflow
why one owner: the union is the point. Today the union is provable by NO single run.
```

What must NOT go in it: anything a product workflow already proves by building (compileability
under each platform's tag profile, artifact digests, BUILD-INFO), because copying those would
create the third parallel proof §8.2 warns about.

## B9. Drift surfaces, scored

| DRIFT SURFACE | COUNT | SITES | SCORE | WHY |
|---|---|---|---|---|
| platform/target table | **1** | `target_field` is the single authority; `TARGET_WORKFLOWS` gates it | **low** — this is the model to copy |
| workflow-file literals | 4 target wfs named in `release-artifacts.sh` + 1 desktop + 12 in `check-release-workflows.py` | `resolve_workflow_id` hard-asserts path == file, so a rename fails loudly | **low** |
| admissibility authority | **2** | `release-artifacts.sh` `newest_run` vs `verify-release-acceptance.sh` | **high — F-6** |
| race coverage sets | **3** | verify / server / macos, all different | **high — F-4** |
| artifact retention values | **5** | 7d, 14d(×4), 30d(×4), 90d(×2+2 defaults) | **medium — F-8** |
| `version` readers | 4 | `release-version`, desktop gate 1, gate 2, injector | **low** — one writer |
| concurrency declarations | 3 of 12 | rest absent | **medium — F-2/F-7** |
| `.md`-derived release inputs | 3 | `verify-fork-handoff.sh` parses 3 `docs/fork/*.md` | **medium — F-10** |

## B10. Newly discovered "奇妙点"

Ordered by priority. Nothing here was written (§25).

---

**F-1 — The release path never consults either push-time gate.**
`PRIORITY = P0 (policy) · ACTION = FIX`

- **WHY SURPRISING** — `verify.yml` and `release-contract-verify.yml` both exist to prove things
  about a commit, and both are triggered by a push to `testing`. The release, whose entire job is
  "promote bytes that CI already proved", consults **neither**. It resolves run identity for the
  four product workflows and the desktop workflow, and for nothing else.
- **EVIDENCE** — `release-artifacts.sh:69-86` defines `TARGET_WORKFLOWS` (server, macos, +windows,
  +android under `RELEASE_EXTRA_PLATFORMS`); `cmd_desktop` handles the fifth. Exhaustive grep of
  `release-artifacts.sh` for `verify.yml` / `release-contract-verify.yml` / `verify` finds only
  `cmd_verify` (its own subcommand) and the word "verified" in messages. `cmd_plan`'s die message
  lists only target workflows.
- **RISK** — a candidate SHA whose Verify was `cancelled` by a later push, or whose RCV never ran,
  is publishable with a green release run. The previous round's report already noted that a
  superseded Verify fails closed *for the release candidate* — but that is true only because
  `verify-release-acceptance.sh` checks it, and that script is **not in the release path either**.
- **RECOMMENDED ACTION** — add a required-evidence rule to `release-artifacts.sh` naming
  `verify.yml` and `release-contract-verify.yml` at the candidate SHA, reusing `newest_run`. This
  is exactly P0-C's "Formal Release proves tooling was already proved at this exact SHA", and it
  closes F-1 as a side effect.
- **WRITE THIS ROUND? = NO**

---

**F-2 — Nothing stops a second release run from cancelling the first.**
`PRIORITY = P1 · ACTION = FIX`

- **WHY SURPRISING** — `verify.yml` has a carefully reasoned concurrency block: cancel a
  superseded push, never cancel a dispatch. `server-linux-amd64.yml` and `client-macos.yml`
  carry `cancel-in-progress: true` **unconditionally**. `release.yml` has **no concurrency block
  at all, and no `timeout-minutes`**. Two distinct failure modes follow, and the second is the one
  that actually bites:
  - two `release.yml` dispatches for the same tag run **concurrently**, both holding
    `contents: write`, both able to publish;
  - a later `server-linux-amd64` or `client-macos` dispatch for the same ref **cancels** an
    in-flight run. If that run was the release candidate's, `plan` then learns
    `conclusion=cancelled` — correctly, and after having spent its wait window discovering it.
- **EVIDENCE** — `verify.yml:39-41` vs `server-linux-amd64.yml:97` and `client-macos.yml:82`
  (`cancel=True`) vs `release.yml` having no `concurrency:` key. `release.yml` also has no
  `timeout-minutes` while every other job has one (10–120m), so a stuck release run is unbounded
  and holds `contents: write`.
- **RISK** — two concurrent `release.yml` runs publishing the same tag; or a release waiting 90
  minutes on evidence another dispatch is about to cancel.
- **RECOMMENDED ACTION** — give `release.yml` a concurrency group keyed on the tag with
  `cancel-in-progress: false` (serialise releases; never cancel one), and a `timeout-minutes`
  above the helper's maximum bounded wait.
- **WRITE THIS ROUND? = NO**

---

**F-3 — The Android producer is already stricter than its consumer, so P1-D is contract lag.**
`PRIORITY = P1 · ACTION = MERGE`

- **WHY SURPRISING** — the previous round reported P1-D as a producer/consumer *split* that the
  backlog still had to perform. Measured, the split has already happened on the producer side and
  the *consumer* is the lagging side: `android-core-arm64.yml` stages exactly four payload files
  in `release-android/` and **asserts that directory is exactly those four**, while uploading
  `libbox.provenance` and `SHA256SUMS.txt` beside it in the same artifact. So the artifact the
  release consumes is *broader* than the producer's own declared payload contract.
- **EVIDENCE** — `android-core-arm64.yml:388-393` (expected_stage == actual_stage, four names) and
  `:418-431` (upload `path:` lists six, including `libbox.provenance` and `SHA256SUMS.txt`).
  Consumer side: `release-artifacts.sh:448` requires exactly one artifact subdirectory, `:970`
  finds the binary by `find -name`.
- **RISK** — the publishable tarball is built from a directory that contains evidence files, and
  the only thing keeping them out is the tar file-list assertion. A producer that adds a seventh
  file ships it unless flatten's assertion catches it.
- **RECOMMENDED ACTION** — treat the producer's four-name assertion as the payload contract and
  make the consumer's download exact-name. This is P1-D, and it is smaller than the backlog
  implied: the producer needs a second upload step, not a restructure.
- **WRITE THIS ROUND? = NO**

---

**F-4 — Release-blocking race coverage depends on a manual input that no release sets.**
`PRIORITY = P1 · ACTION = MOVE`

- **WHY SURPRISING** — both product workflows *have* race steps, and their headers say so. Every
  one is gated `if: github.event_name == 'workflow_dispatch' && inputs.deep_checks`, and
  `deep_checks` is declared in only those two workflows. So the artifact-workflow race legs run
  **only when a human ticks a box**, and `release-artifacts.sh` cannot see whether they did —
  a run with `deep_checks=false` and a run with `deep_checks=true` are both `success`.
- **EVIDENCE** — `server-linux-amd64.yml:81` (`deep_checks:` input), `:681-682`
  (`if: … && inputs.deep_checks`), `client-macos.yml:67`, `:351-352`. Grep for `deep_checks`
  across all 12 workflows returns hits in only those two files.
- **RISK** — the only race coverage that provably ran at the candidate SHA is `verify.yml`'s,
  and `verify.yml` is not consulted by the release (F-1). So a released SHA can have **zero**
  verified race coverage.
- **RECOMMENDED ACTION** — this is the same destination as B8: one final-candidate owner running
  the union. Until it exists, the honest options are to make `deep_checks` recorded-and-required
  evidence, or to accept and document that race is advisory. Do not delete `verify.yml`'s steps
  (previous round's conclusion stands).
- **WRITE THIS ROUND? = NO**

---

**F-5 — Two artifacts are selected by name *shape*, not by identity.**
`PRIORITY = P1 · ACTION = SIMPLIFY`

- **WHY SURPRISING** — the identity chain is otherwise SHA-hard all the way through, and the
  previous round's summary said so. The exception is the artifact *name* layer: for the four
  target workflows the consumer finds the binary with `find -name <binary>`, and for the desktop
  installer `cmd_desktop` selects among the run's artifacts **by shape** (`jiejiebox-windows-x64-
  <version>-signed-test`), explicitly because the v0.1.6 artifact name carried a *stale* version
  and matching on version found nothing. Choosing shape over version was right; the residual
  issue is that nothing asserts the selected artifact is the *only* installer-shaped one, beyond
  a count check.
- **EVIDENCE** — `release-artifacts.sh:970` (`src="$(dirname "$(find "$dir" -type f -name "$binary")")"`),
  `:556-572` (the "selected by SHAPE, not by version" comment and the count assertion).
  Artifact names: three of the five desktop/target artifacts are constants
  (`jiejiebox-windows-build-records`, `jiejiebox-windows-unpacked-audit-input`,
  `jiejiebox-windows-runtime-audit-records`) and one is version-derived.
- **RISK** — low for correctness today (the run is SHA-selected and the count is asserted), but
  it means the artifact *name* is not an identity, so a producer-side rename is caught only by a
  count or a find miss rather than by a contract. F-3 is the concrete instance.
- **RECOMMENDED ACTION** — extend `target_field` with a `payload_artifact` name and select by
  exact name, which is what P1-D's contract needs anyway.
- **WRITE THIS ROUND? = NO**

---

**F-6 — Two authorities still decide "may this SHA be released".**
`PRIORITY = P0 (drift) · ACTION = MERGE`

- **WHY SURPRISING** — not the duplication itself (the backlog knows it) but the *asymmetry*:
  `verify-release-acceptance.sh` implements a strict admissibility rule (exact SHA, exact workflow,
  completed, per-job success, declared artifacts with recorded digests, fail-closed on 18
  snapshots) and **is never called by `release.yml`**. The release uses `newest_run` instead. So
  the stricter authority is the one nobody runs, and the two disagree by construction: `newest_run`
  takes the newest run and dies if it is not success; `verify-release-acceptance.sh` demands a
  complete successful run *and* every declared artifact.
- **EVIDENCE** — grep for `verify-release-acceptance.sh` across workflows and scripts: hits in
  `verify-release-acceptance-test.sh` (its own self-test), `verify-fork-handoff.sh` (a note), and
  `verify.yml` (**comments only** — the step runs `verify-release-acceptance-test.sh`, the
  self-test, not the gate). No hit in `release.yml` or `release-artifacts.sh` as a call.
- **RISK** — the strict rule drifts away from the enforced rule, and the self-test keeps passing
  for a gate nothing invokes.
- **RECOMMENDED ACTION** — P0-D, with F-1: make `release-artifacts.sh` the single selector
  (`select_exact_sha_run` / `require_exact_sha_success`) and have it carry the per-job and
  per-artifact requirements that `verify-release-acceptance.sh` encodes, then retire that script's
  independent verdict.
- **WRITE THIS ROUND? = NO**

---

**F-7 — Four expensive workflows accept unlimited concurrent dispatches.**
`PRIORITY = P2 · ACTION = FIX`

- **WHY SURPRISING** — `android-core-arm64.yml` and `mobile-contract.yml` (the two that can be
  push-triggered) both carry well-reasoned concurrency groups, including the
  "dispatch is never cancelled" refinement. The four *most expensive* ones —
  `client-desktop-windows.yml` (120m), `server-linux-amd64.yml` (120m), `client-apple.yml`
  (4 jobs, 3×120m), `interop-xray.yml` — either have no group or cancel unconditionally.
- **EVIDENCE** — B4 table. `client-desktop-windows.yml`, `client-apple.yml`, `interop-xray.yml`,
  `windows-core-amd64.yml`, `release.yml`, `release-contract-verify.yml`: no `concurrency:` key.
- **RISK** — cost and artifact-name collision (two desktop dispatches for one version produce two
  artifacts with the same version-derived name in different runs — harmless to the consumer,
  confusing to a human reading the Actions list).
- **RECOMMENDED ACTION** — adopt `verify.yml`'s pattern (ref + event, cancel only for non-dispatch)
  in the workflows that build a release artifact.
- **WRITE THIS ROUND? = NO**

---

**F-8 — Retention is five different values with no stated policy, and one is 7 days.**
`PRIORITY = P2 · ACTION = SIMPLIFY`

- **WHY SURPRISING** — the desktop workflow uploads `jiejiebox-windows-unpacked-audit-input` with
  `retention-days: 7` while the audit workflow that *consumes* unpacked input keeps its output for
  30 days, and the other two desktop artifacts fall to GitHub's 90-day default. Nothing states
  which number a release depends on.
- **EVIDENCE** — B5 table: 7d (unpacked audit input), 14d (linux, macos, interop, RCV logs,
  release self-test), 30d (windows core, apple, runtime-audit), 90d default (android, desktop
  installer, desktop records).
- **RISK** — a release attempted from a candidate older than 14 days cannot download its Linux or
  macOS artifact, and the failure surfaces as `gh run download failed` — attributed to the
  download rather than to expiry. `.expired` is read for the desktop artifacts
  (`release-artifacts.sh:567-568`) but **not** for the four target workflows.
- **RECOMMENDED ACTION** — state one policy ("≥ release cadence"), apply it, and read `.expired`
  for every target so the failure names expiry.
- **WRITE THIS ROUND? = NO**

---

**F-9 — The structural checker's "code only" guard does not cover trailing comments.**
`PRIORITY = P2 · ACTION = SIMPLIFY`

- **WHY SURPRISING** — `code_only()` exists precisely because a comment once satisfied a check
  (the audit workflow's header explaining the rejected `continue-on-error` design). It strips
  whole-line comments, and its docstring says leaving trailing comments is deliberate. But several
  `needle in text` checks run against the **raw** `text`, not `code_only(text)`, so a workflow
  could satisfy "the workflow calls X" with a comment mentioning X.
- **EVIDENCE** — `check_release_contract_workflow()` and `check_source_verify()` use both `text`
  and `body`; the race-present checks use `text`; `check_verify`'s older siblings used `body`.
- **RISK** — low today (the assertions that matter most use `body`), but it is the same class of
  defect that already produced one false check in this file.
- **RECOMMENDED ACTION** — decide per assertion whether a comment should satisfy it, and use
  `code_only` for every "the workflow does X" claim.
- **WRITE THIS ROUND? = NO**

---

**F-10 — Three release inputs are Markdown files parsed with `sed`/`grep`.**
`PRIORITY = P1 · ACTION = OBSERVE`

- **WHY SURPRISING** — the previous round's `paths-ignore: *.md` work established that
  `docs/fork/*.md` is machine-read, which is unusual enough that it survived a "we could ignore
  docs/**" simplification. What is worth naming separately is *which* facts live in prose: a
  `<!-- release-verdict: ... -->` marker in `v016-final-release-verdict.md` is a **release input**
  extracted by regex, and the candidate manifest is grepped for the literal string
  `not frozen yet`.
- **EVIDENCE** — `verify-fork-handoff.sh:42-44` (three `*_MD` defaults), `:278` (`sed -n` marker
  extraction), `:313-341` (verdict marker + `grep -q 'not frozen yet'`).
- **RISK** — a documentation edit changes release admissibility, and no test covers the marker's
  grammar. It is a second, prose-shaped authority beside `release/JIEJIE_VERSION`.
- **RECOMMENDED ACTION** — OBSERVE. If it ever needs to change, the right move is to move the two
  machine-read facts into a data file (JSON/env) and leave the prose as commentary. Not now: the
  gate is strict and fails closed on a missing marker.
- **WRITE THIS ROUND? = NO**

---

**F-11 — `interop-xray.yml` and the runtime audit are not reachable from the release.**
`PRIORITY = INFO · ACTION = OBSERVE`

- **WHY SURPRISING** — read-only audit result, recorded because it bounds what a "green release"
  claims: interop has a workflow, the Windows runtime audit has a workflow with `workflow_run`
  automation, and neither is named anywhere in the release path. The audit workflow says so about
  itself in its own header ("NOT release-blocking, by product definition"), which is honest and
  correct; interop says nothing either way.
- **EVIDENCE** — B1/B5 tables; `client-desktop-windows-runtime-audit.yml:36-40`.
- **RISK** — none by itself; the risk is a reader assuming a green release implies interop passed.
- **RECOMMENDED ACTION** — OBSERVE; if interop is meant to gate, it belongs in B8's candidate
  workflow.
- **WRITE THIS ROUND? = NO**

---

## B11. What was refuted

Recorded so the audit is not read as one-sided. Three hypotheses were tested and **failed**:

| HYPOTHESIS | RESULT |
|---|---|
| "`newest_run` mis-selects across re-runs / stale green" | **REFUTED.** `stale_green` (latest failed, older green → FAIL) and `reruns` (three runs → newest selected) are both covered cases in the 92-case suite |
| "The four Windows runtime gates are proven twice, once non-blocking and once gating" | **REFUTED.** The build workflow keeps only gate 4 (signing) and gate 3 (package/arch); gates 5/5b/6/7 live solely in the audit workflow. The split is clean |
| "Workflow-file literals are a drift surface because three named workflows are not on disk" | **REFUTED.** `build.yml`, `moved-linux.yml`, `unrelated.yml` are historical ledgers and deliberate negative fixtures; `resolve_workflow_id` hard-asserts path == file |

## B12. Final state

```text
UNIX_RELEASE_TOOLING_SCOPE_COMPLETE = NO

P0_C             = INCOMPLETE      (BLOCKED: jq + chmod absent)
P0_D             = INCOMPLETE      (BLOCKED: same)
ANDROID_CONSUMER = INCOMPLETE      (BLOCKED: same; needs release-artifacts.sh)

FULL_92_CASE_RELEASE_SUITE  = NOT RUN   (cannot execute: jq + chmod absent)
READ_ONLY_FULL_CHAIN_AUDIT  = COMPLETE

GITHUB_ACTIONS_RUN = NO
PUSHED_THIS_ROUND  = NO
TAG_MOVED          = NO
RELEASE_CREATED    = NO

READY_FOR_FINAL_FREEZE_VALIDATION = NO
```

`READY_FOR_FINAL_FREEZE_VALIDATION` is **NO** for the §32 reason: P0-C, P0-D and the Android
consumer are incomplete, so the three required suites cannot all be green — indeed the 92-case
suite cannot be run at all here.

`FULL_92_CASE_RELEASE_SUITE = NOT RUN` rather than `FAIL`: it did not fail, it could not start,
and recording it as FAIL would be as wrong as recording it as PASS.

Audit artifacts unchanged this round: **11 local commits, none pushed**, worktree clean apart
from the 3 pre-existing untracked `KNOWLEDGE/modules/*.md` (`PRE_EXISTING_UNTRACKED / UNCHANGED`,
per §29). No file named in PART B was modified.
