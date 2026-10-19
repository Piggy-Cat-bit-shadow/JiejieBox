# Debug round 2 — bug ledger

Audited range: `c363e04bb3065de68e2123182013124cbb65a900` (pre-construction) → `a003732e8668e3ca1148290acc925df6f061833c`
(round-1 final). Debug fixes are committed on top of that on `testing`.

Every entry below was **reproduced before the fix** with a test that is now part of the suite.

---

## BUG-R2-001 — the fallback aggregate chose its fact from the secondary class only

**Severity / user impact.** Medium. With fallback enabled, when the primary was a *proven* socket
failure and the secondary failed softly (connect timeout, ambiguous EOF, caller/local), the error
carried **no `FailureFacts` at all**. A LoadBalance group then fell back to its conservative errno
rule on the aggregate: no retry on another member and no demotion — for a member whose primary
endpoint had just been proven refused. In the (definite primary, handshake secondary) pair the
reported stage was the secondary's `Handshake` instead of the proven `Dial`.

**Link to the round-1 change.** `protocol/mieru/outbound.go:dialWithFallback` wrapped the aggregate
with `secondaryClass` because, before round 1, every class that could reach the aggregate produced a
fact. Round 1 added `dialSocketTimeout`, whose whole point is to be fact-less, so the secondary class
stopped implying a fact; and `dialSocketFailure` made the primary's evidence stronger than the
secondary's.

**Trigger.** Fallback enabled; both attempts fail; classes
`(socket, socket-timeout)`, `(socket, ambiguous)`, `(socket, caller/local)`,
`(socket, transport)`, `(transport, socket-timeout)`.

**Reproduction.** `TestAggregateFailureFactsPrecedence` in `protocol/mieru/debug_audit_test.go`.
Before the fix four rows failed:
`timeout after socket`, `ambiguous after socket`, `caller after socket`, `local after socket`
(no fact instead of `Dial/Path`) and `handshake after socket` (`Handshake` instead of `Dial`).
The test drives the real `dialWithFallback` with the same fake SDK clients the round-1 tests use.

**Root cause / boundary violation.** The fact must describe the responsibility of the *final
behaviour*, not the *last attempt*. A fact-less class cannot be used as "no information" when the
other attempt has proof.

**Minimal fix.** `protocol/mieru/fallback.go:aggregateFailureClass` plus its use in
`outbound.go:dialWithFallback`. Precedence: target reply → definite socket → secondary handshake
class → (secondary socket timeout after a primary handshake failure, the pair that published a fact
before the socket classes) → no fact. The rule is documented where it is implemented.

**Failure → pass evidence.** The four rows above fail before and pass after; the rest of the matrix
(pass before and after) is kept so a future change cannot silently move them.

**Adjacent regression.** The group's consumer table already pins Path/Dial → retry + demote, and it
is unchanged; the fixed aggregate now reaches those rows. Full `protocol/group` suite passes.

**Performance / resources.** Error-path only; a switch over two enums, no allocation, no state.

**Status.** Closed.

---

## BUG-R2-002 — local failures were published as `FailureOriginCaller`

**Severity / user impact.** Low–medium, semantics/observability. DNS policy errors, address parsing,
permission failures, a stopped SDK client and local-interface socket errors (`EADDRNOTAVAIL`,
`ENETDOWN`) were exported as **Caller** facts. Policies stayed neutral (both Caller and Local are
neutral), so no retry/penalty was wrong; but "who ended this?" was wrong on every such error, and
`Caller` means the requester withdrew.

**Link to the round-1 change.** Round 1 introduced the fact export (`wrapFailureFacts`) and inherited
the broad `dialCallerFailure` bucket from the pre-existing classifier, which had used one class for
"local" and "caller" alike.

**Trigger.** A Mieru dial that fails on DNS, address parsing, permission, a stopped client, or a
local interface that cannot provide an address.

**Reproduction.** `TestClassifyDialError` rows `dns`, `client stopped`;
`TestClassifySocketErrorUsesTheSharedErrnoSets` row `WSA address not available` (all now expect the
local class); `TestLocalInterfaceFailureIsNotAPathFailure` (class and `FailureOriginLocal`).

**Root cause / boundary violation.** One internal class for two different origins. The directive's
Origin rule: local DNS/permission is not the requester's cancellation and not the member's path.

**Minimal fix.** New `dialLocalFailure` class in `protocol/mieru/fallback.go`:
`ctx.Err()/context.Canceled/DeadlineExceeded` stay Caller; DNS/address/permission,
`ErrClientIsNotRunning`, the local interface errnos and Mieru configuration messages become Local
(`Dial/Local` fact). The selector treats both as neutral (`report` releases a probe reservation and
changes nothing else).

**Failure → pass evidence.** The three tests above fail on the old class and pass on the new one;
`TestFailureFactsPerClass` gained the `local` row.

**Adjacent regression.** `protocol/group` gained a `Local` fact row; both policies stay neutral and
the "facts cover the cancellation family" invariant still holds. Full group suite passes.

**Status.** Closed.

---

## BUG-R2-003 — an owner-shutdown error could be classified as a transport failure

**Severity / user impact.** Low (narrow race), but it violates "Close wins" and "teardown is
neutral". If the outbound is being closed/rebuilt while a dial is inside `ListenPacket`, the bridge
returns `mieru: outbound was closed while creating the UDP underlay`; the SDK wraps it as
`NewUDPUnderlay() failed: ...`, so the round-1 text rules (which check that prefix first) reported a
**transport** failure: the selector could try the secondary *after Close* and the error would carry
`Handshake/Path` facts — blaming the member for our own teardown.

**Trigger.** `Outbound.Close`/`InterfaceUpdated` drains in-flight dials with a 5 s bound; a dial still
inside the SDK's 10 s handshake afterwards can hit the closed packet registry. Reproduced directly by
closing the packet dialer and calling the bridge, without needing the race.

**Reproduction.** `TestLifecycleClosureIsLocalNotTransport`: it closes
`serverDialer.packetDialer`, calls `ListenPacket` under an observation context, wraps the error the
way the SDK does, and asserts the class/fact.

**Root cause / boundary violation.** The underlay-prefix rule was checked before our own lifecycle
message, so a known local teardown matched the generic transport pattern.

**Minimal fix.** In `classifyDialError`, before the SDK underlay prefix rule, match **our own**
message (`mieru: outbound was closed` / `mieru: outbound is closed`) and return `dialLocalFailure`
(`Dial/Local`). The text is stable because this package writes it; the comment says so.

**Failure → pass evidence.** Before: class `transport`, the test fails on the class assertion; after:
`local`, fact `Dial/Local`.

**Status.** Closed.

---

## BUG-R2-004 — a proxy-endpoint DNS failure was reported as a handshake/path failure

**Severity / user impact.** Medium. The SDK flattens resolver errors to text, so a server FQDN that
does not resolve was classified `transport` (through the `NewTCPUnderlay() failed` /
`NewUDPUnderlay() failed` prefix) and published **Handshake/Path**. A group could therefore retry the
flow and demote the member for a name that never resolved — the exact "local DNS is not the member's
path" violation the directive names. The round-1 typed `*net.DNSError` check in `classifyDialError`
never fired because the chain is gone by then.

**Link to the round-1 change.** The typed socket ledger was added by round 1 but only consulted for
errno-shaped failures; a resolver error is typed, yet not an errno, so it fell through. The UDP path
resolves inside the SDK *before* `serverDialer.ListenPacket`, so the bridge did not even record it.

**Trigger.** A domain `server` whose resolution fails (NXDOMAIN, local DNS policy), on either
transport.

**Reproduction.** `TestEndpointResolutionFailureIsLocalButTriesTheOtherEndpoint`: the resolver bridge
is driven with a failing `DNSRouter`, the error is wrapped the way the SDK does it
(`NewUDPUnderlay() failed: ResolveUDPAddr() failed: %v`), and before the fix the class was
`transport`; the new assertions require `endpoint` and a `Dial/Local` fact. The second half of the
test proves the secondary is **still attempted** when only the primary name fails.

**Root cause / boundary violation.** Two separate things were conflated: "this endpoint's address
cannot be prepared" (local to the endpoint, still worth trying a differently-addressed backup) and
"the member's path failed". It also relied on text that only exists after the SDK destroyed the type.

**Minimal fix.**

- `protocol/mieru/dial_observation.go` now records the last setup result, including the resolver
  (field renamed `setupErr`, accessor `setupFailure`);
- `resolverBridge.LookupIP` records lookup errors into the ledger (the SDK resolves the UDP endpoint
  before the packet dialer, so no other producer can see it);
- `classifySocketError` recognises typed `*net.DNSError` / `*net.AddrError`;
- new `dialEndpointFailure` class: transport-class for retry (try the other endpoint once) but
  `Dial/Local` facts (no flow retry, no demotion). Permission/bind/stopped-client stay
  `dialLocalFailure` (no retry at all).
- an endpoint failure participates in the both-failed rule as a stable setup fact
  (`isDefiniteSetupClass`): two unresolvable endpoints keep one dial per request, while
  endpoint+silent resets to re-test the cheaply-failing primary.

**Failure → pass evidence.** The new test fails before the class/fact assertions and passes after;
the classifier table and the aggregate matrix gained endpoint rows.

**Adjacent regression.** `TestClassifyDialError`'s `dns` row and `TestFailureFactsPerClass` were
updated for the endpoint class; group consumers treat `Dial/Local` as neutral (existing row).
Full package suites re-run.

**Status.** Closed.

---

## Verified but not a new bug (round-1 work re-audited)

- **Inbound 65 s shutdown** (fixed before this debug round, `protocol/mieru/inbound.go`): reproduced
  by mutation in round 1 and by `TestMieruInboundCloseReleasesTheUDPUnderlayPromptly`; the test and
  the stress run (rebind + close, 20× each in 0.284 s) are re-run in this round's matrix.
- **Mieru 10 s SDK response floor / late caller answer**: re-measured in round 1 (10.002 s with a
  500 ms budget). Round 2 only changed the *reported reason*; the wait is a documented limit, not a
  fixable bug inside the public SDK API.
- **Client-side `PacketUnderlay.Close` analogue**: 75 outbound packet close/rebuild iterations in
  72.3 s showed no stall; no ordering change was made. Recorded as a measured limit in the round-1
  ledger and re-confirmed here.
