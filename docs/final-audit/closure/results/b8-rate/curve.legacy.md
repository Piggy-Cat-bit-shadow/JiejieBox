# B8 rate curve at 1 % netem loss per direction (80 ms RTT)

All runs: 1200 B, 1 stream, 5 s measured, 1 s warmup, 3 reps, per-direction 1 %
loss (`~2 %` expected end to end). `loss` is null when the drain tail was not
observed. Source runs: `b8-rate*/`, manifest beside this file.

## MASQUE CONNECT-IP (QUIC DATAGRAM)

| offered rate | drain | loss per rep | last reply | note |
|---|---|---|---|---|
| 100 pps | 2 s | 0.6 / 1.4 / 2.6 % | 0.07-0.10 s | near the ~2 % path budget |
| 250 pps | 4 s | 1.68 / 1.84 / 2.96 % | 1.1-1.4 s | near budget, tail growing |
| 300 pps | 4 s | 1.93 / 8.47 / 25.4 % | 1.1-2.6 s | transition, high variance |
| 350 pps | 4 s | 10.5 / 15.3 / 24.6 % | 1.4-2.2 s | above budget |
| 400 pps | 4 s | 25.8 / 35.0 / 37.8 % | 1.5-2.7 s | collapsed |
| 500 pps | 4 s | 40.1 / 43.8 / 47.1 % | 1.7-3.3 s | collapsed |

The transition starts between 250 and 300 pps (2.4-2.9 Mbit/s payload), far below
the 1 Gbit/s link. A 2 s drain at 250-500 pps truncated the tail in several reps
(loss null, marked incomplete); the 4 s runs above are authoritative.

## TrustTunnel H2 UDP2 (reliable stream)

| offered rate | drain | loss per rep | last reply | note |
|---|---|---|---|---|
| 100 pps | 2 s | 0 / 0 / 0 % | 0.10-0.46 s | clean |
| 250 pps | 4 s | 0 / 0 / 0 % | 0.8-2.4 s | clean, tail seconds |
| 500 pps | 2 s | 0 / 77.2 / (null) % | 2.0-2.1 s or >2 s | drain too short, invalid |
| 500 pps | 8 s | 0 / 0 / 0 % (one set); 0 / 77.2 / (null) (another set) | 5.5-10.7 s | bimodal: all delivered 5.5-7.8 s late, or 77 % lost with tail beyond 8 s |

TrustTunnel does not lose cleanly at 500 pps; it develops multi-second queueing
delay, and whether the cell reports loss depends on whether the drain covers the
tail. That is a latency/queue collapse, not path loss.

## Diagnostics and bounds

Across the 1 % cells, the veth qdisc drops only the configured ~1 % of wire
packets, there are no overlimits/requeues, and all UDP socket error counters
(`UdpRcvbufErrors`, `UdpSndbufErrors`, `UdpInErrors`) are zero in both namespaces.
The client qdisc sees 40-54 % fewer packets at 500 pps than at 0 %, so the
backlog/drop is inside the client tunnel/queue path before the veth. The exact
queue (MASQUE device/CONNECT-IP queue vs QUIC send buffer; TrustTunnel H2 stream
queue/flow-control window) is **NOT_AVAILABLE** without in-process counters.

## What this changes

B8 stays `UNRESOLVED`, but the failure is characterised: a rate threshold between
250 and 300 pps for MASQUE and a multi-second queueing collapse (not steady drop)
for TrustTunnel at 500 pps, with the network path and socket buffers exonerated.
The next step is to instrument those two queues for drop/blocking time, not to
change transport semantics.
