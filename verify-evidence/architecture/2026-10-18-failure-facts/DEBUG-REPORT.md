# Adversarial debug report 闁?failure facts

Scope: the cross-protocol failure-fact change (`adapter/failure.go`,
`protocol/group/loadbalance_failover.go`, `protocol/mieru`). This report is
about the integration, not a re-audit of every protocol.

## Change map

```text
Mieru fallback / single-client dial
  -> wrapFailureFacts(target|path|caller)
  -> error chain (errors.Is/As preserved)
  -> LoadBalance DialWithFailover
       -> RetryThisFlow (flow decision)
       -> PenalizeMemberGlobally (member decision)
  -> route log / caller
```

No resource owner changed. Facts live on the per-attempt error, never on a
shared outbound; no fact is recorded on success.

## D-01 闁?P1: a fact must not override the timeout conservatism

### Reproduction

An error can carry a truthful `Path/Handshake` fact and still be a timeout.
The first implementation treated `Path + Dial/Handshake` as a permanent path
failure and returned `true` from `PenalizeMemberGlobally`, even though the
existing errno policy deliberately never penalizes a timeout:

> a connect that TIMED OUT is not evidence that this member's network is gone

The first fact-table row `path dial fact on deadline` expected `global: true`.
That was wrong: the same code path also covers a blackholed or wrong-credential
Mieru handshake (10 s timeout), which would demote a proxy for an authentication
or transient timeout.

### Fix

`PenalizeMemberGlobally` now checks `isTimeoutFailure` before accepting a Path
fact:

- `context.DeadlineExceeded`, `os.ErrDeadlineExceeded`;
- `dialer.TimeoutErrnos` (including Winsock 10060);
- any `net.Error.Timeout()`.

A Path fact at dial/handshake penalizes only a non-timeout path failure. Retry
still returns true for the timeout (another member may help), exactly as before.

### Proof

- `protocol/group/loadbalance_failover_test.go:TestLoadBalanceFailoverPathFactTimeoutDoesNotPenalise`:
  retries the alternate, penalty 0.
- `loadbalance_failure_facts_test.go`: `path dial fact on deadline` has
  `retry: true, global: false`; `path dial fact on ECONNREFUSED` has
  `global: true`.
- The end-to-end target/path fact tests prove the non-timeout branches.

## D-02 閳?P2: cancellation/teardown must stay ahead of any fact

### Reproduction

`PenalizeMemberGlobally` checked the fact before the existing caller/own-teardown
invariant. A protocol that attached a `Path` fact to `context.Canceled` or
`net.ErrClosed` could make the group demote a member for an operation the
caller or this process ended. The fact would be structurally wrong, but the
consumer must not rely on every producer being correct.

### Fix

The function now returns `false` for `adapter.IsCallerCancellation` and
`adapter.IsOwnTeardown` before reading facts. The fact table gained two rows:
`path fact on cancellation` and `path fact on own teardown`. This preserves the
old cancellation attribution and the "not measured" invariant.

## D-03 鈥?P2: URLTest must not delete health on a target refusal

### Reproduction

`URLTestGroup` removed a member's health history for any probe error that was
not `ErrResourceSuspended`/`net.ErrClosed`/`context.Canceled`. A target fact on
the probe (the probe URL refused, or a local configuration error) therefore
deleted a valid node measurement, letting a third-party target move the group
to a worse node.

### Fix

After the existing cancellation/teardown checks, URLTest now leaves the health
history untouched for `FailureOriginTarget` and `FailureOriginLocal` facts. A
`Path` fact still deletes it; the positive control
`TestATargetFactIsNotAPathFact` pins that. The existing `TestARealDial...`
cases remain green.

## D-04 — P2: secondary failure log named the wrong transport

### Reproduction

`dialWithFallback` logged a target/ambiguous failure with `primary.label`
regardless of which transport returned it. When the selector legitimately
preferred the secondary during a primary cooldown, a secondary target reply was
reported as a primary failure. The log is the operator's only attribution for
this path, so the label must be the selected transport.

### Fix

The two debug lines now use `selected.label`. The test forces a primary
transport failure (secondary preferred), makes only the secondary dial, and
asserts the message names the secondary and not the primary.

## Adversarial findings reviewed, no code change

1. **Target fact suppresses retry and penalty.** This matches the existing
   in-band target policy (EOF/reset are terminal for the flow) and prevents a
   destination refusal from demoting a member. The end-to-end test pins it.
2. **Unknown/Data stage falls through to errno.** A zero fact is not attached;
   `FailureFactsOf` returns false, so old behavior remains. Pinned by the
   fact-table rows and `TestFailureFactsDoNotInventUnknown`.
3. **Aggregate errors preserve facts.** `errors.Join`/`E.Errors` still unwrap
   to the original cause; the outer fact is the attempt's answer. Pinned by
   `TestFailureFactsOnAggregate`.
4. **Caller cancellation precedes facts.** `RetryThisFlow` checks
   `IsCallerCancellation`/`IsOwnTeardown` first; a caller fact cannot make a
   cancelled flow retry.
5. **Facts are not stored on a leaf.** No shared state to poison another flow;
   the group reads only the returned error.
6. **No success-path cost.** Facts are attached only on failure; group
   predicates are called only on failure. No lock/timer/goroutine/queue was
   added.

## Resource and performance

- Error path: `FailureFactsOf` 75 ns plain / 180 ns wrapped, 16 B/op.
- Success path: zero calls, zero added allocation from this feature.
- No new background work; `go test -race` for adapter/group/mieru passes.

## Compatibility

- New optional interface/error wrapper only; no config, wire, handshake,
  crypto, MUX or API change.
- Protocols without facts keep their existing errno policy.
- Mieru's error strings and `errors.Is` results are unchanged; facts are an
  additional `errors.As` surface.
- No per-flow data is written into a shared leaf.

## Git

See the top-level final report; this file is committed with the evidence tree.
No reset/rebase/force push was used.

## Residual

- Only Mieru proves facts today; other protocols remain on errno by design.
- No UI/API field; no TLS/QUIC stage provider.
- Real mobile/TUN/long soak not tested; environment boundary.
