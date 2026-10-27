# Baseline

> **2026-10-28 calibration: INCOMPLETE.** This file's v0.1.9 measurements are historical,
> not a current delta description. At target `011f3fdcd99173acd3e80483b50cb76b07bf6ff4`,
> `v0.1.9..target` contains 791 commits and substantial production changes. The published
> `v0.1.30` tag resolves to `6112aa17052297ec9890e0e7f1541a06120b84b2`, an ancestor of the
> target; later TUN fixes are on testing and are not established as part of that release.
> The production baseline is deliberately **not moved**: the intervening subsystems and old
> claims have not all been re-audited. Compute the delta yourself; ignore historical assertions
> below that the baseline equals HEAD or that its post-baseline delta is empty.

**Read this file before anything else. The knowledge base does not describe `HEAD`.**

Every value below was measured against the worktree, not copied from a report. The command that
produced it is given so a reader can re-measure instead of trusting this file.

| Field | Value | How to re-measure |
|---|---|---|
| Repository | `Piggy-Cat-bit-shadow/sing-box` | `git remote -v` |
| Product branch | `testing` (the only long-term source branch) | `git branch --show-current` |
| **Production baseline** | `5fc4f9366a60202fdc7c98cd6fa280cceace65ba` | `git rev-parse HEAD` |
| Release baseline | tag **`v0.1.9`** = the same commit | `git rev-parse v0.1.9^{commit}` |
| Release date | 2026-10-14 23:02:14 +0800 ("test(wireguard): the census poll must not starve the goroutine it waits for") | `git log -1 --format=%ci v0.1.9` |
| Fork version file | `release/JIEJIE_VERSION` = `0.1.9` | `Get-Content release/JIEJIE_VERSION` |
| Upstream project | `https://github.com/SagerNet/sing-box.git` (remote `upstream`) | `git remote -v` |
| **Upstream baseline** | `e4b7e692f4545890bfc9d43960328e1449fa803b` — the last upstream commit this fork contains; **not** `refs/remotes/upstream/testing`, which has moved on to `dfb80349b` | `git merge-base v0.1.9 refs/remotes/upstream/testing` |
| **Fork branch point** | `0d91c4547921a7b72e9c9913e2bc6f3a556801e2` | see below |
| Knowledge reviewed at | 2026-10-14 (refresh at the v0.1.9 release) | this file's Git history |
| Knowledge revision | resolve dynamically — `git log -1 --format=%H -- KNOWLEDGE/BASELINE.md` | do not hardcode |
| **Post-baseline production delta** | substantial; inspect dynamically, see the calibration warning above | `git diff --name-only v0.1.9..HEAD` |

## What this baseline RE placed, and why the move was forced

The previous baseline was `a267e99e0` (= `v0.1.6`), and its delta row read:

> **Post-baseline production delta**: **none.** `v0.1.6..HEAD` is 5 commits and **no `.go` source changed**.

**That row became false and stayed false for two releases.** Measured at the refresh:

```sh
git rev-list --count a267e99e0..5fc4f9366     # 181        (the row said 5)
git diff --shortstat  a267e99e0 5fc4f9366     # 141 files changed, 33110 insertions(+), 1816 deletions(-)
git diff --name-only  a267e99e0 5fc4f9366 -- '*.go' ':(exclude)*_test.go'   # 12 files  (the row said 0)
```

The 12 changed production files:

```text
adapter/experimental.go              option/fast_path.go        route/fast_path.go
box.go                               option/outbound.go         route/route.go
cmd/internal/build_libbox/main.go    option/schema.go           route/router.go
experimental/boxdd/authenticode_windows.go
protocol/group/selector.go           protocol/group/urltest.go
```

This is the failure mode the knowledge model is designed to prevent, and it is recorded here rather
than quietly corrected: **a delta row is a claim with an expiry date, and nothing re-checks it
automatically.** A reader who trusted that row between `v0.1.7` and `v0.1.9` would have concluded the
modules still described the tree, and would have been wrong about a released feature
(`fast_path` group policy), about the root package (`box.go`), and about the Android/Apple build path.

The refresh moved the baseline to `HEAD` because the changed subsystems were audited against current
source, not because time passed. `REFRESH.md` §39 lists the conditions under which the baseline must
**not** move; none applied.

**What "audited" means here, stated precisely, because the distinction is the whole point.** The
modules were checked against current source for the behaviour this delta changed, and two were found
to **contradict** it — both are corrected in the same refresh:

* `modules/fast-path-group-policy.md` §6 claimed the `canFastBypass` eligibility gate was *"unchanged
  by this field"* and that *"none of the existing blocking conditions is bypassed."* False:
  `route/route.go` gained a `fastPathAuthorized` parameter and relaxes exactly one blocker
  (`BypassRefusedOutboundChain`) for an authorised all-transparent chain. Without that branch the
  feature is inert, since a flow routed into a group always has `len(chain) >= 2`.
* `modules/data-path-optimization.md` §A.8 listed outbound chains as out of scope. Still true for
  **opaque** groups, and that is why `loadbalance` still refuses — but no longer true for a group that
  declares `FastPathTransparent() bool`.

`modules/core-lifecycle-runtime.md` §2 was found to have **line anchors stale by ~+47** with its
structural claims intact; the anchors are marked rather than blanket-shifted, because a uniform offset
was never verified anchor by anchor and a guessed line number reads as authoritative.

**This baseline is therefore current for the audited subsystems, and it is not a claim that every
line of every module was re-derived.** A module whose paths the delta did not touch was not re-read.
Any module statement that carries a `file:line` should be re-measured with
`git log --oneline v0.1.9..HEAD -- <path>` before it is relied on, per `modules/README.md`
§How to decide whether a module is still accurate.

## Fork release lineage

Measured, so the tags' own objects are not confused with the commits they name. **Every fork tag is
annotated**, so `git rev-parse vX` returns a *tag object* and `git rev-parse vX^{commit}` returns the
commit. A reader who diffs against the wrong one gets a diff against a tag object, which is a
different thing.

| Tag | Tag object | Commit (peeled) | Date | Subject |
|---|---|---|---|---|
| `v0.1.5` | `0b4725149` | `e70f3d2c8` | 2026-10-07 18:45:11 +0800 | release: correct version to v0.1.5 |
| `v0.1.6` | `a34dac049` | `a267e99e0` | 2026-10-12 11:53:53 +0800 | release: include Windows and Android artifacts |
| `v0.1.7` | `46361ea86` | `ccab3645d` | 2026-10-12 23:33:22 +0800 | fix(release): stage the Android AAR, its checksum and its record together |
| `v0.1.8` | `e4df57f03` | `455458c76` | 2026-10-14 21:12:08 +0800 | fix(release): resolve staged desktop installer path |
| `v0.1.9` | `58f99c630` | `5fc4f9366` | 2026-10-14 23:02:14 +0800 | test(wireguard): the census poll must not starve the goroutine it waits for |

**`v0.1.8` is a historical identity and must never be moved.** It was published as a GitHub Release
with **six** assets where the contract required seven, so it is a *known-defective* release as well as
a fixed one. The defect and its fix are `v0.1.9`; the tag is immutable regardless.

## The two baselines are different, and both matter

```text
upstream baseline  e4b7e692   the last upstream commit this fork contains
                              (== merge-base with refs/remotes/upstream/testing)
        │
        │  1,834 fork-only commits at the v0.1.6 baseline
        │  1,934 fork-only commits including the v0.1.6 -> v0.1.9 delta
        ▼
production baseline 5fc4f9366  tag v0.1.9 — what the KNOWLEDGE modules now describe
        │
        │  documentation and hygiene commits (this refresh)
        ▼
HEAD                          knowledge revision; NOT a production baseline
```

Measured, so a reader does not have to trust the picture above:

```sh
git merge-base v0.1.9 refs/remotes/upstream/testing   # e4b7e692f…  (it is an ancestor)
git rev-parse refs/remotes/upstream/testing           # dfb80349b…  (upstream has moved on)
git rev-list --count e4b7e692..a267e99e0              # 1834  (the v0.1.6 baseline)
git rev-list --count e4b7e692..v0.1.9                 # 2015
git rev-list --count v0.1.9 --not --remotes=upstream  # 2014  (fork-side)
git rev-list --count a267e99e0..v0.1.9                # 181   (the delta this refresh absorbed)
git diff --shortstat e4b7e692 v0.1.9                  # see the row above
```

> Note on the last line: do **not** write a `.go` filter as `... | grep -c '\.go$'`. `grep -c` prints
> `0` **and exits 1** on no match, so a re-checker scripting it will read a correct result as a
> failure. Use the `--` pathspec form:
> `git diff --name-only <a> <b> -- '*.go' ':(exclude)*_test.go'`.

`e4b7e692..a267e99` is an **ancestor-to-descendant range**, not a branch range: `e4b7e692` is the
upstream tip this fork absorbed and the fork baseline is its descendant. The commits in that range are
exactly the commits absent from every upstream ref, which is what makes this a fork.

## The provenance test — use this, never use `git describe`

### Is `<sha>` inherited from upstream, or is it fork-side?

```sh
git rev-list --count <sha> --not --remotes=upstream
```

```text
0  ⇒ <sha> is reachable from an upstream ref   ⇒ UPSTREAM-side
≥1 ⇒ <sha> has commits no upstream ref reaches ⇒ FORK-side
```

Read the direction carefully. `--remotes=origin` is **this fork**, so using it inverts the answer and
will classify the entire fork as upstream.

### The three traps this repository has already paid for

1. **`git describe` lies here.** It reports `v1.14.3-38-ge4b7e692f` for a commit that does not carry
   that tag, because the fork's history contains pre-rewrite upstream orphans. Never use it for
   provenance.
2. **Upstream history is rewritten.** `refs/remotes/upstream/testing` moved from `e4b7e692` to
   `dfb80349b` without the fork consuming it. SHA ancestry alone is therefore not always enough; use
   more than one check when provenance is load-bearing.
3. **Never count upstream work by SHA.** One logical upstream change can appear up to eleven times in
   a range after a rewrite.

## Post-baseline incremental reading — the intended workflow

```sh
# 1. What moved since the knowledge baseline?
git log --oneline v0.1.9..HEAD
git diff --name-only v0.1.9..HEAD

# 2. If that is empty or documentation-only, the modules are still current.
#    If not, map the changed paths with modules/README.md and read only those.

# 3. Nothing in KNOWLEDGE/ needs editing for a normal development commit.
```

## Evidence level of this file

`VERIFIED_GIT` — every row is a command output produced against this worktree at the refresh. No row
depends on a report, a document, or an inference. The one row that is a **claim about the modules**
rather than about Git is the production-baseline row, and it is only as good as the module audit
recorded in `REFRESH.md`'s refresh report.

## Known limitations of this baseline

* The client **source** repositories (`clients/apple`, `clients/android`, `clients/desktop`) are
  separate repositories and are empty in a clean worktree. Their revisions are recorded as gitlinks;
  their contents are **not** covered by any module.
* `experimental/boxdd/**` (the Windows daemon) changed in this delta and is owned by
  **`modules/windows-daemon-security.md`**. It was the last unowned changed subsystem; the ownership
  map now resolves it, with `daemon/**`, the build driver and packaging recorded as CROSS-MODULE.
  **Its trust boundary has no documented end-to-end test** — see that module's §15. A Windows
  test workflow is now declared; actual execution requires run evidence.
* No claim in this knowledge base is `VERIFIED_CI`: the tree carries no workflow-run record. Evidence
  levels stop at `VERIFIED_SOURCE` / `VERIFIED_UNIT` / `VERIFIED_GIT` unless a module says otherwise.
