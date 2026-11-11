# Schema and provenance

## Schema (fixed)

Historical validation with `jsonschema` 4.10.3 against the published v1 schema:
**48 files, 0 pass, 48 fail, 2,056 errors**, all `None is not of type 'object'`
on `/reps/*/{wire,latency,udp}` (712 / 664 / 680). The data was correct: those
values are explicit nulls meaning "not measured" (`wireDelta`/`latencyFrom`
return nil, the UDP block exists only for the udp pattern), and the schema itself
says null never means zero.

`perf-run.v1.1.schema.json` relaxes exactly those three blocks to
`anyOf: [null, $ref]` and restricts `summary.unit` to the five units the runner
emits. Payload version stays `perf-run/v1`; v1 stays frozen for the historical
files.

- `scripts/perf/validate_runs.py`: validates files/dirs, `--self-test`,
  `--schema`; exit 1 on failure, 3 when `jsonschema` is missing (never "pass").
- `scripts/perf/internal/schema/testdata/`: 4 valid fixtures (full TCP, full
  UDP, failure record, minimal) and 3 invalid (goodput string, wrong unit,
  missing source). Self-test result: 0 wrong.
- `run-baseline.sh` validates every produced run and fails the matrix on an
  invalid file; a missing validator is reported as unavailable.
- Result: **48/48 historical files validate**, and the invalid fixtures fail.

## Provenance (deferred, design complete)

`source.Collect` still records host `go version`, tags from a sidecar that may
not exist, and `go.mod` require pins (the replace parser was fixed in the previous
round, but the *binary* is not read). Measured facts on this host:

- `go version -m` / `debug/buildinfo.ReadFile` expose Go version, GOOS/GOARCH,
  `CGO_ENABLED`, `-tags` (comma-normalised), and dependency Replace pairs; the
  built binary lists the effective fork versions.
- VCS stamping is unavailable in this linked worktree: `.git` is a file, Go's
  `buildvcs` requires a directory, so `Main.Version=(devel)` and no
  `vcs.revision` appears. A builder sidecar is therefore required.
- Today's 48 runs record `cgo_enabled: null` even though the binary says
  `CGO_ENABLED=0`, and `build_tags` only when a sidecar exists.

Deferred implementation (files/functions named in the audit report): add
`internal/source/buildinfo.go` (`InspectBinary`, `decodeBuildInfo`,
`effectiveModules`, sha256), schema `Binary`/`ModulePin`/`CacheInfo` blocks,
`mismatch.go` with `PERF_SOURCE_STRICT`/`PERF_ALLOW_SOURCE_MISMATCH`, a
`perflab verify` command, a `build-kernel.sh` sidecar writer, and a keyed
`PERF_SOURCE_JSON` cache. Mismatch rules: binary unreadable, wrong main module,
VCS/sidecar SHA ≠ HEAD (FAIL unless explicitly downgraded), missing VCS (warn,
deterministic here), pin drift (warn/strict FAIL), binary SHA changed during the
run (warn/FAIL).

Until that lands, every report must treat `source.modules` as the go.mod view,
not as binary evidence, and must not quote `cgo_enabled` from it.
