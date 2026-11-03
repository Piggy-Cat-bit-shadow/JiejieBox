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

Rises are immediate. Falls wait `DemoteAfter` (2 s) so one quiet sample cannot re-open admission
into a burst that is still arriving. The `critical` wait is a fixed 100 ms rather than a derived
fraction: at critical, a caller either gets a slot within a round trip or is refused — parking it
for the normal budget is the behaviour the level exists to stop.

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
| **Reuse sessions with capacity** (`sing-anytls` fork: allow a stream on a non-idle session up to N streams) | The structural fix — 200 flows would share ~4 sessions. Rejected *this round* because it needs a new forked module, a new pseudo-version pin, and a re-validation of the Apple/Android build graph, and round 3 has to merge upstream through that pin. Recorded as the highest-value follow-up, with the measurement in §2.5 that quantifies what it would save. |
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
go test ./common/resourceguard                                # 16 tests, pass
go test ./protocol/anytls                                     # 29 tests (8 added), pass
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

1. Fork `sing-anytls` for capacity-based session reuse (§5); re-measure §2.5 afterwards.
2. Wire the same guard into the other packet-oriented outbounds as separate classes, with the
   class reserve already in the API (`ClassInteractive`) used for latency-sensitive flows.
3. Put `GuardStats` into the OOM report metadata so a device report answers "what did the
   governor do" without a rebuild.
