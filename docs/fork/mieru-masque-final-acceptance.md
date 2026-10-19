# Mieru / MASQUE final acceptance

**Repository:** `github.com/Piggy-Cat-bit-shadow/sing-box`, branch `testing`.
**Start HEAD:** `3e0b8a11aa9b37a723e32e781bbd8931403658dc` (clean, remote equal).
**This round's fix:** `6447d0af775f6c14b77170a1f6142cd56ea3f664`; ordering test: `ccc2812e45d8f5fbbca3bc7af14b91bc0ba92a36`.
**Scope:** only the last MASQUE CONNECT-IP boundary and the documented Mieru SDK limit. No new public
failure interface, no probe, no state machine, no HTTP client refactor.

## 1. MASQUE — CONNECT-IP response slower than the window is not an H3 verdict

### Confirmed defect

`http3TunnelEstablishTimeout` (3 s) bounded the whole HTTP/3 tunnel setup. When the QUIC connection
was already established and the server's 200 simply took longer than the window, `openTunnel` saw
`probeExpired == true` and `ErrHTTP3Unavailable` absent, and unconditionally called
`markHTTP3Broken`. That armed the version memory: the next tunnel setup skipped HTTP/3 and used
HTTP/2 for the whole backoff, even though the H3 path had just carried a completed QUIC handshake and
the CONNECT request. The defect was confirmed by the new deterministic test before the fix
(`http3Broken` armed, non-zero).

### Fix (`6447d0af775`)

* Once `acquire()` has returned, a setup-window expiry is reported as
  `errHTTP3ResponsePending` (a multi-unwrap carrying both the marker and the deadline), not as
  `ErrHTTP3Unavailable`.
* `openStream` preserves that marker instead of replacing it with `ctx.Err()`.
* `openTunnel` distinguishes the cases: pending → claim the outcome but leave the verdict untouched;
  `ErrHTTP3Unavailable` or a plain deadline → mark broken; an application error → return it; strict
  mode and caller cancellation are handled before this switch, unchanged.
* `noteHTTP3AmbiguousOutcome` claims only the sequence slot (under the existing verdict mutex), so an
  older transport failure cannot arm the memory after this newer observation. It does not clear a
  previous verdict: a slow response proves the connection came up, not that the path is fully healthy.

Falling back to HTTP/2 still happens for that attempt, so the silent-blackhole behaviour is unchanged.

### Red → green evidence

`TestH3TunnelSlowResponseIsNotATransportVerdict` starts a real HTTP/3 server that completes the QUIC
handshake, receives the CONNECT-IP request, and holds the 200 for 1 s while the tunnel window is
100 ms. The server counts `ConnContext` connections and handled tunnels; the test asserts
`conns >= 1` and `tunnels >= 1`, so the slow-response case is proven rather than simulated.

| Run | Result |
|---|---|
| Before the fix | **FAIL**: `http3Broken` non-zero (`1792382877327939000`), i.e. the version was remembered as unavailable |
| After the fix | **PASS** (0.10 s): error returned, transport `Unknown`, fallback attempted, `http3Broken == 0`, `http3Available() == true` |

### Invariants preserved (existing tests, all passing)

| Invariant | Test |
|---|---|
| Silent QUIC blackhole still falls back with caller budget left | `TestH3TunnelAttemptHasItsOwnWindow`, `TestH3TunnelFallbackLeavesTheCallerBudget` |
| `disable_version_fallback` is strict (no H2, no memory) | `TestH3TunnelStrictWindowExpiryIsStillStrict` |
| Caller cancellation does not arm the verdict | `TestH3TunnelCallerDeadlineIsNotExtendedByTheWindow` |
| HTTP 4xx/application refusal is not a transport verdict | `TestH3TunnelApplicationRefusalIsNotAVersionVerdict` |
| CONNECT success reports H3 and carries payload | `TestH3TunnelSuccessReportsHTTP3`, `TestH3TunnelOverRealLoopbackCarriesPayload`, `TestH3TunnelSetupOverRealLoopbackIsFarInsideTheWindow` |
| H3 pool/generation/result order guards | `TestP14*`, `TestTunnelTransport*`, `advg_d5_claim_effect_race_test.go`, `TestAmbiguousH3OutcomeSupersedesOlderFailures` (full `transport/http` suite) |

**Deliberate scope note:** the ordinary stream CONNECT path (`Client.DialContext`) still treats a
window expiry as a version verdict; this round changed only the CONNECT-IP tunnel branch named by the
acceptance request. The stream path was not refactored, and its behaviour is unchanged.

## 2. Mieru — the 10 s SDK boundary is recorded, not papered over

`docs/fork/mieru-integration.md` now states both parts explicitly:

* the official `PostDialHandshake` SOCKS5 response read is a fixed **10 s**
  (`apis/internal/handshake.go:47`) with no cancellation point;
* a caller that cancels/times out earlier still waits for it — measured **10.002 s for a 500 ms
  caller**; the adapter now reports the caller's reason (the error is joined with `ctx.Err()`), but the
  wall time is unchanged;
* no SDK fork, no per-dial goroutine and no forced close of a connection the adapter does not own;
  the only ways below 10 s are an upstream context-aware handshake or an ownership design reviewed on
  a device.

The unchanged wall time is not described as an optimisation anywhere in this round.

### Mechanisms re-confirmed by the existing suites

| Mechanism | Representative tests |
|---|---|
| Definite socket failure, localized text, WSA errno table | `TestClassifySocketErrorUsesTheSharedErrnoSets`, `TestRealRefusedTCPIsADefiniteDialFailure` |
| Ambiguous bare EOF / wrong password does not switch | `TestMieruFallbackWrongPasswordDoesNotSwitch`, `TestClassifyDialError` |
| Endpoint DNS failure is `Dial/Local`, still tries the other endpoint | `TestEndpointResolutionFailureIsLocalButTriesTheOtherEndpoint` |
| Fact precedence after two attempts | `TestAggregateFailureFactsPrecedence` |
| Both-failed decision table (49 class pairs) | `TestReportBothFailedDecisionTable` |
| Primary/secondary cooling and single-flight recovery | `TestFallbackCooldownDoublesToTheCeiling`, `TestMieruFallbackConcurrentRecoveryProbeSingleFlight`, `TestFallbackSelectorCooldownAndSingleFlightProbe` |
| Two silent failures do not leave the secondary preferred | `TestFallbackBothSilentFailuresDoNotLeaveSecondaryPreferred` |
| Caller cancellation reported, no preference change | `TestCallerDeadlineIsReportedWhenTheSDKAnswersLate`, `TestAlreadyCancelledCallerSkipsTheSDKHandshake` |
| Generation/interface update/close races | `TestMieruFallbackConcurrentInterfaceUpdatesSerialise`, `TestMieruInterfaceUpdateClosesOldSessions`, `TestFallbackStaleGenerationCannotDeliverConnection`, `TestMieruFallbackCloseWinsAgainstInFlightDial` |
| Group/URLTest consume the facts | `TestMieruFailureFactsReachGroupConsumers`, group suite |

## 3. Targeted regression (this environment)

| Command | Result |
|---|---|
| `go test ./protocol/mieru -count=1` | **PASS**, 81.438 s |
| `go test ./protocol/group -count=1` | **PASS**, 3.866 s |
| `go test ./adapter -count=1` | **PASS**, 0.756 s |
| `go test -tags with_quic ./transport/http -count=1` | **PASS**, 25.299 s |
| `go test -tags with_quic ./protocol/masque -count=1` | **PASS**, 48.321 s |
| `go test -tags with_quic ./transport/masque -count=1` | **PASS**, 6.882 s |
| `go test ./transport/http -count=1` (untagged) | **PASS**, 8.394 s |
| `go vet ./transport/http`, `gofmt -l transport/http` | clean |
| `go test -tags with_quic ./transport/http -run TestH3Tunnel -count=5` | **PASS** |
| `go test -tags with_quic ./transport/http -run "TestAmbiguousH3Outcome|TestH3TunnelSlowResponse" -count=5` | **PASS**, 0.542 s |
| `go test -tags 'with_quic,with_low_memory' ./transport/http -run "TestH3TunnelSlowResponse|TestAmbiguousH3Outcome|TestH3TunnelOverRealLoopbackCarriesPayload" -count=3` | **PASS**, 0.362 s (the geometry the Apple gate uses) |
| `BenchmarkDialObservationOverhead` | 49.95 ns/op, 48 B/op, 1 alloc/op (unchanged baseline) |
| `BenchmarkFallbackSelectorChooseReport` | 28.05 ns/op, 0 B/op, 0 alloc/op (unchanged baseline) |

## 4. Not verified / known limitations

* **Local `-race` is blocked** (no cgo toolchain; no mingw-w64 gcc). The remote `verify` step races
  `./protocol/mieru/...` and `./route/...` (this is the Mieru race evidence), but **not**
  `./transport/http/...`; the MASQUE package is exercised without `-race` in the mobile-contract job.
  The fix adds no goroutine, timer or lock; the only new path is one small error value on a timeout.
* **No iOS/Android device measurement** was possible; no `phys_footprint` claim.
* The slow-response test uses a **real loopback HTTP/3 server**, not a live MASQUE node; the timing
  window is shortened for speed. The production 3 s value and the 10 s quic-go handshake bound are
  unchanged.
* The ordinary **stream** CONNECT path is unchanged (see the scope note above).
* Mieru's 10 s SDK read remains the floor; this round only makes the caller's reason correct and the
  limitation explicit.

## 5. Remote CI on the final head

Head `ccc2812e45d8f5fbbca3bc7af14b91bc0ba92a36` (fix + ordering test + reports):

| Check | Result | Duration |
|---|---|---|
| `verify` | **success** | 15m 14s — includes the Mieru race step |
| `Apple tags and the low-memory data paths` (mobile contract) | **success** | 9m 53s — runs `./transport/http` under the Apple tag set and `with_low_memory`, including the new slow-response test |
| `Android ARM64 core` | **success** | 2m 13s |

The `transport/http` package is **not** in `verify`'s race subset; the mobile contract runs its tests
without `-race`. `verify` races `./protocol/mieru/...` (the Mieru section's race evidence). Keep the
local `-race` limitation in §4: no cgo toolchain here.

## 6. Close-out conclusion

The CONNECT-IP boundary now has a deterministic red/green test and a minimal fix inside the existing
HTTP connection/handshake/request boundary: a slow application response no longer becomes an HTTP/3
transport verdict, while blackhole fallback, strict mode, caller cancellation, 4xx handling, pools and
generation/order guards are unchanged. The Mieru mechanisms are re-verified by the existing suites and
the SDK floor is documented with its measured cost.

**The Mieru / MASQUE special development can be closed** with the two standing limits above: the
SDK's non-cancellable 10 s handshake read, and the local `-race`/device gaps delegated to CI and
hardware.
