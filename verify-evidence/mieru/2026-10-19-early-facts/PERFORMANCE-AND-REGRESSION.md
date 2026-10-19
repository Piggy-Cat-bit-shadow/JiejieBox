# Performance and regression

## Environment

| | |
|---|---|
| Host | Windows 10 Enterprise LTSC 19044, AMD Ryzen 7 4800H (8 cores / 16 threads), 15.4 GiB |
| Toolchain | `go1.25.5 windows/amd64`, `CGO_ENABLED=0` (no mingw-w64 gcc; `-race` is BLOCKED, see §Limits) |
| Branch / baseline | `testing` @ `c363e04bb3065de68e2123182013124cbb65a900` |
| Evidence run | 2026-10-19, this working tree |

## What the change costs

The changed code runs on exactly two paths, and on neither of them per packet:

| Path | Frequency | Cost |
|---|---|---|
| Mieru new SDK session (`transportRuntime.dial`) | once per new Mieru session | one context wrapper (48 B, 1 alloc), one mutex-protected record, one read — measured **43.75–45.95 ns/op, 48 B/op, 1 alloc/op** over 3 runs (`BenchmarkDialObservationOverhead`) |
| MASQUE CONNECT-IP setup (`openTunnel`) | once per tunnel establish/reconnect | one child context with a timer (`context.WithTimeout`), cancelled as soon as the H3 attempt returns. No goroutine, no socket, no packet. |
| Mieru selector | once per new session | unchanged: **27.54–28.37 ns/op, 0 alloc/op** (`BenchmarkFallbackSelectorChooseReport`, 3 runs) |

The packet path, the Mux, the UOT framing, the UDP associate wrapper and the MASQUE capsule path were
not modified. That is why the previous round's packet benchmarks are not reused as evidence here: they
would not exercise a single changed instruction.

Existing-session traffic pays nothing: the observation is created inside `DialContext`/`ListenPacket`
for a new session and is unreachable afterwards.

## Measured failure timelines (see FAULT-TIMELINE.md for the table)

| Scenario | Before / without this round's evidence | Now |
|---|---|---|
| Mieru TCP refused | ~1 ms, classified from text (not provable when localised) | ~1 ms, typed `socket`, definite, Dial/Path fact |
| Mieru TCP accept-silent | 10.002 s (SDK read deadline; floor unchanged) | 10.002 s, `transport`, fallback |
| Mieru UDP blackhole | 10.004 s (floor) | 10.004 s, `transport`, fallback |
| Mieru wrong password | 10.002 s, ambiguous-or-timeout depending on the platform text | 10.002 s, `transport`; both-silent still resets to primary |
| Fallback refused → live | 1 ms (already fast) | 1 ms |
| Fallback blackhole → live | 10.002 s (already correct) | 10.002 s |
| MASQUE CONNECT-IP, H3 silent blackhole | >3 s with no return and **no H2 attempt** (baseline worktree: still blocked after 3 s, `h1.calls=0`; bounded only by the caller's 15 s, which then returned the caller's deadline) | 3 s to the H2 attempt, 12 s of caller budget left |
| MASQUE CONNECT-IP healthy, loopback | 3.4 ms setup | 3.4 ms setup (window never engaged, `TestH3TunnelSetupOverRealLoopbackIsFarInsideTheWindow`) |

## Regression surface and why it is small

- **Fallback disabled:** one runtime, the selector is nil, the code path in `dialWithFallback` is not
  entered. The only difference is the per-session observation described above, which also gives
  single-transport outbounds a typed Dial/Path fact.
- **Fallback enabled, primary healthy:** the selector's `choose`/`report(success)` behaviour is
  unchanged (benchmarked), and no socket is opened on the secondary until a transport-class failure.
- **Mieru lifecycle:** `Start`, `Close`, `InterfaceUpdated`, the drain counter, the packet-conn registry
  and the two SDK generations are untouched. The observation object is not shared across generations.
- **MASQUE strict mode / pinned version:** the window obeys the existing `disable_version_fallback`
  branch before any fallback, and the "pinned HTTP/2 config never constructs H3" test still passes.
- **Application errors:** a CONNECT refused with an HTTP status is still returned as-is (test), so the
  tunnel does not move to another version for a server-side policy decision.

## Commands and results

Raw output: `logs/`.

| Command | Result |
|---|---|
| `go test ./protocol/mieru/ -count=1` (before the change) | `ok ... 77.602s` |
| `go test ./protocol/mieru/ -count=1` (after) | `ok ... 78.027s`; the suite's wall clock is dominated by fixed 10 s SDK waits. The earlier bimodal 79 s / 143 s runs were **not** noise: they were the inbound shutdown defect described below, worth exactly 65 s when it fired |
| `go test ./transport/http/ -tags with_quic -count=1` (after) | `ok ... 24.966s` |
| `go test ./transport/http/ -tags with_quic -run TestH3Tunnel -v` | 9 tests pass, including the real loopback setup (2.1–3.4 ms) and a payload round trip through the returned tunnel |
| `go test ./protocol/mieru/ -run '^$' -bench ... -benchmem -count=3` | see the table above |
| `go test -race ./protocol/mieru` | **not run** — build failure `clang: unsupported option '-mthreads'`; no mingw-w64 gcc on this host |
| `go vet ./protocol/mieru ./transport/http` | clean |
| `gofmt -l protocol/mieru transport/http` | no output |

Per-test profile of the slowest cases (from a JSON run, so the outlier above can be attributed):
wrong password 20.00 s, UDP-blackhole→TCP 10.01 s, no-wait wrong password 10.00 s,
TCP-blackhole→UDP 10.00 s, UDP-associate blackhole 10.00 s, close-during-dial 6.92 s — all pre-existing
fixed waits. Every test added this round completes in under 0.1 s except the two real-SDK ones
(refused endpoint and cancelled caller), which are bounded by their own short deadlines.

## Inbound shutdown: the 65 s that made the suite bimodal

The JSON run also showed `TestMieruInboundCanRebindAfterClose` at **65.00 s** (normally 0.03 s). The
goroutine dump from `go test -timeout 25s` put the test in
`Mux.Close → serverUnderlayLoopWG.Wait()`, and the blocked loop in
`PacketUnderlay.readOneSegment → ReadFrom` with the SDK's 60–120 s deadline.

Root cause and fix: see the ledger M9 and the design study #9. The short form is that
`PacketUnderlay.Close` only wakes that read and then closes sessions before closing `done`, so the
woken loop can re-arm the deadline; closing the `PacketConn` the fork owns, before `server.Stop()`,
makes the read return `net.ErrClosed` immediately.

Evidence:

| Item | Before | After |
|---|---|---|
| `TestMieruInboundCanRebindAfterClose` ×10 | 65.00 s in 2/10 runs | 20× in 0.284 s (with the new test) |
| New `TestMieruInboundCloseReleasesTheUDPUnderlayPromptly` (20 cycles) | n/a | 0.01 s |
| Mutation (old ordering restored) | — | test fails at cycle 2: `Close took 1m5.0005327s` |
| Full suite | 79 s or 143 s, depending on the dice | ~79–80 s |

## Mutation checks (the tests fail for the right reason)

| Mutation | Test result |
|---|---|
| Disable the both-definite early return in `reportBothFailed` | `TestDefiniteSocketFailureOnBothTransportsDoesNotDoubleDial` fails: expected `primary.dials = 1`, actual **5** |
| Ignore the observation in `classifyDialError` | `TestTypedSocketEvidenceAddsWhatTextCannotExpress` fails (class 1 = transport instead of 5 = socket); `TestLocalInterfaceFailureIsNotAPathFailure` fails (1 instead of 3 = caller); `TestRealRefusedTCPIsADefiniteDialFailure` fails (1 instead of 5) |
| Baseline `c363e04` without the tunnel window (worktree `_before-tunnel`) | the new tunnel scenario is still blocked after 3 s with `h1.calls = 0`; the H2 branch is unreachable |

## Limits

- **`-race` is BLOCKED_BY_ENVIRONMENT.** The host has no mingw-w64 gcc; Go's Windows cgo driver passes
  `-mthreads`, which the only available `clang` (MSVC target) rejects. Minimum condition to resume:
  install `mingw-w64-x86_64-gcc`, then
  `CGO_ENABLED=1 CC=gcc go test -race ./protocol/mieru ./transport/http`.
- **Windows only.** The Linux UDP bare-EOF path is covered by the existing unit classification and was
  not re-measured here. No claim is made about macOS/Linux wall-clock behaviour.
- **No device evidence.** No TUN, no real Wi-Fi↔cellular transition, no sleep/wake, no multi-hour soak.
  No new background work was added, so there is no new power surface to validate beyond the existing
  ones.
- **Loopback only.** The "remote blackhole" is simulated with a local blackhole listener / absent UDP
  listener, not a real censored path.
- **Target refusal** was not reproducible with the loopback test inbound (its router fabricates the
  destination); that row uses the prior real-mita evidence for its 3 ms number and unit tests for the
  classification.
- Numbers from the previous round's environment (selector 26.6 ns, idle +0.25 MiB, 3000/3000 sessions)
  are **not** restated as this round's measurements; the benchmarks above were re-run here.
