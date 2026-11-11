# Provenance report

## Two separate subjects

- **Measured kernel binary**: SHA256 before and after the run (`binary_sha256_before`,
  `binary_sha256_after`, `binary_changed`). A change invalidates provenance with
  a warning and `provenance_invalid` note; it is never silently treated as the
  same binary. Test: `internal/runner/provenance_test.go`.
- **Runner (perflab)**: its own executable SHA256, Go version, and the tree
  commit/dirty state are stored under `source.runner`. This is independent of the
  kernel SHA. `report-b8.py` prints both.

## Source cache

- The cached module view is recomputed from `go.mod` on every use; a difference
  emits `source_cache_modules_stale` and replaces the map (kept from the previous
  round).
- The dirty flag is **cached**, not re-read. The record now says so explicitly
  with `source_cache_dirty_from_cache`, so a cached clean state is never presented
  as a fresh `git status`. Test: `internal/source/cache_test.go`.
- The linked-worktree limitation is unchanged: Go's `buildvcs` cannot stamp a
  `.git`-file worktree, so `vcs.revision` is absent and `vcs_unavailable` is
  recorded. The chain is binary SHA → BuildInfo → build command → commit.

## B8 data status

- `results/b8-final/`: kernel SHA known, runner source dirty at run time; the
  next commit captured the file (`1fb4bc23...`), and the recovered diff is stored
  as `runner-diff-recovered.patch` (SHA256 `1a05df5f...`). Because no runtime
  hash was recorded, these runs are `PROVENANCE_INCOMPLETE` and `SUPERSEDED` for
  acceptance. See `results/b8-final/PROVENANCE.md`.
- `results/b8-rate/`: runner code committed (`c26a4dd`), dirty only because an
  untracked Mieru document existed; usable as legacy observations, not as
  current-semantics loss.
- `results/b8-evidence-final/`: clean tree, runner fingerprint, pre/post kernel
  SHA, per-epoch target counts, effective parameters — the acceptance set.

## Not claimed

No strict gate, no VCS revision binding, no claim that a warning is a failure.
