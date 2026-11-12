# MASQUE data-path segments and bounded degradation (audit Q36)

Q36 asks for end-to-end attribution and either a production fix or a bounded degradation strategy.
This page states the segments, the bounded policy, and what the local evidence can and cannot prove.

## Segments

| segment | counter / owner | meaning |
|---|---|---|
| sender | `SegmentStats.DatagramsFromDevice` | the device handed this datagram batch to the tunnel |
| queue | device `tun.OutboundQueue` (`MaxQueuedBytes`) + `Dropped` counters | the byte queue is the device's; Q16 inventories it. This transport does not add a second queue |
| local drop | `DatagramsDroppedNoSession`, `DatagramsDroppedNotReady` | this layer discarded the datagram because there is no session or it is not ready; the buffers are released |
| reply | `SegmentStats.DatagramsToDevice` | a packet arrived from the tunnel and was written to the device |
| drain | session ledger `Terminated` + queue release | the session's run() ended / queued buffers were released at close |

The TrustTunnel side has the same five segments in `Service.UDPStats` (received/sent/dropped/expired),
covered by the Q35 matrix.

## Bounded degradation strategy

The data path never grows an unbounded queue:

1. **Device queue is a byte budget** (`tun.OutboundQueue.MaxQueuedBytes`, default 8 MiB across the
   inbound ring and all outbound queues). When full, the packet is dropped and its buffer released;
   there is no retry queue behind it.
2. **No session / not ready**: this transport drops and releases immediately. It does not park
   packets waiting for a reconnect; the device already applies its own queue budget.
3. **QUIC send queue**: 32 frames per batch, and a session that cannot take a 64-packet TUN burst
   chunks it; the part that fits is queued, the rest is dropped by the queue owner. No unbounded
   per-session buffer.
4. **Deadlines**: callers see deadline errors (or the documented lack of `SetReadDeadline` on the
   TrustTunnel UDP wrapper) instead of an indefinite wait; the caller's own select is the absolute
   bound where the wrapper has no deadline.
5. **Close/drain**: closing a session releases queued buffers; the session ledger's `Terminated`
   counts completed releases, not detach calls.

This is the "bounded degradation" branch of Q36's minimum condition: under overload the path drops
and releases, it does not grow or stall indefinitely.

## Local evidence

`transport/masque/segment_test.go` drives the counters through the real client methods:
- no session: `FromDevice=1`, `DroppedNoSession=1`, `ToDevice=0`;
- published but not ready: `DroppedNotReady=1`;
- ready session + a valid IPv4 packet: `ToDevice=1`.

The queue policy itself is pinned by the Q16 tests (`backpressure_test.go`, `batch_chunk_test.go`),
and the TrustTunnel bound by the Q35 matrix.

## What this does NOT claim

It does not claim a throughput or tail-latency result for B8/H3 weak networks. That still needs
Q38's exclusive, calibrated environment and the official comparison with the same success count; no
number here may be cited as a performance conclusion. What it does claim is that every packet has an
attributed fate and that overload degrades by bounded drop-and-release rather than unbounded growth.
