# Early failure facts and MASQUE tunnel fallback — 2026-10-19

Scope: the first round of the unified failure-prediction directive
(`01-testing-BBE48095E99CA2E5A4B8E6BDB7E690A4E88D.md`), executed on `testing`
from baseline `c363e04bb3065de68e2123182013124cbb65a900`.

| | |
|---|---|
| Branch | `testing` |
| Baseline SHA | `c363e04bb3065de68e2123182013124cbb65a900` |
| `origin/testing` at fetch | `c363e04bb3065de68e2123182013124cbb65a900` (no fast-forward needed) |
| Worktree at start | clean |
| Host | Windows 10 19044 LTSC, Ryzen 7 4800H (8C/16T), 15.4 GiB |
| Toolchain | `go1.25.5 windows/amd64`, `CGO_ENABLED=0` |
| Race detector | `BLOCKED_BY_ENVIRONMENT` — no mingw-w64 gcc; `clang` is MSVC-targeted and rejects `-mthreads` (re-confirmed this round, see PERFORMANCE-AND-REGRESSION.md §Limits) |
| Network fetch | blocked in this environment (DNS resolves to non-public addresses); external study therefore rests on the vendored module sources and the fork's own git tree, with one search-result citation marked as such |

## What this round changed

Three production behaviours, each with a deterministic before/after:

1. **Mieru: the socket-level failure proof is no longer thrown away by the SDK.**
   The official client flattens every underlay error to text (`%v`). The fork's bridge now records the
   typed error for the duration of one SDK dial, classifies it with the tree-wide errno sets
   (`common/dialer.ErroredWith`, including the raw Winsock spellings), and exports a **Dial / Path**
   fact only when the socket layer *proved* the first hop unusable. Two such proofs on both transports
   stop the "re-dial the refused primary, then the secondary, on every request" amplification, while
   two silent handshakes (the wrong-password shape) still reset to the primary.

2. **MASQUE CONNECT-IP: the HTTP/3 attempt has a window of its own.**
   `transport/http.Client.openTunnel` passed the caller's context straight to the H3 attempt. Under a
   silently blackholed UDP path the attempt ran to the caller's 15 s budget, returned the caller's
   deadline (not `ErrHTTP3Unavailable`), and the HTTP/2 branch was never reached — on every reconnect.
   The attempt now gets `http3TunnelEstablishTimeout` (3 s), mirroring the stream-dial window, with
   strict mode, caller cancellation and application refusals keeping their existing semantics.

3. **Mieru inbound shutdown no longer waits out a 60–120 s UDP read (found while settling).**
   The SDK's server `PacketUnderlay` is woken by a read deadline and can re-arm a fresh one before its
   done channel closes, so `Mieru Inbound.Close` occasionally took exactly 65 s (2/10 runs at
   baseline). The fork now closes the listener sockets it owns **before** `server.Stop()`, which
   releases the read deterministically; this also explains why the package suite alternated between
   ~79 s and ~143 s.

Nothing else was touched: no new background probe, timer or goroutine; no change to Mieru's wire
protocol, auth, Mux ownership, or to MASQUE's capsules/MTU/DNS handling.

## Files

- `protocol/mieru/dial_observation.go` (new) — the per-dial typed-evidence ledger.
- `protocol/mieru/dialer.go` — the bridge records every socket result.
- `protocol/mieru/fallback.go` — socket classification, two new classes, facts, both-failed rule,
  caller-reason preservation.
- `protocol/mieru/outbound.go` — transport-class helper in the fallback branch.
- `protocol/mieru/inbound.go` — close the listener sockets before the SDK's Stop.
- `transport/http/tunnel_client.go` — the H3 tunnel window.
- Tests: `protocol/mieru/early_failure_test.go`,
  `transport/http/client_h3_tunnel_window_test.go`, `protocol/mieru/e2e_test.go` (prompt-close
  regression), plus the classifier call-site update in `protocol/mieru/fallback_test.go`.
- Design doc updated: `docs/fork/cross-protocol-failure-facts.md` (producer table + the `%v` bridge).

## Evidence map

| Question | File |
|---|---|
| What is the measured timeline of each failure? Where was the earliest reliable fact? | [FAULT-TIMELINE.md](FAULT-TIMELINE.md) |
| Which layer can prove what, and which claims are deliberately not made? | [EARLY-FAILURE-SIGNAL-MATRIX.md](EARLY-FAILURE-SIGNAL-MATRIX.md) |
| Why this design, and which alternatives were rejected? | [ARCHITECTURE-DECISIONS.md](ARCHITECTURE-DECISIONS.md) |
| What was learned from real source, and what was adopted/rejected? | [EXTERNAL-DESIGN-STUDY.md](EXTERNAL-DESIGN-STUDY.md) |
| Which change fixes which reproduction, and what invariant does it keep? | [MIERU-MASQUE-CHANGE-LEDGER.md](MIERU-MASQUE-CHANGE-LEDGER.md) |
| Cost, regression and verification commands | [PERFORMANCE-AND-REGRESSION.md](PERFORMANCE-AND-REGRESSION.md) |

## Reproduction

```pwsh
# affected packages
go test ./protocol/mieru/ -count=1
go test ./transport/http/ -tags with_quic -count=1

# the new behaviours, individually
go test ./protocol/mieru/ -run 'TestTypedSocketEvidenceAddsWhatTextCannotExpress|TestDefiniteSocketFailureOnBothTransportsDoesNotDoubleDial|TestSocketConnectTimeoutMakesSYNBlackholeFallBack|TestRealRefusedTCPIsADefiniteDialFailure' -count=1 -v
go test ./transport/http/ -tags with_quic -run 'TestH3Tunnel' -count=1 -v

# cost of the ledger (error-free path: one observation per new session)
go test ./protocol/mieru/ -run '^$' -bench 'BenchmarkDialObservationOverhead|BenchmarkFallbackSelectorChooseReport' -benchmem
```

## Handoff for the next (debug) directive

Re-verify, in this order:

1. `go test ./protocol/mieru/ -count=1` and `go test ./transport/http/ -tags with_quic -count=1`
   on the new HEAD (they must include the new tests listed in the ledger).
2. `TestRealRefusedTCPIsADefiniteDialFailure` — a real refused loopback port must stay ~1 ms and be
   classified `socket`. If it ever regresses to `transport`, the bridge recording was lost.
3. `TestDefiniteSocketFailureOnBothTransportsDoesNotDoubleDial` — 5 requests over two refused
   endpoints must produce 1 primary + 5 secondary dials, not 10. This is the exact amplification the
   rule exists for.
4. `TestH3TunnelAttemptHasItsOwnWindow` / `TestH3TunnelSetupOverRealLoopbackIsFarInsideTheWindow` —
   the window still bounds the blackhole and still does not touch a healthy 3.4 ms setup.
5. On a host with mingw-w64 gcc: the `-race` commands in PERFORMANCE-AND-REGRESSION.md.
6. Anything that wants to go below the 10 s Mieru floor must first answer the SDK-hook question
   (change the dependency, or accept the floor); do not add a goroutine bridge without an ownership
   review, and do not add a health ticker.

## Deliberately not claimed

- The Mieru silent-blackhole floor is still the SDK's 10 s response read; no public API exposes a
  cancellation point there. A caller with a 500 ms budget still waits ~10 s (measured), although the
  returned error now carries the caller's own reason instead of only the SDK timeout. See
  FAULT-TIMELINE.md §Residual and the ledger's "not fixed" rows.
- No device evidence (iOS/Android/TUN), no real Wi-Fi↔cellular transition, no multi-hour soak.
- No `-race` run on this host.
- The target-refusal path is not reproducible on the loopback test inbound (its router fabricates the
  destination); that row relies on the prior real-mita evidence, and the classification is covered by
  unit tests only.
