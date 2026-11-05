# ResourceGuard adversarial debug

Round 06. Scope: the admission system added in round 05 and the paths it touches — the TLS
handshake seam, the AnyTLS seam, the class propagation, and their interaction with chained
dials. Baseline `testing` at `72e566305e3`; this round's fix is on top of it.

The attack order was "what did the last round's change add, and how does it break under load",
because a new seam is the most likely place for a new defect.

## 1. RT-1 — a chained dial deadlocked against its own permit (P0, fixed)

**Trigger.** Any configuration where one TLS outbound dials through another: a detour, a chain,
a selector fallback, or a proxy whose transport dials through a second outbound. The outer
outbound's `DialTLSContext` takes a handshake permit, then dials through the detour, whose own TLS
handshake re-enters the same seam — and asks for a second permit.

**Before the fix.** With `Limit: 1` (a test stands in for a saturated gate), the outer dial holds
the only slot and the inner dial is refused immediately:

```
handshake_admission_test.go:138: a chained dial was refused by its own outer permit:
    resource guard: admission queue is full
--- FAIL: TestAChainedDialTakesOnePermit
```

With a queue large enough to park (the shipped `Queue: 256`, `Wait: 5s`), the shape is worse than
a refusal: the outer permit cannot be released until the inner handshake finishes, and the inner
cannot start until a permit is free. Every chained dial in that window stalls for the whole wait
budget and then fails. Nothing is wrong on the network. This is a self-inflicted stall introduced
by the round-05 seam, and it is the highest-severity defect found this round.

**Root cause.** The reentrancy marker existed but was only applied to the *AnyTLS* case: the TLS
seam checked `HasHeldPermit(ctx)` at entry but never put the marker into the context the dial runs
with, so a dial that re-entered the seam saw the original, unmarked context.

**Fix** (`common/tls/client.go`): the dial closure receives the dial context, and the admitted
path runs it with `resourceguard.WithHeldPermit(ctx)`. One logical dial may hold at most one
handshake permit, whatever it dials through.

**After the fix.** `TestAChainedDialTakesOnePermit` passes, and the gate reports exactly **one**
`Requested` for the chain of two handshakes (`stats.Gate.Requested == 1`).

**Residual, deliberate.** The inner handshake of a chain is not charged separately, so a chain of
N TLS hops consumes one slot for the whole chain. The alternative — charging each hop — is what
produced the stall. The outer slot still bounds how many chains start at once, and the hop cost
inside a chain is bounded by that.

## 2. What else was attacked, and what it showed

| Attack | Method | Result |
|---|---|---|
| Marked dial must not take a second permit | `TestHandshakeWithAdmissionSkipsAMarkedDial` | PASS — `Requested == 0` |
| A failed handshake must not leak a permit | `TestHandshakeWithAdmissionAdmitsAnUnmarkedDial` | PASS — `Inflight == 0` |
| A saturated gate must refuse, not park, and must not run the handshake | `TestHandshakeWithAdmissionRefusesWhenTheGateIsFull` | PASS |
| No guard (every non-NE platform) must never refuse | `TestHandshakeWithAdmissionWithoutAGuardIsUnbounded` | PASS, 64 iterations |
| Interactive flows reach the reserve; bulk do not | `TestAnInteractiveFlowMayUseTheReserve`, `TestABackgroundFlowDoesNotUseTheReserve` | PASS |
| Unclassified is distinguishable from classified-default | `TestNoClassIsDistinguishableFromDefault` | PASS |
| A waiter cancelled while queued must not run the handshake, and must leave the queue | `TestHandshakeWithAdmissionCancelWhileQueuedDoesNotDial` | PASS — `Waiters == 0`, `Canceled > 0`, `Inflight == 0` |
| The chained fix must hold under contention, not just two dials | `TestChainedDialsUnderPressureStayBounded` | PASS — 16 chains, `Requested == 16`, `MaxInflight == 1`, `Inflight == 0` |

Not attacked, and therefore not claimed: real QUIC/Cronet/SDK reconnect storms (no local server
fixtures), NAT entry churn under a 2000-flow storm, `phys_footprint` behaviour, device jetsam,
reclaim self-excitation (the shed path is unchanged this round and was covered in round 02), and
cross-protocol shared-budget fairness under a live pressure source.

## 3. Test runs

| Command | Result |
|---|---|
| `go test ./common/tls/ -run 'TestHandshakeWithAdmission\|TestAChainedDial'` | 5 PASS |
| `go test ./protocol/anytls/ ./common/resourceguard/ ./common/trafficclass/` | PASS |
| full suite, `-tags <windows profile>` | same three environment failures as the baseline (`common/tls` TLS 1.3 on this host, `common/tlsspoof`, `common/windivert`); no new failure |
| `Verify` on `testing` | run `41773661716`, green |

## 4. Classification

**FIXED**: RT-1 (chained dial stalls on its own permit).
**VERIFIED**: the seam's accounting, refusal and no-guard paths; the interactive reserve; the class
round-trip.
**REMAINS**: `ClassInteractive` still has only one producer class of traffic (whatever the config
classifies); the reuse-before-admission issue from round 05 is unchanged (it needs a
`HasIdleSession` predicate in `sing-anytls`, which is an unforked upstream module).
**NEEDS_DEVICE**: everything about the iOS NE footprint, jetsam and real network switching.

---

## Follow-up: the shared TLS seam ignored the traffic class

The audit of this round found that `handshakeWithAdmission` acquired `ClassNormal` regardless of the
flow's class, so the interactive reserve was unreachable for every TLS outbound except AnyTLS. The
mapping is now the same at both seams and is tested at both: interactive and realtime may use the
reserve; bulk and default may not; and a table test asserts the two seams agree for the same class,
because they are two consumers of one policy and a divergence is exactly the defect this file exists
to record.

---

## Round V2: the stale-publication race (P1-A), and what was excluded

### FIXED — a stale observation could overwrite a newer level

`Guard.Level`, `Acquire` and `Stats` each call `observe()` and then `apply()`, and `apply` used to
decide with `applied != level`. Two steps, one comparison: a caller descheduled between them
published a plan that was already out of date.

* A observes ARMED and stops; pressure rises; B publishes CRITICAL; A resumes and publishes ARMED -
  the gate re-opens to the normal limit while the guard reports critical pressure.
* The mirror image is the dangerous one: a stale CRITICAL published after a demote re-throttles a
  healthy gate **and runs the shed hooks a second time** for one critical episode, which is the
  reclaim loop this system exists to prevent.

`observe()` now returns the transition sequence number; `apply(level, version)` publishes only if
that version is both newer than what was published and still the current one, re-checked under the
same lock that writes the plan - so a reader cannot see a new version with the old plan.

Written before the fix, verified by restoring the old comparison:

```
--- FAIL: TestAStaleCriticalObservationCannotShedAgain
    expected: 1  actual: 2
```

Four tests now pin it: the stale ARMED cannot re-open, the stale CRITICAL cannot shed again, a
sixteen-goroutine publication storm leaves the gate and the reported level in agreement, and
publication is idempotent and ordered.

### EXCLUDED — two reclaim executors (P1-B)

The guard's only production shed hook is `client.CloseIdleConnections` (`protocol/anytls/outbound.go`),
a cheap close of idle sessions that the client already serialises internally. The destructive
reclaim pass remains the OOMKiller's, and its two passes cannot overlap - `reclaim_overlap_test.go`
forces that interleaving and asserts the ordering. With the version gate above, one critical episode
can no longer produce a second shed from a stale observation either.

### Not done here

AnyTLS's permit-before-reuse question needs a `HasIdleSession()` predicate in `sing-anytls`, an
unforked upstream module; taking over a dependency is a maintenance decision, not a fix to land in a
closeout. The TUN UDP NAT and multi-node QUIC stress scenarios need either an injection point in
`sing-tun` or a runner with a real `/dev/net/tun`, and neither exists in this tree today. Both are
recorded rather than half-built.
