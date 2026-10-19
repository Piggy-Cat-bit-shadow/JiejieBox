# Out-of-scope notes

Items noticed during this audit that are **not** round-1/round-2 regressions, or that were measured
and deliberately left alone. They are recorded with evidence so a later round can decide without
re-discovering them. None of them was changed.

## 1. Client-side `PacketUnderlay.Close` has the same wake-only shape as the server one

The SDK's client packet underlay uses the same `SetReadDeadline(now)` + close-sessions ordering. The
server-side stall was reproduced and fixed (round-1 M9); the client side was **measured**: 25
iterations each of `TestMieruOutboundCloseTerminatesPacketSession`,
`TestMieruOutboundUDPCloseReleasesUnderlaySockets`, `TestMieruInterfaceUpdateReopensUDPUnderlay`
(75 runs) took 72.3 s total with no stall. No teardown ordering was changed on the outbound.

Condition to revisit: an outbound `Close`/`InterfaceUpdated` that ever shows ~60–120 s. Apply the same
socket-first ordering in `transportRuntime.stop()` and reuse the inbound regression test as a template.

## 2. TLS certificate failures are wrapped as `ErrHTTP3Unavailable` and arm the H3-broken memory

`http3ClientImpl.acquire` wraps every dial/handshake error with `ErrHTTP3Unavailable`, so a
certificate error on the H3 attempt falls back to H2 and arms the H3-broken memory for one backoff
step. **This is not a security bypass**: the H2 attempt uses the same TLS configuration and fails the
same way; it is not a cross-protocol fact either (the HTTP layer emits no `FailureFacts`). It is a
pre-existing behaviour that the round-1 window did not change. Classification of TLS errors into
"version unavailable" versus "authentication failed" would need a typed TLS-error boundary in
`transport/http`; out of this round's scope (and the directive forbids guessing facts).

## 3. `adapter.IsCallerCancellation` does not include `context.DeadlineExceeded`

It is `E.IsMulti(err, context.Canceled)`. The Mieru caller class covers deadlines through
`FailureOriginCaller`, and the group checks facts before errno rules, so the policy is correct. The
helper's narrower spelling is used by other layers with their own semantics; changing it is a
tree-wide decision, not part of this round.

## 4. `common/dialer` errno semantics differ by consumer (and that is correct)

`EADDRNOTAVAIL` is a family-health signal for the dual-stack scheduler, "shared local interface" for
the group penalty, and now `Dial/Local` for Mieru. The group's errno table still retries a raw
`EADDRNOTAVAIL` on another member, while Mieru's local fact does not ask for a retry. The two do not
contradict: Mieru is making a per-attempt statement with proof, the group is answering "would another
member help?" for an untyped error. No change.

## 5. Two windows (`http3EstablishTimeout`, `http3TunnelEstablishTimeout`) are intentional

They bound different lifecycles. A future operator-facing knob should probably configure both, but
merging them now would silently couple a stream dial's budget to a long-lived tunnel's setup. Not
changed.

## 6. Target-refusal timing is not reproducible on the loopback harness

The test inbound's router fabricates the destination, so no SOCKS error code is produced locally. The
classification is unit-tested (`Target/Target`, no transport switch); the 3 ms real-mita number is
prior evidence from `verify-evidence/mieru/2026-10-18-fallback/`, and is labelled as such in the
round-1 evidence.

## 7. Evidence staleness and the knowledge base

Following the repository rule, `KNOWLEDGE/` was **not** refreshed by this development work; it is a
baseline snapshot, not a description of HEAD. `verify-evidence/**` is point-in-time by its own rule;
the round-2 documents supersede round-1 statements where they differ (local/caller split and the
aggregate precedence), and the living contract `docs/fork/cross-protocol-failure-facts.md` was
updated.

## 8. Not run

`go test -race` (no mingw-w64 gcc; `clang` is MSVC-targeted and rejects `-mthreads`), iOS/Android
devices, TUN, real cellular/Wi-Fi transitions, real GFW blackholes. These are recorded as untested,
not as passes.
