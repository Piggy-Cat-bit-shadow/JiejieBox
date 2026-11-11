# B8 final recheck (2026-11-11)

## Provenance

HEAD `f03b1c7289934dcc93db3b6886ce4d8547f836fe` (dirty for lab/doc files only,
listed in each `run.json`); kernel binary
`0157e713c8e2d43fc214e949cde40b9e3fc2dc812360c64cf41ea4b91f2bd3d6`, Go 1.25.5,
CGO off, 19 lab tags, wireguard-go `v0.0.0-20261111081811-7ece3d66d79d`.
Perflab sha256 `1271cfdbdcc1ff644232bb01079d35f1bf4af09286872e5e8585982075f1af4c`.
Every run records `load_parameters` (rate, drain, duration, warmup, streams).

## Per-cell result (3 reps; `loss` is null when the drain was incomplete)

| cell | configured loss/dir | writes | unique returned | target received | reported loss | drain |
|---|---|---|---|---|---|---|
| MASQUE | 0 % | 2497-2500 | 2497-2500 | 2497-2500 | 0 % | complete |
| MASQUE | 0.1 % | 2499-2500 | 2495-2498 | 2496-2499 | 0.08-0.16 % | complete |
| MASQUE | 1 % @500 pps, 4 s drain | 2500 ×3 | 1302-1491 | 1315-1510 | **40.36-47.92 %** | complete (last 1.9-3.3 s) |
| MASQUE | 1 % @500 pps, 2 s drain (superseded attempt) | 2500 ×3 | 1265-1328 | 1356-1499 | null | incomplete (last 2.00-2.02 s) |
| MASQUE | 1 % @50 pps (control) | 250 ×3 | 245-248 | 245-249 | 0.8-2.0 % | complete |
| TrustTunnel H2 | 0 % | 2500 ×3 | 2500 ×3 | 2500 ×3 | 0 % | complete |
| TrustTunnel H2 | 0.1 % | 2500 ×3 | 2496-2500 | 2496-2500 | 0-0.16 % | complete |
| TrustTunnel H2 | 1 % @500 pps | 2500 ×3 | 459-621 | 1145-1571 | 75.16-81.64 % | complete (last 1.2-1.6 s) |
| TrustTunnel H2 | 1 % @50 pps (control) | 250 ×3 | 250 ×3 | 250 ×3 | 0 % | complete |

Invariants hold in all 27 reps: `scheduled == attempts + skipped`, `writes <=
attempts`, `returned == unique + duplicate`, `unique <= returned`; skipped was
0-3 and never entered loss. The legacy flagged record (`scheduled=2500`,
`attempts=2489`, `skipped=11`) satisfies the invariant; the earlier
`scheduled=2500, attempts=2500, skipped=11` reading mixed columns.

## Diagnostics (`diag/<cell>/before.txt` / `after.txt`)

- veth qdisc at 1 %: client dropped 60-94 packets of 8.0-9.5 k sent, server
  44-103; roughly the configured 1 %, no overlimits/requeues.
- **Client qdisc sent falls from 15.8 k (MASQUE 0 %) to 9.2 k (1 %), and from
  17.5 k to 8.0 k (TrustTunnel): 40-54 % fewer packets ever reached the veth.**
  The missing datagrams were dropped inside the client tunnel/queue path before
  the veth, not by netem, the target, or socket buffers.
- Every UDP socket counter (`UdpRcvbufErrors`, `UdpSndbufErrors`, `UdpInErrors`)
  is 0 in both namespaces in every cell.
- Direction: MASQUE target ≈ unique (e.g. 1486 vs 1465) so the loss is upstream
  of the target and the return path is nearly clean. TrustTunnel target 1145-1571
  vs unique 459-621: both directions are queue-limited.
- At 50 pps the same 1 % configuration is clean for both cells, so the switch is
  offered rate (4.8 Mbit/s over 80 ms RTT), not the loss percentage alone.

## What is NOT_AVAILABLE

Per-queue drop counters inside the QUIC/H2 stacks are not exposed by the current
build; locating the exact client queue (device queue vs transport send buffer)
needs in-process instrumentation (not added this round). Target quiesce outcome is
not recorded per run, so `target_received` is an upper bound if warmup residue
existed; the 0 % cells returning exactly 2500 make a large residue unlikely.

## Conclusion

B8 remains **UNRESOLVED**, bounded to sender-side tunnel/queue saturation under
1 % random loss at 500 pps over 80 ms RTT; kernel/veth/socket counters are clean
and the 50 pps control is clean. No production transport semantics were changed.
