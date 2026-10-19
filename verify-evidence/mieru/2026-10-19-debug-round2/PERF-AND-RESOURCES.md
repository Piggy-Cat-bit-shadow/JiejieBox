# Performance and resources (debug round 2)

Environment and SHAs are in [README.md](README.md). All numbers below were produced on this host in
this round; nothing is copied from the round-1 report except where explicitly labelled as a limit.

## Healthy path

| Metric | Value | Command |
|---|---|---|
| Per-dial observation ledger (Mieru new session) | **49 ns/op, 48 B/op, 1 alloc/op** (three runs: 48.97–49.48 ns) | `go test ./protocol/mieru -run '^$' -bench BenchmarkDialObservationOverhead -benchmem -count=3` — `logs/benchmarks.txt` |
| Selector choose+report | **27.56–27.74 ns/op, 0 alloc/op** | same command, `BenchmarkFallbackSelectorChooseReport` |
| MASQUE CONNECT-IP setup, real loopback H3 | **2.1–3.4 ms** | `TestH3TunnelSetupOverRealLoopbackIsFarInsideTheWindow` (logged) |
| New background goroutines / tickers in round-1/2 code | **none** | code audit; the only timer is the per-setup `context.WithTimeout` |

There is no per-packet change: the observation is created inside `transportRuntime.dial` for one SDK
session and becomes unreachable when it returns; the tunnel window exists for one setup attempt.

## Package matrix (this round, exit 0)

| Command | Result |
|---|---|
| `go test ./adapter ./common/dialer ./protocol/mieru ./protocol/group ./transport/http -count=1` | final tree: adapter 0.706 s, common/dialer 16.219 s, mieru **79.929 s** (standalone final suite 80.004 s), group 3.852 s, http 8.451 s — `logs/packages-untagged.txt`, `logs/mieru-suite.txt` |
| `go test -tags with_quic ./transport/http ./protocol/masque ./transport/masque -count=1` | final tree: http 25.036 s, protocol/masque 54.368 s, transport/masque 6.652 s — `logs/packages-quic.txt` |
| `go test -race ...` | **BLOCKED_BY_ENVIRONMENT** (no mingw-w64 gcc; `clang` MSVC target rejects `-mthreads`). Minimum condition in INVARIANT-MATRIX.md |

The full mieru suite is also run as part of the untagged matrix; the debug fixes added ~20
deterministic tests and did not change its 10 s-SDK-wait-dominated wall clock materially.

## Resources (measured with a scratch test, host-local)

| State | Goroutines |
|---|---|
| process baseline (settled) | 2 |
| one Mieru runtime, 20 streams dialed/closed, then outbound + inbound closed (settled) | **2** (delta 0) |
| fallback enabled (two SDK runtimes), 20 streams, then outbound + both inbounds closed (settled) | **2** (delta 0); packet conns `0/0` |
| a second start/dial/close cycle (settled) | **2** (no accumulation) |

Inbound shutdown regression: `TestMieruInboundCloseReleasesTheUDPUnderlayPromptly` runs 20
start/close cycles in **0.01 s**; rebind + prompt-close at `-count=20` each complete in **0.284 s**
(the pre-fix behaviour was a 65 s stall in 2/10 runs, reproduced by mutation).

## Load soak on the real SDK (scratch test, host-local)

1000 sequential short sessions through a fallback-enabled outbound (primary healthy, echo payload
each), then outbound and both test inbounds closed:

| Metric | Value |
|---|---|
| total | **1.673 s** |
| P50 / P95 / P99 per session (dial + echo + close) | **1.601 ms / 2.172 ms / 2.399 ms** |
| goroutines during the run / after Close | 150 / **2** (process baseline) |
| preference after the soak | primary, `PrimaryFailure = 0` |

Raw: `logs/soak.txt`. This is the round-2 replacement for the older "3000/3000 sessions" figure,
which is not restated as a current measurement.

## Failure-path timings

See MIERU-AUDIT.md for the baseline-vs-debug timeline. The short version: fast failures decide in
**~1 ms**; silent blackholes remain at the SDK's **~10.0 s** floor; the MASQUE tunnel fallback starts
after **3 s** instead of consuming the caller's 15 s.

## Regression surface

- No allocation, lock, goroutine or timer on the packet path; no change to Mux, UOT framing, UDP
  associate or capsule code.
- Disabled fallback: unchanged code path plus one per-session observation.
- Healthy fallback: one primary attempt, no secondary socket (asserted by the e2e test).
- Group/URLTest: only new input is the fact set; the group table is unchanged except one Local row.

## Limits

- `-race` unavailable (above).
- Windows only; Linux/macOS wall clocks not measured.
- No mobile device, TUN or real network transition; the goroutine numbers are process-local and do
  not predict battery behaviour.
- The 10 s SDK response floor and the 500 ms-caller wait are unchanged by design.
