# Debug report

This is the evidence part of the final audit. It records what was attacked, how,
and what the run answered. The three-stage ledger
([`ISSUE-LEDGER.md`](ISSUE-LEDGER.md)) carries the final status of each item;
this document explains the reasoning behind it.

## 0. State and method

- Branch `work/coupling-audit-2026-11-11`, HEAD after merging the current remote
  `testing` tip: `f30e36a4dea` ("merge: bring the coupling-audit branch onto the
  current testing tip"). The merge brought 15 commits (hidden-group feature,
  Apple submodule pin, release locks, docs); none of them touch the files the
  earlier stages changed (`common/tls`, `protocol/mieru`,
  `transport/v2rayxhttp`, `scripts/perf`), and `go.mod`/`go.sum` are unchanged
  across the merge, so the earlier measurements remain comparable.
- The remote deleted `integration/0.1.45-final` while this work ran; the branch
  history still contains the original base `4ec0decd9f2d` and the local
  `localrepo/integration/0.1.45-final` ref. Nothing was reset or rewritten.
- Every claim below has a command, a source location or a captured artifact.
  Where the host cannot run something, it is marked
  `BLOCKED_BY_ENVIRONMENT`; no substitute claim is made.

## 1. Is the test infrastructure itself reliable?

**Finding S3-1 (fixed).** `scripts/perf` recorded `run.json.source.modules` by
parsing `require` lines of `go.mod` and skipping `replace` lines. Every fork
dependency in this repository is redirected by a replace, so the record named the
upstream version that was *not* built: e.g. sing-tun was recorded as
`v0.9.7-0.20261109145802-79ed0653fdc6` while the build used
`v0.9.7-0.20261110222329-99abf3c27666`, and the same held for sing-quic, cronet-go
and the rest. This is a provenance defect in the lab, not in the kernel: the
stored matrices cannot prove which dependency produced a number.

Fix: `modulePins` now parses single-line returns, `require` blocks, `replace`
directives and `replace ( ... )` blocks, and records the replacement target (or
the local path). Tests: `scripts/perf/internal/source/source_test.go` covers a
single-line replacement, a block replacement and a local path. The stage-1/stage-2
runs on disk keep their old (wrong) pin strings; the final runs
([`results/`](results/)) carry the correct ones. `docs/perf/ENVIRONMENT.md` gets a
correction note.

**Sampling design (verified by reading).** The sampler takes its first reading
synchronously (so `RSSStart` is before traffic), then every 100 ms; CPU comes from
`/proc` ticks *and* the authoritative total from `wait4` rusage; PID reuse is
detected via field 22 of `/proc/<pid>/stat`; read failures are counted, not
dropped. The asymmetric design is documented in the package comment and matches
what the schema says.

**Two limits that the audit confirms rather than fixes:**

- `go_heap_peak_bytes` is declared in the schema but **never assigned**; no pprof
  endpoint exists in the production binary (`option/experimental.go` has cache,
  clash_api, v2ray_api and debug only). Every run records `null`, so RSS-only
  attribution is the ceiling (B3 / S3-3).
- `goodput` divides payload by `time.Since(start)`, where `start` precedes the
  stream goroutines, so dial and handshake time are inside the denominator
  (`loadgen.go:140,171`). On a 5 s loopback window this is under 1 %; on an 80 ms
  shaped path it is a few per cent. It is a definition, not a bug (S3-4), and every
  cell uses it, so protocol comparisons remain fair.

**Warehouse (`wire`) counters** are null on loopback because there is no veth to
read; shaped runs read them from netlink in the namespaces. This is recorded in
each run's `wire` field rather than silently zeroed.

## 2. ResourceGuard: leaks, double release, starvation, deadlock

The stage-2 audit located the seams and the unit each admits; this stage attacked
the accounting itself.

- The one invariant every path must keep is `Inflight == Admitted - Released`.
  `FuzzGateSlotAccounting` drives arbitrary acquire/release/cancel sequences
  against a 3-slot gate and checks it; the seed corpus runs on every `go test`.
- `TestGuardRepeatedPressureCycles` walks normal→armed→critical→normal five
  times and requires: limit reopens to 32, each entry into critical sheds exactly
  once, `Inflight` zero at the end.
- The stage-2 rate-window defect is fixed and pinned by
  `TestAQueuedFastFailureStillArmsTheRateLimit`: a dial queued 300 ms at the gate
  and then failing in a microsecond now counts as a fast failure and arms the
  window.

Results on the merged HEAD:

- `go test -count=3 -race ./common/resourceguard` — pass (3.5 s).
- `go test -fuzz FuzzGateSlotAccounting -fuzztime 25s` — pass, 36 650 executions,
  26 new interesting inputs; the invariant `Inflight == Admitted - Released` and
  the final `Inflight == 0` held for every generated sequence.
- `BenchmarkGateAcquireRelease`: 270.7 ns/op, 48 B/op, 1 alloc/op;
  `...Contended`: 1438 ns/op, 504 B/op, 6 allocs/op. The only allocation in the
  uncontended path is the waiter/list bookkeeping the contended path needs; a
  permit costs a few hundred nanoseconds against a handshake measured in
  milliseconds.
- CPU profile of the two benchmarks: futex park/unpark, `time.runtimeNow` and
  `internal/sync.Mutex` handoff dominate (the synthetic bound is a one-slot gate
  with 16 goroutines); no unexpected allocation site appears in `Guard.Acquire`.
  Mutex profile attributes the delay to `Gate.Acquire`/`release`, the queue
  itself, not to a hidden second lock.

Artifacts: [`evidence/resourceguard-profile.txt`](evidence/resourceguard-profile.txt)
with the profile files beside it.

**Deadlock analysis by lock order.** `Gate` takes only its own mutex; `Guard`
takes its own lock and calls the gate after unlocking; `growth` and `rate` never
call back into the gate; shed hooks are registered by protocols and run from
`apply` with the guard lock released. `Close`/`InterfaceUpdated`/pressure paths
do not take a permit while holding a lock that a permit holder may need: the
protocol seams acquire permits *before* taking any protocol lock, and release
them before returning. No cycle found.

**Starvation.** `ClassInteractive` may use a reserve above the limit; a normal
waiter that does not fit may consume the reserve after `StarvationAfter`. The
stage-2 class tests cover the priority direction; no unbounded starvation was
found.

## 3. Pools and session lifecycles

- **TrustTunnel is the reference implementation**: one shared offer, waiters join
  without a second permit, a reset marks the offer discarded and a late success is
  closed instead of published. Its own tests cover waiter cancellation, owner
  cancellation, close×in-flight, and the UDP2 budgets.
- **XHTTP** had the one real defect in this area (F-03, fixed in stage 2): a dial
  racing `Close` could create a pooled connection nothing owned. The fix adds the
  closed flag and an error at `getContext`; two tests drive the after-close and
  racing-close cases, the second under `-race`.
- **AnyTLS** still charges a creation permit for a pure reuse (F-06). The pool is
  in `sing-anytls`; a fix needs a pool-aware hook there. The cost is measurable:
  with the gate saturated, a reuse waits or is refused although it creates
  nothing.
- **Mieru (S3-9, new in this stage)**: `go test -race -count=3 -run
  TestMieruClientObservesServerClosingAnIdleUnderlay ./protocol/mieru` fails 1/3
  with `failed to read socks5 connection response from the server: EOF` and passes
  3/3 without `-race`. The failing assertion is the *new* dial after the idle
  close: the client can pick the underlay the proxy just closed and the SOCKS
  reply hits EOF. The test's expectation is the product behaviour it documents, so
  this is a recovery race, not a flaky assertion; the pick-to-dial window is in
  the SDK. [`evidence/mieru-idle-underlay-race.txt`](evidence/mieru-idle-underlay-race.txt).
- **Mieru (S3-2)**: under the `connect` pattern the client's FD peak grows across
  repetitions — 113→198 at one stream and 222→285 at four, with RSS stepping to
  ~217 MiB — while the earlier patterns stay at 20–43 FDs. The SDK closes an
  underlay only when `SessionCount()==0 && Scheduler().Idle()`
  (`pkg/protocol/mux.go:794`). Two in-process probes sharpen this without
  settling it: 20 sequential dials add at most 16 FDs and stay flat for 40 s
  idle, and 200-dial bursts add diminishing amounts (55→73→83 FDs) with no
  reclamation inside 10 s. Because the in-process probe also counts the test
  inbound's sockets, the decisive measurement is a two-process client with an
  idle tail and an external FD sampler — the lab cannot express that phase yet,
  so the finding stays `DEFERRED`. Evidence:
  [`evidence/mieru-idle-retention.txt`](evidence/mieru-idle-retention.txt).
- **Naive/Cronet** has no pool the runtime can reach: the engine owns its
  connection pools and is not rebuilt on a network change (stage-2 audit). No Go
  leak path was found in the engine's close path; the native side is covered in
  §7.

## 4. RuntimeCoordinator and network generations

- Lock discipline verified: `Advance`/`Close` copy the registration set under the
  bookkeeping lock, release it, then call `observeEpoch`/`invalidate` on each
  registration (`coordinator.go:80-104,109-136`). No callback runs under the
  central lock; the change channel is closed and replaced so every waiter
  observes one transition.
- Leases: `BeginRebind` grants at most one per window, `Complete` releases it, a
  generation change invalidates it and cancels its context; WireGuard is the only
  production user.
- `Registration.Stale`/`Acknowledge` have no production caller (F-13). The dead
  API is a documentation hazard, not a live race.
- The 6 s listener phase (F-15) is the one real gap: a dial inside the window can
  attach to a pool that has not been reset, and no protocol reads the epoch at
  pool hand-out. The window is bounded and the pools do drain; the fix is the
  epoch-at-hand-out change recorded in `docs/coupling/DECISIONS.md` D-3.

## 5. OOM and MemoryPressure

The reclaim ladder (caches → idle pools → GC; crisis adds a full network reset) is
driven by `service/oomkiller` and consumed by the guard's level and by WireGuard's
`MemoryPressure` callback. Two shed hooks exist (AnyTLS, TrustTunnel), both
idle-only and edge-triggered once per entry into Critical.

The audit confirms the intended property: **no active stream can be closed by the
memory path**. The pools re-check `streams>0` before releasing (TrustTunnel,
XHTTP, AnyTLS). Recovery creation is throttled, not blocked (Critical plan:
limit/4, queue/8, 100 ms wait), which is the right trade for an extension whose
failure mode is being killed.

The gap is coverage of native owners: MASQUE's userspace stack, WireGuard's
device and Cronet's engine have no shed hook and their memory is mostly outside
the Go heap. This is unchanged from stage 2 and is part of B3's attribution limit.

## 6. DNS, dual stack and PREF64

The dial layer resolves inside the dial (`dialOptions.DomainResolver` →
`NewDNSQueryOptions`), plans candidates with `planCandidates`, and validates the
network generation at hand-over (`errNetworkChanged`). Family capability is
shared by every protocol that uses the shared dialer.

Bypasses are deliberate and unchanged: MASQUE H3 dials through a raw/bootstrap
dialer, Mieru resolves before its SDK dialer, Cronet resolves natively, and
WireGuard's peer address resolution is its own. They are forced by native/QUIC
lifecycles; the consistency cost is that the family policy is not exercised on
those paths. No duplicate-resolution defect was found in the shared path.

## 7. TrustTunnel UDP2, MASQUE datagrams, WireGuard/AWG, Cronet

- **TrustTunnel UDP2**: the invariants "every reserved byte is released" and
  "association slot returned exactly once" are covered by
  `service_udp_limit_test.go` (session/global association caps, session/global
  queued bytes, bad frame and half-close release), `service_udp_close_test.go`
  (concurrent close and expiry) and `service_udp_idle_test.go` (idle sweeper
  re-check). These run under `-race` in this stage; no mismatch appeared.
- **MASQUE**: boundary tests cover outer QUIC MTU, inner MTU, port MTU
  answerability, startup readiness, server close boundary and bootstrap-racer
  socket ownership. The client endpoint's lack of `Close` (two closers, no
  duplicate-tag cleanup) is F-05, deferred; it is a lifecycle-shape issue, not a
  datagram-boundary one.
- **WireGuard/AWG**: the device/bind/endpoint close order and pause/rebind paths
  are covered by the existing suite (close order, pause deadlock, rebind lease,
  ListenPort, AWG ownership). The remaining high-severity defect is B7, whose
  root cause is now known (see §8); it is a send-path accounting bug in the
  wireguard-go fork, not a teardown ordering bug.
- **Naive/Cronet**: measured thread/FD peaks are 25–26 threads and 22–23 FDs
  versus 17–19 threads and 15–18 FDs for the non-native protocols, with RSS
  ~9 MiB higher; the engine count follows `concurrency`, and `Close` is not
  nil-idempotent. The audit cannot attribute the threads to Chromium separately
  (no per-engine counters, no PSS under WSL2), and a network change does not
  rebuild the engine. Recorded, not changed: rebuilding on every interface event
  would be a product change, and the current behaviour is at least deterministic.

## 8. B7 root cause

The three stages narrowed a "userspace WireGuard is broken" report to a specific
dependency defect:

1. sequential single-stream repetitions pass; two simultaneous streams fail;
2. four concurrent flows through the same topology in one process pass, so the
   stack and the route path are not the trigger;
3. start spacing decides it: 0 ms and 200 ms fail 2/2, 1 s passes 2/2;
4. with the pinned sing-tun instrumented, failing runs show `PEER-WRITE` without
   `PEER-WRITE-DONE` on the last queued write, and the SIGQUIT dump shows the
   writer blocked in `Peer.waitOutboundSpace` → `WritePackets`, with the
   sequential sender idle on `queue.outbound.c` and all 16 encryption goroutines
   idle on their channel. No container is in flight, so nothing can decrement
   `queuedOutboundPackets` or wake the waiter; the wait has no timeout and the
   cap is 1024.

The outbound drain goroutine of the userspace stack is the *only* caller of
`peer.WritePackets` (`transport/wireguard/device_peer_router.go:122`). Blocking it
stops every flow of the peer while keepalives (which bypass the staged path) keep
the tunnel looking alive — exactly the observed "first flows work, then nothing,
no error in any log".

The accounting and the staging queue are inside
`github.com/Piggy-Cat-bit-shadow/wireguard-go`; the fix is a fork change: flush
staged containers when a keypair is installed, and audit every
`queuedOutboundPackets.Add` for a matching decrement on the container path. The
in-repo workaround (make `peerQueue.write` non-blocking with a bounded drop
policy) would hide loss and contradicts wireguard-go's "injection waits instead
of dropping" contract, so it was not applied. Artifact:
[`evidence/b7-wireguard-wedge.txt`](evidence/b7-wireguard-wedge.txt).

## 9. Cross-platform

Four production targets build from the merged tree with the laboratory tag set
and `CGO_ENABLED=0`:

| target | result |
|---|---|
| linux/amd64 | PASS (122 418 559 bytes) |
| linux/arm64 | PASS (115 722 315 bytes) |
| windows/amd64 | PASS (121 356 800 bytes) |
| darwin/arm64 | PASS (110 493 410 bytes) |
| android/arm64 | FAIL — `purego`'s Android file needs cgo `dlopen` and this host has no NDK |
| default tag set, linux/amd64 | PASS |

The Android failure is `BLOCKED_BY_ENVIRONMENT` (no NDK), not a source break: the
same `purego` package is compiled with cgo on a real Android build. These builds
prove compilation for four targets; they say nothing about runtime behaviour on
them, and the platform cells in B6 stay blocked. Artifact:
[`evidence/cross-builds.txt`](evidence/cross-builds.txt).

## 10. What this audit did not cover

- Real iOS Network Extension: memory-pressure callbacks, device pause, Cronet
  Darwin, NE lifecycle. `BLOCKED_BY_ENVIRONMENT`.
- Android TUN and network changes. `BLOCKED_BY_ENVIRONMENT`.
- Windows runtime behaviour (Cronet callbacks, native resource observation).
  Only a cross-build check is possible here.
- Privileged TUN data path, host-wide capture and hardware counters. No root.
- qlog/PTO/loss instrumentation for QUIC: the fork can emit qlog, but enabling it
  perturbs the measurement and the lab has no field for it; deferred to a
  protocol-specific debug session.
