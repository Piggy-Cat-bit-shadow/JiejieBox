# Baseline

**Read this file before anything else. The knowledge base does not describe `HEAD`.**

Every value below was measured against the worktree, not copied from a report. The command that
produced it is given so a reader can re-measure instead of trusting this file.

| Field | Value | How to re-measure |
|---|---|---|
| Repository | `Piggy-Cat-bit-shadow/sing-box` | `git remote -v` |
| Product branch | `testing` (the only long-term source branch) | `git branch --show-current` |
| **Production baseline** | `a267e99e0bf1811dc975b013e2e8d7a15ab40d23` | `git rev-parse a267e99e0` |
| Release baseline | tag **`v0.1.6`** = the same SHA | `git rev-parse v0.1.6^{commit}` |
| Release date | 2026-10-12 11:53:53 +0800 ("release: include Windows and Android artifacts") | `git log -1 --format=%ci v0.1.6` |
| Fork version file | `release/JIEJIE_VERSION` = `0.1.6` | `Get-Content release/JIEJIE_VERSION` |
| Upstream project | `https://github.com/SagerNet/sing-box.git` (remote `upstream`) | `git remote -v` |
| **Upstream baseline** | `e4b7e692f4545890bfc9d43960328e1449fa803b` = `refs/remotes/upstream/testing` | `git rev-parse refs/remotes/upstream/testing` |
| **Fork branch point** | `0d91c4547921a7b72e9c9913e2bc6f3a556801e2` | see below |
| Knowledge reviewed at | 2026-10-12 (Stage 2; delta refresh at `93d757828`) | this file's Git history |
| Knowledge revision | resolve dynamically — `git log -1 --format=%H -- KNOWLEDGE/BASELINE.md` | do not hardcode |
| **Post-baseline production delta** | **none.** `v0.1.6..HEAD` is 5 commits and **no `.go` source changed**. Three are the knowledge base itself (`f45f27341`, `fccf644a8`, `93d757828`), and two are release plumbing — the `clients/apple` gitlink (`7f31bfd5e`) and the Apple CI gate scripts (`ece0fb9d2`). Zero production implementation commits, so module descriptions still hold; see `modules/ci-release.md` §5 for the two gate changes. | `git log --oneline v0.1.6..HEAD`; `git diff --name-only v0.1.6..HEAD` |

## The two baselines are different, and both matter

```text
upstream baseline  e4b7e692   the last upstream commit this fork contains
                              (== refs/remotes/upstream/testing)
        │
        │  1,834 fork-only commits — none of them is reachable from any upstream ref
        ▼
production baseline a267e99e  tag v0.1.6 — what the KNOWLEDGE modules describe
        │
        │  documentation-only commits
        ▼
HEAD                          knowledge revision; NOT a production baseline
```

Measured, so a reader does not have to trust the picture above:

```sh
git rev-parse refs/remotes/upstream/testing          # e4b7e692f…
git merge-base a267e99e0 refs/remotes/upstream/testing   # e4b7e692f…  (e4b7e692 is an ancestor)
git rev-list --count e4b7e692..a267e99e0             # 1834
git rev-list --count e4b7e692..a267e99e0 --not --remotes=upstream   # 1834
git rev-list --count a267e99e0 --not --remotes=upstream             # 1834
git rev-list --count v0.1.6..HEAD                    # 5   (3 docs commits + 2 release-plumbing commits)
git diff --shortstat e4b7e692 a267e99e0              # 1474 files, 403956 insertions(+), 6207 deletions(-)
git diff --name-only v0.1.6..HEAD -- '*.go'          # empty — no production source since the baseline
```

> Note on the last line: do **not** write it as `... | grep -c '\.go$'`. `grep -c` prints `0` **and
> exits 1** on no match, so a re-checker scripting it will read a correct result as a failure. The `--`
> pathspec form above is the one to use.

`e4b7e692..a267e99` is an **ancestor-to-descendant range**, not a branch range: `e4b7e692` is the
upstream tip and the fork baseline is a descendant of it. The 1,834 commits in that range are exactly
the commits absent from every upstream ref, which is what makes them the fork's own work.

## The provenance test — use this, never use `git describe`

```sh
# Is <sha> inherited from upstream, or is it fork-side?
git rev-list --count <sha> --not --remotes=upstream
#   0  ⇒ <sha> is an ANCESTOR of an upstream ref        ⇒ UPSTREAM-side
#   ≥1 ⇒ <sha> has commits no upstream ref reaches     ⇒ FORK-side
```

> **Read the direction of that command carefully — it is the single most common mistake made about
> this repository.** It counts the commits reachable from `<sha>` that are **not** reachable from
> upstream, so a **higher** number means **more fork-side**, and `0` means **upstream-side**.
>
> Two independent confirmations, so you never have to trust the sentence:
>
> ```sh
> git rev-parse upstream/HEAD                                        # e4b7e692f… (the fork range's left end)
> git rev-list --count upstream/HEAD --not --remotes=upstream         # 0     — upstream is upstream-side
> git rev-list --count a267e99e0 --not --remotes=upstream             # 1834  — the fork baseline is fork-side
> git merge-base --is-ancestor <sha> upstream/HEAD                    # exit 0 = upstream-side, exit 1 = fork-side
> ```
>
> Do **not** substitute `--remotes=origin` for `--remotes=upstream`: `origin` **is** the fork
> (`Piggy-Cat-bit-shadow/sing-box`), so that form returns 0 for every fork commit and will tell you the
> fork is upstream. An earlier draft of this knowledge base had the direction inverted; the two
> commands above are the check that catches it.

**The reachability test alone is not enough, and this is the single most misread fact about this
repository.** Upstream **rebases and force-pushes `testing`**. A rebase re-commits the same logical
change under a new SHA while preserving the **author date** and changing the **committer date**. The
fork kept the pre-rewrite SHAs, so its history carries **orphans**: commits that are absent from every
upstream ref *and* are content-duplicates of work already inside `e4b7e692`'s ancestry.

Measured decomposition of the 1,834-commit range:

| Class | Count | How to tell |
|---|---|---|
| **Fork-authored** | **1,505** | reachability test ≥1 **and** no upstream twin |
| **Upstream-authored orphans** (pre-rewrite copies) | **329** | reachability test ≥1 **but** an upstream twin exists |
| — of which authored by `世界 <i@sekai.icu>` | 328 of the 343 in range | the other 15 untwinned 世界 commits are release "Bump version" commits, upstream commits authored before the fork point, and the fork's own first five commits — check each with `git merge-base --is-ancestor` |

An **upstream twin** is a commit reachable from a live upstream ref with the same author, the same
**author date**, and the same subject. Three pairs were confirmed beyond the heuristic:

| Fork orphan | Upstream twin | Test |
|---|---|---|
| `16c898a82` | `a364ff479` | identical `patch-id` `430ec7c883c1da988400160542799993e6d1ec80` |
| `ab287d2fc` | `f7cb0ba1d` | identical `patch-id` `f71ed804db1cc80722822425e192c6b0245da966` |
| `e22cd5406` | `b83089ecf` | identical `patch-id` `9f8b88a4343c914b027af2f9dc78eca5e53974de` |
| `ba5d5f75c:common/srs/mmap.go` | `e7399fd98147:…` | identical blob `7fbe84c8b…` |
| `dd4f62edc:common/sniff/sniff.go` | `58be77893:…` | identical blob `b3651e1ff…` |

**One logical upstream change appears up to 11 times** in the range with an identical subject
(`Use screen state to end device pause on iOS` ×11; `Improve idle connection management` ×10;
`Close idle connections of unreferenced outbounds and DNS servers` ×10; `Bump version` ×10; eight more
subjects ×9). The 343 世界-authored commits carry only **61 distinct subjects** — roughly 5.6 copies
each. **Never count these commits individually**, and never conclude "the fork changed this" from the
number of matching subjects.

**Commit count is not content delta, in either direction.** The pre-rewrite upstream tip
(`6afeff4c0`) differs from `e4b7e692` by only 2 files / 24 lines, so the orphaned commits are
content-duplicates of work already in the baseline's ancestry. The real content delta remains
`git diff e4b7e692 a267e99` = 1,474 files.

Two further traps this exists to avoid:

1. **`git describe --tags e4b7e692` reports `v1.14.3-38-ge4b7e692f`, not `v1.14.3`, and
   `--exact-match` fails.** The tag `v1.14.3` is an upstream tag 38 commits below `upstream/testing`
   and points at `7054cac51`. "This fork is based on v1.14.3" is wrong twice over. **Do not use
   `git describe` for provenance or for release identity.**
2. **Author name is not provenance.** See the table above: 328 commits by the upstream maintainer are
   orphans, and the fork's own identities (`Piggy-Cat-bit-shadow`, `JiejieBox Agent`,
   `kernel-round`, `sniff-closure`, `Apple pin verification`, `Agent F`, `subagent-c`) authored 1,505.

## A fourth trap: a commit can be upstream-tagged while reachable from no upstream branch

`git rev-list --count <sha> --not --remotes=upstream` compares against **branches** (`refs/remotes/…`),
so it says nothing about **tags**. `6afeff4c0` — the pre-rewrite upstream `testing` tip, and the commit
the fork's own `docs/fork/upstream-53-ledger.md` names — is an ancestor of **none of the 42
`refs/remotes/upstream/*` refs** (the test returns 37) yet **is** tagged upstream:
`git tag --contains 6afeff4c0` → `v1.15.0-alpha.11`.

> **So the honest statement about `upstream-53-ledger.md`'s baseline is: "reachable from no current
> upstream *branch*; upstream still publishes tag `v1.15.0-alpha.11` at that commit."** Writing "no
> upstream ref" would be false, and it is exactly the kind of over-claim this file exists to prevent.

This is also why `e4b7e692..a267e99` being 1,834-commits-absent-from-upstream-branches does **not**
imply upstream has never seen that work: upstream rebases branches but **keeps tags**, so the same
content can be reachable through a tag while its branch-tip SHA is gone.

## Release-identity trap

This repository carries **two incompatible tag schemes in one namespace**:

| Scheme | Example | Belongs to |
|---|---|---|
| `v0.1*` — `v0.1`, `v0.1.3`, `v0.1.4`, `v0.1.5`, `v0.1.6` | `v0.1.6` = `a267e99e0` | **this fork's releases** |
| `v1*` — `v1.13.0`, `v1.14.3`, `v1.11.2`, … | `v1.14.3` = `7054cac51` | upstream sing-box, inherited into this history |

`v0.1.0` is an **upstream** tag (`0ca7d1595`, 2022-08-12), not this fork's — the fork's own series
starts at `v0.1`. A reader must never present `v1.x` as a fork release, and must never resolve the
fork's release identity with `git describe`.

**Every fork *release* tag is annotated** — the tag object SHA differs from the commit SHA — which is
its own counting trap: `v0.1.6` is object `a34dac049` → commit `a267e99e0`, and `v0.1.5` is
`0b4725149` → `e70f3d2c8`. The fork also carries **lightweight** tags:
`archive/macos-client-before-consolidation` is a plain commit ref. So "this fork's tags are annotated"
is **not** a rule; only "its release tags are" is.

Fork release lineage, measured:

| Tag | Commit | Date | Subject |
|---|---|---|---|
| `v0.1` | `62d8b7a7d` | 2026-09-30 | release: v0.1 |
| `v0.1.3` | `4f999d461` | 2026-10-06 | test(traffic): gate HIGH-versus-HIGH on the queue and the median, not the p99 |
| `v0.1.4` | `5381be0b0` | 2026-10-07 | chore(release): set version 0.1.4 |
| `v0.1.5` | `e70f3d2c8` | 2026-10-07 | release: correct version to v0.1.5 |
| `v0.1.6` | `a267e99e0` | 2026-10-12 | release: include Windows and Android artifacts |

## The fork branch point — measured, not the value earlier documents assumed

A previous reading treated `e4b7e692` as the fork point. It is not: `e4b7e692` is the upstream tip and
is an **ancestor** of the fork baseline, so it cannot be a divergence point. The real branch point is
the parent of the first fork-authored commit:

```sh
git rev-list --parents -n 1 03de88adb64935274608caf889966f82c55a36a3
#  03de88adb…  0d91c4547921a7b72e9c9913e2bc6f3a556801e2
git merge-base --is-ancestor 0d91c4547 refs/remotes/upstream/testing   # exit 0 → upstream contains it
git merge-base --is-ancestor 03de88adb e4b7e692                        # exit 1 → the fork commit is not upstream
```

* `0d91c4547` — "Fix file manager treating Windows absolute paths as relative", 2026-09-22, 世界.
* `03de88adb` — "Fix network reset not closing connections", 2026-09-23, 世界 — the first commit that
  is not reachable from upstream.

## What "the fork's own history" contains

| Measure | Value |
|---|---|
| Fork-only commits | 1,834 (1,807 non-merge + 27 merges) |
| First-parent commits in the range | 1,399 |
| Inherited upstream commits in the fork baseline's ancestry | 2,827 = `git rev-list --count refs/remotes/upstream/testing` = 4,661 − 1,834 |
| Files changed in the range | 1,474 |
| Test files added in the range | 768 |
| Root-package contract test files (`box_*_test.go`) | 25 |
| Fork-created packages | 17 (see `ARCHITECTURE.md`) |
| Fork range first commit date → last | 2026-09-23 → 2026-10-12 |

## Post-baseline incremental reading — the intended workflow

```sh
# 1. What moved since the knowledge baseline?
git log --oneline v0.1.6..HEAD
git diff --name-only v0.1.6..HEAD

# 2. If that is empty or documentation-only, the modules are still current.
#    If not, map the changed paths to modules with modules/README.md and read only those.

# 3. Nothing in KNOWLEDGE/ needs editing for a normal development commit.
```

## Evidence level of this file

`VERIFIED_GIT` — every row is a command output produced against the worktree at
`f45f27341`/`a267e99e0`. No row depends on a report, a document, or an inference.
