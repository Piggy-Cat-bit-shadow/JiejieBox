# Schema and provenance

## Schema (fixed)

Historical validation with `jsonschema` 4.10.3 against the published v1 schema:
**48 files, 0 pass, 48 fail, 2,056 errors**, all `None is not of type 'object'`
on `/reps/*/{wire,latency,udp}` (712 / 664 / 680). The data was correct: those
values are explicit nulls meaning "not measured", and the schema itself says null
never means zero.

`perf-run.v1.1.schema.json` relaxes exactly those three blocks to
`anyOf: [null, $ref]` and restricts `summary.unit` to the five units the runner
emits. Payload version stays `perf-run/v1`; v1 stays frozen for the historical
files. The UDP accounting block (below) and binary provenance are additive
optional properties in the same revision.

- `scripts/perf/validate_runs.py`: validates files/dirs, `--self-test`,
  `--schema`; exit 1 on failure, 3 when `jsonschema` is missing (never "pass").
- `scripts/perf/internal/schema/testdata/`: 4 valid fixtures and 3 invalid
  (goodput string, wrong unit, missing source). Self-test: 0 wrong.
- `run-baseline.sh` validates every produced run and fails the matrix on an
  invalid file; a missing validator is reported as unavailable.
- Result: **48/48 historical files validate**; invalid fixtures fail.

## Provenance (implemented, warn-only gate)

`source.Collect` now reads the measured executable with `debug/buildinfo` and
records, under `source.binary`:

- binary sha256 (also `source.binary_sha256`) and path;
- Go version, GOOS/GOARCH, `CGO_ENABLED`, **real build tags** and
  `tags_source` (`buildinfo` / `sidecar` / `unavailable`);
- main module path/version, `vcs_revision`/`vcs_time`/`vcs_modified` when present;
- every linked dependency with its replace target (`source.binary.deps`), while
  `source.modules` remains the go.mod view for joining old runs.

BuildInfo wins over the `.tags` sidecar; the two are compared and a mismatch is a
warning. `Collect` warns (and logs before measuring, and records in `notes`) on:
unreadable binary, binary main-module mismatch, module pin drift between the tree
and the binary, missing `vcs.revision` (this linked worktree cannot be stamped by
`buildvcs`), and a stale `PERF_SOURCE_JSON` cache whose commit is not HEAD.

Verified with the real kernel binary: tags of 19 entries read from buildinfo,
`cgo_enabled=false`, effective modules including
`wireguard-go v0.0.0-20261111081811-7ece3d66d79d`, and the vcs warning. Tests:
`buildinfo_test.go` covers synthetic decode, tags precedence, missing/non-Go
files, enrichment from the test binary and the stale cache.

Residual: the comparison is warn-only (no `PERF_SOURCE_STRICT` fail gate yet), and
a binary with no VCS revision cannot be tied to a commit without the builder
sidecar. Both are recorded as follow-ups, not silent passes.
