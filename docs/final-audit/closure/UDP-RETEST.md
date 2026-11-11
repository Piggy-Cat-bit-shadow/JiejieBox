# UDP instrument and B8 (reconciled 2026-11-11)

## Instrument: FIXED_AND_VERIFIED

The lab's UDP cell now measures rather than assumes:

- paced `-udp-rate` (0 = historical unpaced), absolute schedule, skip-never-burst;
- 20-byte header: magic, epoch, sequence, sent_ns; one sequence counter per phase;
- reader parses the SOCKS5 UDP header and the payload header; unique, duplicate,
  out-of-order, foreign and stale are distinct; a bounded sequence window;
- full `-udp-drain` window after the last write (default 500 ms paced, `-1`
  disables, 0 selects the default); `drainCompleteFor` is the single definition:
  live context, a real window when replies are missing, and a tail not within
  50 ms of the edge; otherwise `lost`/`loss_percent` are null;
- target `Echoed` counted only after a successful write; `target_received` and
  `echoed` are separate; runner quiesces the target before the measured phase;
- run JSON records `load_parameters` including stagger/rate/drain; schema v1.1
  accepts the new counters and null loss.

Tests: `pacer_test.go`, `udp_integration_test.go`, `udp_record_test.go`,
`drain_test.go`; all pass. Calibration at 0 % loss returns 2500/2500/2500 in both
MASQUE and TrustTunnel H2.

## Legacy B8 data: SUPERSEDED

The 18 reps collected earlier (drain 500 ms) were re-read from raw JSON under the
final rule: **7/18 are drain-incomplete** (MASQUE 1 % 3/3, TrustTunnel 0.1 % 1/3,
TrustTunnel 1 % 3/3), so their `drain_complete=true` is not acceptable evidence.
The record once flagged as a pacer violation (`scheduled=2500, attempts=2500,
skipped=11`) actually has `attempts=2489` and satisfies the invariant. Old rows
remain on disk under `results/b8/` for context but are **SUPERSEDED** and are not
used for the conclusion.

## Final B8: UNRESOLVED, with a narrowed locus

`results/b8-final/` (manifest, raw runs, qdisc/socket diagnostics, recheck) uses
the final instrument: 80 ms RTT, 500 pps, 1200 B, 5 s, 3 reps, drain 2 s, plus a
50 pps control at 1 %.

- 0 % and 0.1 % per direction: 2500/2500/2500 and ~0.1-0.2 % loss, instrument
  calibrated.
- 1 % at 500 pps: MASQUE returns 1302-1491 unique (target 1315-1510), loss
  40.4-47.9 %, drain complete with a 4 s window (the 2 s attempt was incomplete
  and is superseded); TrustTunnel returns 459-621 (target 1145-1571), loss
  75-82 %, drain complete.
- 1 % at 50 pps: MASQUE 0.8-2 %, TrustTunnel 0 %, both drain-complete.

Why it is not path loss: the veth qdisc drops only the expected ~1 % of wire
packets (60-96 packets), there are no overlimits/requeues, and every UDP socket
counter (`UdpRcvbufErrors`, `UdpSndbufErrors`, `UdpInErrors`) is zero in both
namespaces. The missing datagrams are lost inside the client/tunnel application
queues, only at the high offered rate; at 50 pps the same 1 % configuration is
clean. Per-queue counters inside QUIC/H2 are **NOT_AVAILABLE** without code
instrumentation, so the root cause is bounded, not proven.

Next diagnostic step (not this round): instrument the MASQUE device/CONNECT-IP
queue and the TrustTunnel H2 stream queue for drop counts and blocking time, and
repeat the 1 % cells; do not change production transport semantics first.
