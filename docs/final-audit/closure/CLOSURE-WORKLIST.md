# Closure worklist (reconciled)

Status vocabulary: `FIXED_AND_VERIFIED`, `UNRESOLVED`, `BLOCKED`, `NOT_RUN`,
`DEFERRED`, `SUPERSEDED`. Superseded data stays on disk but is no longer
acceptance evidence.

| item | state at start | this round | status | evidence |
|---|---|---|---|---|
| B7 WG/AWG stall | fork fix on a branch, 36/36 reported | fork default `awg3-inject` fast-forwarded to `7ece3d66d79d`; matrix rerun from a clean final HEAD and new binary | FIXED_AND_VERIFIED (pending final rerun commit) | `WG-AWG-B7.md`, `results/b7-final/` |
| B7 raw provenance | old runs lacked binary SHA / pin / params | new runs record `source.binary`, `binary_sha256`, commit, dirty, tags, cgo, wireguard pin, and `load_parameters` (incl. stagger) | FIXED_AND_VERIFIED (after rerun) | `results/b7-final/*/run.json` |
| Release locks | SHA updated, pseudo-version timestamp stale | `source-lock.json` regenerated with `release-freeze.py snapshot`; `dependency-final-shas.json` version corrected; verify PASS | FIXED_AND_VERIFIED | `evidence/final/release-verify.log` |
| Root/test go.mod, go.sum | new pin present | `go mod verify` clean; test module compiles | FIXED_AND_VERIFIED | `go-mod-verify.log` |
| Schema | v1.1 + fixtures + hook | re-verified at this HEAD | FIXED_AND_VERIFIED | 48/48, fixtures 0 wrong |
| Provenance cache | HEAD-only stale warning | module view refreshed from go.mod on every cache use + warning; tests | FIXED_AND_VERIFIED (warn-only) | `PROVENANCE-AND-SCHEMA.md`, `cache_test.go` |
| UDP instrument | paced/seq/drain implemented | drain completeness made explicit and testable; invocation parameters recorded | FIXED_AND_VERIFIED | `drain_test.go`, `load_parameters` in run JSON |
| Legacy B8 runs | reported 50-60 % loss | 7/18 reps fail the final drain rule; the flagged pacer record actually satisfies `scheduled=attempts+skipped` (attempts 2489, skipped 11); all old B8 reps are SUPERSEDED for acceptance | SUPERSEDED | `UDP-RETEST.md` |
| B8 final | UNRESOLVED | 500 pps with 4 s MASQUE drain, 0/0.1/1 %, 50 pps control, qdisc/socket/target diagnostics; rate ladder 100-500 pps | UNRESOLVED: MASQUE 40-48 % at 500 pps, transition 300-350 pps; TT 500 pps is a 5.5-10 s queue, not path loss; exact queue NOT_AVAILABLE | `results/b8-final/`, `results/b8-rate/curve.md` |
| Mieru S3-9 | 1/20 race | upstream investigation only; no retry semantics change | UNRESOLVED | `MIERU-S3-9.md` |
| Platform runtime | NOT_RUN | unchanged | NOT_RUN | report |
| Earlier F-01/F-05/F-06/F-07b/F-10/F-13/F-14/F-15/S3-7 | deferred decisions | unchanged | DEFERRED | master ledger |
