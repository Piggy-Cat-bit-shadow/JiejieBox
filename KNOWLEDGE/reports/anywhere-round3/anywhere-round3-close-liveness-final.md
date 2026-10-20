# Anywhere round three — close liveness, final

**Date:** 2026-10-20 (Asia/Shanghai)
**Branch:** `testing` only. No branch was created, nothing was force-pushed.
**Runs on:** `11c4c1b27` (start of this pass) plus the one test file added by this pass; dependency
unchanged at `Piggy-Cat-bit-shadow/sing-tun` `276576ea222c`
(`v0.0.0-20261020040525-276576ea222c`).
**Verdict:** **`NO_CHANGE_SUPPORTED_BY_EVIDENCE`** — with the mechanism now measured, not assumed.

---

## 1. The question

The previous passes established two things separately: a blocked port write holds the stage lock and
delays `Close`, and a *silent* QUIC path ends that block at the idle timeout (5.80 s at 6 s). The gap
was the counterexample: the idle timer is refreshed by **received** packets, so a peer that keeps
sending valid QUIC traffic while our queue is full might keep the connection alive forever — and then
nothing would end the block.

This pass ran that counterexample.

## 2. The experiment

Real quic-go connection on loopback, through a relay that can break **one direction only**:

| role | configuration |
|---|---|
| server (peer) | `MaxIdleTimeout: 0` (**disabled**), keepalive every 200 ms |
| client (us) | `MaxIdleTimeout: 3 s` |
| relay | after warm-up: **drop client→server**, keep forwarding server→client |

The server's idle timeout being disabled is deliberate: it removes the easy way out and makes the
client's own timer the only thing that could end the connection. The relay drops only our direction,
which is what a broken outbound path during a network switch looks like.

Both cases are now committed as `transport/http/close_liveness_mechanism_test.go` (`with_quic`).

### Event timeline (measured, monotonic from the break)

```
t+0.000s   outbound dropped; 64 datagrams already warm; queue saturated
t+2.0xx s  a datagram send needs >50 ms  (queue full, waiter present)
t+4.848s   LAST inbound packet from the peer (20 packets in total)
t+5.xxx s  send calls continue to return, queue still saturated
t+7.849s   SendDatagram fails: timeout: no recent network activity
           = last inbound + 3 s (the configured idle timeout), not from t+0
```

The silent control, same build, same machine:

```
t+0.000s   both directions dropped
t+3.0xx s  SendDatagram fails: timeout: no recent network activity
```

## 3. Did the four conditions hold?

| condition | result |
|---|---|
| 1. connection established, queue unable to accept immediately, a send actually waiting | **yes** — a send needed >50 ms with the queue saturated |
| 2. local periodically receives and parses valid QUIC packets; the idle timer is refreshed | **yes, and this is the important half** — the connection lived 4.85 s with a 3 s idle timeout, so inbound traffic demonstrably refreshed the timer |
| 3. `Flush` blocked across the lock while `Close` is triggered | **separately established** (committed `sing-tun` test: `Close` waits ≥200 ms, `Dispatch` waits, `ResetNetwork` does not) |
| 4. hold the state beyond one full idle timeout and observe | **it cannot be held** — the inbound stream stops at 4.85 s because the peer is starved of acknowledgements |

So the counterexample fails, but not for the reason the earlier report implied. The reason is a
property of QUIC congestion control, and it is worth stating plainly:

> A QUIC peer that receives no acknowledgements cannot keep sending. Its congestion window never
> opens. In this test it managed 20 packets and then went quiet, and the client's connection died
> exactly one idle timeout after the last one.

The bound is therefore **`last inbound packet + idle timeout`**, and `last inbound` is bounded by the
peer's send window when we cannot ACK. With a compliant peer on a broken outbound path that is a few
seconds; with any peer it is finite, because sending consumes congestion window and nothing
replenishes it without our acknowledgements.

## 4. The two send branches

| branch | mechanism | result |
|---|---|---|
| **DATAGRAM** | 32-frame queue, `Add` waits for a pop; pops continue while the packetizer runs, and the connection dies at `last inbound + idle` | **measured**, bounded, test committed |
| **Capsule** (reliable stream, flow control) | a stalled stream write waits for the peer's `MAX_STREAM_DATA`; those frames are themselves congestion-controlled on the same broken path | **not measured**; same starvation argument applies, and the analysis says the bound is the same class. Stated as analysis, not as a pass |

## 5. What the earlier reports should say from now on

The debug report said the wait is "bounded by the idle timeout". Precise version, superseding it for
the active-traffic case:

> The wait is bounded by the connection's death, which happens one idle timeout after the last packet
> received. Inbound traffic refresh that timer, but a peer cannot sustain inbound traffic while we are
> unable to acknowledge it, so the timer is still reached. The bound is therefore finite in every
> configuration measured, but it is not "always exactly the configured idle timeout from the start of
> the block".

## 6. Production change: none, and why

The instruction allows two outcomes. The evidence supports the second: the stall cannot be made
unbounded in this system, because sustaining the inbound stream that would refresh the idle timer
requires our acknowledgements, which the blocked outbound path is precisely what prevents.

A fix would still be possible (bind the endpoint's QUIC client to the scope context so cancellation
closes the connection before the inbound cleanup runs), and it would shorten the stall from
`idle timeout` to milliseconds. It is **not** justified by this evidence: there is no unbounded wait
to repair, and that change alters endpoint shutdown semantics for every MASQUE user. The exit
condition from the debug report stands: if field data ever shows a stall users notice, that is the
change to make, with its own before/after measurement.

## 7. Verification

| command | result |
|---|---|
| `go test -tags with_quic ./transport/http -run 'TestTheIdleTimeoutBoundsASilentPath\|TestActiveInboundStopsBecauseThePeerIsStarved' -count=1` | PASS (3.3 s + 8.2 s) |
| `go test -tags with_quic ./transport/http -count=1` (full package) | PASS |
| `go test ./transport/http ./transport/masque ./protocol/tun -count=1` | PASS |
| `go test -race -tags with_quic ./transport/http -run 'TestTheIdleTimeout\|TestActiveInbound'` | PASS |
| `go mod tidy -diff` in both modules | clean |

Known flakes, unchanged and attributed in the debug report: `route` under load, and the `sing-tun`
Windows timing tests (`TestGoKernelDeadline`, `TestGoKernelPacketBatchIdle`, `TestGoKernelFlowControl`
— the last one fails identically on the pre-change code).

## 8. What is still not verified

1. The **composed** `Box.Close` wall time (real TUN + MASQUE endpoint). `tun.Options` has no
   injectable device; the Go stack cannot be started without a platform TUN. Both edges are measured
   separately and the graph is verified; the composition is `NOT_TESTED`.
2. The **Capsule** branch under a stalled reliable stream: analysis only, no experiment.
3. WAN, real devices, iOS/Android footprint: `NOT_TESTED`.

## 9. Verdict

**`NO_CHANGE_SUPPORTED_BY_EVIDENCE`.** The blocked send is bounded in every configuration measured,
including the one designed to defeat the bound, and the mechanism that bounds it is now a committed
test rather than a sentence in a report. No production code changed; no dependency changed; the only
addition is the mechanism test that makes the argument reproducible.
