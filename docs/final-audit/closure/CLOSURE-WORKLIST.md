# Closure worklist

Only the A/B items this round touched. The 31-item master ledger remains the
single source for everything else; this file records the closure-round state.

| item | prior | this round | status | evidence |
|---|---|---|---|---|
| B7 WG/AWG stall | DEFERRED, root cause located | fork wake fix, pin bumped, 36-cell matrix | FIXED_AND_VERIFIED | `WG-AWG-B7.md`, `results/b7/` |
| Schema vs 48 runs | unreported | v1.1 + validator + fixtures + matrix hook | FIXED_AND_VERIFIED | `PROVENANCE-AND-SCHEMA.md` |
| Binary provenance | parser only; `cgo_enabled` null, tags from sidecar | BuildInfo extraction, sha256, tags/cgo/deps, drift warnings, stale-cache warning | FIXED_AND_VERIFIED (warn-only gate) | `PROVENANCE-AND-SCHEMA.md`, `internal/source/buildinfo_test.go` |
| UDP instrument | could not measure loss | paced mode, 20-byte sequence header, drain, target/echo counters, quiesce, new schema block | FIXED_AND_VERIFIED | `UDP-RETEST.md`, `internal/loadgen/udp_test.go` |
| B8 conclusion | CONFIRMED_NOT_A_BUG | measured with the new instrument at fixed offered load + low-rate control | see `UDP-RETEST.md` | `results/b8/`, `results/b8-low/` |
| S3-9 Mieru | 1/3 under race | 20 runs: 1/20 race, 0/20 normal | UNRESOLVED | `MIERU-S3-9.md` |
| Earlier fixes F-03/F-04/F-07/F-08 | FIXED | regression tests rerun | PREVIOUSLY_FIXED_AND_REVERIFIED | commit history + tests |
| Release pin consistency | source-lock stale after pin bump | lock/final-shas moved to the new SHA | FIXED_AND_VERIFIED | `git diff release/` |
| Perf comparison | stage-1/2 matrices | B7 rerun; UDP B8 rerun; no other data-path change | measured scope only | `PERF-COMPARISON.md` |

Not started, on purpose: F-01/F-05/F-06/F-07b/F-10/F-13/F-14/F-15/S3-7 (product,
SDK or design decisions documented in the master ledger), and platform runtime
cells (NOT_RUN).
