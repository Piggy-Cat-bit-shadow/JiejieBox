# Adversarial matrix — disguised sources, torn windows and the ten questions

## Error-source masquerade (directive §4.1)

| Evidence scenario | What this tree reports now | How it is proven | Must not happen |
|---|---|---|---|
| TCP/UDP first-hop refused (typed) | `socket` → Dial/Path, tries the secondary | `TestRealRefusedTCPIsADefiniteDialFailure`, `TestClassifySocketErrorUsesTheSharedErrnoSets` | being read as a target refusal |
| Proxy works, SOCKS target refused | `Target/Target`; no transport switch; group no penalty | `TestClassifyDialError` target row, `TestFallbackTargetRefusalDoesNotTrySecondary`, `TestMieruFailureFactsReachGroupConsumers` | switching / demoting |
| MASQUE CONNECT-IP 4xx | the status error is returned; no version switch; nothing armed | `TestH3TunnelApplicationRefusalIsNotAVersionVerdict` | "UDP path dead" |
| TLS certificate / identity error | the H3 attempt fails; the H2 attempt uses the **same** TLS config and fails the same way; no fact is attached by the HTTP layer | code audit (`client_h3.go:acquire` wraps dial errors as `ErrHTTP3Unavailable`; `openTunnel` falls back; H2 TLS is the same `tlsConfig`); no test asserts a bypass because there is none | retrying around authentication |
| DNS parse/policy, bind permission | `local` → Dial/Local; no switch; no penalty | `TestClassifyDialError` dns row, `TestLocalInterfaceFailureIsNotAPathFailure` | recorded as global UDP death |
| Caller context canceled | `caller` → Dial/Caller; no switch; no penalty; reason preserved | `TestAlreadyCancelledCallerSkipsTheSDKHandshake`, `TestCallerDeadlineIsReportedWhenTheSDKAnswersLate` | switch/penalty/background recovery |
| Owner shutdown / stale generation | teardown is `local`; stale results are dropped | `TestLifecycleClosureIsLocalNotTransport`, `TestFallbackStaleGenerationCannotDeliverConnection`, `TestMieruFallbackCloseWinsAgainstInFlightDial` | reviving after Close |
| Mieru TCP bare EOF / wrong password | EOF → `ambiguous` (no fact); wrong password on this SDK version surfaces as a 10 s timeout → `transport` → Handshake/Path | `TestClassifyDialError`, `TestMieruFallbackWrongPasswordDoesNotSwitch`, FAULT-TIMELINE (10.002 s) | claiming the primary channel is dead from a bare EOF |
| Mieru UDP EOF (Linux unreachable) | `transport` for a UDP primary only | `TestClassifyDialError` UDP EOF row (unit); platform not re-measured here | generalising it to every platform/EOF |
| `context.DeadlineExceeded` | Caller when the caller's context ended; internal H3 window is `ErrHTTP3Unavailable`-shaped and does **not** look like a caller deadline | `TestH3TunnelCallerDeadlineIsNotExtendedByTheWindow`, `TestH3TunnelWindowExpiryErrorNamesTheWindow` | confusing the two |

### Wrapping / join attacks

`TestFailureFactsSurviveWrappingAndJoin` covers `E.Cause`, `errors.Join` and the aggregate shape:
a `Dial/Path` fact survives, and a deliberately fact-less class stays fact-less through the same
wrappers. The aggregate is wrapped **once**, at the outbound boundary, with the class chosen by
`aggregateFailureClass` — so no "first wrapper wins" ambiguity for Mieru errors. For errors from
other layers, `FailureFactsOf` returns the nearest `FailureFactsProvider` in the chain; attaching
facts in two layers would therefore let the outer one win, which is why the producer table says only
the outbound boundary attaches them.

## The ten questions (directive §9)

1. **Can `FailureFacts` express the final responsibility in `errors.Join(primary, fallback)`?**
   Yes, because the producer does not join raw facts: `dialWithFallback` joins the two errors and
   wraps the aggregate with one precedence-chosen fact. That fixed a real loss (BUG-R2-001): a
   fact-less `socket-timeout` on the secondary used to erase the primary's `Dial/Path` proof.
2. **Does Admission reject the proxy-server address or the proxy-internal target?**
   The proxy server. `buildClientConfig` sets `BypassDialerDNS=true`, `serverDialer` dials only the
   configured server (with the port the SDK endpoint requests), and the destination is sent as
   protocol payload. `TestServerDialerStreamPassesDomainThrough` and
   `TestServerDialerUDPKeepsTheForkInChargeOfResolution` pin the dialer's input.
3. **During `stable=false`, does the first layer degrade to unknown or hard-block the new network?**
   Existing `common/dialer` transition tests are the evidence (run in the matrix); round 1/2 did not
   alter the snapshot or the admission policy.
4. **Can a Path fact from an H3 sub-timeout demote a member whose H2 works?**
   No. The HTTP transport never attaches `FailureFacts`; the tunnel window's expiry is returned as an
   error and, if H2 then works, the caller never sees a failure. Even when H2 also fails, the group
   sees no fact and falls back to its conservative rule. (The H3-broken memory is per HTTP client and
   only skips H3 for 5 s–5 min; it does not demote a member.)
5. **Are certificate errors, server auth refusal, HTTP CONNECT 4xx and Mieru wrong passwords put
   on `Path` by text matching?**
   Certificate errors: no fact, and H2 shares the same TLS config (no bypass). CONNECT 4xx: a status
   error, explicitly not a version verdict (test). Mieru wrong password: on this SDK it is a 10 s
   timeout → `transport` → Handshake/Path; that is a documented limitation of the API (no typed auth
   error), and the group's timeout conservatism means it cannot demote the member. A bare EOF (the
   other shape) stays Unknown.
6. **Does a long recovery failure damage the business that is already on the secondary, or copy it?**
   No. The selector's probe is one real business request; other concurrent requests stay on the
   secondary (`TestMieruFallbackConcurrentRecoveryProbeSingleFlight` at 10/50/100). There is no
   mirrored request, no parallel CONNECT and no background probe.
7. **Does `reportBothFailed` double-dial on two immediate refusals, and does the fix pin wrong
   passwords to the secondary?**
   The double-dial was measured (5 requests → 5 primary + 5 secondary dials) and is fixed: two
   definite failures keep the secondary preference and the cooldown schedules one probe. Two silent
   handshakes still reset to the primary (`TestTwoSilentHandshakesStillResetToPrimary`).
8. **Can an old network's success/failure still write into the new preference after
   `ResetConnections` / `Suspend` / `InterfaceUpdated`?**
   The selector is generation-guarded (`TestFallbackStaleGenerationCannotDeliverConnection`,
   `TestMieruFallbackInterfaceUpdateRebuildsBothGenerations`); the H3 verdict is superseded and
   cleared in one critical section (`TestHTTP3ResetSupersedesAnInFlightAttempt`,
   `TestHTTP3StaleFailureDoesNotOverwriteANewerSuccess`). Round 2 added a concurrent
   `InterfaceUpdated` serialisation test.
9. **Does a wrapper that does not implement a new optional interface silently lose a capability?**
   There is no new interface: round 1 and 2 reuse `adapter.FailureFacts` and add no capability. The
   zero value remains "no fact", so an implementation that attaches nothing behaves exactly as
   before. (This is also why the design has no capability discovery to lose.)
10. **Did the unified system add a timer, network probe or session that keeps running in the
    background?**
    No new background work. The Mieru selector has no goroutine; the observation ledger is
    per-dial; the HTTP window creates one context timer per tunnel *setup* and cancels it. The SDKs'
    own per-underlay 5 s clean ticker existed before and is not attributed to this round.
    `BenchmarkDialObservationOverhead` measures the new per-session cost (45 ns, 48 B, 1 alloc).

## Self-found extra item

`protocol/mieru/inbound.go`'s shutdown ordering (round-1 settlement, re-audited here) was not a
directive item. It is included because it was found while auditing this round's timing evidence and
is an owner-shutdown correctness issue (65 s stall reproduced, fixed, mutation-proof).
