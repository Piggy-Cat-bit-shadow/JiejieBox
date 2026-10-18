# Cross-protocol failure facts 閳?implementation evidence

Tree: `testing`, after the Mieru fallback work. The direct design is
[`docs/fork/cross-protocol-failure-facts.md`](../../../docs/fork/cross-protocol-failure-facts.md).

## The defect this closes

The existing group failover predicates had only errno. `ECONNREFUSED` could be:

- the proxy endpoint's listener refusing (first-hop/path failure 閳?another
  member should be tried and this member may be demoted), or
- a valid SOCKS reply from a healthy proxy whose target refused (target
  failure 閳?retrying another member or demoting this member is wrong).

The old code knew this ambiguity and therefore used a narrow errno table plus
the direct/block member kind. `route/failover.go` documented the missing stage
tag. Mieru already proved the difference internally in its fallback
classifier; this round exports that proof through a narrow optional error fact
instead of adding a universal classifier.

## Change

`adapter/failure.go`:

- `FailureStage` = Unknown / Dial / Handshake / Target / Data.
- `FailureOrigin` = Unknown / Caller / Local / Path / Proxy / Target.
- `FailureFacts` + optional `FailureFacts() FailureFacts` error interface.
- `WrapFailure` preserves the original chain; `FailureFactsOf` uses
  `errors.As`; Unknown is not a fact.
- No manager, map, goroutine, timer or per-flow state on a shared object.

`protocol/group/loadbalance_failover.go`:

- `RetryThisFlow`: Target/Caller/Local 閳?false; Path/Proxy at dial/handshake 閳?  true; Data/Unknown 閳?existing errno rules.
- `PenalizeMemberGlobally`: Target/Caller/Local/Proxy 閳?false; Path at
  dial/handshake 閳?true; Data/Unknown 閳?existing errno rules.

`protocol/mieru`:

- Target SOCKS reply 閳?Target/Target.
- Transport/dial/handshake failure 閳?Handshake/Path.
- Caller 閳?Dial/Caller.
- Ambiguous TCP EOF 閳?no fact.
- Wrapped both in the disabled single-client path and in the fallback aggregate.

## URLTest health\n\n`URLTestGroup` uses the same facts: Target/Local leaves the last valid health\nmeasurement alone; Path still invalidates it. This prevents a probe target\nfailure from removing a healthy node's evidence.\n\n## Cost

Facts are attached only when a dial fails. `FailureFactsOf` is only called on
the error path of the two group predicates; success paths never call it. No
new lock, timer, goroutine, queue or allocation on the data path.

Measured: plain error 75 ns/op / 16 B, wrapped 180 ns/op / 16 B, 0
allocations added to success. See `failure-facts-benchmark.txt`.

## Why only Mieru produces facts

A protocol must prove its own stage. Wrapping every protocol's raw errno in a
guessed fact would turn a previous honest ambiguity into a false verdict, which
the task explicitly forbids. The ledger in `CAPABILITY-LEDGER.md` records the
other protocols' actual owners; they keep their existing policy until their
error path can prove a stage.

## Evidence

- `CAPABILITY-LEDGER.md` 閳?protocol/owner audit and what was deliberately not
  changed.
- `TEST-MATRIX.md` 閳?positive/negative matrix and commands.
- `failure-facts-benchmark.txt` 閳?error-path benchmark.
- `adapter/failure_test.go` 閳?chain/Unknown/aggregate tests.
- `protocol/group/loadbalance_failure_facts_test.go` 閳?fact table extended
  with target/path/proxy/caller/data rows.
- `protocol/mieru/fallback_test.go` 閳?producer mapping and fact-less ambiguity.

## Residual

- No TLS/QUIC stage provider yet; no UI/API field.
- Target refusal behind protocols other than Mieru remains unclassified; their
  existing errno policy is unchanged rather than guessed.
- Real mobile/TUN/long soak not tested.
