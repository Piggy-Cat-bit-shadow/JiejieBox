# Anywhere round three — independent acceptance report

**Date:** 2026-10-20 (Asia/Shanghai)
**Branch:** `testing` only. No branch was created, nothing was force-pushed.
**Reviewed change set:** the round-three construction, `d993c53e1` → `8f884e520`, dependency
`Piggy-Cat-bit-shadow/sing-tun` `fix/listenconfig-ipv6-only-selfheal` `a7bd7facc7cc` → `276576ea222c`,
pinned as `v0.0.0-20261020040525-276576ea222c`.
**Method:** every claim below was re-checked against the current sources and current `go.mod`, with a
new experiment where the construction could only say "bounded by QUIC's timers" without a number.
Numbers from the construction round are marked as such; numbers produced by this pass say so.

---

## 1. What this pass added

| question the construction left open | experiment | answer |
|---|---|---|
| Is the blocked send in the close path bounded at all? | a real quic-go connection whose path goes silent, **left open**, blocked send measured until it returns | **bounded**: 5.80 s at a 6 s idle timeout, returning `timeout: no recent network activity` |
| Which operations queue behind a blocked port? | the committed `sing-tun` fixture, extended | `Close` waits, `Dispatch` waits, `ResetNetwork` does not, and after `Close` starts `Dispatch` is refused rather than queued |
| Does the new return path allocate through an escape? | `go test -gcflags=-m` on the dependency | no allocation attributed to the fast path; only the mixed-path `slices.IndexFunc` shows up |

The first one is the load-bearing answer: it turns "a wait, not a deadlock" from an argument into a
measurement, and it gives the wait an upper bound that the code does not state anywhere.

## 2. Close path, as verified

```
Box.Close
 └─ Scope.Close                     cleanups run with slices.Backward: service → inbound → … → endpoint
     └─ tun.Inbound.Close
         └─ Go.Close                posts closeMessage, then <-engine.exitSignal   (stack_go.go:412)
             └─ engine goroutine    calls Flush                                        (stack_go_engine.go:244,300)
                 └─ Flush           holds s.access across the port write               (flow_dispatch.go:775,779)
                     └─ MASQUE port blocks in the QUIC DATAGRAM queue
             (dispatcher.Close at :415 is never reached while that blocks)
     └─ endpoint Close              closes the QUIC connection — the release, scheduled later
```

Three facts make this a **bounded wait** rather than a deadlock, and each is now tested:

1. **No cycle.** Nothing in the inbound close waits for the endpoint close to finish; the scope runs
   the cleanups sequentially. The endpoint is simply scheduled after the inbound.
2. **The block self-terminates.** With the connection left open and the peer silent, the send returns
   at the idle timeout with a typed error (5.80 s measured against a 6 s setting). Production sessions
   take their idle timeout from `option.QUICOptions`, so the real bound is that configured value;
   with no keepalive traffic returning, quic-go's own default is the fallback.
3. **The port call is the only long hold, and it is one batch.** `Flush` does not re-enter: after the
   blocked write returns, the staged buffers are cleared and the engine can process the close message.

**What waits, measured by the committed fixture (`sing-tun` `276576e`):**

| operation | while the port blocks | why |
|---|---|---|
| `Close` | waits ≥ 200 ms, completes on release | takes the stage lock that `Flush` holds |
| `Dispatch` (before close) | waits ≥ 150 ms | same lock, on the data path |
| `Dispatch` (after close started) | returns `false` immediately | `Close` raises `closed` **before** locking, so the entry check refuses |
| `ResetNetwork` | returns immediately | it only bumps the generation; the table is cleared at the next `Flush` |

The last two rows are the ones a naive reading would get wrong in both directions, which is why they
are asserted rather than described.

## 3. Verdict on project C

**No production change, and that is a decision with evidence, not a deferral.** The conditions the
brief sets for a fix are "a real wait cycle" or "an unbounded close wait". Neither holds: there is no
cycle, and the wait is bounded by the idle timeout with a measured termination. The cost of the
remaining wait is the idle-timeout value, which is a configured number, not an architectural one.

**Exit condition (recorded so a later round does not have to re-derive it):** if field data shows a
shutdown stall that users notice, or a stall that exceeds the configured idle timeout, the fix is to
bind the endpoint's QUIC client to the scope context so `Scope.Close`'s `s.cancel()` closes the
connection before the inbound cleanup runs. That collapses the bound from the idle timeout to a
context cancellation. It is a small change, but it changes endpoint shutdown semantics, so it needs
its own before/after measurement — not a speculative edit in an acceptance pass.

## 4. L4 CONNECT-UDP, as verified

The ownership matrix from the construction is re-read against the code and covered by tests:

| state | owner | test |
|---|---|---|
| owned accepted | transport, exactly one release | `...IsTakenAndTransfersOwnership`, `...DoesNotReleaseWhatItTransferred`, live e2e |
| owned refused | caller, buffer byte-identical | `...FailureKeepsCallerOwnership`, `...CapsuleCarriesTheOriginalBytes` |
| capability refused / too large | caller, capsule fallback | `...UnsupportedFallsBackToCapsule`, `...ErrorClassMatrix` |
| batch accepted | transport per packet | `...BatchSizes` (1…65) |
| batch fault | caller for the tail | `...LostTailIsReleased` |
| peer without datagrams | caller, capsules only | `...PeerWithoutDatagrams` |
| concurrent writers | per-packet accounting | `...ConcurrentWritersStayAccounted` |

Two properties worth stating explicitly because they are easy to misread:

- `DatagramTooLargeError` unwraps to `ErrDatagramUnsupported`, so **both** the single and batch paths
  carry a too-large datagram as a capsule. That is the measured behaviour, not an assumption.
- `FrontHeadroom()` is 9 only when the owned capability exists; a stream without it keeps 6, so no
  non-owned path pays the extra reservation.

**Batch-owned remains rejected, with numbers:** on a real queue, copying 10.6 µs/datagram versus
owned 9.5 µs; both are queue-bound, and a batch call would remove per-call queue overhead, not the
queue. The L4 single-packet change is the one with a measurable win (one allocation and one payload
copy per datagram).

## 5. Return path, as verified

The fast path is allocation-free at 1, 64, 256 and 1 024 packets (`AllocsPerRun`), and the A/B on the
real dispatcher is 237.9 ns / 72 B / 2 allocs → 156 ns / 0 B / 0 allocs at one packet. The escape
analysis shows no heap object attributable to the compaction; the only `slices.IndexFunc` inlining is
on the multi-writeback path, which is the uncommon one.

The contract that this pass checked hardest is the **input**: an all-write batch must not be modified,
because the previous implementation only wrote to the caller's slice when a packet was passed back.
That is now pinned by `...LeavesAnAllWriteBatchUntouched`, and the gap case by
`...CompactsAroundADroppedPacket`.

## 6. `equalFoldASCII`

Fixed (`'B'` → `'A'`), with 20 origin cases including the bracketed-IPv6 `A` the old fold missed.
Unreachable in production because `sameOrigin` normalises first; the fix is preventive and the tests
prove the gate is still strict.

## 7. Evidence summary

| item | level | note |
|---|---|---|
| close-path graph | `VERIFIED_SOURCE` | anchors in §2, re-read this pass |
| close wait is bounded | `MEASURED_LOCAL` | 5.80 s at a 6 s idle timeout, connection left open |
| queue block magnitude | `MEASURED_LOCAL` | worst single send 1.762 s over a 2 s window on a silent path |
| L4 ownership | `MEASURED_LOCAL` + `VERIFIED_SOURCE` | 19 unit tests, live HTTP/3 e2e, two mutation kills |
| return-path A/B | `MEASURED_LOCAL` | real dispatcher and real `portNAT` |
| WAN, devices, iOS/Android footprint | `NOT_TESTED` | no device, no WAN in this environment |

## 8. CI and the two flakes this pass had to attribute

All gates ran on `411833f4118a9b6e1c78ec3febd2de4739efd7d8`, the SHA carrying the construction, the
`sing-tun` pin (`276576e`) and this report's predecessors:

| workflow | run | conclusion |
|---|---|---|
| Verify | [39367944918](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39367944918) | **success** |
| Android Core ARM64 | [39367945344](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39367945344) | **success** |
| Mobile contract verification | [39367944908](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39367944908) | **success** after a re-run (first attempt failed in `route`) |

Neither failure is in this change set, and both were attributed rather than assumed:

1. **`route` flaked twice, on two different tests, across two SHAs.** First
   `TestTeardown_OneSidedCompletionNeverRunsOnClose` (a 10 s deadline under race), then
   `TestTheRouterHandsItsRuleSetResourcesToTheScope` (a rule-set lifecycle assertion) on the mobile
   job. `Verify` passed on both SHAs, the mobile job passed on the previous SHA, and `route` shares no
   code with this diff. Re-runs passed. No `route` file was modified.
2. **`sing-tun`'s gVisor integration suite is flaky on this Windows host, pre-existing.** A full run
   failed in `TestGoKernelFlowControl`; the same test was then run five times on the **pre-change**
   `flow_dispatch.go` (`a7bd7fa`) and failed **2 of 5** with the same subtests and the same durations
   (7.19 s / 9.79 s against 7.20 s / 9.79 s on the changed code). It is not caused by the return-path
   change. The two tests the round already excludes (`TestGoKernelDeadline`,
   `TestGoKernelPacketBatchIdle`) are the same class of window-timing assertions.

The tests this round added or touched are stable: the return path, the adversarial matrix and the
close edge pass `-count=3` and under `-race` on every run above.

## 9. Residual risks

1. **Shutdown stall bounded by the idle timeout.** Not a deadlock; the size is a configuration value.
   Exit condition in §3.
2. **The composed `Box.Close` wall time was not measured end to end.** Both halves were measured
   separately (lock hold, queue block) and the graph is verified, but there is no single experiment
   that starts a box with a real TUN inbound and a stalled MASQUE endpoint. That needs a real TUN,
   which this environment does not have.
3. **`route` has a load-sensitive test** (`TestTeardown_OneSidedCompletionNeverRunsOnClose`, a 10 s
   deadline) that flaked once on CI under race and passed on re-run and locally 3/3. It is unrelated
   to this change set and was not modified.

## 10. Verdict

The three construction items hold under an independent adversarial pass. Nothing was reverted,
nothing was found that requires a production edit in this pass, and the one open question — whether
the close wait is bounded — is now answered with a number and a mechanism rather than a plausibility
argument.
