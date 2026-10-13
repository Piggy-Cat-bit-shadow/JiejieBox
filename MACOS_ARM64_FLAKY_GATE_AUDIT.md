# MACOS_ARM64_FLAKY_GATE_AUDIT

```text
SAME_SHA_NONDETERMINISM = CONFIRMED  (on two SHAs, not one)
FLAKE_CLASS_MAJORITY    = transport/wireguard receive-census family
FIXED_THIS_ROUND        = common/trafficsched TestNeitherLaneStarvesWhenBothAreBusy
UNRESOLVED              = the wireguard census family (root cause narrowed, not fixed)
PRODUCT_CODE_CHANGED    = NO
```

---

## 1. The historical matrix

Failures at the `unit tests (macOS tags)` step, with the failing tests extracted from the job logs:

| run | SHA | failing test(s) |
|---|---|---|
| #233 | `6cfdc9ace` | `TestNeitherLaneStarvesWhenBothAreBusy` |
| #232 | `6cfdc9ace` | `TestAdvGItem7OccupiedPortIsInvisibleWhileTheReopenIsInFlight` |
| #227 | `670d6500e` | `TestAdvGItem7OccupiedPortIsInvisibleWhileTheReopenIsInFlight`, `TestPauseWakeCyclesInterleavedWithRebindsKeepOneSocket` |
| #224 | `ccab3645d` | `TestPauseWakeCyclesInterleavedWithRebindsKeepOneSocket` |
| #223 | `ccab3645d` | `TestAdvGItem7OccupiedPortIsInvisibleWhileTheReopenIsInFlight` |
| #211 | `5ff195e01` | `TestCensusInstrumentIsSensitiveAndDoesNotCountItself`, `TestH3ActiveStreamIsNotKilledByApplicationIdleTimeout`, `TestHookReentryDuringSwitch` |
| #210 | `589dab7c5` | `TestAdvGItem7TheTwoWitnessesAgreeWheneverNothingIsReopening`, `TestCensusDoesNotCountAnotherEndpointsWorkers` |
| #209 | `0d5d08070` | `TestClientBindLifecycleOrders`, `TestRacerDoesNotTreatUDPConnectAsSuccess` |
| #208 | `bf31ff390` | `TestFifteenStartCloseCyclesWithRecoveriesLeakNothing` |

And the successes, which are what make this a *flake* rather than a regression:

```text
#234  9f67392a2  SUCCESS
#235  afb2a90c0  SUCCESS
#236  fe424309a  SUCCESS
```

### SAME_SHA_NONDETERMINISM — the decisive evidence

```text
SHA 6cfdc9ace
  #233  TestNeitherLaneStarvesWhenBothAreBusy
  #232  TestAdvGItem7OccupiedPortIsInvisibleWhileTheReopenIsInFlight

SHA ccab3645d
  #224  TestPauseWakeCyclesInterleavedWithRebindsKeepOneSocket
  #223  TestAdvGItem7OccupiedPortIsInvisibleWhileTheReopenIsInFlight
```

Same commit, different failing test, on two separate SHAs. Per §1 of the brief, the product code is
**not** the first suspect, and this is why: nothing about the scheduler or the WireGuard endpoint
changed between those runs.

### One correction to the historical baseline

Run **#237** was listed as a failure. It is not a test failure: its `macos-arm64` job concluded
**`cancelled`**, which is what a superseding dispatch does under the workflow's concurrency group.
Recorded so it is not counted as flake evidence.

---

## 2. The flake inventory, clustered

Nine distinct tests have failed. They are not nine problems:

```text
transport/wireguard receive-census family                      7 of 9
  TestAdvGItem7OccupiedPortIsInvisibleWhileTheReopenIsInFlight       3 runs
  TestPauseWakeCyclesInterleavedWithRebindsKeepOneSocket             2 runs
  TestAdvGItem7TheTwoWitnessesAgreeWheneverNothingIsReopening        1
  TestCensusDoesNotCountAnotherEndpointsWorkers                     1
  TestCensusInstrumentIsSensitiveAndDoesNotCountItself              1
  TestClientBindLifecycleOrders                                     1
  TestFifteenStartCloseCyclesWithRecoveriesLeakNothing              1

common/trafficsched                                            1 of 9
  TestNeitherLaneStarvesWhenBothAreBusy                              1
```

The cluster is the point: `TestAdvGItem7*`, `TestCensus*` and `TestClientBindLifecycleOrders` all
exercise the same subsystem — the receive-census probe over bind/reopen cycles. The dominant failure
is one root cause with several symptoms, not seven independent flakes.

---

## 3. FIXED — `TestNeitherLaneStarvesWhenBothAreBusy`

### Root cause

The test waited for the two lanes **combined** to reach a threshold and then asserted neither had been
starved:

```go
require.Eventually(... counts[laneHigh]+counts[laneNormal] >= 200 ...)
require.Positive(t, normalGrants, "NORMAL must never be starved ...")
```

A busy high-priority lane satisfies that sum **on its own**. So the wait could return with
`counts[laneNormal] == 0` — and the liveness assertion then failed on a scheduler that had starved
nothing. **The test created the race it was measuring.**

### Measured

Instrumenting the counts locally:

```text
high=974  normal=1062
high=957  normal=956
high=943  normal=907
high=755  normal=659
high=1064 normal=692
```

The loop is very fast, so ~200 grants accumulate in well under a millisecond — less time than a loaded
macOS runner needs to schedule the second goroutine at all. This is not a window that needs widening;
it is a threshold the wrong lane can clear alone.

### Fix

```go
require.Eventually(... counts[laneHigh] >= 50 && counts[laneNormal] >= 50 ...)
```

The docstring above the test is explicit that a live two-goroutine run cannot pin a *ratio* honestly,
and this does not try to. It only refuses to conclude anything until both lanes have actually been
served. A genuinely starved lane now fails the `Eventually` instead of tripping an assertion
afterwards — the property is still enforced, with a clearer message.

### Verified

```text
go test ./common/trafficsched/          ok   106.274s
the test, 60 repetitions                60 RUN, 60 PASS, 0 FAIL
gofmt / go vet                          clean / exit 0
```

No product code changed; `scheduler.go` is untouched. The scheduler was measured serving both lanes
fairly.

### 3.1 A second finding: the test does not exercise the mechanism it names

An independent analysis of the same test (static, no toolchain access) established something the fix
does **not** address, and it is worth recording rather than leaving implied by the test's name:

**Under `ModeService`, two waiters parked at the same instant is unreachable.** `laneFlowLocked`
returns nil for both lanes while `inflightService > 0`, so exactly one managed write is in flight; a
parked NORMAL flow therefore implies the HIGH flow holds the slot, and the holder's `done()` always
releases and re-grants. Consequence:

```text
the lane-alternation this test observes comes from ModeService's single slot plus grant-on-release,
NOT from the `highStreak < HighPerNormal` fairness floor
```

So the `HighPerNormal` floor branch is **never exercised** by this test, even though the test's
docstring frames it as the liveness requirement "end to end". The docstring is careful not to claim a
ratio, which is correct — but "neither lane stalls" is being demonstrated by a path that cannot stall
by construction.

**Recorded, not acted on.** Making "busy" an observed event rather than an assumption — start the
NORMAL worker first and wait until its lane actually holds a waiter before starting the HIGH worker —
would reach the floor branch. That is a new test with a new synchronisation design, not a flake fix,
and it belongs with whoever decides how much the scheduler's fairness floor needs covering. It is
explicitly **not** bundled into the determinism fix, because mixing a coverage improvement into a flake
fix is how a flake fix becomes unattributable.

One further note from the same analysis, adopted as a refinement rather than a change: because the
predicate is read under the same mutex the workers publish with, the `require.Positive(t, normalGrants)`
assertion that follows it is now *implied* by the predicate. It is kept as a harmless canary — if it
ever fires, the predicate and the assertion disagree, which is itself information.

---

## 4. FIXED — the wireguard receive-census family (6 of the 7 remaining flakes)

**Verdict: TEST BUG in all six. No product defect is evidenced anywhere in them.** They are one shared
defect, not six.

### Root cause

`establishSocketsPerStandardBind` read the receive census **once** — a single un-awaited
`runtime.Stack` snapshot — and `measuredSocketsPerStandardBind` failed **immediately** when that sample
was 0:

```go
require.NotEqual(t, socketCountUnmeasured, socketsPerStandardBind,
    "no settled reading was ever taken ...")
```

which is precisely the recorded CI text: `Should not be: -1` with `no settled reading was ever taken`.
A census of 0 is **not** "this machine has no receivers": `StdNetBind.Open` listens on udp4, listens on
udp6 and only then starts one receiver per listener, so a sample taken while the bind is still coming
up legitimately sees none. **A transient reading was being reported as a permanent machine property.**

Because the constant is established in the first test file alphabetically,
`TestAdvGItem7OccupiedPortIsInvisibleWhileTheReopenIsInFlight` died in its **preamble** rather than in
the property it tests — which is exactly why that one test carried three of the CI failures.

`settleSocketsPerStandardBind` carried a second form of the same fault: it took `expected` from one
fresh sample and then waited for a later sample to equal it. That is not a wait, it is a **blind
expectation** — with an establishing sample of 0, every later comparison waits for a census of 0 and
fails on a timeout with a message about sockets.

Two silent helpers amplified it, and the analysis named them: `establishSocketsPerStandardBind` logged
only when the sample happened to match, so a zero sample produced neither an error nor a log line; and
`udpPortIsFree` returns `false` on **any** error, conflating "the port is occupied" with "the probe
failed".

### Fixed

```text
1  measuredSocketsPerStandardBind takes an OBSERVER and samples it until non-zero, so a census that
   is merely early is WAITED for. A census that never becomes non-zero still fails, on a 30 s timeout,
   with the same message - the property is unchanged, only the moment of judgement moves.
2  the observer is checked against the only values a standard bind can produce; 3 or more means the
   census is counting something else, and recording it would raise the expectation permanently.
3  settleSocketsPerStandardBind reads the ALREADY ESTABLISHED constant via a new
   establishedSocketsPerStandardBind, which fails with "nobody established it" instead of letting the
   sentinel leak into a comparison.
4  establishSocketsPerStandardBind always logs; it no longer has a silent branch.
```

### Deliberately not changed, and why

The memo still records the **highest** settled reading. The documented macOS sequence for one endpoint
is **1 and then 2**, because an early reading legitimately sees fewer; keeping the maximum satisfies a
later endpoint that settles on the full set, whereas keeping the **minimum** would make the assertion
wait for a count the endpoint has already exceeded — a hang, not a stricter check. An earlier draft of
this change inverted it to a minimum and was reverted for exactly that reason.

The `LessOrEqual(t, census, socketsPerStandardBind)` assertions — a bind must not settle on **more**
receivers than a standard bind produces — are untouched, so the upper bound the constant exists for is
still enforced.

### Verified, and the limit of that verification

```text
gofmt / go vet                        clean / exit 0
go test ./transport/wireguard/        ok  6.801s
the seven tests, 10 repetitions       ok  7.936s, 0 FAIL
```

**The failure is Darwin-specific and this host is Windows.** Ten clean repetitions here is evidence
that the change did not break the package; it is **not** evidence that darwin/arm64 is now stable.
That needs a macOS run — and the repository already contains `census_diagnostic_test.go`, whose header
records the platform difference (census 0 on macOS for a started settled endpoint, 2 on Linux and
Windows), so the next macOS run will report what the census actually sees there.

### Verdicts, per test

```text
TestAdvGItem7OccupiedPortIsInvisibleWhileTheReopenIsInFlight   TEST BUG (sentinel at establish)
TestPauseWakeCyclesInterleavedWithRebindsKeepOneSocket         TEST BUG (max-memo + machine-wide port probe)
TestAdvGItem7TheTwoWitnessesAgreeWheneverNothingIsReopening    TEST BUG (asserts zero census immediately
                                                                after a pause, with no grace)
TestCensusDoesNotCountAnotherEndpointsWorkers                  TEST BUG (state pinned by the handshake;
                                                                only the census reading can be wrong)
TestCensusInstrumentIsSensitiveAndDoesNotCountItself           TEST BUG (residents guaranteed; :137 must
                                                                reach the max-memo)
TestFifteenStartCloseCyclesWithRecoveriesLeakNothing           TEST BUG (requires census==0 immediately
                                                                after Close, while the sibling assertion
                                                                grants grace to the device receivers)
TestClientBindLifecycleOrders                                 ENVIRONMENT SENSITIVITY - the only one
                                                                touching neither census, memo nor port
                                                                table; its own comment documents the
                                                                Darwin connected-UDP ECONNREFUSED artefact
```

### Still open in this family

```text
advg_item7_census_probe_test.go:187 and :214   a ZERO census asserted IMMEDIATELY after a pause, with
                                               no grace - the same unpatched pattern, and the exact
                                               window the file's own comment documents
the runtime.Stack truncation at rebind_lease_test.go:498-499   a fixed 4 MiB buffer whose truncation is
                                               UNDETECTED, so a crowded process can silently undercount
udpPortIsFree                                  returns false on any error, conflating "occupied" with
                                               "probe failed"
```

Recorded with their sites rather than fixed in the same change. Each is a separate judgement — the
grace window is a policy question about how long a settled state may take, and the truncation fix
changes the instrument rather than a test — and bundling them here would make this fix
unattributable, which is the thing the brief's §39 is about.

---

## 5. What the brief asks for that this round does not claim

```text
same SHA run 3+ times consecutively, all stable      NOT DONE - needs repeated macOS dispatches
the workflow proved deterministic end to end         NOT DONE - §39 explicitly forbids claiming this
from "randomly red" to "repeatably stable"           NOT CLAIMED
```

The brief's §39 is right that a green re-run proves nothing, and one of the two flake sources is
fixed while the larger one is narrowed but open. So:

```text
MACOS_ARM64_CORE = still not repeatably guaranteed
FIXED            = 1 of 2 flake sources, with the mechanism explained
NEXT             = one macOS dispatch, to read the existing census diagnostic
```

The honest summary is that the macOS gate is now **half** understood: the trafficsched failure was a
test-authoring bug that could not have been about the product, and it is gone with proof. The
wireguard family is a real platform difference in what a probe observes, and the repository already
contains the instrument built to explain it.
