# UDP segment attribution (audit Q36)

Q36 asks for sender / queue / ingress / reply / drain attribution before any weak-network performance
number is believed. This page defines those segments and states exactly what the in-tree counters can
and cannot prove.

## The segments

| segment | counter | where it is taken | what a non-zero value means |
|---|---|---|---|
| sender | client `PacketConn` write success | caller-side write; not a service counter | the datagram was handed to the tunnel |
| queue | `UDPStats.QueuedBytes`, `DroppedDatagrams` | `udpBudget.acquireBytes` / per-flow queue | bytes are waiting, or a queue limit dropped the datagram |
| ingress | `UDPStats.DatagramsReceived` | `udpSession.loop` after a valid client frame | the datagram REACHED the tunnel, before any admission decision |
| reply | `UDPStats.DatagramsSent` | `udpSession.writeFrame` on a successful write+flush | the target answered and the tunnel returned it |
| drain | `UDPStats.AssociationsExpired` | idle sweep's winning `closeIfIdle` | an association (slot and queued bytes) was reclaimed without the stream ending |

The first two were already present for the UDP limits; ingress/reply/drain were added for Q36 because
without them a blackhole and a tunnel-side drop look identical from outside (both are "no reply").

## Local evidence (Q35's real path)

`TestUDPBlackholeMatrix` runs H2 and, under `with_quic`, H3 against the real service:

- 4 client datagrams: one during the blackhole, three after it is lifted;
- ingress = **4** (all reached the service), reply = **3** (the blackholed one was received and never
  answered), queue drops = **0**, associations expired >= **1** (the drain segment);
- the reply order and the exact reply count are asserted, so a duplicate/late reply fails the test.

That is the attribution primitive: for this path, "no reply" is split into "not received" (ingress
short), "received but not answered" (reply short while ingress is complete), "dropped in the queue"
(queue counters), and "association reclaimed" (drain).

## What this does NOT claim

It does not produce the B8/H3 weak-network throughput or tail-latency conclusion. That needs an
exclusive, calibrated run with the official comparison and the same success count (Q38's
environment). The counters are what such a run would read; they are not a performance result, and no
number here may be cited as one.

For MASQUE the byte owner is the device's `tun.OutboundQueue` (`MaxQueuedBytes`, drop-and-release on
full), inventoried in Q16; this transport does not add a second queue to count.
