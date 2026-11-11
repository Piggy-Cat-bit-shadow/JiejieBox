# UDP retest and B8

## Verdict of this round

The old UDP cell could not measure loss; a paced, sequenced, drained mode now
does, and the question was re-asked. B8 stays `UNRESOLVED`, but the old
`CONFIRMED_NOT_A_BUG` ("UDP has no retransmission") is withdrawn: under a
calibrated 500 pps offered load, 1 % per-direction netem loss loses about
**50-60 % of datagrams end to end**, and roughly the same amount already at
target ingress, i.e. far beyond the ~2 % the configured loss predicts. That is a
real data-path finding, not a test artifact, and needs transport-level work.

## Measured with the new instrument (80 ms RTT, 500 pps, 1200 B, 3 reps)

| cell | configured loss/direction | writes | unique returned | target received | reported loss |
|---|---|---|---|---|---|
| MASQUE DATAGRAM | 0 % | 2500 ×3 | 2500 ×3 | 2500 ×3 | 0 % |
| TrustTunnel H2 UDP2 | 0 % | 2500 ×3 | 2500 ×3 | 2500 ×3 | 0 % |
| MASQUE DATAGRAM | 0.1 % | 2500,2500,2489 | 2498,2496,2484 | 2499,2498,2487 | 0.08-0.20 % |
| TrustTunnel H2 UDP2 | 0.1 % | 2500 ×3 | 2500,2500,2491 | 2500,2500,2491 | 0-0.36 % |
| MASQUE DATAGRAM | 1 % | 2500 ×3 | 869-1138 | 915-1234 | 54-65 % |
| TrustTunnel H2 UDP2 | 1 % | 2500 ×3 | 1044-1248 | 1072-1606 | 50-58 % |

All cells: `skipped=0`, `drain_complete=true`, no duplicates (MASQUE: two
out-of-order in one 0.1 % rep), achieved pps 497-500 except two scheduler-short
reps (348/366 pps). Configured q is per direction, so the expected e2e loss is
~2q-p² (0.2 % and 1.99 %); the 1 % rows are 25-30× that, and the loss is already
present at the target, so it is upstream of the return path and not the drain
(0 % rows prove the instrument). MASQUE and TrustTunnel are not ranked against
each other (different reliability models), but both show the amplification.

## Status

- Instrument: `FIXED_AND_VERIFIED` (paced mode, sequence header, drain, target
  and echo counters, quiesce; smoke and the 0 % rows above).
- B8: `UNRESOLVED` with the table above and raw runs in `results/b8/`. Next step
  is transport-level: qdisc stats vs client queue vs server queue for the 1 %
  cells, starting with the client-side drop counters; no protocol conclusion is
  drawn yet.

## Why the old cell is not a loss experiment

Audited in `scripts/perf/internal/loadgen` and `runner`:

1. No pacing: a goroutine writes back-to-back until the wall deadline.
2. No sequence/token: every datagram is the same PatternPage; the reader counts
   any `n >= 3` reply, so duplicates, reorder and foreign packets are identical.
3. `DatagramsSent` is "the local socket accepted it" (SOCKS write success), not
   client/tunnel emission.
4. The reader stops at the send deadline; there is no drain. At 80 ms RTT a 5 s
   phase loses ≈1.6 % of replies to teardown alone.
5. `streams=4` shares one SOCKS UDP association, so a "4-stream" UDP cell is one
   4-tuple with four writers.
6. The target's ingress counter is fetched and discarded; warmup backlog can
   pollute it, and there is no quiesce before reset.
7. A 100 %-loss cell is recorded as a failure, not as data.
8. MTU is a literal 1200 in the runner; MASQUE's outer packet is inner MTU + 51,
   which blackholes below a 1500 path MTU.
9. `elapsed` is the configured duration, not the actual send window.
10. UDP latency is allocated but never filled; jitter is sender inter-arrival.

Consequence: `lost = sent - received` blends sender saturation, socket buffers,
SOCKS relay, tunnel backpressure, queue policy, real link loss and teardown. The
1 %-loss collapse that stage 1 reported could be dominated by any of (1)-(4).

## Designed fix (not implemented)

- Mode by `--udp-rate`: 0 = `unpaced` (historical, labelled stress), >0 = paced.
- 20-byte header in the fixed packet: magic, epoch, seq, sent_ns.
- Pacer: absolute schedule, timer reuse, skip-never-burst, per-write deadline,
  ctx honoured; `scheduled == attempts + skipped`.
- Reader parses the SOCKS header + payload header; seq window yields unique,
  duplicate, out-of-order, foreign, stale; `lost = writes - unique`.
- Drain always runs `--udp-drain` (default 500 ms, auto mode) after the last
  write; drain is reported separately and never enters goodput.
- Target counts `Echoed` only after a successful write; runner quiesces the
  target before reset and reports `target_received`, separating upstream loss
  from return loss.
- Schema gets the new counters and `mode`; summary emits UDP rows.

Tests named in the audit: payload layout/magic, seq window (duplicate, reorder,
bounded memory), pacer rate/skip/deadline/ctx, loopback paced round trip,
duplicate/reorder/stale/foreign injection, drain capture and bound, target count
match, runner loss derivation and null-when-unavailable.

## B8 rerun matrix (ready to execute)

Calibrate rate at 0 % loss on profile `normal` (20 ms RTT), ladder
200→500→1000→2000→4000 pps, accept the last rung where `skipped=0`,
`drain_complete=true`, `loss=0`; use half of it for 0 / 0.1 / 1 % loss on
MASQUE DATAGRAM and TrustTunnel H2 UDP2, 5 reps × 5 s, 1200 B, drain 500 ms.
Note netem applies per direction, so configured p is ≈2p−p² end to end; the
report must compare against that, not against p.
