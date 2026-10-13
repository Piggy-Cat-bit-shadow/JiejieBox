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
Both were searched for across `C:\Deepseek`, `C:\Users\Jie`, `C:\Program Files` and
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

