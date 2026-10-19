# Invariant matrix

Each row: the invariant from the debug directive, the direct test(s), and the result **measured in
this environment**. A green row is a test result, not a reading of the code.

| # | Invariant | Direct evidence | Result |
|---|---|---|---|
| 1 | Known only with proof; the first layer refuses only a local path/family proven unavailable in the current generation | `common/dialer` admission/family suites (run in the package matrix); Mieru gets the same dialer because `BypassDialerDNS=true` (`buildClientConfig`); `TestLocalInterfaceFailureIsNotAPathFailure` shows a local errno is used as Local, never as the proxy path | PASS (tests); the "proxy internal target family" question is answered by construction in BOUNDARY-AND-CALLCHAIN |
| 2 | Single observation (`epoch + stable + family capability`) | Existing `common/dialer/network_observation*` and `family_capability_single_observation_test.go` (run in the matrix); round 1/2 did not touch them | PASS (existing tests) |
| 3 | Error chain integrity through facts and aggregates | `TestFailureFactsSurviveWrappingAndJoin`, `TestAggregateFailureFactsPrecedence`, `TestCallerDeadlineIsReportedWhenTheSDKAnswersLate` | PASS |
| 4 | Origin/Stage correctness | `TestFailureFactsPerClass`, `TestClassifyDialError`, `TestClassifySocketErrorUsesTheSharedErrnoSets`, `TestEndpointResolutionFailureIsLocalButTriesTheOtherEndpoint`, `TestLocalInterfaceFailureIsNotAPathFailure`, `TestLifecycleClosureIsLocalNotTransport`; group fact table | PASS (bugs R2-002/003/004 fixed) |
| 5 | Protocol ownership (no cross-protocol state machines) | Import audit: `protocol/mieru` does not import `protocol/group`/`masque`; `transport/http` does not import Mieru; the only shared contract is `adapter.FailureFacts` | PASS by `go list -deps` audit |
| 6 | Healthy path cheap | `BenchmarkDialObservationOverhead` (49 ns, 48 B, 1 alloc per **new session**), `BenchmarkFallbackSelectorChooseReport` (~27.6 ns, 0 alloc); no new goroutine/timer on success; the tunnel window timer exists only during a tunnel setup | PASS |
| 7 | Close wins | `TestMieruFallbackCloseWinsAgainstInFlightDial`, `TestMieruFallbackCloseStopsBothRuntimes`, `TestMieruOutboundCloseTerminatesSessions`, `TestMieruInboundCloseReleasesTheUDPUnderlayPromptly`, `TestH3TunnelCallerDeadlineIsNotExtendedByTheWindow`; `TestLifecycleClosureIsLocalNotTransport` (teardown is local, not path) | PASS |
| 8 | Epoch wins | `TestFallbackStaleGenerationCannotDeliverConnection`, `TestAcceptGeneratedConnRejectsClosedOrStaleConnection`, `TestMieruFallbackInterfaceUpdateRebuildsBothGenerations`, `TestMieruFallbackConcurrentInterfaceUpdatesSerialise`, H3 `TestHTTP3ResetSupersedesAnInFlightAttempt` | PASS |
| 9 | One recovery per window | `TestMieruFallbackConcurrentRecoveryProbeSingleFlight` at **10 / 50 / 100** concurrent requests → exactly one probe; `TestFallbackSelectorCooldownAndSingleFlightProbe` | PASS |
| 10 | Caller owns budget | The window resolves to the earlier of caller deadline and 3 s (`TestH3TunnelCallerDeadlineIsNotExtendedByTheWindow`, `TestH3TunnelFallbackLeavesTheCallerBudget`); Mieru's SDK response read is not cancellable — measured 10.002 s for a 500 ms caller, reason now reported (`TestCallerDeadlineIsReportedWhenTheSDKAnswersLate`) | PARTIAL: documented limit, not hidden |
| 11 | No duplicate business effect | No code path mirrors a request or sends the business payload twice; the recovery probe is one real business request (single-flight); round-1/2 did not change the Mux or the tunnel write side; `TestH3TunnelOverRealLoopbackCarriesPayload` proves the tunnel is still writable after setup | PASS |
| 12 | Compatibility first | `TestResolveFallbackOptions*`, `TestMieruFallbackDisabledCreatesNoSecondaryRuntime`, `TestNewOutboundRejectsFallbackConflicts`, `TestMieruNoWaitWrongPasswordDoesNotReportSuccess`, H3 strict/pinned tests (`TestP14*`), full package suites | PASS |
| 13 | Group semantics remain safe | `protocol/group` fact table (including the new Local row) and failover tests: same ECONNREFUSED with target vs path facts → different decisions; timeout/`context.Canceled`/`net.ErrClosed` never penalise | PASS |
| 14 | Resources owned and bounded | Inbound shutdown regression + 20-cycle stress; outbound close tests; `TestMieruOutboundUDPCloseReleasesUnderlaySockets`; H3 `client_h3_leak_test.go`, stand census tests (run in the matrix); round-2 changes add no goroutine/ticker | PASS |

## Not run (environment)

- `go test -race`: blocked. The host has no mingw-w64 gcc; `clang` is MSVC-targeted and Go's cgo
  driver passes `-mthreads`, which it rejects. This was re-confirmed this round. Minimum condition:
  install `mingw-w64-x86_64-gcc` and run
  `CGO_ENABLED=1 CC=gcc go test -race ./protocol/mieru ./protocol/group ./transport/http ./protocol/masque ./transport/masque`.
- iOS/Android device, TUN, real cellular/Wi-Fi transition, real GFW blackhole: not available; no
  claim is made for them.
- Linux/macOS: Windows-only host this round. The Linux-specific UDP bare-EOF path is covered by the
  existing unit classification; its *platform* behaviour is not re-measured here.
