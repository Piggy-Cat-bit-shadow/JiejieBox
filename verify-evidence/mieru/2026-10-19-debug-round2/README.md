# Debug round 2 — adversarial audit of the round-1 failure-fact work

Directive: `02-testing-BBE48095E99CA2E5A4B8E493A1E690A4E88D.md`. Repository
`Piggy-Cat-bit-shadow/sing-box`, branch `testing`, no new branch.

## 1. Final state

| | |
|---|---|
| Pre-construction baseline | `c363e04bb3065de68e2123182013124cbb65a900` |
| Round-1 final (debug start) | `a003732e8668e3ca1148290acc925df6f061833c` (10 commits) |
| Round-2 code + tests | `521e2e69b62a45842a60effeb0e657fadbc92d04` |
| Round-2 evidence | this commit (parent `521e2e69b62`) |
| Branch / remote | `testing`; pushed without force to `origin/testing`; no concurrent update observed (`git fetch` before push) |
| Worktree | clean after the evidence commit |
| Audited range | `git diff c363e04..a003732e` (round-1 changes) and the round-2 fixes on top |

## 2. Conclusion matrix

| Class | Finding | Evidence |
|---|---|---|
| **Introduced by round 1, fixed now** | Aggregate facts used the secondary class; a fact-less soft class erased a proven primary failure, and the stage was wrong for definite+handshake | BUG-R2-001; four failing rows before the fix |
| **Introduced by round 1, fixed now** | Local failures (DNS/permission/stopped client/local interface) were published as `FailureOriginCaller` | BUG-R2-002 |
| **Introduced by round 1, fixed now** | Our own teardown error could be classified transport, so a dial racing Close could switch/publish Path facts | BUG-R2-003 |
| **Pre-existing, found during round-1 settlement and re-audited here** | Mieru inbound `Close` could stall 65 s on the SDK's 60–120 s UDP read | inbound M9 (round-1 commit); test + mutation re-run this round |
| **Existing behaviour verified unchanged** | Disabled fallback, single TCP/UDP, `no_wait` conflict, target refusal, hashed password, multi-user, H3 backoff/ordering, strict mode, pinned version, MASQUE session lifecycle | package matrix + the specific tests listed in MIERU-AUDIT / MASQUE-AUDIT |
| **Not changed, with measured limits** | SDK 10 s response floor (500 ms caller waits 10.002 s); client-side packet-underlay close analogue (75 iterations, no stall); TLS errors wrapped as `ErrHTTP3Unavailable` (same TLS config on H2, no bypass) | MIERU-AUDIT, OUT-OF-SCOPE-NOTES |

## 3. Fixed bugs

Full detail and before/after evidence: [BUG-LEDGER.md](BUG-LEDGER.md).

- **BUG-R2-001** — `aggregateFailureClass` (in `protocol/mieru/fallback.go`, used by
  `outbound.go:dialWithFallback`). Fix validated by `TestAggregateFailureFactsPrecedence`.
- **BUG-R2-002** — `dialLocalFailure` class with `Dial/Local` facts; group table row added.
- **BUG-R2-003** — lifecycle message checked before the SDK underlay prefix;
  `TestLifecycleClosureIsLocalNotTransport`.

## 4. Architecture boundary review

Full chains, ownership and "who may overwrite" table: [BOUNDARY-AND-CALLCHAIN.md](BOUNDARY-AND-CALLCHAIN.md).
Summary: no new interface or state machine was introduced by the debug round. Facts are produced once
at the outbound boundary, `FailureFacts` stays immutable per attempt, the Mieru selector owns its
preference/cooldown per generation, the HTTP window is owned by `transport/http` and only changes when
the tunnel fallback starts, and MASQUE still owns its session. Close/epoch guards are unchanged and
were re-tested.

## 5. Mieru data

[MIERU-AUDIT.md](MIERU-AUDIT.md). Same-injection baseline vs debug tree:

- refused: **1 ms**, baseline `transport/Handshake/Path` → now `socket/Dial/Path`;
- accept-silent 10.003 → **10.002 s**; UDP blackhole 10.003 → **10.002 s**; wrong password 10.002 →
  **10.001 s** (the SDK floor, unchanged);
- fallback after refusal **1 ms**; after blackhole **10.002–10.003 s**;
- caller 500 ms budget: still **10.002 s**, but the error now carries
  `context deadline exceeded` (baseline did not) and the preference is unchanged;
- both-definite outage no longer double-dials (5 requests → 1 primary + 5 secondary, not 5+5);
- recovery: one probe per window at 10/50/100 concurrent requests; cooldown 30 → 60 → 120 → 240 →
  300 s (capped), a cancelled probe does not move it.

## 6. MASQUE data

[MASQUE-AUDIT.md](MASQUE-AUDIT.md). The CONNECT-IP H3 attempt now has its own 3 s window
(`http3TunnelEstablishTimeout`), separate from the stream `http3EstablishTimeout`; baseline blocked
indefinitely (still blocked after 3 s) because the only bound was the caller's 15 s. Real loopback
setup is 2.1–3.4 ms; strict mode, caller cancellation, application refusals, `TunnelTransport`
reporting and the post-200 write side are covered by tests; the tunnel really carries payload.

## 7. Resources and performance

[PERF-AND-RESOURCES.md](PERF-AND-RESOURCES.md).

- Package matrix (exit 0): adapter 0.811 s, common/dialer 16.399 s, mieru 79.476 s (final-tree suite
  82.339 s), group 4.023 s, http 8.508 s; with_quic: http 25.182 s, protocol/masque 57.317 s,
  transport/masque 7.050 s.
- Observation ledger: 49 ns / 48 B / 1 alloc per new session; selector 27.6 ns / 0 alloc.
- After closing both SDK runtimes and the inbound: goroutines back to the process baseline (2),
  packet conns 0/0, no accumulation over a second cycle.
- Inbound close regression: 20 start/close cycles in 0.01 s (pre-fix 65 s in 2/10).
- `-race` is BLOCKED_BY_ENVIRONMENT (no mingw-w64 gcc); minimum condition and command in
  [INVARIANT-MATRIX.md](INVARIANT-MATRIX.md).

## 8. Independent findings

- The aggregate-fact loss (BUG-R2-001) is the one a cross-protocol audit was most likely to miss: it
  is invisible in single-transport tests and only appears when a *soft* class meets a *proven* one.
- BUG-R2-003 shows the round-1 bridge still had one text-matched path where the SDK prefix won over our
  own lifecycle message; the bridge now owns that classification too.
- The 143 s suite runs in round 1 were not noise: they were the inbound shutdown defect
  (65 s), located with a goroutine dump rather than by widening a timeout.
- Deliberately **not** implemented: a per-dial goroutine to abandon the SDK handshake on caller
  cancellation (ownership hazard, documented); an outbound teardown reordering without a measured
  stall; any new health probe/ticker; any `FailureFacts` from TLS/QUIC/HTTP layers (no proof available).

## 9. Not verified (stated, not hidden)

- `-race` (environment); iOS/Android device, TUN, real cellular/Wi-Fi transition, real GFW blackhole.
- Linux/macOS wall-clock behaviour (Windows host this round); Linux UDP bare-EOF remains covered by the
  existing unit classification.
- A slow CONNECT-IP response on an **already live** H3 connection taking > 3 s (arms the H3-broken
  memory once); it is a documented trade-off, not a security path.
- Target-refusal timing on loopback is not reproducible (the test router fabricates the destination);
  the 3 ms figure is prior real-mita evidence, labelled as such.

## 10. Commands used

```pwsh
go test ./adapter ./common/dialer ./protocol/mieru ./protocol/group ./transport/http -count=1
go test -tags with_quic ./transport/http ./protocol/masque ./transport/masque -count=1
go test ./protocol/mieru -run '^$' -bench 'BenchmarkDialObservationOverhead|BenchmarkFallbackSelectorChooseReport' -benchmem -count=3
go test ./protocol/mieru -run 'TestAggregateFailureFactsPrecedence|TestReportBothFailedDecisionTable|TestFallbackCooldownDoublesToTheCeiling' -count=1 -v
go test ./transport/http -tags with_quic -run 'TestH3Tunnel' -count=1 -v
go vet ./protocol/mieru ./protocol/group ./transport/http ./adapter ./common/dialer
```

Raw logs are in `logs/`:

| file | content |
|---|---|
| `packages-untagged.txt` | the untagged matrix |
| `packages-quic.txt` | the with_quic matrix |
| `mieru-suite.txt` | final-tree mieru suite (82.339 s) |
| `benchmarks.txt` | the two benchmarks, 3 runs each |
| `resources.txt` | goroutine/FD-relevant resource scratch run |
| `fault-timeline.txt` / `fault-timeline-baseline.txt` | same-injection timeline on the debug and baseline trees |
