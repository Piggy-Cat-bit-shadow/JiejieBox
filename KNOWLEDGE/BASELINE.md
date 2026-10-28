# Baseline

## Current calibrated baseline — 2026-10-28

| Field | Value and scope |
|---|---|
| Repository / product branch | `Piggy-Cat-bit-shadow/sing-box` / `testing` |
| Production baseline | `cb71cf80d7224a51823b455f0be5959ad8556933` — source target, not a documentation-only commit |
| Release baseline | [v0.1.31](https://github.com/Piggy-Cat-bit-shadow/sing-box/releases/tag/v0.1.31), tag peels to the same commit; non-draft, non-prerelease, published 2026-10-28 00:38:59 UTC |
| Upstream ancestry | checked remote `upstream/testing` was `e5d91fa79f6319d5de940d6c81d81f0fe53b9476`; its merge-base with the production target is `a6952ccf6907c18e18917fcb763a433e8e36937b`. Recheck when refs move; ancestry does not prove authorship. |
| Previous production baseline | `5fc4f9366a60202fdc7c98cd6fa280cceace65ba` (v0.1.9); 795 commits precede the target in that range |
| Knowledge revision | resolve dynamically with `git log -1 --format=%H -- KNOWLEDGE/BASELINE.md` |
| Post-baseline delta | compute `git diff --name-only cb71cf80d7224a51823b455f0be5959ad8556933..HEAD`; the knowledge commits themselves do not change runtime behavior |

The modules describe the checked source mechanisms at this baseline with explicitly scoped evidence;
historical line numbers and older audit measurements still require rechecking. A baseline is not proof
of device, interoperability, artifact-byte or all-platform behavior. The Apple CI call-site scan was
PARTIAL, and the known Windows TUN errno classification gap, reproduced Windows TLS test failure and
other limits are recorded in [the calibration ledger](CALIBRATION-2026-10-28.md) and owning modules.
Do not assume `HEAD` is always the baseline: compute the actual delta.

## Historical v0.1.9 baseline

The following table and narrative preserve the v0.1.9 review. They are not the current baseline;
their values and claims are scoped to that historical revision.

| Field | Value | How to re-measure |
|---|---|---|
| Repository | `Piggy-Cat-bit-shadow/sing-box` | `git remote -v` |
| Product branch | `testing` (the only long-term source branch) | `git branch --show-current` |
| **Production baseline** | `5fc4f9366a60202fdc7c98cd6fa280cceace65ba` | `git rev-parse v0.1.9^{commit}`; this is not necessarily HEAD |
| Release baseline | tag **`v0.1.9`** = the same commit | `git rev-parse v0.1.9^{commit}` |
| Release date | 2026-10-14 23:02:14 +0800 ("test(wireguard): the census poll must not starve the goroutine it waits for") | `git log -1 --format=%ci v0.1.9` |
| Fork version at baseline | `release/JIEJIE_VERSION` = `0.1.9` | `git show v0.1.9:release/JIEJIE_VERSION` |
| Upstream project | `https://github.com/SagerNet/sing-box.git` (remote `upstream`) | `git remote -v` |
| **Historical upstream anchor** | `e4b7e692f4545890bfc9d43960328e1449fa803b` — used by the v0.1.9-era review | historical snapshot; remeasure against the desired target and current upstream ref |
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

**At the v0.1.9 review, that baseline was current for the audited subsystems; it was not a claim that every
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
historical upstream anchor e4b7e692  used by the v0.1.9-era review
        │
        │  1,834 commits in the historical ancestor range to v0.1.6
        │  2,015 commits in the historical ancestor range to v0.1.9
        ▼
production baseline 5fc4f9366  tag v0.1.9 — last declared broad knowledge baseline
        │
        │  substantial production and documentation changes since that baseline
        ▼
HEAD                          current branch tip; re-measure, not necessarily a release
```

Measured, so a reader does not have to trust the picture above:

```sh
git merge-base v0.1.9 refs/remotes/upstream/testing   # remeasure: the ref may be rewritten
git rev-parse refs/remotes/upstream/testing           # remeasure the locally recorded ref
git rev-list --count e4b7e692..a267e99e0              # 1834  (the v0.1.6 baseline)
git rev-list --count e4b7e692..v0.1.9                 # 2015
git rev-list --count v0.1.9 --not --remotes=upstream  # 2014 at that review; unreachable ancestry, not an authorship count
git rev-list --count a267e99e0..v0.1.9                # 181   (the delta this refresh absorbed)
git diff --shortstat e4b7e692 v0.1.9                  # see the row above
```

> Note on the last line: do **not** write a `.go` filter as `... | grep -c '\.go$'`. `grep -c` prints
> `0` **and exits 1** on no match, so a re-checker scripting it will read a correct result as a
> failure. Use the `--` pathspec form:
> `git diff --name-only <a> <b> -- '*.go' ':(exclude)*_test.go'`.

`e4b7e692..a267e99` is an **ancestor-to-descendant range**, not a branch range: `e4b7e692` is the
upstream tip this fork absorbed and the fork baseline is its descendant. The range is not an
authorship census; upstream rewrites and other refs mean reachability alone cannot establish the
origin of every patch.

## The provenance test — use this, never use `git describe`

### Is `<sha>` inherited from upstream, or is it fork-side?

```sh
git rev-list --count <sha> --not --remotes=upstream
```

```text
0  ⇒ <sha> is reachable from an upstream ref
≥1 ⇒ at least one ancestor of <sha> is not reachable from the current upstream refs
```

This is an ancestry test, not an authorship classifier: rewritten upstream orphans can be unreachable
from current upstream refs, and one unreachable ancestor does not establish the origin of the named
commit. Compare patches, blobs and historical evidence when provenance matters.

Read the direction carefully. `--remotes=origin` compares against **this fork**, not upstream;
reachability against it cannot establish upstream provenance.

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

# 2. An empty/code-free delta does not prove old entries are correct.
#    Map changed paths and independently verify existing load-bearing claims.

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
* Historical v0.1.9 entries here have no associated CI-run record in the tree. A later independently
  checked Windows daemon run is recorded with its SHA in `modules/windows-daemon-security.md`.
