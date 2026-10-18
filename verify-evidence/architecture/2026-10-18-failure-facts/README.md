# Cross-protocol failure facts 鈥?implementation evidence

Tree: `testing`, after the Mieru fallback work. The direct design is
[`docs/fork/cross-protocol-failure-facts.md`](../../../docs/fork/cross-protocol-failure-facts.md).

## The defect this closes

The existing group failover predicates had only errno. `ECONNREFUSED` could be:

- the proxy endpoint's listener refusing (first-hop/path failure 鈫?another
  member should be tried and this member may be demoted), or
- a valid SOCKS reply from a healthy proxy whose target refused (target
  failure 鈫?retrying another member or demoting this member is wrong).

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

- `RetryThisFlow`: Target/Caller/Local 鈫?false; Path/Proxy at dial/handshake 鈫?  true; Data/Unknown 鈫?existing errno rules.
- `PenalizeMemberGlobally`: Target/Caller/Local/Proxy 鈫?false; Path at
  dial/handshake 鈫?true; Data/Unknown 鈫?existing errno rules.

`protocol/mieru`:

- Target SOCKS reply 鈫?Target/Target.
- Transport/dial/handshake failure 鈫?Handshake/Path.
- Caller 鈫?Dial/Caller.
- Ambiguous TCP EOF 鈫?no fact.
- Wrapped both in the disabled single-client path and in the fallback aggregate.

## Cost

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

- `CAPABILITY-LEDGER.md` 鈥?protocol/owner audit and what was deliberately not
  changed.
- `TEST-MATRIX.md` 鈥?positive/negative matrix and commands.
- `failure-facts-benchmark.txt` 鈥?error-path benchmark.
- `adapter/failure_test.go` 鈥?chain/Unknown/aggregate tests.
- `protocol/group/loadbalance_failure_facts_test.go` 鈥?fact table extended
  with target/path/proxy/caller/data rows.
- `protocol/mieru/fallback_test.go` 鈥?producer mapping and fact-less ambiguity.

## Residual

- No TLS/QUIC stage provider yet; no UI/API field.
- Target refusal behind protocols other than Mieru remains unclassified; their
  existing errno policy is unchanged rather than guessed.
- Real mobile/TUN/long soak not tested.

