# DS-03 — the inner recovery producer: cancellation, exit and ownership

**Date:** 2026-10-20 (Asia/Shanghai)
**Repository / branch:** `Piggy-Cat-bit-shadow/sing-box`, `testing` only.
**Scope:** the goroutine `recoverCandidates` starts for
`adapter.DNSDualStackRouter.LookupFamilies`. Nothing else was reviewed or changed.

| identity | value |
|---|---|
| `START_SHA` | `b0dddd4a277640edf2239074f642e13f12c6413d` |
| `DS03_CODE_SHA` | `54e600a0d67b12f5e3d98a4469f7f18c1907bab8` (tests only; no production file changed) |
| `DOCUMENT_SHA` | the commit that adds this report |

Historical records stay as they are: the architecture audit
(`DUAL_STACK_CROSS_COMPONENT_ARCHITECTURE_AUDIT_2026-10-20.md`) and the closure report
(`DUAL_STACK_CROSS_COMPONENT_CLOSURE_2026-10-20.md`). Neither is rewritten; this file is the
specialist addendum DS-03 was left open for.

---

## 1. Verdict

**`NO_PRODUCTION_CHANGE`.**

There is no join on the inner `LookupFamilies` goroutine, and that is the correct shape for the
contract involved. For a resolver that honours its context:

- the caller is **not** blocked on the lookup (measured: a resolver parked inside `LookupFamilies`
  does not delay the literal dial's return);
- the cancellation performed on return **does** release the lookup's send and the goroutine exits
  (measured: `activeLookups` returns to zero with the gate still closed);
- no channel stays blocked and no recovered socket exists, because recovery only produces addresses —
  the dial that consumes them has its own ownership.

For a resolver that ignores its context, the code cannot force an exit. That is stated as a boundary
rather than patched with a wait: an unconditional `Wait()` there would hand a successful connection's
return to a resolver that has not answered.

---

## 2. The two ownership chains

### Chain 1 — literal recovery

```
dialLiteralWithRecovery (resolve.go)
  ├─ recoveryCtx, cancelRecovery := WithCancel(ctx)      ; defer cancelRecovery()
  ├─ go func(){ recovered <- d.recoverCandidates(recoveryCtx, …) }()
  │      └─ recoverCandidates (dual_stack_recovery.go)
  │            ├─ streamCtx, cancelStream := WithCancel(recoveryCtx) ; defer cancelStream()
  │            └─ go func(){ LookupFamilies(streamCtx, …); collected <- nil }()   ← INNER, unjoined
  └─ select { original | recovered | ctx.Done() }
```

| element | owner | at return |
|---|---|---|
| `ctx` | caller (`route.ConnectionManager` → `setupCtx`) | outlives the call |
| `recoveryCtx` | `dialLiteralWithRecovery`, cancelled by its `defer` | cancelled |
| `streamCtx` | `recoverCandidates`, cancelled by its `defer` | cancelled when the outer function returns |
| outer recovery worker | `dialLiteralWithRecovery` — **not** waited on; its channel is buffered (1) and it sends exactly once | may still run; its send cannot block |
| `LookupFamilies` goroutine (**inner**) | `recoverCandidates`, **no join** | may still be inside the resolver |
| `collected` | `recoverCandidates` (reader); never closed | garbage-collected |
| `recovered` | `dialLiteralWithRecovery` (reader); buffered, single send | garbage-collected |

**Guaranteed at return:** the caller does not wait for the lookup to finish; the recovery context and
`streamCtx` are cancelled, so a cooperative resolver's *next* publish or terminator takes the
`<-streamCtx.Done()` arm and the goroutine exits. **Not guaranteed:** that the resolver returned.

Measured: `TestLiteralRecoveryDoesNotWaitForTheLookupToFinish`. The resolver is parked at a gate that
the test never opens; the dial still returns (100 ms connect delay), and `activeLookups` falls to zero
afterwards — the exit came from cancellation, not from an answer. If the function waited for the
lookup to finish, this test would hang until its own cleanup.

### Chain 2 — parallel network recovery

```
resolveParallelNetworkDialer.DialParallelNetwork (dual_stack_parallel_network.go)
  └─ feedRecoveryAsLateCandidates
        ├─ feedCtx, cancelFeed := WithCancel(ctx)
        ├─ producers.Add(1); go func(){ defer producers.Done(); defer close(done); defer close(lateCandidates)
        │      recovered := d.recoverCandidates(feedCtx, …)          ← OUTER, joined by stop()
        │      for … { select { lateCandidates <- … ; <-feedCtx.Done() } }
        │  }()
        └─ return recoveryFeed{candidates, done, stop = cancelFeed + producers.Wait()}
```

| element | owner | at return |
|---|---|---|
| `feedCtx` | the feed, cancelled by `stop()` | cancelled |
| outer producer goroutine | the feed; **joined** by `stop()` (`producers.Wait()`, `done` closed as its last act) | exited when `stop()` returns |
| `recoverCandidates`' inner goroutine | `recoverCandidates`, **no join** | may still be inside the resolver after `stop()` returns |
| `lateCandidates` | closed by the producer; read by the scheduler | closed |
| `done` | producer, `defer close` | closed |

**Guaranteed when `stop()` returns:** the outer producer exited. **Not guaranteed:** the inner lookup
exited — `stop()` does not reach it. Measured: `TestResolverThatIgnoresCancellationStatesTheBoundary`
keeps the resolver inside, and `stop()` returns with `done` closed while the lookup count is still one
higher than before the feed started.

---

## 3. The three resolver shapes, as measured

| resolver | test | result |
|---|---|---|
| **cooperative** (returns when `ctx` is cancelled) | `TestLiteralRecoveryDoesNotWaitForTheLookupToFinish`, `TestParallelNetworkRecoveryInnerLookupExitsWithTheFeed` | caller returns without waiting; after the cancellation the lookup exits (`activeLookups → 0`, `exited` closed); no send stays parked |
| **slow but cooperative** (refused family answers first, usable family later) | `TestSlowCooperatingResolverStillFallsBack` | recovery returns the **usable** family (the refused fast one does not end the lookup), elapsed ≈ the slow family's 50 ms, exit observed afterwards |
| **ignores cancellation** (test doubles, released by the test) | `TestResolverThatIgnoresCancellationStatesTheBoundary` | literal dial returns; `stop()` returns and `done` closes; the inner lookup is still inside (`activeLookups == before+1`) and exits only when the test releases it |

All three run under `-race -count=5` and `-count=3` without the detector reporting anything.

The test doubles are test-only. No production resolver is accused of ignoring its context; the third
row exists to state the boundary, exactly as the brief required.

---

## 4. Four distinctions this round was asked to keep separate

| claim | fact measured here |
|---|---|
| "not explicitly joined" ≠ "proven to leak" | the unjoined inner goroutine exits on cancellation for both chains; no blocked send, no channel left parked, no socket involved |
| "context cancelled" ≠ "producer exited" | cancellation is what *causes* the exit, and the exit is a separate event (`activeLookups`, `exited`), observed after it happens |
| "outer producer exited" ≠ "inner goroutine exited" | `stop()` closes `done` while the inner lookup is still inside (measured by count, not by goroutine totals) |
| "waiting for the producer" ≠ "safe fast shutdown" | `stop()` waits only for the outer producer, and it returns because `recoverCandidates` does not wait for the resolver — an inner `Wait()` would have made an uncooperative resolver hold the caller |

---

## 5. Why no code changed

1. **The inner goroutine is not a leak for a conforming resolver.** It exits when the resolver returns,
   and cancellation is delivered through `streamCtx` before `recoverCandidates` returns, so the exit is
   caused, not hoped for. The tests observe it.
2. **A join there is actively wrong.** `recoverCandidates` is called on the literal path *synchronously
   from the caller's goroutine*; waiting for `LookupFamilies` would block the dial's return on the
   resolver, and on a resolver that never returns, forever. The brief forbids an unconditional
   `Wait()`; here it would also be a regression.
3. **No second scheduler / watchdog was added.** The exit relies on the same cancellation that already
   exists; adding a poller would be a new mechanism to prove.
4. **Nothing else in the file changed.** `mergeRecovered`, the first-admissible-family rule, the strict
   family gate, recursion guard and the direct/packet difference are untouched. The DS-01 fix and the
   ssh fix from the previous rounds are untouched.

If a future requirement is "recovery must terminate even when a third-party resolver ignores its
context", that requires an API decision (a resolver that can be abandoned), not a `Wait()`.

---

## 6. Test evidence

| command | result |
|---|---|
| `go test ./common/dialer -run 'TestLiteralRecoveryDoesNotWaitForTheLookupToFinish\|TestParallelNetworkRecoveryInnerLookupExitsWithTheFeed\|TestSlowCooperatingResolverStillFallsBack\|TestResolverThatIgnoresCancellationStatesTheBoundary' -count=3` | PASS |
| `go test -race ./common/dialer -run '…same four…' -count=5` | PASS |
| `go test -race ./common/dialer -count=1` | PASS (18.1 s) |
| `go test ./common/dialer -count=1` | PASS |
| `go vet ./common/dialer`, `gofmt -l` | clean |

`Direct`/`route` were not needed: no production file changed, and the tests drive the real
`resolveDialer`/`resolveParallelNetworkDialer` entry points.

---

## 7. CI

| run | result |
|---|---|
| `423c81d10` Verify | failed at **gofmt** on the new test file (a missing blank line and trailing newline introduced by the file-writing step); fixed in `2075d3c64`; the `race on the fork's own data paths` job passed `common/dialer` (18.0 s) in that run |
| `2075d3c64` Verify | failed in `route` at `TestTeardown_OneSidedCompletionNeverRunsOnClose` ("the peer never received the request", 10 s) — a known load-sensitive flake in this repository, unrelated to a tests-only change in `common/dialer`; the same job passed `common/dialer` (18.0 s) |
| `3745400dd` Verify | **success**, including the `race on the fork's own data paths` job with the new tests |
| `ffca71b24` Verify (docs only) | failed in `route` at `TestInterfacePendingResetMarksNetworkUnstableBeforeResetLock` (`0.00 s`, "the network must be settled once the pending reset has run"); `common/dialer` passed (18 s). A docs-only commit cannot reach this code, and the test fails in a different place than the previous run |

The DS-03 tests themselves ran in the race job of every run and did not fail. Across four runs the only failures were one gofmt slip in the new test file (fixed) and two `route` flake hits in two different tests, both on commits that could not reach `route`'s transition code; the `route` race suite is therefore recorded as flaky in this CI, separately from this round. The `route` flake is
recorded rather than "fixed": it is a pre-existing timing assertion on the teardown path, outside
this round's scope, and changing it here would be an unrelated edit. Its non-recurrence on the final
run is the evidence that it was not introduced here.

---
## 8. Residual limits

- A resolver that violates the context contract cannot be forced to exit, and this code does not
  pretend otherwise. Its goroutine lives until the resolver returns; the caller is not blocked by it.
- The tests use the in-package doubles (`scriptedFamilyRouter`, `ignoringCancelRouter`), not a real
  `dns.Router` against a live resolver. They pin the ownership and cancellation rules; they do not
  measure DNS latency in the field.
- No device/WAN verification; recovery is exercised through the same entry points production uses, but
  with a scripted resolver.
