# WireGuard macOS CI flaky tests — fix record

Baseline: `v0.1.23` / `2a2b3cc2635a6fc1470576cf8a092644e7e59be3` on `testing`.
Scope: `transport/wireguard` tests only. **No product code was changed.**

```text
RESULT                    = DONE (see §8 for the macOS evidence)
ROOT CAUSE A              = TEST ABBA deadlock in
                            TestPauseRegistrationDoesNotRunUnderTheStateLock
ROOT CAUSE B              = TEST expectation latched a transient census reading (1)
                            while the settled standard bind holds 2 on macOS
PRODUCT DEFECT FOUND      = NO
```

## 1. Baseline failures

All three runs are the same commit `2a2b3cc`:

| run | result | failure |
|---|---|---|
| [39535497031](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39535497031) | FAIL | `panic: test timed out after 25m0s`, `TestPauseRegistrationDoesNotRunUnderTheStateLock (24m55s)` |
| [39538351175](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39538351175) | FAIL | `TestAdvGItem7OccupiedPortIsInvisibleWhileTheReopenIsInFlight (15.01s)`, `SOCKETS_PER_STANDARD_BIND=1` |
| [39540368720](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39540368720) | PASS | — |

### 1.1 A: the recorded stack is the cycle itself

```text
goroutine 1071:
github.com/sagernet/sing-box/transport/wireguard.(*fakePauseManager).callbackCount
    transport/wireguard/pause_deadlock_test.go:104
TestPauseRegistrationDoesNotRunUnderTheStateLock
    transport/wireguard/pause_deadlock_test.go:225

goroutine 1069 [chan receive, 24 minutes]:
github.com/sagernet/sing-box/transport/wireguard.holdStateLock.func1
    transport/wireguard/pause_deadlock_test.go:174

github.com/sagernet/sing-box/transport/wireguard.(*Endpoint).onPauseUpdated
    transport/wireguard/endpoint.go:800
github.com/sagernet/sing-box/transport/wireguard.(*fakePauseManager).emit
    transport/wireguard/pause_deadlock_test.go:99
```

Three test goroutines, three locks in a ring:

```text
holder goroutine   holds stateAccess, waiting for releaseCh
emit goroutine     holds pause.access, waiting for stateAccess (onPauseUpdated)
test goroutine     wants pause.access (callbackCount), and only it can close releaseCh
```

The ring closes **inside the test**. The production registration path was not part of it; the test
manually held the state lock and registered from the test goroutine.

### 1.2 B: the recorded numbers

```text
--- FAIL: TestAdvGItem7OccupiedPortIsInvisibleWhileTheReopenIsInFlight (15.01s)
SOCKETS_PER_STANDARD_BIND=1 (established on this machine)
ADVG_ITEM7_PINNED port=61130 census=0 port_occupied=true
advg_item7_census_probe_test.go:116: Condition never satisfied
the released wake must settle on one bind's worth of receivers
```

The pinned half succeeded (`census=0` while the OS says the port is occupied). The failure is the
post-wake equality assertion, and the log did not say what was observed instead.

## 2. Root cause A — test-side lock cycle

`fakePauseManager.emit` holds `access` while it calls callbacks inline, exactly like
`service/pause.defaultManager`. `Endpoint.onPauseUpdated` takes `stateAccess`. The old test:

1. started `manager.DevicePause()` on a goroutine,
2. put `endpoint.stateAccess` in a helper goroutine that waits for the test to release it,
3. registered callbacks from the test goroutine,
4. called `manager.callbackCount()` **before** releasing `stateAccess`.

If the emit reached `onPauseUpdated` first it held `pause.access` and waited for `stateAccess`; the
test then waited for `pause.access` in `callbackCount`; the holder waited for the test. The test
binary cannot recover from that, so `go test` reports it only at the 25-minute timeout.

The same test had a false-negative branch: if the registration won the race before the emit
started, it completed and asserted nothing about the dangerous interleaving.

## 3. Root cause B — the expectation was a transient reading

`socketsPerStandardBind` is measured per process and compared with exact equality. The measurement
waited for the first **non-zero** census sample and recorded *that* value. On the macOS runner the
two receive goroutines become visible to `runtime.Stack` a few milliseconds apart, so the first
non-zero reading is 1 while the settled bind holds 2.

Measured on the macOS arm64 runner by the temporary diagnostic (run
[39558915059](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39558915059), commit
`ea1b75f`, first test in the package):

```text
fixture 1: after Start: histogram=map[1:1 2:127]
fixture 1: after Start:   value=1 at=125ns (held for 1.864916ms, 1 samples)
fixture 1: after Start:   value=2 at=1.865041ms (held until the end, 127 samples)

fixture 2: after Start: histogram=map[0:1 2:116]
fixture 2: after Start:   value=0 at=375ns (held for 2.705417ms, 1 samples)
fixture 2: after Start:   value=2 at=2.705792ms (held until the end, 116 samples)
```

The same run proved the bind itself is healthy and has both sockets:

```text
fixture 0: bind = &{... ipv4:0x14000048420 ipv6:0x14000048430 ...}   (both non-nil)
fixture 0: after Wake: histogram=map[2:948]                          (settled, stable for 948 samples)
fixture 0: post-wake port=55273 held=true socketsPerStandardBind=2
fixture 0/1/2: markerGoroutines matched_device=2 unaddressable=0 truncated=false
```

So, on this machine:

* the standard bind has two sockets (`ipv4` and `ipv6` both non-nil, `ipv6Available=true`);
* the census can legitimately read **0 or 1** for the first few milliseconds after `Start`;
* the settled reading, before and after a wake, is **2**;
* the old establishment recorded exactly the transient 1 from that window and then compared a
  correct 2 against it for 15s.

The recorded `SOCKETS_PER_STANDARD_BIND=1` on the failing run is therefore a measurement bug, not a
machine property: the comment that said "macOS full set is 1" was describing the transient sample.
No lifecycle anomaly is evidenced: the wake returned, the port stayed held by the endpoint, and the
receivers settled on both sockets.

## 4. Changes

| file | change | why it does not weaken the assertion |
|---|---|---|
| `transport/wireguard/pause_deadlock_test.go` | `TestPauseRegistrationDoesNotRunUnderTheStateLock` now drives the **real `Start`** path. A test hook inside `fakePauseManager.RegisterCallback` blocks Start at the registration site; while Start is there the test asks `stateAccess.TryLock()` (with a free-lock positive control before Start and a `t.Cleanup` release registered before the assertions). `TestCloseUnregistersWithoutHoldingTheStateLock` applies the same probe to Close's `UnregisterCallback`, twice. The old storm test is replaced by `TestCloseRacingPauseEmissionsLeavesNoCallback`, whose callback takes `stateAccess` with a **bounded** wait and records a starved lock instead of blocking forever. | The order is now observed directly on the production entry points; a regression fails immediately with the exact path named, instead of deadlocking. Equality on the callback count is unchanged, and `TestStartRegistersThePauseCallbacks` / `TestCloseUnregistersTheCallbacksStartRegistered` still cover the real Start→Close callback pair. |
| `transport/wireguard/rebind_lease_test.go` | `measuredSocketsPerStandardBind` now samples a **settling window** (`minimum`, then quiet) and records the maximum; the memo keeps the maximum across fixtures, still bounded to 1..2, and stores the observation trace and establishment time. `settleSocketsPerStandardBind` goes through the shared waiter. The goroutine dump grows to 256 MiB instead of silently truncating at 4 MiB. `socketGate.state()` exposes armed/attempts/inFlight for diagnostics. | Still an exact equality against a measured value; still refuses 3+; still fails when the census never reaches the value (now with the distribution in the message). The bound `census <= socketsPerStandardBind` is untouched. |
| `transport/wireguard/advg_item7_census_probe_test.go` | the failing post-wake assertion uses `awaitSocketCensus` with 25 ms polling and a one-shot failure diagnostic; the expected value is captured once from the fixture's own proved branch. The pause precondition and the sibling/reverse tests use the same waiter. The `ADVG_ITEM7_PINNED` line now records the expectation, port probe error and gate state. | The 15s deadline is unchanged, the value is still exact, and `census == 0` in the pinned window is still asserted; only the evidence printed on failure and the polling pressure changed. |
| `transport/wireguard/census_await_test.go` (new) | the waiter, the failure-side receiver dump, the port-probe error helper, and deterministic tests of the settling observation (1→2 is kept; stable 1 is valid; never-nonzero is 0). | Tests of the instrument itself; the 1→2 test fails if the old first-nonzero behaviour returns. |
| `transport/wireguard/listen_port_test.go` | `udpPortIsFree` delegates to the error-returning probe. | Same boolean semantics; the diagnostics can now distinguish "occupied" from "probe failed". |

No `endpoint.go`, socket lifecycle, pause callback semantics, or workflow file is changed in the fix
commit.

### 4.1 Why the old lock tests could not be kept as they were

A test that forms the real ABBA ring cannot also be the gate that detects it: once formed, neither
lock can be released and no cleanup can run. The replacement establishes the same happens-before with
an explicit channel barrier and observes the lock state with `TryLock`; the concurrency scenario
(emit in flight while Close unregisters) is kept by the bounded callback probe, which **fails** the
test in ~4s under the pre-fix order and unwinds normally.

## 5. Negative controls

Each control was applied in the working tree, run, and reverted; `git status` was clean afterwards.

| control | test | observed result |
|---|---|---|
| Registration moved back inside `stateAccess` (pre-fix Start) | `TestPauseRegistrationDoesNotRunUnderTheStateLock` | FAIL in 0.00s: "Start called RegisterCallback while holding stateAccess…" |
| Unregistration moved back inside `stateAccess` (pre-fix Close) | `TestCloseUnregistersWithoutHoldingTheStateLock` | FAIL in 0.00s: "Close called UnregisterCallback while holding stateAccess…" |
| Same pre-fix Close order | `TestCloseRacingPauseEmissionsLeavesNoCallback` | FAIL in 4.00s: "a callback could not take stateAccess for a full second…", process exits normally |
| Settling observation reduced to first-non-zero (pre-fix measurement) | `TestSettlingObservationKeepsTheReceiverThatArrivesLate` | FAIL in 0.00s: expected 2, actual 1, trace `sequence=[1@0s]` |
| One real `Start` registration deleted (the `onDeviceWake` registration) | `TestStartRegistersThePauseCallbacks` / `TestCloseUnregistersTheCallbacksStartRegistered` | FAIL in 0.00s: "Start did not register onDeviceWake…" and callback count 1, not 2 |

## 6. Local verification (Windows/amd64, Go 1.25.5, CGO disabled)

`release/DEFAULT_BUILD_TAGS` is the Darwin profile; local runs used
`release/DEFAULT_BUILD_TAGS_OTHERS` (the repository's rule for non-Darwin), and the untagged package
run for the plain fixture tests.

```text
go vet ./transport/wireguard                                             PASS
gofmt -l transport/wireguard                                             (empty)
go test -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -count=1 ./transport/wireguard
                                                                         ok 9.06s
go test -tags ... -count=3 ./transport/wireguard                          ok 27.04s
go test -tags ... -count=5 ./transport/wireguard                          ok 44.62s
go test -tags ... -shuffle=on -count=1 ./transport/wireguard              ok 9.17s
go test -count=50 -run 'TestPauseRegistration…|TestCloseUnregisters…|TestCloseRacing…'
                                                                         ok 0.09s
go test -tags ... -count=30 -run TestAdvGItem7OccupiedPortIsInvisibleWhileTheReopenIsInFlight
                                                                         ok 2.03s
```

`-race` on this host is **BLOCKED_BY_ENVIRONMENT**: the only C compiler is a clang targeting
`x86_64-unknown-windows-msvc`, and cgo fails on `-mthreads`; the repository's race evidence must come
from macOS. The temporary verification branch runs it there (§7).

## 7. macOS evidence

### 7.1 Normal fast-path runs (this fix)

Every dispatch below used `client-macos.yml` with `macos-latest`, `GOOS=darwin`, `GOARCH=arm64`,
`CGO_ENABLED=1`, and `release/DEFAULT_BUILD_TAGS`; the head SHA is the fix commit
`8d5d715dada314e604795fa991cb39610ac7bb76`.

| attempt | run | WireGuard package | whole gate | gate failure (if any) |
|---|---|---|---|---|
| R1 | [39560791778](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39560791778) | **ok 9.58s** | FAIL | `common/trafficsched` `TestPacedModeShapesFromTheFirstWrite` (113.1ms vs 102.4ms) — documented timing-sensitive flake, unrelated package |
| R2 | [39562613204](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39562613204) | **ok 10.27s** | **SUCCESS** | — (headless smoke, capability 56/56, summary all passed) |
| R3 | [39564924106](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39564924106) | **ok 9.69s** | **SUCCESS** | — |
| R4 | [39566770740](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39566770740) | **ok 9.69s** | **SUCCESS** | — |

R2, R3 and R4 are three independent dispatches of the unmodified fast-path workflow on the fix
commit; each ran `unit tests (macOS tags)`, the real core build, the architecture/version/tag check,
the headless smoke test, the full capability check (56/56) and the summary step. R1 is kept in the
table because it failed, and it failed in an unrelated package.

The WireGuard package is the unit under test. It passed in every run listed; the gate failures were
in other packages and are pre-existing, documented load/timing flakes:

* `protocol/group` `TestRecheckSingleFlightCollapsesABurst` is recorded as load-sensitive in
  `KNOWLEDGE/modules/ci-release.md` and `KNOWLEDGE/ACCEPTANCE.md` (20 consecutive isolated runs clean
  on both the fix tree and the untouched base).
* `common/trafficsched` wall-clock assertions are recorded in
  `docs/fork/v016-release-closure-report.md` as failing under full-suite parallelism on a loaded
  host (they pass 3/3 in isolation at load 13 and failed at load 56).

These are not treated as green: the runs above are reported as failures, and the 3-run requirement is
only satisfied by full-gate passes. No unrelated test was touched, skipped, or retried inside a run.

### 7.2 Stress and race (temporary branch, not for merge)

`verify/wg-macos-stress` = the fix commit plus two temporary workflow steps that run **before** the
package gate (so an unrelated package's flake cannot hide them):

```text
go test -tags <macOS tags> -count=100 -run '^TestPauseRegistrationDoesNotRunUnderTheStateLock$' ...
go test -tags <macOS tags> -count=30  -run '^TestAdvGItem7OccupiedPortIsInvisibleWhileTheReopenIsInFlight$' ...
go test -race -tags <macOS tags> -count=10 -run '<the four pause/census tests>' ...
```

| run | head SHA | result |
|---|---|---|
| first attempt [39560767304](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39560767304) | `c52b8ce` | WireGuard **ok 9.89s**; gate failed before the temp steps in `protocol/group`; no stress evidence |
| [39562601824](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39562601824) | `984b439` | **SUCCESS** — stress and race steps passed, then the whole gate passed |

Measured on that successful run (`darwin/arm64`, `CGO_ENABLED=1`, the macOS tag set):

```text
go test -count=100 -run '^TestPauseRegistrationDoesNotRunUnderTheStateLock$'   ok 0.049s
  STRESS PASS: pause lock-order test, 100 consecutive runs
go test -count=30  -run '^TestAdvGItem7OccupiedPortIsInvisibleWhileTheReopenIsInFlight$'
                                                                                ok 2.162s
  STRESS PASS: reopen-window census test, 30 consecutive runs
go test -race -count=10 -run '<four pause/census tests>'                        ok 2.209s
  RACE PASS: wireguard pause/census tests, 10 runs under -race
unit tests (macOS tags)                                                         PASS
  ok github.com/sagernet/sing-box/transport/wireguard 9.528s
```

The temporary branch and its workflow commit were deleted after the run; they are not part of the
fix.

### 7.3 Diagnostic run (temporary, not part of the fix)

[39558915059](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39558915059), commit
`ea1b75f`, is the measurement quoted in §3. It intentionally fails at the end so its log is printed
without `-v`; the `TestAdvGItem7…` failure listed after it is the baseline test failing its *pause*
precondition in that run (not the post-wake assertion), which the fix's diagnostics would report in
detail if it recurs. The WireGuard package passed `ok` in every fix-branch gate run above.

### 7.4 Consolidated evidence table

| platform | arch | Go | tags | CGO | command | repeats | result | time | run / SHA |
|---|---|---|---|---|---|---|---|---|---|
| Windows | amd64 | 1.25.5 | `DEFAULT_BUILD_TAGS_OTHERS` | 0 | `go test ... ./transport/wireguard` | 5 | PASS | 44.62s | local |
| Windows | amd64 | 1.25.5 | `DEFAULT_BUILD_TAGS_OTHERS` | 0 | `go test -count=50 -run '<pause lock-order tests>'` | 50 | PASS | 0.09s | local |
| Windows | amd64 | 1.25.5 | `DEFAULT_BUILD_TAGS_OTHERS` | 0 | `go test -count=30 -run '^TestAdvGItem7…$'` | 30 | PASS | 2.03s | local |
| Windows | amd64 | 1.25.5 | `DEFAULT_BUILD_TAGS_OTHERS` | 0 | `go test -race ...` | — | **BLOCKED_BY_ENVIRONMENT** | — | no mingw toolchain (`clang: unsupported option '-mthreads'`) |
| macOS | arm64 | 1.25.5 | `release/DEFAULT_BUILD_TAGS` | 1 | `go test -count=100 -run '^TestPauseRegistration…$'` | 100 | PASS | 0.049s | [39562601824](https://github.com/Piggy-Cat-bit-shadow/sing-box/actions/runs/39562601824) / `984b439` |
| macOS | arm64 | 1.25.5 | `release/DEFAULT_BUILD_TAGS` | 1 | `go test -count=30 -run '^TestAdvGItem7…$'` | 30 | PASS | 2.162s | 39562601824 / `984b439` |
| macOS | arm64 | 1.25.5 | `release/DEFAULT_BUILD_TAGS` | 1 | `go test -race -count=10 -run '<four tests>'` | 10 | PASS | 2.209s | 39562601824 / `984b439` |
| macOS | arm64 | 1.25.5 | `release/DEFAULT_BUILD_TAGS` | 1 | full fast-path gate | 3 green | PASS | ~15 min each | R2/R3/R4 above / `8d5d715` |

## 8. Verdict

```text
RESULT = DONE
A: test defect (test-side ABBA cycle), fixed; a pre-fix order now fails in <=4s instead of hanging.
B: test-measurement defect, root cause measured on darwin/arm64: the first non-zero census reading
   is 0 or 1 for the first ~2-3ms after Start; the settled bind holds 2 both at start and after the
   wake. The old memo latched the transient 1.
macOS: three independent full-gate passes (R2, R3, R4) on the fix commit;
       100x / 30x stress and 10x -race pass on the same code in the verification run.
No product behaviour is changed, and no product defect is evidenced by either failure.
```

## 9. Remaining risks

* **The census is still an indirect instrument.** It counts goroutines in `runtime.Stack`; a
  future Go runtime that changes how receiver arguments are printed could make it undercount. The
  failure diagnostics now print `marker/matched/other_device/unaddressable/truncated`, so that would
  surface as a measurement failure, not as a silent count.
* **The settle window is a bounded observation, not a proof of atomicity.** 250 ms minimum (first
  establishment) and 50 ms (later fixtures) are far above the measured 1.9-2.7 ms, but a machine
  that takes longer to schedule the second receiver would still record 1; the trace in the failure
  message says so. A later fixture that observes 2 raises the memo and logs the raise.
* **The address-family count is a machine property.** If IPv6 is unavailable the settled count is
  1 and is accepted as such (stable 1 is a valid expectation); if it disappears between a fixture's
  start and its wake, the exact post-wake assertion would fail. That is a real environment change,
  and the diagnostic block distinguishes it from a receiver leak.
* **A baseline census assertion outside the two target failures failed once.** In the diagnostic run
  (which had an extra test creating and closing three endpoints before the package tests), the 10s
  pause precondition of `TestAdvGItem7…` timed out ("Condition never satisfied" at
  `socketCensus == 0`). It did not reproduce in any fix-branch gate run (§7.1). The pause wait now
  reports the observed distribution and the receiver dump, so a recurrence is attributable rather
  than opaque. It is not fixed in this round because it is not evidenced as the same defect, and the
  one run that showed it carried a temporary test the fix does not include.
* **`socketOperationsInFlight` still does not cover the hook-to-bind gap**, as recorded in
  `rebind_lease_test.go`; the pinned-window test keeps that stated limit rather than claiming it
  closed.
* The macOS runs above are the evidence for darwin/arm64. Windows passed locally but is not a
  substitute for Darwin, and the local `-race` run is blocked by the missing mingw toolchain.

## 10. Git

```text
branch          fix/wg-macos-ci-flaky-tests (from testing @ 2a2b3cc)
fix commit      8d5d715dada314e604795fa991cb39610ac7bb76   (code under test in R2/R3/R4)
report commits  02fe2b4e00a (record), ecf9aaed29c (tables), and the commit that updates this block
branch tip      the last report commit; no code changes after 8d5d715 (see `git log -3`)
pushed          origin/fix/wg-macos-ci-flaky-tests (no force-push)
temporary       diag/wg-macos-census (deleted), verify/wg-macos-stress (deleted)
release/tags    untouched; v0.1.23 still resolves to 2a2b3cc
working tree    clean
```
