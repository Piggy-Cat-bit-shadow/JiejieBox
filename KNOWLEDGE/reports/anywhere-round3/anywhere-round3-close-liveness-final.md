# Anywhere round three — close liveness, final

**Date:** 2026-10-20 (Asia/Shanghai)
**Branch:** `testing` only. No branch was created, nothing was force-pushed.
**Runs on:** `11c4c1b27` (start of this pass) plus the one test file added by this pass; dependency
unchanged at `Piggy-Cat-bit-shadow/sing-tun` `276576ea222c`
(`v0.0.0-20261020040525-276576ea222c`).
**Verdict:** **`PARTIALLY_VERIFIED_ENV_BLOCKED`** — the DATAGRAM branch is bounded (measured); the
Capsule branch is **not** bounded when a keepalive is in use (measured), and the shutdown path that
would wait on it is now released by a cancellation-ordering fix whose release mechanism is verified
end to end at the transport level. The composed `Box.Close` wall time remains `NOT_TESTED` because
this environment has no injectable TUN device.

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
| server (peer) | quic-go's **default** idle timeout (`MaxIdleTimeout: 0` selects the 30 s default; it does not disable the timeout), keepalive every 200 ms |
| client (us) | `MaxIdleTimeout: 3 s` |
| relay | after warm-up: **drop client→server**, keep forwarding server→client |

The server sits at quic-go's default (30 s) and the client at 3 s, so the effective timeout is the
client's, which is the value the experiment varies. The relay drops only our direction, which is what
a broken outbound path during a network switch looks like.

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

| branch | mechanism | measured result |
|---|---|---|
| **DATAGRAM** | 32-frame queue; `Add` waits for a pop. A peer without our ACKs cannot keep sending, so inbound stops and **our** idle timer ends the connection | bounded by **our** idle timeout after the last inbound packet (5.80 s at 6 s; 3.0 s at 3 s) |
| **Capsule** (reliable stream, flow control) | the write parks on the peer's stream window. With **no** keepalive, both sides go quiet and the peer's timer ends it. With the production keepalive, the peer keeps receiving our PINGs and we keep receiving its ACKs, so **no timer fires at all** | **no keepalive:** bounded by the peer's idle timeout (9.03 s at 10 s). **with a 200 ms keepalive:** still parked after 12 s (> 4 idle timeouts) with the connection alive — **no idle-timeout bound** |

The two branches have **different bounds, and the capsule one disappears entirely under a keepalive**.
That is why they had to be measured separately, and why the first capsule test (no keepalive) was not
enough: it measured the timer, not the branch.

The operational consequence: a peer that accepts the tunnel and stops reading its request body can
park the write — and the stage lock behind it — for as long as the connection lives, because our own
keepalive is what keeps it alive. The fix for that is §6.

## 5. What the earlier reports should say from now on

The debug report said the wait is "bounded by the idle timeout". Precise version, superseding it for
the active-traffic case:

> The wait is bounded by the connection's death, which happens one idle timeout after the last packet
> received. Inbound traffic refresh that timer, but a peer cannot sustain inbound traffic while we are
> unable to acknowledge it, so the timer is still reached. The bound is therefore finite in every
> configuration measured, but it is not "always exactly the configured idle timeout from the start of
> the block".

## 6. Production change: the endpoint now releases its connection on cancellation

A second experiment changed the picture, and this section replaces the earlier "no change" reasoning.

**The gap in the first capsule test:** it used no keepalive. Without one, a parked write also means
we send nothing, so the peer's idle timer expires and closes the connection — which looked like a
bound. The production MASQUE client does configure a keepalive (`defaultKeepAlivePeriod` in
`transport/masque`), and the follow-up experiment adds it, with explicit non-zero idle timeouts on
both sides (3 s) instead of relying on the zero-value default:

| case | measured result |
|---|---|
| capsule, **no** keepalive | parked write returned after **9.03 s** (peer idle 10 s) — the old control |
| capsule, **200 ms keepalive** | after 12 s (> 4 idle timeouts) the connection is **ALIVE** (a second CONNECT-UDP request succeeds on it) and the parked write is **still parked**; closing the tunnel releases it in **0 s** with `deadline exceeded` |

So the capsule branch has **no idle-timeout bound** when a keepalive is in use. That is the
counterexample this round was asked to settle, and it settles the other way.

**Why it matters for shutdown.** `Scope.Close` cancels its context *before* running cleanups, then
runs them in reverse: the TUN inbound closes first and waits for its stack engine, and that engine
can be inside `ForwardStage.Flush` holding the stage lock while it writes through this endpoint. The
cleanup that would release the write — `c.client.Close` — is scheduled *after* the inbound that waits
for it. Nothing in the write path ends it. The chain is a wait with its release scheduled behind the
waiter, and for the capsule branch there is no timer to break the tie.

**The fix (minimal, one watcher).** In `ClientEndpoint.Start`'s post-start stage, next to the existing
`scope.Add(c.client.Close)`, the endpoint now also waits on its own context:

```go
go func() {
    <-c.ctx.Done()
    _ = c.client.Close()
}()
```

`Scope.Close` calls that context's cancel before any cleanup, so the connection is closed before the
inbound above it begins to wait. Normal traffic is unaffected: the context is only cancelled at scope
close, not on an interface update or a session restart. The second `Close` from the cleanup is safe —
`Client.Close` cancels an idempotent context, receives from an already-closed channel, and closes the
HTTP client again.

**What is proven about the fix, and what is not:**

| claim | evidence |
|---|---|
| closing this connection releases a parked capsule write | `MEASURED`: 0 s, `deadline exceeded` |
| the cancel happens before cleanups | `SOURCE_VERIFIED` (`Scope.Close`: `s.cancel()` before `slices.Backward(cleanups)`) |
| normal close, restart, suspend still work | `protocol/masque` `with_quic` suite: PASS (48 s) |
| the composed `Box.Close` is fast in production | `NOT_TESTED` — no injectable TUN device in this environment |

## 7. Verification

| command | result |
|---|---|
| `go test -tags with_quic ./transport/http -run 'TestTheIdleTimeoutBoundsASilentPath\|TestActiveInboundStopsBecauseThePeerIsStarved' -count=1` | PASS (3.3 s + 8.2 s) |
| `go test -tags with_quic ./transport/http -run 'TestCapsuleWriteWithoutKeepAliveEndsAtThePeerIdleTimeout\|TestCapsuleWriteWithKeepAliveStaysParked' -count=1` | PASS (10.1 s + 23.0 s) |
| `go test -tags with_quic ./protocol/masque -count=1` (endpoint fix) | PASS (48.3 s) |
| the mechanism set together under `-race -count=2` | PASS |
| `go test -tags with_quic ./transport/http -count=1` (full package) | PASS |
| `go test ./transport/http ./transport/masque ./protocol/tun -count=1` | PASS |
| `go mod tidy -diff` in both modules | clean |

Known flakes, unchanged and attributed in the debug report: `route` under load, and the `sing-tun`
Windows timing tests (`TestGoKernelDeadline`, `TestGoKernelPacketBatchIdle`, `TestGoKernelFlowControl`
— the last one fails identically on the pre-change code).

## 8. What is still not verified

1. The **composed** `Box.Close` wall time (real TUN + MASQUE endpoint). `tun.Options` has no
   injectable device; the Go stack cannot be started without a platform TUN. Both edges are measured
   separately and the graph is verified; the composition is `NOT_TESTED`.
2. WAN, real devices, iOS/Android footprint: `NOT_TESTED`.

## 9. Verdict

**`PARTIALLY_VERIFIED_ENV_BLOCKED`.**

- **DATAGRAM:** bounded, measured, mechanism named (our idle timeout after the last inbound packet,
  because a peer without our ACKs cannot keep sending). Test committed.
- **Capsule without keepalive:** bounded by the peer's idle timeout (9.03 s at 10 s). Test committed.
- **Capsule with the production keepalive:** **not bounded by any idle timeout** — measured parked
  after 12 s with the connection demonstrably alive. This is the counterexample the round was asked to
  settle, and it settles against the earlier conclusion.
- **Production change:** the endpoint now closes its own QUIC client when the scope context is
  cancelled, which `Scope.Close` does before it runs any cleanup. The release mechanism is measured
  (closing the connection frees the parked write in 0 s); the cancel ordering is source-verified; the
  endpoint's full `with_quic` suite passes, so normal close, restart and suspend are unaffected.
- **Not verified:** the composed `Box.Close` wall time, because this environment has no injectable TUN
  device (`tun.Options` has no device field, and a public fake-TUN API is explicitly out of scope). The
  claim that shutdown is now fast is `INFERRED` from the two verified halves, not measured end to end.

A later round that can run a real TUN should measure `Box.Close` against this same stalled-capsule
peer before and after the watcher, which is the only missing link in the chain.
