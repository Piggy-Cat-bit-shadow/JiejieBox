# Closure worklist

Only the A/B items this round touched. The 31-item master ledger remains the
single source for everything else; this file records the closure-round state.

| item | prior | this round | status | evidence |
|---|---|---|---|---|
| B7 WG/AWG stall | DEFERRED, root cause located | fork wake fix, pin bumped, 36-cell matrix | FIXED_AND_VERIFIED | `WG-AWG-B7.md`, `$HOME/perf/b7fix` |
| Schema vs 48 runs | unreported | v1.1 + validator + fixtures + matrix hook | FIXED_AND_VERIFIED | `PROVENANCE-AND-SCHEMA.md` |
| Binary provenance | FIXED (parser only) | BuildInfo design + mismatch rules | DEFERRED | `PROVENANCE-AND-SCHEMA.md` |
| UDP instrument / B8 | B8 CONFIRMED_NOT_A_BUG | instrument audit, paced design, old conclusion withdrawn | DEFERRED / UNRESOLVED | `UDP-RETEST.md` |
| S3-9 Mieru | 1/3 under race | 20 runs: 1/20 race, 0/20 normal | UNRESOLVED | `MIERU-S3-9.md` |
| Earlier fixes F-03/F-04/F-07/F-08 | FIXED | regression tests rerun | PREVIOUSLY_FIXED_AND_REVERIFIED | commit `699258bfa95` + prior tests |
| Release pin consistency | source-lock stale after pin bump | lock/final-shas updated by hand | FIXED_AND_VERIFIED | `git diff release/` |
| Perf comparison | stage-1/2 matrices | B7-only rerun; no other data-path change | measured scope only | `PERF-COMPARISON.md` |

Not started, on purpose: resource/FD deep dive on Mieru UDP (needs paced UDP
first), F-01/F-05/F-06/F-07b/F-10/F-13/F-14/F-15/S3-7 (product/SDK/design
decisions, documented in the master ledger).
