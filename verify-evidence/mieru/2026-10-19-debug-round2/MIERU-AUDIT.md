# Mieru audit (round-2 scope)

The original requirement — one Mieru endpoint, primary transport preferred, secondary only on a
proven primary failure, cooldown and recovery, unchanged behaviour when fallback is off — is the
reference for every row. Measurements use the real official SDK (`enfein/mieru v3.38.1`) over
loopback, with local blackhole listeners; the same injection was run on the pre-construction baseline
(`c363e04`) and on the debug tree, so the deltas are comparable.

## Measured timeline — same injection, baseline vs debug tree

| Scenario | Baseline `c363e04` | Debug tree | Notes |
|---|---|---|---|
| TCP refused (closed port), one transport | **1 ms**, class `transport`, fact `Handshake/Path` | **1 ms**, class `socket`, fact **`Dial/Path`** | typed proof; the fastest path gets its correct stage |
| TCP accepted then silent | 10.003 s | **10.002 s** | SDK response read; no earlier fact exists |
| UDP blackhole (no listener) | 10.003 s | **10.002 s** | same floor |
| Wrong password, live server | 10.002 s | **10.001 s** | indistinguishable from blackhole at the public API; timeout → `Handshake/Path`, group does not demote on a timeout |
| Fallback, TCP refused → UDP live | 1 ms | **1 ms** | one refused attempt + one healthy handshake |
| Fallback, UDP blackhole → TCP live | 10.003 s | **10.003 s** | floor + healthy secondary |
| Fallback, TCP silent → UDP live | 10.002 s | **10.002 s** | floor |
| Caller budget 500 ms, UDP blackhole primary | 10.001 s, error is the SDK timeout only | **10.002 s**, error carries `context deadline exceeded` too (`deadlineReason=true`), preference unchanged | the wait is the SDK floor; the *reason* is now correct |

Raw logs: `logs/fault-timeline-baseline.txt` (baseline worktree at `c363e04`) and
`logs/fault-timeline.txt` (debug tree), same loopback injection, same test body.

Deliberately not claimed: an "80 % earlier detection" number. What is earlier is the *classification*
of a refusal (definite Dial/Path at ~1 ms) and the *decision* to fall back at ~1 ms; the silent
blackholes are still bounded by the SDK's 10 s response read.

## §5.1 scenario coverage

| # | Scenario | Evidence | Result |
|---|---|---|---|
| 1 | primary TCP fast refuse / accept-then-close / SYN blackhole; UDP secondary healthy | `TestMieruFallbackTCPRefusedUsesUDPAndRecovers`, `TestMieruFallbackUDPBlackholeUsesTCP`, `TestSocketConnectTimeoutMakesSYNBlackholeFallBack`, timeline above | PASS |
| 2 | primary UDP no route / socket ok but peer drops / ICMP unreachable / port closed; TCP secondary | `TestMieruFallbackUDPBlackholeUsesTCP`, `TestClassifySocketErrorUsesTheSharedErrnoSets`, `TestLocalInterfaceFailureIsNotAPathFailure` | PASS (local no-route paths are unit-level; a real "no route" device state is not available) |
| 3 | reversed roles, different ports/endpoints, range boundaries | `TestMieruFallbackTCPRefusedUsesUDPAndRecovers` (reverse shapes in the UDP-primary tests), `TestMieruFallbackIPv6Endpoint`, `TestResolveFallbackOptionsRangeOnlyWithExplicitPort` | PASS |
| 4 | fast switch; allowed recovery only; sessions not migrated | `TestMieruFallbackTCPRefusedUsesUDPAndRecovers` (old secondary session still carries after recovery), `TestMieruFallbackDoesNotStopExistingPrimarySession` | PASS |
| 5 | both fail fast; no per-request double dial; vs fail-closed auth | `TestDefiniteSocketFailureOnBothTransportsDoesNotDoubleDial`, `TestTwoSilentHandshakesStillResetToPrimary` | PASS (bug R2-001 fixed) |
| 6 | both blackhole; definite + ambiguous; target + broken primary; ≥5 class pairs | `TestAggregateFailureFactsPrecedence` (9 rows), `TestReportBothFailedDecisionTable` (36 pairs), `TestTwoSilentHandshakesStillResetToPrimary` | PASS |
| 7 | wrong password, wrong hashed password, server-side rejection, `no_wait` conflict | `TestMieruFallbackWrongPasswordDoesNotSwitch`, `TestMieruNoWaitWrongPasswordDoesNotReportSuccess`, `TestNewOutboundRejectsFallbackConflicts`, `TestMieruHashedPasswordInterop` | PASS |
| 8 | UDP Associate, stream data, target reject, cancel, Close; real payload | `TestMieruFallbackUDPAssociateUsesSecondary`, `TestMieruFallbackUDPBlackholeUDPAssociateUsesTCP`, `readAndWriteEcho` in every E2E, `TestMieruFallbackCloseWinsAgainstInFlightDial` | PASS |
| 9 | same-generation order: old failure late vs new success; next-session preference | `TestSameGenerationOrderOfDialOutcomes`, `TestLateFailureAffectsTheNextNewSessionPreference` | PASS as a documented, bounded behaviour (see below) |
| 10 | cross-generation order: old network failure after new success | `TestFallbackStaleGenerationCannotDeliverConnection`, `TestMieruFallbackInterfaceUpdateRebuildsBothGenerations` | PASS |
| 11 | repeated `InterfaceUpdated`, interleaving, Close vs dial/probe/start, non-nil conn on error | `TestMieruFallbackConcurrentInterfaceUpdatesSerialise`, `TestMieruFallbackCloseWinsAgainstInFlightDial`, `TestTransportRuntimeClosesConnectionReturnedWithHandshakeError`, `TestOutboundStartRollsBackBothRuntimesOnSecondaryFailure` | PASS |
| 12 | fallback off, TCP-only, UDP-only, `both`, unsupported combos, user hint, IPv4/IPv6, interop | `TestMieruFallbackDisabledCreatesNoSecondaryRuntime`, `TestMieruTCPLoopback`, `TestMieruUDPOverStreamLoopback`, `TestMieruUDPTransportLoopback`, `TestMieruInboundPortRange`, `TestMieruUserHintMandatory`, `TestMieruIPv6Loopback`, `TestMieruFQDDestinationRoundTrip` | PASS |

## Same-generation ordering (row 9), stated precisely

Two attempts on the same generation, the older one reporting a transport failure after the newer one
succeeded: the preference flips to the secondary for one cooldown, and the next new session follows
that hint; the next successful probe restores the primary. This is deliberate and documented in the
round-1 architecture decision: "newer" is ambiguous for a long attempt (it can observe a *later*
world), the cost is one bounded cooldown, and the alternative would keep new sessions on a primary
that is dead now. MASQUE's attempt-order guard exists because there a stale failure pins the version
for a whole backoff and escalates; the Mieru hint does neither.

## Recovery attacks (§5.3)

- Cooldown boundaries: `TestFallbackCooldownDoublesToTheCeiling` walks 29 s (no probe), 30 s (one
  probe), then 60 → 120 → 240 → 300 → 300 s (ceiling), and proves a caller-cancelled probe releases
  the latch without moving the backoff.
- One recovery owner under load: `TestMieruFallbackConcurrentRecoveryProbeSingleFlight` at **10, 50
  and 100** simultaneous requests → exactly one probe; the rest stay on the secondary.
- No wake-up/probe: the selector has no goroutine or timer; the recovery probe is a real business
  request. There is no background reconnection to wake a suspended device.
- Both-bad / primary-recovery cases: covered by the class matrix + the decision table above; no
  infinite switching (the cooldown is monotone until a success clears it).

## Compatibility

`resolveFallbackOptions` is unchanged; disabled fallback never constructs a secondary runtime; the
`no_wait` + fallback conflict is still rejected; `transport: both` restrictions unchanged; hashed
password and multi-user interop unchanged. The debug fixes touched only error classification and
aggregate fact selection.

## Known limit, re-measured

A caller that gives up during the SDK's SOCKS5 response read still waits for that read (10.002 s for a
500 ms budget). The adapter cannot interrupt it through the public API; it now at least reports the
caller's reason. The minimum condition to fix is an SDK hook (context-aware handshake) or an
ownership design with a per-dial goroutine, both out of this round's scope.
