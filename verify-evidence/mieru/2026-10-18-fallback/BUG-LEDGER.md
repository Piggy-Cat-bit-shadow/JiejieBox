# Fallback debug ledger

Feature code: `e1efe4cafdc621473442302d88897b2e476bbf0a`.
Every reproduced item below has a failing command captured before the fix in
`repro/`; the final suite is in `tests/fallback-tests.txt`.

## FB-001 — P1 — handshake error returned a live SDK connection that was dropped

**Affected:** existing `Outbound.dial` (pre-fallback) and the new
`transportRuntime.dial`; the official `mieruclient.DialContext` returns
`(conn, err)` with a non-nil conn when `PostDialHandshake` fails.

**Reproduction:** `TestTransportRuntimeClosesConnectionReturnedWithHandshakeError`.
The pre-fix mutation in `repro/bug1.txt` returns the connection with the error;
the test observes `closed=0`, i.e. a leaked Mieru session. The test was run by
temporarily removing the `conn.Close()` in `transportRuntime.dial`; the production
code was restored.

**Root cause:** the old adapter checked only `err != nil` and returned nil; the
SDK-owned session had no other closer.

**Fix:** `transportRuntime.dial` closes any non-nil conn whenever `DialContext`
returns an error. `acceptGeneratedConn` also rejects/closes a stale or closed
generation's success connection.

**Tests:** the named test plus all fallback timeout paths.

**Performance:** one nil check on the error path; no data-path change.

## FB-002 — P1 — two silent handshakes left the outbound preferring the secondary

**Affected:** initial `fallbackSelector`/`dialWithFallback` implementation.

**Reproduction:** `TestMieruFallbackWrongPasswordDoesNotSwitch` with the
`selector.reportBothFailed` call removed (`repro/bug2.txt`). After a wrong
password, primary and secondary both timeout; without the reset the snapshot is
`preferred=secondary` (`expected 0, actual 1`). The test spends 20 s because the
SDK's standard handshake timeout is 10 s on each transport.

**Root cause:** the primary timeout was classified as transport (the same signal
as a UDP blackhole), so the selector published a durable secondary preference
even though neither transport had produced a SOCKS response. Wrong credentials,
a server-side close and a dead underlay are the same plain error at the SDK API.

**Fix:** `reportBothFailed` resets preferred to primary and clears the cooldown
when both attempts fail without a reply. This is fail-closed: no success is
reported and the next request tries primary again. It does **not** claim that
authentication can be distinguished; one secondary attempt remains possible,
documented in `ERROR-CLASSIFICATION.md`.

**Tests:** wrong-password E2E; both-silent fake flow; target-refusal fake flow.

**Performance:** only on two-failed-attempt path; no success-path cost.

## FB-003 — P2 — a silent recovery probe could be retried on every request

**Affected:** `fallbackSelector.report` ambiguous branch.

**Reproduction:** `TestFallbackSelectorAmbiguousProbeBacksOff` with the
ambiguous-probe backoff removed (`repro/bug3.txt`); the test fails because
`PrimaryUntil` stays in the past and the next `choose` becomes a probe again.

**Root cause:** bare EOF cleared the probe latch but left an expired cooldown,
so every new business request could become a fresh primary probe. A persistent
credential/unknown failure would then hammer the dead primary.

**Fix:** an ambiguous probe doubles the cooldown (capped at 5 min) and moves
`primaryUntil` forward; caller cancellation still only clears the latch.

**Tests:** ambiguous-probe test; existing probe/cancellation/backoff tests.

## FB-004 — P2 hardening — a late generation's successful connection could reach the route

**Affected:** fallback success return path.

**Design:** `InterfaceUpdated` can finish its 5 s drain while a primary/secondary
handshake is still blocked. The selector already ignores a stale report, but the
caller would still receive the old generation's connection. `acceptGeneratedConn`
checks `closed` and the current generation, closes the stale conn and returns
`errFallbackStale`/`errOutboundClosed`. It uses `TryRLock` so the drain is not
blocked by the same write lock it is waiting on.

**Test:** `TestMieruFallbackCloseWinsAgainstInFlightDial` and
`TestFallbackStaleGenerationCannotDeliverConnection` (controlled fake handshake
completes after generation 2 is installed; connection is closed and
`errFallbackStale` returned). This is a **hardening** item; the exact
interleaving was reasoned from the lifecycle and exercised with a fake
generation change, not reproduced as a pre-fix production failure.

## FB-005 — P2 hardening — secondary Start failure rollback

**Affected:** `Outbound.Start`.

**Test:** `TestOutboundStartRollsBackBothRuntimesOnSecondaryFailure` (fake client
returns a Start error). Primary is stopped, both runtime pointers are cleared,
and the next Start builds fresh Clients because an SDK Client cannot be restarted
after Stop.

## FB-006 — P2 — `server_ports`-only primary and omitted fallback port

**Affected:** `resolveFallbackOptions`.

The backup endpoint is one port, not a second range. If the primary has only
`server_ports` and the fallback omits `server_port`, the config is rejected with
`fallback.server_port is required ...` instead of silently selecting the first
range member. If the fallback port is explicit, the primary keeps its full range
and the backup is one endpoint.

**Test:** `TestResolveFallbackOptionsRejects/range_only_with_omitted_fallback_port`
and `...RangeOnlyWithExplicitPort`.

## Also verified, not claimed as new bugs

- `both` and `handshake_mode: "no_wait"` plus fallback are rejected; the old
  independent behavior remains (config tests and full package).
- A valid SOCKS target error (real mita restricted user, code 2) does not switch
  and returns in 3 ms.
- An established primary session survives a fallback caused by new-connection
  failure (proxy test), and an established secondary session survives primary
  recovery (TCP-refused test).
- Target/interface/handshake failures do not stop either SDK client; only
  `Close`/`InterfaceUpdated` do.
- No background timer/goroutine: idle enabled vs disabled is +0.25 MiB and no
  extra thread/socket.

## Not fixed / explicit limitation

The official SDK does not expose a stable discriminator for wrong credentials
versus an underlay blackhole. A wrong password can therefore pay one secondary
attempt before both sides fail silently and the selector resets. It cannot
return a successful connection unless the secondary also authenticates with the
shared credentials. A future SDK typed error would be required to remove the
attempt; this round does not fork the SDK or add an authentication probe.
