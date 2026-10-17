# CI_RELEASE_POST_GREEN_OPTIMIZATION_REPORT

## A. Baseline

```text
START_HEAD                 = 9bdcf8e8b17b67a2c0fd306d2cc865535dc6970e
WORKTREE_STATUS            = clean (staged 0 / unstaged 0 / untracked 0)
UNRELATED_USER_CHANGES_PRESERVED = N/A - there were none to preserve
GITHUB_ACTIONS_RUN         = NO
PUSHED                     = desktop fork only (see C.2); parent NOT pushed
TAG_MOVED                  = NO
RELEASE_CREATED            = NO
```

Carried forward from the brief without re-deriving: the source migration itself is complete and
fresh-clone-verified; the sing-tun sync landed at `9bdcf8e8b` touching only `go.mod`/`go.sum`; the
release design direction, `release-artifacts.sh`'s fail-closed set, and the 92/92 gate self-test are
all correct and are **not** to be weakened.

---

## B. Ownership before/after

| FACT | BEFORE OWNER(S) | AFTER PRIMARY OWNER | SECONDARY CHECK | ACTION |
|---|---|---|---|---|
| desktop static branding rules | parent overlay (`apply-desktop-branding-overlay.py`) **+** fork, split 21/2 | **desktop fork source** | none needed; `updates.ts` is source now | **DONE** — the 2 stragglers migrated |
| desktop update feed / asset matcher | parent overlay only | **desktop fork** (`src/main/updates.ts`) | — | **DONE** |
| desktop release version | parent overlay, anchored on the literal `1.14.2` | `release/JIEJIE_VERSION` (intended) | — | **NOT DONE** — see D.1 |
| release helper 92-case self-test | Verify + Release Contract Verify + Formal Release (3 owners) | Release Contract Verification (intended) | cheap smoke in Verify | **NOT DONE** — see D.2 |
| Release-acceptance authority | `verify-release-acceptance.sh` **and** `release-artifacts.sh` (parallel) | one (intended) | — | **NOT DONE** — see D.3 |
| Windows Desktop tooling identity | floating `RELEASE_BRANCH`/`testing` | exact, `== CORE_SHA` (intended) | build record | **NOT DONE** — see D.4 |
| Android payload / evidence boundary | one artifact, release-side cleanup | split artifacts + exact `--name` selection | — | **NOT DONE** — see D.5 |
| `RELEASE_EXTRA_PLATFORMS` | history switch, always true | canonical target table | — | **NOT DONE** — see D.6 |
| Verify's race legs | Verify (push-time) | product workflows / deep evidence | — | **NOT DONE** — see D.7 |

---

## C. What was actually changed (committed locally)

### C.1 Overlay rule migration matrix — built, not guessed

The 715-line overlay declares **23 `Rule()` entries**. Each was extracted by AST and checked
against the fork source by literal match. Result, and the correction of a first-pass error:

```text
ALREADY_NATIVE      21
MISSING_FROM_FORK    2      both in src/main/updates.ts
```

A naive substring pass initially reported 6 `BOTH_PRESENT` and 5 `NEITHER`, both artefacts:

- `BOTH_PRESENT` (electron-builder `executableName`, `package.ts` `signing.local.json`,
  `installer.nsh` `ciDiagnostic`, dashboard `App.tsx`) — the "old" string is a substring of the
  "new" one, or both occur legitimately at different lines. Verified line by line; all native.
- `NEITHER` (5 dashboard rules) — the dashboard submodule is not checked out in a bare
  `desktop` clone. Verified separately against `clients/desktop/dashboard`: all native.

So the real migration was **2 rules, not 23 and not 7**.

### C.2 `Piggy-Cat-bit-shadow/sing-box-for-desktop` — the 2 missing rules migrated

`917d2a4e9864528e1c10c4c6a39f3949b6f5d878` on `jiejiebox/main`, **1 file**, `src/main/updates.ts`:

```text
RELEASES_URL   https://api.github.com/repos/SagerNet/sing-box/releases
            -> https://api.github.com/repos/Piggy-Cat-bit-shadow/sing-box/releases

asset matcher  startsWith("SFW-")
            -> startsWith("Jiejiebox-v")
```

They belong together and the reasoning is in the commit: the installer this fork produces is
`Jiejiebox-v<version>-windows-<arch>.exe`, so fixing only the URL leaves the updater matching
upstream's `SFW-` prefix against our own assets and reporting "no update" forever.

**This push is Action-free.** The desktop fork has `workflow_count = 0` and `runs.total_count = 0`
— verified via the API before pushing, so no workflow could fire. The dashboard fork's two
workflows trigger on `main`, which was not touched.

Only `src/main/updates.ts` changed; the nested dashboard gitlink is still `3d32338fd`.

### C.3 Parent — gitlink follow-up

`e5bb66c5ff7ec317c6513a814d029f701308a701` on local `testing`, **1 path**, `clients/desktop`:

```text
18ef1ef6ccd831de0c64cca48e3cd33522271358 -> 917d2a4e9864528e1c10c4c6a39f3949b6f5d878
```

Nothing else. **Not pushed** (brief §0/§15). The submodule working tree was moved to the new
commit and its local `jiejiebox/main` aligned, so the parent reports clean.

`[skip ci]` is on both new commits, so a future push of `testing` will not start Verify even
though the freeze rule requires fresh exact-SHA evidence anyway.

---

## D. P0/P1 result

### D.1 Desktop dynamic version model — **BLOCKED (needs the overlay retirement, see below)**

`clients/desktop/version.json = 0.1.5`, `release/JIEJIE_VERSION = 0.1.7`, and the overlay's
`version_rule()` still fails closed against the literal `UPSTREAM_DESKTOP_VERSION = "1.14.2"`. As
the brief states, a Windows build today would be refused. The target model (parse → validate
current version is a legal version string → keep `go_version` → write only `version` →
atomic → re-read → assert → fail on any other unexpected mutation) is **designed but not
written**.

### D.2 Release helper tests → single owner — **NOT DONE**

### D.3 Release-acceptance dual authority — **NOT DONE**

### D.4 Windows Desktop tooling identity (P0) — **NOT DONE**

### D.5 Android payload/evidence split — **NOT DONE** (the scoped subtree cleanup remains the
current, accepted hotfix)

### D.6 `RELEASE_EXTRA_PLATFORMS` — **NOT DONE**

### D.7 Verify race legs — **NOT DONE**

**Why D.1–D.7 are not done, stated plainly rather than implied:** retiring the overlay requires
coordinated edits to `prepare-desktop-client.sh`, `apply-desktop-branding-overlay.py`,
`test-desktop-overlay-version.py`, `check-release-workflows.py`,
`release-contract-verify.yml`, the affected `test-release-artifacts.sh` fixtures, and
`client-desktop-windows.yml` — with each change needing a regression test per §18. D.2/D.4 then
touch `verify.yml`, `release.yml` and `test-release-artifacts.sh` again. Doing that at the tail of
a long session, without the budget to run the gate suite after each step, is exactly the "many
small unverifiable edits to release-critical tooling" failure this brief exists to prevent. I
stopped rather than hand over half-applied release tooling.

**The immediate next step is unambiguous and small**: write the new version injector next to the
existing `version_rule()`, migrate `test-desktop-overlay-version.py`'s fixture off the `1.14.2`
anchor, then delete the overlay. C.2/C.3 have already removed the only two rules that could not be
dropped.

---

## E. Newly discovered optimizations

1. **The overlay is 21/23 already redundant.** Worth recording because the brief anticipated a
   large migration matrix and the reality is two rules. Retiring it is therefore *cheaper* than
   the brief assumed, not more expensive.
2. **`clients/desktop`'s local branch was stale.** After the fork push, the submodule's local
   `jiejiebox/main` still pointed at the old commit, so `git add clients/desktop` staged the old
   gitlink and reported success. A silent-no-op of the same family as the `rm -f` one. Not fixed
   in the parent (it is local checkout state, not tracked content) but worth knowing when
   following these steps on another machine.
3. **The 6 `BOTH_PRESENT` / 5 `NEITHER` false positives above** — a literal-substring migration
   check will mislead on this overlay. Any tooling written to automate the matrix must compare
   *line-anchored* occurrences, not substrings.

None of these were built beyond C.1–C.3.

---

## F. Local verification

| command | result |
|---|---|
| overlay rule extraction (AST) | 23 rules; 21 native, 2 missing, 0 unverifiable |
| matrix re-run after migration | no `MISSING_FROM_FORK` remains |
| `updates.ts` direct read | line 37 = Piggy-Cat-bit-shadow feed; line 249 = `Jiejiebox-v` matcher; old feed and old `SFW-` matcher both absent |
| `git -C desktop diff --name-only` | `src/main/updates.ts` only, no strays |
| desktop `HEAD:dashboard` | `3d32338fd` unchanged |
| parent `git diff --cached --name-only` | `clients/desktop` only, no strays |
| parent `git status --porcelain` | empty after the commit |
| Actions API, desktop fork | `workflow_count = 0`, `runs.total_count = 0` **before** pushing |
| Actions API, parent | not queried for new runs; nothing was pushed |

**NOT run, per the brief:** `go test ./...`, the 489 s sing-tun suite, any artifact/Apple/
Windows/Android build, any `workflow_dispatch`, any push to the parent.

---

## G. Things deliberately NOT changed

```text
product runtime / protocols / MASQUE / Naive / route / DNS
WireGuard census                (frozen per 1.6)
v0.1.7 tag                      (not moved, not re-cut)
GitHub Release                  (not created)
signing architecture            (untouched)
go.mod / go.sum                 (sing-tun sync already landed at 9bdcf8e8b)
release/JIEJIE_VERSION          (not bumped)
.github/workflows/**            (no workflow edited this session)
scripts/ci/**                   (no script edited this session)
Apple / Android / other client pins
```

---

## H. Final state

```text
CI_RELEASE_REFACTOR_LOCAL          = INCOMPLETE
GITHUB_ACTIONS_RUN                 = NO
PUSHED                             = desktop fork ONLY (917d2a4, Action-free);
                                     parent NOT pushed
TAG_MOVED                          = NO
RELEASE_CREATED                    = NO
READY_FOR_FINAL_FREEZE_VALIDATION  = NO

DONE
  overlay migration matrix (23 rules, proven per rule)
  the 2 genuinely-missing static rules migrated into the desktop fork and pushed
  parent gitlink advanced to that fork commit (local commit only)
  #1 above: the remaining overlay work is smaller than assumed

NOT DONE, in the order they should be taken
  1  desktop dynamic version injection + retire the overlay (+ migrate its tests)   P0
  2  Release helper tests: single owner = Release Contract Verification            P0
  3  Formal Release: check exact-SHA contract evidence instead of re-running it    P0
  4  Windows Desktop tooling identity -> exact, == CORE_SHA                        P0
  5  merge the dual release-acceptance authority                                   P0/P1
  6  Verify: move the two race legs out                                            P1
  7  docs-only trigger narrowing (verify.yml + release-contract-verify.yml)        P1
  8  Android payload/evidence split + exact artifact selection; delete the cleanup P1
  9  delete RELEASE_EXTRA_PLATFORMS / two-platform semantics                       P1
  10 Formal Release wait strategy                                                  P2
```

Both local commits and the fork push are in place and individually verified; the next session can
start at item 1 with no cleanup needed.
