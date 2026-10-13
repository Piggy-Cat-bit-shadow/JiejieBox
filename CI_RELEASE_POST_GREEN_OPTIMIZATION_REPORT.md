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

## 4a. The desktop pin on the true remote is a dangling commit

This is the most consequential finding of the round, and it only became visible after the
stale ref was corrected. It is also the finding this report first got **wrong in the alarmist
direction**, so the correction is stated first.

### What is NOT wrong

The true remote tip does pin the fork — commit `b462bbfb0`, "chore(submodule): pin maintained
desktop fork", which also carries the `.gitmodules` URL change this round was asked to make. It
pins **`18ef1ef6`**, not `917d2a4`. `18ef1ef6`'s `src/main/updates.ts` is still upstream's
(`SagerNet/sing-box` feed, `startsWith("SFW-")` asset matcher).

An earlier draft of this section concluded from that alone that `testing` would ship a client
whose updater polls the wrong feed. **That conclusion was wrong, and the measurement that
refutes it is in §6:** the parent overlay is still wired into `prepare-desktop-client.sh` at
the true tip, and run against `18ef1ef6` it *does* rewrite those two lines:

```text
overlay vs 18ef1ef6 (18ef1ef6)
  version.json: version.json application version -> 0.1.5 (from release/JIEJIE_VERSION)
  wrote: src/main/updates.ts
```

So at that pin the build supplies the updater contract, and the shipped client is correct. The
remote's arrangement works.

### What IS wrong

```text
git merge-base 18ef1ef6 917d2a4                     -> (empty: no common ancestor)
git merge-base --is-ancestor 18ef1ef6 917d2a4       -> exit 1
git merge-base --is-ancestor 917d2a4 18ef1ef6       -> exit 1
GitHub compare 18ef1ef6...jiejiebox/main           -> status=ahead ahead_by=1
GitHub compare 18ef1ef6...main                     -> status=diverged ahead=8 behind=1
GitHub compare 18ef1ef6...dev                      -> status=diverged ahead=9 behind=1
```

```text
18ef1ef6  "feat: preserve Jiejiebox Windows build overlays"           (no branch contains it)
917d2a4  "feat(updates): own the Jiejiebox release feed and installer matcher"  = jiejiebox/main
```

`18ef1ef6` is reachable from **no fork branch**. `jiejiebox/main` — the branch `.gitmodules`
names as this submodule's source — is one commit *ahead* of it, and `main`/`dev` diverge from
it. It is a dangling commit:

1. **The pin is one garbage collection away from being unfetchable.** A submodule gitlink no
   branch reaches cannot be relied on to resolve for a fresh clone, so a release built from it
   is not reproducible from the fork's own refs. This is a real fragility even though today's
   builds succeed.
2. **The arrangement is self-inverting.** `917d2a4` is the same fork with the updater contract
   committed, and the overlay is *only* able to supply that contract because `18ef1ef6` lacks
   it. The rule engine requires each rule's **original, pre-branding** text and fails closed
   otherwise. So the day the pin is moved to the branch `.gitmodules` names — the obvious
   maintenance action, and the one the brief's §1.2 asks for — the overlay's two `updates.ts`
   rules find no anchor and the build **fails closed**:

   ```text
   overlay vs 917d2a4 (917d2a4e)
     version.json: version.json application version -> 0.1.5 (from release/JIEJIE_VERSION)
     nothing to write: the client already carries this overlay
   ```

   reaching "nothing to write" only because the branded text is already there. The two are
   mutually exclusive states of one file, and the pin and the overlay are `git add`-apart from
   a configuration that cannot build.

3. **The pin is behind the branch the brief names.** §1.2 says the two static rules were
   migrated into the fork at `917d2a4` and that the gitlink should be `917d2a4`. The remote's
   `b462bbfb0` did the `.gitmodules` half and pinned a different, earlier commit.

### What this round did, and why it is the one edit worth reviewing

Resolved the gitlink conflict to **`917d2a4`**, the tip of `jiejiebox/main`, because:

* it is the revision `.gitmodules` names as the source, and it is on that branch;
* it is a **strict superset** of the remote's pin — `git diff 18ef1ef6 917d2a4` is one file,
  `src/main/updates.ts`, 18 insertions and 2 deletions, and nothing else;
* committing the updater contract is what lets the overlay be retired, which is P0-A's stated
  goal (a rule set that asserts a shape its own pinned source already satisfies is the
  redundancy the brief exists to remove);
* and it removes the dangling-commit fragility.

The two gitlinks are proved, not assumed, exactly as §1.5 requires:

```text
desktop   HEAD 917d2a4e9864528e1c10c4c6a39f3949b6f5d878 == parent index gitlink   MATCH
dashboard HEAD 3d32338fdc35b050cd9728afa7f66c18206e4fb9 == client index gitlink   MATCH
```

**For the owner.** This is the one change in this round that alters what `testing` ships, in
the opposite direction from the remote's own `b462bbfb0`, and it should be reviewed first
rather than trusted. The honest summary of the trade: **both pins produce a correct client
today; they differ in whether the correctness lives in the pinned source or in a parent-side
rewrite.** `917d2a4` + no overlay puts it in the source and makes it reproducible; `18ef1ef6` +
overlay keeps it in the rewrite and depends on a commit no branch reaches. If the owner prefers
the remote's arrangement, the revert is the single gitlink line, and P0-A's overlay retirement
must then be dropped as well — the two choices cannot be mixed, which is the point of the
inversion in (2).

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
| overlay vs `917d2a4` (`jiejiebox/main`) | `24 applied/confirmed` → **`nothing to write`**, probe tree CLEAN |
| overlay vs `18ef1ef6` (the true remote's pin) | `24 applied/confirmed` → **`wrote: src/main/updates.ts`**, probe tree DIRTY |

The second row is the load-bearing one, and it is why §4a was rewritten. The overlay does not
report the same thing on the two pins: on the branch pin it finds the updater contract already
committed and writes nothing; on the dangling pin it supplies that contract at build time,
because that pin kept upstream's `updates.ts`. Retirement is therefore justified by the first
row — the fork branch already carries every rule — and the second row is the evidence that the
two configurations are alternatives, not complements. The procedure, for `917d2a4`:

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
| **The desktop pin on the true remote is a dangling commit** (`18ef1ef6`) reachable from no fork branch, one commit behind the `jiejiebox/main` that `.gitmodules` names | `git merge-base` empty; GitHub compare vs `jiejiebox/main` = ahead 1, vs `main` = diverged, vs `dev` = diverged | **YES** — re-pinned to `917d2a4` in P0-A | **The one edit in this round that changes what `testing` ships. Review it first.** Both pins build a correct client today (the overlay supplies the updater contract on the dangling pin); they differ in whether that correctness lives in the pinned source or in a parent-side rewrite, and the dangling pin risks becoming unfetchable |
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
