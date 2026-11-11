# DESIGN NOTE — measurement semantics

Frozen before implementation. The design keeps the lab honest rather than making
B8 green.

## 1. Drain: four separate facts

1. `observation_window_ms` — the configured post-send horizon.
2. `observation_elapsed_ms` — how long it actually ran.
3. `observation_complete` — the window ran to its end with a live context.
4. `quiet_period_ms` / `tail_active` — time since the last reply at the end, and
   whether the last reply landed inside a guard of the window edge.
5. `target_quiesce` — a separate status from the target epoch counter (below).

No single boolean may mean all of these. `drain_status` is an enum:
`complete_quiet`, `window_elapsed_tail_active`, `canceled`, `zero_window`.
`loss_estimable` is true only for `complete_quiet` with a live context and an
observed quiet guard. The guard default is 250 ms or 10 % of the window,
whichever is larger; it is an operational policy, not proof, and is recorded.

## 2. Loss semantics

- `not_returned_by_deadline = writes - unique` is always recorded. It means
  exactly "no reply observed before the observation deadline", never "network
  dropped".
- `lost` / `loss_percent` are filled **only** when `loss_estimable` and there is
  no sequence-window truncation. They are labelled `loss_basis:
  "not_returned_by_observation_deadline"`, not network-confirmed loss.
- When `loss_estimable=false` or `sequence_tracking_complete=false`, `lost` and
  `loss_percent` stay null; `not_returned_by_deadline` and counts remain.

## 3. Target warmup isolation: per-epoch counters

The 20-byte UDP header already carries an epoch. The target will count
`datagrams` and `echoed` per epoch (bounded map, pruned on reset) in addition to
global counters. The runner reads the measured epoch's count after the run, so
warmup, previous reps and late packets with another epoch cannot enter
`target_received`/`echoed`. This removes the need to prove quiescence for the
measured count.

`quiesceTarget` still exists for the pre-measurement reset and returns a
structured `QuiesceResult` (ok, elapsed, reason, epoch, timeout). For paced UDP
with epoch isolation its result is informational; `target_received` no longer
depends on it. If a target does not expose the epoch map, the field is null /
`NOT_AVAILABLE`, not 0.

## 4. Pacer metrics

Record configured PPS, scheduled PPS, attempt PPS, write-completion PPS, plus
counts (`scheduled`, `attempts`, `writes`, `write_errors`, `skipped`), the last
schedule/write-start/write-end timestamps, the actual send phase duration and
its overrun. Invariants: `scheduled = attempts + skipped` (paced);
`writes <= attempts`; `writes + write_errors = attempts` when every attempt is
either accepted or failed. Achieved PPS uses the actual send phase
(last write completion - start), not the configured duration.

## 5. Provenance: runner and binary are different subjects

- Runner fingerprint: perflab executable SHA256 + its BuildInfo + the tree
  commit/dirty list + the recovered dirty diff hash where available.
- Kernel binary: SHA256 before and after the run; a change invalidates the run
  (`binary_changed=true`, provenance warning/failure) and is never silently
  accepted.
- Source cache still refreshes module pins; cached dirty state is marked as
  cached, and a dirty cache entry can never be reported as clean. The linked
  worktree VCS limitation stays explicit.

## 6. Sequence-window truncation

The window has a bounded capacity. Once full, it stops attributing sequences and
sets `sequence_tracking_complete=false`; unseen sequences are counted as
`unattributed`, not unique. `unique`/`duplicate` are then lower bounds only and
`lost`/`loss_percent` are null. A test drives the window past its (test-sized)
limit and asserts no duplicate is counted as unique.

## 7. Old B8 data

- `results/b8-final/`: `PROVENANCE_INCOMPLETE` and `SUPERSEDED` for acceptance;
  the recovered whitespace diff and blob hash are stored with the explanation.
- `results/b8-rate/`: runner code was clean (untracked doc only), so the runs
  remain useful observations, but their old drain/loss fields are reinterpreted
  as "not returned by the old deadline"; they are not authoritative for the new
  semantics.
- New runs from this branch (clean tree, runner fingerprint, per-epoch target,
  new drain states) are the only acceptance evidence.

## 8. Out of scope

Production transport changes, B8 root-cause instrumentation inside QUIC/H2,
Mieru retry semantics, platform runtime, B7 re-runs, full matrix re-runs.
