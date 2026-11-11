# Timing calibration

## What changed

- UDP send timestamp in the 20-byte header is now `sent_monotonic_ns`: an offset
  from phase start on the monotonic clock, not `time.Now().UnixNano()`.
- The reader computes latency as `mono_now - sent_ns`, also on the same
  process-wide monotonic clock.
- `send_end`, `last_scheduled`, `last_write_start`, `last_write_end` are stored as
  monotonic offsets; the result derives durations directly, no `time.Unix`.
- `UDPEpoch` still uses UnixNano, but only as an opaque identifier; it is never
  used to compute a duration.

Result: an NTP step or manual clock change cannot turn a latency negative, huge,
or shift `send_phase_ms`/last-write offsets.

## What this affects in old B8 data

The old sets used wall-clock `sent_ns` for the per-datagram latency samples
(`jitter_ms` is reader inter-arrival and was already monotonic). Counts
(writes/unique/target/not_returned), drain state and pacer PPS are
duration-based and were **not** affected by the wall clock. Therefore:

- old count-based conclusions stand;
- latency/jitter from the old sets are labelled `legacy_timing` and are not used
  for a latency claim;
- no old run was edited; the new field semantics apply to new runs.

## Calibration run

`results/b8-timing-check/`: MASQUE CONNECT-IP, 250 pps, 1 % per-direction netem,
80 ms RTT, 8 s observation, 3 reps, clean tree at `a64719f`.

| rep | writes | unique | target | loss % | jitter ms | last reply ms | status |
|---|---:|---:|---:|---:|---:|---:|---|
| 0 | 1250 | 1227 | 1238 | 1.84 | 5.14 | 1387 | complete_quiet |
| 1 | 1250 | 1229 | 1238 | 1.68 | 4.50 | 613 | complete_quiet |
| 2 | 1250 | 1237 | 1243 | 1.04 | 4.06 | 106 | complete_quiet |

Provenance: `dirty=false`, runner SHA `7aa6fb4a61c5…`, kernel
`0157e713c8e2…` before = after, `binary_changed=false`. The negative/absurd
latency values the wall-clock path could produce do not appear; jitter and last
reply are plausible for the shaped path.

## Relation to the B8 conclusion

This is a measurement-method calibration, not a new B8 experiment. It does not
change the acceptance table; it confirms the fixed clock domain and leaves the
count-based conclusion untouched. A full latency study would need its own run.
