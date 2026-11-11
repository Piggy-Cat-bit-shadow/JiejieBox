# Schema and provenance (reconciled 2026-11-11)

## Schema: fixed and verified

Historical validation with `jsonschema` 4.10.3 against the published v1 schema:
**48 files, 0 pass, 48 fail, 2,056 errors**, all `None is not of type 'object'`
on `/reps/*/{wire,latency,udp}` (712 / 664 / 680). The data was correct: those
values are explicit nulls meaning "not measured", and the schema itself says null
never means zero.

`perf-run.v1.1.schema.json` relaxes exactly those three blocks to
`anyOf: [null, $ref]` and restricts `summary.unit` to the five units the runner
emits. Payload version stays `perf-run/v1`; v1 stays frozen for historical files.

- `scripts/perf/validate_runs.py`: validates files/dirs, `--self-test`,
  `--schema`; exit 1 on failure, 3 when `jsonschema` is missing (never "pass").
- `scripts/perf/internal/schema/testdata/`: 4 valid fixtures and 3 invalid
  (goodput string, wrong unit, missing source), all fail closed.
- `run-baseline.sh` validates every produced run and fails the matrix; a
  missing validator is reported as unavailable.
- Result at this HEAD: **48/48 historical files validate**, fixture self-test
  0 wrong.

## Provenance: implemented, warn-only

`source.Collect` now reads the measured binary's own `debug/buildinfo` and keeps
the go.mod view separate from what was linked:

- `binary_sha256`, `binary.go_version`, `goos`, `goarch`, `cgo_enabled`,
  `build_tags` with `tags_source` (`buildinfo` / `sidecar` / `unavailable`),
  `main_path`, `main_version`, `deps[]` with replace pairs.
- `modules` remains the go.mod view (interesting pins, replacement wins), so old
  and new runs still join; `binary.deps` is what the executable actually carries.
- Warnings (not failures): unreadable binary, sidecar/buildinfo tag mismatch,
  tree-vs-binary `module_pin_drift`, binary SHA changed during a run, cached
  commit different from HEAD, and cached module view different from the tree.
- `PERF_SOURCE_JSON` is a speed cache only. It is now refreshed from `go.mod` on
  every use: if HEAD is unchanged but a pin (or local replace) moved, the module
  map is replaced and `source_cache_modules_stale` is added. Tests:
  `internal/source/cache_test.go` (changed view and equal view).
- Linked-worktree limitation: `.git` is a file and Go's `buildvcs` requires a
  directory, so `vcs.revision` is absent and `main_version` is `(devel)`. This is
  recorded as `vcs_unavailable`, not claimed as a binding. The evidence chain is
  binary sha256 → BuildInfo deps/tags → builder command (recorded in run logs) →
  tree commit; a builder sidecar can add tags/commit but does not invent a VCS
  revision.

Tests: `internal/source/buildinfo_test.go` (normal read, non-Go binary, missing
file, tag precedence, module drift, real built binary) and `source_test.go`
(module pins, replace handling). Boundary: warn-only; no strict gate was added,
and `PERF_SOURCE_STRICT` is not implemented — do not describe it as one.

## Cache boundary that remains

The module view is refreshed, but the rest of the cached record (branch, commit,
dirty state, sidecar tags) is trusted apart from the commit comparison. With the
same HEAD and a changed dirty state, the cached `dirty` flag can be stale; this
is a warning-level limitation, recorded rather than hidden. The final B7/B8
evidence either uses a clean tree or states the dirty file list in the run JSON.
