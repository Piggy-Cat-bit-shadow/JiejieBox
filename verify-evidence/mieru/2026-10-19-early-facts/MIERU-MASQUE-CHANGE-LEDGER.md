# Mieru / MASQUE change ledger

Baseline `c363e04bb3065de68e2123182013124cbb65a900`, branch `testing`. Every row names the files, the
consumer, the test that fails before it and passes after, and the invariant it keeps.

## Mieru

### M1 — the bridge keeps the typed socket error

- **Files:** `protocol/mieru/dial_observation.go` (new), `protocol/mieru/dialer.go`.
- **Old entry was late/wrong:** `serverDialer` returned `(conn, err)` and forgot it. The SDK then
  rewrote the error with `%v`, so only text survived; a refused port could only be recognised by an
  English substring, and was not provable at all on a localised Windows.
- **New evidence:** `serverDialer.DialContext` / `ListenPacket` record a success or the *last* error in
  a `dialObservation` carried by the SDK dial's context.
- **Consumer:** `transportRuntime.dial` → `classifyDialError`.
- **Tests:** `TestServerDialerRecordsTypedSocketEvidence`, `TestSocketSuccessClearsAnEarlierFailure`.
- **Invariant:** the ledger is attempt-scoped. A later success clears an earlier failure; nothing
  outside the one dial can read it; it is not a health state.

### M2 — classification uses the shared errno sets, not text

- **Files:** `protocol/mieru/fallback.go` (`classifySocketError`, `isTransportClass`,
  `isDefiniteSocketClass`), `protocol/mieru/fallback_test.go` (signature update).
- **Old:** `classifyDialError(ctx, err, transport)`; underlay failures all landed in one
  `dialTransportFailure` via the literal prefix `"NewTCPUnderlay() failed"`.
- **New:** typed evidence is consulted first and mapped with `dialer.ErroredWith(...)` over
  `PeerAnsweredErrnos`, `UnreachableErrnos`, `PathFailureErrnos`, `TimeoutErrnos`, `ResetErrnos`.
- **Consumer:** the selector and `wrapFailureFacts`.
- **Tests:** `TestClassifySocketErrorUsesTheSharedErrnoSets`,
  `TestTypedSocketEvidenceAddsWhatTextCannotExpress`.
- **Invariant:** unknown socket errors are never guessed; a failure to classify falls through to the
  existing text rules, so behaviour without evidence is unchanged.

### M3 — definite socket failure is its own class and a Dial/Path fact

- **Files:** `protocol/mieru/fallback.go` (`dialSocketFailure`, `wrapFailureFacts`).
- **Old:** a refusal was `transport` (when the prefix was present) and produced a **Handshake/Path**
  fact, i.e. the wrong stage; a local `EADDRNOTAVAIL`/`ENETDOWN` also became a Path fact.
- **New:** refusal/unreachable → `socket`, fact **Dial/Path**. Local interface errors →
  `caller`, fact Dial/**Caller** (no member demotion).
- **Consumer:** `protocol/group.RetryThisFlow` / `PenalizeMemberGlobally` (unchanged code, new input),
  and the Mieru selector.
- **Tests:** `TestFailureFactsPerClass`, `TestLocalInterfaceFailureIsNotAPathFailure`,
  `TestRealRefusedTCPIsADefiniteDialFailure`, `TestFailureFactsSurviveWrappingAndJoin` (the fact
  survives `E.Cause`, `errors.Join` and the aggregate wrapper; a fact-less class stays fact-less).
- **Invariant:** a fact is attached only where the stage is proven; `errors.Is/As` still traverses the
  original error (`adapter.WrapFailure`).

### M4 — a socket connect timeout falls back but carries no fact

- **Files:** `protocol/mieru/fallback.go` (`dialSocketTimeout`).
- **Old:** a TCP SYN blackhole produced `NewTCPUnderlay() failed: ... i/o timeout` → `transport`, and
  because the SDK flattened the error the group's `isTimeoutFailure` could not see a `net.Error`; a
  Path/Dial fact would have demoted the member for a stall.
- **New:** distinct `socket-timeout` class: tries the secondary, **no fact**.
- **Consumer:** the selector (tries secondary) and the group (conservative default).
- **Tests:** `TestSocketConnectTimeoutMakesSYNBlackholeFallBack`, `TestFailureFactsPerClass`.
- **Invariant:** a timeout is never proof that a member's path is gone.

### M5 — both-definite outage no longer double-dials

- **Files:** `protocol/mieru/fallback.go` (`reportBothFailed`), `protocol/mieru/outbound.go`.
- **Old:** any two transport-class failures reset the preference, so a refused primary was re-dialled
  on **every** request followed by the secondary. Measured 5 requests → 5 primary + 5 secondary dials.
- **New:** two definite socket failures keep the secondary preference; the existing cooldown schedules
  one probe. Mixed evidence and silent handshakes still reset.
- **Consumer:** `fallbackSelector.choose` on the next request.
- **Tests:** `TestDefiniteSocketFailureOnBothTransportsDoesNotDoubleDial` (5 requests → 1 primary + 5
  secondary), `TestDefinitePrimaryAndSilentSecondaryResetsToPrimary`,
  `TestTwoSilentHandshakesStillResetToPrimary`.
- **Mutation proof:** disabling the new early return makes the first test fail with `primary.dials = 5`,
  expected 1.
- **Invariant:** the wrong-credential fail-closed property from the previous round is unchanged.

### M6 — an already-cancelled caller does not start a handshake, and a late answer still reports the cancellation

- **Files:** `protocol/mieru/fallback.go` (`transportRuntime.dial`).
- **Old:** the pre-check did not exist; the SDK's `mux.DialContext` checked the context in places, but
  the 10 s response read could not be cancelled. A caller that gave up during that read received the
  SDK's own timeout as the whole error, so a user cancel surfaced as a protocol timeout.
- **New:** if `ctx.Err() != nil` when the transport is entered, return the caller error immediately
  with `dialCallerFailure` — no SDK session, no socket, no preference change. If the caller cancels
  *during* the SDK read, the returned error is the SDK error joined with `ctx.Err()`
  (`E.Cause1`): the SDK detail stays in the message, and `errors.Is(err, ctx.Err())` holds, so
  `adapter.IsCallerCancellation` / the route's `IsClosedOrCanceled` see the user cancel.
- **Consumer:** every caller; the selector sees `caller`.
- **Tests:** `TestAlreadyCancelledCallerSkipsTheSDKHandshake`,
  `TestCallerDeadlineIsReportedWhenTheSDKAnswersLate`.
- **Invariant:** caller cancellation is never transport evidence, and it is never upgraded to a fault.

## MASQUE / HTTP transport

### M7 — CONNECT-IP gets its own HTTP/3 attempt window

- **Files:** `transport/http/tunnel_client.go`.
- **Old entry was late/wrong:** `openTunnel` passed `ctx` to `c.http3.OpenTunnel`. With a blackholed
  UDP path the attempt consumed the caller's whole 15 s (`C.TCPTimeout`), returned the caller's deadline
  (not `ErrHTTP3Unavailable`), and never reached the H2/H1 branch; nothing was armed, so the next
  reconnect repeated it.
- **New evidence:** `http3TunnelEstablishTimeout` (3 s) bounds the attempt. On expiry the attempt counts
  as "H3 did not come up": the H3-broken memory is armed (ordering unchanged, via
  `beginHTTP3Attempt`/`markHTTP3Broken`) and the H2/H1 branch runs with the caller's remaining budget.
- **Consumer:** `OpenTunnelWithInfo` → `protocol/masque` (the tunnel is now actually established over
  H2 in this failure), and the existing `TunnelTransport` reporting.
- **Tests:** `TestH3TunnelAttemptHasItsOwnWindow`, `TestH3TunnelStrictWindowExpiryIsStillStrict`,
  `TestH3TunnelCallerDeadlineIsNotExtendedByTheWindow`,
  `TestH3TunnelApplicationRefusalIsNotAVersionVerdict`, `TestH3TunnelImmediateUnavailableFallsBack`,
  `TestH3TunnelSuccessReportsHTTP3`, `TestH3TunnelWindowExpiryErrorNamesTheWindow`,
  `TestH3TunnelSetupOverRealLoopbackIsFarInsideTheWindow` (2.1–3.4 ms measured) and
  `TestH3TunnelOverRealLoopbackCarriesPayload` (the returned tunnel really carries bytes both
  ways after setup).
- **Before-fix evidence:** the same scenario against baseline `c363e04` (worktree `_before-tunnel`):
  "the H3 tunnel attempt is still blocked after 3 s with `h1.calls=0`; the only bound is the caller's
  15 s budget".
- **Invariants:** strict mode does not fall back and arms nothing; a caller that leaves does not trigger
  an H2 tunnel; an application refusal (`403`) is not a version verdict; the tunnel's 200 still starts a
  long-lived stream with an open write side (no change to `openConnectStream`).

## Inbound lifecycle (unexpected finding during settlement)

### M9 — Close releases the UDP underlay read instead of waiting for the SDK's 60–120 s timeout

- **Files:** `protocol/mieru/inbound.go` (ordering in `Close`), `protocol/mieru/e2e_test.go` (new test).
- **Found while checking why the full suite sometimes took 143 s instead of 80 s.**
- **Root cause (source-anchored):** the SDK creates a server `PacketUnderlay` as soon as the UDP listener
  starts (`pkg/protocol/mux.go`, `udp` case) and its event loop blocks in `ReadFrom` with
  `readOneSegmentTimeout = 60..120 s` (`pkg/protocol/underlay_base.go`). `PacketUnderlay.Close` only
  **wakes** that read with `SetReadDeadline(time.Now())` and then closes sessions before closing its
  done channel (`pkg/protocol/underlay_packet.go`). A woken loop can re-arm a fresh 60–120 s deadline
  before `done` is closed, so `Mux.Close`'s `serverUnderlayLoopWG.Wait()` (`pkg/protocol/mux.go`) waits
  for the read. Closing the `PacketConn` we own **before** `server.Stop()` makes the read return
  `net.ErrClosed` immediately and permanently.
- **Measured before:** `TestMieruInboundCanRebindAfterClose` at baseline took exactly **65.000 s** in
  2/10 runs (65 = 60 s read + 5 s `closeWaitTimeout`), and the full suite alternated 79 s / 143 s.
- **Measured after:** `TestMieruInboundCloseReleasesTheUDPUnderlayPromptly` (20 start/close cycles)
  passes in 0.01 s; rebind + prompt-close run 20× each in 0.284 s total.
- **Mutation proof:** restoring the old ordering (`Stop` before `closeAll`) makes the new test fail at
  cycle 2 with `Close took 1m5.0005327s`.
- **Invariant:** `closeOnce` still guards the whole body; the listener registry is idempotent and marks
  itself closed, so a listener created during teardown is refused (`add` returns false) rather than
  surviving.

## Documentation

### M8 — the producer contract documents the new rows

- `docs/fork/cross-protocol-failure-facts.md` gained the definite-socket / socket-timeout / local rows
  and the `%v` explanation, so the design document is no longer stale relative to the code.

## Not fixed, deliberately

| Item | Why | Minimum condition to resume |
|---|---|---|
| Mieru's 10 s response read is not cancellable; a 500 ms caller waits 10.002 s | Public SDK API has no cancellation point. A goroutine bridge would return before connection ownership is settled and would let `Close` stop a client under a live SDK dial. | Either an SDK hook (context-aware handshake) or an ownership design reviewed on device |
| The CLIENT-side `PacketUnderlay.Close` has the same wake-only shape as the server one fixed in M9 | Not measured: 75 outbound packet close/rebuild iterations (25 each of `TestMieruOutboundCloseTerminatesPacketSession`, `TestMieruOutboundUDPCloseReleasesUnderlaySockets`, `TestMieruInterfaceUpdateReopensUDPUnderlay`) took 72.3 s total with no 60 s stall. Changing teardown ordering without a repro would be a guess. | If an outbound `Close`/`InterfaceUpdated` ever shows ~60–120 s, apply the same socket-first ordering in `transportRuntime.stop()` (close `packetDialer` sockets before `client.Stop()`) and keep the inbound test as the template |
| A local family refusal that arrives as text (admission's `errLocalIPv6Unavailable`) has no typed marker | The error is a plain `E.New` in `common/dialer`; the bridge classifies it via the fallback text path (no fact). `EADDRNOTAVAIL`/`ENETDOWN` from the kernel are typed and handled. | An exported sentinel in `common/dialer` (a new API; not justified by one consumer yet) |
| No `FailureFacts` from TLS/QUIC/HTTP errors | No producer can prove the stage at those boundaries today | A protocol that can distinguish path vs application failure with a typed error |
| No caller-cancel test at the MASQUE *session* level | The transport window and its interaction with `connect()` are covered; the session loop's behaviour is unchanged | A slow/blocked H2 server harness |
| `-race` | No mingw-w64 gcc on this host | Install `mingw-w64-x86_64-gcc`, then `CGO_ENABLED=1 CC=gcc go test -race ./protocol/mieru ./transport/http` |
