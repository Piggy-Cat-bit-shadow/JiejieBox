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
| handshake write/read timeout, reset | Handshake / Path |
| socket refused / unreachable (typed errno) | Dial / Path |
| socket connect timeout or reset (typed; the SDK flattens the error to text) | no fact (a stall is not proof, and the flattened error no longer satisfies the group's timeout test) |
| local interface error (EADDRNOTAVAIL/ENETDOWN), DNS/address policy, permission, stopped client, closed/replaced outbound | Dial / Local |
| requester cancellation or deadline | Dial / Caller |
| bare EOF on a TCP-primary handshake | no fact (Unknown; could be auth or close) |
| bare EOF on a UDP primary (Linux unreachable peer) | Handshake / Path |

The last two "neutral" rows are separate on purpose. Caller means the requester withdrew; Local
means this process/machine or its configuration could not make the attempt. Both are neutral for
every consumer (no retry, no demotion), but only Caller is the requester's decision.

The socket rows need one extra step, because the official SDK destroys the error chain
before this package sees it:

```go
// pkg/protocol/mux.go: fmt.Errorf("NewTCPUnderlay() failed: %v", err)
```

`%v`, not `%w`. A refused connect therefore reaches Mieru as text, and the OS part of that text is
formatted in the system language on Windows, so `errors.Is(err, syscall.ECONNREFUSED)` is dead and a
substring match is not portable across locales. (The SDK's own prefix still identifies a transport
failure; what text cannot carry is *which kind* of socket failure it was, and therefore whether it is
proof.) `protocol/mieru/dial_observation.go` records the error at the
bridge (the fork's `serverDialer`), where it still has its type, and
`classifySocketError` reads it with the same `common/dialer` errno sets the rest of the tree uses
(`ErroredWith`, including the raw Winsock spellings). One observation per SDK dial; it carries no
state beyond that attempt and is never read on a success path. The socket class is also the first
Mieru evidence that is *definite*: two definite socket failures mean both endpoints are unusable, so
the fallback selector keeps its secondary preference instead of re-dialling the refused primary on
every request. Two silent handshakes remain the wrong-credential shape and still reset to primary.

The fallback aggregate (`E.Errors`) is wrapped with the **strongest proven evidence** of the two
attempts, not with whichever attempt reported last:

| Aggregate contents | Fact |
| --- | --- |
| a target reply anywhere | Target / Target (the proxy path worked through the other transport) |
| a definite socket failure anywhere | Dial / Path |
| a handshake-class failure on the secondary | Handshake / Path |
| a socket connect timeout on the secondary after a handshake failure on the primary | Handshake / Path (that pair published it before the socket classes existed) |
| only soft evidence (timeouts, ambiguous EOFs, caller/local) | no fact |

The rule matters because a fact-less class would otherwise erase real proof: a refused primary
(Dial/Path) followed by a secondary timeout used to publish nothing at all. Disabled single-client
dials use the same wrapper, so the integration is not limited to fallback users.

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
- `protocol/mieru/early_failure_test.go`: the socket errno table (WSA and
  POSIX), the localized-text case, the local-interface case, the both-definite
  and mixed both-failed matrix, the real-SDK refused endpoint, and the
  cancelled-caller short circuit. The cost of the bridge is
  `BenchmarkDialObservationOverhead` (45 ns, 48 B, 1 alloc per new session).
- Full `protocol/mieru`, `transport/http` (with_quic), `adapter` and
  `protocol/group` suites pass on Windows 2026-10-19. `-race` was
  BLOCKED_BY_ENVIRONMENT there (no mingw-w64 gcc); see
  `verify-evidence/mieru/2026-10-19-early-facts/PERFORMANCE-AND-REGRESSION.md`
  for the exact blocker and the resume command.

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
