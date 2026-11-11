# Final status — UDP evidence closure

**READY_WITH_EXPLICIT_BLOCKERS.**

## Fixed in this round

1. Drain is a state machine, not a 50 ms quiet test. `observation_complete`,
   `quiet_period_ms`, `tail_active`, `drain_status`, `loss_estimable` and
   `not_returned_by_deadline` are separate. Loss is emitted only when estimable;
   deadline misses stay visible without being called network loss.
2. Target warmup/previous-rep contamination is removed by per-epoch counters read
   from the measured epoch; `quiesceTarget` returns a structured result and is no
   longer the proof for the measured count.
3. Pacer records configured, scheduled, attempt and write-completion rates plus
   write errors, phase duration/overrun and last schedule/write timestamps.
4. Effective parameters are recorded after defaults/overrides.
5. Runner and kernel provenance are separated: runner SHA + tree state, kernel
   SHA before/after, `binary_changed`, cache dirty disclosure.
6. Sequence-window truncation sets `sequence_tracking_complete=false`, counts
   unattributed replies separately and forbids a loss percentage.
7. B8 reports are generated from raw JSON and checked; the TrustTunnel 500/2 s
   row is corrected to `legacy_incomplete` (no 77.2 % loss).
8. All local timing uses monotonic offsets from phase start; a MASQUE 250 pps calibration run validates the path and old latency/jitter are labelled legacy_timing.


## B8 result under the fixed instrument

- MASQUE 250 pps: 0 % / 1.60-7.28 % loss (0 % / 1 % netem; one outlier), complete.
- MASQUE 500 pps 1 %: 36.6-51.3 % loss, complete quiet; target ingress also short.
- MASQUE 50 pps 1 %: 2.0-3.2 % loss.
- TrustTunnel 250 pps: 0 % at both losses.
- TrustTunnel 500 pps 1 %: 8 s leaves 1/3 reps tail-active; at 12 s all 2500
  arrive, last reply 5.5-8.3 s. It is delay/queueing, not loss.

B8 remains **UNRESOLVED**: MASQUE loses inside the client/tunnel path at high
rate; TrustTunnel accumulates multi-second delay. The exact queue remains
`NOT_AVAILABLE` and needs in-process counters.

## Old data

- `results/b8-final/`: runner source dirty at run time, no runtime hash;
  `PROVENANCE_INCOMPLETE`/`SUPERSEDED`. Recovered diff stored.
- `results/b8-rate/`: clean runner code, legacy semantics; table regenerated with
  `legacy_*` labels and `not_returned_by_deadline`.
- Historical JSON was not edited.

## Not run / blocked

- Platform devices and privileged TUN: `NOT_RUN`.
- Quiesce timeout/confirmation/unavailable paths: tested in `quiesce_test.go`.
- B8 root-cause instrumentation inside QUIC/H2: `DEFERRED`.
- Mieru retry: `DEFERRED` (no semantic change).
- B7 matrix and other protocols: not rerun by design.

## Scope

No production protocol changes. No B7 rerun. No merge to `testing`. No force-push.
