# Pacer metrics

## Recorded separately

Each UDP rep now carries:

- `configured_pps` — `-udp-rate` (0 means unpaced);
- `scheduled` / `scheduled_pps` — slots the absolute schedule created, including
  missed slots;
- `attempts` / `attempt_pps` — calls to `sendOne` (one per scheduled send that
  was not skipped);
- `writes` / `write_errors` — successful `WriteToUDP` and failed attempts;
- `write_completion_pps` — writes divided by the **actual send phase duration**
  (start to last successful write completion), not by the configured duration;
- `send_phase_ms`, `send_overrun_ms` — actual phase length and how far it
  exceeded `duration`;
- `last_scheduled_ms`, `last_write_start_ms`, `last_write_end_ms` — offsets from
  phase start;
- `skipped` — schedule slots deliberately missed (never counted as loss).

Invariants enforced by tests: `scheduled = attempts + skipped` (paced),
`writes <= attempts`, `writes + write_errors = attempts`, and `returned =
unique + duplicate`. A blocking write is visible as a long `send_phase_ms` and a
lower `write_completion_pps`; the runner never substitutes the configured rate.

## Why it matters for B8

Old records showed achieved PPS below the configured value (e.g. 250 pps →
185.9). That was the write-completion rate being reported without the phase
length. The new record makes the distinction explicit, so a cell that could not
offer the requested rate is not silently compared with one that did.

## Tests

- `internal/loadgen/pacer_test.go`: honours rate, skips missed slots without
  bursting, stops at deadline/context.
- `internal/runner/params_test.go` and `udp_record_test.go`: pps fields derived
  from actual phase; unpaced runs omit paced-only fields.
- The new B8 runs record both configured and completion rates per rep.
