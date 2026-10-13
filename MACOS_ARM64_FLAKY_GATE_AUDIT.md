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

---

## 4. UNRESOLVED — the wireguard receive-census family

### What is known, from the repository's own diagnostic

`transport/wireguard/census_diagnostic_test.go` already exists to investigate this, and its header
records the observation better than a summary would:

> The macOS runner reports `Should not be: -1` from `measuredSocketsPerStandardBind`, which means
> `socketCensus` returned 0 for a STARTED, SETTLED endpoint — the one reading that establishes
> `socketsPerStandardBind`. **On Linux and Windows the same call returns 2.**

So on macOS the census probe sees **zero** receivers for an endpoint that has started and settled,
where Linux and Windows see two. That is a platform difference in *what the probe observes*, and it is
not yet established whether the cause is:

```text
PRODUCT   the receiver goroutines genuinely differ on darwin, so the census is right and the
          expectation is wrong
TEST      the probe's marker/address derivation fails on darwin, so the census is wrong and the
          endpoint is fine
ENV       the runner's scheduling delays the receiver start past the settle window
```

The existing diagnostic distinguishes exactly these — it logs the marker's presence in a live dump,
the receiver address it derives, the device address it compares against, and the census at each point,
and it **never fails on a census value**. It is written and waiting for a macOS run.

### Verdict

```text
TestAdvGItem7OccupiedPortIsInvisibleWhileTheReopenIsInFlight   UNKNOWN - needs the diagnostic run
the other six census-family tests                              UNKNOWN - same subsystem, same probe
```

**NOT FIXED, and deliberately not guessed at.** Tightening or loosening those assertions without the
diagnostic output would be the "move the test until it passes" failure the brief forbids. The blast
radius of a wrong guess here is a release gate that either blocks correct code or admits a broken
endpoint.

**One concrete next action closes it**: dispatch `client-macos.yml` and read the
`census_diagnostic_test.go` output. It is a report, so it cannot fail the run on a census value — which
is precisely why it is safe to run as the next step rather than something to reason about further.

### Scope note

`TestH3ActiveStreamIsNotKilledByApplicationIdleTimeout` and `TestHookReentryDuringSwitch` (both in
#211) are an idle-timeout test and a re-entrancy test. They are **not** obviously part of the census
family and are recorded as separate, single-occurrence items rather than folded into the cluster.

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
