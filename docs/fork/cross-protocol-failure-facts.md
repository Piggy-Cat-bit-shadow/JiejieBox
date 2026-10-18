# Cross-protocol failure facts

This is the cross-protocol integration added on `testing` after the Mieru
fallback work. It addresses the concrete gap that the group classifiers knew
only errno: the same `ECONNREFUSED` can be a dead proxy endpoint or a working
proxy whose destination refused, and the group treated both as first-hop path
failure.

## Contract

`adapter/failure.go` adds a narrow, immutable, optional fact:

```go
type FailureFacts struct {
    Stage  FailureStage
    Origin FailureOrigin
}
```

Stages: `Unknown`, `Dial`, `Handshake`, `Target`, `Data`.
Origins: `Unknown`, `Caller`, `Local`, `Path`, `Proxy`, `Target`.

An error carries the fact by implementing `FailureFacts() FailureFacts`.
`adapter.WrapFailure(err, facts)` preserves the original error chain
(`errors.Is/As` still work), and `adapter.FailureFactsOf(err)` uses `errors.As`
to find the nearest fact. A zero/Unknown fact is not attached: **Unknown means
no fact, not a verdict**.

There is no capability manager, no global enum table, no per-flow state on a
shared outbound and no second control plane.

## Producer: Mieru

Mieru already classifies its fallback handshake result internally; this round
exports only that proof to the group layer:

| Mieru result | Fact |
| --- | --- |
| valid SOCKS target reply (e.g. code 2) | Target / Target |
| handshake write/read timeout, reset, dial refusal | Handshake / Path |
| caller cancellation/configuration | Dial / Caller |
| bare EOF on a TCP-primary handshake | no fact (Unknown; could be auth or close) |
| bare EOF on a UDP primary (Linux unreachable peer) | Handshake / Path |

The fallback aggregate (`E.Errors`) is wrapped with the **secondary** result:
a target failure after a path failure remains target evidence; two path
failures remain path evidence. Disabled single-client dials use the same
wrapper, so the integration is not limited to fallback users.

`protocol/mieru/fallback.go:wrapFailureFacts` is the only producer in this
round. Other protocols are deliberately not given guessed facts: they retain
their existing errno policy until their error path can prove a stage.

## Consumers: LoadBalance failover

`protocol/group.RetryThisFlow` and `PenalizeMemberGlobally` now prefer facts
when present:

| Origin/stage | RetryThisFlow | PenalizeMemberGlobally |
| --- | --- | --- |
| Target | false | false |
| Caller | false | false |
| Local | false | false |
| Proxy handshake | true (one alternate may help) | false (not proof this member's network is down) |
| Path dial/handshake, non-timeout | true | true |\n| Path dial/handshake, timeout | true | false (a timeout is not proof the path is gone) |
| Path data | fall through (EOF -> false) | false |
| Unknown/no fact | existing errno rules | existing errno rules |

`PenalizeMemberGlobally` keeps the existing caller-cancellation and
own-teardown checks ahead of any fact: a producer that attached a Path fact to
`context.Canceled` or `net.ErrClosed` still cannot demote a member.

`URLTestGroup` also consumes the same facts on a failed probe: a Target or
Local fact leaves the member's last health measurement alone (the probe target
or this machine failed, not the node); a Path fact still invalidates it. This
is the health-layer counterpart of the group retry/penalty rule.

The facts are read **only on the failure path**. Success paths allocate no
facts and add no lock/timer/goroutine.

## Why this belongs in adapter, not group

The fact is an attempt-scoped statement any protocol can emit; the policy
(should this flow retry, should this member be demoted) remains in the group.
Putting the fact in group would force every protocol to import group, and
putting policy in adapter would create a universal classifier the existing
architecture explicitly rejects (EADDRNOTAVAIL means different things to
different consumers). The split preserves both boundaries.

## Verification

- `adapter/failure_test.go`: chain preservation, Unknown not attached,
  aggregate facts.
- `protocol/group/loadbalance_failure_facts_test.go`: the existing fact table
  gained target/path/proxy/caller/data rows; `TestTheFactsCover...` keeps the
  "not measured" invariant.
- `protocol/group/loadbalance_failover_test.go`: same ECONNREFUSED with a
  target fact does not retry/penalise; with a path fact it retries and
  penalises; a path-fact timeout still retries but does not penalise. This
  proves the facts are wired into the live policy without overriding the
  existing timeout conservatism.
- `protocol/mieru/fallback_test.go`: Mieru target/path facts; ambiguous EOF
  stays fact-less.
- Full group/adapter packages pass; full mieru package and race pass.

`FailureFactsOf` costs 75 ns on a plain error and 180 ns on a wrapped error,
with 16 B/op, on the error path only. It is never called for a successful dial
or per packet.

## Not done

- No facts on TLS/QUIC library errors yet; those protocols would need to prove
  the stage.
- No UI/API field. The fact is intentionally internal until a client contract
  is defined.
- No change to `IsCallerCancellation`/`IsOwnTeardown`; those remain separate
  and take precedence for their own cases.
