# OOM killer idle release — root cause and fix (2026-10-24)

**START_HEAD:** `07edc609388b1a2c6d6291735ac567b99869f4bf`
**FINAL_HEAD:** the commit carrying this report
**BRANCH:** `testing`

## 1. Confirmed root cause

`service/oomkiller/timer.go` released idle memory when:

```text
not below resume  &&  >=1s since last release  &&  (goroutines/connections shrank || no GC since last release)
```

`FreeOSMemory()` itself runs a GC and the code then stores the new GC cycle count. On a stable
footprint with no other GC, `idleGC` is true again on the next tick, so the condition is
self-sustaining: one `TrimMemory` + `FreeOSMemory` per second at 33 MB forever.

The canonical NetworkExtension policy makes 33 MB eligible for this path: `resume = 30 MiB` (so
`belowResume` is false at 33) while `armed = 40 MiB`; the resume hysteresis boundary was being used
as the everyday admission threshold. The logs (`33 MB -> 33 MB`, 31 of 33 consecutive samples one
second apart) are the direct symptom; they do not by themselves prove zero bytes were released,
which is why the fix is about the trigger, not about lowering the floor.

## 2. Fix

`releaseIdleMemory` now requires an **observed opportunity**:

- a meaningful drop in goroutines or connections since the previous sample, or
- a footprint drop of at least 512 KiB since the previous sample, or
- real pressure (`usage >= armed` in limit/NE mode, or `available <= armed` in available mode).

A completed GC is no longer a trigger at all. After a release with less than 512 KiB of measured
gain, a 30-second low-yield cooldown suppresses ordinary repeats; an armed/triggered footprint
bypasses that cooldown. The release now records the real byte delta and says "no measurable gain"
rather than unconditionally "released idle memory". The idle path joins the existing
`pressureCallbacks` barrier (Add while holding `access` after the `timer != nil` check), so
`Close`/`Reload` cannot return while a trim/GC is still touching the network; a `releaseAccess`
mutex makes the idle cleanup single-flight.

No change was made to `TrimMemory`'s downstream behaviour, to the critical `ReleaseMemory` path, or
to the policy thresholds. Upstream `9c2aa1b` removes the `resume` field from the state machine; that
is a different change and was not adopted here — the iOS crisis hysteresis is a safety contract, and
the bug was its misuse as an everyday admission gate.

## 3. Tests

`service/oomkiller/idle_release_test.go`:

| test | assertion |
|---|---|
| `TestIdleReleaseDoesNotRepeatAtAStableFootprint` | five samples at 33 MB, no drop, no pressure → zero trims, zero forced GCs |
| `TestIdleReleaseRunsOnceForARealDropThenCoolsDown` | 35 → 33 MB → exactly one release; a second 33 MB sample is suppressed by the cooldown |
| `TestIdleReleasePressureBypassesTheCooldown` | after a no-gain release, an armed 41 MB sample still releases (pressure overrides the ordinary cooldown) |

`go test ./service/oomkiller` passes. The package's existing Darwin policy/lifecycle tests also pass
unchanged. `-race` was not run on this host for this package (CGO/mingw setup); CI runs the race
surface for the fork's data paths, not this service. That is a limitation, not a PASS.

## 4. Limitations

- No iOS device: the fix is verified by deterministic policy tests, not by a live NetworkExtension
  CPU/power trace. `DEVICE_VALIDATION = NOT_VERIFIED_ON_DEVICE`.
- No A/B throughput/power measurement; the claim is only "the no-op loop is gone and a real
  opportunity still releases".
- `TrimMemory`'s effect on DNS/HTTP/QUIC pools is unchanged; this round does not attempt partial
  cache trimming.
- The critical `ReleaseMemory` path and its own rate limiting were not modified.

```text
OVERALL = DONE_WITH_LIMITATIONS
BRANCH = testing
ROOT_CAUSE = idleGC (no GC since last release) used as a sufficient trigger; FreeOSMemory itself runs the GC, so the loop is self-sustaining at a stable footprint above resume
NEW_ARCHITECTURE = observed-opportunity trigger + 512 KiB benefit floor + 30 s low-yield cooldown + pressure override + lifecycle barrier
REGRESSION_33MB = PASS (deterministic)
PRESSURE_SAFETY = PASS (armed bypasses cooldown)
LIFECYCLE_RACE = PASS (barrier added; race not run locally)
MULTIPLATFORM = Windows tests pass; Darwin/Apple tests pass; iOS device NOT_VERIFIED
NETWORK_REGRESSION = no TrimMemory semantics changed
BENCHMARK_EVIDENCE = none; no device power/CPU trace
DEVICE_VALIDATION = NOT_VERIFIED_ON_DEVICE
UNRESOLVED = live iOS CPU/power measurement; partial cache trimming was not attempted
```
