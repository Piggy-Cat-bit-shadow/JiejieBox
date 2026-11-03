# iOS Network Extension resource guard

**Status:** implemented, unit- and integration-tested on a Windows host; device verification is
`NEEDS_DEVICE` (see §9).

This document records the evidence, the root cause, the design and the limits of the admission
guard added for the iOS Network Extension (NE). It is written so a later round can attack the
design without re-deriving it, and so that every number below can be re-measured from the
artifacts named in §2.

## 1. What was added, in one paragraph

A process-wide, bounded admission gate for **expensive, memory-amplifying creations** —
`common/resourceguard` — owned by the existing `service/oomkiller` service and installed **only**
for `policyModeNetworkExtension`. Its first consumer is the AnyTLS outbound, which takes one
permit around every stream dial, i.e. immediately before the client decides to reuse an idle
session or create a new one. The gate bounds *concurrent creation*, is fair and cancellable, has
a bounded queue, and tightens by pressure level. It owns no timer, no goroutine and no second
sample of the process footprint: it reads the level the oomkiller timer already publishes.

## 2. Evidence

Two device diagnostics (`2026-11-03T08-06-56.zip`, `2026-11-03T08-07-46.zip`) were re-analysed
independently with `go tool pprof`; every figure below is from the archives, not from a summary.

### 2.1 Footprint

| | diagnostic A | diagnostic B |
|---|---|---|
| system proxy | false | true |
| first sample | 15,794,928 B, 32 goroutines | 15,074,056 B, 32 goroutines |
| last sample | 45,630,240 B (43.5 MiB), 446 goroutines | 49,972,024 B (47.7 MiB), 392 goroutines |
| Go stacks | 13,828,096 B (13.2 MiB) | 15,204,352 B (14.5 MiB) |
| connections | 221 | 230 (snapshot: 229 = 200 UDP + 29 TCP) |

Budget 52,428,800 B (50 MiB); thresholds in B, `trigger` 47,185,920, `armed` 41,943,040,
`resume` 31,457,280. `GOMEMLIMIT` 41,943,040 B with `gogc=100` — a Go soft limit, **not** a
process ceiling.

`go.log` (B) contains exactly one pressure line, `WARN service/oom-killer[0]: memory pressure:
critical, usage: 40 MB`, and the process still grew to 49,972,024 B afterwards. That is the
signature of the failure: the reactive layer fired, and nothing stopped new work from starting.

### 2.2 The burst

`connections.json` (B) holds 229 rows, all AnyTLS: 200 UDP to ten `*.d.ping0.cc:3478`
destinations, twenty each, plus 29 TCP. **201 of the 229 rows carry `createdAt` in the final
second** of the recording. This is a burst, not a steady state.

### 2.3 The amplifier: 201 concurrent session creations

`goroutine.pb` (B) resolved with `go tool pprof -peek`:

```
89.70%  bytes.(*Buffer).ReadFrom
89.70%  utls.(*Conn).readFromUntil
80.61%  utls.(*Conn).readHandshake
80.61%  utls.(*Conn).HandshakeContext   <- 230 goroutines parked here
```

and, one level out:

```
201  100% | sing-anytls.(*Client).DialContext
     0  60.91%  | sing-anytls.(*Client).createSession
201  100% | sing-box/protocol/anytls.(*Outbound).dialOut
201  100% | common/tls.(*defaultDialer).DialTLSContext
```

So the 201 flows were **not** sharing sessions: `createSession` was in flight 201 times, each
inside a TLS handshake, each having already allocated a client, a post-quantum key pair and
record buffers. The distinct `createSession.func1` count (30) is the 30 sessions that had
completed and were serving streams.

The per-flow goroutine is also visible: 200 goroutines created by
`sing-tun.(*UDPNat).getOrCreate` at **32,768 B of stack each** = 6.55 MiB, which is most of the
11.3 MiB `/memory/classes/heap/stacks` figure. `runtime.malg` + `runtime.allocm` add ≈ 2.5 MiB
of runtime bookkeeping for the same goroutines.

### 2.4 Heap attribution

`heap.pb` (B), `inuse_space`, 20,052 kB total, top entries:

| bytes | % | object |
|---|---|---|
| 3,096 kB | 15.4 | `sing/common/buf.newDefaultAllocator.func8` |
| 2,080 kB | 10.4 | `sing/common/buf.newDefaultAllocator.func9` |
| 1,538 kB | 7.7 | `utls.UClient` |
| 1,032 kB | 5.2 | `utls/mlkem.NewDecapsulationKey768` |
| 1,024 kB | 5.1 | `strings.(*Builder).grow` |

The TLS/ML-KEM entries are per-handshake state, and they scale with the *concurrent* creation
count, not with the number of flows. The buffer allocators scale with live sessions.

### 2.5 Per-session cost, measured locally

`TestSessionCreationCostCensus` (`protocol/anytls/session_burst_test.go`) drives the **real**
`sing-anytls` client against a loopback listener:

```
live AnyTLS session cost: 7414 bytes/session (200 sessions added 1,482,952 bytes of live heap)
```

That is the AnyTLS layer only (no TLS). Adding the pprof per-handshake entries (~7.6 kB for
`UClient`, ~5 kB for the ML-KEM key, plus record buffers) puts a production session in the
tens of kilobytes — which is why 201 *simultaneous* creations, rather than 201 sessions, is the
number that matters.

## 3. Root cause

Three mechanisms, in order of how much each one contributed:

1. **Unbounded concurrent creation.** `sing-anytls`'s `Client.DialContext` reuses only a session
   with **zero** streams (`takeIdleSession`); otherwise it calls `createSession`, which dials,
   handshakes and starts a read loop. There is no single-flight, no cap and no queue. Two hundred
   simultaneous flows therefore produce two hundred simultaneous handshakes. This is the
   amplifier.
2. **The burst is legitimate traffic.** Twenty UDP flows per STUN destination is what a
   reachability probe does. The fix cannot be "refuse UDP"; it has to bound the cost per unit
   time of a burst that will otherwise all arrive at once.
3. **Reclaim was reactive only.** The oomkiller's pressure ladder measures, logs and reclaims,
   but until this change nothing could stop new expensive work from starting while the process
   was already over the armed line.

`getOrCreate`'s 32 KiB per-flow stack is real but is *not* addressable at this layer: the
goroutine exists because the flow exists, and it is created before any outbound is chosen. It is
recorded here so a later round does not attribute it to this gate.

## 4. Design

### 4.1 Ownership

```
service/oomkiller (owns the budget and the pressure ladder; darwin+NE only)
        │  publishes  tun.MemoryPressure  (None / Warning / Critical)   [existing]
        │  also owns ──> resourceguard.Guard  ── registered in ctx
        ▼                                        │
common/resourceguard                             │ reads the same atomic
  Gate: bounded, FIFO, cancellable admission     │ (no second sampler)
  Guard: level -> plan, shed hooks               │
        ▲                                        │
        │  Acquire/Release                       │
protocol/anytls.Outbound.dialAdmitted ───────────┘   (TCP and UoT share this seam)
```

The guard never measures anything. On a platform without `policyModeNetworkExtension` it does
not exist, and every consumer sees a nil guard that admits everything.

### 4.2 State table

| level | admission limit | queue | wait | shed actions | notes |
|---|---|---|---|---|---|
| normal | 32 | 256 | 5 s | — | absorbs the observed 200-flow burst |
| armed | 16 | 128 | 2.5 s | — | first pressure line, still admits a burst |
| critical | 8 | 32 | 100 ms | close idle AnyTLS sessions | fail fast; do not park |

Rises are immediate. Falls require the pressure to be **continuously** lower for `DemoteAfter`
(2 s): the stamp is reset by any observation at or above the current level, so a jittering signal
can never accumulate its way to a demotion. That distinction is not cosmetic - measuring "time
since the level last changed" lets an alternating signal demote once per dwell, and every
re-entry into critical runs the shed hooks again (see §11.2 defect 3). The `critical` wait is a
fixed 100 ms rather than a derived fraction: at critical, a caller either gets a slot within a
round trip or is refused — parking it for the normal budget is the behaviour the level exists to
stop.

`ClassInteractive` may use up to `InteractiveReserve` (4) slots beyond the limit, so a background
burst cannot occupy the last few seats. The reserve is enforced in the GRANT path, not only in the
admission check: a slot that frees while the gate is at the limit is left empty rather than handed
to a queued background caller. A waiter that has been passed over for `StarvationAfter` (1 s) may
claim the reserve, which is what stops the priority from becoming a permanent lockout (§11.2
defect 2). Nothing uses `ClassInteractive` yet; the AnyTLS path asks for `ClassNormal`.

### 4.3 Why the permit sits at the AnyTLS dial seam

It is the last point before the client chooses between "reuse" and "create", it is reached by
both TCP and UoT, and it is in this repository. A gate inside `sing-anytls` would bound the same
thing but would require forking that module; a gate in `sing-tun`'s UDP NAT would bound the
goroutine, which the flow has already paid for.

### 4.4 What the permit covers

Concurrent creation only. It is released when the dial returns. Holding it for the connection's
lifetime would additionally cap live streams, but that needs a wrapper around the returned
connection, and the AnyTLS stream implements `ReadBuffer`/`WriteBuffer`, `FrontHeadroom`,
`WriterMTU` and handshake interfaces that route code probes for — a `net.Conn` wrapper would
silently downgrade the copy path. That trade was rejected; see §6.

## 5. Alternatives evaluated and rejected

| candidate | why not |
|---|---|
| **Reuse sessions with capacity** (`sing-anytls` fork: allow a stream on a non-idle session up to N streams) | The structural fix — 200 flows would share ~4 sessions. Rejected *this round*, and after reading the module the reason is sharper than "it is a dependency change": `session.armOpenTimeout` arms a 3 s watchdog on every stream opened from the **second** onwards, and the watchdog closes the **whole session** when no frame arrives. That is safe today because the client only ever reuses a session with zero streams; enabling multi-stream reuse arms it on every multiplexed session, and a quiet UDP/UoT flow — precisely the STUN shape measured here — can trip it and take every stream sharing that session down with it. So the fork would need a second, protocol-visible deviation (disable or re-scope the watchdog for datagram streams) that can only be validated on a device. Recorded as the highest-value follow-up with that hazard attached, plus the per-session measurement in §2.5. |
| Gate inside `sing-tun` `UDPNat.getOrCreate` | Bounds the 32 KiB stack, but requires changing and re-pinning `sing-tun`, and the flow's identity has already been created. The stack is ~6.5 MiB at 200 flows; the handshake state is the larger and cheaper-to-bound term. |
| Global GC tuning (`GOGC`, `GOMEMLIMIT`, periodic `FreeOSMemory`) | Does not free live sessions, does not stop the storm, and `GOMEMLIMIT=40 MiB` was already in force when the process reached 49.97 MiB. |
| Token-bucket pacing instead of a concurrency limit | The reference implementation (Hako/ProjectClash `memory_shed_pacer.go`) paces at 8/s with burst 8 and never refuses. A pure rate limit still allows an arbitrarily large number of *simultaneous* creations when the arrival rate exceeds the rate for a short window; a concurrency bound is what caps the transient. The two compose, and the gate's queue is where a rate limiter would sit later. |
| `if ios` branches inside protocol code | Rejected by design: protocols ask for a class, the policy owner decides whether a guard exists. |

## 6. Known limits

1. **Live sessions are not bounded.** With long-lived flows, 200 sessions are still created —
   serially. Steady state after a 200-flow burst is therefore close to what it was; what changes
   is the transient (201 concurrent handshakes → ≤32/16/8) and the refusal behaviour at critical.
   The structural fix is §5 line 1.
2. **The 32 KiB UDP NAT stack per flow is untouched** (§3).
3. **Refusals surface as connection errors.** At critical a refused dial returns
   `resource guard: admission queue is full`, wrapped by the outbound's existing
   `proxy <tag> (<server>)` prefix, so it is visible in the connection log without a new log line.
4. **`critical` is entered from the footprint ladder, which samples approximately once a second.**
   A burst can grow the process by several megabytes between two samples. The level therefore
   lags reality; the normal-level limit is what has to be right, and the level is what makes the
   lag cheaper once it is detected.
5. **No device verification.** See §9.

## 7. What was deliberately not changed

* `service/oomkiller`'s timer, thresholds, ladder, reclaim executor and recorder: none of it was
  touched. The guard reads `Service.pressure`, which the timer already writes, so there is one
  authority for "how much pressure is the process under".
* `sing-tun`, `sing`, `sing-quic` and every other pin: no dependency changed.
* AnyTLS stream semantics: the permit is outside the stream, so ordering, half-close, framing and
  buffer ownership are unchanged.
* `protocol/anytls`'s UoT path: it already funnelled through the same client, so it acquired the
  guard by changing one constructor argument.

## 8. Verification

Local (Windows, `go1.25.5 windows/amd64`):

```
go build ./...                                                # pass
go vet ./common/resourceguard ./protocol/anytls ./service/oomkiller   # pass
go test ./common/resourceguard                                # 37 tests, pass
go test ./protocol/anytls                                     # 37 tests, pass
go test -run '^$' -bench BenchmarkDialWithAdmission -benchtime 3000x
```

Overhead, measured (`BenchmarkDialWithAdmission`, Ryzen 7 4800H):

| path | ns/op | allocs |
|---|---|---|
| no guard (every non-NE platform) | 27 | 0 B, 0 allocs |
| guard, normal level | 120–141 | 48 B, 1 alloc |

Integration coverage, all on loopback with the real `sing-anytls` client:

| test | asserts |
|---|---|
| `TestSessionBurstWithoutGuardReproducesTheAmplification` | 200 flows → 200 concurrent `DialOut` calls, 200 server connections (the baseline the guard is measured against) |
| `TestSessionBurstWithGuardBoundsConcurrentCreation` | 200 flows → peak ≤ 8 concurrent creations, all 200 admitted, no refusals |
| `TestSessionBurstGuardDoesNotChangeObservableBehaviour` | 16 admitted streams remain distinct and close cleanly |
| `TestSessionCreationCostCensus` | 7,414 B per live session (§2.5) |
| `TestTLSHandshakeBurstAmplificationIsBounded` | the same burst with a REAL TLS handshake blocked on a silent server: concurrent handshakes and marginal peak heap, with and without the guard |
| `TestAdmissionMatrix` | 50 / 200 / 500 / 1000 concurrent callers: ceiling holds, every caller is admitted or refused exactly once, at 1000 the QUEUE bound decides who is refused |
| `TestAdmissionCancelStorm` | 500 queued callers cancelled at once: all return, no slot leaked, gate reusable |
| `TestDialFailuresNeverLeakPermits` | refused port, server hanging up mid-handshake, deadline expiry — every path returns its permit |
| `TestGuardRepeatedPressureCycles` | five normal→armed→critical→normal cycles: one shed per entry, the limit reopens every time |
| `FuzzGateSlotAccounting` | `go test -fuzz -fuzztime 25s`: 21,756 executions, slot invariant `inflight == admitted - released` held |
| `TestGuardStatsReportsThePlanTheLevelImplies` | the snapshot cannot say "critical" while advertising the normal limit |
| `TestMergeExtraMetadata*` | the OOM metadata document keeps every existing key, and an unparsable document is left untouched |

### 8.1 The report carries the counters

An OOM report is written by the client, whose metadata schema this package must not extend, so
the guard contributes one **top-level sibling key**, `resourceGuard`:

```json
{"appVersion": "...", "memoryUsage": "47 MB",
 "resourceGuard": {"level": "critical", "limit": 8, "inflight": 8, "refused": 0, "timedOut": 32, ...}}
```

`Recorder.SetExtraMetadata` splices it in at write time; an existing reader ignores the key, and a
process with no guard writes exactly the document it wrote before (the provider returns nil). That
is what turns the next device diagnostic into an answer to "what did the governor actually do"
instead of only "how big did it get".

The last test is the local version of the device event: 200 dials whose `DialOut` performs a real
`crypto/tls` handshake against a listener that sends nothing, which is the state the profile
showed (`utls.(*Conn).HandshakeContext`, blocked in `readHandshakeBytes`). Measured on the
development host, three consecutive runs of the same binary:

| | concurrent handshakes | marginal peak heap |
|---|---|---|
| no guard | 200 | 3.32 – 3.77 MiB |
| limit 8 | 8 | 0.32 – 0.40 MiB |

That is ~17.5 kB of transient heap per in-flight handshake, which is consistent with the pprof
attribution in §2.4 (`UClient` 7.6 kB + ML-KEM key 5.1 kB + record buffers). The peak is what
jetsam sees; the steady state after the burst is not changed by this gate (§6.1).

## 9. What is NOT verified

`BLOCKED_BY_ENVIRONMENT` / `NEEDS_DEVICE`:

* **No iOS device run.** Nothing here proves the extension survives a real jetsam window. The
  claim is narrower: concurrent session creation is bounded, and the bound is exercised against
  the real client on a local server.
* **No Darwin build.** `service_darwin.go` is `darwin && cgo`; a Windows host cannot compile it,
  and no file under that constraint was modified.
* **`go test -race` did not run on this host** — it needs cgo and a mingw-compatible C toolchain,
  which this machine does not have (the available `clang` is the MSVC-target Swift toolchain and
  rejects `-mthreads`). `.github/workflows/resource-guard.yml` runs the same packages under the
  race detector on a Linux runner on every push that touches them.
* **No multi-protocol matrix.** Hysteria2, TUIC, MASQUE, Mieru and Naive were not exercised; only
  the measured AnyTLS/UoT path has a guard today.

## 10. Follow-ups (not this round)

1. Fork `sing-anytls` for capacity-based session reuse (§5, including the 3 s stream-open watchdog
   that makes it a protocol change rather than a pool tweak); re-measure §2.5 afterwards.
2. Wire the same guard into the other packet-oriented outbounds as separate classes, with the
   class reserve already in the API (`ClassInteractive`) used for latency-sensitive flows.
3. ~~Put `GuardStats` into the OOM report metadata~~ — done: `Recorder.SetExtraMetadata` writes the
   counters under the top-level `resourceGuard` key (§8.1).

## 11. Round 02 — adversarial review (state machine, defects, bounds)

This section is written by the review, not by the author of §1-§10. It attacks the system with
wrong input, wrong timing and wrong state, and records what had to be fixed.

### 11.1 The real call graph (not the file names)

```text
oomkiller timer (1 s adaptive cadence, darwin dispatch source also drives it)
   sample = memory.Total()  ->  nextPressureState()  ->  threshold ladder
        │
        ├─ stores Service.pressure  (atomic uint32, tun.MemoryPressure)
        └─ on Triggered: releaseMemory() [pre-existing path, unchanged]

anytls outbound dial (TCP and UoT share it)
   dialAdmitted -> guard.Acquire
        observe():  pressure() = Service.guardLevel()  <- the SAME atomic
                     rise: immediate; fall: only after DemoteAfter of CONTINUOUSLY lower
        apply():    plan -> gate (edge-triggered; runs shed hooks on entry to critical)
        gate.Acquire: already-canceled ctx? refuse | under limit? admit | queue full? refuse
                      | else park on a private channel with a deadline
   expensive work (session creation: TCP + TLS + ML-KEM)
   defer permit.Release()
```

### 11.1.1 Lifecycle: NetworkExtension start to stop, and what is left behind

```text
extension start
  libbox CommandServer.Start
    registers *oomkiller.Recorder in the service registry
  Box.Start
    serviceManager.Create   (box.go:747)
      oomkiller.NewService
        mode == networkExtension  ->  NewGuard
                                       service.MustRegister[*resourceguard.Guard]
                                       recorder.SetExtraMetadata("resourceGuard", ...)
    outboundManager.Create  (box.go:784)   <- AFTER services, so the guard is already registered
      anytls.NewOutbound  ->  FromContext[*Guard] captured
    outbound.Start(Initialize)
      anytls.Start  ->  RegisterShed(client.CloseIdleConnections)
    service.Start
      oomkiller.startTimer  ->  pressure begins to move
steady state
    dial -> Acquire -> expensive work -> Release      (no long-lived guard state per flow)
tunnel stop / Box close
    outbound Close -> anytls client.Close
    service.stopTimer -> pressure := None
    the Guard is dropped with the Box
```

Nothing has to be torn down for the guard itself: it owns no goroutine, no timer, no file
descriptor and no listener, so there is no shutdown ordering to get wrong. The things to check
when a future change touches this:

| question | answer for this system |
|---|---|
| lingering goroutine after stop | none: the gate parks callers on their own channels, and the caller cancels them |
| lingering timer | none: the only timer is the per-waiter wait budget, stopped by `defer` |
| callback into a dead instance | the shed hook closes idle sessions on a client whose `Close` is idempotent; after stop the hook is unreachable because the guard is |
| global singleton retaining an old policy | the guard lives in the per-Box service registry; a reload registers a NEW guard over the old key, and the old outbounds keep the old guard until they are closed |
| duplicate registration | `Outbound.Start` returns early unless the stage is Initialize, so one hook per outbound instance |
| reload accumulation | the metadata provider is overwritten per reload (same key), so the recorder retains one guard, not one per reload |
What the system does NOT own, and must not be read as owning:

| resource | owner | guarded here? |
|---|---|---|
| goroutine + 32 KiB stack per UDP flow | `sing-tun.UDPNat.getOrCreate` | **no** — created before any outbound exists (§6.2) |
| route connection goroutines | `route.ConnectionManager.NewConnection` | no |
| TLS handshake state per session | `sing-anytls` inside the dial | **yes** — the permit is held across it |
| session/stream buffers after the dial | `sing-anytls` | no — steady state is uncapped (§6.1) |
| the decision "how much pressure are we under" | `service/oomkiller` timer | no — read, never re-measured |

### 11.2 Defects found and fixed this round

Each one was reproduced as a failing test before the fix, and the test stays as the regression.

| # | severity | condition | failure evidence | fix |
|---|---|---|---|---|
| 1 | **high** | any operator config with `Limit < 4` or `Queue < 16` | `planFor(Armed).limit` = 2 while `planFor(Normal).limit` = 1: arming the guard ADMITS MORE than normal. The floors designed to avoid a deadlock could cross the level above them. | derived plans are clamped to the level above (`TestAttackDerivedPlansNeverExceedTheBasePlan`) |
| 2 | **high** | a saturated interactive class | with the gate held at its limit and the reserve continuously re-taken, a queued background caller was passed over 60 times in a row: the reserve was a permanent lockout, not a priority | aged waiters may claim the reserve after `StarvationAfter`, bounded by the absolute cap (`TestAttackInteractiveTrafficCannotStarveBackgroundForever`, `TestAgingUsesTheInjectedClock`) |
| 3 | **high** | a jittering pressure source | the dwell was measured from the last transition, so an alternating signal satisfied "2 s elapsed" after enough toggles and demoted once per dwell — every re-entry ran the shed hooks again. A 5 ms toggle under a 50 ms dwell produced repeated reclaim. | demotion requires the pressure to be CONTINUOUSLY lower; any observation at or above the level resets the stamp (`TestAttackOscillatingPressureDoesNotAmplifyReclaim`, `TestReclaimRateUnderJitterIsZero`) |
| 4 | medium | a waiter granted at the instant it gave up | the caller was counted in BOTH `admitted` and `canceled/timedOut`, and `released` was incremented for a slot nobody used, so `admitted + refused + canceled + timedOut != requested` and the reported "admitted" was a grant count, not a caller count | the phantom grant is undone (`admitted--`, no `released++`) and the slot is passed on (`TestAttackMixedOutcomesKeepTheAccountingExact`) |
| 5 | medium | a context already dead before the call | `Acquire` admitted first and checked nothing, spending a permit on a dial that could not succeed | cancellation is checked before the limit (`TestAttackCancelledCallerNeverTakesASlot`) |
| 6 | low | contended hot path | `apply` took the policy lock on every dial to compare an unchanged level | the check is an atomic load; the lock is only taken when the level actually changed |

Attacks that found nothing, and the test that now holds them:

| attack | result |
|---|---|
| 500 queued waiters, each with a goroutine of its own | the gate spawns none; it costs one channel and one struct per waiter (`TestAttackQueuedWaitersDoNotOwnGoroutines`) |
| 200 dials under steady critical | one shed, not 200 (`TestAttackShedDoesNotRunPerDial`) |
| 1000 metric polls under critical | one shed (`TestAttackShedIsEdgeTriggeredFromTheMetricsPath`) |
| refused dial | the expensive work never starts (`TestDialWithAdmissionNeverDialsWhenRefused`) |
| cancelled while queued | no dial, the caller sees its own context error (`TestDialWithAdmissionCancelDuringQueueDoesNotDial`, `TestAttackContextErrorIsNotSwallowed`) |
| a panicking dial | the permit is still returned and the gate stays usable (`TestDialAdmittedSeesNoPermitLeakOnPanic`) |
| 200 stalled TLS handshakes with every caller timing out | ceiling holds at 4-8, every permit returns, no goroutine left behind (`TestStalledHandshakesLeakNothing`, `TestStallWithQueueExhaustedRefusesFast`) |
| 3 cycles of idle -> critical -> recovery on the real seam | normal admits 40/40 at peak 4, critical admits 5 and refuses 35 at peak 1, recovery reopens the limit every cycle (TestSoakCyclesReopenAdmission) |
| 0 / 1 / cap-1 / cap / cap+1 / 100 callers | ceiling holds, nobody is lost (`TestAttackQueueBoundaries`, `TestDialWithAdmissionBoundarySizes`) |

### 11.3 Anti-reclaim-loop evidence, with numbers

The concern is the loop "pressure → reclaim → no real decrease → reclaim again". For this system
the reclaim action is one hook (close idle AnyTLS sessions), and the structural bound is:

> a reclaim runs once per ENTRY into critical, and entries are separated by at least
> `DemoteAfter` of continuously lower pressure. Maximum rate = one per `DemoteAfter`.

Measured with the production constants (2 s dwell, 1 s sampling) on an injected clock, so a
simulated minute costs milliseconds:

| signal (60 simulated seconds) | reclaims | bound |
|---|---|---|
| sustained critical, sampled every 250 ms (240 samples) | **1** | 1 |
| jitter: 1.9 s below / 0.1 s above, 30 cycles | **1** | 1 |
| full-band square wave: 3 s below / 3 s above | **10** | 31 (= 60/2 + 1) |

Sustained pressure is therefore not a loop at all — it is a single action. The only way to get
another is for the process to genuinely leave the critical band for longer than the dwell and
come back, and even then the rate is capped at 1/dwell. There is no "no-progress" counter because
there is nothing to retry: the hook closes idle sessions, it does not attempt to move a memory
reading, so "it ran but the number did not move" cannot cause it to run again.

### 11.4 Hot path under contention

`BenchmarkDialWithAdmissionParallel`, 16 goroutines, Ryzen 7 4800H:

| path | ns/op | allocs |
|---|---|---|
| no guard (any non-NE build) | 46-59 | 0 |
| guard, normal level | 291-335 | 48 B / 1 |

The 1 allocation is the permit itself. Against a session creation (TCP + TLS + ML-KEM, measured
in milliseconds) this is under 0.1% of the operation it guards, and it is what bounds the peak.

### 11.5 Residual risks this review could NOT remove

1. **A permit's hold time is bounded only by the caller's context** (§6.1, §6.4). A peer that
   completes the TCP connection and then never answers the handshake pins one permit until the
   flow dies. At critical (8 slots) that closes admission for AnyTLS. The degradation is bounded
   and fast — refused in 100 ms, queue capped at 32, no memory growth
   (`TestAttackPinnedGateRefusesFastRatherThanQueueing`) — but it is a real availability trade,
   and fixing it means giving the outbound a session-creation deadline, which is a configuration
   surface this round is not allowed to add.
2. **Goroutine stacks and steady-state sessions remain upstream of the gate** (§6.1, §6.2). The
   guard cannot make the 200-flow burst cheaper than "200 flows"; it removes the handshake storm
   on top of them.
3. **The shed hook runs on the dial path**, synchronously, once per transition. It is bounded and
   idempotent, but a pathological `CloseIdleConnections` would delay exactly one dial.