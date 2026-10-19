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
