# PRECHECK — UDP evidence final closure

Read-only inventory before changes.

## Git and workspace

- Branch created from the latest reconciliation branch:
  `work/udp-evidence-final-2026-11-11` at
  `2f7c915e8cc9ce9860e755774e548660fc1df115` (the current remote tip of
  `work/closure-final-reconcile-2026-11-11`). Worktree clean.
- Historical branches untouched: `work/closure-2026-11-11` (`897553e8122`),
  `work/coupling-audit-2026-11-11` (`2e0003012e1`), `testing`
  (`1ce3aa3a825`, not merged).
- Fork default `awg3-inject` still `7ece3d66d79d`; the fix branch is the same
  commit.

## Code state versus the review checklist

- `drainCompleteFor()` exists and uses: live context + a non-zero window when
  replies are missing + a 50 ms tail guard. It collapses
  observation/quiet/target-quiesce/loss-estimability into one boolean; the
  review is right that the 50 ms quiet period is not proof.
- `udpSeqWindow` caps at `1<<20`; when full it stops storing but still returns
  `true` for unseen sequences, so duplicates can be counted as unique and
  `truncated` never reaches the result. Confirmed in code.
- UDP warmup uses a XORed epoch and the client reader rejects stale epochs, but
  the target has only global counters and `Reset()`; late warmup can pollute
  `target_received`. `quiesceTarget()` returns no status and a timeout is only
  logged; `Stats` has no epoch dimension.
- Pacer counts scheduled/attempts/skipped/writes, but has no write-error count,
  no last-schedule / write-start / write-complete timestamps, and no separate
  achieved-PPS basis. `AchievedPPS` divides writes by `SendWindow`, which is
  honest but undocumented in the record.
- `load_parameters` is written from `opts` before scenario defaults are applied
  (duration/reps/patterns can appear as 0/empty even though defaults ran).
- Provenance: `PERF_SOURCE_JSON` refreshes `go.mod` pins but trusts cached dirty
  state; the kernel binary SHA is taken once at start; the perflab runner has no
  source fingerprint; no pre/post binary comparison.
- B8-final runs: `source.commit=f03b1c7`, `dirty=true`, dirty file
  `M scripts/perf/internal/runner/runner.go` (plus docs). The exact diff can be
  recovered: `f03b1c7..1318fa6` for that file is whitespace-only (7 alignment
  lines in `load_parameters`); the file blob at `1318fa6` is
  `1fb4bc236484de5f7c66caec83ccbf37d403e3f3`. No run-time hash was recorded, so
  the content can be reconstructed but **cannot be cryptographically bound** to
  those runs; they will be marked `PROVENANCE_INCOMPLETE`.
- b8-rate runs: `source.commit=c26a4dd`, dirty only because
  `MIERU-UPSTREAM-ISSUE.md` was untracked; runner code was committed and clean.
  Kernel binary SHA `0157e713c8e2...`. These are usable after re-interpretation,
  but their drain semantics predate this round.
- `curve.md` had one wrong row (TrustTunnel 500 pps / 2 s reported
  `0/77.2/null` while the raw JSON has all three `drain_complete=false,
  lost=null`); confirming the need for a generator.

## Baseline gates (before changes)

- Lab `gofmt/vet/test` green at `2f7c915`.
- Schema: 48/48 historical + 4 valid/3 invalid fixtures green.
- B7 36/36 clean-tree evidence, release verify and `go mod verify` green.
- All background jobs/subagents stopped; both worktrees clean.

## Files in scope

`scripts/perf/internal/{loadgen,runner,target,schema,source}`, `perf-run.v1.1`
schema, lab tests, `scripts/perf/report-b8.py` (new generator), closure reports
under `docs/final-audit/udp-evidence-final/`. No production protocol changes.
