# MASQUE / HTTP transport audit (round-2 scope)

Only the parts the round-1 change touches: the H3→H2 establishment decision for CONNECT-IP, its
interaction with the existing backoff/ordering machinery, and the long-lived session boundary.

## 1. The two establishment paths are distinct, and both now have a bound

| Path | Bound | Evidence |
|---|---|---|
| `Client.DialContext` (stream) | `http3EstablishTimeout` = 3 s, unchanged this round | `http3EstablishTimeout` still `3 * time.Second` in `client.go`; `TestP14*`, `TestHTTP3Backoff*` pass in the matrix |
| `OpenTunnelWithInfo` → `openTunnel` (CONNECT-IP) | **new** `http3TunnelEstablishTimeout` = 3 s | `TestH3TunnelAttemptHasItsOwnWindow`: baseline (worktree at `c363e04`) was still blocked after 3 s; now the attempt returns inside the window and reaches the H2/H1 branch |

The tunnel window is a separate tunable on purpose: the two lifecycles differ, and a change to one
must not silently move the other. The value is justified by measurement, not analogy: a real
loopback CONNECT-IP setup takes **2.1–3.4 ms**; quic-go's own handshake bound is **10 s**
(`2 × HandshakeIdleTimeout`, default 5 s); the MASQUE caller's budget is **15 s**
(`C.TCPTimeout`), so the window leaves 12 s for the fallback. Before the fix the H3 attempt consumed
the whole 15 s and returned the caller's deadline, which is not `ErrHTTP3Unavailable`, so H2 was
never attempted.

## 2. Required checks (directive §6.1)

1. **H3 blackhole, H2 budget.** `TestH3TunnelAttemptHasItsOwnWindow`,
   `TestH3TunnelFallbackLeavesTheCallerBudget` (caller's 15 s context is still alive after the
   fallback ran) and `TestH3TunnelCallerDeadlineIsNotExtendedByTheWindow` (a shorter caller deadline
   wins; no H2 is started). PASS.
2. **H3 came up, CONNECT refused / application error.** `TestH3TunnelApplicationRefusalIsNotAVersionVerdict`:
   a 403 from a working H3 connection returns the status error, makes **no** H2 attempt and arms
   nothing. PASS.
3. **Strict mode even on window expiry.** `TestH3TunnelStrictWindowExpiryIsStillStrict`: no H2, no
   armed memory, and the error names the window. Pinned-version behaviour is unchanged
   (`TestP14PinnedHTTP2ConfigNeverConstructsOrUsesHTTP3`). PASS.
4. **HTTP/2 extended CONNECT unsupported → H1 preserved.** `openTunnel` still branches on
   `errExtendedConnectUnsupported` and falls to `openTunnelHTTP1AndClose` unless strict. No code in
   this round touches that branch; the existing H2/H1 tests pass in the with_quic matrix.
5. **200 starts a tunnel; write side stays open.** `TestH3TunnelOverRealLoopbackCarriesPayload`
   writes through the returned `DatagramStream` on the real loopback H3 stand and reads the echo
   back; `openConnectStream` is unchanged. PASS.
6. **`TunnelTransport` reports the transport that actually carried the tunnel.** `openTunnel` returns
   the transport from the branch that succeeded; `TestH3TunnelSuccessReportsHTTP3`,
   `TestH3TunnelOverRealLoopbackCarriesPayload` and the MASQUE tests (`UpdateTunnelTransport`) pin
   it. No inference from "an H3 connection object exists" was added.

## 3. Backoff and concurrency (§6.2)

Existing machinery, unchanged, and re-run in the matrix:

- one failing event = one escalation step: `TestConcurrentHTTP3FailuresAreOneEscalationStep`;
- stale outcome ordering: `TestHTTP3StaleFailureDoesNotOverwriteANewerSuccess`,
  `TestHTTP3StaleSuccessDoesNotClearANewerFailure`;
- reset during reporting: `TestHTTP3ResetSupersedesAnInFlightAttempt`;
- neutral outcomes arm nothing: `TestNeutralOutcomesArmNoHTTP3Memory`;
- a new tunnel failure is stamped with `beginHTTP3Attempt` **before** the attempt and claims through
  `claimHTTP3Outcome`, exactly like the stream path, so it cannot invent a second ordering system.

Caller cancellation, `net.ErrClosed`, suspension: a caller-done context returns before the verdict
logic (`ctx.Err() != nil`) and arms nothing; the transport's own closed/suspended errors are not
converted into an H3 verdict (tests above plus `TestP14CallerCancel*`).

No `FailureFacts` are attached by the HTTP layer, so a H3 window expiry cannot demote a member or
change URLTest health; the group sees the plain error and keeps its conservative default.

## 4. Reconnect / suspend boundary (§6.3)

`transport/masque/client.go` owns the reconnect loop; the tunnel window lives inside one
`openTunnel` call and adds no goroutine, callback or background timer. `Suspend` cancels the session
context (unchanged); `Close` cancels `c.ctx` (unchanged); the only new timer is the
`context.WithTimeout` created for one attempt and cancelled when it returns. Existing lifecycle
tests pass in the with_quic matrix (`readiness_boundary_test.go`, `lifecycle_generation_test.go`,
`close_boundary_test.go`, `close_boundary_h3_test.go`).

Not re-tested here: a real suspended device (no environment), and a slow response on an **already
live** H3 connection taking > 3 s (would arm the broken memory for one backoff step). That trade-off
is documented in the round-1 ledger; it is not a correctness or security path, and the tunnel is
still carried by the fallback rather than failing.
