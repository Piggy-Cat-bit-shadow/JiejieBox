# UDP segment attribution (audit Q36)

Q36 asks for sender / queue / ingress / reply / drain attribution before any weak-network
performance number is believed. This page defines those segments and states exactly what the in-tree
counters can and cannot prove.

## The segments

| segment | counter | where it is taken | what a non-zero value means |
|---|---|---|---|
| sender | client `PacketConn` write success | caller-side write; not a service counter | the datagram was handed to the tunnel |
| queue | `UDPStats.QueuedBytes` | `udpBudget.acquireBytes` / per-flow queue | bytes are waiting for the handler |
| queue drop | `UDPStats.DroppedDatagrams` | byte budget or the 64-item flow queue | a queue limit refused the datagram at admission; the association keeps working |
| ingress | `UDPStats.DatagramsReceived` | `udpSession.loop` after a valid client frame | the datagram REACHED the tunnel, before any admission decision |
| delivered | `UDPStats.DatagramsDelivered` | `udpFlow.ReadPacket` after the dequeue | the handler owns the datagram from here on |
| reply | `UDPStats.DatagramsSent` | `udpSession.writeFrame` on a successful write+flush | the target answered and the tunnel returned it |
| reply failed | `UDPStats.RepliesFailed` | `writeFrame` closed/encode/write/flush error, `WritePacket` on a closed flow | the handler answered and the tunnel could NOT return it (the client is gone or the stream broke) |
| drain | `UDPStats.AssociationsExpired` | idle sweep's winning `closeIfIdle` | an association (slot and queued bytes) was reclaimed without the stream ending |
| discarded | `UDPStats.DatagramsDiscarded` | `closeFlow`'s final drain, a lost push race, a failed dequeue copy, a frame read while the session was closing | the datagram was received and admitted, then dropped before delivery (normal when a stream closes under load) |

The first two were already present for the UDP limits. Ingress/reply/drain were added in round 10.
Delivered/discarded/reply-failed were added in round 11, because without them the segments did not
add up: a datagram drained at close (or admitted and then lost to a lost race) was attributed to no
segment, and a reply that could not be written looked exactly like a target that never answered.

## The conservation identities

At rest (`QueuedBytes == 0`, and the association count is whatever the test leaves):

```
DatagramsReceived == DatagramsDelivered + DroppedDatagrams
                   + RefusedAssociations + DatagramsDiscarded
DatagramsDelivered == DatagramsSent + RepliesFailed + still-with-the-handler
```

`RefusedAssociations` counts datagrams refused because a new association would cross a session or
service association limit; it is the fifth destination of an ingress datagram. "Still with the
handler" is the one term the transport cannot see: a handler that reads and does not answer (the
blackhole cell) is legitimately outside the reply equation, which is why `DatagramsDelivered` is
measured and `DatagramsSent` is not assumed to equal it.

`TestUDPSegmentsConserve*` and `TestUDPFullQueueIsARefusalNotAStall`
(`transport/trusttunnel/service_udp_segments_test.go`) assert the identity for: a saturated 64-item
queue closed under load, the byte budget, a failed reply, the echo path, and a queue that refuses the
overflow and is then drained.

## Local evidence (Q35's two matrices)

`TestUDPBlackholeMatrix` blackholes at the HANDLER (the service reads and does not answer). H2 and,
under `with_quic`, H3:

- 4 client datagrams: one during the blackhole, three after it is lifted;
- ingress = **4** (all reached the service), reply = **3** (the blackholed one was received and never
  answered), queue drops = **0**, associations expired >= **1** (the drain segment);
- the reply order and the exact reply count are asserted, so a duplicate/late reply fails the test.

`TestUDPTransportLossMatrix` injects the loss on the PATH instead, one direction at a time, at the
frame boundary of the client's `_udp2` stream (a whole encoded datagram frame is dropped, so the
stream stays aligned; dropping bytes under TLS was measured to produce `tls: bad record MAC`, which
models corruption, not loss):

| cell | injection | what the counters must say |
|---|---|---|
| ingress loss | client frames dropped | caller's write succeeds; `DatagramsReceived` unchanged; no association created for the destination |
| ingress recovery | frames flow again | the datagram is received and answered; the association that could not be created before now exists |
| egress loss | server frames dropped | the service received AND answered (`DatagramsSent` grows); the caller sees nothing |
| egress recovery | frames flow again | the caller sees the reply |
| reclaim while lossy | path down, client silent | `AssociationsExpired` grows, `Associations` returns to 0 |
| after reclaim | path restored | a NEW association is created by the datagram, and it is answered |

That is the direction map the review asked for. The original matrix's reference-implementation
columns (an official peer, an official control) are **not run** in this round: they need the
cross-implementation environment the user excluded, and no local test may be presented as them. What
the local matrix proves is that each direction of the path fails separately and is counted
separately, on both transports.

### The transports differ, and the difference is measured

HTTP/2 has no stream read deadline and says so: `NeedAdditionalReadDeadline() == true` and
`SetReadDeadline` returns `os.ErrInvalid`. HTTP/3 streams do have one
(`deadline` is the QUIC stream). The loss matrix pins both declarations. The server-side association
now honours a real read deadline (`udpFlow.SetReadDeadline`, `os.ErrDeadlineExceeded`), because the
router's fragmented-QUIC sniff reads further datagrams under that deadline; before it did not, and a
transport that declared the need had its first (already staged) datagram parked with it.

## What this does NOT claim

It does not produce the B8/H3 weak-network throughput or tail-latency conclusion. That needs an
exclusive, calibrated run with the official comparison and the same success count (Q38's
environment). The counters are what such a run would read; they are not a performance result, and no
number here may be cited as one.

The loss matrix models datagram-level loss, not a sustained link outage: it does not model QUIC
retransmission or a stalled TCP window, and no cell claims them.

For MASQUE the byte owner is the device's `tun.OutboundQueue` (`MaxQueuedBytes`, drop-and-release on
full), inventoried in Q16; this transport does not add a second queue to count.
